package op

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/utils/cache"
	"github.com/charmbracelet/log"
	"gorm.io/gorm"
)

var (
	channelCache      = cache.New[int, model.Channel](16)      // 渠道配置的进程内副本。
	channelKeyCache   = cache.New[int, model.ChannelKey](16)   // 渠道凭据的进程内副本。
	channelModelCache = cache.New[int, model.ChannelModel](16) // 渠道模型的进程内副本。
	channelGrantCache = cache.New[int, model.ChannelGrant](16) // 渠道授权的进程内副本。
)

// 已定义的全部协议位, 用于校验提交的协议掩码。
// ProtocolOpenAIVideo 一并列入, 管理后台勾选不会被判为非法位; 视频路由本身 P2 阶段再补。
const definedProtocols = model.ProtocolOpenAIChatCompletion | model.ProtocolOpenAIResponse | model.ProtocolAnthropicMessage | model.ProtocolOpenAIImage | model.ProtocolOpenAIVideo

// ChannelDetailGet 返回指定渠道的完整配置, 供编辑表单读取; 非归属者按不存在处理。
func ChannelDetailGet(id int, scope model.Scope) (model.ChannelDetail, error) {
	channel, ok := channelCache.Get(id)
	if !ok || !scope.Owns(channel.UserID) {
		return model.ChannelDetail{}, fmt.Errorf("channel not found")
	}
	return channelDetail(channel), nil
}

// ChannelStatsList 返回全部渠道及其模型的累计统计, 自带名称与启停状态, 同时充当列表页的渠道列表。
// 不带整份配置: 路径, 代理与凭据明文只在编辑时用得上, 由 ChannelDetailGet 按主键单独给出。
func ChannelStatsList(scope model.Scope) []model.ChannelStats {
	modelsByChannel := make(map[int][]model.ChannelModelStats, channelCache.Len())
	for _, channelModel := range channelModelCache.GetAll() {
		modelsByChannel[channelModel.ChannelID] = append(modelsByChannel[channelModel.ChannelID], model.ChannelModelStats{
			ModelID:      channelModel.ID,
			ModelName:    channelModel.Name,
			StatsMetrics: channelModel.StatsMetrics,
		})
	}
	stats := make([]model.ChannelStats, 0, channelCache.Len())
	for _, channel := range channelCache.GetAll() {
		// 归属过滤: 管理员看全部, 其余只看自有渠道。
		if !scope.Owns(channel.UserID) {
			continue
		}
		models := modelsByChannel[channel.ID]
		if models == nil {
			models = []model.ChannelModelStats{}
		}
		stats = append(stats, model.ChannelStats{
			ChannelID:    channel.ID,
			UserID:       channel.UserID,
			ChannelName:  channel.Name,
			Shared:       channel.Shared,
			ShareCode:    channel.ShareCode,
			Enabled:      channel.Enabled,
			Models:       models,
			StatsMetrics: channel.StatsMetrics,
		})
	}
	return stats
}

// ChannelCreate 创建渠道及其凭据, 模型与授权, 返回创建后的完整配置。
// 三者在同一事务内落库: 授权按名称引用两侧, 待凭据与模型拿到主键后由 syncChannelGrants 解析,
// 由此建一个带授权的渠道只需一趟请求。
func ChannelCreate(detail *model.ChannelDetail, scope model.Scope, ctx context.Context) (*model.ChannelDetail, error) {
	if err := normalizeChannelDetail(detail); err != nil {
		return nil, err
	}

	channel := model.Channel{UserID: scope.ID, ChannelConfig: detail.ChannelConfig}
	if err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&channel).Error; err != nil {
			return fmt.Errorf("failed to create channel: %w", err)
		}
		return syncChannelChildren(tx, channel.ID, detail)
	}); err != nil {
		return nil, err
	}

	channelCache.Set(channel.ID, channel)
	// 凭据, 模型与授权的主键都在事务内分配, 此刻只在库里; 重载子表缓存以让授权候选与转发都能查到。
	if err := reloadChannelChildren(ctx, channel.ID); err != nil {
		return nil, err
	}
	created := channelDetail(channel)
	return &created, nil
}

// ChannelUpdate 按提交的完整配置整体替换渠道及其凭据, 模型与授权, 返回刷新后的配置。
// 提交即全量而非按字段比对增量: 渠道是人工编辑的十几个字段, 表单本就一次给出完整配置,
// 未列出的凭据与模型会被删除并级联删除其授权。
func ChannelUpdate(detail *model.ChannelDetail, scope model.Scope, ctx context.Context) (*model.ChannelDetail, error) {
	existing, ok := channelCache.Get(detail.ID)
	if !ok || !scope.Owns(existing.UserID) {
		return nil, fmt.Errorf("channel not found")
	}
	if err := normalizeChannelDetail(detail); err != nil {
		return nil, err
	}

	if err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 逐列点名而不整行覆盖: 全量提交下 enabled 置假与被清空的可选字段都必须落库, 按零值跳过会写不进去;
		// 而统计列由转发累加, 不在提交范围内, 整行覆盖会把它抹回提交时的旧值。
		if err := tx.Model(&model.Channel{}).Where("id = ?", detail.ID).
			Select("name", "dialect", "enabled", "base_url",
				"openai_chat_completion_path", "openai_response_path", "openai_image_generation_path", "openai_image_edit_path", "anthropic_message_path",
				"proxy", "channel_proxy", "custom_header", "param_override", "match_regex").
			Updates(&model.Channel{ChannelConfig: detail.ChannelConfig}).Error; err != nil {
			return fmt.Errorf("failed to update channel: %w", err)
		}
		return syncChannelChildren(tx, detail.ID, detail)
	}); err != nil {
		return nil, err
	}

	// 缓存条目由提交的配置重建, 统计从原条目搬过来: 它含本轮尚未落库的累加, 比库内的行更新。
	// 归属与发布状态同样从原条目搬: 提交不含这三者, 不搬会在缓存里丢掉归属与共享编码。
	channelStatsNeedUpdateLock.Lock()
	channel := model.Channel{ID: detail.ID, ChannelConfig: detail.ChannelConfig}
	if cached, ok := channelCache.Get(detail.ID); ok {
		channel.StatsMetrics = cached.StatsMetrics
		channel.UserID = cached.UserID
		channel.Shared = cached.Shared
		channel.ShareCode = cached.ShareCode
	}
	channelCache.Set(detail.ID, channel)
	channelStatsNeedUpdateLock.Unlock()

	// 凭据, 模型与授权的增删都会改变可选路由集合, 重载该渠道的三类缓存并刷新分组。
	if err := reloadChannelChildren(ctx, detail.ID); err != nil {
		return nil, err
	}
	if err := groupRefreshCache(ctx); err != nil {
		return nil, fmt.Errorf("failed to refresh groups: %w", err)
	}
	updated := channelDetail(channel)
	return &updated, nil
}

// normalizeChannelConfig 补齐提交配置中的默认值并校验协议路径。
// 路径留空会与地址拼成错误的上游地址, 故一律回退到协议默认路径; Header 恒为数组, 免得落库后读出 null。
// 全部字段在此去空白: 落库后的配置被读侧无条件信任, 空白与空串都不会再传到转发链路。
func normalizeChannelConfig(config model.ChannelConfig) (model.ChannelConfig, error) {
	config.Name = strings.TrimSpace(config.Name)
	if config.Name == "" {
		return config, fmt.Errorf("channel name is required")
	}
	config.BaseURL = strings.TrimSpace(config.BaseURL)
	if config.BaseURL == "" {
		return config, fmt.Errorf("channel base url is required")
	}
	if config.Dialect == "" {
		config.Dialect = model.DialectGeneric
	}
	var err error
	if config.OpenAIChatCompletionPath, err = normalizedPath(config.OpenAIChatCompletionPath, "/v1/chat/completions"); err != nil {
		return config, err
	}
	if config.OpenAIResponsePath, err = normalizedPath(config.OpenAIResponsePath, "/v1/responses"); err != nil {
		return config, err
	}
	if config.OpenAIImageGenerationPath, err = normalizedPath(config.OpenAIImageGenerationPath, "/v1/images/generations"); err != nil {
		return config, err
	}
	if config.OpenAIImageEditPath, err = normalizedPath(config.OpenAIImageEditPath, "/v1/images/edits"); err != nil {
		return config, err
	}
	if config.AnthropicMessagePath, err = normalizedPath(config.AnthropicMessagePath, "/v1/messages"); err != nil {
		return config, err
	}
	if config.CustomHeader == nil {
		config.CustomHeader = []model.CustomHeader{}
	}

	config.ChannelProxy = strings.TrimSpace(config.ChannelProxy)
	config.ParamOverride = strings.TrimSpace(config.ParamOverride)
	config.MatchRegex = strings.TrimSpace(config.MatchRegex)
	return config, nil
}

// normalizeChannelDetail 规范化整份提交配置; 渠道自身的字段交由 normalizeChannelConfig 处理。
// 凭据, 模型与授权的名称在此去空白并校验非空: 名称是三者的匹配与引用依据, 集中在入口清理后,
// 下游三个同步函数拿到的即是干净数据, 无需各自再 trim 一遍。
func normalizeChannelDetail(detail *model.ChannelDetail) error {
	config, err := normalizeChannelConfig(detail.ChannelConfig)
	if err != nil {
		return err
	}
	detail.ChannelConfig = config

	for i := range detail.Keys {
		detail.Keys[i].Name = strings.TrimSpace(detail.Keys[i].Name)
		if detail.Keys[i].Name == "" {
			return fmt.Errorf("channel key name is required")
		}
		// 凭据两端的空白会被原样拼进认证 Header, 一并去掉。
		detail.Keys[i].Key = strings.TrimSpace(detail.Keys[i].Key)
	}
	for i := range detail.Models {
		detail.Models[i] = strings.TrimSpace(detail.Models[i])
		if detail.Models[i] == "" {
			return fmt.Errorf("channel model name is required")
		}
	}
	for i := range detail.Grants {
		detail.Grants[i].ModelName = strings.TrimSpace(detail.Grants[i].ModelName)
		detail.Grants[i].KeyName = strings.TrimSpace(detail.Grants[i].KeyName)
	}
	return nil
}

// syncChannelChildren 按提交的完整配置整体替换渠道下的凭据, 模型与授权。
// 凭据与模型必须先落库: 授权引用两者的主键, 新增的两者在同一事务内才拿得到。
func syncChannelChildren(tx *gorm.DB, channelID int, detail *model.ChannelDetail) error {
	if err := syncChannelKeys(tx, channelID, detail.Keys); err != nil {
		return err
	}
	if err := syncChannelModels(tx, channelID, detail.Models); err != nil {
		return err
	}
	return syncChannelGrants(tx, channelID, detail.Grants)
}

// ChannelEnabled 更新渠道启用状态。
func ChannelEnabled(id int, enabled bool, scope model.Scope, ctx context.Context) error {
	channel, ok := channelCache.Get(id)
	if !ok || !scope.Owns(channel.UserID) {
		return fmt.Errorf("channel not found")
	}
	if err := db.GetDB().WithContext(ctx).Model(&model.Channel{}).Where("id = ?", id).Update("enabled", enabled).Error; err != nil {
		return err
	}
	channel.Enabled = enabled
	channelCache.Set(id, channel)
	return nil
}

// ChannelDel 删除渠道及其凭据, 模型与渠道授权, 关联分组项由数据库外键级联删除。
func ChannelDel(id int, scope model.Scope, ctx context.Context) error {
	if channel, ok := channelCache.Get(id); !ok || !scope.Owns(channel.UserID) {
		return fmt.Errorf("channel not found")
	}
	grantIDs := channelGrantIDs(id)
	if err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(grantIDs) > 0 {
			if err := clearActiveItems(tx, grantIDs); err != nil {
				return err
			}
		}
		if err := tx.Delete(&model.Channel{}, id).Error; err != nil {
			return fmt.Errorf("failed to delete channel: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}

	channelStatsNeedUpdateLock.Lock()
	channelCache.Del(id)
	delete(channelStatsNeedUpdate, id)
	channelStatsNeedUpdateLock.Unlock()

	channelKeyStatsNeedUpdateLock.Lock()
	for _, channelKey := range channelKeyCache.GetAll() {
		if channelKey.ChannelID == id {
			channelKeyCache.Del(channelKey.ID)
			delete(channelKeyStatsNeedUpdate, channelKey.ID)
		}
	}
	channelKeyStatsNeedUpdateLock.Unlock()

	channelModelStatsNeedUpdateLock.Lock()
	for _, channelModel := range channelModelCache.GetAll() {
		if channelModel.ChannelID == id {
			channelModelCache.Del(channelModel.ID)
			delete(channelModelStatsNeedUpdate, channelModel.ID)
		}
	}
	channelModelStatsNeedUpdateLock.Unlock()

	channelGrantCache.Del(grantIDs...)
	if err := groupRefreshCache(ctx); err != nil {
		return fmt.Errorf("failed to refresh groups: %w", err)
	}
	return nil
}

// ChannelGet 返回指定渠道的缓存副本, 供转发按地址, 路径与代理构造上游请求。
// 不补齐凭据, 模型与授权: 转发所需的授权由 ChannelGrantGet 按主键单独取, 那里已连带给出两侧。
func ChannelGet(id int) (model.Channel, error) {
	channel, ok := channelCache.Get(id)
	if !ok {
		return model.Channel{}, fmt.Errorf("channel not found")
	}
	return channel, nil
}

// ChannelGrantGet 返回可用于转发的渠道授权, 并补齐其模型与凭据。
// 凭据被停用, 以及模型, 凭据缺失时一律返回错误, 使调用方拿到的授权必然可直接转发, 无需再逐项检查。
// 授权本身没有停用状态: 不再授权就删掉该组合, 无需保留一行停用记录。
func ChannelGrantGet(id int) (model.ChannelGrant, error) {
	grant, ok := channelGrantCache.Get(id)
	if !ok {
		return model.ChannelGrant{}, fmt.Errorf("channel grant not found")
	}
	channelModel, ok := channelModelCache.Get(grant.ChannelModelID)
	if !ok {
		return model.ChannelGrant{}, fmt.Errorf("channel model %d not found", grant.ChannelModelID)
	}
	channelKey, ok := channelKeyCache.Get(grant.ChannelKeyID)
	if !ok {
		return model.ChannelGrant{}, fmt.Errorf("channel key %d not found", grant.ChannelKeyID)
	}
	if !channelKey.Enabled {
		return model.ChannelGrant{}, fmt.Errorf("channel key %d is disabled", channelKey.ID)
	}
	grant.ChannelModel = &channelModel
	grant.ChannelKey = &channelKey
	return grant, nil
}

// ChannelGrantCandidates 返回授权候选及其展示字段, 供分组页选取成员。
// 供给侧隔离: 候选一律只含「已发布渠道 + 已上架模型」, 对所有角色一视同仁(含渠道归属者与管理员),
// 未上架模型只在归属者的渠道管理页存在; 渠道名以发布唯一编码展示, 凭据名除归属者外脱敏为空。
// 可用性与 GroupList 补齐成员时同一口径: 渠道与凭据均启用即可用, 由此候选与已选成员不会各判一套。
func ChannelGrantCandidates(scope model.Scope) []model.ChannelGrantCandidate {
	markup := SettingGetFloat(model.SettingKeyMarkupRatio)
	candidates := make([]model.ChannelGrantCandidate, 0, channelGrantCache.Len())
	for _, grant := range channelGrantCache.GetAll() {
		channelModel, modelOK := channelModelCache.Get(grant.ChannelModelID)
		channelKey, keyOK := channelKeyCache.Get(grant.ChannelKeyID)
		if !modelOK || !keyOK {
			continue
		}
		channel, channelOK := channelCache.Get(channelModel.ChannelID)
		if !channelOK {
			continue
		}
		owner := scope.Owns(channel.UserID)
		// 供给侧隔离: 未发布渠道或未上架模型不出候选, 归属者与管理员也不例外。
		if !channel.Shared || !channelModel.Listed {
			continue
		}
		candidate := model.ChannelGrantCandidate{
			ID:          grant.ID,
			ChannelID:   channel.ID,
			ChannelName: channel.Name,
			ModelName:   channelModel.Name,
			KeyName:     channelKey.Name,
			Protocols:   grant.Protocols,
			Available:   channel.Enabled && channelKey.Enabled,
			Listed:      channelModel.Listed,
			SupplyPrice: channelModel.SupplyPrice,
			UserPrice:   channelModel.SupplyPrice.Scale(markup),
		}
		// 展示名定稿: 已发布渠道一律以发布唯一编码示人(含归属者与管理员), 上游名称不出分组管理;
		// 未发布渠道只有归属者与管理员可见, 显示真名。凭据名仍按归属脱敏。
		if channel.Shared && channel.ShareCode != "" {
			candidate.ChannelName = channel.ShareCode
		}
		if !owner {
			candidate.KeyName = ""
		}
		candidates = append(candidates, candidate)
	}
	// 按渠道, 模型, 凭据三级定序: 分组页按这三级组织候选且不提供排序开关, 顺序须由此处定稿。
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].ChannelID != candidates[j].ChannelID {
			return candidates[i].ChannelID < candidates[j].ChannelID
		}
		if candidates[i].ModelName != candidates[j].ModelName {
			return candidates[i].ModelName < candidates[j].ModelName
		}
		return candidates[i].KeyName < candidates[j].KeyName
	})
	return candidates
}

// ChannelPublish 发布或取消发布渠道: 发布时生成大小写字母+数字的唯一编码, 用户侧以该编码展示渠道。
// 已发布渠道再次发布保留原编码; 取消发布清空共享标记但保留编码历史无意义, 故一并清空。
func ChannelPublish(id int, shared bool, scope model.Scope, ctx context.Context) (model.Channel, error) {
	channel, ok := channelCache.Get(id)
	if !ok || !scope.Owns(channel.UserID) {
		return model.Channel{}, fmt.Errorf("channel not found")
	}
	updates := map[string]any{"shared": shared}
	if shared {
		if channel.ShareCode == "" {
			code, err := uniqueShareCode(ctx)
			if err != nil {
				return model.Channel{}, err
			}
			updates["share_code"] = code
			channel.ShareCode = code
		}
	} else {
		updates["shared"] = false
		updates["share_code"] = ""
		channel.ShareCode = ""
	}
	if err := db.GetDB().WithContext(ctx).Model(&model.Channel{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		return model.Channel{}, fmt.Errorf("failed to update channel share state: %w", err)
	}
	channel.Shared = shared
	channelCache.Set(id, channel)
	return channel, nil
}

// generateShareCode 生成大小写字母+数字的 10 位随机编码。
func generateShareCode() (string, error) {
	const chars = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	b := make([]byte, 10)
	max := big.NewInt(int64(len(chars)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = chars[n.Int64()]
	}
	return string(b), nil
}

// uniqueShareCode 生成不与既有编码冲突的发布编码; 编码空间大, 冲突重试几次即可。
func uniqueShareCode(ctx context.Context) (string, error) {
	for range 8 {
		code, err := generateShareCode()
		if err != nil {
			return "", err
		}
		if code == "" {
			return "", fmt.Errorf("failed to generate share code")
		}
		var count int64
		if err := db.GetDB().WithContext(ctx).Model(&model.Channel{}).Where("share_code = ?", code).Count(&count).Error; err != nil {
			return "", err
		}
		if count == 0 {
			return code, nil
		}
	}
	return "", fmt.Errorf("failed to allocate unique share code")
}

// maxSupplyPricePerMillion 单类供货价上限(每百万 token): 正常模型远低于此, 越界基本是少打小数点。
const maxSupplyPricePerMillion = 1_000_000.0

// maxMediaPricePerUnit 媒体单价上限(每张/每秒): 1000 已远超主流生图/生视频定价, 越界按误输入拒绝。
const maxMediaPricePerUnit = 1_000.0

// ChannelModelListing 是一个渠道模型的上架配置提交形状。
type ChannelModelListing struct {
	Name        string           `json:"name"`                   // 渠道模型名称。
	Listed      bool             `json:"listed"`                 // 是否上架。
	SupplyPrice model.LLMPrice   `json:"supply_price"`           // 供货价(读/写/缓存读/缓存写); 未设置即 0。
	MediaSupply model.MediaPrice `json:"media_supply,omitempty"` // 媒体供货价(每张/每秒/分档); 仅媒体形态模型使用。
	Kind        model.MediaKind  `json:"kind,omitempty"`         // 模型媒体形态; 只读回显, 由授权协议位推导。
}

// ChannelModelListingGet 返回渠道的模型上架配置, 供发布管理界面编辑。
func ChannelModelListingGet(id int, scope model.Scope) ([]ChannelModelListing, error) {
	channel, ok := channelCache.Get(id)
	if !ok || !scope.Owns(channel.UserID) {
		return nil, fmt.Errorf("channel not found")
	}
	listings := make([]ChannelModelListing, 0)
	for _, channelModel := range channelModelCache.GetAll() {
		if channelModel.ChannelID != id {
			continue
		}
		listings = append(listings, ChannelModelListing{
			Name:        channelModel.Name,
			Listed:      channelModel.Listed,
			SupplyPrice: channelModel.SupplyPrice,
			MediaSupply: channelModel.MediaSupply,
			Kind:        channelModel.Kind,
		})
	}
	sort.Slice(listings, func(i, j int) bool { return listings[i].Name < listings[j].Name })
	return listings, nil
}

// ChannelModelListingUpdate 整体更新渠道模型的上架与供货价; 未提及的模型保持不变。
// 上架与供货价属于发布配置, 不随渠道编辑的整体替换被覆盖(按名称匹配的行本就不动)。
func ChannelModelListingUpdate(id int, listings []ChannelModelListing, scope model.Scope, ctx context.Context) error {
	channel, ok := channelCache.Get(id)
	if !ok || !scope.Owns(channel.UserID) {
		return fmt.Errorf("channel not found")
	}
	byName := make(map[string]model.ChannelModel)
	for _, channelModel := range channelModelCache.GetAll() {
		if channelModel.ChannelID == id {
			byName[channelModel.Name] = channelModel
		}
	}
	for _, listing := range listings {
		channelModel, ok := byName[strings.TrimSpace(listing.Name)]
		if !ok {
			return fmt.Errorf("channel model %s not found", listing.Name)
		}
		price := listing.SupplyPrice
		// 价格是收入口径, 校验从严: 负数会让用户"反向赚钱", 过大则几乎必然是误输入(少打小数点)。
		for _, pair := range []struct {
			field string
			value float64
		}{
			{"input", price.Input},
			{"output", price.Output},
			{"cache_read", price.CacheRead},
			{"cache_write", price.CacheWrite},
		} {
			if pair.value < 0 {
				return fmt.Errorf("%s supply price cannot be negative", pair.field)
			}
			if pair.value > maxSupplyPricePerMillion {
				return fmt.Errorf("%s supply price %g exceeds the %g per-million cap", pair.field, pair.value, maxSupplyPricePerMillion)
			}
		}
		media := listing.MediaSupply
		// 媒体价与 token 价同一收入口径: 负数拒绝, 越界按单价上限拒绝(每张/每秒远低于此值)。
		for _, pair := range []struct {
			field string
			value float64
		}{
			{"per_image", media.PerImage},
			{"per_second", media.PerSecond},
		} {
			if pair.value < 0 {
				return fmt.Errorf("media %s price cannot be negative", pair.field)
			}
			if pair.value > maxMediaPricePerUnit {
				return fmt.Errorf("media %s price %g exceeds the %g per-unit cap", pair.field, pair.value, maxMediaPricePerUnit)
			}
		}
		for key, value := range media.Resolution {
			if value < 0 {
				return fmt.Errorf("media resolution %s price cannot be negative", key)
			}
			if value > maxMediaPricePerUnit {
				return fmt.Errorf("media resolution %s price %g exceeds the %g per-unit cap", key, value, maxMediaPricePerUnit)
			}
		}
		// 上架但不定价 = 该模型对用户可见却按 0 计费, 是实打实的收入泄漏, 故直接拒绝。
		// 媒体形态模型以媒体价为准: token 四类全零但按张定价同样视为已定价。
		unpriced := price.Input == 0 && price.Output == 0 && price.CacheRead == 0 && price.CacheWrite == 0
		mediaUnpriced := media.PerImage == 0 && media.PerSecond == 0 && len(media.Resolution) == 0
		if listing.Listed && unpriced && mediaUnpriced {
			return fmt.Errorf("channel model %s is listed but has no supply price", listing.Name)
		}
		// supply_price 走 JSON 序列化器, 必须用结构体更新: map 更新绕过序列化器会写入非法值。
		if err := db.GetDB().WithContext(ctx).Model(&model.ChannelModel{}).Where("id = ?", channelModel.ID).
			Select("listed", "supply_price", "media_supply").
			Updates(model.ChannelModel{Listed: listing.Listed, SupplyPrice: price, MediaSupply: media}).Error; err != nil {
			return fmt.Errorf("failed to update channel model listing: %w", err)
		}
		channelModel.Listed = listing.Listed
		channelModel.SupplyPrice = price
		channelModel.MediaSupply = media
		channelModelCache.Set(channelModel.ID, channelModel)
	}
	return nil
}

// channelRefreshCache 从数据库刷新渠道, 凭据, 模型与渠道授权缓存。
func channelRefreshCache(ctx context.Context) error {
	conn := db.GetDB().WithContext(ctx)
	channels := []model.Channel{}
	if err := conn.Find(&channels).Error; err != nil {
		log.Warnf("failed to get channels: %v", err)
		return err
	}
	channelKeys := []model.ChannelKey{}
	if err := conn.Find(&channelKeys).Error; err != nil {
		return err
	}
	channelModels := []model.ChannelModel{}
	if err := conn.Find(&channelModels).Error; err != nil {
		return err
	}
	channelGrants := []model.ChannelGrant{}
	if err := conn.Find(&channelGrants).Error; err != nil {
		return err
	}

	channelCache.Clear()
	channelKeyCache.Clear()
	channelModelCache.Clear()
	channelGrantCache.Clear()
	for _, channel := range channels {
		channelCache.Set(channel.ID, channel)
	}
	for _, channelKey := range channelKeys {
		channelKeyCache.Set(channelKey.ID, channelKey)
	}
	for _, channelModel := range channelModels {
		channelModelCache.Set(channelModel.ID, channelModel)
	}
	for _, grant := range channelGrants {
		channelGrantCache.Set(grant.ID, grant)
	}
	return nil
}

// reloadChannelChildren 重新加载单个渠道的凭据, 模型与渠道授权缓存。
// 存活目标保留缓存中尚未落库的统计, 避免刷新丢失本轮累加。
func reloadChannelChildren(ctx context.Context, channelID int) error {
	conn := db.GetDB().WithContext(ctx)
	channelKeys := []model.ChannelKey{}
	if err := conn.Where("channel_id = ?", channelID).Find(&channelKeys).Error; err != nil {
		return fmt.Errorf("failed to load channel keys: %w", err)
	}
	channelModels := []model.ChannelModel{}
	if err := conn.Where("channel_id = ?", channelID).Find(&channelModels).Error; err != nil {
		return fmt.Errorf("failed to load channel models: %w", err)
	}
	modelIDs := make([]int, 0, len(channelModels))
	for _, channelModel := range channelModels {
		modelIDs = append(modelIDs, channelModel.ID)
	}
	grants := []model.ChannelGrant{}
	if len(modelIDs) > 0 {
		if err := conn.Where("channel_model_id IN ?", modelIDs).Find(&grants).Error; err != nil {
			return fmt.Errorf("failed to load channel grants: %w", err)
		}
	}

	// 先按库内行覆盖再清理消失的行: 配置以库内为准, 统计以缓存为准。
	// 存活行的缓存值含尚未落库的累加, 比库内的行更新, 不能被库内的统计覆盖。
	channelKeyStatsNeedUpdateLock.Lock()
	liveKeys := make(map[int]struct{}, len(channelKeys))
	for _, channelKey := range channelKeys {
		liveKeys[channelKey.ID] = struct{}{}
		if cached, ok := channelKeyCache.Get(channelKey.ID); ok {
			channelKey.StatsMetrics = cached.StatsMetrics
		}
		channelKeyCache.Set(channelKey.ID, channelKey)
	}
	for _, cached := range channelKeyCache.GetAll() {
		if _, live := liveKeys[cached.ID]; cached.ChannelID == channelID && !live {
			channelKeyCache.Del(cached.ID)
		}
	}
	channelKeyStatsNeedUpdateLock.Unlock()

	channelModelStatsNeedUpdateLock.Lock()
	liveModels := make(map[int]struct{}, len(channelModels))
	for _, channelModel := range channelModels {
		liveModels[channelModel.ID] = struct{}{}
		if cached, ok := channelModelCache.Get(channelModel.ID); ok {
			channelModel.StatsMetrics = cached.StatsMetrics
		}
		channelModelCache.Set(channelModel.ID, channelModel)
	}
	for _, cached := range channelModelCache.GetAll() {
		if _, live := liveModels[cached.ID]; cached.ChannelID == channelID && !live {
			channelModelCache.Del(cached.ID)
		}
	}
	channelModelStatsNeedUpdateLock.Unlock()

	// 授权不带统计, 直接按库内行整体替换。
	for _, grantID := range channelGrantIDs(channelID) {
		channelGrantCache.Del(grantID)
	}
	for _, grant := range grants {
		channelGrantCache.Set(grant.ID, grant)
	}
	return nil
}

// channelDetail 把渠道缓存与其凭据, 模型和授权合并为编辑表单所需的完整配置。
// 授权按名称给出而不给主键: 名称在渠道内唯一, 提交时也按名称引用, 读写同一种寻址界面才无需翻译。
// 三个集合字段恒为数组, 空集合也给出, 免得各消费方各自兜底 null; Header 由写入侧保证已是数组。
// 三者均按名称定序: 缓存遍历顺序随机, 而编辑表单不提供排序开关, 顺序须由此处定稿。
func channelDetail(channel model.Channel) model.ChannelDetail {
	detail := model.ChannelDetail{ID: channel.ID, ChannelConfig: channel.ChannelConfig}

	detail.Keys = make([]model.ChannelKeyConfig, 0)
	keyNameByID := make(map[int]string)
	for _, channelKey := range channelKeyCache.GetAll() {
		if channelKey.ChannelID == channel.ID {
			detail.Keys = append(detail.Keys, channelKey.ChannelKeyConfig)
			keyNameByID[channelKey.ID] = channelKey.Name
		}
	}
	sort.Slice(detail.Keys, func(i, j int) bool { return detail.Keys[i].Name < detail.Keys[j].Name })

	detail.Models = make([]string, 0)
	modelNameByID := make(map[int]string)
	for _, channelModel := range channelModelCache.GetAll() {
		if channelModel.ChannelID == channel.ID {
			detail.Models = append(detail.Models, channelModel.Name)
			modelNameByID[channelModel.ID] = channelModel.Name
		}
	}
	sort.Strings(detail.Models)

	// 授权按模型主键归属本渠道, 两侧主键在此翻译成名称。
	grants := make([]model.ChannelGrantConfig, 0)
	for _, grant := range channelGrantCache.GetAll() {
		modelName, ok := modelNameByID[grant.ChannelModelID]
		if !ok {
			continue
		}
		keyName, ok := keyNameByID[grant.ChannelKeyID]
		if !ok {
			continue
		}
		grants = append(grants, model.ChannelGrantConfig{ModelName: modelName, KeyName: keyName, Protocols: grant.Protocols})
	}
	sort.Slice(grants, func(i, j int) bool {
		if grants[i].ModelName != grants[j].ModelName {
			return grants[i].ModelName < grants[j].ModelName
		}
		return grants[i].KeyName < grants[j].KeyName
	})
	detail.Grants = grants
	return detail
}

// channelGrantIDs 返回指定渠道下全部渠道授权的主键。
func channelGrantIDs(channelID int) []int {
	grantIDs := make([]int, 0)
	for _, grant := range channelGrantCache.GetAll() {
		if channelModel, ok := channelModelCache.Get(grant.ChannelModelID); ok && channelModel.ChannelID == channelID {
			grantIDs = append(grantIDs, grant.ID)
		}
	}
	return grantIDs
}

// syncChannelKeys 按提交的凭据集合新增, 更新与删除渠道凭据。
// 凭据在渠道内按名称唯一, 名称作为匹配依据; 删除凭据会级联删除其渠道授权。
func syncChannelKeys(tx *gorm.DB, channelID int, requested []model.ChannelKeyConfig) error {
	var existing []model.ChannelKey
	if err := tx.Where("channel_id = ?", channelID).Find(&existing).Error; err != nil {
		return fmt.Errorf("failed to load channel keys: %w", err)
	}
	existingByName := make(map[string]model.ChannelKey, len(existing))
	for _, channelKey := range existing {
		existingByName[channelKey.Name] = channelKey
	}
	for _, requestedKey := range requested {
		if current, ok := existingByName[requestedKey.Name]; ok {
			if current.Key != requestedKey.Key || current.Enabled != requestedKey.Enabled {
				if err := tx.Model(&model.ChannelKey{}).Where("id = ?", current.ID).
					Updates(map[string]any{"key": requestedKey.Key, "enabled": requestedKey.Enabled}).Error; err != nil {
					return fmt.Errorf("failed to update channel key: %w", err)
				}
			}
			delete(existingByName, requestedKey.Name)
			continue
		}
		newKey := model.ChannelKey{ChannelID: channelID, ChannelKeyConfig: requestedKey}
		if err := tx.Create(&newKey).Error; err != nil {
			return fmt.Errorf("failed to create channel key: %w", err)
		}
	}
	deletedKeyIDs := make([]int, 0, len(existingByName))
	for _, channelKey := range existingByName {
		deletedKeyIDs = append(deletedKeyIDs, channelKey.ID)
	}
	if len(deletedKeyIDs) == 0 {
		return nil
	}
	if err := clearActiveItems(tx, grantsOf(tx, "channel_key_id", deletedKeyIDs)); err != nil {
		return err
	}
	if err := tx.Delete(&model.ChannelKey{}, deletedKeyIDs).Error; err != nil {
		return fmt.Errorf("failed to delete channel keys: %w", err)
	}
	return nil
}

// syncChannelModels 按提交的模型名称集合新增与删除渠道模型。
// 模型在渠道内按名称唯一, 除名称外无可更新字段, 故提交侧直接给名称; 删除模型会级联删除其渠道授权。
func syncChannelModels(tx *gorm.DB, channelID int, requested []string) error {
	var existing []model.ChannelModel
	if err := tx.Where("channel_id = ?", channelID).Find(&existing).Error; err != nil {
		return fmt.Errorf("failed to load channel models: %w", err)
	}
	existingByName := make(map[string]model.ChannelModel, len(existing))
	for _, channelModel := range existing {
		existingByName[channelModel.Name] = channelModel
	}
	for _, requestedModel := range requested {
		if _, ok := existingByName[requestedModel]; ok {
			delete(existingByName, requestedModel)
			continue
		}
		if err := tx.Create(&model.ChannelModel{ChannelID: channelID, Name: requestedModel}).Error; err != nil {
			return fmt.Errorf("failed to create channel model: %w", err)
		}
	}
	deletedModelIDs := make([]int, 0, len(existingByName))
	for _, channelModel := range existingByName {
		deletedModelIDs = append(deletedModelIDs, channelModel.ID)
	}
	if len(deletedModelIDs) == 0 {
		return nil
	}
	if err := clearActiveItems(tx, grantsOf(tx, "channel_model_id", deletedModelIDs)); err != nil {
		return err
	}
	if err := tx.Delete(&model.ChannelModel{}, deletedModelIDs).Error; err != nil {
		return fmt.Errorf("failed to delete channel models: %w", err)
	}
	return nil
}

// syncChannelGrants 按提交的授权集合新增, 更新与删除渠道授权。
// 授权以 (模型, 凭据) 组合唯一, 该组合作为匹配依据; 提交方按名称引用, 名称在此解析为本渠道的主键。
// 凭据与模型已在本事务内先行同步, 故新增的两者在此都能查到, 一次提交即可完成建模型与授权。
func syncChannelGrants(tx *gorm.DB, channelID int, requested []model.ChannelGrantConfig) error {
	var channelModels []model.ChannelModel
	if err := tx.Where("channel_id = ?", channelID).Find(&channelModels).Error; err != nil {
		return fmt.Errorf("failed to load channel models: %w", err)
	}
	modelIDByName := make(map[string]int, len(channelModels))
	modelIDList := make([]int, 0, len(channelModels))
	for _, channelModel := range channelModels {
		modelIDByName[channelModel.Name] = channelModel.ID
		modelIDList = append(modelIDList, channelModel.ID)
	}
	var channelKeys []model.ChannelKey
	if err := tx.Where("channel_id = ?", channelID).Find(&channelKeys).Error; err != nil {
		return fmt.Errorf("failed to load channel keys: %w", err)
	}
	keyIDByName := make(map[string]int, len(channelKeys))
	for _, channelKey := range channelKeys {
		keyIDByName[channelKey.Name] = channelKey.ID
	}

	var existing []model.ChannelGrant
	if len(modelIDList) > 0 {
		if err := tx.Where("channel_model_id IN ?", modelIDList).Find(&existing).Error; err != nil {
			return fmt.Errorf("failed to load channel grants: %w", err)
		}
	}
	type grantKey struct {
		modelID int // 渠道模型主键。
		keyID   int // 渠道凭据主键。
	}
	existingByKey := make(map[grantKey]model.ChannelGrant, len(existing))
	for _, grant := range existing {
		existingByKey[grantKey{grant.ChannelModelID, grant.ChannelKeyID}] = grant
	}

	for _, requestedGrant := range requested {
		modelID, ok := modelIDByName[requestedGrant.ModelName]
		if !ok {
			return fmt.Errorf("channel model %q does not belong to channel %d", requestedGrant.ModelName, channelID)
		}
		keyID, ok := keyIDByName[requestedGrant.KeyName]
		if !ok {
			return fmt.Errorf("channel key %q does not belong to channel %d", requestedGrant.KeyName, channelID)
		}
		if requestedGrant.Protocols == 0 || requestedGrant.Protocols&^definedProtocols != 0 {
			return fmt.Errorf("channel grant protocols %d is empty or contains undefined bits", requestedGrant.Protocols)
		}
		key := grantKey{modelID, keyID}
		if current, ok := existingByKey[key]; ok {
			if current.Protocols != requestedGrant.Protocols {
				if err := tx.Model(&model.ChannelGrant{}).Where("id = ?", current.ID).
					Update("protocols", requestedGrant.Protocols).Error; err != nil {
					return fmt.Errorf("failed to update channel grant: %w", err)
				}
			}
			delete(existingByKey, key)
			continue
		}
		newGrant := model.ChannelGrant{
			ChannelModelID: modelID,
			ChannelKeyID:   keyID,
			Protocols:      requestedGrant.Protocols,
		}
		if err := tx.Create(&newGrant).Error; err != nil {
			return fmt.Errorf("failed to create channel grant: %w", err)
		}
	}

	deletedGrantIDs := make([]int, 0, len(existingByKey))
	for _, grant := range existingByKey {
		deletedGrantIDs = append(deletedGrantIDs, grant.ID)
	}
	if len(deletedGrantIDs) > 0 {
		if err := clearActiveItems(tx, deletedGrantIDs); err != nil {
			return err
		}
		if err := tx.Delete(&model.ChannelGrant{}, deletedGrantIDs).Error; err != nil {
			return fmt.Errorf("failed to delete channel grants: %w", err)
		}
	}
	// 授权定稿后同步模型媒体形态: 任一存留授权带生图位即视为生图模型, 生图位全部撤销后回退文本。
	// 形态跟着协议位走而非独立编辑, 保证"授权决定能力, 形态描述能力"不会被改成互相矛盾。
	if err := syncChannelModelKinds(tx, channelID); err != nil {
		return err
	}
	return nil
}

// syncChannelModelKinds 按渠道现存授权重算每个渠道模型的媒体形态并落库。
func syncChannelModelKinds(tx *gorm.DB, channelID int) error {
	var models []model.ChannelModel
	if err := tx.Where("channel_id = ?", channelID).Find(&models).Error; err != nil {
		return fmt.Errorf("failed to load channel models for kind sync: %w", err)
	}
	if len(models) == 0 {
		return nil
	}
	modelIDs := make([]int, 0, len(models))
	for _, channelModel := range models {
		modelIDs = append(modelIDs, channelModel.ID)
	}
	var grants []model.ChannelGrant
	if err := tx.Where("channel_model_id IN ?", modelIDs).Find(&grants).Error; err != nil {
		return fmt.Errorf("failed to load channel grants for kind sync: %w", err)
	}
	imageModels := make(map[int]bool, len(models))
	for _, grant := range grants {
		if grant.Protocols&model.ProtocolOpenAIImage != 0 {
			imageModels[grant.ChannelModelID] = true
		}
	}
	for _, channelModel := range models {
		kind := model.MediaKindText
		if imageModels[channelModel.ID] {
			kind = model.MediaKindImage
		}
		if channelModel.Kind == kind {
			continue
		}
		if err := tx.Model(&model.ChannelModel{}).Where("id = ?", channelModel.ID).
			Update("kind", string(kind)).Error; err != nil {
			return fmt.Errorf("failed to sync channel model kind: %w", err)
		}
	}
	return nil
}

// normalizedPath 去掉协议路径两端空白, 留空时回退到默认路径, 并校验前导斜杠。
// 缺少前导斜杠会与地址拼成错误的上游地址, 在写入前拒绝。
func normalizedPath(value, fallback string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	if !strings.HasPrefix(value, "/") {
		return value, fmt.Errorf("channel path %q must start with /", value)
	}
	return value, nil
}

// clearActiveItems 清理引用待删除授权的分组当前项。
// grants 既可以是授权主键切片, 也可以是筛选授权主键的子查询, 由调用方按删除的是授权, 模型还是凭据给出。
func clearActiveItems(tx *gorm.DB, grants any) error {
	itemIDs := tx.Model(&model.GroupItem{}).Select("id").Where("channel_grant_id IN (?)", grants)
	if err := tx.Model(&model.Group{}).
		Where("active_item_id IN (?)", itemIDs).
		Update("active_item_id", 0).Error; err != nil {
		return fmt.Errorf("failed to clear active items: %w", err)
	}
	return nil
}

// grantsOf 返回筛选指定列命中某组主键的授权主键子查询, 供 clearActiveItems 级联定位。
func grantsOf(tx *gorm.DB, column string, ids []int) *gorm.DB {
	return tx.Model(&model.ChannelGrant{}).Select("id").Where(column+" IN ?", ids)
}

// ChannelModelBrief 是渠道模型的展示要素: 上游模型名与归属渠道的对外展示名。
type ChannelModelBrief struct {
	ID          int
	Name        string
	ChannelName string // 已发布渠道示编码, 未发布渠道示真名。
}

// ChannelModelBriefs 返回对 scope 可见的渠道模型展示要素, 供模型页把评分按名称归并。
// 与 ChannelGrantCandidates 同一供给侧口径: 未发布渠道或未上架模型不出现在他人视角,
// 免得模型评分顺带泄漏内部渠道真名; 归属者与管理员仍可见自己的未发布渠道。
func ChannelModelBriefs(scope model.Scope) []ChannelModelBrief {
	briefs := make([]ChannelModelBrief, 0, channelModelCache.Len())
	for _, channelModel := range channelModelCache.GetAll() {
		channel, ok := channelCache.Get(channelModel.ChannelID)
		if !ok {
			continue
		}
		owner := scope.Owns(channel.UserID)
		if !channel.Shared && !owner {
			continue
		}
		if !channel.Shared && !channelModel.Listed && !owner {
			continue
		}
		name := channel.Name
		if channel.Shared && channel.ShareCode != "" {
			name = channel.ShareCode
		}
		briefs = append(briefs, ChannelModelBrief{ID: channelModel.ID, Name: channelModel.Name, ChannelName: name})
	}
	return briefs
}

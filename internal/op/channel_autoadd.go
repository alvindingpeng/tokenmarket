package op

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/charmbracelet/log"
	"github.com/dlclark/regexp2"
	"gorm.io/gorm"
)

// autoAddModelLimit 单次自动添加最多并入的新模型数, 防上游返回超长列表拖慢保存并撑爆渠道模型表。
const autoAddModelLimit = 200

// ProbeClientFactory 按渠道配置构建探测用的 HTTP 客户端(直连 / 应用代理 / 渠道专用代理)。
// 由启动装配注入(rhttp 构建客户端要读设置, 而 rhttp 已依赖 op —— op 直接 import 会成环):
// 保存时自动添加走 handler 注入的客户端, 后台任务走这里注入的工厂, 两处构建逻辑同源。
// 未注入(nil)时后台任务自行降级跳过, 不影响保存触发的自动添加。
var ProbeClientFactory func(target model.ChannelConfig) (*http.Client, error)

// AutoAddProbeTimeout 单个渠道探测的总时长上限: 上游 /models 挂在半开连接上时,
// 不让它拖住整轮后台复查 —— 该渠道跳过记日志, 其余渠道照常。
const AutoAddProbeTimeout = 90 * time.Second

// AutoAddFilter 是自动添加的条件集(B4), 全部来自设置页, 每次保存时重新读取 —— 管理员改完条件即生效, 无需重启。
//
// 空条件(全 0 / 空正则 / 开关关)表示不限制, 保持第一版「只要探测到就并入」的行为:
// 升级到本版不改变任何既有渠道的并入结果, 除非管理员主动收紧条件。
type AutoAddFilter struct {
	PriceMin  float64         // 参考价最低单价(每百万 token 的 blended 价), 0 不限。
	PriceMax  float64         // 参考价最高单价, 0 不限。
	MarkupMin float64         // 倍率下限(模型价 ÷ 渠道现有价位中位数), 0 不限。
	MarkupMax float64         // 倍率上限, 0 不限。
	Exclude   *regexp2.Regexp // 排除正则, nil 表示不排除。
	// Include 是名称白名单正则: 非 nil 时只有命中的模型名才允许并入。
	// 与 Exclude 的语义相反 —— 空条件(全 0 / 双 nil)仍是"探测到什么就并入什么", 升级到本版不改变既有渠道的并入结果。
	Include *regexp2.Regexp
	// FillPrice 为真时按参考价自动预填供货价, 让新并入的模型立刻可定价可上架(上架仍需人工确认)。
	FillPrice bool
	// Baseline 是渠道现有模型参考价的中位数, 作为倍率条件的基准; 0 表示渠道尚无定价模型, 倍率条件不生效。
	Baseline float64
}

// loadAutoAddFilter 从设置读取自动添加条件; 正则编译失败时降级为不排除并记日志。
// 设置项本身在保存时已校验过正则, 这里失败只可能是历史脏数据, 不该让自动添加整个失败。
// baseline 由调用方按渠道现有模型算出: 倍率条件要跟"这个渠道平时的价位"比, 而不是跟全局比。
func loadAutoAddFilter(baseline float64) AutoAddFilter {
	filter := AutoAddFilter{
		PriceMin:  SettingGetFloat(model.SettingKeyAutoAddPriceMin),
		PriceMax:  SettingGetFloat(model.SettingKeyAutoAddPriceMax),
		MarkupMin: SettingGetFloat(model.SettingKeyAutoAddMarkupMin),
		MarkupMax: SettingGetFloat(model.SettingKeyAutoAddMarkupMax),
		FillPrice: settingBoolDefault(model.SettingKeyAutoAddFillPrice, false),
		Baseline:  baseline,
	}
	if pattern := strings.TrimSpace(mustSettingString(model.SettingKeyAutoAddExclude)); pattern != "" {
		if compiled, err := regexp2.Compile(pattern, regexp2.ECMAScript); err == nil {
			filter.Exclude = compiled
		} else {
			log.Warnf("auto-add: invalid exclude regex %q, ignoring: %v", pattern, err)
		}
	}
	if pattern := strings.TrimSpace(mustSettingString(model.SettingKeyAutoAddInclude)); pattern != "" {
		if compiled, err := regexp2.Compile(pattern, regexp2.ECMAScript); err == nil {
			filter.Include = compiled
		} else {
			// 与排除项不同, 白名单编译失败时降级为"全放行"是危险的: 管理员设白名单的目的就是收紧,
			// 编译失败再放行等于条件形同虚设。但保存时已校验过, 这里失败只可能是历史脏数据, 照旧降级并记日志。
			log.Warnf("auto-add: invalid include regex %q, ignoring: %v", pattern, err)
		}
	}
	return filter
}

// settingBoolDefault 读布尔设置, 缺键或非法时返回 fallback。
func settingBoolDefault(key model.SettingKey, fallback bool) bool {
	value, err := SettingGetBool(key)
	if err != nil {
		return fallback
	}
	return value
}

// channelPriceBaseline 返回渠道现有已定价模型参考价的中位数, 作为倍率条件的基准。
// 用中位数而不是均值: 一个超贵的模型就能把均值拉高, 让倍率条件失去意义。
// 没有任何已定价模型时返回 0, 调用方据此跳过倍率条件(新渠道没有"平时价位"可言)。
func channelPriceBaseline(channelID int) float64 {
	prices := make([]float64, 0, 16)
	for _, channelModel := range channelModelCache.GetAll() {
		if channelModel.ChannelID != channelID {
			continue
		}
		// 优先用渠道自己的供货价; 未定价时回落参考价, 让基线在渠道刚建好时也有意义。
		if blended := (channelModel.SupplyPrice.Input + channelModel.SupplyPrice.Output) / 2; blended > 0 {
			prices = append(prices, blended)
			continue
		}
		if reference, ok := referenceBlendedPrice(channelModel.Name); ok {
			prices = append(prices, reference)
		}
	}
	if len(prices) == 0 {
		return 0
	}
	sort.Float64s(prices)
	mid := len(prices) / 2
	if len(prices)%2 == 1 {
		return prices[mid]
	}
	return (prices[mid-1] + prices[mid]) / 2
}

// Allow 判断一个上游模型是否通过全部条件; reason 说明被拒原因, 供审计与界面解释。
//
// 两个条件的语义:
//   - 价格区间: 模型的参考 blended 单价(每百万 token)必须落在 [PriceMin, PriceMax] 内;
//   - 倍率区间: 模型参考价 ÷ 渠道现有价位中位数(Baseline) 必须落在 [MarkupMin, MarkupMax] 内,
//     即「只添加与这个渠道平时供给价位相当的模型」, 用来挡住数量级不匹配的模型(如给低价渠道塞旗舰模型)。
//
// 参考价缺失时**放行**: 官方登记里查不到的模型(私有部署、内部别名)不该被价格条件一刀切掉,
// 否则条件一开就再也添加不进新厂商的模型。倍率同理 —— 没有参考价就无从计算倍率。
// 渠道尚无定价模型(Baseline=0)时倍率条件整体跳过: 新渠道没有"平时价位"可言。
func (f AutoAddFilter) Allow(name string) (bool, string) {
	// 白名单在最前: 设了 Include 就只放行命中的模型, 后面的条件只对通过白名单的名字生效。
	if f.Include != nil {
		matched, err := f.Include.MatchString(name)
		if err != nil {
			// 白名单运行期出错(通常是回溯超限)时若放行, 等于白名单被绕过 —— 保守方向是拒绝。
			return false, "include regex error"
		}
		if !matched {
			return false, "not in include regex"
		}
	}
	if f.Exclude != nil {
		matched, err := f.Exclude.MatchString(name)
		if err != nil {
			// 正则运行期错误(通常是回溯超限): 保守起见不排除, 只记原因。
			return true, ""
		}
		if matched {
			return false, "excluded by regex"
		}
	}

	// 参考价取 models.dev 的 blended 单价(输入输出均值), 与改价工作台的「参考价」同口径。
	price, hasPrice := referenceBlendedPrice(name)
	if !hasPrice {
		return true, ""
	}

	if f.PriceMin > 0 && price < f.PriceMin {
		return false, fmt.Sprintf("price %.4f below min %.4f", price, f.PriceMin)
	}
	if f.PriceMax > 0 && price > f.PriceMax {
		return false, fmt.Sprintf("price %.4f above max %.4f", price, f.PriceMax)
	}

	// 倍率条件需要基准: 渠道还没有任何定价模型时不判定, 否则新渠道永远添加不进第一个模型。
	if f.Baseline > 0 && (f.MarkupMin > 0 || f.MarkupMax > 0) {
		ratio := price / f.Baseline
		if f.MarkupMin > 0 && ratio < f.MarkupMin {
			return false, fmt.Sprintf("markup %.4fx below min %.4fx (baseline %.4f)", ratio, f.MarkupMin, f.Baseline)
		}
		if f.MarkupMax > 0 && ratio > f.MarkupMax {
			return false, fmt.Sprintf("markup %.4fx above max %.4fx (baseline %.4f)", ratio, f.MarkupMax, f.Baseline)
		}
	}
	return true, ""
}

// referenceBlendedPrice 返回模型的参考 blended 单价(每百万 token)与是否有登记。
// 与改价工作台同一口径: 输入输出均值, 查 models.dev 同步来的价格表(llm_infos)。
// 名称按小写匹配: models.dev 的键统一小写, 上游返回的模型名可能带大写。
func referenceBlendedPrice(name string) (float64, bool) {
	price, ok := llmModelCache.Get(strings.ToLower(strings.TrimSpace(name)))
	if !ok {
		return 0, false
	}
	if price.Input == 0 && price.Output == 0 {
		return 0, false
	}
	return (price.Input + price.Output) / 2, true
}

// AutoAddChannelModels 渠道保存后按配置自动探测并并入上游模型。
// 触发: 保存成功后由 handler 调用, 开关取自渠道配置的 model_auto_add; 探测为 best-effort,
// 失败只记日志并继续其余凭据, 不影响已经完成的渠道保存。
//
// 并入规则:
//   - 先按 AutoAddFilter 条件过滤(排除正则 / 价格区间 / 倍率区间), 未通过的不并入;
//   - 名称 trim 后去重(探测结果本身已按名称去重并合并协议位);
//   - 只增不删: 上游未返回的本地模型原样保留, 自动添加永不删除模型;
//   - 授权只并入不覆盖: 探测协议位按位 OR 进该凭据的授权, 管理员手设的协议位不被清掉;
//   - 单次新增上限 autoAddModelLimit, 超出部分跳过并截断;
//   - 授权定稿后重算模型媒体形态(生图位 → kind=image), 并刷新渠道子表与分组缓存。
//
// 审计: 新增模型以 channel.model-added 落一条记录, 含数量与名称明细(截断时注明);
// 被条件挡下的模型以 channel.model-skipped 单独记一条, 让管理员能看出「条件是否过紧」。
func AutoAddChannelModels(ctx context.Context, channelID int, config model.ChannelConfig, client *http.Client, actorID uint) error {
	// 只处理启用且已填 Key 的凭据: 空 Key 探测必失败, 停用的凭据不该被自动启用。
	var keys []model.ChannelKey
	if err := db.GetDB().WithContext(ctx).Where("channel_id = ?", channelID).Find(&keys).Error; err != nil {
		return fmt.Errorf("failed to load channel keys: %w", err)
	}
	filter := loadAutoAddFilter(channelPriceBaseline(channelID))
	keyIDByName := make(map[string]int, len(keys))
	type probeResult struct {
		keyName string
		fetched []model.ChannelFetchModel
	}
	var results []probeResult
	var skipped []string
	for _, channelKey := range keys {
		if !channelKey.Enabled || strings.TrimSpace(channelKey.Key) == "" {
			continue
		}
		keyIDByName[channelKey.Name] = channelKey.ID
		fetched, err := ProbeChannelModels(client, config, channelKey.Key, ctx)
		if err != nil {
			log.Warnf("auto-add: probe key %q of channel %d failed: %v", channelKey.Name, channelID, err)
			continue
		}
		// 条件过滤在探测之后、入库之前: 探测本身无法预知模型价格, 只能先拿到清单再筛。
		allowed := make([]model.ChannelFetchModel, 0, len(fetched))
		for _, item := range fetched {
			ok, reason := filter.Allow(item.Name)
			if !ok {
				skipped = append(skipped, item.Name+" ("+reason+")")
				continue
			}
			allowed = append(allowed, item)
		}
		if len(allowed) == 0 {
			continue
		}
		results = append(results, probeResult{keyName: channelKey.Name, fetched: allowed})
	}
	if len(results) == 0 && len(skipped) == 0 {
		return nil
	}

	var added []string
	truncated := false
	if len(results) > 0 {
		if err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			newCount := 0
			for _, result := range results {
				keyID, ok := keyIDByName[result.keyName]
				if !ok {
					continue
				}
				keyAdded, keyTruncated, err := mergeFetchedForKey(tx, channelID, keyID, result.fetched, autoAddModelLimit, &newCount)
				if err != nil {
					return err
				}
				added = append(added, keyAdded...)
				truncated = truncated || keyTruncated
			}
			// 预填供货价(B4): 只填参考价, 不改变上架状态 —— 上架等于对客可见, 必须人工确认。
			if filter.FillPrice {
				if err := fillSupplyPriceFromReference(tx, channelID, added); err != nil {
					// 预填失败不该回滚已并入的模型: 模型本身已经可用, 只是价格还要人工补。
					log.Warnf("auto-add: fill supply price for channel %d failed: %v", channelID, err)
				}
			}
			// 授权定稿后媒体形态可能变化(新并入生图位), 与手工保存同一套重算。
			return syncChannelModelKinds(tx, channelID)
		}); err != nil {
			return err
		}

		// 子表缓存与分组路由都要跟上新并入的模型与授权。
		if err := reloadChannelChildren(ctx, channelID); err != nil {
			return err
		}
		if err := groupRefreshCache(ctx); err != nil {
			return err
		}
	}

	if len(added) > 0 || truncated {
		detail := fmt.Sprintf("added=%d: %s", len(added), strings.Join(added, ", "))
		if truncated {
			detail += fmt.Sprintf(" (capped at %d new models)", autoAddModelLimit)
		}
		if err := AuditLogAdd(ctx, actorID, "channel.model-added", fmt.Sprintf("channel#%d", channelID), detail); err != nil {
			log.Warnf("auto-add: audit channel %d failed: %v", channelID, err)
		}
		// 自动发现的模型不触发复审: 它来自已过审的同一上游(地址与路径未动), 只是枚举供给面;
		// 基线随发现推进, 免得下一次人工保存被误判指纹失配。人工增删模型仍走 update 的指纹比对触发 stale。
		if err := channelReviewSyncBaseline(ctx, channelID); err != nil {
			log.Warnf("auto-add: sync review baseline for channel %d failed: %v", channelID, err)
		}
	}
	// 被条件挡下的模型单独记审计: 只记前 20 个, 免得一次探测几百个被拒模型把审计表刷满。
	if len(skipped) > 0 {
		detail := fmt.Sprintf("skipped=%d: %s", len(skipped), strings.Join(head(skipped, 20), ", "))
		if len(skipped) > 20 {
			detail += fmt.Sprintf(" (+%d more)", len(skipped)-20)
		}
		if err := AuditLogAdd(ctx, actorID, "channel.model-skipped", fmt.Sprintf("channel#%d", channelID), detail); err != nil {
			log.Warnf("auto-add: audit skipped models for channel %d failed: %v", channelID, err)
		}
	}
	return nil
}

// fillSupplyPriceFromReference 给刚并入的模型按参考价预填供货价。
// 只处理四价全 0 的模型: 管理员手工改过价的不覆盖, 否则每次保存渠道都会把人工定价冲掉。
func fillSupplyPriceFromReference(tx *gorm.DB, channelID int, names []string) error {
	if len(names) == 0 {
		return nil
	}
	for _, name := range names {
		var channelModel model.ChannelModel
		if err := tx.Where("channel_id = ? AND name = ?", channelID, name).First(&channelModel).Error; err != nil {
			continue
		}
		price := channelModel.SupplyPrice
		if price.Input != 0 || price.Output != 0 || price.CacheRead != 0 || price.CacheWrite != 0 {
			continue // 已定价, 不覆盖人工结果。
		}
		reference, ok := llmModelCache.Get(strings.ToLower(strings.TrimSpace(name)))
		if !ok {
			continue
		}
		// 必须用 Select + 结构体 Updates: supply_price 带 serializer:json,
		// Update("supply_price", struct) 会让 gorm 跳过序列化, 把结构体直接塞给驱动并报 unsupported type。
		patch := model.ChannelModel{SupplyPrice: reference}
		if err := tx.Model(&model.ChannelModel{}).Where("id = ?", channelModel.ID).
			Select("supply_price").Updates(&patch).Error; err != nil {
			return fmt.Errorf("failed to fill supply price for %q: %w", name, err)
		}
	}
	return nil
}

// head 返回切片前 n 个元素; 短于 n 时原样返回。
func head(items []string, n int) []string {
	if len(items) <= n {
		return items
	}
	return items[:n]
}

// mergeFetchedForKey 把单个凭据的探测结果并入渠道: 缺失模型建行, 该凭据上的授权按位 OR。
// newCount 跨凭据累计新增模型数, 达到 limit 即截断并停止处理剩余条目; 已存在模型的授权并入不计入上限。
// 只增不删: 不在此删除任何模型或授权, 上游未返回的本地内容原样保留。
func mergeFetchedForKey(tx *gorm.DB, channelID, keyID int, fetched []model.ChannelFetchModel, limit int, newCount *int) (added []string, truncated bool, err error) {
	var models []model.ChannelModel
	if err := tx.Where("channel_id = ?", channelID).Find(&models).Error; err != nil {
		return nil, false, fmt.Errorf("failed to load channel models: %w", err)
	}
	modelIDByName := make(map[string]int, len(models))
	for _, channelModel := range models {
		modelIDByName[channelModel.Name] = channelModel.ID
	}
	var grants []model.ChannelGrant
	if err := tx.Where("channel_key_id = ?", keyID).Find(&grants).Error; err != nil {
		return nil, false, fmt.Errorf("failed to load channel grants: %w", err)
	}
	protocolByModel := make(map[int]model.Protocol, len(grants))
	for _, grant := range grants {
		protocolByModel[grant.ChannelModelID] = grant.Protocols
	}

	for _, fetchedModel := range fetched {
		name := strings.TrimSpace(fetchedModel.Name)
		if name == "" {
			continue
		}
		protocols := fetchedModel.Protocols & definedProtocols
		modelID, ok := modelIDByName[name]
		if !ok {
			if *newCount >= limit {
				truncated = true
				break
			}
			channelModel := model.ChannelModel{ChannelID: channelID, Name: name}
			if err := tx.Create(&channelModel).Error; err != nil {
				return added, truncated, fmt.Errorf("failed to create channel model %q: %w", name, err)
			}
			modelID = channelModel.ID
			modelIDByName[name] = modelID
			*newCount++
			added = append(added, name)
		}
		if protocols == 0 {
			continue
		}
		current := protocolByModel[modelID]
		merged := current | protocols
		if merged == current {
			continue
		}
		if current == 0 {
			grant := model.ChannelGrant{ChannelModelID: modelID, ChannelKeyID: keyID, Protocols: merged}
			if err := tx.Create(&grant).Error; err != nil {
				return added, truncated, fmt.Errorf("failed to create channel grant: %w", err)
			}
		} else if err := tx.Model(&model.ChannelGrant{}).
			Where("channel_model_id = ? AND channel_key_id = ?", modelID, keyID).
			Update("protocols", merged).Error; err != nil {
			return added, truncated, fmt.Errorf("failed to update channel grant: %w", err)
		}
		protocolByModel[modelID] = merged
	}
	return added, truncated, nil
}

// AutoAddChannelsRun 按后台设置复查「已开启 model_auto_add 的渠道」, 探测并并入新上游模型。
// 触发: 由 task.channel_model_autoadd 周期调用; 开关默认关, 管理员在设置页「后台自动添加」打开。
//
// 为什么需要它: 保存触发的自动添加只覆盖「保存那一刻」的上游清单。上游后来上新的模型
// 永远不会被发现, 除非有人再去点一次保存 —— 自动添加因此只是一次性动作, 不是持续同步。
//
// 设计约束:
//   - 只扫已过审且未被收回的渠道: 探测等于向上游发请求, 拿未过审渠道去消耗上游配额没有意义;
//   - 每渠道独立判错: 一个上游超时不该让整轮复查判成任务失败, 一路飙到告警阈值;
//   - 工厂未注入时直接跳过: 只影响后台任务, 保存触发的自动添加由 handler 直接构造客户端;
//   - 逐渠道并入仍走 AutoAddChannelModels, 审计口径(channel.model-added / model-skipped)与手动触发完全一致,
//     操作者记为 0(系统), 轮次汇总只记日志, 不额外刷审计表。
func AutoAddChannelsRun(ctx context.Context) error {
	if !settingBoolDefault(model.SettingKeyAutoAddBackground, false) {
		return nil
	}
	if ProbeClientFactory == nil {
		log.Warn("auto-add-background: ProbeClientFactory not injected, skipping")
		return nil
	}

	var channels []model.Channel
	if err := autoAddScopeQuery(ctx).Find(&channels).Error; err != nil {
		return fmt.Errorf("failed to list channels for background auto-add: %w", err)
	}
	if len(channels) == 0 {
		// 空集不是"正常无事发生", 而是最容易被误判成功能没生效的状态:
		// 总开关开着、任务按时调度, 但渠道级开关一个都没开, 每轮都空跑。
		// 把范围与原因打进日志, 管理员看日志就知道该改 scope 还是去开渠道开关。
		log.Infof("auto-add-background: no channel in scope (scope=%s), nothing to do; "+
			"set %s=approved to cover all approved channels",
			AutoAddScopeName(), model.SettingKeyAutoAddScope)
		return nil
	}

	totalAdded := 0
	failed := 0
	for i := range channels {
		channel := channels[i]
		added, err := autoAddChannelOnce(ctx, channel.ID, channel.ChannelConfig)
		if err != nil {
			// 单渠道失败只记日志并计数, 不向上返回: 一个上游不可达不该让整轮任务判失败。
			failed++
			log.Warnf("auto-add-background: channel %d failed: %v", channel.ID, err)
			continue
		}
		totalAdded += added
	}
	log.Infof("auto-add-background: channels=%d added=%d failed=%d", len(channels), totalAdded, failed)
	return nil
}

// autoAddChannelOnce 处理单个渠道, 返回新增模型数。
// 探测超时由 context 控制: 上游 /models 挂在半开连接上时不该拖住整轮复查。
func autoAddChannelOnce(ctx context.Context, channelID int, config model.ChannelConfig) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, AutoAddProbeTimeout)
	defer cancel()

	client, err := ProbeClientFactory(config)
	if err != nil {
		return 0, fmt.Errorf("build probe client: %w", err)
	}
	// actorID=0 表示系统发起: 审计记录能区分「管理员点保存」与「后台定时发现」。
	before := countChannelModels(channelID)
	if err := AutoAddChannelModels(ctx, channelID, config, client, 0); err != nil {
		return 0, err
	}
	after := countChannelModels(channelID)
	if after > before {
		return after - before, nil
	}
	return 0, nil
}

// autoAddScopeQuery 构造后台复查的渠道筛选条件, 抽出来是为了让范围语义可被测试直接断言 ——
// 这段条件决定「哪些渠道会被真实探测」, 写错了不会有任何报错, 只会静默扫错范围。
//
// 扫描范围由 channel_auto_add_scope 决定:
//   - flagged  —— 只扫渠道级开关打开的(默认, 与升级前语义一致);
//   - approved —— 扫全部已过审渠道, 逐渠道用同一套条件过滤。
//
// 无论哪种都排除已收回的渠道: 它此时在选路里已被踢出, 探测它没有意义。
func autoAddScopeQuery(ctx context.Context) *gorm.DB {
	query := db.GetDB().WithContext(ctx).
		Where("review_status = ? AND trust_level <> ?",
			model.ChannelReviewApproved, model.ChannelTrustRevoked)
	if !AutoAddScopeAllApproved() {
		// model_auto_add 是 channels 表的平铺列(ChannelConfig 内嵌), 可直接作为 SQL 条件。
		query = query.Where("model_auto_add = ?", true)
	}
	return query
}

// AutoAddScopeAllApproved 判断后台复查是否应覆盖全部已过审渠道。
// 缺键或值非法一律按 false(只扫已开启的): 范围放大到全部渠道属于"会向上游多发请求"的动作,
// 读不到明确配置时保守处理, 不让一次配置读取失败变成对所有渠道的探测。
func AutoAddScopeAllApproved() bool {
	return AutoAddScopeName() == "approved"
}

// AutoAddScopeName 返回规范化后的复查范围名, 供日志与判定共用。
// 未配置/读取失败/值拼错一律归一成 flagged: 范围放大意味着向更多上游发真实请求,
// 配置不可信时保守处理, 不让一次读取失败悄悄扩散探测面。
func AutoAddScopeName() string {
	scope, err := SettingGetString(model.SettingKeyAutoAddScope)
	if err != nil {
		return "flagged"
	}
	if strings.TrimSpace(scope) == "approved" {
		return "approved"
	}
	return "flagged"
}

// countChannelModels 数某渠道当前的模型数; 后台任务据此算出本轮新增了多少。
// 直接查库而不读 channelModelCache: 缓存刷新由 AutoAddChannelModels 内部完成, 这里要的是落库结果。
func countChannelModels(channelID int) int {
	var count int64
	if err := db.GetDB().Model(&model.ChannelModel{}).Where("channel_id = ?", channelID).Count(&count).Error; err != nil {
		return 0
	}
	return int(count)
}

package op

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/charmbracelet/log"
	"github.com/dlclark/regexp2"
	"gorm.io/gorm"
)

// autoAddModelLimit 单次自动添加最多并入的新模型数, 防上游返回超长列表拖慢保存并撑爆渠道模型表。
const autoAddModelLimit = 200

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

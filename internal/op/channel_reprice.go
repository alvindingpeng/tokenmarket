package op

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// maxRepriceMultiplier 统一倍率上限: 官方价本身已是每百万 token 的价格口径, 超过这个倍数几乎必然是误输入。
const maxRepriceMultiplier = 100.0

// ChannelRepriceRow 是单个渠道模型的一键改价结果: 当前供货价, 官方价, 套用倍率后的新价与是否变化。
// 跳过原因一并给出, 界面才能解释"为什么这个模型没被改"(官方没收录 / 媒体模型 / 超出上限)。
type ChannelRepriceRow struct {
	Name     string          `json:"name"`              // 渠道模型名称。
	Kind     model.MediaKind `json:"kind,omitempty"`    // 媒体形态; 非文本模型不参与 token 口径改价。
	Listed   bool            `json:"listed"`            // 是否已上架。
	Current  model.LLMPrice  `json:"current"`           // 当前供货价。
	Official model.LLMPrice  `json:"official"`          // 官方价(价格表按模型名匹配得来)。
	Next     model.LLMPrice  `json:"next"`              // 官方价 × 统一倍率。
	Changed  bool            `json:"changed"`           // 新价与当前价是否有差异。
	Skipped  string          `json:"skipped,omitempty"` // 跳过原因: no_official_price | media_model | over_cap。
}

// ChannelRepriceResult 是一次一键改价的整体回执。
type ChannelRepriceResult struct {
	ChannelID  int                 `json:"channel_id"`
	Multiplier float64             `json:"multiplier"`
	Rows       []ChannelRepriceRow `json:"rows"`
	Changed    int                 `json:"changed"` // 价格发生变化的行数。
	Skipped    int                 `json:"skipped"` // 被跳过的行数。
	Updated    int                 `json:"updated"` // 实际写入的行数(仅 apply 时非零)。
}

// ChannelRepricePlan 按官方价 × 统一倍率算出渠道内每个模型的目标供货价, 不写库。
// 只处理文本模型: 官方价格表是 token 口径, 生图/生视频模型按张与秒计价, 官方登记里没有这类单价,
// 用 token 价去覆盖媒体价等于把收入口径改错, 故一律跳过并给出原因, 由管理员在改价工作台手工填。
func ChannelRepricePlan(channelID int, scope model.Scope, multiplier float64) (ChannelRepriceResult, error) {
	channel, ok := channelCache.Get(channelID)
	if !ok || !scope.Owns(channel.UserID) {
		return ChannelRepriceResult{}, fmt.Errorf("channel not found")
	}
	if multiplier <= 0 || multiplier > maxRepriceMultiplier {
		return ChannelRepriceResult{}, fmt.Errorf("multiplier must be between 0 and %g", maxRepriceMultiplier)
	}
	result := ChannelRepriceResult{ChannelID: channelID, Multiplier: multiplier, Rows: make([]ChannelRepriceRow, 0)}
	for _, channelModel := range channelModelCache.GetAll() {
		if channelModel.ChannelID != channelID {
			continue
		}
		row := ChannelRepriceRow{
			Name:    channelModel.Name,
			Kind:    channelModel.Kind,
			Listed:  channelModel.Listed,
			Current: channelModel.SupplyPrice,
		}
		if channelModel.Kind != model.MediaKindText {
			// 媒体形态模型的收入口径是按张/按秒, 与官方 token 价不同源, 不能一起改。
			row.Skipped = "media_model"
		} else if official, err := LLMGet(channelModel.Name); err != nil {
			// 官方价来自本地价格表(llm_infos): 它是 models.dev 的镜像, 由价格同步任务与重建接口维护。
			// 表里没有即官方未收录(或已被清理), 不猜价, 交由管理员在改价工作台手工填。
			row.Skipped = "no_official_price"
		} else {
			row.Official = official
			row.Next = official.Scale(multiplier - 1)
			if row.Next.Input > maxSupplyPricePerMillion || row.Next.Output > maxSupplyPricePerMillion ||
				row.Next.CacheRead > maxSupplyPricePerMillion || row.Next.CacheWrite > maxSupplyPricePerMillion {
				row.Skipped = "over_cap"
			} else {
				row.Changed = row.Next != row.Current
			}
		}
		if row.Skipped != "" {
			result.Skipped++
		} else if row.Changed {
			result.Changed++
		}
		result.Rows = append(result.Rows, row)
	}
	sort.Slice(result.Rows, func(i, j int) bool { return result.Rows[i].Name < result.Rows[j].Name })
	return result, nil
}

// ChannelRepriceApply 按统一倍率改价并落库。
// 只改供货价, 不动上架状态: 改价与上架是两件事, 一次改价不该顺带把未上架的模型放出去。
// names 非空时只改这些模型(界面按预览结果挑行), 为空则改全部可改的行。
func ChannelRepriceApply(channelID int, scope model.Scope, multiplier float64, names []string, ctx context.Context) (ChannelRepriceResult, error) {
	plan, err := ChannelRepricePlan(channelID, scope, multiplier)
	if err != nil {
		return ChannelRepriceResult{}, err
	}
	selected := make(map[string]struct{}, len(names))
	for _, name := range names {
		selected[strings.TrimSpace(name)] = struct{}{}
	}
	byName := make(map[string]model.ChannelModel)
	for _, channelModel := range channelModelCache.GetAll() {
		if channelModel.ChannelID == channelID {
			byName[channelModel.Name] = channelModel
		}
	}
	for i := range plan.Rows {
		row := &plan.Rows[i]
		if row.Skipped != "" || !row.Changed {
			continue
		}
		if len(selected) > 0 {
			if _, ok := selected[row.Name]; !ok {
				continue
			}
		}
		channelModel, ok := byName[row.Name]
		if !ok {
			continue
		}
		// supply_price 走 JSON 序列化器, 必须用结构体更新, 否则会写入非法值。
		if err := db.GetDB().WithContext(ctx).Model(&model.ChannelModel{}).Where("id = ?", channelModel.ID).
			Select("supply_price").Updates(model.ChannelModel{SupplyPrice: row.Next}).Error; err != nil {
			return ChannelRepriceResult{}, fmt.Errorf("failed to update supply price of %s: %w", row.Name, err)
		}
		channelModel.SupplyPrice = row.Next
		channelModelCacheWriteMu.Lock()
		channelModelCache.Set(channelModel.ID, channelModel)
		channelModelCacheWriteMu.Unlock()
		plan.Updated++
	}
	if plan.Updated > 0 {
		// 分组快照带用户价(供货价 × 上浮), 改价后必须重建, 否则界面与选路还按旧价。
		if err := groupRefreshCache(ctx); err != nil {
			return plan, err
		}
	}
	return plan, nil
}

// ChannelBreakerReset 手动解除渠道下的熔断: 按名称清零错误计数并清掉熔断截止时间。
// 自动熔断只按时间自愈, 上游修好(换 Key、补额度)后管理员需要能立刻恢复, 否则只能干等熔断到期。
func ChannelBreakerReset(channelID int, keyNames, modelNames []string, scope model.Scope, ctx context.Context) (int, error) {
	channel, ok := channelCache.Get(channelID)
	if !ok || !scope.Owns(channel.UserID) {
		return 0, fmt.Errorf("channel not found")
	}
	if len(keyNames) == 0 && len(modelNames) == 0 {
		return 0, fmt.Errorf("key_names or model_names is required")
	}
	reset := 0
	conn := db.GetDB().WithContext(ctx)
	for _, channelKey := range channelKeyCache.GetAll() {
		if channelKey.ChannelID != channelID || !containsName(keyNames, channelKey.Name) {
			continue
		}
		if err := conn.Model(&model.ChannelKey{}).Where("id = ?", channelKey.ID).
			Updates(map[string]any{"error_count": 0, "disabled_until": nil}).Error; err != nil {
			return reset, fmt.Errorf("failed to reset channel key %s: %w", channelKey.Name, err)
		}
		channelKey.ErrorCount = 0
		channelKey.DisabledUntil = nil
		channelKeyCache.Set(channelKey.ID, channelKey)
		reset++
	}
	for _, channelModel := range channelModelCache.GetAll() {
		if channelModel.ChannelID != channelID || !containsName(modelNames, channelModel.Name) {
			continue
		}
		if err := conn.Model(&model.ChannelModel{}).Where("id = ?", channelModel.ID).
			Updates(map[string]any{"error_count": 0, "disabled_until": nil}).Error; err != nil {
			return reset, fmt.Errorf("failed to reset channel model %s: %w", channelModel.Name, err)
		}
		channelModel.ErrorCount = 0
		channelModel.DisabledUntil = nil
		channelModelCacheWriteMu.Lock()
		channelModelCache.Set(channelModel.ID, channelModel)
		channelModelCacheWriteMu.Unlock()
		reset++
	}
	if reset > 0 {
		if err := groupRefreshCache(ctx); err != nil {
			return reset, err
		}
	}
	return reset, nil
}

// containsName 判断名称是否在清单里; 清单为空表示"不按名称过滤", 一律返回假。
func containsName(names []string, name string) bool {
	for _, item := range names {
		if strings.TrimSpace(item) == name {
			return true
		}
	}
	return false
}

package op

import (
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// MarketQualityBand 是广场一条挂牌行的质量区间(只给区间文案, 不给精确值)。
type MarketQualityBand struct {
	Success string `json:"success,omitempty"`
	Latency string `json:"latency,omitempty"`
	Uptime  string `json:"uptime,omitempty"`
}

// MarketQualityBandsForChannelModels 按渠道模型主键取近 days 天的质量区间。
//
// 为什么入参是渠道模型而不是渠道: 广场对外的 MarketChannel 刻意不含 channel_id(脱敏, 不外泄
// 上游身份), 但它带着 ChannelModelID —— 由它反查渠道, 才能既不破脱敏约定又拿到质量数据。
//
// 时效门槛与 op.ShareQualityOf 完全一致(7 天内), 复用同一套判定而不是各写一份:
// 过期快照展示出去等于给出过期承诺。
//
// 三个维度各自独立判样本门槛: 探测次数不足时只省略可用率, 不把成功率与延迟一起抹掉 ——
// "没开探测"和"质量差"是两件事, 混在一起会让没开探测的渠道永远显示为空。
func MarketQualityBandsForChannelModels(channelModelIDs []int, days int) map[int]MarketQualityBand {
	out := make(map[int]MarketQualityBand, len(channelModelIDs))
	if len(channelModelIDs) == 0 {
		return out
	}
	if days <= 0 {
		days = 7
	}
	if days > 90 {
		days = 90
	}
	// 反查渠道: 一次查询带出 (channel_model_id → channel_id) 的映射。
	var channelModels []model.ChannelModel
	if err := db.GetDB().Where("id IN ?", channelModelIDs).Find(&channelModels).Error; err != nil {
		return out
	}
	channelOfModel := make(map[int]int, len(channelModels))
	channelIDs := make([]int, 0, len(channelModels))
	seenChannel := make(map[int]struct{}, len(channelModels))
	for _, cm := range channelModels {
		channelOfModel[cm.ID] = cm.ChannelID
		if _, ok := seenChannel[cm.ChannelID]; !ok {
			seenChannel[cm.ChannelID] = struct{}{}
			channelIDs = append(channelIDs, cm.ChannelID)
		}
	}
	if len(channelIDs) == 0 {
		return out
	}

	// 每个渠道取窗口内最新的一条快照: 与 ShareQualityOf 的 Order("date DESC").First 同口径。
	// days 在这里既当"取数窗口"又当"时效门槛": 两者本来就应当一致, 分开写只会允许窗口外的旧快照混进来。
	since := time.Now().AddDate(0, 0, -(days - 1)).Format("20060102")
	var snapshots []model.ChannelQualitySnapshot
	if err := db.GetDB().Where("channel_id IN ? AND date >= ?", channelIDs, since).
		Order("date DESC").Find(&snapshots).Error; err != nil {
		return out
	}
	latest := make(map[int]model.ChannelQualitySnapshot, len(channelIDs))
	for _, snapshot := range snapshots {
		if _, ok := latest[snapshot.ChannelID]; !ok {
			latest[snapshot.ChannelID] = snapshot
		}
	}

	for modelID, channelID := range channelOfModel {
		snapshot, ok := latest[channelID]
		if !ok {
			continue
		}
		band := MarketQualityBand{}
		// 成功率与延迟: 样本按 RequestCount 判门槛(与 QualityMinSamples 同源)。
		if snapshot.RequestCount >= model.QualityMinSamples {
			band.Success = model.QualityBandSuccess(snapshot.SuccessRate)
			band.Latency = model.QualityBandLatency(snapshot.AvgWaitMs)
		}
		// 可用率另判探测次数: 与 QualityGradeOf 里"探测不足则不参与加权"同一条规则。
		if snapshot.HealthChecks >= marketMinHealthChecks {
			band.Uptime = model.QualityBandUptime(snapshot.UptimePct)
		}
		if band.Success == "" && band.Latency == "" && band.Uptime == "" {
			continue
		}
		out[modelID] = band
	}
	return out
}

// marketMinHealthChecks 与 model 包里同名常量的取值一致。
// 不直接引用那一个是因为它未导出: 这里的判定口径必须与 QualityGradeOf 相同, 写死同一个数字
// 并在测试里锁定"两者相等", 比各写一份魔法数字更难漂移。
const marketMinHealthChecks int64 = 3

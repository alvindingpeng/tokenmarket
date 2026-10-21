package op

import (
	"context"
	"fmt"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/charmbracelet/log"
	"gorm.io/gorm"
)

// 渠道观察期(P2)与质量快照(P3)。
//
// 两者共用同一份原料, 但职责不同:
//   - 观察期是「准入决策」: 新过审的渠道先在聚光灯下跑 N 天, 达标转正(trusted), 不达标降级(probation→revoked);
//   - 质量快照是「历史沉淀」: 每天把真实流量与探测可用率固化成一行, 供 30 天趋势与公开分享页展示。
//
// 铁律(与既有资金/运营改造一致): 与计费/结算/钱包完全正交 —— 本文件只读统计, 不写任何余额口径。

// 观察期相关设置读取。全部经 SettingGet* 拿, 缺键时回落到设计稿默认值,
// 避免旧库尚未补齐默认行的瞬间让评估逻辑跑在 0 天上。
func observationDays() int {
	if n, err := SettingGetInt(model.SettingKeyChannelObservationDays); err == nil && n > 0 {
		return n
	}
	return 7
}

func observationMinSamples() int {
	if n, err := SettingGetInt(model.SettingKeyChannelObservationMinSamples); err == nil && n > 0 {
		return n
	}
	return 100
}

func observationSuccessFloor() float64 {
	floor := SettingGetFloat(model.SettingKeyChannelObservationSuccessFlr)
	if floor <= 0 || floor > 1 {
		return 0.98
	}
	return floor
}

// ChannelTrustLevelOf 返回渠道当前信任级别; 渠道不存在时 ok=false。
// 选路侧据此做观察期折减, 故只读缓存、不查库: 它在每个请求的评分路径上被调用。
func ChannelTrustLevelOf(channelID int) (level string, ok bool) {
	channel, found := channelCache.Get(channelID)
	if !found {
		return "", false
	}
	return channel.TrustLevel, true
}

// ChannelTrustStart 把渠道置入观察期: 审核通过时调用。
// 管理员免审渠道同样走观察期 —— 免审只省掉审批动作, 不豁免质量观察, 否则管理员自建渠道永远不受质量约束。
// 已处于 trusted 的渠道不重置观察期: 复审(stale→approved)不应让一个稳定渠道重新从头跑 7 天。
func ChannelTrustStart(tx *gorm.DB, channelID int) error {
	now := time.Now()
	until := now.AddDate(0, 0, observationDays())
	return tx.Model(&model.Channel{}).
		Where("id = ? AND trust_level <> ?", channelID, model.ChannelTrustTrusted).
		Updates(map[string]any{
			"trust_level":       model.ChannelTrustObserving,
			"observation_start": &now,
			"observation_until": &until,
		}).Error
}

// ChannelTrustEvaluate 逐渠道评估观察期(P2 日任务)。
//
// 判定只用「观察期内的累计数据」, 口径与设计稿一致:
//   - 请求数达 observationMinSamples 且成功率 >= observationSuccessFloor → 转正 trusted;
//   - 请求数达标但成功率不达标 → 首次 probation(延长一轮), 已 probation 再不合格 → revoked(收回发布);
//   - 观察期未到期且样本不足 → 保持 observing, 等数据; 到期仍无样本 → probation(无法证明质量, 不给转正)。
//
// 转正/降级都写审计, 便于事后解释「这个渠道为什么被收回」。
func ChannelTrustEvaluate(ctx context.Context) error {
	dbConn := db.GetDB().WithContext(ctx)

	// 只评估已过审渠道: 观察期是「审核通过之后」的阶段, 未过审渠道本来就不能发布,
	// 对它们做降级毫无意义, 只会在审核队列里制造一堆看不懂的 revoked。
	var channels []model.Channel
	if err := dbConn.Where("trust_level IN ? AND review_status = ?", []string{
		model.ChannelTrustObserving, model.ChannelTrustProbation,
	}, model.ChannelReviewApproved).Find(&channels).Error; err != nil {
		return fmt.Errorf("failed to load channels for trust evaluation: %w", err)
	}
	if len(channels) == 0 {
		return nil
	}

	minSamples := observationMinSamples()
	floor := observationSuccessFloor()
	now := time.Now()

	for _, channel := range channels {
		// 没有观察期窗口的渠道一律跳过, 不参与判定。
		// 存量渠道(本能力上线前就已存在)由迁移 021 一次性标为 trusted, 不该被追溯性判成观察期:
		// 若在这里按「无窗口 ⇒ 视为已到期 ⇒ 无观察期样本 ⇒ 不达标」处理, 首次运行会把全站老渠道
		// 批量打成 probation, 等于给它们的评分统一乘 0.70 —— 一次真实的线上回归。
		// 这里只做防御性跳过: 窗口为空说明数据异常, 保持现状比误判安全。
		if channel.ObservationStart == nil {
			log.Warnf("trust eval: channel %d has no observation window, skipping", channel.ID)
			continue
		}
		// 统计窗口: 观察期起点到今天。
		// 用累计而非「昨日单日」: 一次抖动不该决定新渠道的生死, 观察期本就是看整体表现。
		start := *channel.ObservationStart
		if !channel.ObservationStart.Before(now) {
			start = now.AddDate(0, 0, -observationDays())
		}
		var daily []model.StatsChannelDaily
		if err := dbConn.Where("channel_id = ? AND date >= ?", channel.ID, start.Format("20060102")).
			Find(&daily).Error; err != nil {
			log.Warnf("trust eval: load daily stats for channel %d failed: %v", channel.ID, err)
			continue
		}
		var success, failed int64
		for _, row := range daily {
			success += row.RequestSuccess
			failed += row.RequestFailed
		}
		total := success + failed
		successRate := 0.0
		if total > 0 {
			successRate = float64(success) / float64(total)
		}

		// 未到期且样本不足: 继续等, 既不算达标也不算不达标。
		// 到期(或已 probation 一轮)仍无足够样本 → 无法证明质量, 按不达标处理。
		expired := channel.ObservationUntil == nil || !now.Before(*channel.ObservationUntil)
		if total < int64(minSamples) && !expired {
			continue
		}

		passed := total >= int64(minSamples) && successRate >= floor
		switch {
		case passed:
			if channel.TrustLevel == model.ChannelTrustTrusted {
				continue
			}
			if err := dbConn.Model(&model.Channel{}).Where("id = ?", channel.ID).
				Update("trust_level", model.ChannelTrustTrusted).Error; err != nil {
				log.Warnf("trust eval: promote channel %d failed: %v", channel.ID, err)
				continue
			}
			channelCacheUpdateTrust(channel.ID, model.ChannelTrustTrusted)
			auditTrust(ctx, channel.ID, "channel.trust_promote",
				fmt.Sprintf("samples=%d success_rate=%.4f floor=%.4f", total, successRate, floor))
		case channel.TrustLevel == model.ChannelTrustProbation:
			// 已经是延长观察仍不达标 → 收回发布资格。
			if err := dbConn.Model(&model.Channel{}).Where("id = ?", channel.ID).
				Update("trust_level", model.ChannelTrustRevoked).Error; err != nil {
				log.Warnf("trust eval: revoke channel %d failed: %v", channel.ID, err)
				continue
			}
			channelCacheUpdateTrust(channel.ID, model.ChannelTrustRevoked)
			auditTrust(ctx, channel.ID, "channel.trust_revoke",
				fmt.Sprintf("samples=%d success_rate=%.4f floor=%.4f (probation failed)", total, successRate, floor))
		default:
			// 首次不达标: 延长一轮观察而不是直接收回, 给新渠道一次自我修正的机会。
			until := now.AddDate(0, 0, observationDays())
			if err := dbConn.Model(&model.Channel{}).Where("id = ?", channel.ID).
				Updates(map[string]any{
					"trust_level":       model.ChannelTrustProbation,
					"observation_until": &until,
				}).Error; err != nil {
				log.Warnf("trust eval: probation channel %d failed: %v", channel.ID, err)
				continue
			}
			channelCacheUpdateTrust(channel.ID, model.ChannelTrustProbation)
			auditTrust(ctx, channel.ID, "channel.trust_probation",
				fmt.Sprintf("samples=%d success_rate=%.4f floor=%.4f", total, successRate, floor))
		}
	}
	return nil
}

// channelCacheUpdateTrust 同步进程内渠道副本的信任级别。
// 不回读整行: 评估只改这一个字段, 从库里重查会把并发改动一起卷进来。
func channelCacheUpdateTrust(channelID int, trustLevel string) {
	channel, ok := channelCache.Get(channelID)
	if !ok {
		return
	}
	channel.TrustLevel = trustLevel
	channelCache.Set(channelID, channel)
}

// auditTrust 记录观察期升降级; actorID 为 0 表示系统自动评估。
func auditTrust(ctx context.Context, channelID int, action, detail string) {
	if err := AuditLogAdd(ctx, 0, action, fmt.Sprintf("channel#%d", channelID), detail); err != nil {
		log.Warnf("trust eval: audit %s for channel %d failed: %v", action, channelID, err)
	}
}

// ChannelQualitySnapshotRun 生成昨日质量快照并裁剪超期数据(P3 日任务)。
//
// 为什么跑「昨日」而不是「今日」: 今日的统计还在内存里累加、尚未完整落库,
// 拿它固化会把半天的数据写成一天, 趋势曲线出现虚假低谷。只有已经翻页的昨日才是定稿数据。
func ChannelQualitySnapshotRun(ctx context.Context) error {
	dbConn := db.GetDB().WithContext(ctx)
	yesterday := time.Now().AddDate(0, 0, -1).Format("20060102")

	var channels []model.Channel
	if err := dbConn.Select("id").Find(&channels).Error; err != nil {
		return fmt.Errorf("failed to load channels: %w", err)
	}
	if len(channels) == 0 {
		return nil
	}

	// 昨日探测数据一次性捞出按渠道聚合, 避免每渠道一条查询。
	type healthAgg struct {
		ChannelID int
		Checks    int64
		Fails     int64
	}
	var aggs []healthAgg
	startOfDay := time.Now().AddDate(0, 0, -1)
	startOfDay = time.Date(startOfDay.Year(), startOfDay.Month(), startOfDay.Day(), 0, 0, 0, 0, startOfDay.Location())
	if err := dbConn.Model(&model.ChannelHealthRecord{}).
		Select("channel_id, COUNT(*) AS checks, SUM(CASE WHEN healthy THEN 0 ELSE 1 END) AS fails").
		Where("checked_at >= ? AND checked_at < ?", startOfDay, startOfDay.AddDate(0, 0, 1)).
		Group("channel_id").
		Scan(&aggs).Error; err != nil {
		// 探测数据缺失不该阻断真实流量快照: 可用率维度记 0 并继续。
		log.Warnf("quality snapshot: aggregate health records failed: %v", err)
	}
	healthByChannel := make(map[int]healthAgg, len(aggs))
	for _, agg := range aggs {
		healthByChannel[agg.ChannelID] = agg
	}

	written := 0
	for _, channel := range channels {
		var daily model.StatsChannelDaily
		err := dbConn.Where("channel_id = ? AND date = ?", channel.ID, yesterday).First(&daily).Error
		if err != nil && err != gorm.ErrRecordNotFound {
			log.Warnf("quality snapshot: load daily stats for channel %d failed: %v", channel.ID, err)
			continue
		}
		// 无流量且无探测的渠道不写行: 空行会让 30 天趋势出现一堆假的零点。
		health := healthByChannel[channel.ID]
		if err == gorm.ErrRecordNotFound && health.Checks == 0 {
			continue
		}

		total := daily.RequestSuccess + daily.RequestFailed
		successRate := 0.0
		if total > 0 {
			successRate = float64(daily.RequestSuccess) / float64(total)
		}
		avgWait := int64(0)
		if total > 0 {
			avgWait = daily.WaitTime / total
		}
		uptime := 0.0
		if health.Checks > 0 {
			uptime = float64(health.Checks-health.Fails) / float64(health.Checks) * 100
		}
		_, grade := model.QualityGradeOf(successRate, avgWait, uptime, total, health.Checks)

		snapshot := model.ChannelQualitySnapshot{
			ChannelID:    channel.ID,
			Date:         yesterday,
			RequestCount: total,
			SuccessCount: daily.RequestSuccess,
			FailCount:    daily.RequestFailed,
			SuccessRate:  successRate,
			AvgWaitMs:    avgWait,
			UptimePct:    uptime,
			HealthChecks: health.Checks,
			HealthFails:  health.Fails,
			Grade:        grade,
			CreatedAt:    time.Now(),
		}
		// 用 upsert 而非 create: 任务重跑(手动触发、进程重启后 RunOnStart)不能因唯一键冲突失败。
		if err := dbConn.Where("channel_id = ? AND date = ?", channel.ID, yesterday).
			Assign(snapshot).FirstOrCreate(&model.ChannelQualitySnapshot{}).Error; err != nil {
			log.Warnf("quality snapshot: upsert channel %d failed: %v", channel.ID, err)
			continue
		}
		written++
	}

	// 180 天裁剪: 与设计稿一致, 更早的快照只占空间且已无展示用途。
	cutoff := time.Now().AddDate(0, 0, -180).Format("20060102")
	if err := dbConn.Where("date < ?", cutoff).Delete(&model.ChannelQualitySnapshot{}).Error; err != nil {
		log.Warnf("quality snapshot: prune old snapshots failed: %v", err)
	}
	if written > 0 {
		log.Infof("quality snapshot: date=%s channels=%d", yesterday, written)
	}
	return nil
}

// ChannelQualitySummary 是单个渠道当前质量概览(内部视图, 给归属者与管理员)。
type ChannelQualitySummary struct {
	ChannelID    int    `json:"channel_id"`
	TrustLevel   string `json:"trust_level"`
	ReviewStatus string `json:"review_status"`
	// Observing 表示仍在观察期, 前端据此显示进度条与角标。
	Observing        bool       `json:"observing"`
	ObservationStart *time.Time `json:"observation_start,omitempty"`
	ObservationUntil *time.Time `json:"observation_until,omitempty"`
	ObservationDays  int        `json:"observation_days"`
	MinSamples       int        `json:"min_samples"`
	SuccessFloor     float64    `json:"success_floor"`
	// WindowSamples 是观察期内累计请求数, 前端拿它与 MinSamples 画进度。
	WindowSamples     int64   `json:"window_samples"`
	WindowSuccessRate float64 `json:"window_success_rate"`
	// Yesterday 是最近一天的快照(可能为 nil: 当天无流量)。
	Yesterday *model.ChannelQualitySnapshot `json:"yesterday,omitempty"`
	// Trend 是近 N 天快照, 按日期升序。
	Trend []model.ChannelQualitySnapshot `json:"trend"`
}

// ChannelQualityGet 返回渠道质量概览与趋势; 非归属者按 404 语义处理, 不暴露资源存在性。
func ChannelQualityGet(channelID int, days int, scope model.Scope) (ChannelQualitySummary, error) {
	channel, ok := channelCache.Get(channelID)
	if !ok || !scope.Owns(channel.UserID) {
		return ChannelQualitySummary{}, fmt.Errorf("channel not found")
	}
	if days < 1 || days > 180 {
		days = 30
	}
	dbConn := db.GetDB()

	var trend []model.ChannelQualitySnapshot
	since := time.Now().AddDate(0, 0, -(days - 1)).Format("20060102")
	if err := dbConn.Where("channel_id = ? AND date >= ?", channelID, since).
		Order("date ASC").Find(&trend).Error; err != nil {
		return ChannelQualitySummary{}, err
	}
	if trend == nil {
		trend = []model.ChannelQualitySnapshot{}
	}

	// 观察期累计: 与评估任务同一窗口口径, 让界面上的进度条与后台判定不会各说各话。
	start := time.Now().AddDate(0, 0, -observationDays())
	if channel.ObservationStart != nil && channel.ObservationStart.Before(time.Now()) {
		start = *channel.ObservationStart
	}
	var daily []model.StatsChannelDaily
	if err := dbConn.Where("channel_id = ? AND date >= ?", channelID, start.Format("20060102")).
		Find(&daily).Error; err != nil {
		return ChannelQualitySummary{}, err
	}
	var success, failed int64
	for _, row := range daily {
		success += row.RequestSuccess
		failed += row.RequestFailed
	}
	windowTotal := success + failed
	windowRate := 0.0
	if windowTotal > 0 {
		windowRate = float64(success) / float64(windowTotal)
	}

	summary := ChannelQualitySummary{
		ChannelID:         channelID,
		TrustLevel:        channel.TrustLevel,
		ReviewStatus:      channel.ReviewStatus,
		Observing:         channel.TrustLevel == model.ChannelTrustObserving || channel.TrustLevel == model.ChannelTrustProbation,
		ObservationStart:  channel.ObservationStart,
		ObservationUntil:  channel.ObservationUntil,
		ObservationDays:   observationDays(),
		MinSamples:        observationMinSamples(),
		SuccessFloor:      observationSuccessFloor(),
		WindowSamples:     windowTotal,
		WindowSuccessRate: windowRate,
		Trend:             trend,
	}
	if len(trend) > 0 {
		last := trend[len(trend)-1]
		summary.Yesterday = &last
	}
	return summary, nil
}

// ShareQuality 是公开分享页的质量摘要(脱敏): 只给区间与等级, 不出精确成功率/延迟/请求量。
// 精确值会变成渠道商的竞争情报, 也会让用户按小数点比较不同渠道。
type ShareQuality struct {
	Grade       string `json:"grade,omitempty"`        // 等级 A|B|C|D, 空串表示样本不足不评级。
	UptimeBand  string `json:"uptime_band,omitempty"`  // 99.9%+ | 99%+ | 95%+ | <95%
	LatencyBand string `json:"latency_band,omitempty"` // <2s | 2-5s | >5s
	SuccessBand string `json:"success_band,omitempty"` // 99.5%+ | 99%+ | 95%+ | <95%
	Observing   bool   `json:"observing"`              // 观察期标注(透明化, 不隐藏)。
	Since       string `json:"since,omitempty"`        // 质量数据起算日(第一条快照日期)。
}

// ShareQualityOf 按渠道最近一天快照算出脱敏质量摘要; 无快照时返回 nil(分享页不显示质量条)。
func ShareQualityOf(channelID int) *ShareQuality {
	dbConn := db.GetDB()
	var latest model.ChannelQualitySnapshot
	if err := dbConn.Where("channel_id = ?", channelID).
		Order("date DESC").First(&latest).Error; err != nil {
		return nil
	}
	// 超过 7 天没更新的快照不再代表现状, 展示出去等于给出过期承诺。
	if parsed, err := time.ParseInLocation("20060102", latest.Date, time.Local); err == nil {
		if time.Since(parsed) > 7*24*time.Hour {
			return nil
		}
	}
	var earliest model.ChannelQualitySnapshot
	since := ""
	if err := dbConn.Where("channel_id = ?", channelID).
		Order("date ASC").First(&earliest).Error; err == nil {
		if parsed, err := time.ParseInLocation("20060102", earliest.Date, time.Local); err == nil {
			since = parsed.Format("2006-01-02")
		}
	}
	observing := false
	if channel, ok := channelCache.Get(channelID); ok {
		observing = channel.TrustLevel == model.ChannelTrustObserving || channel.TrustLevel == model.ChannelTrustProbation
	}
	return &ShareQuality{
		Grade:       latest.Grade,
		UptimeBand:  model.QualityBandUptime(latest.UptimePct),
		LatencyBand: model.QualityBandLatency(latest.AvgWaitMs),
		SuccessBand: model.QualityBandSuccess(latest.SuccessRate),
		Observing:   observing,
		Since:       since,
	}
}

package op

import (
	"context"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// 本文件锁的是「统计页与账单页同一区间同一份钱」这条不变量:
// 用户统计的金额/词元/请求数必须读「明细 ∪ 日汇总」(billing_records + billing_rollups),
// 否则明细按留存期(默认 180 天)清理后, 统计页会随清理一天天变小, 而账单页(BillingTotal)
// 并了汇总 —— 两页数字对不上, 用户对账时无法解释差额。

// userStatsDayWindow 返回某一天对应的 start_date/end_date 查询参数(单日区间)。
func userStatsDayWindow(day time.Time) (string, string) {
	stamp := day.Format("2006-01-02")
	return stamp, stamp
}

// userStatsSeedRollup 直接写一行用户维度的日汇总, 模拟「明细已被清理、只剩汇总」的状态。
func userStatsSeedRollup(t *testing.T, userID uint, day time.Time, requests int64, cost float64, tokens int64) model.BillingRollup {
	t.Helper()
	row := model.BillingRollup{
		Dimension:     model.BillingRollupUser,
		DimensionID:   userID,
		Day:           day.Format("20060102"),
		InputToken:    tokens,
		RequestCount:  requests,
		UserCost:      cost,
		SealedAt:      time.Now(),
		SourceRecords: requests,
	}
	if err := db.GetDB().Create(&row).Error; err != nil {
		t.Fatalf("seed rollup: %v", err)
	}
	return row
}

// TestUserStatsBillingMergedMatchesDetails 未归档窗口下, 新合并读口与旧明细口径逐字段相等。
// 防的是"合并把已有数字算成两倍": 没有汇总行时, 合并结果必须等于明细聚合。
func TestUserStatsBillingMergedMatchesDetails(t *testing.T) {
	payer, _ := userStatsTestSetup(t)
	ctx := context.Background()
	recent := dayStart(time.Now()).AddDate(0, 0, -10)
	older := dayStart(time.Now()).AddDate(0, 0, -40)
	userStatsInsert(t,
		userStatsRow(payer.ID, "m", model.BillingStatusSettled, recent.Add(9*time.Hour), 2, 10),
		userStatsRow(payer.ID, "m", model.BillingStatusSettled, recent.Add(10*time.Hour), 3, 20),
		userStatsRow(payer.ID, "m", model.BillingStatusRefunded, older.Add(12*time.Hour), 4, 30),
	)

	var rollups int64
	if err := db.GetDB().Model(&model.BillingRollup{}).
		Where("dimension = ? AND dimension_id = ?", model.BillingRollupUser, payer.ID).
		Count(&rollups).Error; err != nil {
		t.Fatalf("count rollups: %v", err)
	}
	if rollups != 0 {
		t.Fatalf("本用例前提是窗口内没有汇总行, 实际 %d 行", rollups)
	}

	startStamp := older.Format("2006-01-02")
	endStamp := recent.Format("2006-01-02")
	overview, err := UserStatsOverview(ctx, payer.ID, startStamp, endStamp)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	// 退款行照旧计入(口径与 BillingTotal 的全额列一致), 不能被净额口径改掉。
	if overview.TotalRequests != 3 || overview.TotalCost != 9 || overview.TotalTokens != 60 {
		t.Fatalf("合并读口与明细口径不一致(疑似重复计数或漏算): %+v", overview)
	}
	if overview.InputTokens != 60 || overview.OutputTokens != 0 || overview.CacheReadTokens != 0 {
		t.Fatalf("词元分项: %+v", overview)
	}
	if overview.BillingCoverageStart != startStamp {
		t.Fatalf("覆盖起点应为区间内最早明细日 %s, got %q", startStamp, overview.BillingCoverageStart)
	}
	if overview.BillingRollupCoveredUntil != "" {
		t.Fatalf("没有汇总行时不应报告汇总覆盖: %q", overview.BillingRollupCoveredUntil)
	}

	// 曲线同理: 没有汇总时, 两天的点与金额都要与明细一致。
	trend, err := UserStatsTrend(ctx, payer.ID, startStamp, endStamp, "day")
	if err != nil {
		t.Fatalf("trend: %v", err)
	}
	if len(trend) != 2 {
		t.Fatalf("趋势点数 = %d, want 2: %+v", len(trend), trend)
	}
	var sumCost float64
	var sumRequests int64
	for _, point := range trend {
		sumCost += point.Cost
		sumRequests += point.Requests
	}
	if sumRequests != 3 || sumCost != 9 {
		t.Fatalf("趋势合计 = (%d, %v), want (3, 9): %+v", sumRequests, sumCost, trend)
	}
}

// TestUserStatsBillingMergedSurvivesRetention 超期明细被清理后, 同一区间的金额/词元/请求数不缩水。
// 这就是本缺陷的复现路径: 清理前统计页能看到的数, 清理后必须一分不少(改由日汇总补齐)。
func TestUserStatsBillingMergedSurvivesRetention(t *testing.T) {
	payer, _ := userStatsTestSetup(t)
	ctx := context.Background()
	withBillingSettings(t, 180, false)

	// 明确落在留存期之外的一天, 且整日都在截止点之前(清理按整天固化)。
	oldDay := billingRetentionCutoff(180).AddDate(0, 0, -30)
	userStatsInsert(t, userStatsRow(payer.ID, "m", model.BillingStatusSettled, oldDay.Add(9*time.Hour), 7, 100))

	startStamp, endStamp := userStatsDayWindow(oldDay)
	before, err := UserStatsOverview(ctx, payer.ID, startStamp, endStamp)
	if err != nil {
		t.Fatalf("overview before: %v", err)
	}
	if before.TotalRequests != 1 || before.TotalCost != 7 || before.TotalTokens != 100 {
		t.Fatalf("清理前概览: %+v", before)
	}
	beforeTrend, err := UserStatsTrend(ctx, payer.ID, startStamp, endStamp, "day")
	if err != nil {
		t.Fatalf("trend before: %v", err)
	}
	if len(beforeTrend) != 1 || beforeTrend[0].Cost != 7 || beforeTrend[0].Requests != 1 {
		t.Fatalf("清理前趋势: %+v", beforeTrend)
	}

	result, err := BillingRetentionRun(ctx)
	if err != nil {
		t.Fatalf("billing retention: %v", err)
	}
	if result.DeletedRecords != 1 {
		t.Fatalf("删除明细 = %d, want 1", result.DeletedRecords)
	}
	var live int64
	if err := db.GetDB().Model(&model.BillingRecord{}).Where("user_id = ?", payer.ID).Count(&live).Error; err != nil {
		t.Fatalf("count live records: %v", err)
	}
	if live != 0 {
		t.Fatalf("明细应已被清理, 还剩 %d 行", live)
	}

	after, err := UserStatsOverview(ctx, payer.ID, startStamp, endStamp)
	if err != nil {
		t.Fatalf("overview after: %v", err)
	}
	if after.TotalRequests != before.TotalRequests || after.TotalCost != before.TotalCost || after.TotalTokens != before.TotalTokens {
		t.Fatalf("清理后统计缩水(汇总没补上): before=%+v after=%+v", before, after)
	}
	if after.InputTokens != before.InputTokens || after.OutputTokens != before.OutputTokens {
		t.Fatalf("清理后词元分项缩水: before=%+v after=%+v", before, after)
	}
	// 汇总侧必须能说明"这段来自每日汇总", 否则用户看到金额却下钻不到明细。
	if after.BillingRollupCoveredUntil != oldDay.Format("2006-01-02") {
		t.Fatalf("汇总覆盖截止日 = %q, want %s", after.BillingRollupCoveredUntil, oldDay.Format("2006-01-02"))
	}

	// 曲线: 清理前后点数与金额一致 —— 曲线是用户最直观的"去年有没有变少"。
	afterTrend, err := UserStatsTrend(ctx, payer.ID, startStamp, endStamp, "day")
	if err != nil {
		t.Fatalf("trend after: %v", err)
	}
	if len(afterTrend) != len(beforeTrend) {
		t.Fatalf("清理前后趋势点数不同: before=%+v after=%+v", beforeTrend, afterTrend)
	}
	for i := range beforeTrend {
		if afterTrend[i].Date != beforeTrend[i].Date || afterTrend[i].Cost != beforeTrend[i].Cost || afterTrend[i].Requests != beforeTrend[i].Requests {
			t.Fatalf("清理前后趋势点不一致: before=%+v after=%+v", beforeTrend, afterTrend)
		}
	}

	// 周桶同理: 期间只有这一天有量, 周合计必须仍是 7 而不是 0。
	weekTrend, err := UserStatsTrend(ctx, payer.ID, "2000-01-01", endStamp, "week")
	if err != nil {
		t.Fatalf("week trend: %v", err)
	}
	var weekCost float64
	for _, point := range weekTrend {
		weekCost += point.Cost
	}
	if weekCost != 7 {
		t.Fatalf("周桶合计 = %v, want 7: %+v", weekCost, weekTrend)
	}
}

// TestUserStatsRollupDayRangeTrimsPartialDays 窗口首尾不是自然日边界时, 不能把整天的汇总算进来。
// 汇总是"一整天"的合计, 与半天的明细不是同一粒度: 窗口从某天正午开始时, 那天的汇总整行计入
// 会把窗口外的前半天凭空算进总额。
func TestUserStatsRollupDayRangeTrimsPartialDays(t *testing.T) {
	payer, _ := userStatsTestSetup(t)
	ctx := context.Background()
	day := dayStart(time.Now()).AddDate(0, 0, -10)

	// 该天上午的量已被清理, 只剩汇总; 下午的量是清理之后新产生的明细。
	userStatsSeedRollup(t, payer.ID, day, 2, 5, 40)
	userStatsInsert(t, userStatsRow(payer.ID, "m", model.BillingStatusSettled, day.Add(15*time.Hour), 3, 60))

	// 窗口 [该天正午, 次日 00:00): 只有下午的明细落在窗口内, 上午的汇总必须被排除。
	halfDay, err := userStatsBillingAggregate(ctx, payer.ID, day.Add(12*time.Hour), day.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("half-day aggregate: %v", err)
	}
	if halfDay.RequestCount != 1 || halfDay.UserCost != 3 || halfDay.InputToken != 60 {
		t.Fatalf("午间起点把整天的汇总算进来了: %+v", halfDay)
	}
	// 汇总侧整天范围在这种情况下必须为空: 没有任何一整天完全落在窗口内。
	fromKey, toKey := userStatsRollupDayRange(day.Add(12*time.Hour), day.AddDate(0, 0, 1))
	if fromKey != "" || toKey != "" {
		t.Fatalf("半天窗口不该有整天汇总区间: from=%q to=%q", fromKey, toKey)
	}

	// 窗口 [00:00, 次日 00:00) 才允许把汇总整天算上: 明细 + 汇总 = 整天的量。
	fullDay, err := userStatsBillingAggregate(ctx, payer.ID, day, day.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("full-day aggregate: %v", err)
	}
	if fullDay.RequestCount != 3 || fullDay.UserCost != 8 || fullDay.InputToken != 100 {
		t.Fatalf("整天窗口相加错误: %+v", fullDay)
	}
	// 整日边界: 起止都对齐 00:00 时, 该天必须落进汇总区间。
	if from, to := userStatsRollupDayRange(day, day.AddDate(0, 0, 1)); from != day.Format("20060102") || to != day.Format("20060102") {
		t.Fatalf("整天窗口的汇总区间 = (%q, %q), want (%s, %s)", from, to, day.Format("20060102"), day.Format("20060102"))
	}
}

// TestUserStatsRollupBucketsMatchSQLBuckets 汇总侧在 Go 侧归桶, 规则必须与 SQL 桶表达式一致。
// 桶对不上, 同一张图上"明细段"与"汇总段"就会按不同日期切片, 接缝处凭空缺一段或多一段。
func TestUserStatsRollupBucketsMatchSQLBuckets(t *testing.T) {
	// 2026-05-10 是周日: 当周周一为 05-04, 当月 1 日为 05-01。
	cases := []struct {
		day         string
		granularity string
		want        string
	}{
		{"20260510", "day", "2026-05-10"},
		{"20260510", "week", "2026-05-04"},
		{"20260510", "month", "2026-05-01"},
		{"20260504", "week", "2026-05-04"},
	}
	for _, tc := range cases {
		got, ok := userStatsBucketLabel(tc.day, tc.granularity)
		if !ok || got != tc.want {
			t.Fatalf("bucket label %s/%s = (%q, %v), want %q", tc.day, tc.granularity, got, ok, tc.want)
		}
	}
	if _, ok := userStatsBucketLabel("2026051", "day"); ok {
		t.Fatalf("日键长度非法时应跳过, 否则脏数据会被算成某个桶")
	}
}

// TestUserStatsTrendHourIgnoresRollups 小时粒度不并汇总: 汇总表按天存, 一天的聚合无法还原各小时分布,
// 硬并只会伪造出一个"所有量都堆在某小时"的桶。单日区间必然落在留存期内, 明细本就完整。
func TestUserStatsTrendHourIgnoresRollups(t *testing.T) {
	payer, _ := userStatsTestSetup(t)
	ctx := context.Background()
	day := dayStart(time.Now()).AddDate(0, 0, -10)
	userStatsSeedRollup(t, payer.ID, day, 2, 5, 40)

	stamp := day.Format("2006-01-02")
	hourly, err := UserStatsTrend(ctx, payer.ID, stamp, stamp, "hour")
	if err != nil {
		t.Fatalf("hourly trend: %v", err)
	}
	if len(hourly) != 0 {
		t.Fatalf("小时粒度不得由汇总伪造桶: %+v", hourly)
	}
	// 同一天按日粒度仍然能看到汇总补回来的量。
	daily, err := UserStatsTrend(ctx, payer.ID, stamp, stamp, "day")
	if err != nil {
		t.Fatalf("daily trend: %v", err)
	}
	if len(daily) != 1 || daily[0].Requests != 2 || daily[0].Cost != 5 {
		t.Fatalf("日粒度未并入汇总: %+v", daily)
	}
}

// TestUserStatsBillingCoverageReportsRollupBoundary 覆盖起点/汇总截止日必须如实返回:
// 统计页现在能显示留存期之外的金额(来自日汇总), 前端要靠这两条边界提示"哪段是汇总补的、不可下钻",
// 同时说明按模型拆分在留存期之外不可得。
func TestUserStatsBillingCoverageReportsRollupBoundary(t *testing.T) {
	payer, _ := userStatsTestSetup(t)
	ctx := context.Background()
	oldDay := dayStart(time.Now()).AddDate(0, 0, -400)
	recentDay := dayStart(time.Now()).AddDate(0, 0, -5)
	userStatsSeedRollup(t, payer.ID, oldDay, 1, 2, 10)
	userStatsInsert(t, userStatsRow(payer.ID, "m", model.BillingStatusSettled, recentDay.Add(9*time.Hour), 3, 20))

	startStamp := oldDay.AddDate(0, 0, -1).Format("2006-01-02")
	endStamp := recentDay.Format("2006-01-02")
	overview, err := UserStatsOverview(ctx, payer.ID, startStamp, endStamp)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if overview.TotalRequests != 2 || overview.TotalCost != 5 || overview.TotalTokens != 30 {
		t.Fatalf("汇总与明细应相加: %+v", overview)
	}
	if overview.BillingCoverageStart != oldDay.Format("2006-01-02") {
		t.Fatalf("覆盖起点 = %q, want %s(汇总比明细更早)", overview.BillingCoverageStart, oldDay.Format("2006-01-02"))
	}
	if overview.BillingRollupCoveredUntil != oldDay.Format("2006-01-02") {
		t.Fatalf("汇总截止日 = %q, want %s", overview.BillingRollupCoveredUntil, oldDay.Format("2006-01-02"))
	}
	// 按模型拆分只有明细能算: 覆盖起点就是区间内最早的一条明细。
	if overview.ModelBreakdownCoverageStart != recentDay.Format("2006-01-02") {
		t.Fatalf("模型拆分覆盖起点 = %q, want %s", overview.ModelBreakdownCoverageStart, recentDay.Format("2006-01-02"))
	}

	// 汇总在窗口之外(早于窗口起点)时不得计入, 也不该报告为覆盖。
	recentStamp := recentDay.Format("2006-01-02")
	outside, err := UserStatsOverview(ctx, payer.ID, recentStamp, recentStamp)
	if err != nil {
		t.Fatalf("overview outside: %v", err)
	}
	if outside.TotalRequests != 1 || outside.TotalCost != 3 || outside.BillingRollupCoveredUntil != "" {
		t.Fatalf("窗口外的汇总被算进来了: %+v", outside)
	}
}

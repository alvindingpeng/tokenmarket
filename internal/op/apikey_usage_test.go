package op

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// apiKeyUsageTestDB 独立库 + 清空进程级缓存。
//
// 换库必须先 resetProcessCaches: 缓存活在进程里, 不属于任何一个库, -shuffle=on 下顺序一变
// 就会读到上一个用例的旧库数据。收尾同理, 先清缓存再关库, 免得迟到的异步写落进已关闭的库。
func apiKeyUsageTestDB(t *testing.T) {
	t.Helper()
	NotificationDispatchDrain()
	AlertDispatchDrain()
	resetProcessCaches()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "apikey-usage.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		NotificationDispatchDrain()
		AlertDispatchDrain()
		resetProcessCaches()
		_ = db.Close()
	})
}

// apiKeyUsageSeed 造一条指定时间点的计费明细; createdAt 决定它落进哪个本地自然日桶。
func apiKeyUsageSeed(t *testing.T, apiKeyID int, createdAt time.Time, input, output int64, userCost float64, status string) model.BillingRecord {
	t.Helper()
	record := model.BillingRecord{
		UserID:          1,
		OwnerID:         2,
		ChannelID:       1,
		APIKeyID:        apiKeyID,
		ModelName:       "gpt-test",
		InputToken:      input,
		OutputToken:     output,
		UserCost:        userCost,
		OwnerRevenue:    userCost / 2,
		PlatformRevenue: userCost / 2,
		Status:          status,
		CreatedAt:       createdAt,
	}
	if err := db.GetDB().Create(&record).Error; err != nil {
		t.Fatalf("seed billing record: %v", err)
	}
	return record
}

// TestAPIKeyUsageReportFromLiveRecords 未归档窗口: 报表合计必须与逐行求和严格一致,
// 其它 Key 的行不能串进来。
func TestAPIKeyUsageReportFromLiveRecords(t *testing.T) {
	apiKeyUsageTestDB(t)
	ctx := context.Background()
	now := time.Now()
	localDay := dayStart(now)
	// 用自然日边界两侧的固定时刻，避免测试运行在 UTC 凌晨时 -3h/-26h 落入同一天。
	recent := localDay.Add(time.Nanosecond)
	previousDay := localDay.Add(-time.Nanosecond)
	apiKeyUsageSeed(t, 11, recent, 100, 50, 2, model.BillingStatusSettled)
	apiKeyUsageSeed(t, 11, previousDay, 200, 100, 4, model.BillingStatusSettled)
	apiKeyUsageSeed(t, 12, now.Add(-2*time.Hour), 999, 999, 99, model.BillingStatusSettled)

	report, err := APIKeyUsageReport(ctx, 11, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if report.Total.RequestCount != 2 {
		t.Fatalf("request count = %d, want 2", report.Total.RequestCount)
	}
	if report.Total.InputToken != 300 || report.Total.OutputToken != 150 {
		t.Fatalf("tokens = %d/%d, want 300/150", report.Total.InputToken, report.Total.OutputToken)
	}
	if diff := report.Total.UserCost - 6; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("user_cost = %v, want 6", report.Total.UserCost)
	}
	if report.Total.RefundedRecords != 0 || report.Total.RefundedUserCost != 0 {
		t.Fatalf("no refunded rows expected: %+v", report.Total)
	}
	// 逐日求和必须等于合计(证明按日桶聚合没丢没重)。
	var sum int64
	var sumCost float64
	for _, day := range report.Days {
		sum += day.RequestCount
		sumCost += day.UserCost
	}
	if sum != report.Total.RequestCount {
		t.Fatalf("day sum = %d, total = %d", sum, report.Total.RequestCount)
	}
	if diff := sumCost - report.Total.UserCost; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("day cost sum = %v, total = %v", sumCost, report.Total.UserCost)
	}
	if report.CoverageStart == "" || report.From == "" || report.To == "" {
		t.Fatalf("coverage window incomplete: %+v", report)
	}
	if len(report.Days) != 2 {
		t.Fatalf("days = %d, want 2 distinct local days", len(report.Days))
	}
	if report.From != report.CoverageStart {
		t.Fatalf("from = %s, want coverage start %s when from is unspecified", report.From, report.CoverageStart)
	}
}

// TestAPIKeyUsageReportSurvivesRetention 留存清理后合计不缩水: 证明 apikey 维度真的写进了汇总表,
// 且同一天多把 Key 各自成行, 不互相串味。
func TestAPIKeyUsageReportSurvivesRetention(t *testing.T) {
	apiKeyUsageTestDB(t)
	_ = chdirTemp(t)
	ctx := context.Background()
	if err := SettingUpsert(model.SettingKeyBillingRetentionDays, "180"); err != nil {
		t.Fatalf("set retention: %v", err)
	}
	if err := SettingUpsert(model.SettingKeyBillingArchiveEnabled, "false"); err != nil {
		t.Fatalf("set archive flag: %v", err)
	}
	old := time.Now().Add(-400 * 24 * time.Hour)
	// 同一天的两把 Key: 归档后各自成行, 互不串味。
	apiKeyUsageSeed(t, 21, old, 1000, 500, 10, model.BillingStatusSettled)
	apiKeyUsageSeed(t, 22, old.Add(time.Hour), 2000, 1000, 20, model.BillingStatusSettled)
	// 近期账留在明细里, 不该被删。
	apiKeyUsageSeed(t, 21, time.Now().Add(-24*time.Hour), 10, 5, 1, model.BillingStatusSettled)

	before, err := APIKeyUsageReport(ctx, 21, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("report before: %v", err)
	}
	if before.Total.RequestCount != 2 || before.Total.InputToken != 1010 {
		t.Fatalf("before retention: %+v", before.Total)
	}

	result, err := BillingRetentionRun(ctx)
	if err != nil {
		t.Fatalf("retention: %v", err)
	}
	if result.DeletedRecords != 2 {
		t.Fatalf("deleted = %d, want 2 (两条超期明细)", result.DeletedRecords)
	}

	after, err := APIKeyUsageReport(ctx, 21, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("report after: %v", err)
	}
	if after.Total.RequestCount != before.Total.RequestCount {
		t.Fatalf("request count shrank: before=%d after=%d (apikey 汇总没写进去)", before.Total.RequestCount, after.Total.RequestCount)
	}
	if after.Total.InputToken != before.Total.InputToken || after.Total.OutputToken != before.Total.OutputToken {
		t.Fatalf("tokens shrank: before=%d/%d after=%d/%d", before.Total.InputToken, before.Total.OutputToken,
			after.Total.InputToken, after.Total.OutputToken)
	}
	if diff := after.Total.UserCost - before.Total.UserCost; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("user cost changed: before=%v after=%v", before.Total.UserCost, after.Total.UserCost)
	}

	// 同日多 Key 不串味: 22 号 Key 的归档合计必须正好是它自己那一笔。
	other, err := APIKeyUsageReport(ctx, 22, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("report other key: %v", err)
	}
	if other.Total.RequestCount != 1 || other.Total.InputToken != 2000 {
		t.Fatalf("other key usage polluted: %+v", other.Total)
	}
}

// TestAPIKeyUsageReportRefundedRows 退款行: 词元与笔数照常计入("用量确实发生过"),
// 只有金额从实付里剔除并单独落在 refunded_user_cost / refunded_records。
func TestAPIKeyUsageReportRefundedRows(t *testing.T) {
	apiKeyUsageTestDB(t)
	ctx := context.Background()
	now := time.Now()
	apiKeyUsageSeed(t, 31, now.Add(-2*time.Hour), 100, 50, 3, model.BillingStatusSettled)
	apiKeyUsageSeed(t, 31, now.Add(-1*time.Hour), 200, 100, 6, model.BillingStatusRefunded)

	report, err := APIKeyUsageReport(ctx, 31, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if report.Total.RequestCount != 2 {
		t.Fatalf("request count = %d, want 2 (退款行仍是发生过的请求)", report.Total.RequestCount)
	}
	if report.Total.InputToken != 300 || report.Total.OutputToken != 150 {
		t.Fatalf("tokens = %d/%d, want 300/150 (退款不退流量)", report.Total.InputToken, report.Total.OutputToken)
	}
	if diff := report.Total.UserCost - 3; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("user_cost = %v, want 3 (只计未退费的 3)", report.Total.UserCost)
	}
	if report.Total.RefundedRecords != 1 {
		t.Fatalf("refunded records = %d, want 1", report.Total.RefundedRecords)
	}
	if diff := report.Total.RefundedUserCost - 6; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("refunded user cost = %v, want 6", report.Total.RefundedUserCost)
	}
}

// TestBillingRecordAPIKeyCreatedIndexExists 报表按 (api_key_id, created_at) 过滤与排序;
// 索引只写在 tag 里而不校验时, 拼错会静默退化成全表扫描, 故这里断言 AutoMigrate 真的建出了它。
func TestBillingRecordAPIKeyCreatedIndexExists(t *testing.T) {
	apiKeyUsageTestDB(t)
	if !db.GetDB().Migrator().HasIndex(&model.BillingRecord{}, "idx_billing_records_apikey_created") {
		t.Fatal("billing_records 缺少 (api_key_id, created_at) 复合索引")
	}
	// 新增索引声明不能顶掉既有的两条复合索引(tag 用分号并列, 少写一条就会静默丢失)。
	for _, name := range []string{"idx_billing_records_user_created", "idx_billing_records_channel_created"} {
		if !db.GetDB().Migrator().HasIndex(&model.BillingRecord{}, name) {
			t.Fatalf("billing_records 丢失既有复合索引 %s", name)
		}
	}
}

// TestAPIKeyUsageReportEmpty 空库: Days 必须是非 nil 空切片, CoverageStart 为空串,
// 且区间非法时明确报错而不是给一份全零报表。
func TestAPIKeyUsageReportEmpty(t *testing.T) {
	apiKeyUsageTestDB(t)
	ctx := context.Background()
	report, err := APIKeyUsageReport(ctx, 41, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if report.Days == nil {
		t.Fatal("Days must be a non-nil empty slice")
	}
	if len(report.Days) != 0 {
		t.Fatalf("days = %d, want 0", len(report.Days))
	}
	if report.CoverageStart != "" {
		t.Fatalf("coverage start = %q, want empty", report.CoverageStart)
	}
	if report.Total != (model.APIKeyUsageTotals{}) {
		t.Fatalf("total = %+v, want zero", report.Total)
	}
	if _, err := APIKeyUsageReport(ctx, 41, time.Now(), time.Now().Add(-time.Hour)); err == nil {
		t.Fatal("from after until must be rejected")
	}
}

// TestAPIKeyUsageReportWindowNarrowingKeepsCoverage 收窄 from 不能把 coverage_start 一起挪:
// 覆盖率是"这把 Key 最早可见的一天", 与本次查询窗口无关。窗口比 30 天短时 recent_from 也不能
// 跑到窗口外, 否则前端画的区间比 total 覆盖的范围还大。
func TestAPIKeyUsageReportWindowNarrowingKeepsCoverage(t *testing.T) {
	apiKeyUsageTestDB(t)
	ctx := context.Background()
	now := time.Now()
	apiKeyUsageSeed(t, 51, now.Add(-60*24*time.Hour), 100, 50, 2, model.BillingStatusSettled)
	apiKeyUsageSeed(t, 51, now.Add(-2*time.Hour), 10, 5, 1, model.BillingStatusSettled)

	from := now.Add(-48 * time.Hour)
	report, err := APIKeyUsageReport(ctx, 51, from, time.Time{})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	earliest := now.Add(-60 * 24 * time.Hour).In(time.Local).Format("2006-01-02")
	if report.CoverageStart != earliest {
		t.Fatalf("coverage start = %s, want %s (不应随窗口收窄)", report.CoverageStart, earliest)
	}
	if report.From != from.In(time.Local).Format("2006-01-02") {
		t.Fatalf("from = %s, want explicit window start", report.From)
	}
	// 窗口只有两天, 近 30 天必然覆盖整个窗口: recent 必须等于 total。
	if report.Recent.RequestCount != report.Total.RequestCount {
		t.Fatalf("recent=%d total=%d (窗口短于 30 天时应相等)", report.Recent.RequestCount, report.Total.RequestCount)
	}
	if report.RecentFrom != report.From {
		t.Fatalf("recent_from = %s, want clamped to window start %s", report.RecentFrom, report.From)
	}
	// 窗口外的旧账不能算进 total。
	if report.Total.RequestCount != 1 {
		t.Fatalf("total requests = %d, want 1 (窗口外的旧账不计)", report.Total.RequestCount)
	}
}

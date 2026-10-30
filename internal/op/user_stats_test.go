package op

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
)

// userStatsCST 固定 +08:00 测试时区: "周一凌晨/月末午夜" 用例在任意机器 TZ 下都能稳定
// 触发 SQLite date 函数的 UTC 归一化偏移(旧实现会把周一 00:30 归进上一周), 不依赖测试机时区。
var userStatsCST = time.FixedZone("+08:00", 8*3600)

// userStatsTestSetup 初始化隔离测试库并创建两个用户。
func userStatsTestSetup(t *testing.T) (payer, other model.User) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "user_stats.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	userCache.Clear()
	userNameIndex.Clear()
	payer = model.User{Username: "stats-payer", Password: "secret", Role: model.RoleUser, Status: model.StatusActive}
	if err := db.GetDB().Create(&payer).Error; err != nil {
		t.Fatalf("create payer: %v", err)
	}
	other = model.User{Username: "stats-other", Password: "secret", Role: model.RoleUser, Status: model.StatusActive}
	if err := db.GetDB().Create(&other).Error; err != nil {
		t.Fatalf("create other: %v", err)
	}
	return payer, other
}

// userStatsRow 构造一条计费行; at 的挂钟时间即业务时间。
func userStatsRow(userID uint, modelName string, status string, at time.Time, cost float64, tokens int64) model.BillingRecord {
	return model.BillingRecord{
		UserID:     userID,
		ModelName:  modelName,
		Status:     status,
		UserCost:   cost,
		InputToken: tokens,
		CreatedAt:  at,
	}
}

func userStatsInsert(t *testing.T, recs ...model.BillingRecord) {
	t.Helper()
	if err := db.GetDB().Create(&recs).Error; err != nil {
		t.Fatalf("insert billing rows: %v", err)
	}
}

// TestUserStatsUserIsolation 各统计接口只统计本人计费行, 不串其他用户。
func TestUserStatsUserIsolation(t *testing.T) {
	payer, other := userStatsTestSetup(t)
	at := time.Date(2026, 3, 2, 10, 0, 0, 0, time.Local)
	userStatsInsert(t,
		userStatsRow(payer.ID, "payer-model", model.BillingStatusSettled, at, 1.5, 10),
		userStatsRow(payer.ID, "payer-model", model.BillingStatusSettled, at.Add(time.Hour), 2.5, 20),
		userStatsRow(other.ID, "other-model", model.BillingStatusSettled, at, 9, 90),
		userStatsRow(other.ID, "other-model", model.BillingStatusSettled, at.Add(time.Hour), 9, 90),
		userStatsRow(other.ID, "other-model", model.BillingStatusSettled, at.Add(2*time.Hour), 9, 90),
	)
	ctx := context.Background()

	overview, err := UserStatsOverview(ctx, payer.ID, "2026-03-01", "2026-03-31")
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if overview.TotalRequests != 2 || overview.TotalCost != 4 || overview.TotalTokens != 30 {
		t.Fatalf("payer overview 串用户: %+v", overview)
	}
	otherOverview, err := UserStatsOverview(ctx, other.ID, "2026-03-01", "2026-03-31")
	if err != nil {
		t.Fatalf("other overview: %v", err)
	}
	if otherOverview.TotalRequests != 3 || otherOverview.TotalCost != 27 {
		t.Fatalf("other overview: %+v", otherOverview)
	}

	trend, err := UserStatsTrend(ctx, payer.ID, "2026-03-01", "2026-03-31", "day")
	if err != nil {
		t.Fatalf("trend: %v", err)
	}
	if len(trend) != 1 || trend[0].Date != "2026-03-02" || trend[0].Requests != 2 || trend[0].Cost != 4 {
		t.Fatalf("payer trend 串用户: %+v", trend)
	}

	models, err := UserStatsModelBreakdown(ctx, payer.ID, "2026-03-01", "2026-03-31")
	if err != nil {
		t.Fatalf("models: %v", err)
	}
	if len(models) != 1 || models[0].ModelName != "payer-model" || models[0].Requests != 2 {
		t.Fatalf("payer models 串用户: %+v", models)
	}
}

// TestUserStatsDateBoundaries 日期区间为 [start_date 00:00, end_date 次日 00:00), 边界精确。
func TestUserStatsDateBoundaries(t *testing.T) {
	payer, _ := userStatsTestSetup(t)
	day := func(d int, h, m, s int) time.Time {
		return time.Date(2026, 1, d, h, m, s, 0, time.Local)
	}
	userStatsInsert(t,
		userStatsRow(payer.ID, "m", model.BillingStatusSettled, day(1, 23, 59, 59), 1, 1), // 起点前一秒, 不计
		userStatsRow(payer.ID, "m", model.BillingStatusSettled, day(2, 0, 0, 0), 2, 2),    // 起点整点, 计入
		userStatsRow(payer.ID, "m", model.BillingStatusSettled, day(3, 23, 59, 59), 4, 4), // 末日最后一秒, 计入
		userStatsRow(payer.ID, "m", model.BillingStatusSettled, day(4, 0, 0, 0), 8, 8),    // 末日次日整点, 不计
	)
	ctx := context.Background()

	overview, err := UserStatsOverview(ctx, payer.ID, "2026-01-02", "2026-01-03")
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if overview.TotalRequests != 2 || overview.TotalCost != 6 || overview.TotalTokens != 6 {
		t.Fatalf("边界口径: %+v", overview)
	}

	trend, err := UserStatsTrend(ctx, payer.ID, "2026-01-02", "2026-01-03", "day")
	if err != nil {
		t.Fatalf("trend: %v", err)
	}
	if len(trend) != 2 || trend[0].Date != "2026-01-02" || trend[1].Date != "2026-01-03" ||
		trend[0].Requests != 1 || trend[1].Requests != 1 {
		t.Fatalf("边界日分组: %+v", trend)
	}

	// 单日查询: end_date 当天包含。
	one, err := UserStatsTrend(ctx, payer.ID, "2026-01-02", "2026-01-02", "day")
	if err != nil {
		t.Fatalf("single day: %v", err)
	}
	if len(one) != 1 || one[0].Date != "2026-01-02" || one[0].Requests != 1 {
		t.Fatalf("单日口径: %+v", one)
	}
}

// TestUserStatsEmptySet 无数据时返回零值概览与空序列, 不报错。
func TestUserStatsEmptySet(t *testing.T) {
	payer, _ := userStatsTestSetup(t)
	ctx := context.Background()

	overview, err := UserStatsOverview(ctx, payer.ID, "2026-01-01", "2026-01-31")
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if overview.TotalRequests != 0 || overview.TotalCost != 0 || overview.TotalTokens != 0 || overview.SuccessRate != 0 {
		t.Fatalf("空集概览: %+v", overview)
	}
	trend, err := UserStatsTrend(ctx, payer.ID, "2026-01-01", "2026-01-31", "day")
	if err != nil {
		t.Fatalf("trend: %v", err)
	}
	if len(trend) != 0 {
		t.Fatalf("空集趋势: %+v", trend)
	}
	models, err := UserStatsModelBreakdown(ctx, payer.ID, "2026-01-01", "2026-01-31")
	if err != nil {
		t.Fatalf("models: %v", err)
	}
	if len(models) != 0 {
		t.Fatalf("空集模型: %+v", models)
	}
}

// TestUserStatsRefundKeepsBillingScope 退费行保留原口径:
// 总量/费用/Token 含 refunded 行不剔除; 成功数仅 settled, SuccessRate 是 settled 计费行占比(并非请求成功率);
// 模型分组保持既有 status=settled 过滤不变。
func TestUserStatsRefundKeepsBillingScope(t *testing.T) {
	payer, _ := userStatsTestSetup(t)
	at := time.Date(2026, 4, 10, 9, 0, 0, 0, time.Local)
	userStatsInsert(t,
		userStatsRow(payer.ID, "m", model.BillingStatusSettled, at, 2, 11),
		userStatsRow(payer.ID, "m", model.BillingStatusRefunded, at.Add(time.Minute), 3, 5),
	)
	ctx := context.Background()

	overview, err := UserStatsOverview(ctx, payer.ID, "2026-04-01", "2026-04-30")
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if overview.TotalRequests != 2 || overview.TotalCost != 5 || overview.TotalTokens != 16 {
		t.Fatalf("refunded 被剔除或超范围: %+v", overview)
	}
	if overview.SuccessRate != 50 {
		t.Fatalf("成功率应为 settled 占比 50%%: %+v", overview)
	}

	trend, err := UserStatsTrend(ctx, payer.ID, "2026-04-01", "2026-04-30", "day")
	if err != nil {
		t.Fatalf("trend: %v", err)
	}
	if len(trend) != 1 || trend[0].Requests != 2 || trend[0].Cost != 5 || trend[0].SuccessRate != 50 {
		t.Fatalf("趋势退费口径: %+v", trend)
	}

	models, err := UserStatsModelBreakdown(ctx, payer.ID, "2026-04-01", "2026-04-30")
	if err != nil {
		t.Fatalf("models: %v", err)
	}
	if len(models) != 1 || models[0].Requests != 1 || models[0].Cost != 2 || models[0].Tokens != 11 {
		t.Fatalf("模型分组保持 settled 过滤: %+v", models)
	}
}

// TestUserStatsTrendWeekMondaySunday 周分组以周一为一周起点, 标签为当周周一:
// 周一 00:30(+08:00) 在 UTC 还是周日, 旧的 weekday 0,-6 days 会把它归到上一周("周一归到上周");
// 周日 23:30 应归入当周(周一开始)而不是下一周。
func TestUserStatsTrendWeekMondaySunday(t *testing.T) {
	payer, _ := userStatsTestSetup(t)
	userStatsInsert(t,
		// 上周日: 属于 2025-12-29 那一周(12-29 ~ 01-04)。
		userStatsRow(payer.ID, "m", model.BillingStatusSettled, time.Date(2026, 1, 4, 12, 0, 0, 0, userStatsCST), 1, 1),
		// 周一 00:30(+08:00): UTC 还是 2026-01-04(周日), 旧实现归到 2025-12-29 那一周。
		userStatsRow(payer.ID, "m", model.BillingStatusSettled, time.Date(2026, 1, 5, 0, 30, 0, 0, userStatsCST), 2, 2),
		// 周日 23:30: 应归入 2026-01-05 那一周(01-05 ~ 01-11)。
		userStatsRow(payer.ID, "m", model.BillingStatusSettled, time.Date(2026, 1, 11, 23, 30, 0, 0, userStatsCST), 4, 4),
	)
	trend, err := UserStatsTrend(context.Background(), payer.ID, "2025-12-29", "2026-01-11", "week")
	if err != nil {
		t.Fatalf("trend week: %v", err)
	}
	if len(trend) != 2 {
		t.Fatalf("周桶数: %+v", trend)
	}
	if trend[0].Date != "2025-12-29" || trend[0].Requests != 1 || trend[0].Cost != 1 {
		t.Fatalf("上周桶(上周日归上周一那周): %+v", trend[0])
	}
	if trend[1].Date != "2026-01-05" || trend[1].Requests != 2 || trend[1].Cost != 6 {
		t.Fatalf("本周桶(周一日出 + 周日夜归同周): %+v", trend[1])
	}
}

// TestUserStatsTrendMonthAndDay 日/月分组按写入挂钟时间:
// 2 月 1 日 00:30(+08:00) 在 UTC 还是 1 月 31 日, 旧实现会把日归 01-31、月归 2026-01。
func TestUserStatsTrendMonthAndDay(t *testing.T) {
	payer, _ := userStatsTestSetup(t)
	userStatsInsert(t,
		userStatsRow(payer.ID, "m", model.BillingStatusSettled, time.Date(2026, 1, 31, 23, 30, 0, 0, userStatsCST), 1, 1),
		userStatsRow(payer.ID, "m", model.BillingStatusSettled, time.Date(2026, 2, 1, 0, 30, 0, 0, userStatsCST), 2, 2),
	)
	ctx := context.Background()

	months, err := UserStatsTrend(ctx, payer.ID, "2026-01-31", "2026-02-01", "month")
	if err != nil {
		t.Fatalf("trend month: %v", err)
	}
	if len(months) != 2 || months[0].Date != "2026-01-01" || months[0].Requests != 1 ||
		months[1].Date != "2026-02-01" || months[1].Requests != 1 {
		t.Fatalf("月分组: %+v", months)
	}

	days, err := UserStatsTrend(ctx, payer.ID, "2026-01-31", "2026-02-01", "day")
	if err != nil {
		t.Fatalf("trend day: %v", err)
	}
	if len(days) != 2 || days[0].Date != "2026-01-31" || days[1].Date != "2026-02-01" ||
		days[0].Requests != 1 || days[1].Requests != 1 {
		t.Fatalf("日分组: %+v", days)
	}
}

// TestUserStatsTrendDayBusinessLocalDate SQLite 保存的 created_at 带 +08:00 等偏移时,
// DATE() 会先归一到 UTC 拿到前一天(如 07:30+08:00 → UTC 前一日 23:30、00:30+08:00 → UTC 前一日 16:30);
// 统计必须按业务本地日期聚合: 日桶与概览日期边界都取写入的本地日期, 而不是 UTC 日期。
func TestUserStatsTrendDayBusinessLocalDate(t *testing.T) {
	payer, _ := userStatsTestSetup(t)
	userStatsInsert(t,
		// 07:30+08:00 → UTC 前一日 23:30; 业务本地日期是 2026-03-05。
		userStatsRow(payer.ID, "m", model.BillingStatusSettled, time.Date(2026, 3, 5, 7, 30, 0, 0, userStatsCST), 1, 1),
		// 00:30+08:00 → UTC 前一日 16:30; 业务本地日期是 2026-03-06。
		userStatsRow(payer.ID, "m", model.BillingStatusSettled, time.Date(2026, 3, 6, 0, 30, 0, 0, userStatsCST), 2, 2),
	)
	ctx := context.Background()

	days, err := UserStatsTrend(ctx, payer.ID, "2026-03-05", "2026-03-06", "day")
	if err != nil {
		t.Fatalf("trend day: %v", err)
	}
	if len(days) != 2 || days[0].Date != "2026-03-05" || days[0].Requests != 1 ||
		days[1].Date != "2026-03-06" || days[1].Requests != 1 {
		t.Fatalf("日桶应按业务本地日期 2026-03-05/2026-03-06(而非 UTC 日期 03-04/03-05): %+v", days)
	}

	// 概览的日期区间边界同样按业务本地日期划界: 只取 2026-03-05 当天。
	overview, err := UserStatsOverview(ctx, payer.ID, "2026-03-05", "2026-03-05")
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if overview.TotalRequests != 1 || overview.TotalCost != 1 || overview.TotalTokens != 1 {
		t.Fatalf("概览应按业务本地日期划界(只含 07:30+08:00 那条): %+v", overview)
	}
}

// TestUserStatsBucketExprDialects 三种方言的日/周/月归桶表达式形态固定(周为周一起)。
func TestUserStatsBucketExprDialects(t *testing.T) {
	cases := []struct {
		dialect, granularity, want string
	}{
		{"sqlite", "day", "DATE(SUBSTR(created_at, 1, 10))"},
		{"sqlite", "week", "DATE(SUBSTR(created_at, 1, 10), '-6 days', 'weekday 1')"},
		{"sqlite", "month", "DATE(SUBSTR(created_at, 1, 10), 'start of month')"},
		{"mysql", "day", "DATE_FORMAT(created_at, '%Y-%m-%d')"},
		{"mysql", "week", "DATE_FORMAT(created_at - INTERVAL WEEKDAY(created_at) DAY, '%Y-%m-%d')"},
		{"mysql", "month", "DATE_FORMAT(created_at, '%Y-%m-01')"},
		{"postgres", "day", "TO_CHAR(DATE_TRUNC('day', created_at), 'YYYY-MM-DD')"},
		{"postgres", "week", "TO_CHAR(DATE_TRUNC('week', created_at), 'YYYY-MM-DD')"},
		{"postgres", "month", "TO_CHAR(DATE_TRUNC('month', created_at), 'YYYY-MM-DD')"},
	}
	for _, c := range cases {
		if got := statsBucketExpr(c.dialect, c.granularity); got != c.want {
			t.Fatalf("%s/%s: %q != %q", c.dialect, c.granularity, got, c.want)
		}
	}
	// 未知方言按 sqlite 兜底。
	if got := statsBucketExpr("unknown", "day"); got != cases[0].want {
		t.Fatalf("兜底: %q", got)
	}
}

// TestUserStatsCompositeIndexAndQueryPlan AutoMigrate 由 gorm tag 建出 (user_id, created_at) 复合索引,
// 原单列索引全部保留(不新增 owner 复合索引), 且用户统计范围聚合走该索引(EXPLAIN QUERY PLAN, SQLite)。
func TestUserStatsCompositeIndexAndQueryPlan(t *testing.T) {
	payer, _ := userStatsTestSetup(t)
	userStatsInsert(t,
		userStatsRow(payer.ID, "m", model.BillingStatusSettled, time.Date(2026, 5, 1, 10, 0, 0, 0, time.Local), 1, 1),
	)

	type idxRow struct {
		Name string
		SQL  string
	}
	var indexes []idxRow
	if err := db.GetDB().Raw("SELECT name, sql FROM sqlite_master WHERE type = 'index' AND tbl_name = 'billing_records'").Scan(&indexes).Error; err != nil {
		t.Fatalf("list indexes: %v", err)
	}
	byName := map[string]string{}
	for _, ix := range indexes {
		byName[ix.Name] = ix.SQL
	}

	// 复合索引: (user_id, created_at), 由 AutoMigrate 按 model gorm tag 自动创建。
	if sql, ok := byName["idx_billing_records_user_created"]; !ok {
		t.Fatalf("复合索引缺失: %+v", byName)
	} else {
		low := strings.ToLower(sql)
		if !strings.Contains(low, "user_id") || !strings.Contains(low, "created_at") ||
			strings.Index(low, "user_id") > strings.Index(low, "created_at") {
			t.Fatalf("复合索引列序错误: %s", sql)
		}
	}
	// 原单列索引保留。
	for _, name := range []string{
		"idx_billing_records_user_id",
		"idx_billing_records_created_at",
		"idx_billing_records_request_id",
		"idx_billing_records_owner_id",
	} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("原索引 %s 缺失: %+v", name, byName)
		}
	}

	// EXPLAIN QUERY PLAN: 概览聚合与趋势聚合都应以复合索引 SEARCH, 不做全表扫描。
	explain := func(name, sqlText string) string {
		t.Helper()
		var plan []struct {
			Detail string
		}
		if err := db.GetDB().Raw("EXPLAIN QUERY PLAN " + sqlText).Scan(&plan).Error; err != nil {
			t.Fatalf("%s explain: %v", name, err)
		}
		joined := ""
		for _, p := range plan {
			joined += p.Detail + "\n"
		}
		if !strings.Contains(joined, "idx_billing_records_user_created") {
			t.Fatalf("%s 未使用复合索引:\n%s", name, joined)
		}
		if !strings.Contains(joined, "SEARCH billing_records") {
			t.Fatalf("%s 不是索引 SEARCH(疑似全表扫描):\n%s", name, joined)
		}
		if strings.Contains(joined, "SCAN billing_records") {
			t.Fatalf("%s 仍含全表扫描:\n%s", name, joined)
		}
		return joined
	}

	start, end := time.Date(2026, 5, 1, 0, 0, 0, 0, time.Local), time.Date(2026, 5, 2, 0, 0, 0, 0, time.Local)
	overviewSQL := db.GetDB().ToSQL(func(tx *gorm.DB) *gorm.DB {
		var out struct {
			TotalRequests int64
		}
		return tx.Model(&model.BillingRecord{}).
			Select("COUNT(*) as total_requests, SUM(user_cost) as total_cost").
			Where("user_id = ? AND created_at >= ? AND created_at < ?", payer.ID, start, end).
			Scan(&out)
	})
	explain("overview", overviewSQL)

	trendSQL := db.GetDB().ToSQL(func(tx *gorm.DB) *gorm.DB {
		var out []struct {
			Date     string
			Requests int64
		}
		bucket := statsBucketExpr(statsDialect(), "day")
		return tx.Model(&model.BillingRecord{}).
			Select(bucket+" as date, COUNT(*) as requests").
			Where("user_id = ? AND created_at >= ? AND created_at < ?", payer.ID, start, end).
			Group(bucket).
			Order("date ASC").
			Scan(&out)
	})
	explain("trend", trendSQL)
}

// userStatsBenchBase 基准数据: 单用户大批量历史计费样本(对齐生产形态: 全部行属同一用户),
// 分布在 180 天里; 查询窗口取历史中段, 避免与插入边界重合。
type userStatsBenchBase struct {
	userID  uint
	baseDay time.Time
}

const (
	userStatsBenchRows     = 100000
	userStatsBenchDays     = 180
	userStatsBenchWindowAt = 90 // 窗口起点: 历史第 90 天(中段)
)

func userStatsBenchSetup(b *testing.B) userStatsBenchBase {
	b.Helper()
	if err := db.InitDB("sqlite", filepath.Join(b.TempDir(), "user_stats_bench.db"), false); err != nil {
		b.Fatalf("init db: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })
	userCache.Clear()
	userNameIndex.Clear()
	user := model.User{Username: "bench-user", Password: "secret", Role: model.RoleUser, Status: model.StatusActive}
	if err := db.GetDB().Create(&user).Error; err != nil {
		b.Fatalf("create user: %v", err)
	}

	baseDay := time.Date(2025, 6, 1, 0, 0, 0, 0, time.Local)
	perDay := userStatsBenchRows / userStatsBenchDays
	batch := make([]model.BillingRecord, 0, 1000)
	for day := 0; day < userStatsBenchDays; day++ {
		for i := 0; i < perDay; i++ {
			at := baseDay.AddDate(0, 0, day).Add(time.Duration(i) * time.Minute)
			status := model.BillingStatusSettled
			if i%10 == 9 {
				status = model.BillingStatusRefunded
			}
			batch = append(batch, model.BillingRecord{
				UserID:     user.ID,
				ModelName:  "bench-model",
				Status:     status,
				UserCost:   0.01,
				InputToken: 10,
				CreatedAt:  at,
			})
			if len(batch) == 1000 {
				if err := db.GetDB().Create(&batch).Error; err != nil {
					b.Fatalf("insert batch: %v", err)
				}
				batch = batch[:0]
			}
		}
	}
	if len(batch) > 0 {
		if err := db.GetDB().Create(&batch).Error; err != nil {
			b.Fatalf("insert tail: %v", err)
		}
	}
	return userStatsBenchBase{userID: user.ID, baseDay: baseDay}
}

func (s userStatsBenchBase) window(days int) (string, string) {
	start := s.baseDay.AddDate(0, 0, userStatsBenchWindowAt)
	end := start.AddDate(0, 0, days-1)
	return start.Format("2006-01-02"), end.Format("2006-01-02")
}

// BenchmarkUserStatsHistoricalWindows 对比旧单列索引计划与 (user_id, created_at) 复合索引:
// 生产事实是全部计费行属同一用户、30 天窗口覆盖全数据时 user_id 单列索引已足够快(毫秒级),
// 复合索引的收益在"历史多时间区间选择性范围"——窄窗口只触窗口内的行, 而单列计划无论窗口宽窄
// 都要扫完该用户的全部历史行再按 created_at 过滤。full_180d 用于如实展示全范围下两者相近。
func BenchmarkUserStatsHistoricalWindows(b *testing.B) {
	base := userStatsBenchSetup(b)
	ops := []struct {
		name string
		run  func(ctx context.Context, userID uint, start, end string) error
	}{
		{"overview", func(ctx context.Context, userID uint, start, end string) error {
			_, err := UserStatsOverview(ctx, userID, start, end)
			return err
		}},
		{"trend_day", func(ctx context.Context, userID uint, start, end string) error {
			_, err := UserStatsTrend(ctx, userID, start, end, "day")
			return err
		}},
	}
	windows := []struct {
		name  string
		days  int
		fullR bool
	}{
		{"window_1d", 1, false},
		{"window_7d", 7, false},
		{"window_30d", 30, false},
		{"full_180d", userStatsBenchDays, true},
	}
	modes := []struct {
		name      string
		composite bool
	}{
		{"composite_index", true},
		{"single_column_plan", false},
	}
	ctx := context.Background()
	for _, mode := range modes {
		b.Run(mode.name, func(b *testing.B) {
			if !mode.composite {
				if err := db.GetDB().Migrator().DropIndex(&model.BillingRecord{}, "idx_billing_records_user_created"); err != nil {
					b.Fatalf("drop composite index: %v", err)
				}
				b.Cleanup(func() {
					_ = db.GetDB().Migrator().CreateIndex(&model.BillingRecord{}, "idx_billing_records_user_created")
				})
			}
			for _, win := range windows {
				start, end := base.window(win.days)
				if win.fullR {
					start = base.baseDay.Format("2006-01-02")
					end = base.baseDay.AddDate(0, 0, win.days-1).Format("2006-01-02")
				}
				for _, op := range ops {
					b.Run(op.name+"/"+win.name, func(b *testing.B) {
						b.ReportAllocs()
						b.ResetTimer()
						for i := 0; i < b.N; i++ {
							if err := op.run(ctx, base.userID, start, end); err != nil {
								b.Fatal(err)
							}
						}
					})
				}
			}
		})
	}
}

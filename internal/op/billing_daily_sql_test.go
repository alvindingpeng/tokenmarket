package op

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// billingReferenceAggregate 是"旧实现"的逐行累加对照物: 把时间窗内的明细整行读回 Go,
// 按 record.CreatedAt.Format("20060102") 归日(月桶取 Format("200601")+"01")后累加。
// 它刻意不复用被测代码的任何聚合路径, 只共享同一组 Where 条件, 用来证明 SQL 聚合与
// 内存聚合逐桶等值。浮点只按 1e-9 容差比较(数据库 SUM 的求和顺序与 Go 逐行相加不同)。
func billingReferenceAggregate(t *testing.T, kind string, id uint, since time.Time, granularity string) []BillingAggregate {
	t.Helper()
	column := "user_id"
	if kind == "owner" {
		column = "owner_id"
	}
	records := []model.BillingRecord{}
	if err := db.GetDB().Where(column+" = ? AND created_at >= ?", id, since).
		Order("created_at").Find(&records).Error; err != nil {
		t.Fatalf("load reference records: %v", err)
	}
	byDate := map[string]*BillingAggregate{}
	order := make([]string, 0)
	for _, record := range records {
		date := record.CreatedAt.Format("20060102")
		if granularity == "month" {
			date = record.CreatedAt.Format("200601") + "01"
		}
		row, ok := byDate[date]
		if !ok {
			row = &BillingAggregate{Date: date}
			byDate[date] = row
			order = append(order, date)
		}
		row.InputToken += record.InputToken
		row.OutputToken += record.OutputToken
		row.CacheReadToken += record.CacheReadToken
		row.CacheWriteToken += record.CacheWriteToken
		row.RequestCount++
		row.UserCost += record.UserCost
		row.OwnerRevenue += record.OwnerRevenue
		row.PlatformRevenue += record.PlatformRevenue
	}
	rows := make([]BillingAggregate, 0, len(order))
	for _, date := range order {
		rows = append(rows, *byDate[date])
	}
	return rows
}

// billingAssertSameAggregates 逐桶逐字段比对 SQL 聚合与逐行累加: 整数字段必须完全相等,
// 金额按 1e-9 绝对值容差(实测差 ≤1.07e-14), 且日期集合、顺序、空切片语义一致。
func billingAssertSameAggregates(t *testing.T, label string, got, want []BillingAggregate) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s: 空结果必须返回非 nil 空切片, got nil", label)
	}
	if len(got) != len(want) {
		t.Fatalf("%s: 桶数 got %d want %d\n got=%+v\n want=%+v", label, len(got), len(want), got, want)
	}
	const epsilon = 1e-9
	for i := range want {
		g, w := got[i], want[i]
		if g.Date != w.Date {
			t.Fatalf("%s: 第 %d 个桶日期 got %q want %q (顺序必须按日期升序)", label, i, g.Date, w.Date)
		}
		if g.InputToken != w.InputToken || g.OutputToken != w.OutputToken ||
			g.CacheReadToken != w.CacheReadToken || g.CacheWriteToken != w.CacheWriteToken ||
			g.RequestCount != w.RequestCount {
			t.Fatalf("%s: %s 整数桶不一致\n got=%+v\n want=%+v", label, g.Date, g, w)
		}
		for name, pair := range map[string][2]float64{
			"user_cost":        {g.UserCost, w.UserCost},
			"owner_revenue":    {g.OwnerRevenue, w.OwnerRevenue},
			"platform_revenue": {g.PlatformRevenue, w.PlatformRevenue},
		} {
			if math.Abs(pair[0]-pair[1]) > epsilon {
				t.Fatalf("%s: %s 的 %s 不一致 got %v want %v", label, g.Date, name, pair[0], pair[1])
			}
		}
	}
}

// billingSeedCrossDayRecords 构造跨日/跨月、且带显式时区偏移的明细; 时间点刻意压在
// UTC 与本地日期的分界上, 用 UTC 归一日就会错位的那些样本。
func billingSeedCrossDayRecords(t *testing.T, payer, owner model.User) {
	t.Helper()
	cst := time.FixedZone("CST", 8*60*60)
	utc := time.UTC
	type sample struct {
		at       time.Time
		tokens   [4]int64
		user     float64
		ownerRv  float64
		platform float64
	}
	samples := []sample{
		// 本地 11-01 23:30(+08:00) = UTC 11-01 15:30: 两侧同为 11-01。
		{time.Date(2026, 11, 1, 23, 30, 0, 123456789, cst), [4]int64{100, 10, 5, 1}, 1.25, 0.75, 0.5},
		// 本地 11-02 00:30(+08:00) = UTC 11-01 16:30: 本地已是 11-02, 按 UTC 归日会错进 11-01。
		{time.Date(2026, 11, 2, 0, 30, 0, 987654321, cst), [4]int64{200, 20, 7, 3}, 2.5, 1.5, 1.0},
		// 本地 11-02 07:30(+08:00) = UTC 11-01 23:30: 同上, 是"UTC 简化"最典型的错位样本。
		{time.Date(2026, 11, 2, 7, 30, 0, 0, cst), [4]int64{300, 30, 9, 4}, 3.75, 2.25, 1.5},
		// 零偏移写入: 按它自己的挂钟日期归日(11-03), 不随部署机器时区漂移。
		{time.Date(2026, 11, 3, 5, 0, 0, 0, utc), [4]int64{400, 40, 11, 5}, 5.0, 3.0, 2.0},
		// 本地 11-30 23:59 = UTC 11-30 15:59, 与 12-01 00:00 只差一分钟: 跨月分界。
		{time.Date(2026, 11, 30, 23, 59, 0, 0, cst), [4]int64{500, 50, 13, 6}, 6.25, 3.75, 2.5},
		// 本地 12-01 00:00(+08:00) = UTC 11-30 16:00: 按 UTC 归月会错进 11 月。
		{time.Date(2026, 12, 1, 0, 0, 0, 0, cst), [4]int64{600, 60, 15, 7}, 7.5, 4.5, 3.0},
	}
	rows := make([]model.BillingRecord, 0, len(samples))
	for i, s := range samples {
		rows = append(rows, model.BillingRecord{
			RequestID:       uint64(1000 + i),
			UserID:          payer.ID,
			OwnerID:         owner.ID,
			ModelName:       "sql-agg-model",
			InputToken:      s.tokens[0],
			OutputToken:     s.tokens[1],
			CacheReadToken:  s.tokens[2],
			CacheWriteToken: s.tokens[3],
			UserCost:        s.user,
			OwnerRevenue:    s.ownerRv,
			PlatformRevenue: s.platform,
			Status:          model.BillingStatusSettled,
			CreatedAt:       s.at,
		})
	}
	// 另一个账户的同期明细: 聚合必须按 user_id / owner_id 隔离, 不能混进来。
	foreignPayer := model.User{Username: "sql-agg-foreign", Password: "secret", Role: model.RoleUser, Status: model.StatusActive}
	if err := db.GetDB().Create(&foreignPayer).Error; err != nil {
		t.Fatalf("create foreign payer: %v", err)
	}
	rows = append(rows, model.BillingRecord{
		RequestID:  9999,
		UserID:     foreignPayer.ID,
		OwnerID:    foreignPayer.ID,
		ModelName:  "sql-agg-model",
		InputToken: 999999,
		UserCost:   99.99,
		Status:     model.BillingStatusSettled,
		CreatedAt:  time.Date(2026, 11, 2, 1, 0, 0, 0, time.FixedZone("CST", 8*60*60)),
	})
	if err := db.GetDB().Create(&rows).Error; err != nil {
		t.Fatalf("seed billing records: %v", err)
	}
	// 落库后再回读, 用库里的 created_at(带写入偏移的时间串)确认种子确实按预期落库。
	var got []model.BillingRecord
	if err := db.GetDB().Where("request_id BETWEEN ? AND ?", 1000, 1005).Order("request_id").Find(&got).Error; err != nil {
		t.Fatalf("reload seeded records: %v", err)
	}
	for i, record := range got {
		if record.ID == 0 {
			t.Fatalf("seeded record %d not persisted", i)
		}
	}
}

// TestBillingDailySQLAggregationMatchesRowByRow 是 P6 的主用例: 跨日且带时区偏移的数据,
// 数据库侧 GROUP BY 的结果必须与逐行累加逐桶一致, 日期升序, 账户隔离。
func TestBillingDailySQLAggregationMatchesRowByRow(t *testing.T) {
	payer, owner := billingTestSetup(t, "sql-agg", 0)
	billingSeedCrossDayRecords(t, payer, owner)
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)

	for _, kind := range []string{"user", "owner"} {
		id := payer.ID
		if kind == "owner" {
			id = owner.ID
		}
		got, err := BillingDaily(context.Background(), kind, id, since)
		if err != nil {
			t.Fatalf("BillingDaily(%s): %v", kind, err)
		}
		want := billingReferenceAggregate(t, kind, id, since, "day")
		billingAssertSameAggregates(t, "BillingDaily/"+kind, got, want)

		// 日期必须是本地挂钟的日: 11-01 / 11-02 / 11-03 / 11-30 各一桶, 12-01 独立。
		seen := map[string]bool{}
		for _, row := range got {
			seen[row.Date] = true
		}
		for _, day := range []string{"20261101", "20261102", "20261103", "20261130", "20261201"} {
			if !seen[day] {
				t.Fatalf("BillingDaily/%s 缺少日桶 %s: %+v", kind, day, got)
			}
		}
		// 跨日样本: 本地 11-02 00:30(+08:00) 在 UTC 还是 11-01, 不能被并进 11-01。
		for _, row := range got {
			if row.Date == "20261101" && row.RequestCount != 1 {
				t.Fatalf("BillingDaily/%s: 11-01 桶被 UTC 归日污染, request_count=%d: %+v", kind, row.RequestCount, row)
			}
			if row.Date == "20261102" && row.RequestCount != 2 {
				t.Fatalf("BillingDaily/%s: 11-02 桶应有 2 行, request_count=%d: %+v", kind, row.RequestCount, row)
			}
		}
	}
}

// TestBillingMonthlySQLAggregationMatchesRowByRow 月桶必须与逐行累加一致, 且
// 12-01 00:00+08:00(UTC 仍是 11-30)不能被并进 11 月桶。
func TestBillingMonthlySQLAggregationMatchesRowByRow(t *testing.T) {
	payer, owner := billingTestSetup(t, "sql-agg-month", 0)
	billingSeedCrossDayRecords(t, payer, owner)
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)

	for _, kind := range []string{"user", "owner"} {
		id := payer.ID
		if kind == "owner" {
			id = owner.ID
		}
		got, err := BillingMonthly(context.Background(), kind, id, since)
		if err != nil {
			t.Fatalf("BillingMonthly(%s): %v", kind, err)
		}
		want := billingReferenceAggregate(t, kind, id, since, "month")
		billingAssertSameAggregates(t, "BillingMonthly/"+kind, got, want)
		if len(got) != 2 {
			t.Fatalf("BillingMonthly/%s: 应有 11 月与 12 月两个桶, got %+v", kind, got)
		}
		if got[0].Date != "20261101" || got[1].Date != "20261201" {
			t.Fatalf("BillingMonthly/%s: 月桶标签必须为当月 1 日且升序, got %q, %q", kind, got[0].Date, got[1].Date)
		}
		if got[0].RequestCount != 5 || got[1].RequestCount != 1 {
			t.Fatalf("BillingMonthly/%s: 跨月分界被 UTC 归一错了, counts=%d,%d", kind, got[0].RequestCount, got[1].RequestCount)
		}
	}
}

// TestBillingDailyEmptyResultIsEmptySlice 空窗口必须返回空切片而非 nil, 调用方按 len 判断。
func TestBillingDailyEmptyResultIsEmptySlice(t *testing.T) {
	payer, owner := billingTestSetup(t, "sql-agg-empty", 0)
	billingSeedCrossDayRecords(t, payer, owner)
	// 窗口完全落在种子数据的未来: 聚合零行, 但仍必须有 WHERE 之外的语义不变。
	future := time.Date(2027, 1, 1, 0, 0, 0, 0, time.Local)
	for _, tc := range []struct {
		name string
		run  func() ([]BillingAggregate, error)
	}{
		{"daily", func() ([]BillingAggregate, error) {
			return BillingDaily(context.Background(), "user", payer.ID, future)
		}},
		{"monthly", func() ([]BillingAggregate, error) {
			return BillingMonthly(context.Background(), "user", payer.ID, future)
		}},
	} {
		rows, err := tc.run()
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if rows == nil {
			t.Fatalf("%s: 空结果返回 nil, 应为空切片", tc.name)
		}
		if len(rows) != 0 {
			t.Fatalf("%s: 期望零行, got %+v", tc.name, rows)
		}
	}
}

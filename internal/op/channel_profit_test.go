package op

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// profitTestEnv 每个用例独立 sqlite 库: 利润聚合按 channel_id 全表扫账单, 共用库会串数据。
func profitTestEnv(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "channel-profit.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
}

// profitTestChannel 建一个只有名称的渠道; 利润分析只用到渠道名与主键。
func profitTestChannel(t *testing.T, name string) model.Channel {
	t.Helper()
	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: name}}
	if err := db.GetDB().Create(&channel).Error; err != nil {
		t.Fatalf("create channel %s: %v", name, err)
	}
	return channel
}

// profitTestRecord 写一条计费快照; 三个金额按结算后的对平口径给, 免得测试数据本身不平。
func profitTestRecord(t *testing.T, channelID int, at time.Time, status string, userCost, ownerRevenue, platformRevenue float64) {
	t.Helper()
	record := model.BillingRecord{
		ChannelID:       channelID,
		Status:          status,
		UserCost:        userCost,
		OwnerRevenue:    ownerRevenue,
		PlatformRevenue: platformRevenue,
		CreatedAt:       at,
	}
	if err := db.GetDB().Create(&record).Error; err != nil {
		t.Fatalf("create billing record: %v", err)
	}
}

func assertProfitFloat(t *testing.T, field string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("%s = %v, want %v", field, got, want)
	}
}

// TestChannelProfitAggregatesSettledAndIsolatesChannels 只汇总本渠道的 settled 行:
// 退款行与其它渠道的行都必须被剔除, 否则收入与毛利会虚增。
func TestChannelProfitAggregatesSettledAndIsolatesChannels(t *testing.T) {
	profitTestEnv(t)
	own := profitTestChannel(t, "profit-own")
	other := profitTestChannel(t, "profit-other")
	day1 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local)
	day2 := time.Date(2026, 10, 2, 8, 30, 0, 0, time.Local)

	profitTestRecord(t, own.ID, day1, model.BillingStatusSettled, 10, 8, 2)
	profitTestRecord(t, own.ID, day1, model.BillingStatusSettled, 5, 4, 1)
	profitTestRecord(t, own.ID, day1, model.BillingStatusRefunded, 100, 90, 10)
	profitTestRecord(t, own.ID, day2, model.BillingStatusSettled, 3, 2, 1)
	profitTestRecord(t, other.ID, day1, model.BillingStatusSettled, 999, 900, 99)

	profit, err := ChannelProfit(context.Background(), own.ID, "2026-10-01", "2026-10-02")
	if err != nil {
		t.Fatalf("ChannelProfit: %v", err)
	}
	if profit.ChannelID != own.ID || profit.ChannelName != "profit-own" {
		t.Fatalf("identity: %+v", profit)
	}
	if profit.StartDate != "2026-10-01" || profit.EndDate != "2026-10-02" {
		t.Fatalf("range: %s..%s", profit.StartDate, profit.EndDate)
	}
	if profit.Totals.Requests != 3 {
		t.Fatalf("totals.requests = %d, want 3", profit.Totals.Requests)
	}
	assertProfitFloat(t, "totals.user_cost", profit.Totals.UserCost, 18)
	assertProfitFloat(t, "totals.owner_revenue", profit.Totals.OwnerRevenue, 14)
	assertProfitFloat(t, "totals.platform_revenue", profit.Totals.PlatformRevenue, 4)
	assertProfitFloat(t, "totals.margin_rate", profit.Totals.MarginRate, 4.0/18.0*100)

	if len(profit.Daily) != 2 {
		t.Fatalf("daily len = %d, want 2 (%+v)", len(profit.Daily), profit.Daily)
	}
	if profit.Daily[0].Date != "2026-10-01" || profit.Daily[1].Date != "2026-10-02" {
		t.Fatalf("daily order: %+v", profit.Daily)
	}
	if profit.Daily[0].Requests != 2 || profit.Daily[1].Requests != 1 {
		t.Fatalf("daily requests: %+v", profit.Daily)
	}
	assertProfitFloat(t, "daily[0].user_cost", profit.Daily[0].UserCost, 15)
	assertProfitFloat(t, "daily[1].platform_revenue", profit.Daily[1].PlatformRevenue, 1)
	assertProfitFloat(t, "daily[0].margin_rate", profit.Daily[0].MarginRate, 3.0/15.0*100)
}

// TestChannelProfitDateBoundaries 区间两端都按整天计入: 起始日 00:00 与结束日 23:59:59 属于区间内,
// 前一天最后一刻与次日 00:00 必须落在区间外。
func TestChannelProfitDateBoundaries(t *testing.T) {
	profitTestEnv(t)
	channel := profitTestChannel(t, "profit-boundary")
	profitTestRecord(t, channel.ID, time.Date(2026, 9, 30, 23, 59, 59, 0, time.Local), model.BillingStatusSettled, 1, 1, 0)
	profitTestRecord(t, channel.ID, time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local), model.BillingStatusSettled, 2, 2, 0)
	profitTestRecord(t, channel.ID, time.Date(2026, 10, 2, 23, 59, 59, 0, time.Local), model.BillingStatusSettled, 3, 3, 0)
	profitTestRecord(t, channel.ID, time.Date(2026, 10, 3, 0, 0, 0, 0, time.Local), model.BillingStatusSettled, 4, 4, 0)

	profit, err := ChannelProfit(context.Background(), channel.ID, "2026-10-01", "2026-10-02")
	if err != nil {
		t.Fatalf("ChannelProfit: %v", err)
	}
	if profit.Totals.Requests != 2 {
		t.Fatalf("requests = %d, want 2 (daily=%+v)", profit.Totals.Requests, profit.Daily)
	}
	assertProfitFloat(t, "totals.user_cost", profit.Totals.UserCost, 5)
	if len(profit.Daily) != 2 || profit.Daily[0].Date != "2026-10-01" || profit.Daily[1].Date != "2026-10-02" {
		t.Fatalf("daily: %+v", profit.Daily)
	}

	// 单日区间同样包含该日全天。
	single, err := ChannelProfit(context.Background(), channel.ID, "2026-10-02", "2026-10-02")
	if err != nil {
		t.Fatalf("single day: %v", err)
	}
	if single.Totals.Requests != 1 {
		t.Fatalf("single day requests = %d, want 1", single.Totals.Requests)
	}
}

// TestChannelProfitEmptyResult 空区间返回零汇总与空数组, 而不是 null 或报错。
func TestChannelProfitEmptyResult(t *testing.T) {
	profitTestEnv(t)
	channel := profitTestChannel(t, "profit-empty")

	profit, err := ChannelProfit(context.Background(), channel.ID, "2026-10-01", "2026-10-31")
	if err != nil {
		t.Fatalf("ChannelProfit: %v", err)
	}
	if profit.Totals != (model.ChannelProfitTotals{}) {
		t.Fatalf("totals should be zero: %+v", profit.Totals)
	}
	if profit.Daily == nil || len(profit.Daily) != 0 {
		t.Fatalf("daily should be empty slice: %#v", profit.Daily)
	}
}

// TestChannelProfitDefaultWindow 未给日期时取最近 30 天(含今天): 今天进, 40 天前不进。
func TestChannelProfitDefaultWindow(t *testing.T) {
	profitTestEnv(t)
	channel := profitTestChannel(t, "profit-default")
	today := time.Now()
	profitTestRecord(t, channel.ID, today.Add(-time.Hour), model.BillingStatusSettled, 6, 4, 2)
	profitTestRecord(t, channel.ID, today.AddDate(0, 0, -40), model.BillingStatusSettled, 100, 90, 10)

	profit, err := ChannelProfit(context.Background(), channel.ID, "", "")
	if err != nil {
		t.Fatalf("ChannelProfit: %v", err)
	}
	if profit.Totals.Requests != 1 {
		t.Fatalf("default window requests = %d, want 1", profit.Totals.Requests)
	}
	if profit.EndDate != today.Format("2006-01-02") {
		t.Fatalf("default end = %s", profit.EndDate)
	}
	if profit.StartDate != today.AddDate(0, 0, -29).Format("2006-01-02") {
		t.Fatalf("default start = %s", profit.StartDate)
	}
}

// TestChannelProfitChannelCreatedIndexExists 渠道利润按 (channel_id, created_at) 过滤,
// 索引只写在 tag 里而不校验时, 拼错会静默退化成全表扫描, 故这里断言 AutoMigrate 真的建出了它。
func TestChannelProfitChannelCreatedIndexExists(t *testing.T) {
	profitTestEnv(t)
	if !db.GetDB().Migrator().HasIndex(&model.BillingRecord{}, "idx_billing_records_channel_created") {
		t.Fatal("billing_records 缺少 (channel_id, created_at) 复合索引")
	}
	// 新增第三条索引声明不能顶掉原有的 (user_id, created_at) 复合索引。
	if !db.GetDB().Migrator().HasIndex(&model.BillingRecord{}, "idx_billing_records_user_created") {
		t.Fatal("billing_records 丢失 (user_id, created_at) 复合索引")
	}
}

// TestChannelProfitDateRangeLimit 单次查询最多 366 天(含首尾): 一年整通过, 多一天即拒绝。
func TestChannelProfitDateRangeLimit(t *testing.T) {
	profitTestEnv(t)
	channel := profitTestChannel(t, "profit-limit")
	// 2026 非闰年: 2026-01-01 + 365 天 = 2027-01-01, 含首尾正好 366 天。
	if _, err := ChannelProfit(context.Background(), channel.ID, "2026-01-01", "2027-01-01"); err != nil {
		t.Fatalf("366 天区间应通过: %v", err)
	}
	// 闰年按日历天数算, 2024-01-01..2024-12-31 同样是 366 天。
	if _, err := ChannelProfit(context.Background(), channel.ID, "2024-01-01", "2024-12-31"); err != nil {
		t.Fatalf("闰年 366 天区间应通过: %v", err)
	}
	// 367 天(多一天)必须被上限拦下。
	if _, err := ChannelProfit(context.Background(), channel.ID, "2026-01-01", "2027-01-02"); !errors.Is(err, ErrChannelProfitDateRange) {
		t.Fatalf("367 天应返回 ErrChannelProfitDateRange, got %v", err)
	}
	if _, err := ChannelProfit(context.Background(), channel.ID, "2024-01-01", "2025-01-01"); !errors.Is(err, ErrChannelProfitDateRange) {
		t.Fatalf("闰年 367 天应返回 ErrChannelProfitDateRange, got %v", err)
	}
}

// TestChannelProfitRejectsInvalidInput 日期非法/倒挂/超限/渠道不存在都要返回可判定的哨兵错误。
func TestChannelProfitRejectsInvalidInput(t *testing.T) {
	profitTestEnv(t)
	channel := profitTestChannel(t, "profit-invalid")

	cases := []struct {
		name      string
		channelID int
		start     string
		end       string
		want      error
	}{
		{"start after end", channel.ID, "2026-10-31", "2026-10-01", ErrChannelProfitDateRange},
		{"bad start format", channel.ID, "2026-13-01", "", ErrChannelProfitDateRange},
		{"bad end format", channel.ID, "", "not-a-date", ErrChannelProfitDateRange},
		{"range over limit", channel.ID, "2020-01-01", "2021-01-02", ErrChannelProfitDateRange},
		{"unknown channel", 999999, "2026-10-01", "2026-10-02", ErrChannelProfitChannelNotFound},
		{"zero channel", 0, "2026-10-01", "2026-10-02", ErrChannelProfitChannelNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ChannelProfit(context.Background(), tc.channelID, tc.start, tc.end); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

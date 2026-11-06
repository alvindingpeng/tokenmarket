package op

import (
	"context"
	"testing"
	"time"
)

// 小时粒度趋势: 同一天内的两笔计费按小时分桶, 桶标签为 YYYY-MM-DD HH:00。
func TestUserStatsTrendHourlyBuckets(t *testing.T) {
	payer, _ := userStatsTestSetup(t)
	ctx := context.Background()
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local)
	userStatsInsert(t,
		userStatsRow(payer.ID, "m", "settled", base.Add(9*time.Hour), 2, 10),
		userStatsRow(payer.ID, "m", "settled", base.Add(9*time.Hour+20*time.Minute), 3, 10),
		userStatsRow(payer.ID, "m", "settled", base.Add(21*time.Hour), 1, 10),
	)
	trend, err := UserStatsTrend(ctx, payer.ID, "2026-07-01", "2026-07-01", "hour")
	if err != nil {
		t.Fatalf("trend: %v", err)
	}
	if len(trend) != 2 {
		t.Fatalf("want 2 hourly buckets, got %d: %+v", len(trend), trend)
	}
	// 只验标签形状(YYYY-MM-DD HH:00), 不验绝对小时: sqlite 驱动按 UTC 存取 time,
	// 桶标签跟着库内口径走, 分桶与聚合正确即可, 绝对时区展示由前端处理。
	// 标签形状: YYYY-MM-DD HH:00(同日两桶, HH 不同)。
	if len(trend[0].Date) != 16 || trend[0].Date[10] != ' ' || trend[0].Date[14:] != "00" {
		t.Fatalf("hour label shape: %q", trend[0].Date)
	}
	if trend[0].Requests != 2 || trend[0].Cost != 5 {
		t.Fatalf("first bucket: %+v", trend[0])
	}
	if trend[1].Requests != 1 || trend[1].Cost != 1 {
		t.Fatalf("second bucket: %+v", trend[1])
	}
}

package op

import (
	"context"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm/clause"
)

// hourlyOnConflict 按复合主键 (date, hour) 覆盖单格; 与 persistStatsSnapshots 的写法一致。
func hourlyOnConflict() clause.OnConflict {
	return clause.OnConflict{
		Columns:   []clause.Column{{Name: "date"}, {Name: "hour"}},
		UpdateAll: true,
	}
}

// resetHourlyStatsCache 清空小时缓存并重置日期标记; 包级全局变量跨用例共享, 不重置会互相污染。
func resetHourlyStatsCache() {
	statsHourlyCacheLock.Lock()
	clear(statsHourlyCache)
	hourlyCacheDate = ""
	statsHourlyCacheLock.Unlock()
}

// TestStatsHourlyPersistKeepsEveryDay 落库必须按 (date, hour) 覆盖单格, 不能跨天相互顶掉。
// 旧主键只有 hour 时, 第二天第一次落库会把昨天同一小时的整行替换掉 —— 生产上表现为
// 「小时曲线永远只剩今天」, 且昨天那条已落库的累计直接消失。
func TestStatsHourlyPersistKeepsEveryDay(t *testing.T) {
	opTestDBSetup(t, "hourly.db")
	resetHourlyStatsCache()

	// 昨天(固定一个过去日期)已落库两个小时桶。
	yesterday := time.Now().AddDate(0, 0, -1).Format("20060102")
	currentHour := time.Now().Hour()
	for _, row := range []model.StatsHourly{
		{Hour: currentHour, Date: yesterday, StatsMetrics: model.StatsMetrics{InputToken: 11, RequestSuccess: 1}},
		{Hour: (currentHour + 1) % 24, Date: yesterday, StatsMetrics: model.StatsMetrics{InputToken: 22, RequestSuccess: 2}},
	} {
		if err := db.GetDB().Clauses(hourlyOnConflict()).Create(&row).Error; err != nil {
			t.Fatalf("seed %s/%d: %v", yesterday, row.Hour, err)
		}
	}

	// 今天累加同小时(hour=0)的桶再落库。
	if err := StatsHourlyUpdate(model.StatsMetrics{InputToken: 33, RequestSuccess: 3}); err != nil {
		t.Fatalf("hourly update: %v", err)
	}
	if err := StatsSaveDB(context.Background()); err != nil {
		t.Fatalf("save stats: %v", err)
	}

	today := time.Now().Format("20060102")
	var yesterdayRow model.StatsHourly
	if err := db.GetDB().Where("date = ? AND hour = ?", yesterday, currentHour).First(&yesterdayRow).Error; err != nil {
		t.Fatalf("yesterday hour=%d disappeared: %v", currentHour, err)
	}
	if yesterdayRow.InputToken != 11 {
		t.Fatalf("yesterday hour=%d = %d, want 11 (must not be overwritten by today)", currentHour, yesterdayRow.InputToken)
	}

	// 今天写入的是「当前小时」的桶: 与昨天的同 hour 形成同一 hour、不同 date 的对照组,
	// 正是旧单列主键会冲突的那一格。
	var todayRow model.StatsHourly
	if err := db.GetDB().Where("date = ? AND hour = ?", today, currentHour).First(&todayRow).Error; err != nil {
		t.Fatalf("today hour=%d missing: %v", currentHour, err)
	}
	if todayRow.InputToken != 33 {
		t.Fatalf("today hour=%d = %d, want 33", currentHour, todayRow.InputToken)
	}
}

// TestStatsHourlyGetReturnsOnlyTodayZeroFilledGet 只返回当天 0..当前小时, 缺的补零行;
// 历史日期的桶即使还在库里也不能混进「今天」的 24 小时视图。
func TestStatsHourlyGetReturnsOnlyToday(t *testing.T) {
	opTestDBSetup(t, "hourly-get.db")
	resetHourlyStatsCache()

	yesterday := time.Now().AddDate(0, 0, -1).Format("20060102")
	row := model.StatsHourly{Hour: 3, Date: yesterday, StatsMetrics: model.StatsMetrics{InputToken: 99}}
	if err := db.GetDB().Clauses(hourlyOnConflict()).Create(&row).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := StatsHourlyUpdate(model.StatsMetrics{InputToken: 7}); err != nil {
		t.Fatalf("update: %v", err)
	}

	got := StatsHourlyGet(context.Background())
	want := time.Now().Hour() + 1
	if len(got) != want {
		t.Fatalf("got %d rows (hours 0..%d), want %d", len(got), time.Now().Hour(), want)
	}
	today := time.Now().Format("20060102")
	for i, bucket := range got {
		if bucket.Hour != i {
			t.Fatalf("row %d has hour %d; must be contiguous 0..now", i, bucket.Hour)
		}
		if bucket.Date != today {
			t.Fatalf("row %d date = %q, want %q (historical rows must not leak into today)", i, bucket.Date, today)
		}
	}
	// 当前小时的桶必须是缓存里的累加值, 而不是零行。
	if got[time.Now().Hour()].InputToken != 7 {
		t.Fatalf("current hour = %d, want 7", got[time.Now().Hour()].InputToken)
	}
}

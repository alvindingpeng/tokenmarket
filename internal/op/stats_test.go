package op

import (
	"math/rand"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/utils/log"
)

func TestStatsGenDaily(t *testing.T) {
	if err := db.InitDB("D:\\Code\\Github\\switchyard\\data\\data.db", false); err != nil {
		log.Errorf("database init error: %v", err)
		return
	}
	defer db.Close()

	// 先清空现有的测试数据，避免 UNIQUE constraint 错误
	if err := db.GetDB().Exec("DELETE FROM stats_dailies").Error; err != nil {
		log.Errorf("failed to clear existing data: %v", err)
		t.Fatalf("failed to clear existing data: %v", err)
	}

	// 生成30天的随机数据，从最早的日期开始到今天
	now := time.Now()
	var statsRecords []model.StatsDaily

	// 从29天前开始，到今天（i=0），按时间顺序生成
	for i := 465; i >= 0; i-- {
		// 计算日期：从29天前开始
		date := now.AddDate(0, 0, -i)
		// 将时间设置为当天的0点0分0秒
		date = time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())

		stats := model.StatsDaily{
			Date: date,
			StatsMetrics: model.StatsMetrics{
				InputToken:     rand.Int63n(1000000), // 0 到 1,000,000 之间的随机数
				OutputToken:    rand.Int63n(500000),  // 0 到 500,000 之间的随机数
				RequestSuccess: rand.Int63n(7500),    // 0 到 7,500 之间的随机数
				RequestFailed:  rand.Int63n(7500),    // 0 到 7,500 之间的随机数
				InputCost:      rand.Float64() * 100, // 0 到 100 之间的随机数
				OutputCost:     rand.Float64() * 100, // 0 到 100 之间的随机数
				WaitTime:       rand.Int63n(5000),    // 0 到 5,000 之间的随机数
			},
		}

		statsRecords = append(statsRecords, stats)
	}

	// 批量插入数据库
	if err := db.GetDB().Create(&statsRecords).Error; err != nil {
		log.Errorf("failed to insert stats data: %v", err)
		t.Fatalf("failed to insert stats data: %v", err)
	}

	log.Infof("成功生成并插入 %d 天的统计数据，从 %s 到 %s",
		len(statsRecords),
		statsRecords[0].Date.Format("2006-01-02"),
		statsRecords[len(statsRecords)-1].Date.Format("2006-01-02"))
	t.Logf("成功生成并插入 %d 天的统计数据，从 %s 到 %s",
		len(statsRecords),
		statsRecords[0].Date.Format("2006-01-02"),
		statsRecords[len(statsRecords)-1].Date.Format("2006-01-02"))
}

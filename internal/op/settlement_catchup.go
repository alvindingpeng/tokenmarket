package op

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm/clause"
)

var settlementCatchupMu sync.Mutex

// SettlementCatchupRun 补偿最近七个已结束的本地自然日(含昨天), 旧账期优先,
// 每轮最多七个。now 仅用于确定窗口, 与 SettlementGenerateDay 一样使用 time.Local。
// 首次部署会生成/刷新近七天的未审核单; 成功 checkpoint 持久化后不再自动刷新,
// 跨日和进程重启不依赖内存。没有来源数据的空日也可成功。
//
// 运维建议: 超过七天的历史失败/停机、成功后迟到数据须人工检查并手动生成指定账期,
// 不要为追赶历史扩大自动窗口。已审核差异仍由生成函数拒绝, 人工留调整凭证;
// 本任务不审核、不付款。checkpoint 失败时下轮安全地幂等重跑生成。
func SettlementCatchupRun(ctx context.Context, now time.Time) error {
	settlementCatchupMu.Lock()
	defer settlementCatchupMu.Unlock()
	return settlementCatchup(ctx, now, SettlementGenerateDay)
}

func settlementCatchup(ctx context.Context, now time.Time, generate func(context.Context, string) error) error {
	now = now.In(time.Local)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	first := today.AddDate(0, 0, -7)
	database := db.GetDB()
	if database == nil {
		return errors.New("settlement catchup: database unavailable")
	}
	var runs []model.SettlementRun
	if err := database.WithContext(ctx).Where("period >= ? AND period < ?", first.Format("20060102"), today.Format("20060102")).Find(&runs).Error; err != nil {
		// Fail closed: 没有可靠 checkpoint 视图, 不得盲目刷新已有成功日。
		return fmt.Errorf("load settlement checkpoints: %w", err)
	}
	succeeded := make(map[string]bool, len(runs))
	for _, run := range runs {
		succeeded[run.Period] = true
	}
	var errs []error
	for offset := 0; offset < 7; offset++ {
		period := first.AddDate(0, 0, offset).Format("20060102")
		if succeeded[period] {
			continue
		}
		if err := generate(ctx, period); err != nil {
			errs = append(errs, fmt.Errorf("generate settlement for %s: %w", period, err))
			continue
		}
		run := model.SettlementRun{Period: period, SucceededAt: time.Now()}
		if err := database.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&run).Error; err != nil {
			errs = append(errs, fmt.Errorf("checkpoint settlement for %s: %w", period, err))
		}
	}
	// 单账期失败不阻塞其他日期, 同时保留所有错误供任务失败告警。
	return errors.Join(errs...)
}

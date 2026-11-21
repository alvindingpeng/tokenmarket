package op

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// TestBillingRetentionSurvivesConcurrentWriter 留存固化的写事务与并发写者同时跑, 不许出现
// SQLITE_BUSY_SNAPSHOT(517)。
//
// 这是线上真实形态的回归: 单轮留存要按天固化汇总, 而"读当天明细 -> 写汇总 -> 删明细"是一个
// 先读后写的短事务。首条语句是 SELECT 只拿读快照, 之后的写要把它升级成写锁, 而 WAL 下这次
// 升级遇到并发提交就是 517 —— busy_timeout 对这条路径完全无效, 整轮只能失败。
// 实测触发: -shuffle=31337 时 TestProfitMarginNetSurvivesRetention 报
// "create billing rollup user/1/...: database is locked (517)"。
//
// 修法是在事务开头先取写锁(见 db.AcquireWriteLock), 并发方改为在写锁上按 busy_timeout 排队。
// 本用例钉住"并发写者存在时留存仍能跑完且汇总正确": 每次跑都要真的撞上并发, 否则它会退化成
// 一个恒绿的摆设 —— 因此并发写者必须在留存事务之前启动并在其提交前持续提交。
func TestBillingRetentionSurvivesConcurrentWriter(t *testing.T) {
	breakerTestSetup(t)
	_ = chdirTemp(t)
	withBillingSettings(t, 30, true)
	ctx := context.Background()

	// 三天前的一批超期明细: 让留存至少要处理一天(至少一个 sealAndDeleteBillingDay 事务)。
	for i := 0; i < 20; i++ {
		seedBillingRecord(t, 7, 8, 3, 40*24*time.Hour+time.Duration(i)*time.Minute, 10, 6)
	}

	before, err := BillingTotal(ctx, "user", 7)
	if err != nil {
		t.Fatalf("billing total before: %v", err)
	}

	// 并发写者: 持续提交"近期"计费明细。近期明细不会被留存删除, 所以它只制造写锁竞争,
	// 不改变留存该处理的超期行数。
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	writerErr := make(chan error, 1)
	go func() {
		defer wg.Done()
		writer := model.User{Username: "retention-writer", Role: model.RoleUser, Status: model.StatusActive}
		if err := db.GetDB().Create(&writer).Error; err != nil {
			writerErr <- fmt.Errorf("create writer user: %w", err)
			return
		}
		for {
			select {
			case <-stop:
				return
			default:
			}
			record := model.BillingRecord{
				UserID:          writer.ID,
				OwnerID:         8,
				ChannelID:       3,
				ModelName:       "gpt-test",
				InputToken:      1000,
				OutputToken:     500,
				UserCost:        1,
				OwnerRevenue:    0.5,
				PlatformRevenue: 0.5,
				Status:          model.BillingStatusSettled,
				CreatedAt:       time.Now(),
			}
			if err := db.GetDB().Create(&record).Error; err != nil {
				writerErr <- fmt.Errorf("concurrent create: %w", err)
				return
			}
		}
	}()

	result, runErr := BillingRetentionRun(ctx)
	close(stop)
	wg.Wait()
	select {
	case err := <-writerErr:
		t.Fatalf("concurrent writer failed: %v", err)
	default:
	}
	if runErr != nil {
		// 报错时把原文打出来: 517 与普通 BUSY 的区别直接体现在这句话上。
		t.Fatalf("BillingRetentionRun under concurrent writer: %v", runErr)
	}
	if result.DeletedRecords != 20 {
		t.Fatalf("deleted records = %d, want 20", result.DeletedRecords)
	}

	// 汇总必须把被删的金额补回来(留存的核心不变量在并发下同样成立)。
	after, err := BillingTotal(ctx, "user", 7)
	if err != nil {
		t.Fatalf("billing total after: %v", err)
	}
	if after.RequestCount != before.RequestCount || after.UserCost != before.UserCost {
		t.Fatalf("lifetime totals changed under concurrency: before=(%d,%v) after=(%d,%v)",
			before.RequestCount, before.UserCost, after.RequestCount, after.UserCost)
	}
}

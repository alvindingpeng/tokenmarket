package migrate

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{Version: 37, Up: Migration037_AlertRedeliverBackfillNullFix})
}

// Migration037_AlertRedeliverBackfillNullFix 修复 036 在生产上"命中 0 行"的问题。
//
// 事故经过: 036 的判定条件是 notify_attempts = 0。但 notify_attempts 这一列是
// 036 同批新增的, AutoMigrate 给存量行补列时填的是 NULL 而不是 0 —— 而 SQL 三值逻辑下
// "NULL = 0" 恒不成立。结果 036 在线上报告成功、却一条记录都没排进补发队列:
// 库里 260 条未送达告警全部漏掉, 补发机制的补救入口等于没打开。
// 这正是本次改造要消灭的那类静默失效, 只是换了个位置出现。
//
// 修复口径与 036 完全一致(只碰"从未尝试过、无排期、未送达"的行), 只是把 NULL 一并算作 0:
//   - 已有排期或已尝试过的行, 补发逻辑正在管, 不改写其退避节奏;
//   - 已送达的行(notified = true)完全不动。
//
// 幂等: 回填后 next_notify_at 非空, 重复执行命中 0 行。新装库上 036 已走 NULL 安全路径,
// 本迁移自然命中 0 行。
func Migration037_AlertRedeliverBackfillNullFix(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("channel_alerts") || !db.Migrator().HasColumn("channel_alerts", "next_notify_at") {
		return nil
	}
	// 第一步: 全表把 NULL 归零。存量行的该列是 AutoMigrate 补列时留下的 NULL;
	// 归零让库里从此不再有 NULL, 之后任何人写裸 "notify_attempts = 0" 也不会再静默漏行。
	// 已送达的行一并归零 —— 语义等价(从未重试即送达 = 0 轮), 只是让全表口径统一。
	if err := db.Table("channel_alerts").
		Where("notify_attempts IS NULL").
		Update("notify_attempts", 0).Error; err != nil {
		return fmt.Errorf("normalize null notify_attempts: %w", err)
	}
	// 第二步: 排期回填, 口径与 036 一致(只碰未送达 + 从未尝试 + 无排期 + 48 小时内的行)。
	// 归零之后 COALESCE 不再是必须, 但保留它作为防御 —— 将来若再有列被后加, 判定不回退。
	//
	// 48 小时窗口是线上处置定下的口径: 首次部署时无窗口, 260 条存量里 245 条是三周前的历史告警
	// (2026-10-09~10-11), 一旦补发就会向 Telegram 灌几百条失去上下文的旧告警。
	// 生产已按同一口径手动收窄(245 条撤销排期、15 条当日告警保留), 这里让迁移本身带上窗口,
	// 从更早的备份恢复时不会再重演。
	now := time.Now()
	result := db.Table("channel_alerts").
		Where("notified = ? AND next_notify_at IS NULL AND COALESCE(notify_attempts, 0) = ? AND created_at >= ?",
			false, 0, now.Add(-48*time.Hour)).
		Updates(map[string]any{
			"next_notify_at":    now,
			"last_notify_error": "历史告警: 改造前外发失败即终止, 未记录原因",
		})
	if result.Error != nil {
		return fmt.Errorf("backfill undelivered alerts (null fix): %w", result.Error)
	}
	return nil
}

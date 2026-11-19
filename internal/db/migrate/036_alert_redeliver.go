package migrate

import (
	"fmt"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{Version: 36, Up: Migration036_AlertRedeliverBackfill})
}

// Migration036_AlertRedeliverBackfill 把历史上"发出失败但从未重试"的告警纳入补发队列。
//
// 背景: 改造前 notified=false 是终点。2026-11-02 生产实测 260 条告警因 Telegram 实体解析
// 返回 400(can't parse entities)全部静默丢失 —— 记录还躺在库里, 但没有任何机制会再试一次,
// 管理员也不知道有告警没送达。
//
// 本次回填只做一件事: 给这些存量记录排出第一次补发时间(立即到点), 让新一轮的补发任务
// 把它们捞起来重发。判定条件刻意收窄到"从未尝试过"的行(notify_attempts = 0 且没有排期):
//   - 已有排期或已尝试过的行是本次改造之后产生的, 补发逻辑正在管, 回填不得改写其退避节奏;
//   - 已送达的行(notified = true)完全不动。
//
// 重发上限仍由补发逻辑统一控制, 因此这次回填不会让任何一条记录无限重试。
// 幂等: 更新后 notify_attempts 仍为 0 但 next_notify_at 非空, 故重复执行命中 0 行。
func Migration036_AlertRedeliverBackfill(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("channel_alerts") || !db.Migrator().HasColumn("channel_alerts", "next_notify_at") {
		return nil
	}
	now := time.Now()
	// 用 map 而不是结构体更新: 零值字段会被 GORM 跳过, 而这里正需要"保持 notify_attempts 为 0"。
	result := db.Table("channel_alerts").
		// COALESCE 兜底: 本迁移新增的 notify_attempts 列对存量行是 NULL 而不是 0,
		// 而 "notify_attempts = 0" 对 NULL 恒不成立 —— 直接写 0 会让本迁移命中 0 行,
		// 补发队静默漏掉全部历史告警(2026-11-02 线上实测确实如此)。
		// 时间窗: 只补最近 48 小时内的告警。线上存量里有 245 条是三周前的历史
		// (2026-10-09~10-11 集中爆发), 全量补发等于向 Telegram 灌几百条失去上下文的旧告警,
		// 把告警群刷成噪音 —— 2026-11-02 部署后按窗口收窄处置。超窗的行只归零计数、保留记录。
		Where("notified = ? AND next_notify_at IS NULL AND COALESCE(notify_attempts, 0) = ? AND created_at >= ?",
			false, 0, time.Now().Add(-48*time.Hour)).
		Updates(map[string]any{
			"next_notify_at":    now,
			"notify_attempts":   0, // 显式落 0: 顺手把 NULL 归一, 后续查询不必依赖 COALESCE。
			"last_notify_error": "历史告警: 改造前外发失败即终止, 未记录原因",
		})
	if result.Error != nil {
		return fmt.Errorf("backfill undelivered alerts: %w", result.Error)
	}
	_ = result.RowsAffected
	return nil
}

// 编译期断言: 迁移用到的模型字段确实存在, 改字段名时这里先报错。
var _ = model.ChannelAlert{}.NextNotifyAt

package migrate

import (
	"fmt"

	"github.com/charmbracelet/log"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{Version: 38, Up: Migration038_SettlementSnapshotPurge})
}

// Migration038_SettlementSnapshotPurge 清空已完成结算任务的 snapshot 列。
//
// 背景: billing_settlement_tasks 的每一行都带一份约 773 字节的经济快照 JSON, 线上已累积
// 8824 行且只增不减 —— 因为清理逻辑此前完全不存在。快照只在任务待执行时被读:
// 本仓库所有 json.Unmarshal(task.Snapshot) 前面都有"status 必须是 pending/retry"的状态门,
// 而 completed 意味着 billingSettleApply 已经成功、钱已经动完, 权威留痕在 billing_records
// 与 balance_ledgers。也就是说, 已完成任务行的快照是纯粹的存量死字节, 而且自动备份走
// VACUUM INTO 物理快照, 这些死字节会被每一份备份整份复制。
//
// 本迁移只处理**已经**是 completed 的存量行。新产生的 completed 行由
// op.billingSettlementTerminalFields 在置终态的同一个 UPDATE 里一并清空
// (不能分成两条语句, 否则会开出一个"已终态但快照还在"的中间态)。
//
// 不动 manual 行: 那张快照是人工对账的唯一证据。也不删任何行: BillingReleaseStale 的
// NOT EXISTS 依赖任务行存在, 删行会让"有结算意图"的预扣被当成可释放。
//
// 幂等: 第二次执行时 WHERE snapshot <> ” 命中 0 行。新装库没有存量行, 同样命中 0 行。
func Migration038_SettlementSnapshotPurge(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("billing_settlement_tasks") || !db.Migrator().HasColumn("billing_settlement_tasks", "snapshot") {
		return nil
	}
	result := db.Exec("UPDATE billing_settlement_tasks SET snapshot = '' WHERE status = ? AND snapshot <> ''",
		"completed")
	if result.Error != nil {
		return fmt.Errorf("purge completed settlement snapshots: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		log.Infof("migrate 38: purged snapshot from %d completed settlement tasks", result.RowsAffected)
	}
	return nil
}

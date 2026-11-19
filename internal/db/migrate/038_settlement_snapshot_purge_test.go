package migrate

import (
	"path/filepath"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// migrateTestDB 建一个只含本迁移所需表的最小库。
func migrateTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "migrate038.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := conn.AutoMigrate(&model.BillingSettlementTask{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	return conn
}

// seedTask 插入一条任务行。reservation_id 是唯一索引, 必须逐行不同。
func seedTask(t *testing.T, conn *gorm.DB, reservationID uint64, status, snapshot string) model.BillingSettlementTask {
	t.Helper()
	task := model.BillingSettlementTask{ReservationID: reservationID, Status: status, Snapshot: snapshot}
	if err := conn.Create(&task).Error; err != nil {
		t.Fatalf("seed %s: %v", status, err)
	}
	return task
}

// TestMigration038PurgesCompletedSnapshots 是本迁移的核心断言:
// completed 行的快照被清空, manual 行原样保留(那是人工对账的唯一证据)。
func TestMigration038PurgesCompletedSnapshots(t *testing.T) {
	conn := migrateTestDB(t)
	body := `{"user_cost":4,"request_id":7001}`

	completed := seedTask(t, conn, 1, model.BillingSettlementStatusCompleted, body)
	manual := seedTask(t, conn, 2, model.BillingSettlementStatusManual, body)
	pending := seedTask(t, conn, 3, model.BillingSettlementStatusPending, body)
	retry := seedTask(t, conn, 4, model.BillingSettlementStatusRetry, body)
	// 已清空的行: 幂等重跑时不该被当成命中行。
	empty := seedTask(t, conn, 5, model.BillingSettlementStatusCompleted, "")

	if err := Migration038_SettlementSnapshotPurge(conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	read := func(id uint64) string {
		var task model.BillingSettlementTask
		if err := conn.First(&task, id).Error; err != nil {
			t.Fatalf("read %d: %v", id, err)
		}
		return task.Snapshot
	}
	if got := read(completed.ID); got != "" {
		t.Errorf("completed 行的快照未被清空: %q", got)
	}
	for name, id := range map[string]uint64{"manual": manual.ID, "pending": pending.ID, "retry": retry.ID} {
		if got := read(id); got != body {
			t.Errorf("%s 行的快照被误清空: %q", name, got)
		}
	}
	if got := read(empty.ID); got != "" {
		t.Errorf("空快照行被改写: %q", got)
	}
	// 行本身一条都不能少: BillingReleaseStale 的 NOT EXISTS 依赖任务行存在。
	var count int64
	if err := conn.Model(&model.BillingSettlementTask{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Errorf("迁移删了行: 期望 5 行, 实际 %d 行", count)
	}
}

// TestMigration038IsIdempotent 第二次执行必须无可改行(迁移记录本身也会跳过, 但函数要自证幂等)。
func TestMigration038IsIdempotent(t *testing.T) {
	conn := migrateTestDB(t)
	seedTask(t, conn, 1, model.BillingSettlementStatusCompleted, `{"user_cost":4}`)

	if err := Migration038_SettlementSnapshotPurge(conn); err != nil {
		t.Fatalf("first run: %v", err)
	}
	result := conn.Exec("UPDATE billing_settlement_tasks SET snapshot = snapshot WHERE 0")
	_ = result
	if err := Migration038_SettlementSnapshotPurge(conn); err != nil {
		t.Fatalf("second run: %v", err)
	}
	var task model.BillingSettlementTask
	if err := conn.First(&task).Error; err != nil {
		t.Fatal(err)
	}
	if task.Snapshot != "" {
		t.Errorf("second run resurrected snapshot: %q", task.Snapshot)
	}
}

// TestMigration038TolerantOfMissingTable 老库/只读巡检没有该表时不能挡启动。
func TestMigration038TolerantOfMissingTable(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "empty.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := Migration038_SettlementSnapshotPurge(conn); err != nil {
		t.Fatalf("missing table should be tolerated: %v", err)
	}
}

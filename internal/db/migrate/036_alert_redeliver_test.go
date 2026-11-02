package migrate

import (
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// openAlertMigrateDB 建一个只含告警表的临时库。
func openAlertMigrateDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := conn.AutoMigrate(&model.ChannelAlert{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	return conn
}

// TestMigration036BackfillsUndeliveredAlerts 存量未送达告警必须被排进补发队列。
// 这是 2026-11-02 那 260 条静默丢失告警的补救入口: 只改从未尝试过的行。
func TestMigration036BackfillsUndeliveredAlerts(t *testing.T) {
	conn := openAlertMigrateDB(t)
	legacy := model.ChannelAlert{Kind: "down", Subject: "渠道A", Reason: "probe failed", CreatedAt: time.Now().Add(-1 * time.Hour)}
	if err := conn.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	// 已送达的不动。
	delivered := model.ChannelAlert{Kind: "recovered", Subject: "渠道B", Reason: "ok", Notified: true, CreatedAt: time.Now()}
	if err := conn.Create(&delivered).Error; err != nil {
		t.Fatal(err)
	}

	if err := Migration036_AlertRedeliverBackfill(conn); err != nil {
		t.Fatalf("migration: %v", err)
	}

	got := model.ChannelAlert{}
	if err := conn.Where("id = ?", legacy.ID).First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.NextNotifyAt == nil {
		t.Fatal("存量未送达告警应排出补发时间")
	}
	if got.Notified {
		t.Fatal("回填不得把未送达标记成已送达")
	}
	if got.LastNotifyError == "" {
		t.Fatal("回填应说明这些是历史未记录原因的告警")
	}

	kept := model.ChannelAlert{}
	if err := conn.Where("id = ?", delivered.ID).First(&kept).Error; err != nil {
		t.Fatal(err)
	}
	if kept.NextNotifyAt != nil {
		t.Fatal("已送达的告警不应被排进补发队列")
	}
}

// TestMigration036IsIdempotent 重复执行不得重置已有排期或改写退避节奏。
func TestMigration036IsIdempotent(t *testing.T) {
	conn := openAlertMigrateDB(t)
	// 一条本次改造之后产生、已尝试失败并排期的记录: 回填必须完全不动它。
	scheduled := time.Now().Add(37 * time.Minute)
	active := model.ChannelAlert{
		Kind: "down", Subject: "渠道C", Reason: "probe failed",
		NotifyAttempts: 2, NextNotifyAt: &scheduled, LastNotifyError: "telegram api 400",
		CreatedAt: time.Now(),
	}
	if err := conn.Create(&active).Error; err != nil {
		t.Fatal(err)
	}

	if err := Migration036_AlertRedeliverBackfill(conn); err != nil {
		t.Fatalf("migration: %v", err)
	}
	if err := Migration036_AlertRedeliverBackfill(conn); err != nil {
		t.Fatalf("migration second run: %v", err)
	}

	got := model.ChannelAlert{}
	if err := conn.Where("id = ?", active.ID).First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.NextNotifyAt == nil || !got.NextNotifyAt.Equal(scheduled) {
		t.Fatalf("已有排期被改写: %v (want %v)", got.NextNotifyAt, scheduled)
	}
	if got.LastNotifyError != "telegram api 400" {
		t.Fatalf("已有失败原因被覆盖: %q", got.LastNotifyError)
	}
	if got.NotifyAttempts != 2 {
		t.Fatalf("尝试次数被改写: %d", got.NotifyAttempts)
	}
}

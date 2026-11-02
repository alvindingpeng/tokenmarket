package op

import (
	"path/filepath"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// TestChannelAlertScanToleratesNullAttempts 实测: 存量行 notify_attempts 为 NULL 时,
// 全表扫描(SELECT *)扫进 Go int 是否报错。
// 这决定了读路径是否必须写 COALESCE —— 若这里报错, 任何一次列表查询都会 500。
func TestChannelAlertScanToleratesNullAttempts(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "nullscan.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	conn := db.GetDB()
	// 模拟存量行: 该列为 NULL。
	if err := conn.Exec("INSERT INTO channel_alerts (channel_id, kind, reason, notified, created_at, notify_attempts) VALUES (1, 'down', 'x', 0, datetime('now'), NULL)").Error; err != nil {
		t.Fatalf("insert: %v", err)
	}
	rows := []model.ChannelAlert{}
	if err := conn.Find(&rows).Error; err != nil {
		t.Fatalf("SELECT * 扫 NULL notify_attempts 报错: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	t.Logf("scan ok: attempts=%d", rows[0].NotifyAttempts)
}

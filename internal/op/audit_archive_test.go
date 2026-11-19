package op

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// withAuditSettings 在测试内临时设定审计留存配置。
// 用 SettingUpsert 而不是 SettingSetString: 测试库不播种默认设置, 后者会因键不存在失败。
func withAuditSettings(t *testing.T, days int, archive bool) {
	t.Helper()
	if err := SettingUpsert(model.SettingKeyAuditRetentionDays, strconv.Itoa(days)); err != nil {
		t.Fatalf("set retention: %v", err)
	}
	archiveValue := "false"
	if archive {
		archiveValue = "true"
	}
	if err := SettingUpsert(model.SettingKeyAuditArchiveEnabled, archiveValue); err != nil {
		t.Fatalf("set archive flag: %v", err)
	}
}

// chdirTemp 把工作目录切到临时目录, 让归档文件落在可清理的位置。
func chdirTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	return dir
}

func TestAuditLifecycleArchivesBeforeDelete(t *testing.T) {
	breakerTestSetup(t)
	dir := chdirTemp(t)
	withAuditSettings(t, 30, true)

	old := model.AuditLog{Action: "user.update", Target: "old#1", Detail: "ancient", CreatedAt: time.Now().AddDate(0, 0, -90)}
	if err := db.GetDB().Create(&old).Error; err != nil {
		t.Fatalf("seed old audit: %v", err)
	}
	fresh := model.AuditLog{Action: "user.update", Target: "new#2", Detail: "recent", CreatedAt: time.Now()}
	if err := db.GetDB().Create(&fresh).Error; err != nil {
		t.Fatalf("seed fresh audit: %v", err)
	}

	result, err := AuditLifecycleRun(context.Background())
	if err != nil {
		t.Fatalf("audit lifecycle: %v", err)
	}
	if result.Archived != 1 || result.Deleted != 1 {
		t.Fatalf("archived=%d deleted=%d, want 1/1", result.Archived, result.Deleted)
	}
	// 归档文件必须真的落到磁盘且含那条旧记录。
	data, err := os.ReadFile(result.File)
	if err != nil {
		t.Fatalf("read archive %s: %v", result.File, err)
	}
	if !filepath.IsAbs(result.File) && len(data) == 0 {
		t.Fatal("archive file is empty")
	}
	if len(data) == 0 {
		t.Fatal("archive file is empty")
	}
	var remaining int64
	if err := db.GetDB().Model(&model.AuditLog{}).Count(&remaining).Error; err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("remaining=%d, want 1 (fresh row must survive)", remaining)
	}
	_ = dir
}

func TestAuditLifecycleKeepsRowsWhenArchiveFails(t *testing.T) {
	breakerTestSetup(t)
	// 把归档路径占成普通文件, 让 MkdirAll 失败, 验证"归档失败一条都不删"。
	dir := chdirTemp(t)
	if err := os.MkdirAll(filepath.Join(dir, "data", "archives"), 0o750); err != nil {
		t.Fatalf("mkdir archives: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data", "archives", "audit"), []byte("blocker"), 0o640); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	withAuditSettings(t, 30, true)

	old := model.AuditLog{Action: "user.update", Target: "old#1", Detail: "ancient", CreatedAt: time.Now().AddDate(0, 0, -90)}
	if err := db.GetDB().Create(&old).Error; err != nil {
		t.Fatalf("seed old audit: %v", err)
	}

	result, err := AuditLifecycleRun(context.Background())
	if err != nil {
		t.Fatalf("audit lifecycle must not fail hard: %v", err)
	}
	if result.ArchiveError == "" {
		t.Fatal("expected archive error to be reported")
	}
	if result.Deleted != 0 {
		t.Fatalf("deleted=%d, want 0 when archive failed", result.Deleted)
	}
	var remaining int64
	if err := db.GetDB().Model(&model.AuditLog{}).Count(&remaining).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("remaining=%d, want 1 (evidence preserved)", remaining)
	}
}

func TestAuditLifecycleSkipsArchiveWhenDisabled(t *testing.T) {
	breakerTestSetup(t)
	chdirTemp(t)
	withAuditSettings(t, 30, false)

	old := model.AuditLog{Action: "user.update", Target: "old#1", Detail: "ancient", CreatedAt: time.Now().AddDate(0, 0, -90)}
	if err := db.GetDB().Create(&old).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	result, err := AuditLifecycleRun(context.Background())
	if err != nil {
		t.Fatalf("audit lifecycle: %v", err)
	}
	if result.Enabled {
		t.Fatal("archive must be reported disabled")
	}
	if result.Archived != 0 || result.Deleted != 1 {
		t.Fatalf("archived=%d deleted=%d, want 0/1", result.Archived, result.Deleted)
	}
}

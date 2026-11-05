package op

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// 本文件钉死归档文件留存清理的删除面与留存语义:
//   - 只删 relay-YYYYMMDD.ndjson 与 audit/audit-YYYYMMDD.ndjson; 手工文件/其他扩展名/目录永不删除;
//   - 按文件名里的日期判断新旧(归档是追加写, mtime 会被补写抬高, 不能当留存依据);
//   - 永远保留最近几份, 即便全部超期。

// archiveRetentionTestEnv 初始化隔离库、设定留存天数, 并把工作目录切到临时目录。
func archiveRetentionTestEnv(t *testing.T, days int) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "archive.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := SettingUpsert(model.SettingKeyArchiveRetentionDays, fmt.Sprintf("%d", days)); err != nil {
		t.Fatalf("set archive retention: %v", err)
	}
	t.Chdir(t.TempDir())
}

// archiveTestFile 在给定目录里造一个指定大小的文件, 并把 mtime 设成很久以前。
// mtime 故意与实际归档日期不一致: 用来证明清理只看文件名, 不看 mtime。
func archiveTestFile(t *testing.T, dir, name string, size int) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	ancient := time.Now().AddDate(-3, 0, 0)
	if err := os.Chtimes(path, ancient, ancient); err != nil {
		t.Fatalf("chtimes %s: %v", name, err)
	}
}

func archiveTestExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	return err == nil
}

// TestArchiveRetentionPrunesOnlyManagedFiles 超期的受管归档被删, 手工文件与子目录原样保留。
func TestArchiveRetentionPrunesOnlyManagedFiles(t *testing.T) {
	archiveRetentionTestEnv(t, 30)
	// day(n) 是 n 天前的归档日期; 数值越大越老。
	day := func(offsetDays int) string { return time.Now().AddDate(0, 0, -offsetDays).Format("20060102") }

	relayDir := ArchiveDir()
	auditDir := AuditArchiveDir()
	// 五个超期的受管文件: 保留最近三份, 最老的两份应被删。
	archiveTestFile(t, relayDir, "relay-"+day(100)+".ndjson", 10)
	archiveTestFile(t, relayDir, "relay-"+day(90)+".ndjson", 10)
	archiveTestFile(t, relayDir, "relay-"+day(80)+".ndjson", 10)
	archiveTestFile(t, relayDir, "relay-"+day(70)+".ndjson", 10)
	archiveTestFile(t, relayDir, "relay-"+day(60)+".ndjson", 10)
	// 非受管内容: 绝不删除。
	archiveTestFile(t, relayDir, "notes.txt", 10)
	archiveTestFile(t, relayDir, "backup-"+day(200)+".ndjson", 10)
	archiveTestFile(t, relayDir, "relay-manual.ndjson", 10)
	// 审计归档存在自己的子目录里: 同样按文件名判期, 但目录不参与 relay 目录的清理。
	archiveTestFile(t, auditDir, "audit-"+day(120)+".ndjson", 7)

	result, err := ArchiveRetentionRun(context.Background())
	if err != nil {
		t.Fatalf("ArchiveRetentionRun: %v", err)
	}
	// 审计目录只有一份文件, 落在"保留最近三份"的兜底里, 因此本轮只删 relay 的两份。
	if result.Pruned != 2 {
		t.Fatalf("expected 2 pruned archives, got %d (result=%+v)", result.Pruned, result)
	}
	for _, pruned := range []string{"relay-" + day(100) + ".ndjson", "relay-" + day(90) + ".ndjson"} {
		if archiveTestExists(t, filepath.Join(relayDir, pruned)) {
			t.Fatalf("archive past retention should be pruned: %s", pruned)
		}
	}
	for _, keep := range []string{"relay-" + day(80) + ".ndjson", "relay-" + day(70) + ".ndjson", "relay-" + day(60) + ".ndjson"} {
		if !archiveTestExists(t, filepath.Join(relayDir, keep)) {
			t.Fatalf("recent archive %s must be kept", keep)
		}
	}
	for _, untouched := range []string{"notes.txt", "backup-" + day(200) + ".ndjson", "relay-manual.ndjson"} {
		if !archiveTestExists(t, filepath.Join(relayDir, untouched)) {
			t.Fatalf("non-managed file %s must never be deleted", untouched)
		}
	}
	if !archiveTestExists(t, filepath.Join(auditDir, "audit-"+day(120)+".ndjson")) {
		t.Fatal("audit archive within the keep-minimum window must survive")
	}
}

// TestArchiveRetentionKeepsMinimumEvenIfAllExpired 全部超期时仍留最近几份, 避免清空历史。
func TestArchiveRetentionKeepsMinimumEvenIfAllExpired(t *testing.T) {
	archiveRetentionTestEnv(t, 1)
	day := func(offset int) string { return time.Now().AddDate(0, 0, -offset).Format("20060102") }
	for _, offset := range []int{10, 20, 30, 40} {
		archiveTestFile(t, ArchiveDir(), "relay-"+day(offset)+".ndjson", 5)
	}

	result, err := ArchiveRetentionRun(context.Background())
	if err != nil {
		t.Fatalf("ArchiveRetentionRun: %v", err)
	}
	if result.Pruned != 1 {
		t.Fatalf("expected only the oldest archive to go, got %d", result.Pruned)
	}
	if result.Kept != archiveRetentionKeepMinimum {
		t.Fatalf("expected %d kept archives, got %d", archiveRetentionKeepMinimum, result.Kept)
	}
}

// TestArchiveRetentionIgnoresModTime 用文件名日期判断: 补写过旧归档(mtime 变成今天)照样要被淘汰。
func TestArchiveRetentionIgnoresModTime(t *testing.T) {
	archiveRetentionTestEnv(t, 30)
	day := func(offset int) string { return time.Now().AddDate(0, 0, -offset).Format("20060102") }
	// 三个超期文件, 但其中一个的 mtime 被抬到今天: 按 mtime 判它会活下来, 按文件名判该删的是它。
	archiveTestFile(t, ArchiveDir(), "relay-"+day(200)+".ndjson", 5)
	archiveTestFile(t, ArchiveDir(), "relay-"+day(150)+".ndjson", 5)
	archiveTestFile(t, ArchiveDir(), "relay-"+day(100)+".ndjson", 5)
	touched := filepath.Join(ArchiveDir(), "relay-"+day(200)+".ndjson")
	now := time.Now()
	if err := os.Chtimes(touched, now, now); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	result, err := ArchiveRetentionRun(context.Background())
	if err != nil {
		t.Fatalf("ArchiveRetentionRun: %v", err)
	}
	if result.Pruned != 0 {
		t.Fatalf("all three are within the keep-minimum window, nothing should go: %+v", result)
	}
	if err := SettingUpsert(model.SettingKeyArchiveRetentionDays, "30"); err != nil {
		t.Fatal(err)
	}
	// 再加两份更新的, 凑够让"最近 3 份"之外的那份被淘汰。
	archiveTestFile(t, ArchiveDir(), "relay-"+day(10)+".ndjson", 5)
	archiveTestFile(t, ArchiveDir(), "relay-"+day(5)+".ndjson", 5)
	result, err = ArchiveRetentionRun(context.Background())
	if err != nil {
		t.Fatalf("ArchiveRetentionRun: %v", err)
	}
	if result.Pruned != 2 {
		t.Fatalf("expected the two oldest by name to be pruned, got %d", result.Pruned)
	}
	if archiveTestExists(t, touched) {
		t.Fatalf("archive past retention must be pruned regardless of its mtime: %s", touched)
	}
}

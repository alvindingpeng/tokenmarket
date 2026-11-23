package op

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// 本文件钉死备份轮转的删除面: 只认服务自己生成的 backup-YYYYMMDD-HHMMSS.db(.enc)。
// 备份目录里可能还有运维手工放进来的其他 .db(导入中间文件、恢复前手工快照),
// 按扩展名删会把它们当"旧备份"一起清掉 —— 那是别人的数据, 不是本服务的轮转对象。

// backupTestArtifact 在备份目录里造一个名字给定、时间可排序的文件。
func backupTestArtifact(t *testing.T, dir, name string, age time.Duration) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	stamp := time.Now().Add(-age)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatalf("chtimes %s: %v", name, err)
	}
}

func TestIsBackupArtifactNarrowNames(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"backup-20260101-000000.db", true},
		{"backup-20260101-000000.db.enc", true},
		{"backup-20260101-000000.db.sha256", false}, // 校验和跟主体走, 不单独计数
		{"manual-restore.db", false},                // 运维手工文件不能被轮转
		{"backup.db", false},                        // 无时间戳, 不是本服务产物
		{"backup-latest.db", false},
		{"data.db", false},
		{"backup-20260101-000000.txt", false},
	}
	for _, tc := range cases {
		if got := isBackupArtifact(tc.name); got != tc.want {
			t.Errorf("isBackupArtifact(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestPruneBackupsKeepsOnlyManagedArtifacts 手工文件不占保留名额, 也不会被删。
func TestPruneBackupsKeepsOnlyManagedArtifacts(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	backupDir := BackupDir()
	if err := os.MkdirAll(backupDir, 0o750); err != nil {
		t.Fatal(err)
	}
	backupTestArtifact(t, backupDir, "backup-20260101-000000.db", 3*time.Hour)
	backupTestArtifact(t, backupDir, "backup-20260102-000000.db", 2*time.Hour)
	backupTestArtifact(t, backupDir, "backup-20260103-000000.db", time.Hour)
	backupTestArtifact(t, backupDir, "manual-restore.db", 10*time.Hour)

	if pruned := pruneBackups(2); pruned != 1 {
		t.Fatalf("expected exactly one pruned backup, got %d", pruned)
	}
	if _, err := os.Stat(filepath.Join(backupDir, "backup-20260101-000000.db")); !os.IsNotExist(err) {
		t.Fatalf("oldest managed backup should be pruned, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(backupDir, "manual-restore.db")); err != nil {
		t.Fatalf("manual file must survive rotation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(backupDir, "backup-20260103-000000.db")); err != nil {
		t.Fatalf("newest managed backup must survive rotation: %v", err)
	}
}

func TestAutoBackupKeepsLastValidSnapshotOnVerificationFailure(t *testing.T) {
	t.Chdir(t.TempDir())
	opTestDBSetup(t, "source.db")
	for key, value := range map[model.SettingKey]string{
		model.SettingKeyAutoBackupEnabled: "true",
		model.SettingKeyAutoBackupKeep:    "1",
	} {
		if err := SettingUpsert(key, value); err != nil {
			t.Fatal(err)
		}
	}

	valid, err := AutoBackupRun()
	if err != nil || !valid.Verified || !VerifyBackupChecksum(valid.File) {
		t.Fatalf("expected a verified backup with checksum, got %+v, %v", valid, err)
	}
	if err := db.GetDB().Migrator().DropTable(&model.User{}); err != nil {
		t.Fatal(err)
	}
	if result, err := AutoBackupRun(); err == nil || result.Pruned != 0 || result.File != "" {
		t.Fatalf("invalid snapshot should fail before publication and rotation: %+v, %v", result, err)
	}
	if !VerifyBackupChecksum(valid.File) {
		t.Fatal("previous recovery point or checksum was lost")
	}
	files, err := os.ReadDir(BackupDir())
	if err != nil || len(files) != 2 {
		t.Fatalf("expected only the previous snapshot and checksum, got %v, %v", files, err)
	}
}

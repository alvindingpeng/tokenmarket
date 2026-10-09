package op

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// BackupDir 返回自动备份目录。
func BackupDir() string { return filepath.Join("data", "backups") }

// AutoBackupResult 一次自动备份的结果。
type AutoBackupResult struct {
	File      string `json:"file"`
	Size      int64  `json:"size"`
	Verified  bool   `json:"verified"`
	Pruned    int    `json:"pruned"`
	Encrypted bool   `json:"encrypted"`
	Checksum  string `json:"checksum"`
	Remote    string `json:"remote"` // ok | skipped | failed
}

// AutoBackupRun 一致性备份 SQLite 数据库并校验, 保留固定份数。
// 对 SQLite 使用 VACUUM INTO 生成独立快照文件, 避免 WAL 带来的不一致。
// 对 MySQL/PostgreSQL 仅记录状态: 由 DBA 做离线备份, 不在进程内导出全量数据。
func AutoBackupRun() (AutoBackupResult, error) {
	if enabled, _ := SettingGetBool(model.SettingKeyAutoBackupEnabled); !enabled {
		return AutoBackupResult{}, nil
	}
	if err := os.MkdirAll(BackupDir(), 0o750); err != nil {
		return AutoBackupResult{}, fmt.Errorf("create backup dir: %w", err)
	}
	name := "backup-" + time.Now().Format("20060102-150405") + ".db"
	path := filepath.Join(BackupDir(), name)

	// VACUUM INTO 需要在主库连接上执行, 生成的文件是完整一致快照。
	sqlDB, err := db.GetDB().DB()
	if err != nil {
		return AutoBackupResult{}, fmt.Errorf("get sql db: %w", err)
	}
	if _, err := sqlDB.Exec(fmt.Sprintf("VACUUM INTO '%s'", path)); err != nil {
		return AutoBackupResult{}, fmt.Errorf("vacuum into backup: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return AutoBackupResult{}, fmt.Errorf("stat backup: %w", err)
	}
	result := AutoBackupResult{File: path, Size: info.Size(), Remote: "skipped"}

	// 校验备份可打开并含关键表。
	result.Verified = verifyBackup(path)

	// 可选加密: 口令来自 config/env(见 backupcrypto.go), 缺口令视为配置错误, 备份整体判失败。
	artifact := path
	if encrypt, _ := SettingGetBool(model.SettingKeyBackupEncrypt); encrypt {
		passphrase := strings.TrimSpace(conf.AppConfig.Backup.Passphrase)
		if passphrase == "" {
			return result, fmt.Errorf("backup encryption enabled but backup.passphrase is not configured")
		}
		encPath := path + ".enc"
		if err := EncryptBackupFile(path, encPath, passphrase); err != nil {
			return result, fmt.Errorf("encrypt backup: %w", err)
		}
		if err := os.Remove(path); err != nil {
			return result, fmt.Errorf("remove plaintext backup: %w", err)
		}
		artifact = encPath
		result.Encrypted = true
		result.File = artifact
		result.Size = 0
		if encInfo, err := os.Stat(artifact); err == nil {
			result.Size = encInfo.Size()
		}
	}

	// 校验和必写: 恢复前先验后用, 异地取回的备份也以此验真。
	if err := WriteBackupChecksum(artifact); err != nil {
		return result, err
	}
	result.Checksum = filepath.Base(BackupChecksumFile(artifact))

	// 异地推送: 本地备份已成功, 推送失败不判整体失败, 记告警进闭环。
	if remote, _ := SettingGetBool(model.SettingKeyBackupRemote); remote {
		if !BackupRemoteConfigured() {
			result.Remote = "failed"
			RaiseAlert(context.Background(), 0, "system", "backup_failed", "backup remote push skipped: backup.remote_url is not configured")
		} else if err := PushBackupRemote(artifact, BackupChecksumFile(artifact)); err != nil {
			result.Remote = "failed"
			RaiseAlert(context.Background(), 0, "system", "backup_failed", "backup remote push failed: "+err.Error())
		} else {
			result.Remote = "ok"
		}
	}

	// 保留策略: 保留最近 N 份, 多余删除。
	keep, err := SettingGetInt(model.SettingKeyAutoBackupKeep)
	if err != nil || keep < 1 {
		keep = 7
	}
	result.Pruned = pruneBackups(keep)
	return result, nil
}

// verifyBackup 打开备份文件并检查关键表存在, 返回是否通过。
func verifyBackup(path string) bool {
	verify, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		return false
	}
	sqlDB, err := verify.DB()
	if err != nil {
		return false
	}
	defer sqlDB.Close()
	var count int64
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='users'").Scan(&count); err != nil || count < 1 {
		return false
	}
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='relay_requests'").Scan(&count); err != nil || count < 1 {
		return false
	}
	return true
}

// pruneBackups 删除超出保留份数的旧备份, 返回删除数。
func pruneBackups(keep int) int {
	entries, err := os.ReadDir(BackupDir())
	if err != nil {
		return 0
	}
	type fileEntry struct {
		name    string
		modTime time.Time
	}
	files := make([]fileEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !isBackupArtifact(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, fileEntry{name: entry.Name(), modTime: info.ModTime()})
	}
	if len(files) <= keep {
		return 0
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modTime.After(files[j].modTime) })
	pruned := 0
	for _, file := range files[keep:] {
		full := filepath.Join(BackupDir(), file.name)
		if err := os.Remove(full); err == nil {
			pruned++
			_ = os.Remove(BackupChecksumFile(full)) // 校验和与备份同生共死。
		}
	}
	return pruned
}

// isBackupArtifact 判断是否为备份产物(明文 .db 或密文 .db.enc), .sha256 旁路文件不算。
func isBackupArtifact(name string) bool {
	if strings.HasSuffix(name, ".enc") {
		return strings.HasSuffix(strings.TrimSuffix(name, ".enc"), ".db")
	}
	return filepath.Ext(name) == ".db"
}

// BackupVerifySQLite 导出版校验: 打开备份并确认关键表存在。
func BackupVerifySQLite(path string) bool { return verifyBackup(path) }

// BackupList 返回备份目录中已有备份, 新的在前。
func BackupList() ([]map[string]any, error) {
	entries, err := os.ReadDir(BackupDir())
	if err != nil {
		if os.IsNotExist(err) {
			return []map[string]any{}, nil
		}
		return nil, err
	}
	files := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !isBackupArtifact(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		full := filepath.Join(BackupDir(), entry.Name())
		_, checksumErr := os.Stat(BackupChecksumFile(full))
		files = append(files, map[string]any{
			"name":      entry.Name(),
			"size":      info.Size(),
			"mod_time":  info.ModTime(),
			"encrypted": strings.HasSuffix(entry.Name(), ".enc"),
			"checksum":  checksumErr == nil,
		})
	}
	return files, nil
}

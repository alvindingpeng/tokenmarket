package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/alvindingpeng/switchyard/internal/conf"
	"github.com/charmbracelet/log"
)

type updateMarker struct {
	BackupPath string `json:"backup_path"`
	OldVersion string `json:"old_version"`
	NewVersion string `json:"new_version"`
	Timestamp  int64  `json:"timestamp_unix_ms"`
}

const markerName = ".switchyard-update-marker.json"

func markerPath() string {
	execPath, err := os.Executable()
	if err != nil {
		return filepath.Join(".", markerName)
	}
	return filepath.Join(filepath.Dir(execPath), markerName)
}

func writeMarker(backupPath, oldVersion, newVersion string) {
	marker := updateMarker{
		BackupPath: backupPath,
		OldVersion: oldVersion,
		NewVersion: newVersion,
		Timestamp:  time.Now().UnixMilli(),
	}
	data, err := json.Marshal(marker)
	if err != nil {
		log.Warnf("failed to marshal update marker: %v", err)
		return
	}
	// 0600: marker 含备份二进制绝对路径等本地信息, 不应让同机其他用户读取。
	if err := os.WriteFile(markerPath(), data, 0600); err != nil {
		log.Warnf("failed to write update marker: %v", err)
	}
}

func removeMarker() {
	if err := os.Remove(markerPath()); err != nil && !os.IsNotExist(err) {
		log.Warnf("failed to remove update marker: %v", err)
	}
}

func HealthCheckAndRollback() {
	path := markerPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		log.Warnf("failed to read update marker: %v", err)
		return
	}

	var marker updateMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		log.Warnf("failed to parse update marker, ignoring: %v", err)
		os.Remove(path)
		return
	}

	elapsed := time.Since(time.UnixMilli(marker.Timestamp))
	if elapsed > 30*time.Second {
		log.Infof("update marker is stale (%v old), skipping health check", elapsed)
		os.Remove(path)
		return
	}

	log.Infof("post-update startup detected (from %s to %s), running health check...",
		marker.OldVersion, marker.NewVersion)

	time.Sleep(5 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), healthCheckTimeout)
	defer cancel()

	addr := fmt.Sprintf("http://127.0.0.1:%d/healthz", conf.AppConfig.Server.Port)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr, nil)
	if err != nil {
		log.Warnf("health check request failed, marking update as healthy: %v", err)
		os.Remove(path)
		return
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Errorf("health check FAILED (%v), rolling back to %s", err, marker.OldVersion)
		doRollback(marker.BackupPath, conf.Version, marker.OldVersion)
		return
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Errorf("health check status %d, rolling back to %s", resp.StatusCode, marker.OldVersion)
		doRollback(marker.BackupPath, conf.Version, marker.OldVersion)
		return
	}

	log.Infof("health check passed, update to %s is healthy", marker.NewVersion)
	os.Remove(path)
}

func doRollback(backupPath, failedVersion, restoreVersion string) {
	log.Warnf("rolling back from %s to %s (backup: %s)", failedVersion, restoreVersion, backupPath)
	execPath, err := os.Executable()
	if err != nil {
		log.Errorf("cannot determine current executable path: %v", err)
		return
	}
	execPath, err = filepath.Abs(execPath)
	if err != nil {
		log.Errorf("cannot resolve executable path: %v", err)
		return
	}
	// 回滚来源必须严格等于「当前可执行文件 + .old」: marker 里的 backup_path 来自磁盘 JSON,
	// 不校验就等于允许本地任意文件被 rename 到可执行路径再被执行(提权/驻留原语)。
	expected := execPath + ".old"
	if filepath.Clean(backupPath) != filepath.Clean(expected) {
		log.Errorf("refusing rollback: backup path %q is not the expected %q", backupPath, expected)
		removeMarker()
		return
	}
	if _, err := os.Stat(backupPath); err != nil {
		log.Errorf("backup binary %s unavailable, cannot rollback: %v", backupPath, err)
		return
	}
	if err := os.Rename(backupPath, execPath); err != nil {
		log.Errorf("rollback rename failed: %v", err)
		return
	}
	if err := os.Chmod(execPath, 0755); err != nil {
		log.Warnf("rollback chmod failed: %v", err)
	}
	removeMarker()
	log.Infof("rollback complete, restarting with %s", execPath)
	if runtime.GOOS == "windows" {
		cmd := exec.Command(execPath, os.Args[1:]...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Start(); err != nil {
			log.Errorf("rollback restart failed: %v", err)
		}
		os.Exit(0)
	}
	syscall.Exec(execPath, os.Args, os.Environ())
}

const healthCheckTimeout = 10 * time.Second

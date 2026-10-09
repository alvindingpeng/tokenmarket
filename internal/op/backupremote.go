package op

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/conf"
)

var backupRemoteClient = &http.Client{Timeout: 60 * time.Second}

// BackupRemoteConfigured 异地推送是否已配置(仅校验地址形状, 不发起请求)。
func BackupRemoteConfigured() bool {
	rawURL := strings.TrimSpace(conf.AppConfig.Backup.RemoteURL)
	if rawURL == "" {
		return false
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

// PushBackupRemote 把备份文件逐一 PUT 到 WebDAV 异地目录(目录须预先存在)。
// 每个文件最多 3 次尝试; 凭据来自 config.json backup.remote_* 或对应环境变量, 不入库。
func PushBackupRemote(files ...string) error {
	base := strings.TrimRight(strings.TrimSpace(conf.AppConfig.Backup.RemoteURL), "/")
	if base == "" {
		return fmt.Errorf("backup remote url is not configured")
	}
	for _, file := range files {
		if err := pushBackupFile(base, file); err != nil {
			return err
		}
	}
	return nil
}

func pushBackupFile(base, file string) error {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if attempt > 1 {
			time.Sleep(time.Duration(attempt-1) * time.Second)
		}
		lastErr = putBackupFile(base, file)
		if lastErr == nil {
			return nil
		}
	}
	return fmt.Errorf("push %s failed after 3 attempts: %w", filepath.Base(file), lastErr)
}

func putBackupFile(base, file string) error {
	fh, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	defer fh.Close()
	target := base + "/" + url.PathEscape(filepath.Base(file))
	req, err := http.NewRequest(http.MethodPut, target, fh)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if user := conf.AppConfig.Backup.RemoteUser; user != "" {
		req.SetBasicAuth(user, conf.AppConfig.Backup.RemotePassword)
	}
	resp, err := backupRemoteClient.Do(req)
	if err != nil {
		return fmt.Errorf("remote unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("remote returned status %d", resp.StatusCode)
	}
	return nil
}

package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/alvindingpeng/switchyard/internal/conf"
	"github.com/alvindingpeng/switchyard/internal/utils/shutdown"
	"github.com/charmbracelet/log"
)

const restartDelay = 1500 * time.Millisecond

// updateMarkerPath 返回更新标记文件的路径(与可执行文件同目录)。
func updateMarkerPath() string {
	execPath, err := os.Executable()
	if err != nil {
		return filepath.Join(".", ".switchyard-update-marker.json")
	}
	return filepath.Join(filepath.Dir(execPath), ".switchyard-update-marker.json")
}

type Result struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Asset      string `json:"asset"`
	ExecPath   string `json:"exec_path"`
	BackupPath string `json:"backup_path"`
}

func UpdateCore() error { _, err := Apply(false); return err }

func Apply(forceSameVersion bool) (*Result, error) {
	if Paused() {
		return nil, ErrUpdatePaused
	}
	status, err := Status(true)
	if err != nil {
		return nil, err
	}
	if status.CheckFailed {
		return nil, fmt.Errorf("update check failed: %s", status.CheckError)
	}
	if status.LatestVersion == "" {
		return nil, errors.New("latest release has no version tag")
	}
	// The parameter remains for compatibility with older callers, but a running
	// service must never reinstall the same version or accept a downgrade.
	_ = forceSameVersion
	if !status.UpdateAvailable {
		return nil, fmt.Errorf("%w: current version is %s", ErrUpToDate, conf.Version)
	}
	if status.AssetMissing || status.DownloadURL == "" {
		return nil, fmt.Errorf("release %s has no asset for %s: %s", status.LatestVersion, status.Platform, status.ExpectedAsset)
	}
	// 下载地址必须落在本仓库的 releases/download 前缀下: GitHub API 响应被投毒、代理回退链
	// 被劫持时, 都不能把下载指向任意 URL —— 那会演变成「下载并执行任意代码」。
	if !isTrustedDownloadURL(status.DownloadURL) {
		return nil, fmt.Errorf("untrusted download URL for update: %s", status.DownloadURL)
	}
	// 缺 SHA-256 digest 直接拒绝: 原实现仅告警后继续, 会在无任何完整性验证的情况下
	// 执行下载来的候选二进制(verifyCandidate 本身就会运行它)。
	if status.AssetSHA256 == "" {
		return nil, errors.New("release asset has no SHA-256 digest; refusing to run unverified update")
	}

	data, err := doRequestWithFallback(status.DownloadURL)
	if err != nil {
		return nil, fmt.Errorf("download %s failed: %w", status.AssetName, err)
	}
	if len(data) == 0 {
		return nil, errors.New("downloaded update archive is empty")
	}
	actual := sha256Hex(data)
	if !strings.EqualFold(actual, status.AssetSHA256) {
		return nil, fmt.Errorf("update archive checksum mismatch: got %s, expected %s", actual, status.AssetSHA256)
	}
	log.Infof("verified update archive sha256=%s", actual)

	execPath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("get executable path failed: %w", err)
	}
	execPath, err = filepath.Abs(execPath)
	if err != nil {
		return nil, fmt.Errorf("resolve executable path failed: %w", err)
	}
	if info, statErr := os.Stat(execPath); statErr != nil || info.IsDir() {
		return nil, fmt.Errorf("current executable is unavailable: %v", statErr)
	}

	tmpDir, err := os.MkdirTemp(filepath.Dir(execPath), ".switchyard-update-*")
	if err != nil {
		tmpDir, err = os.MkdirTemp("", "switchyard-update-*")
	}
	if err != nil {
		return nil, fmt.Errorf("create update temp directory failed: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	if err := unzip(data, tmpDir); err != nil {
		return nil, fmt.Errorf("unpack update archive failed: %w", err)
	}
	newExec, err := findCandidateExecutable(tmpDir, execPath)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(newExec, 0o755); err != nil {
		return nil, fmt.Errorf("make candidate executable failed: %w", err)
	}
	if err := verifyCandidate(newExec, status.LatestVersion); err != nil {
		return nil, err
	}

	oldInfo, err := os.Stat(execPath)
	if err != nil {
		return nil, fmt.Errorf("stat current executable failed: %w", err)
	}
	stagedPath, backupPath := execPath+".new", execPath+".old"
	_ = os.Remove(stagedPath)
	if err := copyFile(newExec, stagedPath); err != nil {
		return nil, fmt.Errorf("stage new executable failed: %w", err)
	}
	if err := os.Chmod(stagedPath, oldInfo.Mode().Perm()); err != nil {
		_ = os.Remove(stagedPath)
		return nil, fmt.Errorf("set staged executable permissions failed: %w", err)
	}
	_ = os.Remove(backupPath)
	if err := os.Rename(execPath, backupPath); err != nil {
		_ = os.Remove(stagedPath)
		return nil, fmt.Errorf("backup current executable failed: %w", err)
	}
	if err := os.Rename(stagedPath, execPath); err != nil {
		_ = os.Rename(backupPath, execPath)
		_ = os.Remove(stagedPath)
		return nil, fmt.Errorf("install new executable failed: %w", err)
	}
	log.Infof("update staged successfully: %s -> %s; rollback copy: %s", conf.Version, status.LatestVersion, backupPath)
	writeMarker(backupPath, conf.Version, status.LatestVersion)
	return &Result{From: conf.Version, To: status.LatestVersion, Asset: status.AssetName, ExecPath: execPath, BackupPath: backupPath}, nil
}

var versionLine = regexp.MustCompile("(?m)Version:\\s*(\\S+)")

// isTrustedDownloadURL 校验下载地址严格落在本仓库 release 资产前缀下(https 且路径以
// downloadBase 开头)。downloadBase 形如 https://github.com/alvindingpeng/tokenmarket/releases/download,
// 是编译期常量、不随 API 响应变化, 因此能作为独立于 API 响应的下载地址信任锚。
func isTrustedDownloadURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "https" {
		return false
	}
	// downloadBase 是常量(不含尾斜杠), 要求其后的第一个字符必须是 "/" 以防前缀拼接绕过。
	return strings.HasPrefix(u.String(), downloadBase+"/")
}

func verifyCandidate(path, expected string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "version")
	cmd.Dir = filepath.Dir(path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("candidate binary self-check failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	match := versionLine.FindStringSubmatch(string(out))
	if len(match) != 2 || !sameTag(match[1], expected) {
		return fmt.Errorf("candidate binary reports version %q, expected %q", strings.TrimSpace(string(out)), expected)
	}
	return nil
}

func sameTag(a, b string) bool {
	return strings.TrimPrefix(strings.TrimSpace(a), "v") == strings.TrimPrefix(strings.TrimSpace(b), "v")
}

func findCandidateExecutable(root, currentPath string) (string, error) {
	canonical := filepath.Join(root, conf.APP_NAME)
	if runtime.GOOS == "windows" {
		canonical += ".exe"
	}
	for _, candidate := range []string{canonical, filepath.Join(root, filepath.Base(currentPath))} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Size() > 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("update archive does not contain executable %q at its root", filepath.Base(canonical))
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func ScheduleRestart(execPath string) {
	time.AfterFunc(restartDelay, func() { restartExecutable(execPath) })
}

func restartExecutable(execPath string) {
	shutdown.Shutdown()
	log.Infof("restarting updated executable: %q %q", execPath, os.Args[1:])
	if runtime.GOOS == "windows" {
		cmd := exec.Command(execPath, os.Args[1:]...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Start(); err != nil {
			log.Errorf("restarting failed: %v", err)
			return
		}
		os.Exit(0)
	}
	if err := syscall.Exec(execPath, os.Args, os.Environ()); err != nil {
		log.Errorf("restarting failed: %v", err)
	}
}

func getDownloadFilename() (string, error) {
	name := ExpectedAsset()
	if name == "" {
		return "", fmt.Errorf("unsupported platform: %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return name, nil
}

func digestForData(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

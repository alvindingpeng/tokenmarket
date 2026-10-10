package update

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/rhttp"
	"github.com/charmbracelet/log"
)

// 更新源就是本仓库自己的 Releases(见 docs/update-pipeline.md)。
// 绝不指回上游 bestruirui/octopus: 上游产物里没有本分支的功能, 用它覆盖线上等于把系统降级。
const (
	RepoSlug = "alvindingpeng/tokenmarket"

	releaseListAPI = "https://api.github.com/repos/" + RepoSlug + "/releases?per_page=10"
	latestTagAPI   = "https://api.github.com/repos/" + RepoSlug + "/releases/latest"
	downloadBase   = "https://github.com/" + RepoSlug + "/releases/download"
)

// ErrUpdatePaused 表示更新源被摘掉(留空即停用整条链路)。
var ErrUpdatePaused = errors.New("update paused: update source not configured")

// ErrUpToDate 表示检查成功了, 但仓库里没有比当前更新的版本。
var ErrUpToDate = errors.New("already up to date")

// maxArchiveBytes 是给下载留的上限: 本项目的发布包在 60MB 量级, 留足余量,
// 同时避免上游返回异常内容时无界地吃内存。
const maxArchiveBytes = 512 << 20

// Paused 报告在线更新是否停用。地址都在常量里, 正常恒为 false;
// 保留它是为了换仓库或临时摘掉更新源时能一处关掉全链路(任务、接口、界面)。
func Paused() bool {
	return releaseListAPI == "" || downloadBase == ""
}

// ReleaseAsset 是 GitHub Release 上的一个产物。
type ReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	Digest             string `json:"digest"` // GitHub 对上传产物给 "sha256:<hex>"
}

// ReleaseInfo 是一条 Release。
type ReleaseInfo struct {
	TagName     string         `json:"tag_name"`
	Name        string         `json:"name"`
	Body        string         `json:"body"`
	PublishedAt string         `json:"published_at"`
	HTMLURL     string         `json:"html_url"`
	Draft       bool           `json:"draft"`
	Prerelease  bool           `json:"prerelease"`
	Message     string         `json:"message"` // API 报错时的提示(如限流), 正常发布里不出现。
	Assets      []ReleaseAsset `json:"assets"`
}

// StatusInfo 是「设置 > 信息」更新面板的全部数据。
//
// 一次检查就回答四件事: 现在什么版本、仓库最新什么版本、本平台有没有可下载的包、
// 以及上一次检查为什么失败。判断都放在后端, 界面只做展示, 不会出现
// "后端答 503、界面显示成浏览器缓存问题" 那类误判。
type StatusInfo struct {
	CurrentVersion   string `json:"current_version"`
	Paused           bool   `json:"paused"`
	CheckEnabled     bool   `json:"check_enabled"`
	CheckIntervalMin int    `json:"check_interval_min"`
	LastCheckAt      int64  `json:"last_check_at"`

	UpdateAvailable bool   `json:"update_available"`
	UpToDate        bool   `json:"up_to_date"`
	CheckFailed     bool   `json:"check_failed"`
	CheckError      string `json:"check_error"`

	Platform      string `json:"platform"`
	ExpectedAsset string `json:"expected_asset"`
	AssetMissing  bool   `json:"asset_missing"`

	LatestVersion     string `json:"latest_version"`
	LatestPublishedAt string `json:"latest_published_at"`
	LatestBody        string `json:"latest_body"`
	ReleaseURL        string `json:"release_url"`
	AssetName         string `json:"asset_name"`
	AssetSize         int64  `json:"asset_size"`
	AssetSHA256       string `json:"asset_sha256"`
	DownloadURL       string `json:"download_url"`
}

// cachedCheck 缓存上一次的检查结果。检查按设置里的间隔在后台跑, 界面只读这份缓存,
// 于是打开页面不会每次都打 GitHub API(匿名调用限流 60 次/小时, 很容易被点爆)。
type cachedCheck struct {
	mu       sync.RWMutex
	release  *ReleaseInfo
	err      error
	at       int64
	interval int
}

var cache cachedCheck

// githubPAT 用于抬高 GitHub API 限流并访问私有仓库, 完全可选:
// 环境变量 OCTOPUS_GITHUB_PAT。
var githubPAT = strings.TrimSpace(os.Getenv(strings.ToUpper(conf.APP_NAME) + "_GITHUB_PAT"))

func (c *cachedCheck) load() (*ReleaseInfo, error, int64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.release, c.err, c.at
}

func (c *cachedCheck) store(rel *ReleaseInfo, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.release = rel
	c.err = err
	c.at = time.Now().Unix()
}

// Invalidate 丢掉缓存, 让下一次读状态时立即重新检查。
// 改完检查开关或刚更新过版本时调用, 否则界面会拿着旧结果看新状态。
func Invalidate() {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.release = nil
	cache.err = nil
	cache.at = 0
}

// Platform 返回 linux/amd64 这样的运行平台标识, 界面直接显示。
func Platform() string { return runtime.GOOS + "/" + runtime.GOARCH }

// ExpectedAsset 是本平台在 Release 上应当看到的归档文件名, 与 scripts/build.sh 的产物同名。
func ExpectedAsset() string {
	name, err := archiveName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return ""
	}
	return name
}

// archiveName 给出某平台的归档名。本平台不在发布矩阵里(例如 riscv64)时报错,
// 而不是猜一个别的平台的包 —— 装错架构的二进制会让服务起不来, 比不更新糟得多。
func archiveName(goos, goarch string) (string, error) {
	switch goos {
	case "windows":
		if goarch == "amd64" {
			return "octopus-windows-amd64.zip", nil
		}
	case "darwin":
		if goarch == "amd64" || goarch == "arm64" {
			return "octopus-darwin-" + goarch + ".zip", nil
		}
	case "linux":
		switch goarch {
		case "386", "amd64", "arm", "arm64":
			return "octopus-linux-" + goarch + ".zip", nil
		}
	case "android":
		switch goarch {
		case "386", "amd64", "arm", "arm64":
			return "octopus-android-" + goarch + ".zip", nil
		}
	}
	return "", fmt.Errorf("unsupported platform: %s/%s", goos, goarch)
}

// CheckEnabled 报告是否允许自动检查更新(设置里的开关, 缺省开)。
func CheckEnabled() bool {
	if v, err := op.SettingGetBool(model.SettingKeyUpdateCheckEnabled); err == nil {
		return v
	}
	return true
}

// CheckIntervalMinutes 返回检查间隔(分钟), 越界收敛到 1-1440。
func CheckIntervalMinutes() int {
	v := op.SettingGetIntDefault(model.SettingKeyUpdateCheckMinutes, 60)
	if v < 1 {
		v = 1
	}
	if v > 1440 {
		v = 1440
	}
	return v
}

// Status 返回更新面板的数据。force=true 时立即重新查 GitHub, 否则复用未过期的缓存。
// 只有更新源被摘掉才返回 error; 网络失败等信息在 StatusInfo.CheckError 里,
// 因为界面需要区分"没得更新"和"查不出来"。
func Status(force bool) (*StatusInfo, error) {
	if Paused() {
		return nil, ErrUpdatePaused
	}
	platform := Platform()
	assetName, nameErr := archiveName(runtime.GOOS, runtime.GOARCH)
	interval := CheckIntervalMinutes()
	now := time.Now().Unix()

	var rel *ReleaseInfo
	var err error
	at := now
	haveCache := false
	if !force {
		cachedRel, cachedErr, cachedAt := cache.load()
		// 缓存由后台任务按同一个间隔写入, 这里只判断新鲜度, 不复用任务里那份过期结果。
		if cachedAt > 0 && now-cachedAt < int64(interval*60) {
			rel, err, at = cachedRel, cachedErr, cachedAt
			haveCache = true
		}
	}
	if !haveCache { // 还没拿到可用缓存, 现场查一次(失败就落进 CheckError)。
		rel, err = fetchLatest()
		cache.store(rel, err)
		at = time.Now().Unix()
	}
	return buildStatus(rel, err, platform, assetName, nameErr, interval, at), nil
}

// CheckNow 供后台定时任务调用: 发现可更新版本时返回状态, 否则返回 (nil, nil)。
func CheckNow() (*StatusInfo, error) {
	st, err := Status(true)
	if err != nil {
		return nil, err
	}
	if st.UpdateAvailable && !st.AssetMissing {
		return st, nil
	}
	return nil, nil
}

func buildStatus(rel *ReleaseInfo, checkErr error, platform, assetName string, nameErr error, interval int, at int64) *StatusInfo {
	info := &StatusInfo{
		CurrentVersion:   conf.Version,
		Paused:           Paused(),
		CheckEnabled:     CheckEnabled(),
		CheckIntervalMin: interval,
		LastCheckAt:      at,
		Platform:         platform,
		ExpectedAsset:    assetName,
	}
	if nameErr != nil {
		info.AssetMissing = true
		info.CheckError = nameErr.Error()
		return info
	}
	if checkErr != nil {
		info.CheckFailed = true
		info.CheckError = checkErr.Error()
		if rel != nil { // 保留上一次成功看到的版本, 界面仍有个参考值, 但不宣称"可更新"。
			info.LatestVersion = rel.TagName
			info.ReleaseURL = rel.HTMLURL
		}
		return info
	}
	if rel == nil {
		info.CheckFailed = true
		info.CheckError = "no published release found in " + RepoSlug
		return info
	}

	info.LatestVersion = rel.TagName
	info.LatestPublishedAt = rel.PublishedAt
	info.LatestBody = rel.Body
	info.ReleaseURL = rel.HTMLURL
	info.UpdateAvailable = greaterTag(rel.TagName, conf.Version)
	info.UpToDate = !info.UpdateAvailable

	asset := pickAsset(rel, assetName)
	if asset == nil {
		info.AssetMissing = true
		return info
	}
	algo, digest := splitDigest(asset.Digest)
	info.AssetName = asset.Name
	info.AssetSize = asset.Size
	info.DownloadURL = asset.BrowserDownloadURL
	if algo == "sha256" {
		info.AssetSHA256 = digest
	}
	return info
}

// pickAsset 先精确命中, 再大小写无关兜底。命中不了就是产物没上传, 绝不换别的平台的包。
func pickAsset(rel *ReleaseInfo, want string) *ReleaseAsset {
	for i := range rel.Assets {
		if rel.Assets[i].Name == want {
			return &rel.Assets[i]
		}
	}
	for i := range rel.Assets {
		if strings.EqualFold(rel.Assets[i].Name, want) {
			return &rel.Assets[i]
		}
	}
	return nil
}

func splitDigest(digest string) (string, string) {
	if idx := strings.Index(digest, ":"); idx > 0 {
		return digest[:idx], strings.ToLower(digest[idx+1:])
	}
	return "", strings.ToLower(digest)
}

// fetchLatest 读 Release 列表并挑最新的一条非草稿。用列表而不是 latest 端点,
// 是为了"最新"由版本号决定, 而不是由 GitHub 的发布时间决定。
func fetchLatest() (*ReleaseInfo, error) {
	body, err := doRequestWithFallback(releaseListAPI)
	if err != nil {
		return nil, err
	}
	var releases []ReleaseInfo
	if err := json.Unmarshal(body, &releases); err != nil {
		return nil, fmt.Errorf("parse release list failed: %w", err)
	}
	var best *ReleaseInfo
	for i := range releases {
		r := releases[i]
		if r.Draft || r.TagName == "" {
			continue
		}
		if best == nil || greaterTag(r.TagName, best.TagName) {
			best = &r
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no published release found in %s", RepoSlug)
	}
	return best, nil
}

// GetLatestInfo 只取最新发布的标签与说明, 供不需要平台差异判断的调用方使用。
func GetLatestInfo() (*ReleaseInfo, error) {
	if Paused() {
		return nil, ErrUpdatePaused
	}
	body, err := doRequestWithFallback(latestTagAPI)
	if err != nil {
		return nil, err
	}
	var rel ReleaseInfo
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, fmt.Errorf("parse latest release failed: %w", err)
	}
	if rel.Message != "" {
		return nil, fmt.Errorf("failed to get latest info: %s", rel.Message)
	}
	if rel.TagName == "" {
		return nil, fmt.Errorf("no published release found in %s", RepoSlug)
	}
	return &rel, nil
}

// doRequestWithFallback 先直连, 失败再走「系统设置」里的代理。
func doRequestWithFallback(url string) ([]byte, error) {
	data, err := doRequest(url, false)
	if err == nil {
		return data, nil
	}
	log.Debugf("update: direct request to %s failed: %v, retrying via proxy", url, err)
	proxied, perr := doRequest(url, true)
	if perr != nil {
		return nil, fmt.Errorf("%w (proxy: %v)", err, perr)
	}
	return proxied, nil
}

func doRequest(url string, useProxy bool) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var hc *http.Client
	var err error
	if useProxy {
		hc, err = rhttp.Proxy()
	} else {
		hc, err = rhttp.Direct()
	}
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", conf.APP_NAME+"-updater/"+conf.Version)
	if githubPAT != "" {
		req.Header.Set("Authorization", "Bearer "+githubPAT)
	}

	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxArchiveBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("github %d: %s", resp.StatusCode, summarizeError(resp.Status, data))
	}
	return data, nil
}

// summarizeError 把 GitHub 的错误响应压成一行: 只有 JSON 里的 message 值得带出来,
// 整段 HTML 错误页塞进日志和界面只会把真错误埋掉。
func summarizeError(status string, data []byte) string {
	var v map[string]any
	if err := json.Unmarshal(data, &v); err == nil {
		if msg, ok := v["message"].(string); ok && msg != "" {
			return status + ": " + msg
		}
	}
	s := strings.TrimSpace(string(data))
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	if s == "" {
		return status
	}
	return status + ": " + s
}

// greaterTag 比较两个 vX.Y.Z 标签, 左边更新则为 true。
// 任一边解析不出来(比如 dev、rc 后缀)就退化成"标签不同即视为有更新",
// 宁可让界面提示一次, 也不要因为解析失败把真更新吞掉。
func greaterTag(a, b string) bool {
	av, aok := parseTag(a)
	bv, bok := parseTag(b)
	if !aok || !bok {
		return normalizeTag(a) != normalizeTag(b)
	}
	for i := range av {
		if av[i] != bv[i] {
			return av[i] > bv[i]
		}
	}
	return false
}

type semver [4]int

func parseTag(tag string) (semver, bool) {
	var out semver
	s := strings.TrimPrefix(strings.TrimSpace(tag), "v")
	if s == "" {
		return out, false
	}
	main := s
	if idx := strings.IndexAny(main, "-+"); idx >= 0 {
		main = main[:idx]
	}
	fields := strings.Split(main, ".")
	if len(fields) == 0 || len(fields) > 4 {
		return out, false
	}
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func normalizeTag(tag string) string {
	return strings.TrimPrefix(strings.TrimSpace(tag), "v")
}

func unzip(data []byte, dest string) error {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		log.Debugf("new zip reader failed: %v", err)
		return err
	}

	for _, f := range r.File {
		fpath := filepath.Join(dest, f.Name)

		if !isPathInDest(fpath, dest) {
			log.Debugf("invalid file path: %s", fpath)
			return fmt.Errorf("invalid file path in archive: %s", f.Name)
		}

		info := f.FileInfo()
		if info.IsDir() {
			if err := os.MkdirAll(fpath, os.ModePerm); err != nil {
				return err
			}
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			// 符号链接一律不落地: 更新包只该有普通文件, 越界的软链能把内容写到任意路径。
			log.Debugf("skip symlink entry: %s", f.Name)
			continue
		}

		if err := extractFile(f, fpath); err != nil {
			return err
		}
	}
	return nil
}

func extractFile(f *zip.File, fpath string) error {
	if err := os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
		log.Debugf("mkdir all failed: %v", err)
		return err
	}

	// 0o600 起步: zip 里的权限位由上传方决定, 不能让发布包直接指定可执行位。
	outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		if err = os.Remove(fpath); err != nil {
			log.Debugf("remove file failed: %v", err)
			return err
		}
		outFile, err = os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			log.Debugf("open file failed: %v", err)
			return err
		}
	}
	defer outFile.Close()

	rc, err := f.Open()
	if err != nil {
		log.Debugf("open file failed: %v", err)
		return err
	}
	defer rc.Close()

	if _, err = io.Copy(outFile, rc); err != nil {
		log.Debugf("copy failed: %v", err)
		return err
	}
	return nil
}

func isPathInDest(fpath, dest string) bool {
	rel, err := filepath.Rel(dest, fpath)
	if err != nil {
		return false
	}
	return filepath.IsLocal(rel)
}

// sha256Hex 计算摘要, 供下载后核对 Release 上的 digest。
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

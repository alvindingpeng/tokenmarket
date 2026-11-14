package op

// 上游账号 OAuth 刷新的执行层。
//
// 这一层只做三件事: 决定该不该刷、发一次刷新请求、把结果原子回写。
// 它不进转发路径 —— 令牌提供者(relay 侧)是同步读缓存的, 刷新必须在定时任务里发生。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/netguard"
	"github.com/charmbracelet/log"
)

// accountRefreshLead 提前多久刷新。
// 取 10 分钟: 高到足以覆盖一次上游抖动与一次失败重试, 又低到不会把一个刚刷的令牌立刻再刷一遍
// (Codex 的令牌通常是小时级有效)。
const accountRefreshLead = 10 * time.Minute

// accountRefreshTimeout 单次刷新请求的超时。与上游交互, 不跟着请求上下文走。
const accountRefreshTimeout = 20 * time.Second

// accountRefreshLocks 按凭据 ID 串行化刷新。
//
// 这不是可选的优化。refresh_token 是单次有效的: 两个 goroutine 同时拿同一个旧 refresh_token
// 去刷新, 上游会接受第一个、拒绝第二个 —— 而两边都以为自己成功的话, 后写的那份会把前一份
// 轮换出来的新 refresh_token 覆盖掉, 账号就此永久失效。所以同一凭据的刷新必须串行,
// 且"读旧值 -> 刷新 -> 回写"必须在同一把锁内完成。
var accountRefreshLocks sync.Map // map[int]*sync.Mutex

func accountRefreshLockOf(channelKeyID int) *sync.Mutex {
	lock, _ := accountRefreshLocks.LoadOrStore(channelKeyID, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// AccountCredentialOf 把凭据列的账号 JSON 解成统一形态; 非账号凭据返回 false。
func AccountCredentialOf(channelKey model.ChannelKey) (*model.AccountCredential, bool) {
	if channelKey.AccountKind == "" || channelKey.AccountPlatform == "" {
		return nil, false
	}
	credential, err := model.AccountJSONParse(channelKey.AccountPlatform, []byte(channelKey.Key))
	if err != nil {
		// 解析失败不在这里报错: 调用方(刷新任务)需要区分"这行不是账号"与"这行是账号但坏了",
		// 前者静默跳过, 后者要标记失效。解析错误交给后者处理。
		return nil, false
	}
	return credential, true
}

// AccountNeedsRefresh 判断一条凭据是否到了该刷新的时机。
func AccountNeedsRefresh(channelKey model.ChannelKey, now time.Time) bool {
	if channelKey.AccountKind != model.AccountKindOAuth {
		return false
	}
	credential, ok := AccountCredentialOf(channelKey)
	if !ok {
		return false
	}
	// 到期时间以库里的列为准: 它是刷新后写入的权威值。JSON 里的 expired 只在导入时参考。
	if channelKey.TokenExpiresAt == nil {
		return model.AccountCredentialNeedsRefresh(*credential, now, accountRefreshLead)
	}
	return !now.Add(accountRefreshLead).Before(*channelKey.TokenExpiresAt)
}

// AccountRefresh 刷新一条账号凭据并把新令牌原子回写。
//
// 返回的错误要能区分两类:
//   - 可重试(网络抖动、5xx): 保持原样, 下一轮再来;
//   - 不可重试(invalid_grant 等): 调用方应标记该凭据失效并告警, 否则每轮都会拿一个
//     已作废的 refresh_token 去打上游。
func AccountRefresh(ctx context.Context, channelKeyID int, now time.Time) error {
	lock := accountRefreshLockOf(channelKeyID)
	lock.Lock()
	defer lock.Unlock()

	// 锁内重新读库: 等锁期间别人可能已经刷完了, 用缓存里的旧值会拿作废的 refresh_token 去打上游。
	var channelKey model.ChannelKey
	if err := db.GetDB().First(&channelKey, channelKeyID).Error; err != nil {
		return fmt.Errorf("load channel key %d: %w", channelKeyID, err)
	}
	if channelKey.AccountKind != model.AccountKindOAuth {
		return nil
	}
	credential, err := model.AccountJSONParse(channelKey.AccountPlatform, []byte(channelKey.Key))
	if err != nil {
		return fmt.Errorf("channel key %d: %w", channelKeyID, err)
	}
	spec, err := model.AccountOAuthSpecOf(channelKey.AccountPlatform)
	if err != nil {
		return fmt.Errorf("channel key %d: %w", channelKeyID, err)
	}

	contentType, body, err := model.AccountOAuthRefreshRequest(spec, *credential)
	if err != nil {
		return fmt.Errorf("channel key %d: %w", channelKeyID, err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, accountRefreshTimeout)
	defer cancel()
	client := accountRefreshHTTPClient()
	// 令牌端点是写死的官方域名, 但 SSRF 防护仍然要走一遍: 它同时校验协议与主机,
	// 是"这个地址能不能打"的唯一一道统一出口。
	//
	// 私网许可以应用设置为准(AllowPrivateUpstream)而不是硬编码 false:
	//   - 生产默认拒绝私网, 这才是要防的场景;
	//   - 但令牌端点本身是我们自己定的官方地址, 而且测试必须能指向本地假服务端 ——
	//     硬编码 false 会让任何测试都打不通, 于是单测只能去打真实上游。
	if err := netguard.CheckURL(reqCtx, spec.TokenURL, AllowPrivateUpstream()); err != nil {
		return fmt.Errorf("channel key %d: %w", channelKeyID, err)
	}
	request, err := http.NewRequestWithContext(reqCtx, http.MethodPost, spec.TokenURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("channel key %d: %w", channelKeyID, err)
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Accept", "application/json")
	// 平台要求的固定头(Kiro 的 AWS SDK 系列头等): 少了它上游会拒, 且不说明原因。
	for name, value := range model.AccountOAuthHeaders(spec) {
		request.Header.Set(name, value)
	}

	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("channel key %d: refresh request: %w", channelKeyID, err)
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("channel key %d: read response: %w", channelKeyID, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// 把上游回复带回调用方: invalid_grant 之类的语义是"这个账号没救了", 与"网络抖了一下"
		// 的处理完全不同, 而单看状态码 400 分辨不出来。
		return &AccountRefreshError{Status: response.StatusCode, Body: string(raw)}
	}

	result, err := model.AccountOAuthParseRefreshResponse(spec, *credential, raw, now)
	if err != nil {
		return fmt.Errorf("channel key %d: %w", channelKeyID, err)
	}

	// 回写: 把更新后的账号 JSON 与元数据一起写回, 顺序与内容都必须完整。
	// 漏写 refresh_token 就是账号永久失效, 因此这里先把 JSON 组装好再一次性落库。
	updated := map[string]any{}
	if err := json.Unmarshal([]byte(channelKey.Key), &updated); err != nil || updated == nil {
		updated = map[string]any{}
	}
	// 回写的键名与层级必须与"解析层读的那一层"完全对齐, 否则会写出一个自己读不回来的文件:
	// 令牌确实换到了, 但下次刷新读不到, 症状与账号被封一模一样。
	//
	// 三种形状:
	//   顶层 snake_case        —— Codex 及令牌端点响应原文
	//   {"tokens":{...}}       —— Codex CLI 导出
	//   {"claudeAiOauth":{..}} —— Claude Code 凭据文件(camelCase)
	accessField, refreshField, container := model.AccountOAuthStoreFields(spec)
	writeTokensTo := func(target map[string]any) {
		target[accessField] = result.AccessToken
		target[refreshField] = result.RefreshToken
	}
	// 判定写回层级时按平台声明的 container 优先, 再回退到实际存在的 tokens 层:
	// Claude 的 container 是 claudeAiOauth, 若那次导出的文件里没有该层则落到顶层。
	switch {
	case container != "":
		layer, ok := updated[container].(map[string]any)
		if !ok {
			layer = map[string]any{}
			updated[container] = layer
		}
		writeTokensTo(layer)
	case hasMapAt(updated, "tokens"):
		writeTokensTo(updated["tokens"].(map[string]any))
	default:
		writeTokensTo(updated)
	}
	encoded, err := json.Marshal(updated)
	if err != nil {
		return fmt.Errorf("channel key %d: marshal updated account: %w", channelKeyID, err)
	}

	refreshedAt := now
	// map 更新绕过字段序列化器, 但密文归属由 gorm 回调按 (channel_id, name) 推得,
	// 因此 Model 必须带上身份, 否则会 fail closed 拒绝写入。
	identity := model.ChannelKey{ID: channelKey.ID, ChannelID: channelKey.ChannelID,
		ChannelKeyConfig: model.ChannelKeyConfig{Name: channelKey.Name}}
	updates := map[string]any{
		"key":              string(encoded),
		"token_expires_at": result.ExpiresAt,
		"token_refresh_at": refreshedAt,
	}
	if err := db.GetDB().Model(&identity).Where("id = ?", channelKey.ID).Updates(updates).Error; err != nil {
		// 写失败是最坏的情况: 上游已经轮换掉旧 refresh_token, 而新值没落库。
		// 此时必须把错误放大 —— 这是"账号即将永久失效"的信号, 不能被当成普通刷新失败重试掉。
		return &AccountRefreshPersistError{ChannelKeyID: channelKeyID, Err: err}
	}

	// 内存缓存同步: 转发路径读的是缓存, 只改库不改缓存等于刷新白做(要等下一次全量重载才生效)。
	updateChannelKeyBreakerCache(channelKey.ID, func(cached *model.ChannelKey) {
		cached.Key = string(encoded)
		cached.TokenExpiresAt = &result.ExpiresAt
		cached.TokenRefreshAt = &refreshedAt
		cached.AccountEmail = accountEmailOrExisting(*credential, cached.AccountEmail)
	})
	log.Infof("account refresh: channel_key=%d platform=%s expires_at=%s rotated=%v",
		channelKeyID, channelKey.AccountPlatform, result.ExpiresAt.Format(time.RFC3339), result.RefreshTokenRotated)
	return nil
}

// AccountRefreshError 上游明确拒绝了刷新(4xx/5xx)。Status 与 Body 用于区分
// "账号已失效"与"上游暂时故障"。
type AccountRefreshError struct {
	Status int
	Body   string
}

func (e *AccountRefreshError) Error() string {
	return fmt.Sprintf("refresh rejected: status=%d body=%s", e.Status, accountBodyExcerpt(e.Body))
}

// AccountRefreshPersistError 刷新成功但回写失败 —— 最危险的失败模式, 见 AccountRefresh 内注释。
type AccountRefreshPersistError struct {
	ChannelKeyID int
	Err          error
}

func (e *AccountRefreshPersistError) Error() string {
	return fmt.Sprintf("channel key %d refreshed but persist failed (refresh_token may be lost): %v", e.ChannelKeyID, e.Err)
}

func (e *AccountRefreshPersistError) Unwrap() error { return e.Err }

// AccountRefreshesDue 列出所有到期待刷新的账号凭据。
// 只扫 history 一遍, 不做逐条判定之外的查询: 待刷新集合通常很小(账号数×一个周期一次)。
func AccountRefreshesDue(now time.Time) []model.ChannelKey {
	due := make([]model.ChannelKey, 0, 8)
	for _, channelKey := range channelKeyCache.GetAll() {
		if channelKey.AccountKind != model.AccountKindOAuth {
			continue
		}
		if !channelKey.Enabled {
			// 停用的凭据不刷新: 它不参与转发, 刷了也用不上, 反而多打上游。
			continue
		}
		if AccountNeedsRefresh(channelKey, now) {
			due = append(due, channelKey)
		}
	}
	return due
}

// hasMapAt 报告 target[key] 是否为 JSON 对象, 避免在 switch 里重复写类型断言。
func hasMapAt(target map[string]any, key string) bool {
	value, ok := target[key]
	if !ok {
		return false
	}
	_, isMap := value.(map[string]any)
	return isMap
}

func accountEmailOrExisting(credential model.AccountCredential, existing string) string {
	if credential.Email != "" {
		return credential.Email
	}
	return existing
}

// accountJSONKeyOf 返回账号 JSON 里 access_token 的键名。CLI 导出形状用 snake_case。
func accountJSONKeyOf(spec model.AccountOAuthSpec) string {
	return spec.AccessTokenField
}

// accountRefreshClient 令牌端点专用客户端: 挂 DialGuard 防私网 SSRF 且禁止跟随重定向。
// 与 channelhealth.go 同一范式 —— op 不能 import rhttp(会形成循环依赖), 故直接用 netguard 构建。
// 不跟随重定向是必要的: 令牌端点被 302 到别的地址时, 我们宁可失败也不该把 refresh_token 发过去。
var accountRefreshClient = func() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = netguard.DialGuard{AllowPrivate: AllowPrivateUpstream, Next: transport.DialContext}.DialContext
	return &http.Client{
		Timeout:   accountRefreshTimeout,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}()

func accountRefreshHTTPClient() *http.Client { return accountRefreshClient }

func accountBodyExcerpt(body string) string {
	if len(body) <= 200 {
		return body
	}
	return body[:200] + "..."
}

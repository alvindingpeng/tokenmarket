package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/secretstore"
	"github.com/alvindingpeng/switchyard/internal/server/auth"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/alvindingpeng/switchyard/internal/totp"
	"github.com/gin-gonic/gin"
)

// 2FA 信任设备的接口契约: 登录免码、设置页清单/吊销、恢复码自助重生成。
//
// 这一层测的是"接线对不对"(op 层已单独测过判据): 登录响应到底有没有下发 Cookie、
// 免码登录是否真的 200、只读清单是否被 step-up 拦住、以及重新生成后旧码是否立即失效。

type trustedDeviceEnv struct {
	user    model.User
	session string
	secret  string
	// enableCode 是启用 2FA 时用掉的那一格动态码; 后续用例必须用后面的格子, 否则是重放。
	enableCode string
	engine     *gin.Engine
}

func trustedDeviceSetup(t *testing.T) *trustedDeviceEnv {
	t.Helper()
	u, session := stepupTestEnv(t)
	secretstore.Reset()
	t.Cleanup(secretstore.Reset)
	if err := secretstore.Init(secretstore.KEKConfig{Inline: strings.Repeat("51", 32)}); err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	if err := router.RegisterAll(engine); err != nil {
		t.Fatal(err)
	}
	return &trustedDeviceEnv{user: u, session: session, engine: engine}
}

func (e *trustedDeviceEnv) do(t *testing.T, method, path, body string, cookies map[string]string, stepUp string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.240:4242"
	for name, value := range cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	if stepUp != "" {
		req.Header.Set("X-Step-Up-Token", stepUp)
	}
	w := httptest.NewRecorder()
	e.engine.ServeHTTP(w, req)
	return w
}

func cookieValue(w *httptest.ResponseRecorder, name string) string {
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

// enableTOTPFor 直接走 op 层启用 2FA, 避开 setup/enable 的 UI 流程(那是另一个用例的事)。
func (e *trustedDeviceEnv) enableTOTPFor(t *testing.T) {
	t.Helper()
	secret, _, err := op.UserTOTPSetup(e.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := totp.Generate(secret, time.Now().Unix())
	if _, err := op.UserTOTPEnable(e.user.ID, code); err != nil {
		t.Fatal(err)
	}
	// 启用 2FA 会把 token_version +1 —— 之前签的会话 JWT 立即失效, 所以这里必须重签一枚,
	// 否则后面每个带 auth Cookie 的请求都会 401, 失败原因还跟被测逻辑毫无关系。
	fresh, err := op.UserGetByID(e.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if e.session, _, err = auth.GenerateJWTToken(fresh, 3600); err != nil {
		t.Fatal(err)
	}
	e.user = fresh
	e.secret = secret
	// 后面的登录用码必须是**下一格**: 启用时那一格已被消耗(last_step), 重放会算无效码。
	e.enableCode = code
}

func loginBody(t *testing.T, username, password, code string, trust bool) string {
	t.Helper()
	payload := map[string]any{"username": username, "password": password, "totp_code": code}
	if trust {
		payload["trust_device"] = true
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTrustedDeviceLoginBypassesSecondFactor(t *testing.T) {
	env := trustedDeviceSetup(t)
	env.enableTOTPFor(t)
	// 登录限流的计数是包级全局状态: 每个用例结束都清掉, 否则 -shuffle 下彼此污染。
	defer auth.LoginSuccess("ip:192.0.2.240")
	defer auth.LoginSuccess("u:" + env.user.Username)

	code, _ := totp.Generate(env.secret, time.Now().Unix()+30)
	// 勾了「信任本机」+ 正确动态码: 必须下发信任设备 Cookie。
	w := env.do(t, "POST", "/api/v1/user/login", loginBody(t, env.user.Username, "password123", code, true), nil, "")
	if w.Code != 200 {
		t.Fatalf("login with trust: %d %s", w.Code, w.Body.String())
	}
	trusted := cookieValue(w, model.TrustedDeviceCookieName)
	if trusted == "" {
		t.Fatal("no trusted device cookie issued")
	}
	if cookie := w.Result().Cookies(); func() bool {
		for _, c := range cookie {
			if c.Name == model.TrustedDeviceCookieName {
				// HttpOnly 必开: 让 JS 读得到等于把绕过 2FA 的能力交给任何一处 XSS。
				return c.HttpOnly
			}
		}
		return false
	}() == false {
		t.Fatal("trusted device cookie is not HttpOnly")
	}
	auth.LoginSuccess("ip:192.0.2.240")
	auth.LoginSuccess("u:" + env.user.Username)

	// 同一台机器再登录: 不输码也必须成功。
	w = env.do(t, "POST", "/api/v1/user/login", loginBody(t, env.user.Username, "password123", "", false),
		map[string]string{model.TrustedDeviceCookieName: trusted}, "")
	if w.Code != 200 || cookieValue(w, "auth") == "" {
		t.Fatalf("trusted login without code: %d %s", w.Code, w.Body.String())
	}
	auth.LoginSuccess("ip:192.0.2.240")
	auth.LoginSuccess("u:" + env.user.Username)

	// 没有 Cookie: 仍然要码。这条是"旁路只对这台机器生效"的底线。
	w = env.do(t, "POST", "/api/v1/user/login", loginBody(t, env.user.Username, "password123", "", false), nil, "")
	if w.Code != 401 || !strings.Contains(w.Body.String(), "two_factor_required") {
		t.Fatalf("no cookie should require code: %d %s", w.Code, w.Body.String())
	}
	auth.LoginSuccess("ip:192.0.2.240")
	auth.LoginSuccess("u:" + env.user.Username)

	// 伪造 Cookie: 同样要码(哈希查不到)。
	w = env.do(t, "POST", "/api/v1/user/login", loginBody(t, env.user.Username, "password123", "", false),
		map[string]string{model.TrustedDeviceCookieName: strings.Repeat("f", 64)}, "")
	if w.Code != 401 {
		t.Fatalf("bogus cookie accepted: %d %s", w.Code, w.Body.String())
	}
}

func TestTrustedDeviceListAndRevokeEndpoints(t *testing.T) {
	env := trustedDeviceSetup(t)
	env.enableTOTPFor(t)
	code, _ := totp.Generate(env.secret, time.Now().Unix()+30)
	op.UserTOTPVerify(mustUser(t, env.user.ID), code) // 消耗掉这一格, 避免"上一格重放"干扰

	if _, _, err := op.TrustedDeviceIssue(env.user.ID, "Mozilla/5.0 (test)", "192.0.2.240"); err != nil {
		t.Fatal(err)
	}
	session := map[string]string{"auth": env.session}

	// 只读清单: 不要求 step-up(看一眼列表不该弹密码确认)。
	w := env.do(t, "GET", "/api/v1/user/2fa/trusted-devices", "", session, "")
	if w.Code != 200 {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	var listed struct {
		Items []model.TrustedDeviceView `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v (%s)", err, w.Body.String())
	}
	// resp.Success 可能包一层 data: 兼容两种形状, 找到 items 即可。
	if len(listed.Items) == 0 {
		var wrapped struct {
			Data struct {
				Items []model.TrustedDeviceView `json:"items"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &wrapped); err == nil {
			listed.Items = wrapped.Data.Items
		}
	}
	if len(listed.Items) != 1 {
		t.Fatalf("expected 1 device, got %d (%s)", len(listed.Items), w.Body.String())
	}
	// 视图里绝不能带令牌哈希。
	if strings.Contains(w.Body.String(), "token_hash") || strings.Contains(w.Body.String(), "TokenHash") {
		t.Fatal("token hash leaked into response")
	}
	deviceID := listed.Items[0].ID

	// 吊销是安全写: 没有 step-up 必须 428。
	w = env.do(t, "DELETE", "/api/v1/user/2fa/trusted-devices/"+itoa(deviceID), "", session, "")
	if w.Code != http.StatusPreconditionRequired {
		t.Fatalf("revoke without step-up: %d %s", w.Code, w.Body.String())
	}

	// 换一枚 step-up 凭证后放行。
	stepUp := env.issueStepUp(t)
	w = env.do(t, "DELETE", "/api/v1/user/2fa/trusted-devices/"+itoa(deviceID), "", session, stepUp)
	if w.Code != 200 {
		t.Fatalf("revoke: %d %s", w.Code, w.Body.String())
	}
	if remaining, err := op.TrustedDeviceList(env.user.ID); err != nil || len(remaining) != 0 {
		t.Fatalf("device survived revoke: %d %v", len(remaining), err)
	}
}

func TestRecoveryRegenerateEndpointInvalidatesOldCodes(t *testing.T) {
	env := trustedDeviceSetup(t)
	env.enableTOTPFor(t)
	before, _ := op.UserGetByID(env.user.ID)
	var oldHashes []string
	if err := json.Unmarshal([]byte(before.TOTPRecoveryHashes), &oldHashes); err != nil || len(oldHashes) != 10 {
		t.Fatalf("initial hashes: %d %v", len(oldHashes), err)
	}
	session := map[string]string{"auth": env.session}
	stepUp := env.issueStepUp(t)

	code, _ := totp.Generate(env.secret, time.Now().Unix()+30)
	body, _ := json.Marshal(map[string]string{"code": code})
	w := env.do(t, "POST", "/api/v1/user/2fa/recovery-codes/regenerate", string(body), session, stepUp)
	if w.Code != 200 {
		t.Fatalf("regenerate: %d %s", w.Code, w.Body.String())
	}
	var payload struct {
		Codes []string `json:"recovery_codes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil || len(payload.Codes) != 10 {
		var wrapped struct {
			Data struct {
				Codes []string `json:"recovery_codes"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &wrapped); err == nil && len(wrapped.Data.Codes) == 10 {
			payload.Codes = wrapped.Data.Codes
		}
	}
	if len(payload.Codes) != 10 {
		t.Fatalf("expected 10 new codes, got %d (%s)", len(payload.Codes), w.Body.String())
	}
	after, _ := op.UserGetByID(env.user.ID)
	// 整体替换而不是追加: 20 条哈希说明旧码还活着。
	var newHashes []string
	if err := json.Unmarshal([]byte(after.TOTPRecoveryHashes), &newHashes); err != nil || len(newHashes) != 10 {
		t.Fatalf("hashes after regenerate: %d %v", len(newHashes), err)
	}
	var oldCodesStillWork bool
	for _, h := range newHashes {
		for _, old := range oldHashes {
			if h == old {
				oldCodesStillWork = true
			}
		}
	}
	if oldCodesStillWork {
		t.Fatal("old recovery hash survived regeneration")
	}

	// 没有 step-up 时不能重生成(它输出的是 10 把能绕过第二因子的钥匙)。
	w = env.do(t, "POST", "/api/v1/user/2fa/recovery-codes/regenerate", string(body), session, "")
	if w.Code != http.StatusPreconditionRequired {
		t.Fatalf("regenerate without step-up: %d %s", w.Code, w.Body.String())
	}
}

// issueStepUp 用登录态 + 密码换一枚 step-up 凭证。
func (e *trustedDeviceEnv) issueStepUp(t *testing.T) string {
	t.Helper()
	w := e.do(t, "POST", "/api/v1/user/step-up", `{"password":"password123"}`, map[string]string{"auth": e.session}, "")
	if w.Code != 200 {
		t.Fatalf("step-up: %d %s", w.Code, w.Body.String())
	}
	var payload struct {
		Token string `json:"token"`
		Data  struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode step-up: %v", err)
	}
	if payload.Token == "" {
		payload.Token = payload.Data.Token
	}
	if payload.Token == "" {
		t.Fatalf("no step-up token: %s", w.Body.String())
	}
	return payload.Token
}

func mustUser(t *testing.T, id uint) model.User {
	t.Helper()
	u, err := op.UserGetByID(id)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func itoa(v uint) string {
	if v == 0 {
		return "0"
	}
	var digits []byte
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	return string(digits)
}

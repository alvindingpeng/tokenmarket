package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/auth"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-gonic/gin"
)

// stepupTestEnv 起一套最小可用环境: 临时 sqlite 库、跳过默认管理员改密闸门(该闸门由
// auth_gate_test 专门覆盖)、初始化签名密钥、自建一个非默认口令的管理员。
func stepupTestEnv(t *testing.T) (model.User, string) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "stepup-api.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := op.SettingUpsert(model.SettingKeyAdminDefaultPwdChanged, "true"); err != nil {
		t.Fatal(err)
	}
	if err := auth.InitSecret(); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	user, err := op.UserCreate("stepup-admin", "", "password123", model.RoleAdmin, model.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = op.UserDelete(user.ID) })
	token, _, err := auth.GenerateJWTToken(user, 3600)
	if err != nil {
		t.Fatal(err)
	}
	return user, token
}

func stepupBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("parse response %s: %v", w.Body.String(), err)
	}
	if payload.Data == nil {
		t.Fatalf("response has no data: %s", w.Body.String())
	}
	return payload.Data
}

// TestStepUpEndpointIssuesSessionBoundCredential 密码重认证端点: 正确口令换短期凭证,
// 凭证能在同一登录会话里过闸; 审计里既不落口令也不落凭证。
func TestStepUpEndpointIssuesSessionBoundCredential(t *testing.T) {
	user, sessionToken := stepupTestEnv(t)

	engine := gin.New()
	engine.POST("/api/v1/user/step-up", middleware.Auth(), middleware.RequireJSON(), stepUp)
	engine.POST("/high-risk", middleware.Auth(), middleware.RequireStepUp(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	request := func(path, body, stepUpToken string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "auth", Value: sessionToken})
		if stepUpToken != "" {
			req.Header.Set(middleware.StepUpTokenHeader, stepUpToken)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}

	// 高危端点先撞闸: 没有凭证进不去。
	if w := request("/high-risk", "{}", ""); w.Code != http.StatusPreconditionRequired {
		t.Fatalf("high-risk without step-up: got %d want 428", w.Code)
	}

	// 正确口令换凭证。
	w := request("/api/v1/user/step-up", `{"password":"password123"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("step-up with the correct password: %d %s", w.Code, w.Body.String())
	}
	data := stepupBody(t, w)
	token, _ := data["token"].(string)
	if token == "" {
		t.Fatalf("step-up response must carry a credential: %s", w.Body.String())
	}
	if expiresIn, ok := data["expires_in"].(float64); !ok || expiresIn <= 0 || expiresIn > auth.StepUpTTL.Seconds() {
		t.Fatalf("step-up credential must be short-lived, expires_in=%v", data["expires_in"])
	}

	// 凭证带在同一登录会话上能过闸。
	if w := request("/high-risk", "{}", token); w.Code != http.StatusOK {
		t.Fatalf("valid step-up credential rejected: %d %s", w.Code, w.Body.String())
	}

	// 换一段登录会话(同账号、不同 JWT), 凭证不跟随。
	otherSession, _, err := auth.GenerateJWTToken(user, 7200)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/high-risk", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "auth", Value: otherSession})
	req.Header.Set(middleware.StepUpTokenHeader, token)
	w2 := httptest.NewRecorder()
	engine.ServeHTTP(w2, req)
	if w2.Code != http.StatusPreconditionRequired {
		t.Fatalf("credential must not travel to another login session: %d %s", w2.Code, w2.Body.String())
	}

	// 审计留痕但不留密: 所有审计行都不得出现口令、凭证或会话令牌。
	var audits []model.AuditLog
	if err := db.GetDB().Find(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if len(audits) == 0 {
		t.Fatal("step-up attempts must be audited")
	}
	sawSuccess := false
	for _, row := range audits {
		line := row.Action + " " + row.Target + " " + row.Detail
		for _, secret := range []string{"password123", token, sessionToken, otherSession} {
			if strings.Contains(line, secret) {
				t.Fatalf("audit leaked a secret: %q", line)
			}
		}
		if row.Action == "user.step-up" {
			sawSuccess = true
		}
	}
	if !sawSuccess {
		t.Fatalf("missing user.step-up audit row: %+v", audits)
	}
}

// TestStepUpEndpointRejectsWrongPasswordAndThrottles 错口令 401、审计同样不落口令,
// 连续试错触发 429 + Retry-After —— 密码验证与登录同一套防爆破闸门。
func TestStepUpEndpointRejectsWrongPasswordAndThrottles(t *testing.T) {
	user, sessionToken := stepupTestEnv(t)
	// 防爆破闸门是进程级内存表, 而测试库是每个用例一份: 用完把自己那两条计数清掉,
	// 免得下一个用例撞上前一个用例留下的锁。
	t.Cleanup(func() { auth.StepUpSuccess("10.77.0.5", user.ID) })

	engine := gin.New()
	engine.POST("/api/v1/user/step-up", middleware.Auth(), middleware.RequireJSON(), stepUp)

	attempt := func(password string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/user/step-up", strings.NewReader(`{"password":"`+password+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "auth", Value: sessionToken})
		req.RemoteAddr = "10.77.0.5:4242"
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}

	if w := attempt("not-the-password"); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: got %d want 401", w.Code)
	}
	var audits []model.AuditLog
	if err := db.GetDB().Find(&audits).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range audits {
		if strings.Contains(row.Detail, "not-the-password") || strings.Contains(row.Target, "not-the-password") {
			t.Fatalf("audit leaked the attempted password: %+v", row)
		}
	}

	// 连续试错直到限流: 到达阈值后 429, 且给 Retry-After。
	throttled := false
	for i := 0; i < 12; i++ {
		w := attempt("still-wrong")
		if w.Code == http.StatusTooManyRequests {
			if w.Header().Get("Retry-After") == "" {
				t.Fatal("throttled response must carry Retry-After")
			}
			throttled = true
			break
		}
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("unexpected status %d: %s", w.Code, w.Body.String())
		}
	}
	if !throttled {
		t.Fatal("password verification must be rate limited")
	}

	// 限流期间即便口令正确也不放行(先过防爆破闸, 再验口令)。
	if w := attempt("password123"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("locked-out verification must stay throttled: got %d %s", w.Code, w.Body.String())
	}
}

// TestStepUpProtectedRoutesThroughRealRouter 从真实路由注册表出发的覆盖:
// 高危写操作(余额调整/提现审批/备份导入/敏感导出/结算生成/计费恢复/系统设置/用户管理)
// 缺 step-up 一律 428, 读列表不需要; 高危端点带有效凭证能进到业务处理。
// RegisterAll 会消费全局注册表, 因此整包里只能有这一个用例走真实路由。
func TestStepUpProtectedRoutesThroughRealRouter(t *testing.T) {
	_, sessionToken := stepupTestEnv(t)
	engine := gin.New()
	if err := router.RegisterAll(engine); err != nil {
		t.Fatal(err)
	}

	request := func(method, path, body, stepUpToken string) *httptest.ResponseRecorder {
		var reader *strings.Reader
		if body == "" {
			reader = strings.NewReader("")
		} else {
			reader = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, path, reader)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		req.AddCookie(&http.Cookie{Name: "auth", Value: sessionToken})
		if stepUpToken != "" {
			req.Header.Set(middleware.StepUpTokenHeader, stepUpToken)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}

	protected := []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/redemption/batch/create", "{}"},
		{http.MethodPost, "/api/v1/redemption/code/disable", "{}"},
		{http.MethodPost, "/api/v1/dispute/review", "{}"},
		{http.MethodGet, "/api/v1/ops/metrics/token", ""},
		{http.MethodPost, "/api/v1/ops/metrics/token/rotate", "{}"},
		{http.MethodPost, "/api/v1/ops/auth-secret/rotate", "{}"},
		{http.MethodPost, "/api/v1/ops/log-lifecycle/run", "{}"},
		{http.MethodPost, "/api/v1/ops/backup/run", "{}"},
		{http.MethodPost, "/api/v1/update", "{}"},
		{http.MethodPost, "/api/v1/user/manage/create", `{"username":""}`},
		{http.MethodPost, "/api/v1/user/manage/update", `{"id":999999,"balance":1}`},
		{http.MethodPost, "/api/v1/user/manage/reset-password", `{"id":999999,"password":"password123"}`},
		{http.MethodDelete, "/api/v1/user/manage/delete/999999", ""},
		{http.MethodPost, "/api/v1/withdrawal/review", "{}"},
		{http.MethodPost, "/api/v1/withdrawal/mark-paid", "{}"},
		{http.MethodPost, "/api/v1/withdrawal/rollback", "{}"},
		{http.MethodPost, "/api/v1/setting/set", `{"key":"stepup_probe","value":"1"}`},
		{http.MethodGet, "/api/v1/setting/export", ""},
		{http.MethodPost, "/api/v1/setting/import", ""},
		{http.MethodGet, "/api/v1/audit/export", ""},
		{http.MethodGet, "/api/v1/log/export", ""},
		{http.MethodGet, "/api/v1/stats/billing/records/export", ""},
		{http.MethodPost, "/api/v1/settlement/generate", `{"day":"20990101"}`},
		{http.MethodPost, "/api/v1/ops/billing-recovery/run", "{}"},
	}
	for _, route := range protected {
		w := request(route.method, route.path, route.body, "")
		if w.Code != http.StatusPreconditionRequired {
			t.Fatalf("%s %s without step-up: got %d want 428: %s", route.method, route.path, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"error_code":"step_up_required"`) {
			t.Fatalf("%s %s: missing step_up_required code: %s", route.method, route.path, w.Body.String())
		}
	}

	// 读列表类端点不需要二次验证。
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/user/manage/list"},
		{http.MethodGet, "/api/v1/setting/list"},
		{http.MethodGet, "/api/v1/audit/list"},
		{http.MethodGet, "/api/v1/log/list"},
		{http.MethodGet, "/api/v1/settlement/list"},
		{http.MethodGet, "/api/v1/withdrawal/list"},
		{http.MethodGet, "/api/v1/ops/billing-recovery/list"},
		{http.MethodGet, "/api/v1/stats/billing/records"},
	} {
		w := request(route.method, route.path, "", "")
		if w.Code == http.StatusPreconditionRequired {
			t.Fatalf("%s %s is a read endpoint and must not require step-up", route.method, route.path)
		}
	}

	// 完整交互: 真实路由上的 /api/v1/user/step-up 换凭证, 再打高危端点进到业务层。
	w := request(http.MethodPost, "/api/v1/user/step-up", `{"password":"password123"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("real step-up endpoint: %d %s", w.Code, w.Body.String())
	}
	token, _ := stepupBody(t, w)["token"].(string)
	if token == "" {
		t.Fatalf("real step-up endpoint returned no credential: %s", w.Body.String())
	}
	// 整库导出: 拿到文件本体而不是 428, 证明闸门对带凭证的请求放行。
	if w := request(http.MethodGet, "/api/v1/setting/export", "", token); w.Code != http.StatusOK {
		t.Fatalf("protected export with a valid step-up credential: %d %s", w.Code, w.Body.String())
	}
	// 用户管理写操作: 进到业务层后因目标不存在失败(4xx), 但绝不再是 428。
	w = request(http.MethodPost, "/api/v1/user/manage/update", `{"id":999999,"balance":1}`, token)
	if w.Code == http.StatusPreconditionRequired {
		t.Fatalf("valid step-up credential still rejected: %s", w.Body.String())
	}
}

// TestStepUpEndpointDocumentedInOpenAPI 新端点与 428 契约进文档: 调用方(含前端)要靠它知道
// 高危操作得带 X-Step-Up-Token、失败码是 step_up_required。
func TestStepUpEndpointDocumentedInOpenAPI(t *testing.T) {
	paths := openAPIAdminPaths()
	route, ok := paths["/api/v1/user/step-up"].(gin.H)
	if !ok {
		t.Fatal("missing OpenAPI path /api/v1/user/step-up")
	}
	operation, ok := route["post"].(gin.H)
	if !ok || operation["summary"] == nil {
		t.Fatal("missing operation post /api/v1/user/step-up")
	}
	description, _ := operation["description"].(string)
	for _, want := range []string{"X-Step-Up-Token", "step_up_required", "428"} {
		if !strings.Contains(description, want) {
			t.Fatalf("step-up description must document %q, got %q", want, description)
		}
	}
	responses, ok := operation["responses"].(gin.H)
	if !ok || responses["429"] == nil {
		t.Fatal("step-up endpoint must document password throttling (429)")
	}
	schemas := openAPIAdminSchemas()
	if schemas["StepUpRequest"] == nil || schemas["StepUpCredential"] == nil {
		t.Fatal("step-up schemas missing from the admin openapi docs")
	}
}

// TestStepUpRoleDenialPrecedesStepUpGate 403 必须先于 step-up 的 428 落地: 无权限者拿到的是
// 角色拒绝而不是密码确认框, 428 也不该比 403 更早把「有这么个高危端点」透露出去。
// 两个闸门都排在业务 handler 之前 —— 拒绝必须零副作用。
func TestStepUpRoleDenialPrecedesStepUpGate(t *testing.T) {
	_, adminToken := stepupTestEnv(t)
	plain, err := op.UserCreate("stepup-plain", "", "password123", model.RoleUser, model.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = op.UserDelete(plain.ID) })
	plainToken, _, err := auth.GenerateJWTToken(plain, 3600)
	if err != nil {
		t.Fatal(err)
	}
	plainStep, _, err := auth.IssueStepUpToken(plain, plainToken)
	if err != nil {
		t.Fatal(err)
	}

	engine := gin.New()
	if err := router.RegisterAll(engine); err != nil {
		t.Fatal(err)
	}
	request := func(token, stepUp string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/user/manage/create", strings.NewReader(`{"username":"intruder","password":"intruder123","role":"admin"}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "auth", Value: token})
		if stepUp != "" {
			req.Header.Set(middleware.StepUpTokenHeader, stepUp)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}

	// 无权限者先撞角色闸门: 403, 且不是伪装成 403 的 step-up 挑战。
	w := request(plainToken, "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("role denial must come first: got %d want 403: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "step_up_required") {
		t.Fatalf("403 must not masquerade as a step-up challenge: %s", w.Body.String())
	}
	// 就算带着无权限者自己的有效凭证, 凭证也不能把无权限变成有权限。
	if w := request(plainToken, plainStep); w.Code != http.StatusForbidden {
		t.Fatalf("valid credential must not bypass the role guard: %d %s", w.Code, w.Body.String())
	}
	// 有权限者没带凭证: 这才轮到 step-up 闸门(证明 403 在前不是因为路由根本没到 step-up)。
	if w := request(adminToken, ""); w.Code != http.StatusPreconditionRequired {
		t.Fatalf("authorized caller without credential: got %d want 428: %s", w.Code, w.Body.String())
	}
	// 三次拒绝都发生在业务处理之前: 一个用户都没被创建。
	var count int64
	if err := db.GetDB().Model(&model.User{}).Where("username = ?", "intruder").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rejected requests must have no side effects, created=%d", count)
	}
}

// gateTestEnvNoMarker 一套「默认口令尚未修改」的环境: 不翻 admin_default_pwd_changed 标记。
func gateTestEnvNoMarker(t *testing.T) (model.User, string) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "stepup-gate.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := auth.InitSecret(); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	// 显式置 false 而不是依赖种子默认值: settingCache 是进程级的, 前面测试翻过的标记会串场。
	if err := op.SettingUpsert(model.SettingKeyAdminDefaultPwdChanged, "false"); err != nil {
		t.Fatal(err)
	}
	user, err := op.UserCreate("gate-admin", "", "password123", model.RoleAdmin, model.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = op.UserDelete(user.ID) })
	if op.AdminDefaultPasswordChanged() {
		t.Fatal("gate env must start with the default-password gate still armed")
	}
	token, _, err := auth.GenerateJWTToken(user, 3600)
	if err != nil {
		t.Fatal(err)
	}
	return user, token
}

// TestStepUpCredentialDoesNotBypassDefaultPasswordGate step-up 凭证与「默认口令已改」标记是两把
// 完全独立的闸, 互不顶替: 标记未翻时连凭证都换不到(端点同样在改密闸门内), 即便凭证直接
// 签发出来也照样被改密闸门拦下 —— 拦人的不是 step-up, 二者不可混淆。
func TestStepUpCredentialDoesNotBypassDefaultPasswordGate(t *testing.T) {
	user, sessionToken := gateTestEnvNoMarker(t)
	engine := gin.New()
	if err := router.RegisterAll(engine); err != nil {
		t.Fatal(err)
	}
	request := func(path, body, stepUp string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "auth", Value: sessionToken})
		if stepUp != "" {
			req.Header.Set(middleware.StepUpTokenHeader, stepUp)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}

	// ① 换凭证端点自己也在改密闸门内: 标记未翻时连 step-up 都做不成, 不给「先换凭证再改密」的绕行。
	w := request("/api/v1/user/step-up", `{"password":"password123"}`, "")
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "change the default admin password first") {
		t.Fatalf("step-up must stay behind the default-password gate: %d %s", w.Code, w.Body.String())
	}
	// ② 凭证即便直接签发(模拟任何途径拿到了), 也顶替不了默认改密标记。
	minted, _, err := auth.IssueStepUpToken(user, sessionToken)
	if err != nil {
		t.Fatal(err)
	}
	w = request("/api/v1/user/manage/update", `{"id":999999,"balance":1}`, minted)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "change the default admin password first") {
		t.Fatalf("step-up credential must not unlock the default-password gate: %d %s", w.Code, w.Body.String())
	}
	// ③ 对照: 标记一翻, 同一枚凭证立刻能过 step-up 闸 —— 说明②里拦人的确实是改密闸门。
	if err := op.SettingUpsert(model.SettingKeyAdminDefaultPwdChanged, "true"); err != nil {
		t.Fatal(err)
	}
	if w := request("/api/v1/user/manage/update", `{"id":999999,"balance":1}`, minted); w.Code == http.StatusForbidden || w.Code == http.StatusPreconditionRequired {
		t.Fatalf("with the deployment gate cleared the credential must pass step-up: %d %s", w.Code, w.Body.String())
	}
}

// TestChangePasswordBootstrapIsNotStepUpGated 「改密本身」必须免 step-up: 否则默认口令未改时
// 换不到凭证就得先改密、改密又要求凭证, 死循环。这里把整个 bootstrap 走完。
func TestChangePasswordBootstrapIsNotStepUpGated(t *testing.T) {
	user, sessionToken := gateTestEnvNoMarker(t)
	engine := gin.New()
	if err := router.RegisterAll(engine); err != nil {
		t.Fatal(err)
	}
	request := func(session, path, body, stepUp string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "auth", Value: session})
		if stepUp != "" {
			req.Header.Set(middleware.StepUpTokenHeader, stepUp)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}

	// 无任何 step-up 凭证直接改默认口令: 必须成功(这是改密闸门豁免的唯一写操作)。
	w := request(sessionToken, "/api/v1/user/change-password", `{"old_password":"password123","new_password":"newpassword456"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("change-password must not require step-up (bootstrap loop): %d %s", w.Code, w.Body.String())
	}
	if !op.AdminDefaultPasswordChanged() {
		t.Fatal("changing the default password must clear the deployment gate")
	}
	// 改密换 token_version, 旧会话作废; 重新登录后用新口令换凭证, 链路彻底打通。
	user, err := op.UserGetByID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	freshSession, _, err := auth.GenerateJWTToken(user, 3600)
	if err != nil {
		t.Fatal(err)
	}
	w = request(freshSession, "/api/v1/user/step-up", `{"password":"newpassword456"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("step-up after bootstrap: %d %s", w.Code, w.Body.String())
	}
	token, _ := stepupBody(t, w)["token"].(string)
	if token == "" {
		t.Fatalf("step-up response must carry a credential: %s", w.Body.String())
	}
	if w := request(freshSession, "/api/v1/user/manage/update", `{"id":999999,"balance":1}`, token); w.Code == http.StatusPreconditionRequired {
		t.Fatalf("credential must pass the step-up gate after bootstrap: %d %s", w.Code, w.Body.String())
	}
}

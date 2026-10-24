package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/conf"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/auth"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// TestRequireStepUpGate 高危操作闸门的准入矩阵: 只有"本用户 + 本令牌版本 + 本登录会话"的
// 短期凭证放行, 其余一律 428 + step_up_required, 且被拦下的请求绝不进业务处理。
func TestRequireStepUpGate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	gateTestDB(t)
	if err := op.SettingUpsert(model.SettingKeyAdminDefaultPwdChanged, "true"); err != nil {
		t.Fatal(err)
	}
	if err := auth.InitSecret(); err != nil {
		t.Fatal(err)
	}
	user, err := op.UserCreate("stepup-gate-admin", "", "password123", model.RoleAdmin, model.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = op.UserDelete(user.ID) })

	sessionToken, _, err := auth.GenerateJWTToken(user, 3600)
	if err != nil {
		t.Fatal(err)
	}
	// 另一枚"同一账号的另一段登录": 有效期不同 -> JWT 不同 -> 会话指纹不同。
	otherSession, _, err := auth.GenerateJWTToken(user, 7200)
	if err != nil {
		t.Fatal(err)
	}
	stepToken, _, err := auth.IssueStepUpToken(user, sessionToken)
	if err != nil {
		t.Fatal(err)
	}

	var executed int
	router := gin.New()
	router.Use(Auth())
	router.POST("/high-risk", RequireStepUp(), func(c *gin.Context) {
		executed++
		c.Status(http.StatusOK)
	})

	perform := func(session, stepUp string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/high-risk", nil)
		if session != "" {
			request.AddCookie(&http.Cookie{Name: "auth", Value: session})
		}
		if stepUp != "" {
			request.Header.Set(StepUpTokenHeader, stepUp)
		}
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder
	}

	// 缺凭证: 明确的 step-up 错误(前端据此弹密码确认), 业务处理一次都没跑。
	w := perform(sessionToken, "")
	if w.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing step-up credential: got %d want 428: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"error_code":"step_up_required"`) {
		t.Fatalf("step-up error must carry the machine-readable code: %s", w.Body.String())
	}
	if executed != 0 {
		t.Fatalf("rejected request must not reach the handler, executed=%d", executed)
	}

	// 有效凭证: 放行且只放行一次。
	if w := perform(sessionToken, stepToken); w.Code != http.StatusOK {
		t.Fatalf("valid step-up credential rejected: %d %s", w.Code, w.Body.String())
	}
	if executed != 1 {
		t.Fatalf("handler must run exactly once, executed=%d", executed)
	}

	// 会话不匹配: 凭证换到另一段登录上作废。
	if w := perform(otherSession, stepToken); w.Code != http.StatusPreconditionRequired {
		t.Fatalf("credential from another session accepted: %d %s", w.Code, w.Body.String())
	}

	// 登录 JWT 不能顶替 step-up 凭证。
	if w := perform(sessionToken, sessionToken); w.Code != http.StatusPreconditionRequired {
		t.Fatalf("login JWT accepted as step-up credential: %d %s", w.Code, w.Body.String())
	}

	// 改密: token_version 递增 —— 旧登录会话先被 Auth 拦成 401, 凭证的版本绑定在
	// auth 包单测里单独钉死(见 TestStepUpTokenBindsUserVersionAndSession)。
	if err := op.UserChangePassword(user.ID, "password123", "newpassword456"); err != nil {
		t.Fatal(err)
	}
	if w := perform(sessionToken, stepToken); w.Code != http.StatusUnauthorized {
		t.Fatalf("old session must die with a password change: %d %s", w.Code, w.Body.String())
	}
	// 改密后重新登录: 旧 step-up 凭证不能跟着新会话复活。
	user, err = op.UserGetByID(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	freshSession, _, err := auth.GenerateJWTToken(user, 3600)
	if err != nil {
		t.Fatal(err)
	}
	if w := perform(freshSession, stepToken); w.Code != http.StatusPreconditionRequired {
		t.Fatalf("stale credential must not revive under a new session: %d %s", w.Code, w.Body.String())
	}

	// 没有登录会话的请求根本不该到这一步(401 由 Auth 负责)。
	if w := perform("", stepToken); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous request: got %d want 401", w.Code)
	}
}

// TestRequireStepUpRejectsForgedAndForeign 伪造与冒用矩阵: 乱码头、伪签名(载荷形状齐全但
// 换一把密钥签)、篡改载荷、他人凭证 —— 一律按「需要二次验证」处理: 统一 428 + step_up_required
// 且响应体与缺凭证完全一致(不给攻击者分辨是哪一项被识破的机会), 业务处理一次都不跑。
func TestRequireStepUpRejectsForgedAndForeign(t *testing.T) {
	gin.SetMode(gin.TestMode)
	gateTestDB(t)
	if err := op.SettingUpsert(model.SettingKeyAdminDefaultPwdChanged, "true"); err != nil {
		t.Fatal(err)
	}
	if err := auth.InitSecret(); err != nil {
		t.Fatal(err)
	}
	owner, err := op.UserCreate("stepup-forge-owner", "", "password123", model.RoleAdmin, model.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = op.UserDelete(owner.ID) })
	stranger, err := op.UserCreate("stepup-forge-stranger", "", "password123", model.RoleAdmin, model.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = op.UserDelete(stranger.ID) })

	sessionToken, _, err := auth.GenerateJWTToken(owner, 3600)
	if err != nil {
		t.Fatal(err)
	}
	valid, _, err := auth.IssueStepUpToken(owner, sessionToken)
	if err != nil {
		t.Fatal(err)
	}
	// 他人凭证: 绑的是 stranger 的身份与 stranger 的会话, 拿到 owner 的请求上两个都对不上。
	strangerSession, _, err := auth.GenerateJWTToken(stranger, 3600)
	if err != nil {
		t.Fatal(err)
	}
	strangerStep, _, err := auth.IssueStepUpToken(stranger, strangerSession)
	if err != nil {
		t.Fatal(err)
	}
	// 伪签: 载荷与真凭证形状完全一致, 但签名密钥不是本服务的 —— 只有持密钥者能签出真凭证。
	now := time.Now()
	forged, err := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.StepUpClaims{
		UserID:       owner.ID,
		TokenVersion: owner.TokenVersion,
		Session:      auth.SessionFingerprint(sessionToken),
		Purpose:      auth.StepUpPurpose,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    conf.APP_NAME,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
		},
	}).SignedString([]byte("not-the-server-signing-secret"))
	if err != nil {
		t.Fatal(err)
	}
	// 篡改: 有效凭证动一个载荷字符, 签名校验当场失败。
	parts := strings.Split(valid, ".")
	if len(parts) != 3 {
		t.Fatalf("unexpected credential shape: %s", valid)
	}
	parts[1] = "x" + parts[1][1:]
	tampered := strings.Join(parts, ".")

	var executed int
	router := gin.New()
	router.Use(Auth())
	router.POST("/high-risk", RequireStepUp(), func(c *gin.Context) {
		executed++
		c.Status(http.StatusOK)
	})
	perform := func(stepUp string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/high-risk", nil)
		request.AddCookie(&http.Cookie{Name: "auth", Value: sessionToken})
		if stepUp != "" {
			request.Header.Set(StepUpTokenHeader, stepUp)
		}
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder
	}

	baseline := perform("")
	if baseline.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing credential: got %d want 428", baseline.Code)
	}
	for name, candidate := range map[string]string{
		"garbage header":    "definitely-not-a-jwt",
		"forged signature":  forged,
		"tampered payload":  tampered,
		"another user's":    strangerStep,
		"zero-value header": strings.Repeat("A", 43),
	} {
		w := perform(candidate)
		if w.Code != http.StatusPreconditionRequired {
			t.Fatalf("%s: got %d want 428: %s", name, w.Code, w.Body.String())
		}
		if w.Body.String() != baseline.Body.String() {
			t.Fatalf("%s: rejection must look identical to a missing credential\n got %s\nwant %s", name, w.Body.String(), baseline.Body.String())
		}
	}
	if executed != 0 {
		t.Fatalf("forged/foreign credentials must never reach the handler, executed=%d", executed)
	}
}

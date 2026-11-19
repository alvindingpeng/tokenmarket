package handlers

import (
	"encoding/json"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/secretstore"
	"github.com/alvindingpeng/switchyard/internal/server/auth"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/alvindingpeng/switchyard/internal/totp"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTOTPLoginContractAndStepUp(t *testing.T) {
	u, session := stepupTestEnv(t)
	secretstore.Reset()
	t.Cleanup(secretstore.Reset)
	if err := secretstore.Init(secretstore.KEKConfig{Inline: strings.Repeat("37", 32)}); err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	if err := router.RegisterAll(engine); err != nil {
		t.Fatal(err)
	}
	request := func(path, body string, cookie bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "192.0.2.201:1234"
		if cookie {
			req.AddCookie(&http.Cookie{Name: "auth", Value: session})
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}
	for _, p := range []string{"setup", "enable", "disable"} {
		w := request("/api/v1/user/2fa/"+p, "{}", true)
		if w.Code != 428 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s gate: %d %s", p, w.Code, w.Body.String())
		}
	}
	secret, _, err := op.UserTOTPSetup(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := totp.Generate(secret, time.Now().Unix())
	codes, err := op.UserTOTPEnable(u.ID, code)
	if err != nil {
		t.Fatal(err)
	}
	defer auth.LoginSuccess("ip:192.0.2.201")
	defer auth.LoginSuccess("u:" + u.Username)
	loginBody := func(code string) string {
		b, _ := json.Marshal(map[string]string{"username": u.Username, "password": "password123", "totp_code": code})
		return string(b)
	}
	for _, tc := range []struct{ code, message string }{{"", "two_factor_required"}, {"bad", "invalid verification code"}, {code, "invalid verification code"}} {
		w := request("/api/v1/user/login", loginBody(tc.code), false)
		if w.Code != 401 || !strings.Contains(w.Body.String(), tc.message) || len(w.Result().Cookies()) != 0 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("login %s: %d %s", tc.message, w.Code, w.Body.String())
		}
	}
	w := request("/api/v1/user/login", loginBody(codes[0]), false)
	if w.Code != 200 || len(w.Result().Cookies()) == 0 {
		t.Fatalf("recovery login: %d %s", w.Code, w.Body.String())
	}
	// Prior 2FA failures count toward the existing login lockout; password success
	// alone must not reset them.
	if err := op.SettingUpsert(model.SettingKeyLoginMaxAttempts, "2"); err != nil {
		t.Fatal(err)
	}
	defer op.SettingUpsert(model.SettingKeyLoginMaxAttempts, "5")
	for i := 0; i < 2; i++ {
		w = request("/api/v1/user/login", loginBody("bad"), false)
		if w.Code != 401 {
			t.Fatalf("failure %d: %d", i, w.Code)
		}
	}
	w = request("/api/v1/user/login", loginBody(codes[1]), false)
	if w.Code != 429 {
		t.Fatalf("lockout reset before 2FA: %d %s", w.Code, w.Body.String())
	}
}

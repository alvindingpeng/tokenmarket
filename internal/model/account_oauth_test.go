
package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// 这一层守的是"刷新一次不会把账号刷废"。
//
// 上游每次刷新通常都会轮换 refresh_token, 旧值当场失效。若解析/回写有偏差,
// 账号会永久失效, 而表面症状只是"下一次刷新失败", 极易被误判成账号被封。

const codexRefreshOK = `{"access_token":"new-access","refresh_token":"rotated-refresh","expires_in":3600}`

func TestAccountOAuthParseRefreshResponseCodex(t *testing.T) {
	spec, err := AccountOAuthSpecOf(AccountPlatformCodex)
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	previous := AccountCredential{Platform: AccountPlatformCodex, RefreshToken: "old-refresh"}

	result, err := AccountOAuthParseRefreshResponse(spec, previous, []byte(codexRefreshOK), now)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if result.AccessToken != "new-access" {
		t.Fatalf("access = %q", result.AccessToken)
	}
	// 关键断言: 必须拿到轮换后的新值, 而不是沿用旧值。
	if result.RefreshToken != "rotated-refresh" {
		t.Fatalf("refresh = %q, want rotated-refresh", result.RefreshToken)
	}
	if !result.RefreshTokenRotated {
		t.Fatal("应当标记 refresh_token 已轮换")
	}
	if want := now.Add(time.Hour); !result.ExpiresAt.Equal(want) {
		t.Fatalf("expires = %v want %v", result.ExpiresAt, want)
	}
}

// 上游未轮换 refresh_token 是合法情况, 不能当成错误, 也不能把令牌清空。
func TestAccountOAuthParseRefreshResponseWithoutRotation(t *testing.T) {
	spec, _ := AccountOAuthSpecOf(AccountPlatformCodex)
	previous := AccountCredential{Platform: AccountPlatformCodex, RefreshToken: "keep-me"}
	raw := []byte(`{"access_token":"new-access","expires_in":60}`)

	result, err := AccountOAuthParseRefreshResponse(spec, previous, raw, time.Now())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if result.RefreshToken != "keep-me" {
		t.Fatalf("refresh = %q, want keep-me", result.RefreshToken)
	}
	if result.RefreshTokenRotated {
		t.Fatal("未轮换时不应标记 rotated")
	}
}

// Kiro 的响应字段是 camelCase。用 Codex 的字段名去读会得到空值 ——
// 这里钉住"按平台规格读字段", 而不是写死一套键名。
func TestAccountOAuthParseRefreshResponseKiroCamelCase(t *testing.T) {
	spec, err := AccountOAuthSpecOf(AccountPlatformKiro)
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	raw := []byte(`{"accessToken":"kiro-access","refreshToken":"kiro-rotated","expiresIn":1800}`)
	result, err := AccountOAuthParseRefreshResponse(spec, AccountCredential{RefreshToken: "kiro-old"}, raw, time.Now())
	if err != nil {
		t.Fatalf("parse kiro: %v", err)
	}
	if result.AccessToken != "kiro-access" || result.RefreshToken != "kiro-rotated" {
		t.Fatalf("kiro 字段读错: %+v", result)
	}

	// 反向: 给 Kiro 喂 Codex 的 snake_case 响应必须失败, 证明字段名没有归一化。
	if _, err := AccountOAuthParseRefreshResponse(spec, AccountCredential{RefreshToken: "x"}, []byte(`{"access_token":"a","refresh_token":"b"}`), time.Now()); err == nil {
		t.Fatal("Kiro 不认 snake_case 响应, 应当报错")
	}
}

// 刷新请求的形态也各不相同: Codex 是 form, Kiro 是 JSON 且必带 clientId/clientSecret。
func TestAccountOAuthRefreshRequestShapes(t *testing.T) {
	codexSpec, _ := AccountOAuthSpecOf(AccountPlatformCodex)
	contentType, body, err := AccountOAuthRefreshRequest(codexSpec, AccountCredential{RefreshToken: "rt-1"})
	if err != nil {
		t.Fatalf("codex request: %v", err)
	}
	if contentType != "application/x-www-form-urlencoded" {
		t.Fatalf("codex content-type = %q", contentType)
	}
	for _, want := range []string{"grant_type=refresh_token", "refresh_token=rt-1", "client_id="} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("codex form 缺少 %q: %s", want, body)
		}
	}

	kiroSpec, _ := AccountOAuthSpecOf(AccountPlatformKiro)
	contentType, body, err = AccountOAuthRefreshRequest(kiroSpec, AccountCredential{
		RefreshToken: "rt-2", ClientID: "cid", ClientSecret: "sec",
	})
	if err != nil {
		t.Fatalf("kiro request: %v", err)
	}
	if contentType != "application/json" {
		t.Fatalf("kiro content-type = %q", contentType)
	}
	var fields map[string]string
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("kiro body 不是 JSON: %v", err)
	}
	// Kiro 的请求体沿用 grant_type(refresh_token) 但字段名是 camelCase 的 clientId/clientSecret。
	// 三条都要断, 少任何一条都会让"请求发出去了但换不到令牌"变成无提示的失败。
	if fields["clientId"] != "cid" || fields["clientSecret"] != "sec" || fields["grant_type"] != "refresh_token" {
		t.Fatalf("kiro 请求体字段错误: %v", fields)
	}

	// 缺 clientSecret 必须当场失败: 拿空值去换令牌只会得到一个 401,
	// 而错误信息会指向"账号失效", 把排查引向错的方向。
	if _, _, err := AccountOAuthRefreshRequest(kiroSpec, AccountCredential{RefreshToken: "rt", ClientID: "cid"}); err == nil {
		t.Fatal("kiro 缺 clientSecret 应当报错")
	}
}

// 空 refresh_token 不能发请求; 未登记的平台不能猜端点。
func TestAccountOAuthGuards(t *testing.T) {
	codexSpec, _ := AccountOAuthSpecOf(AccountPlatformCodex)
	if _, _, err := AccountOAuthRefreshRequest(codexSpec, AccountCredential{RefreshToken: "  "}); err == nil {
		t.Fatal("空 refresh_token 应当报错")
	}
	if _, err := AccountOAuthSpecOf("unknown"); err == nil {
		t.Fatal("未登记平台应当报错")
	}
	// Cookie 类平台没有 OAuth 规格 —— 这是正确行为: 它们根本不该走刷新。
	if _, err := AccountOAuthSpecOf(AccountPlatformGemini); err == nil {
		t.Fatal("gemini 不应有 OAuth 刷新规格")
	}
}

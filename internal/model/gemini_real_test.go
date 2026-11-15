package model

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// 真实端点验证: 内置的 Gemini client 凭据必须被 Google 接受。
//
// 为什么必须真打端点: 内置那对 client_id/client_secret 是"从公开 npm 包里提取出来的常量",
// 单测只能验证它没被改错成别的样子, 验证不了它到底还被 Google 认不认。
// 而这恰恰是最容易出事的地方 —— 一个写错的 secret 会让所有 Gemini 登录静默失败,
// 上游回 invalid_client, 看起来像"账号坏了", 排查方向完全错。
// 这个 bug 本轮真的发生过一次(secret 被写成包里不存在的值), 靠人工核对才抓到, 太脆弱。
//
// 判定三种结果:
//
//	invalid_grant  -> 这一对 client_id/secret 有效(只是 refresh_token 是假的), 期望结果
//	invalid_client -> 凭据本身被拒, 必须让测试失败
//	网络不可达      -> Skip, 不让 CI 因为断网变红
//
// 用假的 refresh_token 是有意的: 本测试只回答"这对 client 凭据是否有效",
// 不涉及任何真实账号, 不产生任何真实授权。
func TestGeminiBuiltinClientAcceptedByRealEndpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过真实端点验证")
	}
	// 显式清空配置层, 保证测的是**内置默认值**本身, 而不是环境里恰好配了什么。
	t.Setenv(geminiOAuthClientIDEnv, "")
	t.Setenv(geminiOAuthClientSecretEnv, "")
	originalGetter := GeminiOAuthSettingGetter
	GeminiOAuthSettingGetter = nil
	defer func() { GeminiOAuthSettingGetter = originalGetter }()

	id, sec := GeminiOAuthClient()
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {id},
		"client_secret": {sec},
		"refresh_token": {"fake_refresh_token_for_probe"},
	}
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.PostForm("https://oauth2.googleapis.com/token", form)
	if err != nil {
		t.Skipf("网络不可达, 跳过: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	body := string(raw)
	t.Logf("http=%d body=%s", resp.StatusCode, body)

	if strings.Contains(body, "invalid_client") {
		t.Fatalf("内置 Gemini client 凭据被 Google 拒绝(invalid_client)。"+
			"说明内置的 id/secret 是错的, 上线后所有 Gemini 登录都会失败: %s", body)
	}
	if !strings.Contains(body, "invalid_grant") {
		t.Fatalf("期望 invalid_grant(证明这对凭据有效), 实际响应: %s", body)
	}
}

package signin

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// 真实端点验证: 生成的授权 URL 必须被上游接受, 而不是我们自己觉得对。
//
// 这是"实现自洽但上游不认"的典型场景 —— 参数名写错一个字母, 授权页会直接报错,
// 而单测只验证了我们按自己的规格生成 URL, 验证不了对方的规格。
//
// 用 HEAD/GET 打真实授权端点看状态码:
//   200/302 -> 端点与参数被接受
//   400     -> 参数有问题(通常带 error 说明)
// 网络不可达时跳过, 不让 CI 因为断网变红。
func TestAuthorizeURLAcceptedByRealEndpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过真实端点验证")
	}
	cases := []string{"claude", "codex", "gemini"}

	for _, platform := range cases {
		t.Run(platform, func(t *testing.T) {
			if platform == "gemini" {
				t.Setenv("SWITCHYARD_GEMINI_OAUTH_CLIENT_ID", "test-client-for-url-shape")
				t.Setenv("SWITCHYARD_GEMINI_OAUTH_CLIENT_SECRET", "test-secret")
			}
			provider, err := ProviderOf(platform)
			if err != nil {
				t.Skipf("%s 未配置, 跳过: %v", platform, err)
			}
			store := NewStore()
			flow, err := store.Begin(provider)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}

			client := &http.Client{
				Timeout: 20 * time.Second,
				// 不跟随跳转: 我们要看的就是这个端点对参数的反应, 跟随后错误会被藏起来。
				CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
					return http.ErrUseLastResponse
				},
			}
			req, err := http.NewRequest(http.MethodGet, flow.AuthorizeURL, nil)
			if err != nil {
				t.Fatalf("构造请求: %v", err)
			}
			req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; switchyard-signin-test)")
			resp, err := client.Do(req)
			if err != nil {
				t.Skipf("网络不可达, 跳过 %s: %v", platform, err)
			}
			defer resp.Body.Close()

			body, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
			// 地区封锁必须与"参数被拒"分开: 前者是这台机器的出口地区不被该端点接受,
			// 与我们的实现无关 —— 用户在他所在地区可能完全正常。把它判成失败会让
			// 这个测试永远红着, 然后所有人都开始忽略它。
			if resp.StatusCode == http.StatusForbidden && strings.Contains(string(body), "unsupported_country_region_territory") {
				t.Skipf("%s 授权端点按地区封锁当前出口(与实现无关): %s", platform, strings.TrimSpace(string(body)))
			}
			// 200 是正常渲染授权页; 302/307 是端点接受参数并跳登录页。
			// 其他 4xx 说明参数组合有问题, 必须失败而不是放过。
			if resp.StatusCode >= 400 {
				t.Fatalf("%s 授权端点拒绝了生成的 URL: HTTP %d %.200s", platform, resp.StatusCode, string(body))
			}
			t.Logf("%s 授权端点接受 URL: HTTP %d", platform, resp.StatusCode)
		})
	}
}

// 端点本身的可达性: 写错的域名会得到 DNS 失败, 与"参数被拒"是完全不同的排查方向。
func TestTokenEndpointsReachable(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过真实端点验证")
	}
	for _, platform := range []string{"claude", "codex"} {
		p, err := ProviderOf(platform)
		if err != nil {
			t.Skipf("%s: %v", platform, err)
		}
		// 用 OPTIONS 探测: 不提交任何凭据, 只确认域名与路径存在。
		req, _ := http.NewRequest(http.MethodPost, p.TokenURL, strings.NewReader(""))
		client := &http.Client{Timeout: 20 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			t.Skipf("网络不可达, 跳过 %s: %v", platform, err)
		}
		resp.Body.Close()
		// 400/401 都说明端点存在且拒绝空请求 —— 正是我们要的。404 则说明路径写错了。
		if resp.StatusCode == http.StatusNotFound {
			t.Fatalf("%s 令牌端点 404: %s", platform, p.TokenURL)
		}
		t.Logf("%s 令牌端点存在: HTTP %d", platform, resp.StatusCode)
	}
}

package op

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// 订阅渠道把「整包账号 JSON」存在 ChannelKey.Key 里, 而探测链要的是 access_token。
//
// 真实故障(生产上遇到的):
//
//	gemini cli loadCodeAssist: status=401 body={... Invalid Credentials ...}
//
// 原因不是账号失效, 而是把 JSON 整串塞进了 Authorization 头 ——
// 上游只看到一串不是令牌的东西, 于是回 401, 而报错完全指不到"你把 JSON 当 token 用了"。
//
// 这个测试用「上游只接受正确令牌」的假服务器来区分两种行为:
//   - 走 ProbeChannelKey(正确): 上游收到 access_token, 返回模型清单 → 通过
//   - 直接传 channelKey.Key(错误): Authorization 里是整包 JSON → 上游 401 → 报错
func TestProbeChannelKeyUnwrapsAccountJSON(t *testing.T) {
	const wantToken = "ya29.correct-access-token"
	const accountJSON = `{"access_token":"` + wantToken + `","refresh_token":"r","expiry_date":4102444800000}`

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		// 上游只认正确令牌: 这样"传了 JSON"一定会失败, 不依赖它的具体格式。
		if gotAuth != "Bearer "+wantToken {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":{"code":401,"message":"Request had invalid authentication credentials.","status":"UNAUTHENTICATED"}}`)
			return
		}
		switch {
		case strings.Contains(r.URL.Path, "loadCodeAssist"):
			fmt.Fprint(w, `{"cloudaicompanionProject":"proj-1","currentTier":{"id":"free-tier"}}`)
		case strings.Contains(r.URL.Path, "retrieveUserQuota"):
			fmt.Fprint(w, `{"quota":{"buckets":[{"modelId":"gemini-2.5-pro","remainingFraction":0.9}]}}`)
		default:
			fmt.Fprint(w, `{}`)
		}
	}))
	defer srv.Close()

	target := model.ChannelConfig{
		Dialect: model.DialectSubscription,
		BaseURL: srv.URL,
	}
	channelKey := model.ChannelKey{ChannelKeyConfig: model.ChannelKeyConfig{
		Key:             accountJSON,
		AccountKind:     model.AccountKindOAuth,
		AccountPlatform: model.AccountPlatformAntigravity,
	}}

	models, err := ProbeChannelKey(srv.Client(), target, channelKey, context.Background())
	if err != nil {
		t.Fatalf("账号 JSON 必须被解成 access_token 再发出去, 实际报错: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("探测必须带回模型清单")
	}
	// 反向断言: 上游收到的必须只是令牌, 不能是整包 JSON。
	if strings.Contains(gotAuth, "refresh_token") {
		t.Fatalf("Authorization 里带了整包账号 JSON(泄漏且必然 401): %q", gotAuth)
	}
}

// 静态凭据(非 oauth)原样透传: 不能因为修订阅渠道就把普通渠道的令牌改坏。
func TestProbeChannelKeyPassesPlainTokenThrough(t *testing.T) {
	const token = "sk-plain-static-key"
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if gotAuth != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":{"code":401}}`)
			return
		}
		switch {
		case strings.Contains(r.URL.Path, "loadCodeAssist"):
			fmt.Fprint(w, `{"cloudaicompanionProject":"p","currentTier":{"id":"t"}}`)
		case strings.Contains(r.URL.Path, "retrieveUserQuota"):
			fmt.Fprint(w, `{"quota":{"buckets":[{"modelId":"m","remainingFraction":1}]}}`)
		}
	}))
	defer srv.Close()

	models, err := ProbeChannelKey(
		srv.Client(),
		model.ChannelConfig{Dialect: model.DialectSubscription, BaseURL: srv.URL},
		model.ChannelKey{ChannelKeyConfig: model.ChannelKeyConfig{Key: token}},
		context.Background(),
	)
	if err != nil {
		t.Fatalf("静态凭据必须原样透传, 实际: %v", err)
	}
	if gotAuth != "Bearer "+token {
		t.Fatalf("静态凭据被改动了: %q", gotAuth)
	}
	_ = models
}

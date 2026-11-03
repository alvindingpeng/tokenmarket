package op

import (
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// TestEgressErrorNeverLeaksCredentials 探查: 出口地址带用户名密码时, 校验失败的错误信息里
// 会不会把凭据带出去。错误信息会经 HTTP 响应回到浏览器(toast)并可能进日志。
func TestEgressErrorNeverLeaksCredentials(t *testing.T) {
	ctx, _, admin := reviewTestEnv(t)
	const secret = "Sup3rSecretPw"

	cases := []struct {
		name string
		url  string
	}{
		{"私网地址", "http://user:" + secret + "@172.16.0.29:7890"},
		{"协议不符", "socks5://user:" + secret + "@1.2.3.4:1080"},
		{"畸形地址", "http://user:" + secret + "@[::1"},
		{"缺少主机", "http://user:" + secret + "@"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			detail := reviewTestDetail("leak-" + string(rune('a'+i)))
			detail.Egresses = []model.ChannelEgress{{Name: "egress-1", Kind: model.EgressKindHTTP, URL: tc.url, Enabled: true}}
			_, err := ChannelCreate(detail, admin, ctx)
			if err == nil {
				t.Skip("该形式被接受, 无错误信息可泄漏")
			}
			msg := err.Error()
			if strings.Contains(msg, secret) {
				t.Errorf("凭据泄漏进错误信息: %q", msg)
			} else {
				t.Logf("安全: %q", msg)
			}
		})
	}
}

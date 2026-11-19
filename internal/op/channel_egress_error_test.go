package op

import (
	"errors"
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// TestChannelEgressPrivateAddressIsInvalidInput 私网出口地址必须被归类成"输入错误"。
//
// 这是 2026-11-03 线上问题的回归。当天的现象是"配好出口信息后无法保存":
//  1. 地址是私网段 -> netguard 的 SSRF 防护拒绝(这一步是**正确**行为);
//  2. ChannelUpdate 返回的错误被 handler 一律映射成 HTTP 500, 表现为服务端故障;
//  3. 前端更新分支没有 onError, 错误被静默丢弃 —— 管理员连原因都看不到。
//
// 部署时实测响应体 97 字节, 与 ChannelUpdate 对该地址返回的错误逐字节吻合。
//
// 断言三件事: 错误可被 errors.Is 识别(handler 据此回 400)、原因点名到具体出口与地址、
// 失败时事务整体回滚不留半截配置。
func TestChannelEgressPrivateAddressIsInvalidInput(t *testing.T) {
	ctx, _, admin := reviewTestEnv(t)
	// 刻意不打开 allow_private_upstream: 本用例验证的正是"默认拦截私网出口"这条路径。

	created, err := ChannelCreate(reviewTestDetail("egress-private"), admin, ctx)
	if err != nil {
		t.Fatalf("创建渠道: %v", err)
	}

	update := *created
	update.Egresses = []model.ChannelEgress{
		{Name: "egress-1", Kind: model.EgressKindHTTP, URL: "http://192.168.1.1:7890", Enabled: true},
	}
	_, err = ChannelUpdate(&update, admin, ctx)
	if err == nil {
		t.Fatal("私网出口地址必须被拒绝")
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("应归类为输入错误(handler 据此回 400 而非 500), 实得: %v", err)
	}
	// 原因必须点名是哪条出口、哪个地址, 否则管理员不知道该改哪一项。
	msg := err.Error()
	for _, want := range []string{"egress-1", "192.168.1.1"} {
		if !strings.Contains(msg, want) {
			t.Errorf("失败原因应点名 %q, 实得 %q", want, msg)
		}
	}
	// 保存失败不得留下半截配置: 否则下一次保存会基于一个用户看不见的中间状态。
	var count int64
	if err := db.GetDB().Model(&model.ChannelEgress{}).Where("channel_id = ?", created.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("保存失败却写入了 %d 条出口", count)
	}
}

// TestChannelEgressPublicAddressesAccepted 公网出口地址必须放行。
// SSRF 防护拦的是内网: 若连公网地址也拦, 出口池功能本身就等于关闭了 ——
// 这类"防护过严"的回归不会有任何报错, 只会表现为"什么地址都存不进去"。
func TestChannelEgressPublicAddressesAccepted(t *testing.T) {
	ctx, _, admin := reviewTestEnv(t)

	cases := []struct {
		name string
		kind model.EgressKind
		url  string
	}{
		{"http", model.EgressKindHTTP, "http://203.0.113.9:8080"},
		{"https", model.EgressKindHTTP, "https://proxy.example.com:8443"},
		{"socks5", model.EgressKindSOCKS5, "socks5://203.0.113.9:1080"},
		{"socks5h", model.EgressKindSOCKS5, "socks5h://203.0.113.9:1080"},
		{"带认证", model.EgressKindHTTP, "http://user:pa%40ss@203.0.113.9:8080"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			detail := reviewTestDetail("egress-public-" + string(rune('a'+i)))
			detail.Egresses = []model.ChannelEgress{{Name: "egress-1", Kind: tc.kind, URL: tc.url, Enabled: true}}
			saved, err := ChannelCreate(detail, admin, ctx)
			if err != nil {
				t.Fatalf("%s %s 应被接受: %v", tc.kind, tc.url, err)
			}
			if len(saved.Egresses) != 1 {
				t.Fatalf("出口数量不对: %+v", saved.Egresses)
			}
			// 回读值内嵌凭据时要掩码(见 TestEgressURLCredentialsMaskedOnReadback),
			// 无凭据的地址必须原样返回。
			want := tc.url
			if strings.Contains(tc.url, "@") {
				if !strings.Contains(saved.Egresses[0].URL, egressMaskMark) {
					t.Fatalf("带凭据的地址应掩码回显: %q", saved.Egresses[0].URL)
				}
				if strings.Contains(saved.Egresses[0].URL, "pa%40ss") {
					t.Fatalf("回显泄漏了密码: %q", saved.Egresses[0].URL)
				}
				want = "" // 已单独断言
			}
			if want != "" && saved.Egresses[0].URL != want {
				t.Fatalf("出口地址未原样返回: want %q got %q", want, saved.Egresses[0].URL)
			}
		})
	}
}

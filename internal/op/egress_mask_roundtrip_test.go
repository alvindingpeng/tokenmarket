package op

import (
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// TestEgressURLCredentialsMaskedOnReadback 出口地址内嵌的凭据不得明文回显。
//
// 回显出口地址此前与紧邻的凭据处理完全不一致: channel_keys 的 key 走掩码, 出口 URL 原样返回。
// 代理地址常用 http://user:pass@host:port, 明文回显意味着密码会经 API 进浏览器,
// 被 XSS/会话劫持/抓包带走, 也直接显示在编辑页上。
func TestEgressURLCredentialsMaskedOnReadback(t *testing.T) {
	ctx, _, admin := reviewTestEnv(t)
	const password = "pa%40ss"

	detail := reviewTestDetail("egress-mask")
	detail.Egresses = []model.ChannelEgress{
		{Name: "egress-1", Kind: model.EgressKindHTTP, URL: "http://user:" + password + "@203.0.113.9:8080", Enabled: true},
	}
	created, err := ChannelCreate(detail, admin, ctx)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(created.Egresses) != 1 {
		t.Fatalf("出口数量 = %d", len(created.Egresses))
	}
	got := created.Egresses[0].URL
	if strings.Contains(got, password) || strings.Contains(got, "ss") {
		t.Fatalf("回显泄漏了密码: %q", got)
	}
	if !strings.Contains(got, egressMaskMark) {
		t.Fatalf("回显应为掩码: %q", got)
	}
	// 掩码之外的信息要保留, 否则编辑表单不可读 —— 管理员得能认出是哪台机器、哪个账号。
	for _, keep := range []string{"http://", "user", "203.0.113.9:8080"} {
		if !strings.Contains(got, keep) {
			t.Errorf("掩码后应保留 %q, 实得 %q", keep, got)
		}
	}
	// 库内必须是明文原文: 掩码只是回显层的事, 转发路径要拿真地址去连。
	var row model.ChannelEgress
	if err := db.GetDB().Where("channel_id = ?", created.ID).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(row.URL, password) {
		t.Fatalf("库内地址被掩码污染(转发会连不上): %q", row.URL)
	}
}

// TestEgressMaskPlaceholderKeepsStoredURL 回显的掩码原样提交回来时, 必须保持库内真实地址。
//
// 这是最容易出事的一条: 编辑页只要改一下权重或地区, 整份配置都会回传, 其中就包含掩码过的地址。
// 若不识别掩码, 真实代理地址会被覆盖成 "http://user:****@host" —— 保存"成功", 而出口从此连不上。
// 与 syncChannelKeys 识别掩码占位是同一套约定。
func TestEgressMaskPlaceholderKeepsStoredURL(t *testing.T) {
	ctx, _, admin := reviewTestEnv(t)
	const realURL = "http://user:secret@203.0.113.9:8080"

	detail := reviewTestDetail("egress-mask-roundtrip")
	detail.Egresses = []model.ChannelEgress{{Name: "egress-1", Kind: model.EgressKindHTTP, URL: realURL, Weight: 1, Enabled: true}}
	created, err := ChannelCreate(detail, admin, ctx)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	masked := created.Egresses[0].URL
	if !strings.Contains(masked, egressMaskMark) {
		t.Fatalf("前提不成立: 回显未被掩码: %q", masked)
	}

	// 模拟界面: 把回显的掩码值原样提交, 只改了权重。
	update := *created
	update.Egresses = []model.ChannelEgress{{Name: "egress-1", Kind: model.EgressKindHTTP, URL: masked, Weight: 9, Enabled: true}}
	if _, err := ChannelUpdate(&update, admin, ctx); err != nil {
		t.Fatalf("update: %v", err)
	}

	var row model.ChannelEgress
	if err := db.GetDB().Where("channel_id = ?", created.ID).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.URL != realURL {
		t.Fatalf("掩码占位覆盖了真实地址:\n  want %q\n  got  %q", realURL, row.URL)
	}
	if row.Weight != 9 {
		t.Fatalf("同一份提交里其他字段应照常更新: weight = %d", row.Weight)
	}
}

// TestEgressPlainURLCanStillBeChanged 没有凭据的地址必须能正常改 —— 掩码召回逻辑不能把正常编辑也挡住。
func TestEgressPlainURLCanStillBeChanged(t *testing.T) {
	ctx, _, admin := reviewTestEnv(t)

	detail := reviewTestDetail("egress-plain-change")
	detail.Egresses = []model.ChannelEgress{{Name: "egress-1", Kind: model.EgressKindHTTP, URL: "http://203.0.113.9:8080", Enabled: true}}
	created, err := ChannelCreate(detail, admin, ctx)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	update := *created
	update.Egresses = []model.ChannelEgress{{Name: "egress-1", Kind: model.EgressKindHTTP, URL: "http://203.0.113.10:9090", Enabled: true}}
	if _, err := ChannelUpdate(&update, admin, ctx); err != nil {
		t.Fatalf("update: %v", err)
	}
	var row model.ChannelEgress
	if err := db.GetDB().Where("channel_id = ?", created.ID).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.URL != "http://203.0.113.10:9090" {
		t.Fatalf("无凭据地址应能正常修改, got %q", row.URL)
	}
}

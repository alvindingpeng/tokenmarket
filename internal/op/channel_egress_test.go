package op

import (
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// TestChannelEgressRoundTrip 覆盖出口池的完整读写链路:
// 创建落库 -> 缓存可读 -> 转发侧拿得到 -> 更新按名称增删改 -> 校验拦截非法配置。
// 出口配错不会在保存时报错, 只会在转发时炸, 所以这一路的每一步都要有断言。
func TestChannelEgressRoundTrip(t *testing.T) {
	ctx, _, admin := reviewTestEnv(t)
	// 用例特意用私网地址验证读写链路: 打开私网放行, SSRF 拒绝语义由专门用例覆盖。
	if err := SettingUpsert(model.SettingKeyAllowPrivateUpstream, "true"); err != nil {
		t.Fatalf("seed allow-private: %v", err)
	}

	detail := reviewTestDetail("egress-roundtrip")
	detail.Egresses = []model.ChannelEgress{
		{Name: "hk", Kind: model.EgressKindHTTP, URL: "http://10.0.0.1:3128", Region: "hk", Weight: 5, Enabled: true},
		{Name: "direct", Kind: model.EgressKindDirect, Enabled: true},
	}
	created, err := ChannelCreate(detail, admin, ctx)
	if err != nil {
		t.Fatalf("创建渠道: %v", err)
	}
	if len(created.Egresses) != 2 {
		t.Fatalf("创建后应返回 2 条出口, 实得 %d", len(created.Egresses))
	}
	byName := make(map[string]model.ChannelEgress, len(created.Egresses))
	for _, egress := range created.Egresses {
		if egress.ID == 0 {
			t.Fatalf("出口 %s 应已分配主键", egress.Name)
		}
		byName[egress.Name] = egress
	}
	if byName["hk"].Region != "HK" {
		t.Fatalf("地区码应规范为大写, 实得 %q", byName["hk"].Region)
	}
	if byName["direct"].URL != "" {
		t.Fatalf("直连出口不应保留地址, 实得 %q", byName["direct"].URL)
	}
	hkID := byName["hk"].ID

	// 转发路径每轮都从缓存取渠道, 缓存里必须有出口。
	cached, err := ChannelGet(created.ID)
	if err != nil {
		t.Fatalf("读取渠道缓存: %v", err)
	}
	if len(cached.Egresses) != 2 {
		t.Fatalf("缓存里应有 2 条出口, 实得 %d", len(cached.Egresses))
	}

	// 更新: 删掉 direct, 改 hk 的权重与地址。同名出口必须保留原主键, 否则历史成绩对不上。
	update := *created
	update.Egresses = []model.ChannelEgress{
		{Name: "hk", Kind: model.EgressKindHTTP, URL: "http://10.0.0.2:3128", Region: "HK", Weight: 9, Enabled: false},
	}
	updated, err := ChannelUpdate(&update, admin, ctx)
	if err != nil {
		t.Fatalf("更新渠道: %v", err)
	}
	if len(updated.Egresses) != 1 {
		t.Fatalf("更新后应剩 1 条出口, 实得 %d", len(updated.Egresses))
	}
	kept := updated.Egresses[0]
	if kept.ID != hkID {
		t.Fatalf("同名出口应保留原主键 %d, 实得 %d", hkID, kept.ID)
	}
	if kept.URL != "http://10.0.0.2:3128" || kept.Weight != 9 || kept.Enabled {
		t.Fatalf("出口字段未按提交更新: %+v", kept)
	}
	cached, err = ChannelGet(created.ID)
	if err != nil {
		t.Fatalf("重读渠道缓存: %v", err)
	}
	if len(cached.Egresses) != 1 || cached.Egresses[0].Weight != 9 {
		t.Fatalf("缓存未跟随更新: %+v", cached.Egresses)
	}
}

// TestChannelEgressValidation 非法出口配置必须在保存时就被拦住。
func TestChannelEgressValidation(t *testing.T) {
	ctx, _, admin := reviewTestEnv(t)

	cases := []struct {
		name    string
		egress  model.ChannelEgress
		wantErr bool
	}{
		{name: "协议与类型不符", egress: model.ChannelEgress{Name: "x", Kind: model.EgressKindHTTP, URL: "socks5://1.2.3.4:1080"}, wantErr: true},
		{name: "缺少代理地址", egress: model.ChannelEgress{Name: "x", Kind: model.EgressKindHTTP}, wantErr: true},
		{name: "未知类型", egress: model.ChannelEgress{Name: "x", Kind: "vpn"}, wantErr: true},
		{name: "缺少名称", egress: model.ChannelEgress{Kind: model.EgressKindDirect}, wantErr: true},
		{name: "合法直连", egress: model.ChannelEgress{Name: "x", Kind: model.EgressKindDirect}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			detail := reviewTestDetail("egress-validate-" + c.name)
			detail.Egresses = []model.ChannelEgress{c.egress}
			_, err := ChannelCreate(detail, admin, ctx)
			if c.wantErr && err == nil {
				t.Fatalf("应报错")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("不应报错: %v", err)
			}
		})
	}
}

// TestChannelEgressDuplicateName 重名出口必须被拒绝: 名称是增删改的定位键。
func TestChannelEgressDuplicateName(t *testing.T) {
	ctx, _, admin := reviewTestEnv(t)

	detail := reviewTestDetail("egress-duplicate")
	detail.Egresses = []model.ChannelEgress{
		{Name: "same", Kind: model.EgressKindDirect},
		{Name: "same", Kind: model.EgressKindDirect},
	}
	if _, err := ChannelCreate(detail, admin, ctx); err == nil {
		t.Fatalf("重名出口应报错")
	}
}

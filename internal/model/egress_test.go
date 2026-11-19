package model

import "testing"

// TestChannelEgressNormalize 覆盖出口配置的校验规则。
// 这些规则挡的是"能存进去但转发时才炸"的配置, 所以每种非法组合都要有用例。
func TestChannelEgressNormalize(t *testing.T) {
	cases := []struct {
		name    string
		egress  ChannelEgress
		wantErr bool
		check   func(t *testing.T, got ChannelEgress)
	}{
		{
			name:   "直连不需要地址",
			egress: ChannelEgress{Name: " 直连 ", Kind: EgressKindDirect, URL: "http://ignored:1"},
			check: func(t *testing.T, got ChannelEgress) {
				if got.Name != "直连" {
					t.Fatalf("名称应去掉两端空白, 实得 %q", got.Name)
				}
				if got.URL != "" {
					t.Fatalf("直连应清空地址, 实得 %q", got.URL)
				}
			},
		},
		{
			name:   "http 代理",
			egress: ChannelEgress{Name: "hk", Kind: EgressKindHTTP, URL: " http://10.0.0.1:3128 ", Region: "hk"},
			check: func(t *testing.T, got ChannelEgress) {
				if got.URL != "http://10.0.0.1:3128" {
					t.Fatalf("地址应去空白, 实得 %q", got.URL)
				}
				if got.Region != "HK" {
					t.Fatalf("地区码应统一大写, 实得 %q", got.Region)
				}
			},
		},
		{
			name:   "https 代理",
			egress: ChannelEgress{Name: "us", Kind: EgressKindHTTP, URL: "https://proxy.example.com:8443"},
		},
		{
			name:   "socks5 代理",
			egress: ChannelEgress{Name: "jp", Kind: EgressKindSOCKS5, URL: "socks5://10.0.0.2:1080"},
		},
		{
			name:   "socks5h 也接受",
			egress: ChannelEgress{Name: "jp2", Kind: EgressKindSOCKS5, URL: "socks5h://10.0.0.2:1080"},
		},
		{name: "缺少名称", egress: ChannelEgress{Kind: EgressKindDirect}, wantErr: true},
		{name: "未知类型", egress: ChannelEgress{Name: "x", Kind: "vpn"}, wantErr: true},
		{name: "http 缺地址", egress: ChannelEgress{Name: "x", Kind: EgressKindHTTP}, wantErr: true},
		{name: "http 填成 socks5", egress: ChannelEgress{Name: "x", Kind: EgressKindHTTP, URL: "socks5://1.2.3.4:1080"}, wantErr: true},
		{name: "socks5 填成 http", egress: ChannelEgress{Name: "x", Kind: EgressKindSOCKS5, URL: "http://1.2.3.4:8080"}, wantErr: true},
		{name: "地址无法解析", egress: ChannelEgress{Name: "x", Kind: EgressKindHTTP, URL: "http://[::1"}, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.egress.Normalize()
			if c.wantErr {
				if err == nil {
					t.Fatalf("应报错, 实得 %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("不应报错: %v", err)
			}
			if c.check != nil {
				c.check(t, got)
			}
		})
	}
}

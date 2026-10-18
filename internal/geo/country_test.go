package geo

import "testing"

// TestHeaderCountry 覆盖采信条件与脏值过滤。
// 关键是 trusted=false 必须一律返回空: 这些头任何客户端都能伪造,
// 无边缘接入时采信它们等于让调用方自己决定统计口径。
func TestHeaderCountry(t *testing.T) {
	header := func(values map[string]string) func(string) string {
		return func(key string) string { return values[key] }
	}

	cases := []struct {
		name    string
		headers map[string]string
		trusted bool
		want    string
	}{
		{name: "可信来源取 Cloudflare 头", headers: map[string]string{"CF-IPCountry": "US"}, trusted: true, want: "US"},
		{name: "不可信来源一律忽略", headers: map[string]string{"CF-IPCountry": "US"}, trusted: false, want: ""},
		{name: "大小写与空白归一", headers: map[string]string{"CF-IPCountry": " de "}, trusted: true, want: "DE"},
		{name: "未知占位 XX 视为空", headers: map[string]string{"CF-IPCountry": "XX"}, trusted: true, want: ""},
		{name: "Tor 出口 T1 视为空", headers: map[string]string{"CF-IPCountry": "T1"}, trusted: true, want: ""},
		{name: "非两字母脏值忽略", headers: map[string]string{"CF-IPCountry": "USA"}, trusted: true, want: ""},
		{name: "数字脏值忽略", headers: map[string]string{"CF-IPCountry": "12"}, trusted: true, want: ""},
		{name: "回落到自建边缘头", headers: map[string]string{"X-Geo-Country": "JP"}, trusted: true, want: "JP"},
		{name: "Cloudflare 头优先", headers: map[string]string{"CF-IPCountry": "SG", "X-Geo-Country": "JP"}, trusted: true, want: "SG"},
		{name: "全空返回空", headers: map[string]string{}, trusted: true, want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HeaderCountry(header(tc.headers), tc.trusted); got != tc.want {
				t.Fatalf("HeaderCountry() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestHeaderCountryNilGetter 验证 getter 为 nil 时不 panic。
func TestHeaderCountryNilGetter(t *testing.T) {
	if got := HeaderCountry(nil, true); got != "" {
		t.Fatalf("HeaderCountry(nil, true) = %q, want empty", got)
	}
}

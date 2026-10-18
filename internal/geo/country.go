// Package geo 解析调用方所在地区。
//
// 只从反代注入的头取国家码, 不做 IP 库查询: 一是 IP 库需要额外数据文件与许可证,
// 二是部署了边缘节点后这些头天然存在且更准(边缘按真实 IP 判定, 不受客户端伪造影响)。
package geo

import "strings"

// countryHeaders 是按优先级排列的反代注入国家码头。
// Cloudflare 用 CF-IPCountry, 自建 nginx/其他 CDN 通常用 X-Geo-Country 或 X-Country-Code。
var countryHeaders = []string{"CF-IPCountry", "X-Geo-Country", "X-Country-Code"}

// unknownCountryCodes 是"未知"的占位值, 不应落库成真实国家。
// Cloudflare 对无法判定的来源(如 Tor)返回 XX, T1 表示 Tor 出口。
var unknownCountryCodes = map[string]bool{"": true, "XX": true, "T1": true}

// HeaderCountry 从请求头取国家码。
//
// trusted 必须由调用方按"是否部署在可信反代之后"给出(config.json 的 server.trusted_proxies 非空):
// 这些头任何客户端都能伪造, 无边缘接入时采信它们等于把统计口径交给调用方。
func HeaderCountry(header func(string) string, trusted bool) string {
	if !trusted || header == nil {
		return ""
	}
	for _, name := range countryHeaders {
		code := strings.ToUpper(strings.TrimSpace(header(name)))
		if unknownCountryCodes[code] || len(code) != 2 {
			continue
		}
		if code[0] >= 'A' && code[0] <= 'Z' && code[1] >= 'A' && code[1] <= 'Z' {
			return code
		}
	}
	return ""
}

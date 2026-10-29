// Package netguard 提供上游地址的私网防护: 判断目标地址是否指向内网/环回/链路本地,
// 并在拨号层按解析结果复核, 防 DNS rebinding 绕过保存时校验。
//
// 威胁模型: 渠道归属者(含普通渠道商)可自填上游地址与出口代理, 网关会代为发起请求。
// 不加防护时, 指向 169.254.169.254(云元数据)或内网服务的渠道能把内网响应带回调用方。
// 管理员如需指向内网上游(本地 vLLM 等), 可显式打开允许私网设置。
package netguard

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"time"
)

// dialTimeout 单次解析的超时: 防护本身不能成为新的挂起点。
const dialTimeout = 10 * time.Second

// DialContext 是 Transport.DialContext 的函数形状。
type DialContext = func(ctx context.Context, network, address string) (net.Conn, error)

// blockedNets 是标准库 IsPrivate/IsLoopback 等未覆盖的不可路由网段。
// 这些段不在 RFC1918/4193 内, 但同样不可用于公网直连, 且其中包含云元数据服务地址:
//   - 100.64.0.0/10   运营商级 NAT(CGNAT): 阿里云元数据 100.100.100.200 就在此段, 原实现漏判。
//   - 0.0.0.0/8       "this network", IsUnspecified 只拦 0.0.0.0 单地址。
//   - 192.0.0.0/24    IETF 协议保留段。
//   - 192.88.99.0/24  6to4 中继(已废弃), 不应被直连。
//   - 198.18.0.0/15   网络基准测试保留段。
//   - 240.0.0.0/4     IPv4 保留(E 类, 含广播)。
//   - fec0::/10       IPv6 站点本地(已废弃)。
//   - 64:ff9b::/96    NAT64 前缀, 可编码内网 IPv4。
var blockedNets = mustParseCIDRs(
	"100.64.0.0/10",
	"0.0.0.0/8",
	"192.0.0.0/24",
	"192.88.99.0/24",
	"198.18.0.0/15",
	"240.0.0.0/4",
	"fec0::/10",
	"64:ff9b::/96",
)

func mustParseCIDRs(cidrs ...string) []*net.IPNet {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(fmt.Sprintf("netguard: invalid cidr %q: %v", cidr, err))
		}
		nets = append(nets, n)
	}
	return nets
}

// IsPrivateIP 判断地址是否属于不允许直连的范围: 环回/私网/链路本地/未指定/组播/保留段。
func IsPrivateIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	for _, n := range blockedNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// CheckHost 解析主机名的全部地址, 任一为私网即拒绝; 全部为公网才放行。
// 解析失败不算违规: 保存时的校验只拦截"确定指向内网"的配置, 解析抖动交给拨号层复核。
func CheckHost(ctx context.Context, host string, allowPrivate bool) error {
	if allowPrivate {
		return nil
	}
	if host == "" {
		return fmt.Errorf("upstream host is empty")
	}
	if ip := net.ParseIP(host); ip != nil {
		if IsPrivateIP(ip) {
			return fmt.Errorf("upstream host %s resolves to a private address", host)
		}
		return nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
	if err != nil {
		// 解析不了不是"确定内网": 放行保存, 真正拨号时会再复核一次。
		return nil
	}
	for _, address := range addresses {
		if IsPrivateIP(address.IP) {
			return fmt.Errorf("upstream host %s resolves to a private address", host)
		}
	}
	return nil
}

// CheckURL 校验 URL 的主机指向: 仅接受 http/https 与 ws/wss(讯飞星火等 WebSocket 上游的完整端点),
// 主机不得解析到私网(除非允许)。ws/wss 只是 HTTP 升级协议, 主机防护语义与 http(s) 完全一致。
func CheckURL(ctx context.Context, rawURL string, allowPrivate bool) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid upstream url: %w", err)
	}
	switch parsed.Scheme {
	case "http", "https", "ws", "wss":
	default:
		return fmt.Errorf("upstream url scheme must be http, https, ws or wss, got %q", parsed.Scheme)
	}
	return CheckHost(ctx, parsed.Hostname(), allowPrivate)
}

// CheckProxyURL 校验代理地址的主机指向: 代理可以是 http/https/socks5/socks5h,
// 主机不得解析到私网(除非允许)。与 CheckURL 的区别仅在允许 socks5/socks5h 协议:
// 代理服务器地址本身指向内网与直连内网是同一类风险。
func CheckProxyURL(ctx context.Context, rawURL string, allowPrivate bool) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid proxy url: %w", err)
	}
	switch parsed.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return fmt.Errorf("proxy url scheme must be http, https, socks5 or socks5h, got %q", parsed.Scheme)
	}
	return CheckHost(ctx, parsed.Hostname(), allowPrivate)
}

// DialGuard 包一层拨号: 每次真实建连前按解析结果复核目标地址。
// 这是 DNS rebinding 的最终防线 —— 保存时校验的是当时解析, 拨号时可能已被换绑到内网。
type DialGuard struct {
	// AllowPrivate 返回当前是否允许私网目标; 由设置层注入, 每次拨号实时读取。
	AllowPrivate func() bool
	// Next 是底层拨号器(带超时/SOCKS 等); 为 nil 时用默认 net.Dialer。
	Next DialContext
}

// DialContext 与 net.Dialer.DialContext 同形, 可直接赋给 Transport.DialContext。
//
// 安全语义(相较原实现的两点收紧):
//  1. 解析一次 → 全量校验 → 按校验过的 IP 拨号: 不再把 hostname 交给下层拨号器二次解析,
//     消除「检查用一次解析、连接用另一次解析」的 TOCTOU(DNS rebinding)窗口。
//  2. 解析失败按拒绝处理(fail-closed): 原实现解析失败直接放行给拨号器, 攻击者可用
//     「第一次 SERVFAIL、第二次返回内网 A 记录」的抖动绕过。
func (g DialGuard) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	dial := g.Next
	if dial == nil {
		dial = (&net.Dialer{Timeout: dialTimeout}).DialContext
	}
	// 管理员显式允许私网时保持原始行为(直接拨, 保留 hostname)。
	if g.AllowPrivate == nil || g.AllowPrivate() {
		return dial(ctx, network, address)
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		// 切不出主机端口即无法校验: fail-closed, 不透明放行未校验地址。
		return nil, fmt.Errorf("dial guard cannot split %q into host:port: %w", address, err)
	}
	lookupCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	ips, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("dial guard resolve %s failed: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("dial guard resolve %s returned no addresses", host)
	}
	// 任一解析结果为私网即整体拒绝(与 CheckHost 的「全部公网才放行」口径一致)。
	targets := make([]string, 0, len(ips))
	for _, ip := range ips {
		if IsPrivateIP(ip.IP) {
			return nil, fmt.Errorf("dial to private address %s is blocked", address)
		}
		targets = append(targets, net.JoinHostPort(ip.IP.String(), port))
	}
	// 用已校验的 IP 依次拨号, 不再二次解析。
	var lastErr error
	for _, target := range targets {
		conn, derr := dial(ctx, network, target)
		if derr == nil {
			return conn, nil
		}
		lastErr = derr
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no resolved address available for %s", host)
	}
	return nil, lastErr
}

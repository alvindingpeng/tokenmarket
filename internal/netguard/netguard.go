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

// IsPrivateIP 判断地址是否属于不允许直连的范围: 环回/私网/链路本地/未指定/组播。
func IsPrivateIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
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

// CheckURL 校验 URL 的主机指向: 仅接受 http/https, 主机不得解析到私网(除非允许)。
func CheckURL(ctx context.Context, rawURL string, allowPrivate bool) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid upstream url: %w", err)
	}
	switch parsed.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("upstream url scheme must be http or https, got %q", parsed.Scheme)
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
func (g DialGuard) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if g.AllowPrivate != nil && !g.AllowPrivate() {
		if host, _, err := net.SplitHostPort(address); err == nil {
			lookupCtx, cancel := context.WithTimeout(ctx, dialTimeout)
			addresses, lookupErr := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
			cancel()
			if lookupErr == nil {
				for _, candidate := range addresses {
					if IsPrivateIP(candidate.IP) {
						return nil, fmt.Errorf("dial to private address %s is blocked", address)
					}
				}
			}
			// 解析失败不代替拨号报错: 交给后续真实拨号给出连接级错误。
		}
	}
	if g.Next == nil {
		return (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, network, address)
	}
	return g.Next(ctx, network, address)
}

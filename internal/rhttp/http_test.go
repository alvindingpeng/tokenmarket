package rhttp

import (
	"net/http"
	"testing"
)

// TestSharedReturnsSameClient 是本次修复的核心断言。
// 旧实现每个请求都新建 client 并在用完时关闭空闲连接, 等于每请求重付一次 TCP+TLS 握手;
// 这里必须保证同一代理地址拿到的永远是同一个连接池。
func TestSharedReturnsSameClient(t *testing.T) {
	first, err := Shared("http://127.0.0.1:18080")
	if err != nil {
		t.Fatalf("Shared() 失败: %v", err)
	}
	second, err := Shared("http://127.0.0.1:18080")
	if err != nil {
		t.Fatalf("Shared() 二次调用失败: %v", err)
	}
	if first != second {
		t.Fatalf("同一代理地址应返回同一个客户端实例, 实得不同实例(连接池未复用)")
	}

	other, err := Shared("socks5://127.0.0.1:1080")
	if err != nil {
		t.Fatalf("Shared() 其它地址失败: %v", err)
	}
	if other == first {
		t.Fatalf("不同代理地址不应共用客户端")
	}
}

// TestSharedRejectsEmptyURL 验证空地址报错而不是静默直连。
func TestSharedRejectsEmptyURL(t *testing.T) {
	if _, err := Shared(""); err == nil {
		t.Fatalf("空代理地址应报错")
	}
}

// TestSharedRejectsUnsupportedScheme 验证不支持的协议报错。
func TestSharedRejectsUnsupportedScheme(t *testing.T) {
	if _, err := Shared("ftp://127.0.0.1:21"); err == nil {
		t.Fatalf("不支持的协议应报错")
	}
}

// TestTransportPoolTuning 守住连接池参数。
// Go 默认 MaxIdleConnsPerHost=2: 同一上游并发超过 2 时连接用完即关,
// 下一个请求又要重新握手。参数被改回默认时这条测试必须失败。
func TestTransportPoolTuning(t *testing.T) {
	client, err := Direct()
	if err != nil {
		t.Fatalf("Direct() 失败: %v", err)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Direct() 的 Transport 类型不符: %T", client.Transport)
	}
	if transport.MaxIdleConnsPerHost != maxIdleConnsPerHost {
		t.Fatalf("MaxIdleConnsPerHost = %d, want %d", transport.MaxIdleConnsPerHost, maxIdleConnsPerHost)
	}
	if transport.MaxIdleConnsPerHost <= 2 {
		t.Fatalf("MaxIdleConnsPerHost 不应停留在 Go 默认的 2")
	}
	if !transport.ForceAttemptHTTP2 {
		t.Fatalf("ForceAttemptHTTP2 应为 true")
	}
	if transport.IdleConnTimeout != idleConnTimeout {
		t.Fatalf("IdleConnTimeout = %v, want %v", transport.IdleConnTimeout, idleConnTimeout)
	}
}

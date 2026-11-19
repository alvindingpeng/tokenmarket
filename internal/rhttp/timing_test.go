package rhttp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestConnTimingSnapshot 验证采集器能区分"新建连接"与"复用连接"。
// 这是本采集器的核心语义: 复用时 TLS 恒为 0, 若不用 Reused 标记,
// 统计出来的"平均建连耗时"会被大量 0 稀释成假象。
func TestConnTimingSnapshot(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	client := server.Client()

	// 第一次: 必然新建连接, 应有 TLS 握手耗时。
	first := &ConnTiming{}
	ctx := WithTiming(context.Background(), first)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	// 必须读完正文再关闭, 否则连接不会回到空闲池, 复用断言会假失败。
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()

	firstSnapshot := first.Snapshot()
	if firstSnapshot.ConnReused {
		t.Fatalf("首个请求不应复用连接: %+v", firstSnapshot)
	}
	if firstSnapshot.TLSNs <= 0 {
		t.Fatalf("首个请求应有 TLS 握手耗时, 实得 %+v", firstSnapshot)
	}
	if firstSnapshot.TTFBNs <= 0 {
		t.Fatalf("首个请求应有首字节耗时, 实得 %+v", firstSnapshot)
	}

	// 第二次: 连接池命中, TLS 应为 0 且标记复用。
	second := &ConnTiming{}
	ctx = WithTiming(context.Background(), second)
	request, err = http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	response, err = client.Do(request)
	if err != nil {
		t.Fatalf("second request: %v", err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()

	secondSnapshot := second.Snapshot()
	if !secondSnapshot.ConnReused {
		t.Fatalf("第二个请求应复用连接: %+v", secondSnapshot)
	}
	if secondSnapshot.TLSNs != 0 {
		t.Fatalf("复用连接不应有 TLS 握手耗时, 实得 %d", secondSnapshot.TLSNs)
	}
	if secondSnapshot.TTFBNs <= 0 {
		t.Fatalf("复用连接的请求仍应有首字节耗时, 实得 %+v", secondSnapshot)
	}
}

// TestConnTimingWithoutTrace 验证请求失败时快照不 panic 且保持零值。
func TestConnTimingWithoutTrace(t *testing.T) {
	timing := &ConnTiming{}
	snapshot := timing.Snapshot()
	if snapshot.TTFBNs != 0 || snapshot.TLSNs != 0 || snapshot.ConnReused {
		t.Fatalf("未采集时应为零值, 实得 %+v", snapshot)
	}
}

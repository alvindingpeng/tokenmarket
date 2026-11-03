package server

import (
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"
)

// TestCloseWaitsForInflightRequest 验证停服会等在途请求结束, 而不是直接掐断。
//
// 线上 98.3% 是流式请求, 一条 SSE 流可以挂几分钟; 而换二进制部署时 systemctl stop
// 会发 SIGTERM。此前 Close() 直接调 httpSrv.Close(), 立即断开所有连接 ——
// 每次部署都打断正在生成的回答, 且服务端日志里没有任何错误可查
// (客户端侧看到的是"回答突然中断")。
//
// 本测试直接驱动真实的 Close(): 起一个慢处理器, 请求在途时停服,
// 断言请求正常收到完整响应。
func TestCloseWaitsForInflightRequest(t *testing.T) {
	addr := freeAddr(t)

	release := make(chan struct{})
	started := make(chan struct{})
	srv := &http.Server{Addr: addr, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release // 模拟一条仍在传输的流式响应。
		_, _ = fmt.Fprint(w, "complete")
	})}
	startServer(t, srv)

	type result struct {
		body string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := http.Get("http://" + addr + "/slow")
		if err != nil {
			done <- result{err: err}
			return
		}
		defer resp.Body.Close()
		buf := make([]byte, 64)
		n, _ := resp.Body.Read(buf)
		done <- result{body: string(buf[:n])}
	}()

	<-started // 请求已在处理中。
	// 停服必须阻塞等待, 直到处理器放行。
	closed := make(chan error, 1)
	go func() { closed <- Close() }()

	select {
	case err := <-closed:
		t.Fatalf("Close 在请求仍在处理时就返回了(会在部署时掐断流式回答): %v", err)
	case <-time.After(150 * time.Millisecond):
		// 仍在等待 —— 符合预期。
	}

	close(release) // 让响应写完。

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("在途请求被中断: %v", got.err)
		}
		if got.body != "complete" {
			t.Fatalf("在途请求响应不完整: %q", got.body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("在途请求始终没有结束")
	}

	if err := <-closed; err != nil {
		t.Fatalf("Close 返回错误: %v", err)
	}
}

// TestCloseStopsAcceptingNewRequests 停服后新连接必须被拒 —— 优雅关闭不能变成"一直开着"。
func TestCloseStopsAcceptingNewRequests(t *testing.T) {
	addr := freeAddr(t)
	srv := &http.Server{Addr: addr, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "ok")
	})}
	startServer(t, srv)

	if err := Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := http.Get("http://" + addr + "/after"); err == nil {
		t.Fatal("停服后仍能建立新请求")
	}
}

// TestCloseIsIdempotent 停服可重入: 重复调用、或从未启动时调用都必须安全返回 nil。
//
// 这不是洁癖: shutdown 钩子与部署脚本都会触达停服路径, 若第二次调用 panic,
// 停服中途崩掉会把 systemd 拖到 TimeoutStopSec 才 SIGKILL。
func TestCloseIsIdempotent(t *testing.T) {
	addr := freeAddr(t)
	srv := &http.Server{Addr: addr, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})}
	startServer(t, srv)

	if err := Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := Close(); err != nil {
		t.Fatalf("second Close 必须安全返回 nil: %v", err)
	}
	// 从未启动的情况: 包级实例为 nil。
	if err := Close(); err != nil {
		t.Fatalf("Close 在没有服务实例时必须返回 nil: %v", err)
	}
}

// TestCloseAfterGracefulStopReturnsNil 已经关过的实例再次 Close 仍返回 nil。
func TestCloseAfterGracefulStopReturnsNil(t *testing.T) {
	addr := freeAddr(t)
	srv := &http.Server{Addr: addr, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})}
	startServer(t, srv)
	if err := Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if currentHTTPServer() != nil {
		t.Fatal("关闭后包级实例未被清空, 下一次 Close 会重复操作同一个 server")
	}
}

// freeAddr 取一个可用端口: 先监听再释放, 避免固定端口在 CI 上冲突。
func freeAddr(t *testing.T) string {
	t.Helper()
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	addr := probe.Addr().String()
	_ = probe.Close()
	return addr
}

// startServer 把 srv 注册为当前服务实例并在后台监听, 等端口就绪后返回。
//
// 收尾先等监听协程真正退出再清空包级变量: ListenAndServe 期间一直读着 srv,
// 提前改写会在 -race 下被判成数据竞争。
func startServer(t *testing.T, srv *http.Server) {
	t.Helper()
	previous := setHTTPServer(srv)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.ListenAndServe()
	}()
	t.Cleanup(func() {
		_ = srv.Close() // 幂等: 已经 Shutdown 过也是空操作。
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("监听协程没有退出")
		}
		setHTTPServer(previous)
	})
	waitListening(t, srv.Addr)
}

// waitListening 轮询到端口可连为止, 避免用 sleep 猜时间。
func waitListening(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("服务未在 %s 上就绪", addr)
}

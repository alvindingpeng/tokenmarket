package server

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// 服务端超时的两个方向都要验:
//   - ReadHeaderTimeout 真的会断开"只发一半请求头"的连接(slowloris);
//   - 长耗时响应**不会**被它误杀 —— 线上 98.3% 是流式请求, 一条 SSE 流可以挂几分钟,
//     误杀就是 v1.5.16 刚修好的"回答突然中断"换个触发条件重演。

// TestReadHeaderTimeoutDropsSlowloris 验证慢速发请求头的连接会被主动断开。
//
// 只发 "GET / HTTP/1.1" 加一个换行就挂住: 请求头永远收不全, 处理器根本不会被调用。
// 没有 ReadHeaderTimeout 时这条连接会一直占着 goroutine 与文件描述符。
func TestReadHeaderTimeoutDropsSlowloris(t *testing.T) {
	restore := shortenTimeouts(t, 50*time.Millisecond, time.Second)
	defer restore()

	addr := timeoutTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "should never be reached")
	}))

	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// 半个请求头, 然后什么都不发。服务端应在超时后自行关闭连接。
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: test\r\n"); err != nil {
		t.Fatalf("write partial header: %v", err)
	}

	// 读会返回 EOF/错误, 说明连接已被服务端关闭。给足余量避免慢 CI 上误报。
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 256)
	_, err = conn.Read(buf)
	if err == nil {
		t.Fatal("服务端在超时后仍保持连接可读")
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("ReadHeaderTimeout 未生效: 5 秒后连接仍然挂着")
	}
}

// TestSlowHandlerSurvivesWithoutWriteTimeout 验证长耗时响应不会被掐断。
//
// 处理器故意比读头超时慢得多: 若有人给 WriteTimeout/ReadTimeout 设了值,
// 这里会以"响应不完整"失败。这条测试是防回退的 —— 加超时看起来总是更"稳健",
// 而在这个项目里加 WriteTimeout 就是线上事故。
func TestSlowHandlerSurvivesWithoutWriteTimeout(t *testing.T) {
	restore := shortenTimeouts(t, 50*time.Millisecond, time.Second)
	defer restore()

	body := make(chan struct{})
	headersOut := make(chan struct{})
	addr := timeoutTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 先把响应头写出去并 flush: 客户端拿到头才返回, 之后才是"长时间不写正文"的流式阶段。
		// 不 flush 的话 http.Get 会一直阻塞在等响应头, 测试自己就死锁了。
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		close(headersOut)
		select {
		case <-body:
		case <-time.After(10 * time.Second):
			return
		}
		_, _ = fmt.Fprint(w, "complete")
	}))

	resp, err := http.Get("http://" + addr + "/slow")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	<-headersOut

	// 响应头已到, 读头超时此刻早已过期。处理器还能慢慢写正文, 说明没有别的超时在掐。
	time.Sleep(300 * time.Millisecond)
	close(body)

	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("长耗时响应被中断(说明设了 WriteTimeout/ReadTimeout): %v", err)
	}
	if string(got) != "complete" {
		t.Fatalf("响应不完整: %q", got)
	}
}

// TestApplyTimeoutsShapesServer 固定超时策略的取值形状: 两个设、两个不设。
//
// 单独断言"不设"是有意义的回归保护: WriteTimeout 是 HTTP 服务最常见的"最佳实践",
// 任何人后来补上都应该先看到这条测试失败再读一遍 applyTimeouts 的注释。
func TestApplyTimeoutsShapesServer(t *testing.T) {
	var srv http.Server
	applyTimeouts(&srv)

	if srv.ReadHeaderTimeout <= 0 {
		t.Error("ReadHeaderTimeout 必须 > 0, 否则 slowloris 会耗尽连接")
	}
	if srv.IdleTimeout <= 0 {
		t.Error("IdleTimeout 必须 > 0, 否则空闲 keep-alive 连接会长期堆积")
	}
	if srv.ReadTimeout != 0 {
		t.Error("ReadTimeout 必须为 0: 它把请求体读取一起限时, 会掐断慢速上传")
	}
	if srv.WriteTimeout != 0 {
		t.Error("WriteTimeout 必须为 0: 它会掐断长流式响应(线上 98.3% 是流式)")
	}
	if srv.ReadHeaderTimeout >= srv.IdleTimeout {
		t.Errorf("读头超时(%s)应远小于空闲超时(%s)", srv.ReadHeaderTimeout, srv.IdleTimeout)
	}
}

// shortenTimeouts 把包级超时压到毫秒级, 返回恢复函数。
func shortenTimeouts(t *testing.T, header, idle time.Duration) func() {
	t.Helper()
	oldHeader, oldIdle := readHeaderTimeout, idleTimeout
	readHeaderTimeout, idleTimeout = header, idle
	return func() { readHeaderTimeout, idleTimeout = oldHeader, oldIdle }
}

// timeoutTestServer 用当前超时策略起一个独立服务, 返回监听地址。
// 独立实例而不复用包级实例: 超时测试与停服测试互不干扰, -race 下也不会撞。
func timeoutTestServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	addr := freeAddr(t)
	srv := &http.Server{Addr: addr, Handler: handler}
	applyTimeouts(srv)
	startServer(t, srv)
	return addr
}

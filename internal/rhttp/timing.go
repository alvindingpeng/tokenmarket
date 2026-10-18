package rhttp

import (
	"context"
	"crypto/tls"
	"net/http/httptrace"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// ConnTiming 采集一次上游请求的连接各阶段耗时。
//
// 为什么需要它: 现在全站只有"首字节耗时"一个数, 它是「我们到上游的建连」与「上游自己排队」的合计。
// 不拆开就无法回答"该换上游还是该加出口"—— 换上游只影响排队, 加出口只影响建连。
//
// 复用连接时 DNS/建连/TLS 三个钩子根本不会触发, 三段为 0 且 Reused=true:
// 必须用标记区分"没有建连"与"建连极快", 否则均值会被复用的 0 拉低成假象。
type ConnTiming struct {
	mu sync.Mutex

	start        time.Time // 挂载采集器的时刻, 作为首字节耗时的起点。
	dnsStart     time.Time
	dnsDone      time.Time
	connectStart time.Time
	connectDone  time.Time
	tlsStart     time.Time
	tlsDone      time.Time
	firstByte    time.Time
	reused       bool
}

// WithTiming 把采集器挂到 ctx 上; 该 ctx 之后发起的 HTTP 请求会把各阶段耗时回填进 t。
// t 为 nil 时原样返回, 便于调用方无条件调用。
func WithTiming(ctx context.Context, t *ConnTiming) context.Context {
	if t == nil {
		return ctx
	}
	t.mu.Lock()
	t.start = time.Now()
	t.mu.Unlock()

	trace := &httptrace.ClientTrace{
		DNSStart:          func(httptrace.DNSStartInfo) { t.markFirst(&t.dnsStart) },
		DNSDone:           func(httptrace.DNSDoneInfo) { t.markLast(&t.dnsDone) },
		ConnectStart:      func(_, _ string) { t.markFirst(&t.connectStart) },
		ConnectDone:       func(_, _ string, _ error) { t.markLast(&t.connectDone) },
		TLSHandshakeStart: func() { t.markFirst(&t.tlsStart) },
		TLSHandshakeDone:  func(tls.ConnectionState, error) { t.markLast(&t.tlsDone) },
		GotConn: func(info httptrace.GotConnInfo) {
			t.mu.Lock()
			t.reused = info.Reused
			t.mu.Unlock()
		},
		GotFirstResponseByte: func() { t.markLast(&t.firstByte) },
	}
	return httptrace.WithClientTrace(ctx, trace)
}

// markFirst 只记录阶段的第一次开始: 重试与多地址并发探测会重复触发, 取最早的那个才反映真实起点。
func (t *ConnTiming) markFirst(target *time.Time) {
	t.mu.Lock()
	if target.IsZero() {
		*target = time.Now()
	}
	t.mu.Unlock()
}

// markLast 记录阶段的最后一次结束, 使区间覆盖全部重试, 不低估该阶段的总耗时。
func (t *ConnTiming) markLast(target *time.Time) {
	t.mu.Lock()
	*target = time.Now()
	t.mu.Unlock()
}

// Snapshot 取出当前采集结果, 供落库使用; 可在请求进行中多次调用。
func (t *ConnTiming) Snapshot() model.UpstreamTiming {
	t.mu.Lock()
	defer t.mu.Unlock()

	out := model.UpstreamTiming{
		DNSNs:      phaseSpan(t.dnsStart, t.dnsDone),
		ConnectNs:  phaseSpan(t.connectStart, t.connectDone),
		TLSNs:      phaseSpan(t.tlsStart, t.tlsDone),
		ConnReused: t.reused,
	}
	if !t.start.IsZero() && t.firstByte.After(t.start) {
		out.TTFBNs = int64(t.firstByte.Sub(t.start))
	}
	return out
}

// phaseSpan 返回阶段耗时; 未触发(如复用连接跳过建连)或时间倒挂时返回 0。
func phaseSpan(start, done time.Time) int64 {
	if start.IsZero() || done.IsZero() || done.Before(start) {
		return 0
	}
	return int64(done.Sub(start))
}

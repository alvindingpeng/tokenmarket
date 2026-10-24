package rhttp

import (
	"errors"
	"io"
	"mime"
	"net/http"
)

// maxResponseBytes 上游单个响应的读取上限(非流式)。
// 上游是渠道商自配的: 一个"响应"几十 GB 会把网关内存打爆。库内 httpclient 用
// io.ReadAll 全量读正文, 上限只能在传输层包一层。流式响应(text/event-stream)
// 逐事件消费且解码器有单事件上限, 不在此截断 —— 截断它反而会毁掉流。
const maxResponseBytes = 128 << 20 // 128 MiB

// errResponseTooLarge 非流式响应超出上限; 按上游失败处理, 不写入内存。
var errResponseTooLarge = errors.New("upstream response exceeds size limit")

// limitedBodyTransport 包住底层 RoundTripper: 非流式响应的正文读取限制在 maxResponseBytes 内。
type limitedBodyTransport struct {
	Base http.RoundTripper
}

// RoundTrip 透传请求, 收到响应时按 Content-Type 判定是否限读。
func (t limitedBodyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.Base.RoundTrip(request)
	if err != nil || response == nil || response.Body == nil {
		return response, err
	}
	if isEventStream(response.Header.Get("Content-Type")) {
		return response, nil
	}
	response.Body = &limitedReadCloser{underlying: response.Body, remaining: maxResponseBytes}
	return response, nil
}

// isEventStream 判断响应是否 SSE; 参数缺失按非流式处理(宁限勿爆)。
func isEventStream(contentType string) bool {
	if contentType == "" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return mediaType == "text/event-stream"
}

// limitedReadCloser 超限即报错: 上游想用超大响应打爆内存时, 收到的是一次请求失败而不是 OOM。
type limitedReadCloser struct {
	underlying io.ReadCloser
	remaining  int64
}

func (l *limitedReadCloser) Read(p []byte) (int, error) {
	if l.remaining <= 0 {
		// 上限已用尽: 再探 1 字节判断是否恰好读完(EOF)还是真的超限。
		probe := make([]byte, 1)
		n, err := l.underlying.Read(probe)
		if n == 0 && (err == io.EOF || err == nil) {
			return 0, io.EOF
		}
		return 0, errResponseTooLarge
	}
	// 预留 1 字节判超: 读完上限整好这么多字节时不算超限。
	if int64(len(p)) > l.remaining+1 {
		p = p[:l.remaining+1]
	}
	n, err := l.underlying.Read(p)
	l.remaining -= int64(n)
	if l.remaining < 0 {
		return n, errResponseTooLarge
	}
	return n, err
}

func (l *limitedReadCloser) Close() error { return l.underlying.Close() }

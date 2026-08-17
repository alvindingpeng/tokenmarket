package relay

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
	"golang.org/x/net/http/httpguts"
)

var errClientWrite = errors.New("failed to write client response") // 客户端响应写入失败。

// commitResult 保存响应提交后的审计结果和客户端写入错误。
type commitResult struct {
	responseBody []byte     // 响应或流聚合后的审计内容。
	usage        *llm.Usage // 标准化后的最终用量。
	firstWriteAt time.Time  // 首次成功写入客户端的时间。
	err          error      // 提交或提交后流处理的错误。
}

// 已经验证但尚未提交给客户端的响应。
type preparedResponse interface {
	Commit(context.Context, *gin.Context) commitResult
	Close() error
}

// bufferedResponse 保存已经验证的非流式响应。
type bufferedResponse struct {
	status  int         // 客户端应收到的状态码。
	headers http.Header // 允许复制给客户端的响应头。
	body    []byte      // 客户端应收到的响应体。
}

// Commit 将非流式响应写入客户端。
func (r *bufferedResponse) Commit(_ context.Context, c *gin.Context) commitResult {
	copyHeaders(c.Writer.Header(), r.headers)
	if c.Writer.Header().Get("Content-Type") == "" {
		c.Header("Content-Type", "application/json")
	}
	c.Status(r.status)
	if _, err := c.Writer.Write(r.body); err != nil {
		return commitResult{responseBody: r.body, err: fmt.Errorf("%w: %v", errClientWrite, err)}
	}
	return commitResult{responseBody: r.body, firstWriteAt: time.Now()}
}

// Close 释放非流式响应持有的资源。
func (r *bufferedResponse) Close() error {
	return nil
}

// copyHeaders 复制允许端到端传递的响应头。
func copyHeaders(destination, source http.Header) {
	for key, values := range source {
		switch http.CanonicalHeaderKey(key) {
		case "Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade", "Content-Length":
			continue
		}
		if httpguts.HeaderValuesContainsToken(source.Values("Connection"), key) {
			continue
		}
		destination[key] = slices.Clone(values)
	}
}

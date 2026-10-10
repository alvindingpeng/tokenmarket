package handlers

import (
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/conf"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/relay"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-contrib/sse"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/log").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/overview/stream", http.MethodGet).
				Handle(streamOverview),
		).
		AddRoute(
			router.NewRoute("/request-body/:id", http.MethodGet).
				Handle(getRequestBody),
		).
		AddRoute(
			router.NewRoute("/response-body/:id", http.MethodGet).
				Handle(getResponseBody),
		).
		AddRoute(
			router.NewRoute("/stop/:id", http.MethodPost).
				Handle(stopRequest),
		).
		AddRoute(
			router.NewRoute("/stop/:id/:round", http.MethodPost).
				Handle(stopRequest),
		).
		AddRoute(
			router.NewRoute("/attempts/:id", http.MethodGet).Handle(getLogAttempts),
		).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(listLogs),
		).
		AddRoute(
			router.NewRoute("/export", http.MethodGet).
				Handle(exportLogs),
		).
		AddRoute(
			router.NewRoute("/clear", http.MethodDelete).
				Allow(model.RoleAdmin).
				Handle(clearLog),
		).
		AddRoute(
			router.NewRoute("/replay/:id", http.MethodPost).
				Handle(replayRequest),
		)
}

// getLogAttempts returns durable attempt history with the same ownership guard as bodies.
func getLogAttempts(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid request id")
		return
	}
	if !canViewRequest(c, id) {
		resp.Error(c, http.StatusNotFound, "request not found")
		return
	}
	rows, err := op.RelayAttemptsByRequest(id)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, "failed to read attempts")
		return
	}
	resp.Success(c, gin.H{"items": rows})
}

// logFilterFromQuery 解析日志筛选条件, 列表与导出共用同一套口径。
func logFilterFromQuery(c *gin.Context) relay.RequestFilter {
	filter := relay.RequestFilter{
		Keyword:  c.Query("keyword"),
		ClientIP: c.Query("client_ip"),
		Model:    c.Query("model"),
		Channel:  c.Query("channel"),
	}
	if statusText := c.Query("status"); statusText != "" {
		for _, part := range strings.Split(statusText, ",") {
			if part = strings.TrimSpace(part); part != "" {
				filter.Status = append(filter.Status, relay.Status(part))
			}
		}
	}
	if fromText := c.Query("from"); fromText != "" {
		if from, err := time.Parse(time.RFC3339, fromText); err == nil {
			filter.From = from
		}
	}
	if toText := c.Query("to"); toText != "" {
		if to, err := time.Parse(time.RFC3339, toText); err == nil {
			filter.To = to
		}
	}
	return filter
}

// listLogs 分页返回调用日志, 支持关键字/终态/IP/模型/渠道/时间范围筛选; 可见性与实时流一致(管理员看全部, 其余看自有)。
func listLogs(c *gin.Context) {
	userID, role := middleware.CurrentUser(c)
	filter := logFilterFromQuery(c)
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	items, total := relay.RequestList(model.Scope{ID: userID, Role: role}, filter, limit, offset)
	resp.Success(c, gin.H{"items": items, "total": total})
}

// exportLogs 以 CSV 导出命中筛选条件的调用日志, 单次上限 5000 条; 可见性与列表一致。
func exportLogs(c *gin.Context) {
	userID, role := middleware.CurrentUser(c)
	filter := logFilterFromQuery(c)
	limit, _ := strconv.Atoi(c.Query("limit"))
	if limit <= 0 || limit > 5000 {
		limit = 5000
	}
	items, total := relay.RequestList(model.Scope{ID: userID, Role: role}, filter, limit, 0)

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=switchyard-logs-"+time.Now().Format("20060102-150405")+".csv")
	writer := csv.NewWriter(c.Writer)
	_ = writer.Write([]string{
		"id", "started_at", "status", "duration_ms", "user_id", "api_key_name", "client_ip",
		"model", "target_model", "channel", "round", "prompt_tokens", "completion_tokens", "cached_tokens", "cache_write_tokens", "cost", "error",
	})
	for _, item := range items {
		var cached, writeCached int64
		if item.Usage.PromptTokensDetails != nil {
			cached = item.Usage.PromptTokensDetails.CachedTokens
			writeCached = item.Usage.PromptTokensDetails.WriteCachedTokens
		}
		_ = writer.Write([]string{
			strconv.FormatUint(item.ID, 10),
			item.StartedAt.Format(time.RFC3339),
			string(item.Status),
			strconv.FormatInt(item.Duration.Milliseconds(), 10),
			strconv.FormatUint(uint64(item.UserID), 10),
			item.APIKeyName,
			item.ClientIP,
			item.Model,
			item.TargetModel,
			item.TargetChannelKey,
			strconv.Itoa(item.Round),
			strconv.FormatInt(item.Usage.PromptTokens, 10),
			strconv.FormatInt(item.Usage.CompletionTokens, 10),
			strconv.FormatInt(cached, 10),
			strconv.FormatInt(writeCached, 10),
			strconv.FormatFloat(item.Cost, 'f', 6, 64),
			item.Error,
		})
	}
	writer.Flush()
	if total > int64(len(items)) {
		// 截断提示以注释行落尾, 不破坏 CSV 解析。
		_, _ = c.Writer.WriteString("# truncated: exported " + strconv.Itoa(len(items)) + " of " + strconv.FormatInt(total, 10) + " rows\n")
	}
	audit(c, "log.export", "", fmt.Sprintf("rows=%d total=%d", len(items), total))
}

// stopRequest 按是否提供轮次参数, 中止单个轮次或整个请求。
func stopRequest(c *gin.Context) {
	requestID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid request id")
		return
	}
	if !canViewRequest(c, requestID) {
		resp.Error(c, http.StatusNotFound, "request not found")
		return
	}
	if roundText := c.Param("round"); roundText != "" {
		round, err := strconv.Atoi(roundText)
		if err != nil || round < 1 {
			resp.Error(c, http.StatusBadRequest, "invalid round")
			return
		}
		relay.Interrupt(requestID, round)
	} else {
		relay.CancelRequest(requestID)
	}
	c.Status(http.StatusNoContent)
}

// clearLog 删除访问者可见的已完成请求记录(管理员全部, 其余仅自有), 并在释放记录引用后主动执行垃圾回收。
func clearLog(c *gin.Context) {
	userID, role := middleware.CurrentUser(c)
	relay.Clear(model.Scope{ID: userID, Role: role})
	runtime.GC()
	audit(c, "log.clear", "", "")
	c.Status(http.StatusNoContent)
}

// canViewRequest 请求体与响应体只对归属者与管理员开放。
func canViewRequest(c *gin.Context, requestID uint64) bool {
	userID, role := middleware.CurrentUser(c)
	ownerID, exists := relay.RequestOwner(requestID)
	return exists && model.Scope{ID: userID, Role: role}.Owns(ownerID)
}

// getRequestBody 返回指定请求的原始请求体。
func getRequestBody(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid request id")
		return
	}
	if !canViewRequest(c, id) {
		resp.Error(c, http.StatusNotFound, "request not found")
		return
	}
	resp.Success(c, relay.RequestBody(id))
}

// getResponseBody 返回指定请求当前保存的响应体。
func getResponseBody(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid request id")
		return
	}
	if !canViewRequest(c, id) {
		resp.Error(c, http.StatusNotFound, "request not found")
		return
	}
	resp.Success(c, relay.ResponseBody(id))
}

// streamOverview 逐条发送建立连接时的概览及后续请求更新。
func streamOverview(c *gin.Context) {
	prepareSSE(c)
	userID, role := middleware.CurrentUser(c)
	snapshot, updates := relay.OpenRequestStream(model.Scope{ID: userID, Role: role})
	defer relay.CloseRequestStream(updates)
	if len(snapshot) == 0 {
		// Flush a real SSE comment so proxies forward the empty-state response immediately.
		if _, err := c.Writer.Write([]byte(": connected\n\n")); err != nil {
			return
		}
		c.Writer.Flush()
	}
	for _, request := range snapshot {
		if err := sse.Encode(c.Writer, sse.Event{Event: "log", Data: request}); err != nil {
			return
		}
		c.Writer.Flush()
	}

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := c.Writer.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			c.Writer.Flush()
		case request, ok := <-updates:
			if !ok {
				return
			}
			if err := sse.Encode(c.Writer, sse.Event{Event: "log", Data: request}); err != nil {
				return
			}
			c.Writer.Flush()
		}
	}
}

// prepareSSE 设置实时日志连接需要的响应头。
func prepareSSE(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
}

// replayResponseLimit 重放响应收集上限(4MB): 正常对话响应远小于此, 超出按截断返回。
const replayResponseLimit = 4 << 20

// replayPath 把请求协议映射回转发入口路径; 媒体请求保存的请求体是 multipart 摘要而非原文, 无法原样重放。
func replayPath(protocol model.Protocol) (string, bool) {
	switch protocol {
	case model.ProtocolOpenAIChatCompletion:
		return "/v1/chat/completions", true
	case model.ProtocolOpenAIResponse:
		return "/v1/responses", true
	case model.ProtocolAnthropicMessage:
		return "/v1/messages", true
	default:
		return "", false
	}
}

// replayRequest 用保存的原始请求体(可现场修改)经指定 API Key 重新走一遍转发链路:
// 与客户端调用完全同一条链路(认证/限流/计费/路由/日志), 重放本身会产生一条新的调用日志。
func replayRequest(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid request id")
		return
	}
	if !canViewRequest(c, id) {
		resp.Error(c, http.StatusNotFound, "request not found")
		return
	}
	payload := struct {
		RequestBody string `json:"request_body"`
		APIKeyID    int    `json:"api_key_id"`
	}{}
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&payload); err != nil {
			resp.Error(c, http.StatusBadRequest, "invalid replay payload")
			return
		}
	}
	row, err := op.RelayRequestGet(id)
	if err != nil {
		resp.Error(c, http.StatusNotFound, "request not found")
		return
	}
	path, ok := replayPath(model.Protocol(row.Protocol))
	if !ok {
		resp.Error(c, http.StatusBadRequest, "request protocol does not support replay")
		return
	}
	body := strings.TrimSpace(payload.RequestBody)
	if body == "" {
		body = row.RequestBody
	}
	if body == "" {
		resp.Error(c, http.StatusBadRequest, "request body is empty")
		return
	}
	keyID := payload.APIKeyID
	if keyID == 0 {
		keyID = row.APIKeyID
	}
	apiKey, err := op.APIKeyGet(keyID, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "api key not found")
		return
	}
	// 自调用转发入口: 监听 0.0.0.0 时回环地址同样可达, 与客户端调用同一条链路。
	target := fmt.Sprintf("http://127.0.0.1:%d%s", conf.AppConfig.Server.Port, path)
	upstream, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, target, strings.NewReader(body))
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, "failed to build replay request")
		return
	}
	upstream.Header.Set("Authorization", "Bearer "+apiKey.APIKey)
	upstream.Header.Set("Content-Type", "application/json")
	started := time.Now()
	client := &http.Client{Timeout: 10 * time.Minute}
	upstreamResp, err := client.Do(upstream)
	if err != nil {
		resp.Error(c, http.StatusBadGateway, "replay failed: "+err.Error())
		return
	}
	defer upstreamResp.Body.Close()
	result, err := io.ReadAll(io.LimitReader(upstreamResp.Body, replayResponseLimit))
	if err != nil {
		resp.Error(c, http.StatusBadGateway, "failed to read replay response")
		return
	}
	audit(c, "log.replay", fmt.Sprintf("request#%d", id), fmt.Sprintf("status=%d bytes=%d", upstreamResp.StatusCode, len(result)))
	resp.Success(c, gin.H{
		"status_code":   upstreamResp.StatusCode,
		"duration_ms":   time.Since(started).Milliseconds(),
		"model":         row.Model,
		"request_body":  body,
		"response_body": string(result),
		"content_type":  upstreamResp.Header.Get("Content-Type"),
	})
}


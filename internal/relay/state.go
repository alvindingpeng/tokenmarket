package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/ratelimit"
	"github.com/charmbracelet/log"
	"github.com/looplj/axonhub/llm"
)

// 客户端请求在转发过程中的当前状态。
type Status string

const (
	StatusRunning   Status = "running"   // 循环中: 正在选目标, 等待或请求上游。
	StatusCommitted Status = "committed" // 首字节已写出客户端, 此后不可再重试。
	StatusSuccess   Status = "success"   // 响应已完整交付客户端。
	StatusFailed    Status = "failed"    // 请求以错误结束。
	StatusCanceled  Status = "canceled"  // 客户端提前断开或取消。
)

// 客户端请求的完整进程内状态, 同时作为状态流的消息形状; 上半部分在请求到达时写入并在结束时定稿, 下半部分每轮循环覆盖。
type RouteEvent struct {
	At     time.Time `json:"at"`
	Phase  string    `json:"phase"`
	Reason string    `json:"reason"`
}

type RequestState struct {
	RouteEvents []RouteEvent `json:"route_events,omitempty"`
	finalized   bool

	ID                 uint64         `json:"id"`                   // 请求在当前进程内的唯一标识。
	Status             Status         `json:"status"`               // 请求当前状态。
	StartedAt          time.Time      `json:"started_at"`           // 请求到达时间。
	Duration           time.Duration  `json:"duration"`             // 请求从到达到结束的总耗时, 未结束时为零。
	FirstTokenDuration time.Duration  `json:"first_token_duration"` // 流式正确响应轮次开始到首字节提交的耗时, 非流式响应为零。
	StreamDuration     time.Duration  `json:"stream_duration"`      // 流式响应从首字节提交到响应结束的耗时, 非流式响应为零。
	ResponseDuration   time.Duration  `json:"response_duration"`    // 非流式正确响应轮次开始到完整响应提交的耗时, 流式响应为零。
	ClientIP           string         `json:"client_ip"`
	UserID             uint           `json:"user_id"`          // 发起请求的用户 ID(API Key 归属者), 日志与计费的归属依据。
	Model              string         `json:"model"`            // 客户端请求的模型名称, 即分组名称。
	ReasoningEffort    string         `json:"reasoning_effort"` // 客户端请求的思考等级, 未指定时为空。
	Protocol           model.Protocol `json:"protocol"`         // 客户端请求使用的协议, 由入站格式定出, 单个协议位而非掩码组合。
	GroupID            int            `json:"group_id"`         // 承载本请求的分组 ID, 供界面按主键直接定位分组而不必按名称回查。
	APIKeyName         string         `json:"api_key_name"`     // 发起请求时的 API Key 名称。
	Usage              llm.Usage      `json:"usage"`            // 请求结束时写入的展示用量。
	Cost               float64        `json:"cost"`             // 请求结束时写入的累计费用。
	OutputChars        int            `json:"output_chars"`     // 流式过程中按事件数量估算并实时累计的输出字符数, 仅用于界面展示, 不参与结算。

	Round             int            `json:"round"`               // 最新一轮循环的递增序号, 人工中止按此匹配以免误杀下一轮。
	RoundStartedAt    time.Time      `json:"round_started_at"`    // 最新一轮上游请求的开始时间。
	TargetChannelKey  string         `json:"target_channel_key"`  // 最新一轮选中的渠道名称和 Key 名称, 以空格分隔。
	TargetChannelCode string         `json:"target_channel_code"` // 最新一轮落地渠道的发布编码, 即用户侧看到的渠道标识; 未发布为空。
	TargetModel       string         `json:"target_model"`        // 最新一轮实际请求上游的模型名称。
	TargetProtocol    model.Protocol `json:"target_protocol"`     // 最新一轮实际请求上游的协议, 与 Protocol 不同即本轮做了跨协议转换; 0 表示尚未选出。
	Sending           bool           `json:"sending"`             // 最新一轮是否仍在等待上游响应。
	Error             string         `json:"error,omitempty"`     // 最新一轮的失败原因, 请求结束后即为最终错误。

	requestBody   string // 客户端原始请求体, 体积大故不进状态流, 由独立接口按需拉取。
	responseBody  string // 聚合后的完整最终响应体, 同样按需拉取。
	apiKeyID      int    // 发起请求的 API Key ID, 用于请求完成后的归属统计。
	routeDecision model.RelayRouteDecision
	roundItemID   int // 最新一轮的分组成员 ID, 供尝试记录落库。

	// 用户侧限流上下文: 请求开始时预留, 结束时按真实用量结算。
	rateScopes      []ratelimit.Scope
	rateReservation *ratelimit.Reservation

	// 计费上下文: admin 账号不计费(reservationID 为 0); 目标信息随最终轮次覆盖。
	reservationID     uint64          // 余额预扣记录 ID, 0 表示未预扣。
	targetChannelID   int             // 最终轮次落地渠道 ID。
	targetOwnerID     uint            // 最终轮次落地渠道的归属用户, 供货价收入计入该用户。
	targetShareCode   string          // 最终轮次落地渠道的发布编码, 计费明细脱敏展示用。
	targetSupplyPrice model.LLMPrice  // 最终轮次落地模型的供货价快照(token 模型)。
	targetMediaSupply model.MediaPrice // 最终轮次落地模型的媒体供货价快照(图片/视频), text 模型为零值。
	targetKind        model.MediaKind // 最终轮次落地模型的媒体形态, 用于区分计费路径。

	requestCtx    context.Context    // 请求级上下文, 同时约束等待、当前轮次和后续重试。
	requestCancel context.CancelFunc // 中止整个请求, 同时打断等待、当前轮次和后续重试。
	roundCancel   context.CancelFunc // 中止最新一轮上游请求, 仅在该轮等待响应期间非空。
	lastPublish   time.Time          // 上次向状态流发布快照的时间, 输出字符数按此节流发布。
	streamStarted time.Time          // 流式响应首字节提交时间, 用于计算实际流式传输耗时。
}

const streamBuffer = 16                              // 单个状态流连接的非阻塞消息缓冲容量。
const maxFinished = 500                              // 进程内保留的终态请求数量, 供日志分页回溯。
const maxBodyKeep = 50                               // 其中最近这些条保留请求/响应正文, 更早的释放正文只留概览。
const outputPublishInterval = 500 * time.Millisecond // 输出字符数实时推送的最短发布间隔。

var (
	idSeq    atomic.Uint64                             // 进程内严格递增的请求 ID。
	mu       sync.Mutex                                // 全部共享状态的互斥锁。
	requests = make(map[uint64]*RequestState)          // 按请求 ID 保存的全部请求状态。
	watchers = make(map[chan RequestState]model.Scope) // 全部状态流 SSE 连接及其访问者作用域, 发布时按归属过滤。
)

// newRequestState 分配请求 ID 并登记初始运行状态; 返回的记录是本请求后续全部状态写入的入口。
func newRequestState(ctx context.Context, modelName, reasoningEffort string, groupID int, protocol model.Protocol, body string, apiKeyID int, userID uint, clientIP string, timeoutSeconds int) *RequestState {
	if timeoutSeconds < 1 {
		timeoutSeconds = 600
	}
	requestCtx, requestCancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	mu.Lock()
	defer mu.Unlock()

	request := &RequestState{
		RouteEvents:     []RouteEvent{{At: time.Now(), Phase: "received", Reason: "request_received"}},
		ID:              nextRequestID(), // 跨进程重启唯一, 日志持久化以此为主键。
		Status:          StatusRunning,
		StartedAt:       time.Now(),
		UserID:          userID,
		Model:           modelName,
		ReasoningEffort: reasoningEffort,
		Protocol:        protocol,
		GroupID:         groupID,
		requestBody:     body,
		apiKeyID:        apiKeyID,
		ClientIP:        clientIP,
		requestCtx:      requestCtx,
		requestCancel:   requestCancel,
	}
	// 登记时保存名称快照, 查询失败时留空。
	if apiKey, err := op.APIKeyGet(apiKeyID, ctx); err == nil {
		request.APIKeyName = apiKey.Name
	}
	requests[request.ID] = request
	publishRequestLocked(request)
	// 初始登记即落库, 服务被杀也不会丢请求痕迹; 终态定稿时覆盖全量字段。
	persistRequestLocked(request)
	return request
}

// startRound 记录本轮选中的目标并进入上游请求, cancel 供人工中止本轮, 返回递增的轮次序号。
// 渠道归属与供货价随目标一并登记: 计费按最终轮次的落地渠道结算。
func (r *RequestState) startRound(cancel context.CancelFunc, channelKeyName, modelName string, protocol model.Protocol, channelID int, ownerID uint, shareCode string, supplyPrice model.LLMPrice, itemID int) int {
	mu.Lock()
	defer mu.Unlock()

	r.Round++
	r.RoundStartedAt = time.Now()
	r.OutputChars = 0 // 新一轮从头计数, 避免累计上一轮未提交的输出。
	r.lastPublish = time.Time{}
	r.TargetChannelKey = channelKeyName
	r.TargetChannelCode = shareCode
	r.TargetModel = modelName
	r.TargetProtocol = protocol
	r.targetChannelID = channelID
	r.targetOwnerID = ownerID
	r.targetShareCode = shareCode
	r.targetSupplyPrice = supplyPrice
	r.Sending = true
	r.Error = ""
	r.roundCancel = cancel
	r.roundItemID = itemID
	publishRequestLocked(r)
	// 本轮上游尝试先登记在途, 结束时按结果覆盖。
	persistAttempt(attemptRow(r, itemID, channelID, shareCode, modelName, protocol, r.RoundStartedAt, "running", ""))
	return r.Round
}

// setMediaTarget 在 startRound 之后追加媒体定价与形态, 供图片/视频计费使用; 文本模型不调用。
// 在锁内写入避免与状态流发布互相踩踏。
func (r *RequestState) setMediaTarget(kind model.MediaKind, media model.MediaPrice) {
	mu.Lock()
	defer mu.Unlock()
	r.targetKind = kind
	r.targetMediaSupply = media
}

// firstFormValue 取 multipart 表单字段的第一个值; 缺失返回空串。
func firstFormValue(values map[string][]string, key string) string {
	if v, ok := values[key]; ok && len(v) > 0 {
		return v[0]
	}
	return ""
}

// setRouteDecision saves a safe per-request selection snapshot.
func (r *RequestState) addRouteEvent(phase, reason string) {
	mu.Lock()
	defer mu.Unlock()
	r.addRouteEventLocked(phase, reason)
}

func (r *RequestState) addRouteEventLocked(phase, reason string) {
	if len(r.RouteEvents) >= 128 {
		return
	}
	if n := len(r.RouteEvents); n > 0 && r.RouteEvents[n-1].Phase == phase && r.RouteEvents[n-1].Reason == reason {
		return
	}
	r.RouteEvents = append(append([]RouteEvent(nil), r.RouteEvents...), RouteEvent{At: time.Now(), Phase: phase, Reason: reason})
}

func (r *RequestState) setRouteDecision(decision model.RelayRouteDecision) {
	mu.Lock()
	defer mu.Unlock()
	r.routeDecision = decision
	r.addRouteEventLocked("selection", decision.Reason)
}

// SetReservation 登记余额预扣记录, 供请求结束时结算或释放。
func (r *RequestState) SetReservation(reservationID uint64) {
	mu.Lock()
	defer mu.Unlock()
	r.reservationID = reservationID
}

// finishRound 记录本轮上游结果并落库本轮尝试; errText 为空表示已取得可提交响应。
// attemptStatus 为尝试终态: success / failed / limited / timeout / canceled。
func (r *RequestState) finishRound(errText, attemptStatus string) {
	mu.Lock()
	defer mu.Unlock()

	r.Sending = false
	r.Error = errText
	r.roundCancel = nil
	publishRequestLocked(r)
	persistAttempt(attemptRow(r, r.roundItemID, r.targetChannelID, r.TargetChannelCode, r.TargetModel, r.TargetProtocol, r.RoundStartedAt, attemptStatus, errText))
}

// addOutput 每个转发事件累加一个输出字符并按节流间隔发布快照; 距上次发布不足阈值时只累加不出流。
func (r *RequestState) addOutput() {
	mu.Lock()
	defer mu.Unlock()

	r.OutputChars++
	if time.Since(r.lastPublish) >= outputPublishInterval {
		r.lastPublish = time.Now()
		if !r.streamStarted.IsZero() {
			r.StreamDuration = time.Since(r.streamStarted)
		}
		publishRequestLocked(r)
	}
}

// Interrupt 中止指定请求仍在等待响应且轮次匹配的上游请求; 轮次不匹配说明该轮已结束, 不影响后续轮次。
func Interrupt(id uint64, round int) {
	mu.Lock()
	request := requests[id]
	if request == nil || request.Round != round || request.roundCancel == nil {
		mu.Unlock()
		return
	}
	cancel := request.roundCancel
	request.roundCancel = nil
	mu.Unlock()

	cancel()
}

// CancelRequest 取消指定的完整请求; 已结束请求不会被重新改写状态。
func CancelRequest(id uint64) {
	mu.Lock()
	request := requests[id]
	if request == nil {
		mu.Unlock()
		return
	}
	cancel := request.requestCancel
	request.requestCancel = nil
	mu.Unlock()

	if cancel != nil {
		cancel()
	}
}

// wait 在重新选择目标之前退避 seconds 秒; 客户端在退避期间断开时以取消终态定稿并返回 false。
func (r *RequestState) wait(ctx context.Context, seconds int) bool {
	select {
	case <-ctx.Done():
		r.markCanceled(ctx.Err(), "", nil)
		return false
	case <-time.After(time.Duration(seconds) * time.Second):
		return true
	}
}

// markCommitted 标记响应已提交, 并按响应方式记录最终正确轮次的首字或完整响应耗时。
func (r *RequestState) markCommitted(streaming bool) {
	mu.Lock()
	defer mu.Unlock()

	now := time.Now()
	r.Status = StatusCommitted
	if streaming {
		r.FirstTokenDuration = now.Sub(r.RoundStartedAt)
		r.streamStarted = now
	} else {
		r.ResponseDuration = now.Sub(r.RoundStartedAt)
	}
	publishRequestLocked(r)
}

// finishStream 记录首字节提交至流式响应实际结束的耗时。
func (r *RequestState) finishStream() {
	mu.Lock()
	defer mu.Unlock()

	if !r.streamStarted.IsZero() {
		r.StreamDuration = time.Since(r.streamStarted)
	}
}

// markSucceeded 以成功终态定稿请求。
func (r *RequestState) markSucceeded(responseBody string, usage *llm.Usage) {
	mu.Lock()
	defer mu.Unlock()

	r.Status = StatusSuccess
	r.Error = ""
	r.responseBody = responseBody
	r.finishLocked(usage)
}

// markFailed 以失败终态定稿请求, 最终错误取自本次失败原因。
func (r *RequestState) markFailed(err error, responseBody string, usage *llm.Usage) {
	mu.Lock()
	defer mu.Unlock()

	if r.requestCtx.Err() == context.Canceled {
		r.Status = StatusCanceled
		r.Error = r.requestCtx.Err().Error()
	} else {
		r.Status = StatusFailed
		r.Error = err.Error()
	}
	if responseBody != "" {
		r.responseBody = responseBody
	}
	r.finishLocked(usage)
}

// markCanceled 以取消终态定稿请求, 用于客户端提前断开或主动取消。
func (r *RequestState) markCanceled(err error, responseBody string, usage *llm.Usage) {
	mu.Lock()
	defer mu.Unlock()

	r.Status = StatusCanceled
	if err == context.DeadlineExceeded {
		r.Status = StatusFailed
	}
	r.Error = err.Error()
	if responseBody != "" {
		r.responseBody = responseBody
	}
	r.finishLocked(usage)
}

// finishLocked 写入用量和费用, 发布终态, 更新请求级统计并裁剪历史; 调用方必须持有锁。
func (r *RequestState) finishLocked(usage *llm.Usage) {
	if r.finalized {
		return
	}
	r.finalized = true
	r.addRouteEventLocked("finished", string(r.Status))
	r.Sending = false
	r.roundCancel = nil
	if r.requestCancel != nil {
		r.requestCancel()
	}
	r.requestCancel = nil
	if usage != nil {
		r.Usage = *usage
	}
	// 请求级统计(总览/统计/API Key)与日志费用都按用户端计费标准计算:
	// 本轮落地渠道的供货价上浮后即渠道模型发布价, 与 settleBillingLocked 的结算完全同口径。
	metrics := usageMetrics(r.targetSupplyPrice.Scale(op.SettingGetFloat(model.SettingKeyMarkupRatio)), usage)
	r.Cost = metrics.InputCost + metrics.OutputCost
	// 媒体计费: 按张/按秒的费用折入输出侧桶, 图表与统计的输入/输出两桶口径保持不变。
	if r.targetKind == model.MediaKindImage {
		if images := mediaImageCount(r.responseBody); images > 0 {
			mediaCost := r.targetMediaSupply.Scale(op.SettingGetFloat(model.SettingKeyMarkupRatio)).CostImages(images, "")
			metrics.OutputCost += mediaCost
			r.Cost += mediaCost
		}
	}
	r.Duration = time.Since(r.StartedAt)
	metrics.WaitTime = r.Duration.Milliseconds()
	if r.Status == StatusSuccess {
		metrics.RequestSuccess = 1
	} else {
		metrics.RequestFailed = 1
	}
	_ = op.StatsTotalUpdate(metrics)
	_ = op.StatsHourlyUpdate(metrics)
	_ = op.StatsDailyUpdate(context.Background(), metrics)
	if r.apiKeyID > 0 {
		_ = op.StatsAPIKeyUpdate(r.apiKeyID, metrics)
	}
	r.settleBillingLocked(usage)
	r.settleRateLimitLocked(usage)
	persistRequestLocked(r)
	publishRequestLocked(r)

	trimLocked()
}

// trimLocked 裁剪终态历史: 超出 maxFinished 的最旧记录整条丢弃, 超出 maxBodyKeep 的只释放正文。
// 保留概览让日志能分页回溯, 释放正文则避免大响应体长期驻留内存。调用方必须持有锁。
func trimLocked() {
	ids := make([]uint64, 0, len(requests))
	for id, request := range requests {
		if request.Status == StatusRunning || request.Status == StatusCommitted {
			continue
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] > ids[j] }) // 新到旧
	for index, id := range ids {
		request := requests[id]
		if request == nil {
			continue
		}
		if index >= maxBodyKeep {
			request.requestBody = ""
			request.responseBody = ""
		}
		if index >= maxFinished {
			delete(requests, id)
		}
	}
}

// setRateScopes 登记用户侧限流预留范围与估算, 供请求结束时结算。
func (r *RequestState) setRateScopes(scopes []ratelimit.Scope, reservation *ratelimit.Reservation) {
	mu.Lock()
	defer mu.Unlock()
	r.rateScopes = scopes
	r.rateReservation = reservation
	r.addRouteEventLocked("admission", "user_admitted")
}

// settleRateLimitLocked 请求结束后按真实用量结算用户侧限流额度; 无用量即退还预留。
func (r *RequestState) settleRateLimitLocked(usage *llm.Usage) {
	if len(r.rateScopes) == 0 {
		return
	}
	reservation := r.rateReservation
	r.rateScopes = nil
	r.rateReservation = nil
	if usage == nil {
		reservation.Settle(0, 0, true)
		return
	}
	reservation.Settle(usage.PromptTokens, usage.CompletionTokens, false)
	// 用量聚合供监控: 系统默认(全局)、用户与 API Key 三个维度各记一份。
	op.RateUsageAdd(model.RateScopeSystem, 0, 1, usage.PromptTokens, usage.CompletionTokens, 0)
	op.RateUsageAdd(model.RateScopeUser, int(r.UserID), 1, usage.PromptTokens, usage.CompletionTokens, 0)
	op.RateUsageAdd(model.RateScopeAPIKey, r.apiKeyID, 1, usage.PromptTokens, usage.CompletionTokens, 0)
}

// settleBillingLocked 结算或释放本请求的余额预扣; 无用量(失败/取消且上游未回用量)即全额释放。
// 计费口径: 用户按上浮后的用户价支出, 发布者按供货价收入, 平台收入为差额; 全量计费含 admin。
// 调用方必须持有锁; 计费落库失败不影响请求终态, 由预扣超时回滚兜底, 并记日志与告警而非静默吞错。
func (r *RequestState) settleBillingLocked(usage *llm.Usage) {
	if r.reservationID == 0 {
		return
	}
	reservationID := r.reservationID
	r.reservationID = 0
	read, output, cacheRead, cacheWrite := usageBuckets(usage)
	supply := r.targetSupplyPrice
	markupRatio := op.SettingGetFloat(model.SettingKeyMarkupRatio)
	userPrice := supply.Scale(markupRatio)
	userCost := userPrice.Cost(read, output, cacheRead, cacheWrite)
	ownerRevenue := supply.Cost(read, output, cacheRead, cacheWrite)
	record := &model.BillingRecord{
		RequestID:       r.ID,
		UserID:          r.UserID,
		APIKeyID:        r.apiKeyID,
		GroupID:         r.GroupID,
		GroupModel:      r.Model,
		ChannelID:       r.targetChannelID,
		OwnerID:         r.targetOwnerID,
		ShareCode:       r.targetShareCode,
		ModelName:       r.TargetModel,
		SupplyPrice:     supply,
		UserPrice:       userPrice,
		MarkupRatio:     markupRatio,
		PriceSource:     "channel_model",
		InputToken:      read,
		OutputToken:     output,
		CacheReadToken:  cacheRead,
		CacheWriteToken: cacheWrite,
	}
	// 媒体计费: 按响应中的实际张数结算, token 用量(上游返回时)照常叠加;
	// 张数从最终响应体的 data 数组解析, 兼容透传与转换两条路径, 失败/取消行没有 data 数组即 0。
	if r.targetKind == model.MediaKindImage {
		images := mediaImageCount(r.responseBody)
		if images == 0 && usage == nil {
			// 无张数也无 token 用量: 视为未产生计费用量, 全额释放预扣。
			if err := op.BillingRelease(reservationID); err != nil {
				log.Warnf("billing release failed for reservation %d: %v", reservationID, err)
			}
			return
		}
		userMedia := r.targetMediaSupply.Scale(markupRatio)
		userCost += userMedia.CostImages(images, "")
		ownerRevenue += r.targetMediaSupply.CostImages(images, "")
		record.MediaSupply = r.targetMediaSupply
		record.MediaUser = userMedia
		record.MediaUnits = model.MediaUnits{Images: images}
		record.PriceSource = "channel_model_media"
	} else if usage == nil {
		if err := op.BillingRelease(reservationID); err != nil {
			log.Warnf("billing release failed for reservation %d: %v", reservationID, err)
		}
		return
	}
	record.UserCost = userCost
	record.OwnerRevenue = ownerRevenue
	if err := op.BillingSettle(reservationID, record); err != nil {
		log.Warnf("billing settle failed for reservation %d request %d: %v", reservationID, r.ID, err)
		op.RaiseAlert(context.Background(), 0, "system", "billing_failed",
			fmt.Sprintf("billing settle failed: request=%d reservation=%d err=%v", r.ID, reservationID, err))
	}
}

// mediaImageCount 从生图响应体解析返回的图片张数; 形状为 {"data":[...]}。
// 解析失败或正文为空都按 0 张处理, 调用方据此决定释放预扣还是按 token 结算。
func mediaImageCount(body string) int64 {
	if body == "" {
		return 0
	}
	var parsed struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return 0
	}
	return int64(len(parsed.Data))
}

// usageBuckets 把统一用量拆成读/写/缓存读/缓存写四个计费桶。
// 输入总量含缓存命中部分, 未命中缓存的输入才按"读"计价; 口径与 usageMetrics 一致。
func usageBuckets(usage *llm.Usage) (read, output, cacheRead, cacheWrite int64) {
	if usage == nil {
		return 0, 0, 0, 0
	}
	if usage.PromptTokensDetails != nil {
		cacheRead = usage.PromptTokensDetails.CachedTokens
		cacheWrite = usage.PromptTokensDetails.WriteCachedTokens
	}
	read = max(int64(0), usage.PromptTokens-cacheRead-cacheWrite)
	output = usage.CompletionTokens
	return read, output, cacheRead, cacheWrite
}

// usageMetrics 将统一用量按给定单价转换为 Token 与费用统计; 无用量时对应费用为零。
// 单价由调用方决定: 请求级统计传用户价(渠道模型发布价), 渠道侧统计传供货价;
// 不再查全局模型价, 保证所有费用口径都能与计费对上。
func usageMetrics(price model.LLMPrice, usage *llm.Usage) model.StatsMetrics {
	if usage == nil {
		return model.StatsMetrics{}
	}
	metrics := model.StatsMetrics{InputToken: usage.PromptTokens, OutputToken: usage.CompletionTokens}
	cachedTokens, writeCachedTokens := int64(0), int64(0)
	if usage.PromptTokensDetails != nil {
		cachedTokens = usage.PromptTokensDetails.CachedTokens
		writeCachedTokens = usage.PromptTokensDetails.WriteCachedTokens
	}
	inputTokens := max(int64(0), usage.PromptTokens-cachedTokens-writeCachedTokens)
	metrics.InputCost = (float64(inputTokens)*price.Input + float64(cachedTokens)*price.CacheRead + float64(writeCachedTokens)*price.CacheWrite) / 1_000_000
	metrics.OutputCost = float64(usage.CompletionTokens) * price.Output / 1_000_000
	return metrics
}

// publishRequestLocked 非阻塞发布最新请求状态, 连接拥塞时关闭它并交给客户端重连获取全量快照; 调用方必须持有锁。
// 按访问者作用域过滤: 管理员收全部, 其余只收自有请求。
func publishRequestLocked(request *RequestState) {
	for stream, scope := range watchers {
		if !scope.Owns(request.UserID) {
			continue
		}
		select {
		case stream <- *request:
		default:
			delete(watchers, stream)
			close(stream)
		}
	}
}

// OpenRequestStream 注册请求状态流连接, 返回按请求 ID 倒序的可见快照和后续增量通道。
// 日志页不提供排序开关, 而 requests 是 map, 遍历顺序随机, 故顺序须由此处定稿。
// 快照与增量都按访问者作用域过滤: 管理员看全部, 其余只看自有请求。
func OpenRequestStream(scope model.Scope) ([]RequestState, chan RequestState) {
	mu.Lock()
	defer mu.Unlock()

	stream := make(chan RequestState, streamBuffer)
	watchers[stream] = scope

	snapshot := make([]RequestState, 0, len(requests))
	for _, request := range requests {
		if !scope.Owns(request.UserID) {
			continue
		}
		snapshot = append(snapshot, *request)
	}
	sort.Slice(snapshot, func(i, j int) bool { return snapshot[i].ID > snapshot[j].ID })
	return snapshot, stream
}

// CloseRequestStream 注销并关闭指定请求状态流连接。
func CloseRequestStream(stream chan RequestState) {
	mu.Lock()
	defer mu.Unlock()

	if _, exists := watchers[stream]; exists {
		delete(watchers, stream)
		close(stream)
	}
}

// RequestBody 返回指定请求保存的原始请求体: 内存优先, 重启后回退持久化日志。
func RequestBody(id uint64) string {
	mu.Lock()
	request := requests[id]
	body := ""
	if request != nil {
		body = request.requestBody
	}
	mu.Unlock()
	if body != "" {
		return body
	}
	if row, err := op.RelayRequestGet(id); err == nil {
		return row.RequestBody
	}
	return ""
}

// ResponseBody 返回指定请求当前保存的响应体: 内存优先, 重启后回退持久化日志。
func ResponseBody(id uint64) string {
	mu.Lock()
	request := requests[id]
	body := ""
	if request != nil {
		body = request.responseBody
	}
	mu.Unlock()
	if body != "" {
		return body
	}
	if row, err := op.RelayRequestGet(id); err == nil {
		return row.ResponseBody
	}
	return ""
}

// Clear 删除访问者可见的已结束请求记录: 管理员清全部, 其余只清自有; 内存与持久化日志一并清理。
func Clear(scope model.Scope) {
	mu.Lock()
	for id, request := range requests {
		if request.Status != StatusRunning && request.Status != StatusCommitted {
			if scope.Owns(request.UserID) {
				delete(requests, id)
			}
		}
	}
	mu.Unlock()

	if scope.IsAdmin() {
		if err := op.RelayRequestClear(); err != nil {
			log.Warnf("relay log clear failed: %v", err)
		}
	} else if err := op.RelayRequestClearUser(scope.ID); err != nil {
		log.Warnf("relay log clear failed: %v", err)
	}
}

// RequestFilter 是日志分页查询的筛选条件; 各字段为零值即不参与过滤。
type RequestFilter struct {
	Keyword  string    // 匹配模型名, 上游模型名, Key 名称, 渠道编码与错误文本。
	Status   []Status  // 只看这些终态; 空表示全部。
	From     time.Time // 起始时间(含); 零值不限。
	To       time.Time // 结束时间(含); 零值不限。
	ClientIP string    // 客户端 IP 模糊匹配。
	Model    string    // 模型名模糊匹配(客户端模型或上游落地模型)。
	Channel  string    // 渠道模糊匹配(渠道与 Key 名称或发布编码)。
}

// matchesLocked 判断记录是否命中筛选条件; 调用方必须持有锁。
func (f RequestFilter) matchesLocked(request *RequestState) bool {
	if !f.From.IsZero() && request.StartedAt.Before(f.From) {
		return false
	}
	if !f.To.IsZero() && request.StartedAt.After(f.To) {
		return false
	}
	if len(f.Status) > 0 {
		hit := false
		for _, status := range f.Status {
			if request.Status == status {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	if f.Keyword == "" {
		return true
	}
	keyword := strings.ToLower(f.Keyword)
	for _, field := range []string{request.Model, request.TargetModel, request.APIKeyName, request.TargetChannelCode, request.TargetChannelKey, request.Error} {
		if strings.Contains(strings.ToLower(field), keyword) {
			return true
		}
	}
	return false
}

// RequestList 按筛选条件倒序分页返回可见请求与命中总数。
// 查询走持久化日志(服务重启后仍可回溯), 未定稿的行以内存最新状态覆盖, 保证实时一致。
func RequestList(scope model.Scope, filter RequestFilter, limit, offset int) ([]RequestState, int64) {
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	opFilter := op.RelayRequestFilter{Keyword: filter.Keyword, From: filter.From, To: filter.To, ClientIP: filter.ClientIP, Model: filter.Model, Channel: filter.Channel}
	for _, status := range filter.Status {
		if opFilter.Status != "" {
			opFilter.Status += ","
		}
		opFilter.Status += string(status)
	}
	rows, total, err := op.RelayRequestList(scope, opFilter, limit, offset)
	if err != nil {
		log.Warnf("relay log list failed: %v", err)
		return []RequestState{}, 0
	}
	page := make([]RequestState, 0, len(rows))
	mu.Lock()
	for _, row := range rows {
		if live := requests[row.ID]; live != nil {
			page = append(page, *live)
		} else {
			page = append(page, rowToState(row))
		}
	}
	mu.Unlock()
	return page, total
}

// ActiveCount 返回当前仍在处理中的请求数, 供健康探针与运维概览。
func ActiveCount() int {
	mu.Lock()
	defer mu.Unlock()
	count := 0
	for _, request := range requests {
		if !request.finalized {
			count++
		}
	}
	return count
}

// RequestOwner 返回请求归属用户: 内存优先, 重启后回退持久化日志。
func RequestOwner(id uint64) (uint, bool) {
	mu.Lock()
	request := requests[id]
	mu.Unlock()
	if request != nil {
		return request.UserID, true
	}
	if row, err := op.RelayRequestGet(id); err == nil {
		return row.UserID, true
	}
	return 0, false
}

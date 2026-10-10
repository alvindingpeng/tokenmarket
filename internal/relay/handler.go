package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/ratelimit"
	"github.com/gin-contrib/sse"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
	"github.com/looplj/axonhub/llm/transformer/openai"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
	"github.com/tidwall/sjson"
)

// settleUpstream 按真实用量结算尝试并归还并发租约; 无用量归还预留。
// 上游用量同时按渠道/凭据/渠道模型三个维度聚合, 供限流管理「实时用量」展示。
func settleUpstream(reservation *ratelimit.Reservation, scopes []ratelimit.Scope, usage *llm.Usage) {
	if usage == nil {
		reservation.Settle(0, 0, true)
		return
	}
	reservation.Settle(usage.PromptTokens, usage.CompletionTokens, false)
	recordScopeUsage(scopes, 1, usage.PromptTokens, usage.CompletionTokens, 0)
}

// recordScopeUsage 按给定限流范围逐一聚合小时级用量。
func recordScopeUsage(scopes []ratelimit.Scope, requests, inputTokens, outputTokens, rejected int64) {
	for _, scope := range scopes {
		op.RateUsageAdd(scope.Kind, scope.ID, requests, inputTokens, outputTokens, rejected)
	}
}

// Forward 按客户端协议承载一个请求的完整转发过程: 解析请求, 定位分组, 循环选目标请求上游, 直至提交响应或请求结束。
func Forward(format llm.APIFormat) gin.HandlerFunc {
	// 客户端协议同时定出入站转换器和请求协议位: 后者随请求状态推给界面, 也是每轮选择上游协议的首选。
	var inbound transformer.Inbound
	requestProtocol := model.ProtocolOpenAIChatCompletion
	switch format {
	case llm.APIFormatOpenAIResponse:
		inbound = responses.NewInboundTransformer()
		requestProtocol = model.ProtocolOpenAIResponse
	case llm.APIFormatAnthropicMessage:
		inbound = anthropic.NewInboundTransformer()
		requestProtocol = model.ProtocolAnthropicMessage
	case llm.APIFormatOpenAIImageGeneration, llm.APIFormatOpenAIImageEdit, llm.APIFormatOpenAIImageVariation:
		// 生图协议: generations 走 JSON, edits/variations 走 multipart; 转发流程按 Content-Type 区分解析。
		requestProtocol = model.ProtocolOpenAIImage
		if format == llm.APIFormatOpenAIImageEdit || format == llm.APIFormatOpenAIImageVariation {
			inbound = openai.NewImageEditInboundTransformer()
		} else {
			inbound = openai.NewImageGenerationInboundTransformer()
		}
	default:
		inbound = openai.NewInboundTransformer()
	}

	return func(c *gin.Context) {
		// 完整读取客户端请求, 正文先登记到请求状态, 后续每轮直接改写为当前目标请求。
		raw, err := httpclient.ReadHTTPRequest(c.Request)
		if err != nil {
			rejectRequest(c, inbound, err)
			return
		}

		// 此处只读取选组和分流所需字段; 完整协议校验由同协议上游或跨协议 pipeline 完成。
		// 生图 edits 请求是 multipart 表单, model 在表单字段里而不是 JSON, 需单独解析;
		// 同时为日志生成一份去掉图片二进制的摘要正文, 原始 multipart 不入库。
		var metadata struct {
			Model     string `json:"model"`  // 客户端请求的分组名称。
			Streaming bool   `json:"stream"` // 客户端是否请求流式响应。
		}
		bodyForLog := string(raw.Body)
		isMultipart := strings.HasPrefix(strings.ToLower(raw.Headers.Get("Content-Type")), "multipart/form-data")
		if isMultipart {
			form, parseErr := parseMultipartForm(raw)
			if parseErr != nil {
				rejectRequest(c, inbound, parseErr)
				return
			}
			metadata.Model = strings.TrimSpace(firstFormValue(form.Value, "model"))
			metadata.Streaming = strings.EqualFold(strings.TrimSpace(firstFormValue(form.Value, "stream")), "true")
			bodyForLog = multipartBodySummary(form)
			// 大图会落临时文件, 取完元数据即释放。
			form.RemoveAll()
		} else if err := json.Unmarshal(raw.Body, &metadata); err != nil {
			rejectRequest(c, inbound, err)
			return
		}

		// 生图当前只支持同步响应: 上游以事件流下发部分图片的做法尚未纳入 P1 范围。
		if requestProtocol == model.ProtocolOpenAIImage && metadata.Streaming {
			rejectRequest(c, inbound, errors.New("image generation does not support streaming"))
			return
		}

		// API Key 限定了模型范围时只放行范围内的模型, 为空表示不限制。
		if allowed, ok := c.Get("supported_models"); ok {
			if names, _ := allowed.([]string); len(names) > 0 && !slices.Contains(names, metadata.Model) {
				rejectRequest(c, inbound, errors.New("model not supported by this api key"))
				return
			}
		}

		// 客户端请求的模型名称即分组名称; 分组按 API Key 归属者的命名空间解析(分组名跨用户可重名),
		// 分组不存在说明模型名错误或不属于该用户, 等待也不会出现该分组。
		// 分组主键随请求状态一并登记, 界面由此可直接按主键取分组而不必按名称回查。
		keyUserID := uint(c.GetInt("api_key_user_id"))
		group, err := op.GroupGetByName(metadata.Model, keyUserID)
		if err != nil {
			rejectRequest(c, inbound, errors.New("model not found"))
			return
		}

		// 用户侧限流: RPM/TPM 按用户与 API Key 双维度一次预留, 触顶直接 429, 不产生日志行。
		// 预留按估算 token 数, 请求结束时按真实用量结算; 生图正文可能是数 MB 的 base64, 与 token 无关, 按 1 计。
		estIn, estOut := int64(1), int64(1)
		if requestProtocol != model.ProtocolOpenAIImage {
			estIn, estOut = estimateTokens(raw.Body)
		}
		userScopes := userRateScopes(keyUserID, c.GetInt("api_key_id"), metadata.Model)
		userDecision := limiter.Reserve(userScopes, estIn, estOut)
		if !userDecision.Allowed {
			op.RateUsageAdd(model.RateScopeSystem, 0, 0, 0, 0, 1)
			op.RateUsageAdd(model.RateScopeUser, int(keyUserID), 0, 0, 0, 1)
			op.RateUsageAdd(model.RateScopeAPIKey, c.GetInt("api_key_id"), 0, 0, 0, 1)
			rejectRateLimited(c, inbound, userDecision)
			return
		}

		// 登记进程内请求状态, 返回的记录是后续全部状态写入和前端可视化推送的入口。
		// 入站转换器统一解析各协议的思考等级; 解析失败仍由原有转发流程处理请求。
		reasoningEffort := ""
		if parsed, parseErr := inbound.TransformRequest(c.Request.Context(), raw); parseErr == nil {
			reasoningEffort = parsed.ReasoningEffort
		}
		clientIP := c.ClientIP()
		if forwarded := c.GetHeader("X-Forwarded-For"); forwarded != "" {
			if first := strings.SplitN(forwarded, ",", 2)[0]; strings.TrimSpace(first) != "" {
				clientIP = strings.TrimSpace(first)
			}
		}
		request := newRequestState(c.Request.Context(), metadata.Model, reasoningEffort, group.ID, requestProtocol, bodyForLog, c.GetInt("api_key_id"), keyUserID, clientIP, group.RelayConfig.RequestTimeoutSeconds)
		request.setRateScopes(userScopes, userDecision.Reservation)
		ctx := request.requestCtx

		// 余额预扣: 全量计费, 不区分角色(admin 同样计费); 按分组内最高用户价与输出上限估算并冻结, 结束按实际用量结算。
		// 2026-10-08 起取消 admin 豁免: 余额必须严格随调用费用扣减, 否则账目与统计对不上。
		if err := reserveBalance(request, group); err != nil {
			userDecision.Reservation.Settle(0, 0, true) // 未产生用量, 幂等退还。
			request.markFailed(err, "", nil)
			rejectRequest(c, inbound, err)
			return
		}

		failedItemID := 0 // 当前累计连续失败次数的成员 ID。
		failures := 0     // 该成员包含首次请求的连续失败次数。
		totalAttempts := 0
		waiter := requestWait{queue: &waiting, group: group.ID}
		defer waiter.leave()
		stop := func(cause error, status int) {
			if errors.Is(cause, context.Canceled) {
				request.markCanceled(cause, "", nil)
				return
			}
			request.markFailed(cause, "", nil)
			if !c.Writer.Written() && c.Request.Context().Err() == nil {
				if status == http.StatusTooManyRequests {
					c.Header("Retry-After", "1")
				}
				response := inbound.TransformError(context.WithoutCancel(ctx), &llm.ResponseError{StatusCode: status, Detail: llm.ErrorDetail{Message: cause.Error(), Type: "relay_unavailable"}})
				c.Data(response.StatusCode, "application/json", response.Body)
			}
		}
		waitAvailable := func() bool {
			if waiter.ticket == nil {
				request.addRouteEvent("waiting", "waiting_for_upstream")
			}
			if cause := waiter.pause(ctx, group.RelayConfig); cause != nil {
				status := http.StatusTooManyRequests
				if errors.Is(cause, context.DeadlineExceeded) {
					status = http.StatusGatewayTimeout
				}
				stop(cause, status)
				return false
			}
			return true
		}

		for {
			if group.RelayConfig.MaxTotalAttempts > 0 && totalAttempts >= group.RelayConfig.MaxTotalAttempts {
				stop(errors.New("relay maximum total attempts exceeded"), http.StatusServiceUnavailable)
				return
			}
			if waiter.ticket == nil && waiting.pending(group.ID) {
				if cause := waiter.join(group.RelayConfig); cause != nil {
					stop(cause, http.StatusTooManyRequests)
					return
				}
			}
			if cause := waiter.turn(ctx); cause != nil {
				status := http.StatusTooManyRequests
				if errors.Is(cause, context.DeadlineExceeded) {
					status = http.StatusGatewayTimeout
				}
				stop(cause, status)
				return
			}
			if ctx.Err() != nil {
				stop(ctx.Err(), http.StatusGatewayTimeout)
				return
			}

			// 分组配置和成员随时可改, 故每轮重新读取; 分组被删除时等待它重新出现。
			group, err = op.GroupGetByName(metadata.Model, keyUserID)
			if err != nil {
				if !waitAvailable() {
					return
				}
				continue
			}

			// 手动模式取人工指定的成员, 故障转移模式按优先级选择未禁用且不在冷却中的成员。
			// 没有目标时等待重新选择, 期间人工切换渠道, 补齐成员或成员冷却到期即可让请求继续。
			item := pickGroupItem(group)
			request.setRouteDecision(routeExplanation(group, item))
			if item.ID == 0 {
				if !waitAvailable() {
					return
				}
				continue
			}

			// 成员指向的授权缺失, 凭据被停用或两侧已被删除时等待, 该成员可能很快被改回可用配置。
			// ChannelGrantGet 一次校验齐这几种情况, 取到的授权必然可直接转发, 无需再逐项检查。
			grant, err := op.ChannelGrantGet(item.ChannelGrantID)
			if err != nil {
				if !waitAvailable() {
					return
				}
				continue
			}
			channelModel := grant.ChannelModel
			channelKey := grant.ChannelKey

			// 成员指向的渠道已被删除时同样等待, 该成员可能很快被改回可用渠道。
			channel, err := op.ChannelGet(channelModel.ChannelID)
			if err != nil {
				if !waitAvailable() {
					return
				}
				continue
			}

			// 将分组成员配置的真实模型写入本轮上游请求; multipart 表单按字段重写而非 JSON 路径。
			if isMultipart {
				err = rewriteMultipartModel(raw, channelModel.Name)
			} else {
				raw.Body, err = sjson.SetBytes(raw.Body, "model", channelModel.Name)
			}
			if err != nil {
				request.markFailed(err, "", nil)
				rejectRequest(c, inbound, err)
				return
			}
			// OpenAI Chat 流式响应需显式要求上游在末尾附带用量。
			if metadata.Streaming && format == llm.APIFormatOpenAIChatCompletion {
				raw.Body, err = sjson.SetBytes(raw.Body, "stream_options.include_usage", true)
				if err != nil {
					request.markFailed(err, "", nil)
					rejectRequest(c, inbound, err)
					return
				}
			}

			// 在渠道授权支持的协议内选出本轮上游协议, 按该协议的路径与授权绑定的凭据构造出站转换器。
			// 先于登记本轮目标: 选中的协议是本轮目标的一部分, 需与渠道和模型一并推给界面。
			outbound, targetProtocol, passthrough, err := buildOutbound(channel, grant, *channelKey, requestProtocol, format)

			// 为本轮上游调用建立独立取消入口并登记当前目标; 取消原因用于区分人工中止与响应超时。
			roundCtx, cancelRoundCause := context.WithCancelCause(ctx)
			// 人工中止和本轮完成都使用普通 canceled 原因, 超时回调则写入具体的超时错误。
			cancelRound := func() {
				cancelRoundCause(context.Canceled)
			}
			totalAttempts++
			request.startRound(cancelRound, channel.Name+" · "+channelKey.Name, channelModel.Name, targetProtocol,
				channel.ID, channel.UserID, channel.ShareCode, channelModel.SupplyPrice, item.ID)
			// 媒体计费上下文: 仅媒体形态模型登记; 文本模型此项保持零值, settlement 走 token 路径不变。
			// 选择上游时只在本组成员内挑选 kind=image 的成员, 故只要命中即可认定本轮用媒体计费。
			if channelModel.Kind == model.MediaKindImage {
				request.setMediaTarget(model.MediaKindImage, channelModel.MediaSupply)
			}

			// 上游侧限流: 每次实际尝试独立预留 RPM/TPM 额度, 触顶即记为限流尝试并跳过该成员:
			// 不计失败率、不冷却, 由选路侧自动跳过受限成员。
			upstreamScopes := upstreamRateScopes(channel.ID, channelKey.ID, channelModel.ID, channelModel.Name)
			upEstIn, upEstOut := int64(1), int64(1)
			if requestProtocol != model.ProtocolOpenAIImage {
				upEstIn, upEstOut = estimateTokens(raw.Body)
			}
			var upstreamReservation *ratelimit.Reservation
			if err == nil {
				decision := limiter.Reserve(upstreamScopes, upEstIn, upEstOut)
				if !decision.Allowed {
					recordScopeUsage(upstreamScopes, 0, 0, 0, 1) // 触顶拒绝计入上游各维度 rejected。
					request.finishRound("upstream rate limited", "limited")
					cancelRound()
					releaseRouteProbe(group, item.ID)
					continue
				}
				upstreamReservation = decision.Reservation
				waiter.leave()
				request.addRouteEvent("dispatch", "upstream_admitted")
			}

			roundStartedAt := time.Now() // 本轮上游调用的开始时间, 用于统计首个有效响应耗时。

			// 请求上游并等待首个有效响应: 非流式等待完整响应, 流式等待首个事件。
			// 同协议渠道原样直通, 跨协议渠道经转换后请求; 此时尚未写给客户端, 失败仍可换目标重试。
			var result *upstreamResponse
			if err == nil {
				timeoutSeconds := group.RelayConfig.MemberNonStreamResponseTimeoutSeconds // 非流式等待完整响应, 流式分支改为首事件超时。
				timeoutErr := errors.New("upstream non-stream response timeout")          // 具体错误用于区分超时与人工中止。
				if metadata.Streaming {
					timeoutSeconds = group.RelayConfig.MemberStreamFirstEventTimeoutSeconds
					timeoutErr = errors.New("upstream stream first event timeout")
				}
				// 计时器取消本轮上下文, 让正在等待 HTTP 响应或首个流事件的调用及时返回。
				timeoutTimer := time.AfterFunc(time.Duration(timeoutSeconds)*time.Second, func() {
					cancelRoundCause(timeoutErr)
				})
				// 客户端与渠道协议一致时直接透传, 其余组合通过 pipeline 转换。
				if passthrough {
					result, err = sendPassthrough(roundCtx, format, raw, channel, outbound, metadata.Streaming, channelModel.Name)
				} else {
					result, err = sendConverted(roundCtx, format, raw, channel, outbound, metadata.Streaming)
				}
				// 上游调用返回即结束首响应等待; Stop 失败说明已到期, 主动取消可避免等待异步回调完成。
				if !timeoutTimer.Stop() {
					cancelRoundCause(timeoutErr)
				}
				if context.Cause(roundCtx) == timeoutErr {
					err = timeoutErr
					// 超时与响应返回同时发生时舍弃尚未提交的流结果, 避免把超时误记为成功。
					if result != nil && result.events != nil {
						result.events.Close()
						if result.closeIdle != nil {
							result.closeIdle()
						}
					}
				}
			}

			if err != nil {
				// 记录本轮上游调用已经结束及其失败原因。
				request.finishRound(err.Error(), attemptStatusOf(err))
				// 父上下文结束说明客户端已经取消, 归还探测占用并以取消终态结束请求。
				if ctx.Err() != nil {
					releaseRouteProbe(group, item.ID)
					upstreamReservation.Settle(0, 0, true)
					cancelRound()
					stop(ctx.Err(), http.StatusGatewayTimeout)
					return
				}
				// 仅人工中止本轮时不计失败也不等待; 响应超时属于真实失败并消耗尝试次数。
				if context.Cause(roundCtx) == context.Canceled {
					releaseRouteProbe(group, item.ID)
					upstreamReservation.Settle(0, 0, true)
					continue
				}
				// 上游 429 视为限流而非故障: 标记该上游当前窗口触顶供选路跳过, 不计失败率也不冷却。
				if isUpstreamRateLimited(err) {
					releaseRouteProbe(group, item.ID)
					upstreamReservation.Settle(0, 0, true)
					limiter.MarkBlocked(upstreamScopes, upEstIn, upEstOut)
					cancelRound()
					continue
				}
				// 其余失败未产生用量, 退还本次尝试的限流预留。
				upstreamReservation.Settle(0, 0, true)
				cancelRound()
				// 本轮真实失败只计入当前渠道和成员, 客户端取消与人工中止不计为渠道故障。
				metrics := model.StatsMetrics{WaitTime: time.Since(roundStartedAt).Milliseconds(), RequestFailed: 1}
				_ = op.ChannelStatsUpdate(channel.ID, metrics)
				_ = op.ChannelModelStatsUpdate(channelModel.ID, metrics)
				_ = op.ChannelKeyStatsUpdate(channelKey.ID, metrics)
				op.ChannelDailyStatsUpdate(channel.ID, metrics)
				op.ChannelModelDailyStatsUpdate(channelModel.ID, metrics)
				recordMemberMetric(group.ID, item.ID, channelModel.ID, group.RelayConfig.MetricWindowSize, time.Since(roundStartedAt).Milliseconds(), false)

				// 成员改变时重新开始累计该成员在本请求内的连续失败次数。
				if failedItemID == item.ID {
					failures++
				} else {
					failedItemID = item.ID
					failures = 1
				}
				// 达到总尝试次数时成员进入冷却并立即重新选路, 否则等待后重试。
				if recordRouteFailure(group, item.ID, failures) {
					continue
				}
				// 同组还有别的可用成员时立即切换, 不等待重试(避免同一失效端点反复阻塞)。
				if hasOtherEligible(group, item.ID) {
					recordRouteFailure(group, item.ID, group.RelayConfig.MemberMaxAttempts)
					continue
				}
				timer := time.NewTimer(time.Duration(group.RelayConfig.MemberRetryIntervalSeconds) * time.Second)
				select {
				case <-ctx.Done():
					timer.Stop()
					stop(ctx.Err(), http.StatusGatewayTimeout)
					return
				case <-timer.C:
				}
				continue
			}
			// 记录本轮已经取得可提交的上游响应。
			request.finishRound("", "success")
			// 请求级取消可能与上游成功同时到达, 此时不应提交响应或继续重试。
			if ctx.Err() != nil {
				releaseRouteProbe(group, item.ID)
				settleUpstream(upstreamReservation, upstreamScopes, result.usage)
				if result.events != nil {
					result.events.Close()
				}
				if result.closeIdle != nil {
					result.closeIdle()
				}
				cancelRound()
				request.finishRound(ctx.Err().Error(), attemptStatusOf(ctx.Err()))
				request.markCanceled(ctx.Err(), "", result.usage)
				if ctx.Err() == context.DeadlineExceeded && !c.Writer.Written() {
					response := inbound.TransformError(context.WithoutCancel(ctx), &llm.ResponseError{StatusCode: http.StatusGatewayTimeout, Detail: llm.ErrorDetail{Message: "request deadline exceeded", Type: "relay_timeout"}})
					c.Data(response.StatusCode, "application/json", response.Body)
				}
				return
			}
			roundWaitTime := time.Since(roundStartedAt).Milliseconds() // 流式响应只统计等待首帧的时间。
			// 上游成功后解除该成员的冷却与探测占用, 并按路由配置开始亲和。
			recordRouteSuccess(group, item.ID)
			// 记录成员运行指标(首响应耗时与成功), 供延迟/成功率/综合评分策略排名。
			recordMemberMetric(group.ID, item.ID, channelModel.ID, group.RelayConfig.MetricWindowSize, roundWaitTime, true)
			// 非流式已取得最终用量; 流式必须等到末帧聚合后才结算并释放并发租约。
			if !metadata.Streaming {
				settleUpstream(upstreamReservation, upstreamScopes, result.usage)
			}
			// 同协议透传时原样返回上游响应头; 跨协议响应没有需要透传的响应头。
			for key, values := range result.header {
				c.Writer.Header()[key] = values
			}

			// 非流式响应已经完整取得, 提交后一次写给客户端。
			if !metadata.Streaming {
				cancelRound()
				if c.Writer.Header().Get("Content-Type") == "" {
					c.Header("Content-Type", "application/json")
				}
				// 非流式响应已有完整用量, 本轮渠道和成员统计可在提交前一次完成。
				// 渠道侧统计按供货价(渠道成本)计, 与请求级统计的用户价口径区分。
				metrics := usageMetrics(channelModel.SupplyPrice, result.usage)
				metrics.WaitTime = roundWaitTime
				metrics.RequestSuccess = 1
				_ = op.ChannelStatsUpdate(channel.ID, metrics)
				_ = op.ChannelModelStatsUpdate(channelModel.ID, metrics)
				_ = op.ChannelKeyStatsUpdate(channelKey.ID, metrics)
				op.ChannelDailyStatsUpdate(channel.ID, metrics)
				op.ChannelModelDailyStatsUpdate(channelModel.ID, metrics)
				request.markCommitted(false)
				n, err := c.Writer.Write(result.body)
				if err == nil && n != len(result.body) {
					err = io.ErrShortWrite
				}
				if err != nil {
					if ctx.Err() != nil {
						request.markCanceled(ctx.Err(), string(result.body), result.usage)
					} else {
						request.markFailed(err, string(result.body), result.usage)
					}
					return
				}
				request.markSucceeded(string(result.body), result.usage)
				return
			}

			// 首帧提交后仍需逐个事件判断协议终态: 上游发出结束事件后未必立即关闭响应体, 继续读取会一直阻塞到
			// 客户端断开, 从而把已完整交付的响应误判为 context canceled。
			if c.Writer.Header().Get("Content-Type") == "" {
				c.Header("Content-Type", "text/event-stream")
			}
			var encoded bytes.Buffer
			var chunks []*httpclient.StreamEvent
			event := result.first
			last := result.last // 已转发的最后一个事件是否已按客户端协议结束整个响应流。
			committed := false
			idleErr := errors.New("upstream stream idle timeout")
			for {
				if event != nil {
					chunks = append(chunks, event)
					// 每个事件计一个输出字符供日志页展示, 按节流间隔发布。
					request.addOutput()
					encoded.Reset()
					if encodeErr := sse.Encode(&encoded, sse.Event{Id: event.LastEventID, Event: event.Type, Data: event.Data}); encodeErr != nil {
						err = encodeErr
						break
					}
					if !committed {
						request.markCommitted(true)
						committed = true
					}
					n, writeErr := c.Writer.Write(encoded.Bytes())
					if writeErr == nil && n != encoded.Len() {
						writeErr = io.ErrShortWrite
					}
					if writeErr != nil {
						err = writeErr
						break
					}
					c.Writer.Flush()
				}
				if last {
					break
				}
				idleTimer := time.AfterFunc(time.Duration(group.RelayConfig.MemberStreamIdleTimeoutSeconds)*time.Second, func() { cancelRoundCause(idleErr) })
				next := result.events.Next()
				idleTimer.Stop()
				if context.Cause(roundCtx) == idleErr {
					err = idleErr
					break
				}
				if !next {
					err = result.events.Err()
					break
				}
				event = result.events.Current()
				// 已提交的响应不能再换目标重试, 结束事件自身携带的失败原样转发给客户端, 并在转发后作为本请求终态。
				last, err = inspectStreamEvent(format, event)
			}
			request.finishStream()
			result.events.Close()
			// 事件流已读完, 渠道专用代理的独占连接池到此归还。
			if result.closeIdle != nil {
				result.closeIdle()
			}
			cancelRound()
			// 使用客户端协议转换器聚合已转发事件, 统一取得最终响应正文和用量。
			responseBody, meta, aggregateErr := inbound.AggregateStreamChunks(context.WithoutCancel(ctx), chunks)
			if aggregateErr == nil {
				result.usage = meta.Usage
			}
			settleUpstream(upstreamReservation, upstreamScopes, result.usage)
			if ctx.Err() != nil {
				err = ctx.Err()
			}
			if err != nil {
				request.finishRound(err.Error(), attemptStatusOf(err))
			} else {
				request.finishRound("", "success")
			}
			// 流式响应结束并聚合出用量后, 按最终结果完成本轮渠道和成员统计(供货价口径)。
			metrics := usageMetrics(channelModel.SupplyPrice, result.usage)
			metrics.WaitTime = roundWaitTime
			if err == nil {
				metrics.RequestSuccess = 1
			} else {
				metrics.RequestFailed = 1
			}
			_ = op.ChannelStatsUpdate(channel.ID, metrics)
			_ = op.ChannelModelStatsUpdate(channelModel.ID, metrics)
			_ = op.ChannelKeyStatsUpdate(channelKey.ID, metrics)
			op.ChannelDailyStatsUpdate(channel.ID, metrics)
			op.ChannelModelDailyStatsUpdate(channelModel.ID, metrics)
			if err != nil {
				if ctx.Err() != nil {
					request.markCanceled(ctx.Err(), string(responseBody), result.usage)
				} else {
					request.markFailed(err, string(responseBody), result.usage)
				}
				return
			}
			request.markSucceeded(string(responseBody), result.usage)
			return
		}
	}
}

// reserveBalance 按分组内最高用户价与配置的输出上限估算费用并预扣。
// 估算只用于冻结额度: 结束时按实际用量结算差额, 估算偏大只是多冻结, 结算后自动释放。
// 模型里包含媒体形态(image)成员时, 额外按生图保守张数冻结一张最高价的金额, 文本成员冻结照旧。
// 取最大而非求和是因为同时按多个上游冻结没有意义 -- 实际只会按选路命中其中一个, 与冻结方式无关。
func reserveBalance(request *RequestState, group model.Group) error {
	var maxInput, maxOutput, maxMediaImage float64
	var hasImageItem bool
	for _, item := range group.Items {
		maxInput = max(maxInput, item.UserPrice.Input)
		maxOutput = max(maxOutput, item.UserPrice.Output)
		if item.Kind == model.MediaKindImage {
			hasImageItem = true
			maxMediaImage = max(maxMediaImage, item.UserMedia.MaxImagesPerRequest())
		}
	}
	capTokens, err := op.SettingGetInt(model.SettingKeyBalanceReserveCap)
	if err != nil || capTokens < 0 {
		capTokens = 4096
	}
	// 读写各按上限估算: 输入侧保守按输出上限同样计, 覆盖常见长上下文请求。
	estimate := (maxInput*float64(capTokens) + maxOutput*float64(capTokens)) / 1_000_000
	if hasImageItem {
		reserveImages, err := op.SettingGetInt(model.SettingKeyBalanceReserveImages)
		if err != nil || reserveImages < 1 {
			reserveImages = 4 // 默认 4 张, 与新增设置的种子值一致; 缺失键时回落。
		}
		estimate += maxMediaImage * float64(reserveImages)
	}
	reservationID, err := op.BillingReserve(request.UserID, request.ID, estimate)
	if err != nil {
		return err
	}
	request.SetReservation(reservationID)
	return nil
}

// isUpstreamRateLimited 判断上游错误是否为限流 429。
func isUpstreamRateLimited(err error) bool {
	if err == nil {
		return false
	}
	var responseErr *llm.ResponseError
	if errors.As(err, &responseErr) {
		return responseErr.StatusCode == http.StatusTooManyRequests
	}
	// 透传路径的非 2xx 上游响应以 httpclient.Error 承载, 与转换路径同判。
	var httpErr *httpclient.Error
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == http.StatusTooManyRequests
	}
	return strings.Contains(err.Error(), "429")
}

// attemptStatusOf 把上游错误映射为尝试终态: success / limited / timeout / canceled / failed。
func attemptStatusOf(err error) string {
	if err == nil {
		return "success"
	}
	if isUpstreamRateLimited(err) {
		return "limited"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "timeout") {
		return "timeout"
	}
	return "failed"
}

// rejectRateLimited 以 429 拒绝触顶的请求, 带 Retry-After 与限流错误类型供客户端退避。
func rejectRateLimited(c *gin.Context, inbound transformer.Inbound, decision ratelimit.Decision) {
	retry := int(decision.RetryAfter.Seconds() + 0.999)
	if retry < 1 {
		retry = 1
	}
	c.Header("Retry-After", strconv.Itoa(retry))
	response := inbound.TransformError(c.Request.Context(), &llm.ResponseError{
		StatusCode: http.StatusTooManyRequests,
		Detail:     llm.ErrorDetail{Message: "rate limit exceeded", Type: "rate_limit_exceeded"},
	})
	c.Data(response.StatusCode, "application/json", response.Body)
	c.Abort()
}

// rejectRequest 以客户端协议的错误格式返回请求级失败, 用于尚未登记状态因而无需定稿的请求。
func rejectRequest(c *gin.Context, inbound transformer.Inbound, err error) {
	response := inbound.TransformError(c.Request.Context(), &llm.ResponseError{
		StatusCode: http.StatusBadRequest,
		Detail:     llm.ErrorDetail{Message: err.Error(), Type: "invalid_request_error"},
	})
	c.Data(response.StatusCode, "application/json", response.Body)
	c.Abort()
}

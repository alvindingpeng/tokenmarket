package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/conf"
	"github.com/alvindingpeng/switchyard/internal/geo"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/ratelimit"
	"github.com/alvindingpeng/switchyard/internal/rhttp"
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
	if reservation == nil {
		return
	}
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
	case llm.APIFormatOpenAIVideo:
		// 生视频: 创建任务同步返回任务对象, 真正的视频由客户端轮询 /v1/videos/{id} 取回。
		requestProtocol = model.ProtocolOpenAIVideo
		inbound = openai.NewVideoInboundTransformer()
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
		// ReadHTTPRequest 已解码正文; 当前依赖与原请求共享 headers, 正常 gzip 也已剥头。
		// 仍直接清理后续实际使用的 raw.Headers, 防御非规范大小写/未来依赖复制 headers;
		// 出站正文可能改写或重新序列化, 编码及长度不能沿用入站值。这不是生产根因证明。
		stripDecodedRequestHeaders(raw.Headers)

		// 此处只读取选组和分流所需字段; 完整协议校验由同协议上游或跨协议 pipeline 完成。
		// 生图 edits 请求是 multipart 表单, model 在表单字段里而不是 JSON, 需单独解析;
		// 同时为日志生成一份去掉图片二进制的摘要正文, 原始 multipart 不入库。
		var metadata struct {
			Model     string `json:"model"`  // 客户端请求的分组名称。
			Streaming bool   `json:"stream"` // 客户端是否请求流式响应。
		}
		// 留痕正文按需生成: 只有"保存正文"开启时才做这份整份拷贝。关闭时
		// persist.maskedBody 一律返回空串, 这个 string 从头到尾没有读者, 却是一次与请求体
		// 等大的内存拷贝 —— /v1 的请求体上限是 64 MiB, 也就是每个大请求凭空多占一份。
		// 更糟的是它会被请求状态持有到过期, 而状态缓存保留最近 maxBodyKeep 条正文。
		// multipart 的摘要只有几百字节, 与开关无关照留。
		//
		// 必须在改写 raw.Body 之前取: 后续每轮都会把 model 换成上游真实模型名(见下方
		// sjson.SetBytes 与 rewriteMultipartModel), 而日志页要看的是客户端发来的原文。
		var bodyForLog string
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
		} else {
			if err := json.Unmarshal(raw.Body, &metadata); err != nil {
				rejectRequest(c, inbound, err)
				return
			}
			if op.ShouldStoreBody() {
				bodyForLog = string(raw.Body)
			}
		}

		// 生图支持流式: gpt-image-1 的 stream:true 下上游以 SSE 事件下发部分图与最终图
		// (image_generation.partial_image / image_generation.completed), 见 image_stream.go。
		// 生视频只支持同步响应: 它的「异步」体现在任务对象上, 由客户端轮询取回, 不是流式响应。
		if metadata.Streaming && requestProtocol == model.ProtocolOpenAIVideo {
			rejectRequest(c, inbound, errors.New("video generation does not support streaming"))
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
		// Match the client group name before routing rewrites and any reservation.
		if policy, ok := op.UserTierLimitsForUser(keyUserID); ok && len(policy.AllowedModels) > 0 && !slices.Contains(policy.AllowedModels, metadata.Model) {
			rejectRequest(c, inbound, errors.New("model not supported by this user tier"))
			return
		}
		group, err := op.GroupGetByName(metadata.Model, keyUserID)
		if err != nil {
			rejectRequest(c, inbound, errors.New("model not found"))
			return
		}
		group = effectiveGroupForRequest(group, c.GetString("routing_preference"))

		// 用户侧限流: RPM/TPM 按用户与 API Key 双维度一次预留, 触顶直接 429, 不产生日志行。
		// 预留按估算 token 数, 请求结束时按真实用量结算; 生图正文可能是数 MB 的 base64, 与 token 无关, 按 1 计。
		estIn, estOut := int64(1), int64(1)
		if !isMediaProtocol(requestProtocol) {
			estIn, estOut = estimateTokens(raw.Body)
		}
		userScopes := userRateScopes(keyUserID, c.GetInt("api_key_id"), metadata.Model)
		userDecision := limiter.Reserve(userScopes, estIn, estOut)
		if !userDecision.Allowed {
			op.RateUsageAdd(model.RateScopeSystem, 0, 0, 0, 0, 1)
			op.RateUsageAdd(model.RateScopeUser, int(keyUserID), 0, 0, 0, 1)
			op.RateUsageAdd(model.RateScopeAPIKey, c.GetInt("api_key_id"), 0, 0, 0, 1)
			// 通知为最佳努力异步操作; 429 主响应不能被通知写库拖慢或改变。
			op.NotifyRateLimitHitAsync(keyUserID, metadata.Model, userDecision.BlockedScope.Kind, uint64(userDecision.BlockedScope.ID))
			rejectRateLimited(c, inbound, userDecision)
			return
		}

		// 登记进程内请求状态, 返回的记录是后续全部状态写入和前端可视化推送的入口。
		// 入站转换器统一解析各协议的思考等级; 解析失败仍由原有转发流程处理请求。
		reasoningEffort := ""
		if parsed, parseErr := inbound.TransformRequest(c.Request.Context(), raw); parseErr == nil {
			reasoningEffort = parsed.ReasoningEffort
		}
		// 调用方 IP 一律走 c.ClientIP(): 它按 server.trusted_proxies 从右往左跳过可信代理,
		// 取第一个不可信地址。自己解析 X-Forwarded-For 第一段是不安全的 —— 调用方可以随便填一个,
		// 于是审计里的 client_ip 想写什么就是什么, 按 IP 的限流也能被绕过。
		clientIP := c.ClientIP()
		// 国家码只采信可信反代注入的头: 无边缘接入时这些头可被任意伪造, 采信等于把统计口径交给调用方。
		clientCountry := geo.HeaderCountry(c.GetHeader, len(conf.AppConfig.Server.TrustedProxies) > 0)
		request := newRequestState(c.Request.Context(), metadata.Model, reasoningEffort, group.ID, requestProtocol, bodyForLog, c.GetInt("api_key_id"), keyUserID, clientIP, clientCountry, group.RelayConfig.RequestTimeoutSeconds)
		var autoProtectionGroup model.Group
		var autoProtectionTask autoTask
		autoProtectionEngaged := false
		defer func() {
			if !autoProtectionEngaged {
				return
			}
			switch request.statusSnapshot() {
			case StatusSuccess:
				autoCircuitRecordSuccess(autoProtectionGroup.ID, autoProtectionTask.name)
			case StatusFailed:
				autoCircuitRecordFailure(autoProtectionGroup, autoProtectionTask.name)
			}
		}()
		request.setRateScopes(userScopes, userDecision.Reservation)
		// 生视频的计费用量来自请求本身(seconds 与分辨率档位), 预扣与结算回落都用它;
		// 其余协议的请求没有媒体用量, 保持零值。
		if requestProtocol == model.ProtocolOpenAIVideo {
			request.setRequestedMedia(parseVideoRequestUnits(raw.Body))
		}
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
		// rateRetries 单独统计"上游 429 之后的退避重试"次数, 不并入 totalAttempts(理由见 rateRetryBackoff)。
		// 跨成员累计、换成员后不清零: 限流是上游侧的窗口状态, 与"换个成员重新开始"无关。
		rateRetries := 0
		// pendingRateRetry 表示上一轮是退避过的上游 429, 本轮是那次退避换来的重试。
		// 预算检查必须为它放行: MaxTotalAttempts 收紧时(线上实测可到 1), 否则一次瞬时 429
		// 就会在"还没退避"就被判死, 退避逻辑形同虚设 —— 正是 TestRateRetryDoesNotConsumeTotalAttemptBudget 抓到的形态。
		//
		// 与"不计入 totalAttempts"是两道互补的保护, 不是重复: 前者管住"这一轮能不能跑",
		// 后者管住"预算数字不被虚增"。任一条单独存在时常见路径都能跑通(故删其一未必触发失败),
		// 但两条都留着才能同时防住紧预算(1~2 次)与多成员多次 429 两种形态。
		pendingRateRetry := false
		var scoreRetries scoreRetryState
		var lastScoreError error
		// 灰度命中在请求首次解析分组时定下, 并沿用到整条重试链: 同一次请求若在灰度与常规路径之间
		// 来回切换, 重试可能落到第三个上游, 日志也无法解释这次请求到底算不算灰度样本。
		// 灰度成员一旦失败即在本请求内永久停用灰度, 避免冷却到期后又把同一请求送回刚失败的成员。
		canaryHit, canaryDecided, canaryFailed, canaryWarned := false, false, false, false
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
					// 上游限流是按固定窗口(60 秒)计的: 告诉客户端 1 秒后重试, 只会让它在整个窗口里
					// 反复撞墙, 每个请求都再吃一次 429。按真实窗口给出 Retry-After。
					c.Header("Retry-After", strconv.Itoa(ratelimit.WindowSeconds()))
				}
				response := inbound.TransformError(context.WithoutCancel(ctx), relayResponseError(cause, status))
				c.Data(response.StatusCode, "application/json", response.Body)
			}
		}
		waitAvailable := func() bool {
			if group.Mode == model.GroupModeScore && lastScoreError != nil {
				stop(lastScoreError, upstreamStatus(lastScoreError))
				return false
			}
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
			// Score bounds use the refreshed configuration below. Other modes
			// retain their existing retry semantics.
			// pendingRateRetry 的轮次不查预算: 它不消耗 totalAttempts(见 rateRetryBackoff),
			// 若在这里被拦, 429 之后的退避永远等不到执行。
			if !pendingRateRetry && group.Mode != model.GroupModeScore && group.RelayConfig.MaxTotalAttempts > 0 && totalAttempts >= group.RelayConfig.MaxTotalAttempts {
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
			autoMode := ""
			if group.Mode == model.GroupModeScore {
				autoMode = group.RelayConfig.AutoRouting
			}
			group = effectiveGroupForRequest(group, c.GetString("routing_preference"))
			// A rate retry is explicitly waiting to probe the same upstream again. In score mode,
			// ordinary failures stay excluded, but this retry must survive the request-local filter.
			if !pendingRateRetry {
				group = scoreRetries.filtered(group)
			}
			var autoExcluded []model.RelayRouteCandidate
			var shadowExcluded []model.RelayRouteCandidate
			observed, observedTask := request.autoObservation()
			if autoMode != "" {
				if !observed {
					observedTask = classifyAuto(raw.Body, requestProtocol, estIn, estOut)
					request.setAutoObservation(observedTask, autoMode)
					observed = true
				}
			}
			if autoMode == "active" && observed {
				task := observedTask
				if autoCircuitOpen(group.ID, task.name) {
					request.addRouteEvent("auto_routing", "auto_circuit_open")
				} else if !autoRampHit(group.ID, c.GetInt("api_key_id"), int(keyUserID), group.RelayConfig.AutoRampPercent) {
					request.addRouteEvent("auto_routing", "auto_ramp_hold")
				} else {
					eligible, excluded := autoCandidates(group, task)
					group, autoExcluded = eligible, excluded
					if len(group.Items) > 0 {
						autoProtectionGroup, autoProtectionTask, autoProtectionEngaged = group, task, true
					}
					request.addRouteEvent("auto_routing", task.name+":forced")
					if len(group.Items) == 0 {
						request.addRouteEvent("auto_routing", task.name+":no_candidate")
					}
				}
			} else if autoMode == "shadow" {
				task := observedTask
				eligible, excluded := autoCandidates(group, task)
				shadowExcluded = excluded
				state := RouteStateOf(group)
				candidates := slices.DeleteFunc(slices.Clone(eligible.Items), func(item model.GroupItem) bool {
					return upstreamItemLimited(item) || upstreamItemBlockedByPolicy(item) ||
						state.Cooldowns[item.ID] > time.Now().UnixMilli() || state.ProbeItemID == item.ID
				})
				reason := task.name + ":no_candidate"
				var prediction *autoPredictionDetails
				if len(candidates) > 0 {
					predicted := selectGroupItem(eligible, candidates)
					reason = fmt.Sprintf("%s:item=%d", task.name, predicted.ID)
					prediction = &autoPredictionDetails{
						ItemID: predicted.ID, Model: predicted.ModelName,
						CostUSD:   eligible.AutoRequestCosts[predicted.ID],
						TaskScore: eligible.AutoTaskScores[predicted.ID],
					}
				}
				request.addRouteEvent("auto_shadow", reason)
				request.setAutoPredictionDetails(autoDecisionDetailsJSON(task, prediction, excluded))
			}
			// Recheck after refresh: a live configuration change may lower the
			// budget or switch this request into score mode between attempts.
			// The pending rate retry is a bounded probe and does not consume totalAttempts,
			// so score mode must not reject it at the refreshed budget guard.
			if !pendingRateRetry && group.Mode == model.GroupModeScore && totalAttempts >= scoreAttemptLimit(group.RelayConfig.MaxTotalAttempts) {
				if lastScoreError != nil {
					stop(lastScoreError, upstreamStatus(lastScoreError))
				} else {
					stop(errors.New("relay maximum total attempts exceeded"), http.StatusServiceUnavailable)
				}
				return
			}

			// 手动模式取人工指定的成员, 故障转移模式按优先级选择未禁用且不在冷却中的成员。
			// 没有目标时等待重新选择, 期间人工切换渠道, 补齐成员或成员冷却到期即可让请求继续。
			if !canaryDecided {
				// 手动模式是人工硬约束, 灰度不参与: 否则"人工指定"会被比例悄悄改写。
				canaryHit = group.Mode != model.GroupModeManual &&
					group.RelayConfig.CanaryHit(keyUserID, c.GetInt("api_key_id"), rand.Uint64())
				canaryDecided = true
			}
			// 灰度先于策略选路: 命中者本轮只走灰度成员, 与价格/评分等策略无关。
			// 灰度成员此刻不可用(已停用、额度熔断、在冷却)则不占用本轮, 直接回落策略选路 ——
			// 灰度是"额外分一点流量给新渠道", 不能因为新渠道还没就绪就把请求卡住。
			var item model.GroupItem
			canary := false
			if group.AutoRequestCosts == nil && canaryHit && !canaryFailed {
				if canaryItem, ok := pickCanaryItem(group); ok {
					item, canary = canaryItem, true
				} else if !canaryWarned {
					// 每个请求只记一条: 灰度成员在冷却期间会连续回落, 每轮都记会把时间线刷满同一个原因。
					// 记下来才能区分"没抽中"与"抽中了却不可用"。
					canaryWarned = true
					request.addRouteEvent("selection", "canary_unavailable")
				}
			}
			if !canary {
				item = pickGroupItem(group)
			}
			decision := routeExplanation(group, item, canary)
			if group.AutoRequestCosts != nil {
				decision.Candidates = append(decision.Candidates, autoExcluded...)
				decision.Reason = "auto_" + observedTask.name
				// The actual candidate set is the one still admissible at selection
				// time. Compute scores after admission instead of using the pre-filter
				// candidate list, so the displayed score matches the chosen item.
				scoreCandidates := group.Items
				state := RouteStateOf(group)
				now := time.Now().UnixMilli()
				scoreCandidates = slices.DeleteFunc(slices.Clone(scoreCandidates), func(member model.GroupItem) bool {
					return upstreamItemLimited(member) || upstreamItemBlockedByPolicy(member) ||
						state.Cooldowns[member.ID] > now || state.ProbeItemID == member.ID
				})
				detailsCandidates := autoCandidatesForDetails(group, autoCandidateScores(group, scoreCandidates))
				for i := range detailsCandidates {
					if detailsCandidates[i].ItemID == item.ID {
						detailsCandidates[i].Reason = "selected"
					}
				}
				decision.Details = autoDecisionDetailsJSON(observedTask, nil, detailsCandidates)
			} else if autoMode == "shadow" {
				decision.Candidates = append(decision.Candidates, shadowExcluded...)
			}
			request.setRouteDecision(decision)
			if item.ID == 0 {
				// Score retries exhaust the currently eligible remaining candidates;
				// never wait for cooldown expiry or probe completion to revisit one.
				if group.Mode == model.GroupModeScore && lastScoreError != nil {
					stop(lastScoreError, upstreamStatus(lastScoreError))
					return
				}
				if group.AutoRequestCosts != nil && len(group.Items) == 0 {
					status := http.StatusServiceUnavailable
					if observedTask.name == "unsupported" {
						status = http.StatusBadRequest
					}
					stop(fmt.Errorf("auto routing has no compatible upstream for task %s", observedTask.name), status)
					return
				}
				// 分组一个成员都没有时, 等待永远等不到上游: 直接点明配置缺失, 不再报笼统的"无可用上游"。
				if len(group.Items) == 0 {
					stop(fmt.Errorf("group %q has no upstream members configured", group.Name), http.StatusServiceUnavailable)
					return
				}
				// 全部候选都因上游 429 在本窗口被强制跳过时, 别再说"等待被禁用": 阻塞原因是上游限流,
				// 恢复时机是窗口结束(固定 60 秒), 与分组是否开启排队无关。
				if reason := limitedUpstreamReason(group); reason != "" {
					stop(errors.New(reason), http.StatusTooManyRequests)
					return
				}
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

			// 成员指向的渠道已被删除时同样等待, 该成员可能很快被改回可用渠道。
			channel, err := op.ChannelGet(channelModel.ChannelID)
			if err != nil {
				if !waitAvailable() {
					return
				}
				continue
			}

			// 动态选择渠道 Key：根据 channel.KeyRotationStrategy 从可用 keys 中选择
			// 如果 Grant 绑定了固定 key (grant.ChannelKey != nil)，优先使用固定 key（向后兼容）
			// 否则使用轮换策略动态选择
			//
			// 订阅渠道例外: 一个渠道挂多个订阅账号时, "用哪个账号"必须由轮换策略决定,
			// 而不是由分组成员碰巧引用了哪个 Grant 决定。此时 grant.ChannelKey 只是占位,
			// 不参与本轮选择 —— 它仍然存在, 因为 Grant 表要求 ChannelKeyID 非空。
			var channelKey *model.ChannelKey
			if channel.Dialect.Subscribable() {
				// 订阅渠道: 从该渠道所有可用 key 里按策略选, 忽略 Grant 绑定的占位 key。
				channelKey, err = op.SelectChannelKey(channelModel.ChannelID, channel.KeyRotationStrategy)
				if err != nil {
					if !waitAvailable() {
						return
					}
					continue
				}
			} else if grant.ChannelKey != nil && grant.ChannelKey.Enabled {
				// Grant 绑定了固定 key，直接使用（兼容旧行为）
				channelKey = grant.ChannelKey
			} else {
				// 使用轮换策略动态选择 key
				channelKey, err = op.SelectChannelKey(channelModel.ChannelID, channel.KeyRotationStrategy)
				if err != nil {
					if !waitAvailable() {
						return
					}
					continue
				}
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

			// 检查请求是否超过模型的上下文限制。
			if exceeded, limit, estimated := checkContextLimit(channelModel.Name, raw.Body); group.AutoRequestCosts == nil && exceeded {
				err := fmt.Errorf("Request exceeds model context limit: estimated %d tokens, limit %d tokens", estimated, limit)
				request.markFailed(err, "", nil)
				// 使用 context_length_exceeded 错误类型，与上游错误保持一致
				response := inbound.TransformError(ctx, &llm.ResponseError{
					StatusCode: http.StatusBadRequest,
					Detail: llm.ErrorDetail{
						Message: err.Error(),
						Type:    "invalid_request_error",
						Code:    "context_length_exceeded",
					},
				})
				c.Data(response.StatusCode, "application/json", response.Body)
				c.Abort()
				return
			}

			// 在渠道授权支持的协议内选出本轮上游协议, 按该协议的路径与授权绑定的凭据构造出站转换器。
			// 先于登记本轮目标: 选中的协议是本轮目标的一部分, 需与渠道和模型一并推给界面。
			outbound, targetProtocol, passthrough, err := buildOutbound(channel, grant, *channelKey, requestProtocol, format)

			// 为本轮上游调用建立独立取消入口并登记当前目标; 取消原因用于区分人工中止与响应超时。
			roundCtx, cancelRoundCause := context.WithCancelCause(ctx)
			// 采集本轮上游连接的阶段耗时。挂在 ctx 上后, 同协议透传与跨协议转换两条路径都会回填:
			// 前者走 client.Do, 后者走 pipeline, 都使用这个 ctx 发起请求。
			roundTiming := &rhttp.ConnTiming{}
			roundCtx = rhttp.WithTiming(roundCtx, roundTiming)
			// 人工中止和本轮完成都使用普通 canceled 原因, 超时回调则写入具体的超时错误。
			cancelRound := func() {
				cancelRoundCause(context.Canceled)
			}
			request.startRound(cancelRound, channel.Name+" · "+channelKey.Name, channelModel.Name, targetProtocol,
				channel.ID, channel.UserID, channel.ShareCode, channelModel.SupplyPrice, channelModel.PriceCurrency, item.ID)
			request.setTargetKeyID(channelKey.ID)
			// 媒体计费上下文: 仅媒体形态模型登记; 文本模型此项保持零值, settlement 走 token 路径不变。
			// 选择上游时只在本组成员内挑选 kind=image 的成员, 故只要命中即可认定本轮用媒体计费。
			if channelModel.Kind != model.MediaKindText {
				request.setMediaTarget(channelModel.Kind, channelModel.MediaSupply, grant.ID)
			}

			// 出口池: 渠道配了出口就按实测耗时择优, 没配则 egress 为零值, 沿用渠道自身的代理配置。
			// 选择在每轮独立进行, 因此某轮失败后下一轮可能换到另一个出口, 与成员换路是同一套节奏。
			egress, _, egressErr := pickEgress(channel, request.ClientCountry)
			if egressErr != nil {
				// 配了出口却一个都没启用属于配置错误: 换成员也不会好, 直接结束并说明原因,
				// 而不是悄悄直连 —— 那会让流量从管理员明确停用的线路出去。
				request.markFailed(egressErr, "", nil)
				cancelRound()
				releaseRouteProbe(group, item.ID)
				stop(egressErr, http.StatusBadGateway)
				return
			}
			request.setRoundEgress(egress)
			if egress.ID != 0 {
				// 出口选择登记为一条路由事件: 排查"这轮为什么慢"时要能看出走的是哪条线路。
				request.addRouteEvent("dispatch", "egress:"+egress.Name)
			}

			// 上游侧限流: 每次实际尝试独立预留 RPM/TPM 额度, 触顶即记为限流尝试并跳过该成员:
			// 不计失败率、不冷却, 由选路侧自动跳过受限成员。
			upstreamScopes := upstreamRateScopes(channel.ID, channelKey.ID, channelModel.ID, channelModel.Name)
			upEstIn, upEstOut := int64(1), int64(1)
			if !isMediaProtocol(requestProtocol) {
				upEstIn, upEstOut = estimateTokens(raw.Body)
			}
			var upstreamReservation *ratelimit.Reservation
			if err == nil {
				// 持有恢复探测名额的请求改用 ReserveProbe: 它忽略上游 429 强制标记, 其余核算照旧。
				//
				// 为什么必须在准入层一并放开, 而不是只放开选路: 只放开选路会形成死锁 ——
				// 成员被标记 → 选路放行探测 → 准入层看到标记又拒绝(一次上游都没打) →
				// 请求失败、成功不了 → 清不掉标记 → 下一轮继续拒绝。探测名额形同虚设,
				// 整组仍要空等窗口重置, 这正是用户看到"一个账号都没跑通"的那条链路。
				var decision ratelimit.Decision
				if probeHeldBy(group.ID, item.ID) {
					decision = limiter.ReserveProbe(upstreamScopes, upEstIn, upEstOut)
					request.addRouteEvent("admission", "forced_probe_bypass")
				} else {
					decision = limiter.Reserve(upstreamScopes, upEstIn, upEstOut)
				}
				if !decision.Allowed {
					recordScopeUsage(upstreamScopes, 0, 0, 0, 1) // admission rejected; no upstream dispatch took place.
					request.addRouteEvent("admission", "upstream_rate_limited")
					// 尝试行照常收尾(startRound 已登记在途行), 但状态记 limited 而不是失败。
					request.finishRound("admission rejected: upstream rate limited", "limited")
					cancelRound()
					releaseRouteProbe(group, item.ID)
					// 准入拒绝没有真正向上游发出请求, 本就不消耗跨成员尝试预算(预算在真正向上游
					// 发出请求时才递增): 否则按估算 token 预留被拒时(TPM 余量不足以容纳本请求但窗口
					// 未满, 选路侧的 Blocked 判不出这种情况), 同一成员会被反复选中、反复拒绝,
					// 几毫秒内烧光 MaxTotalAttempts, 请求以 503 告终。
					// 不立刻重选而是走等待路径限速: 排队按 MaxWaitSeconds 预算等待, 等待关闭则按 429
					// 立即回绝 —— 既不空转, 也不越过等待队列的公平性。
					if !waitAvailable() {
						return
					}
					continue
				}
				upstreamReservation = decision.Reservation
				waiter.leave()
				// 跨成员尝试预算只在真正向上游发出请求的轮次消耗: 准入拒绝与等待轮都不计,
				// 换成员、超时与 5xx 等真实尝试照常计, MaxTotalAttempts 仍是重试上限。
				totalAttempts++
				// 这次退避换来的重试已经真正发出去了, 豁免随之失效: 后续轮次回到正常预算约束。
				pendingRateRetry = false
				request.addRouteEvent("dispatch", "upstream_admitted")
			}

			roundStartedAt := time.Now() // 本轮上游调用的开始时间, 用于统计首个有效响应耗时。

			// 请求上游并等待首个有效响应: 非流式等待完整响应, 流式等待首个事件。
			// 同协议渠道原样直通, 跨协议渠道经转换后请求; 此时尚未写给客户端, 失败仍可换目标重试。
			dispatched := false // 本轮是否真正向上游发出了请求; 生图/生视频发出后一律不再重发(见 mediaOneShot)。
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
				// 从这里开始本轮已真正向上游发出请求。
				dispatched = true
				// 客户端与渠道协议一致时直接透传, 其余组合通过 pipeline 转换。
				if passthrough {
					result, err = sendPassthrough(roundCtx, format, raw, channel, egress, outbound, metadata.Streaming, channelModel.Name)
				} else {
					result, err = sendConverted(roundCtx, format, raw, channel, egress, outbound, metadata.Streaming)
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
					}
				}
			}

			// 上游调用一结束就取快照并登记: 失败轮次同样要留下建连耗时,
			// 否则统计里只剩成功轮次的数据, 会把"连不上"的慢完全隐藏掉。
			request.setRoundTiming(roundTiming.Snapshot())

			if err != nil {
				// 出站构造等未走到准入的失败同样消耗尝试预算: 否则配置性错误会让重试循环按成员
				// 重试间隔一直空转到整请求超时(手动模式下没有冷却兜底, 尤其明显)。
				if !dispatched {
					totalAttempts++
				}
				// 媒体创建单发语义: 生图/生视频的请求一旦真正发到上游, 上游可能已落地副作用
				// (生成图片、创建视频任务)。此后任何失败都不再换目标重发 —— 含 429、响应超时与 5xx:
				// 重发必然重复创建, 而计费与任务归属只认最终响应, 被放弃那几轮的产物既不计费也无归属。
				// 未真正发出请求的失败(选路、出站构造)不受影响, 仍按原流程换目标重试。
				mediaOneShot := dispatched && isMediaProtocol(requestProtocol)
				// 按错误分类决定重试/换渠道/透传: 语义(code/type/文本)优先、状态码兜底, 规则见 upstream_error.go。
				class, ruleName := classifyUpstreamErrorForChannel(err, channel.Dialect)
				request.addRouteEvent("error_class", ruleName)
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
					if mediaOneShot {
						// 人工中止的媒体轮次同样只停不换目标: 本轮创建请求可能已被上游受理。
						stop(err, upstreamStatus(err))
						return
					}
					continue
				}
				// 灰度成员本轮失败: 本请求内不再走灰度, 后续轮次完全回落到原本的选择策略。
				if canary {
					canaryFailed = true
				}
				if group.Mode == model.GroupModeScore {
					// Rate failures are bounded by rateRetryBackoff and must be probed again after the wait;
					// all other score-mode failures remain excluded for this request.
					if class != ErrorClassRate {
						scoreRetries.exclude(item, class == ErrorClassQuota)
					}
					lastScoreError = err
				}
				// 上游限流视为限流而非故障: 标记该上游当前窗口触顶供选路跳过, 不计失败率也不冷却。
				if class == ErrorClassRate {
					releaseRouteProbe(group, item.ID)
					upstreamReservation.Settle(0, 0, true)
					limiter.MarkBlocked(upstreamScopes, upEstIn, upEstOut)
					cancelRound()
					if mediaOneShot || c.Writer.Written() {
						// 429 也可能发生在上游受理创建之后, 媒体保守不重发, 直接透传限流错误。
						stop(err, upstreamStatus(err))
						return
					}
					// 上游限流的重试必须退避, 且不消耗跨成员尝试预算。
					//
					// 缺陷(线上实测 2026-11-17): 这里原本是裸 continue —— 打完一次 429 立刻重选同一成员接着打。
					// 单账号订阅渠道只有一个候选, 摘掉探测名额后下一轮又选它, 于是 MaxTotalAttempts 的 10 次
					// 在约 2 秒内(实测每次 215~317ms)全部砸向上游。上游刚说"当前窗口已满", 网关却继续猛打,
					// 把剩余额度一秒烧光, 用户侧从"偶发 429"变成"整段请求必然失败"。
					//
					// 不计入 totalAttempts 的理由: 429 代表请求根本没被模型处理, 与准入拒绝同性质;
					// 若照计, 退避重试反而更快耗尽预算, 比不修更差。真正打到模型并失败的轮次照旧计。
					if _, ok := rateRetryBackoff(ctx, request, group, rateRetries); ok {
						rateRetries++
						pendingRateRetry = true
						continue
					}
					stop(err, upstreamStatus(err))
					return
				}
				// 请求本身有问题或内容被安全策略拦截: 换渠道必然同样失败, 直接透传上游错误。
				// 内容拦截不换渠道, 是为了不跨供应商绕过内容审查。
				if class == ErrorClassClient || class == ErrorClassPolicy {
					releaseRouteProbe(group, item.ID)
					upstreamReservation.Settle(0, 0, true)
					cancelRound()
					stop(err, upstreamStatus(err))
					return
				}
				// 其余失败未产生用量, 退还本次尝试的限流预留。
				upstreamReservation.Settle(0, 0, true)
				cancelRound()
				// 本轮真实失败只计入当前渠道和成员, 客户端取消与人工中止不计为渠道故障。
				// 模型没上架不是渠道故障: 只换路不计失败率, 避免拉低健康渠道的成功率。
				if class != ErrorClassModel {
					metrics := model.StatsMetrics{WaitTime: time.Since(roundStartedAt).Milliseconds(), RequestFailed: 1}
					_ = op.ChannelStatsUpdate(channel.ID, metrics)
					_ = op.ChannelModelStatsUpdate(channelModel.ID, metrics)
					_ = op.ChannelKeyStatsUpdate(channelKey.ID, metrics)
					op.ChannelDailyStatsUpdate(channel.ID, metrics)
					op.ChannelModelDailyStatsUpdate(channelModel.ID, metrics)
					recordMemberMetric(group.ID, item.ID, channelModel.ID, group.RelayConfig.MetricWindowSize, time.Since(roundStartedAt).Milliseconds(), false)
					// 出口同样记一次失败: 出口成绩是"这条线路本身行不行", 与成员成绩互不替代。
					recordEgressSample(egress.ID, group.RelayConfig.MetricWindowSize, time.Since(roundStartedAt).Milliseconds(), false)
					// 记录 Key / 渠道模型错误用于故障熔断
					// 余额不足立即熔断整个渠道; 凭据失效仍只影响该 Key, 临时故障隔离到渠道模型。
					if class == ErrorClassQuota {
						op.RecordChannelQuotaError(channel.ID)
					} else if channelKey != nil && class == ErrorClassAuth {
						op.RecordChannelKeyError(channelKey.ID, string(class))
					} else if class == ErrorClassOverload || class == ErrorClassTransient || class == ErrorClassUnknown {
						op.RecordChannelModelError(channelModel.ID, string(class))
					}
				}
				if group.Mode == model.GroupModeScore {
					// One failed channel/model gets one chance per request, regardless
					// of member retry settings, key rotation or shared recovery state.
					cooldown := classCooldownSeconds(class)
					if cooldown <= 0 {
						cooldown = group.RelayConfig.MemberCooldownSeconds
					}
					coolItem(group, item.ID, cooldown)
					coolEgress(egress.ID, cooldown)
					if mediaOneShot || c.Writer.Written() {
						stop(err, upstreamStatus(err))
						return
					}
					continue
				}
				// 凭据失效/额度耗尽/过载/模型缺失/请求编码不兼容: 原地重试无益, 冷却当前成员后换渠道;
				// 没有其他可用成员时等待同样无益, 立即透传最后错误(模型缺失包一层友好提示)。
				if class == ErrorClassAuth || class == ErrorClassQuota || class == ErrorClassOverload || class == ErrorClassModel || class == ErrorClassCompatibility {
					coolItem(group, item.ID, classCooldownSeconds(class))
					// 出口一并冷却: 下一轮即便还选同一个成员, 也不会再走这条刚失败的线路。
					coolEgress(egress.ID, classCooldownSeconds(class))
					// 媒体单发: 冷却照做(保护其他请求不再撞同一个故障端点), 但不换目标重发,
					// 落到下方按类别透传错误(模型缺失包一层友好提示)。
					if !c.Writer.Written() && !mediaOneShot && group.Mode != model.GroupModeManual && hasOtherEligible(group, item.ID) {
						continue
					}
					if class == ErrorClassModel {
						stop(modelUnavailableError(request.Model), http.StatusNotFound)
					} else {
						stop(err, upstreamStatus(err))
					}
					return
				}
				// 媒体单发兜底: 响应超时、5xx 与网络错误这类"可重试"失败也不再重发, 上游可能已受理创建。
				if mediaOneShot {
					stop(err, upstreamStatus(err))
					return
				}
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
			// 请求级取消可能与上游成功同时到达, 此时不应提交响应或继续重试。
			if ctx.Err() != nil {
				releaseRouteProbe(group, item.ID)
				settleUpstream(upstreamReservation, upstreamScopes, result.usage)
				if result.events != nil {
					result.events.Close()
				}
				cancelRound()
				finalizeCanceledSuccess(request, result, ctx.Err())
				if ctx.Err() == context.DeadlineExceeded && !c.Writer.Written() {
					response := inbound.TransformError(context.WithoutCancel(ctx), &llm.ResponseError{StatusCode: http.StatusGatewayTimeout, Detail: llm.ErrorDetail{Message: "request deadline exceeded", Type: "relay_timeout"}})
					c.Data(response.StatusCode, "application/json", response.Body)
				}
				return
			}
			// 记录本轮已经取得可提交的上游响应。
			// 流式响应此刻只拿到首个事件, 响应尚未交付完: 终态由流末分支统一收尾(见下方 finishRound),
			// 这里提前记成功会让尝试行先写 success 再被覆盖, 也把在途的流伪装成已成功。
			if !metadata.Streaming {
				request.finishRound("", "success")
			}
			roundWaitTime := time.Since(roundStartedAt).Milliseconds() // 流式响应只统计等待首帧的时间。
			// 首帧只保存延迟, 不代表流已成功。评分与熔断恢复必须等完整终态。
			recordSuccess := func() {
				recordRouteSuccess(group, item.ID)
				// 记录成员运行指标(首响应耗时与成功), 供延迟/成功率/综合评分策略排名。
				recordMemberMetric(group.ID, item.ID, channelModel.ID, group.RelayConfig.MetricWindowSize, roundWaitTime, true)
				// 出口成绩同源记录: 下一轮选出口就按这份实测耗时排序。
				recordEgressSample(egress.ID, group.RelayConfig.MetricWindowSize, roundWaitTime, true)
				// 成功即证明上游限流已解除: 撤销本窗口的 429 强制触顶标记。
				// 少了这一步, 触顶成员虽被允许做探测请求, 探测成功后标记仍在,
				// 后续请求会继续被判为触顶直到窗口重置 —— 探测就失去了意义。
				limiter.ClearBlocked(upstreamScopes)
				// 成功请求重置 Key 错误计数
				if channelKey != nil {
					op.ResetChannelKeyErrors(channelKey.ID)
					op.ResetChannelModelErrors(channelModel.ID)
				}
			}
			if !metadata.Streaming {
				recordSuccess()
			}
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
				n, err := c.Writer.Write(result.body)
				if err == nil && n != len(result.body) {
					err = io.ErrShortWrite
				}
				if n > 0 {
					request.markCommitted(false)
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
			// 告诉反向代理不要攒缓冲: nginx 默认 proxy_buffering on, 会把流式响应整段攒住,
			// 客户端表现为"卡很久然后一次性吐完", 流式等于失效。日志流接口已设过同样的头。
			c.Header("X-Accel-Buffering", "no")
			c.Header("Cache-Control", "no-cache")
			var encoded bytes.Buffer
			var chunks []*httpclient.StreamEvent
			// 聚合缓冲上限: chunks 是计费聚合的输入, 无上限时恶意上游可用长流把内存打爆。
			// 超限按本轮失败收场; 已写给客户端的部分不受影响(提交后的失败不再重试)。
			var aggregatedBytes int
			event := result.first
			last := result.last // 已转发的最后一个事件是否已按客户端协议结束整个响应流。
			committed := false
			upstreamFailure := true // 下游写入/本地处理失败不应污染上游评分。
			idleErr := errors.New("upstream stream idle timeout")
			for {
				if event != nil {
					var allowed bool
					aggregatedBytes, allowed = trackAggregatedChunk(aggregatedBytes, event.Data)
					if !allowed {
						err = fmt.Errorf("aggregated stream exceeds %d bytes", maxAggregatedStreamBytes)
						upstreamFailure = false
						break
					}
					chunks = append(chunks, event)
					// 每个事件计一个输出字符供日志页展示, 按节流间隔发布。
					request.addOutput()
					encoded.Reset()
					if encodeErr := sse.Encode(&encoded, sse.Event{Id: event.LastEventID, Event: event.Type, Data: event.Data}); encodeErr != nil {
						err = encodeErr
						upstreamFailure = false
						break
					}
					n, writeErr := c.Writer.Write(encoded.Bytes())
					if writeErr == nil && n != encoded.Len() {
						writeErr = io.ErrShortWrite
					}
					if !committed && n > 0 {
						request.markCommitted(true)
						committed = true
					}
					if writeErr != nil {
						err = writeErr
						upstreamFailure = false
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
					if err == nil {
						err = io.ErrUnexpectedEOF // 没有协议结束事件的断流不是完整响应。
					}
					break
				}
				event = result.events.Current()
				// 已提交的响应不能再换目标重试, 结束事件自身携带的失败原样转发给客户端, 并在转发后作为本请求终态。
				last, err = inspectStreamEvent(format, event)
			}
			request.finishStream()
			result.events.Close()
			cancelRound()
			// 使用客户端协议转换器聚合已转发事件, 统一取得最终响应正文和用量。
			// 生图流式不走转换器聚合: 库的生图入站把流式聚合判为不支持, 而 OpenAIImages 流式
			// 以 image_generation.completed 事件携带最终图, 按事件自建 {"data":[...]} 计费体,
			// 张数口径与同步响应一致(数 data 数组长度)。
			var responseBody []byte
			var meta llm.ResponseMeta
			var aggregateErr error
			if requestProtocol == model.ProtocolOpenAIImage {
				responseBody, meta = aggregateImageStreamEvents(chunks)
				result.usage = meta.Usage
			} else {
				responseBody, meta, aggregateErr = inbound.AggregateStreamChunks(context.WithoutCancel(ctx), chunks)
				if aggregateErr == nil {
					result.usage = meta.Usage
				}
			}
			if aggregateErr != nil && err == nil {
				err = aggregateErr
				upstreamFailure = false
			}
			// 结算口径(显式策略, 不是顺手为之): 部分流已写给客户端、却聚合不出用量(上游没发用量帧
			// 或聚合失败)时, 按「未产生用量」结算 —— 释放预扣、不扣费。已交付的部分输出要不要计费
			// 是计费侧的未决口径(估读 token 或按输出字符折算都会自造账目), 定案前转发侧只按上游
			// 报告的用量结算, 绝不发明计费。
			settleUpstream(upstreamReservation, upstreamScopes, result.usage)
			// 客户端取消只在「流没有完整送达」时才覆盖错误: 结束事件已转发且无错误的轮次
			// 是完整交付, 此刻与断开竞态发生也必须保住成功终态, 否则已计费的成功被记成取消。
			if ctx.Err() != nil && !(last && err == nil) {
				err = ctx.Err()
			}
			if err != nil {
				request.finishRound(err.Error(), attemptStatusOf(err))
			} else {
				request.finishRound("", "success")
				recordSuccess()
			}
			// 沿用首响应失败的分类: 限流/客户端/策略/模型缺失不降成功率。
			countFailure := false
			if err != nil {
				if ctx.Err() != nil || !upstreamFailure {
					releaseRouteProbe(group, item.ID)
				} else {
					class, ruleName := classifyUpstreamErrorForChannel(err, channel.Dialect)
					request.addRouteEvent("error_class", ruleName)
					switch class {
					case ErrorClassRate:
						releaseRouteProbe(group, item.ID)
						limiter.MarkBlocked(upstreamScopes, upEstIn, upEstOut)
					case ErrorClassClient, ErrorClassPolicy:
						releaseRouteProbe(group, item.ID)
					default:
						countFailure = class != ErrorClassModel
						if countFailure {
							recordMemberMetric(group.ID, item.ID, channelModel.ID, group.RelayConfig.MetricWindowSize, roundWaitTime, false)
							recordEgressSample(egress.ID, group.RelayConfig.MetricWindowSize, roundWaitTime, false)
							if class == ErrorClassQuota {
								op.RecordChannelQuotaError(channel.ID)
							} else if channelKey != nil && class == ErrorClassAuth {
								op.RecordChannelKeyError(channelKey.ID, string(class))
							} else if class == ErrorClassOverload || class == ErrorClassTransient || class == ErrorClassUnknown {
								op.RecordChannelModelError(channelModel.ID, string(class))
							}
						}
						if class == ErrorClassAuth || class == ErrorClassQuota || class == ErrorClassOverload || class == ErrorClassModel || class == ErrorClassCompatibility {
							coolItem(group, item.ID, classCooldownSeconds(class))
							coolEgress(egress.ID, classCooldownSeconds(class))
						} else {
							if failedItemID != item.ID {
								failures = 0
							}
							recordRouteFailure(group, item.ID, failures+1)
						}
					}
				}
			}
			// 流式响应结束并聚合出用量后, 按最终结果完成本轮渠道和成员统计(供货价口径)。
			metrics := usageMetrics(channelModel.SupplyPrice, result.usage)
			metrics.WaitTime = roundWaitTime
			if err == nil {
				metrics.RequestSuccess = 1
			} else if countFailure {
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
	var maxInput, maxOutput, maxMediaImage, maxMediaVideo float64
	var hasImageItem, hasVideoItem bool
	requested := request.requestedMediaUnits()
	for _, item := range group.Items {
		maxInput = max(maxInput, item.UserPrice.Input)
		maxOutput = max(maxOutput, item.UserPrice.Output)
		switch item.Kind {
		case model.MediaKindImage:
			hasImageItem = true
			maxMediaImage = max(maxMediaImage, item.UserMedia.MaxImagesPerRequest())
		case model.MediaKindVideo:
			hasVideoItem = true
			maxMediaVideo = max(maxMediaVideo, item.UserMedia.MaxVideosPerRequest(videoReserveSeconds(requested.Seconds)))
		}
	}
	capTokens, err := op.SettingGetInt(model.SettingKeyBalanceReserveCap)
	if err != nil || capTokens <= 0 {
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
	// 生视频的时长由请求自带(seconds 字段), 无需像生图那样按倍数保守估算: 一次请求只产出一条视频, 冻结一次即可。
	if hasVideoItem {
		estimate += maxMediaVideo
	}
	// API Key 配额检查: 在余额预扣之前检查请求数/token 配额。
	if request.apiKeyID > 0 {
		if err := op.CheckAPIKeyQuota(request.requestCtx, request.apiKeyID); err != nil {
			if quotaErr, ok := err.(op.QuotaExceeded); ok {
				return fmt.Errorf("quota exceeded: %s %d/%d", quotaErr.Kind, quotaErr.Current, quotaErr.Limit)
			}
			return fmt.Errorf("failed to check quota: %w", err)
		}
	}
	// 用户花费预算检查: 与配额同一位置(预扣前)。block 档超限直接拒绝;
	// warn 档只在 op 内部发去重通知。查询失败按预算系统故障放行, 不误杀请求。
	if err := op.CheckUserBudget(request.requestCtx, request.UserID, request.apiKeyID); err != nil {
		if budgetErr, ok := err.(model.BudgetExceeded); ok {
			return fmt.Errorf("budget exceeded: %s spent %.2f/%.2f", budgetErr.Scope, budgetErr.Current, budgetErr.Limit)
		}
		return fmt.Errorf("failed to check budget: %w", err)
	}

	reservationID, err := op.BillingReserve(request.UserID, request.ID, estimate)
	if err != nil {
		return err
	}
	request.SetReservation(reservationID)
	return nil
}

// attemptStatusOf 上游结果的尝试终态: 人工中止/客户端断开为 canceled, 超时单列, 其余按错误分类映射。
func attemptStatusOf(err error) string {
	if err == nil {
		return "success"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if isTimeoutError(err) {
		return "timeout"
	}
	class, _ := classifyUpstreamError(err)
	return attemptStatusOfClass(class)
}

// finalizeCanceledSuccess 处理「请求级取消与上游成功同时到达」的收尾: 本轮已拿到可提交的上游响应,
// 不再提交也不再重试, 以取消终态定稿。尝试行只按取消收尾一次(不再先记成功再覆盖),
// 正文随终态保留: 非流式已完整取得 result.body, 生图/生视频的图片或视频任务可能已在上游落地,
// 丢掉正文会让 recordVideoJob 与媒体用量双双落空 —— 冻结额按「未产生用量」释放, 上游却已产生成本。
// 流式响应的正文要到聚合后才有, result.body 恒为空, 行为与原先一致。
func finalizeCanceledSuccess(request *RequestState, result *upstreamResponse, cause error) {
	request.finishRound(cause.Error(), attemptStatusOf(cause))
	request.markCanceled(cause, string(result.body), result.usage)
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

// checkContextLimit 检查请求的 token 数是否超过模型的上下文限制。
// 使用 estimateTokens 的粗略估算（字符数/4）来检查，避免明显超限的请求被转发。
// 返回 true 表示限制已超过，应拒绝请求。
func checkContextLimit(modelName string, bodyBytes []byte) (exceeded bool, limit int, estimated int64) {
	capability, ok := op.CapabilityGet(modelName)
	if !ok || capability.Context == 0 {
		// 没有能力信息或未设置限制，放行
		return false, 0, 0
	}

	// 粗略估算输入 token 数（与 estimateTokens 保持一致的算法）
	estInput := int64(len(bodyBytes))/4 + 32

	if estInput > int64(capability.Context) {
		return true, capability.Context, estInput
	}

	return false, capability.Context, estInput
}

// rateRetryBackoffMaxRetries 是单个请求内允许的"上游 429 后退避重试"次数上限。
//
// 与 MaxTotalAttempts 分开计数: 429 说明请求根本没被模型处理(与准入拒绝同性质),
// 计入总预算会让退避重试反而更快撞到"maximum total attempts exceeded"。
// 上限定 3 是实测口径: 上游 429 多半在几秒内自愈, 三次退避足够跨过一次瞬时窗口;
// 再多也不会让一次必然失败的请求变得更可能成功, 只让用户多等。
const rateRetryBackoffMaxRetries = 3

// rateRetryWait 算第 retry 次(0 起)429 重试前的退避时长: 基数取成员重试间隔, 指数递增并封顶 8 倍。
//
// 抽成纯函数是为了让时长曲线可被确定性断言, 不必在测试里真的等上几秒。
func rateRetryWait(group model.Group, retry int) time.Duration {
	wait := time.Duration(group.RelayConfig.MemberRetryIntervalSeconds) * time.Second
	if wait <= 0 {
		wait = time.Second
	}
	mult := 1 << uint(retry)
	if mult > 8 {
		mult = 8
	}
	return wait * time.Duration(mult)
}

// rateRetryBackoff 在一次上游 429 之后退避等待, 返回是否应当继续重试。
//
// 为什么必须退避: 上游刚用 429 明确表示"本窗口已满"。立即重选同一成员再打(原实现是裸 continue),
// 单账号订阅渠道下 10 次重试会在约 2 秒内全部砸向已满的窗口(实测每次 215~317ms),
// 把剩余额度瞬间烧光 —— 用户侧从"偶发限流"变成"这段请求必然失败"。
//
// 退避时长取 max(成员重试间隔, 指数递增): 首次即等满配置的重试间隔(默认 1 秒),
// 之后 2s、4s, 最多 8s, 与常见的上游固定窗口节奏相称。
// 用 ctx 的剩余预算封顶: 请求总超时(默认 600s)必须优先于退避, 不能把用户挂死在这里。
func rateRetryBackoff(ctx context.Context, request *RequestState, group model.Group, retry int) (time.Duration, bool) {
	if retry >= rateRetryBackoffMaxRetries {
		request.addRouteEvent("rate_retry", "retry_budget_exhausted")
		return 0, false
	}
	wait := rateRetryWait(group, retry)
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < wait {
			// 剩余预算不足: 不硬等到超时, 直接把这轮的限流错误透传, 让客户端自己排。
			request.addRouteEvent("rate_retry", "deadline_insufficient")
			return 0, false
		}
	}
	request.addRouteEvent("rate_retry", wait.String())
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return 0, false
	case <-timer.C:
		return wait, true
	}
}

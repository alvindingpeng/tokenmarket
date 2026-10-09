package relay

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/charmbracelet/log"
	"github.com/looplj/axonhub/llm"
)

// 请求 ID 以启动时刻的 Unix 秒为基数: 跨进程重启唯一, 且不超出前端 number 安全整数范围。
var bootIDBase = uint64(time.Now().Unix()) * 1_000_000

// nextRequestID 生成全局唯一请求 ID。
func nextRequestID() uint64 { return bootIDBase + idSeq.Add(1) }

// rowLocked 生成请求的持久化行快照; 调用方必须持有锁。
func (r *RequestState) rowLocked() model.RelayRequest {
	events, _ := json.Marshal(r.RouteEvents)
	cached, writeCached := int64(0), int64(0)
	if r.Usage.PromptTokensDetails != nil {
		cached = r.Usage.PromptTokensDetails.CachedTokens
		writeCached = r.Usage.PromptTokensDetails.WriteCachedTokens
	}
	return model.RelayRequest{
		ID:                   r.ID,
		ClientIP:             r.ClientIP,
		UserID:               r.UserID,
		APIKeyID:             r.apiKeyID,
		APIKeyName:           r.APIKeyName,
		GroupID:              r.GroupID,
		Model:                r.Model,
		ReasoningEffort:      r.ReasoningEffort,
		Protocol:             int(r.Protocol),
		Status:               string(r.Status),
		StartedAt:            r.StartedAt,
		FinishedAt:           finishedAtOf(r),
		DurationNs:           int64(r.Duration),
		FirstTokenDurationNs: int64(r.FirstTokenDuration),
		StreamDurationNs:     int64(r.StreamDuration),
		ResponseDurationNs:   int64(r.ResponseDuration),
		Round:                r.Round,
		PromptTokens:         r.Usage.PromptTokens,
		CompletionTokens:     r.Usage.CompletionTokens,
		CachedTokens:         cached,
		CacheWriteTokens:     writeCached,
		Cost:                 r.Cost,
		OutputChars:          r.OutputChars,
		TargetChannelKey:     r.TargetChannelKey,
		TargetChannelCode:    r.TargetChannelCode,
		TargetModel:          r.TargetModel,
		TargetProtocol:       int(r.TargetProtocol),
		Error:                r.Error,
		RequestBody:          maskedBody(r.requestBody),
		ResponseBody:         maskedBody(r.responseBody),
		RouteEvents:          string(events),
	}
}

// finishedAtOf 终态请求给出结束时间, 运行中零值。
func finishedAtOf(r *RequestState) time.Time {
	if r.Status == StatusRunning || r.Status == StatusCommitted {
		return time.Time{}
	}
	return r.StartedAt.Add(r.Duration)
}

// maskedBody 对保存的正文按系统配置脱敏, 关闭正文保存时置空。
func maskedBody(body string) string {
	if !op.ShouldStoreBody() {
		return ""
	}
	return op.MaskJSONBody(body)
}

// persistRequestLocked 落库请求快照; 写失败只告警, 不影响转发主流程。
// 终态定稿与初始登记为同步写, 保证服务被杀时日志不缺终态; 调用方必须持有锁。
func persistRequestLocked(r *RequestState) {
	if err := op.RelayRequestSave(r.rowLocked()); err != nil {
		log.Warnf("relay request persist failed: %v", err)
	}
}

// attemptRow 生成本轮上游尝试的持久化行。
func attemptRow(r *RequestState, itemID, channelID int, channelCode, targetModel string, protocol model.Protocol, startedAt time.Time, status, errText string) model.RelayRequestAttempt {
	explanation := r.routeDecision
	explanation.Phase = status
	// Keep the actual selection reason; the separate phase records attempt outcome.
	decision, _ := json.Marshal(explanation)
	return model.RelayRequestAttempt{
		RequestID:      r.ID,
		Round:          r.Round,
		GroupItemID:    itemID,
		ChannelID:      channelID,
		ChannelCode:    channelCode,
		TargetModel:    targetModel,
		TargetProtocol: int(protocol),
		StartedAt:      startedAt,
		DurationMs:     time.Since(startedAt).Milliseconds(),
		Status:         status,
		Error:          errText,
		Decision:       string(decision),
	}
}

// routeDecisionReason 提供稳定的机器可读路由事件原因。
func routeDecisionReason(status string) string {
	switch status {
	case "limited":
		return "upstream_limit"
	case "success":
		return "upstream_success"
	case "timeout":
		return "upstream_timeout"
	case "failed":
		return "upstream_failure"
	case "canceled":
		return "request_canceled"
	default:
		return "route_selected"
	}
}

// persistAttempt 落库一次上游尝试; 写失败只告警。
func persistAttempt(row model.RelayRequestAttempt) {
	if err := op.RelayAttemptSave(row); err != nil {
		log.Warnf("relay attempt persist failed: %v", err)
	}
}

// rowToState 把持久化行还原为状态流同形状的请求状态, 供日志分页接口使用。
func rowToState(row model.RelayRequest) RequestState {
	var events []RouteEvent
	_ = json.Unmarshal([]byte(row.RouteEvents), &events)
	usage := llm.Usage{
		PromptTokens:     row.PromptTokens,
		CompletionTokens: row.CompletionTokens,
		TotalTokens:      row.PromptTokens + row.CompletionTokens,
	}
	if row.CachedTokens > 0 || row.CacheWriteTokens > 0 {
		usage.PromptTokensDetails = &llm.PromptTokensDetails{
			CachedTokens:      row.CachedTokens,
			WriteCachedTokens: row.CacheWriteTokens,
		}
	}
	return RequestState{
		RouteEvents:        events,
		ID:                 row.ID,
		ClientIP:           row.ClientIP,
		Status:             Status(row.Status),
		StartedAt:          row.StartedAt,
		Duration:           time.Duration(row.DurationNs),
		FirstTokenDuration: time.Duration(row.FirstTokenDurationNs),
		StreamDuration:     time.Duration(row.StreamDurationNs),
		ResponseDuration:   time.Duration(row.ResponseDurationNs),
		UserID:             row.UserID,
		Model:              row.Model,
		ReasoningEffort:    row.ReasoningEffort,
		Protocol:           model.Protocol(row.Protocol),
		GroupID:            row.GroupID,
		APIKeyName:         row.APIKeyName,
		Usage:              usage,
		Cost:               row.Cost,
		OutputChars:        row.OutputChars,
		Round:              row.Round,
		TargetChannelKey:   row.TargetChannelKey,
		TargetChannelCode:  row.TargetChannelCode,
		TargetModel:        row.TargetModel,
		TargetProtocol:     model.Protocol(row.TargetProtocol),
		Error:              row.Error,
	}
}

// 路由状态与指标的脏集, 由后台定时批量落库, 避免选路热路径同步写库。
var (
	persistDirtyMu sync.Mutex
	routeDirty     = make(map[int]bool)
	metricDirty    = make(map[memberMetricKey]bool)
	persistOnce    sync.Once
)

// markRouteDirty 标记分组路由状态待落库, 并确保后台刷盘协程已启动。
func markRouteDirty(groupID int) {
	persistDirtyMu.Lock()
	routeDirty[groupID] = true
	persistDirtyMu.Unlock()
	startPersistFlusher()
}

// markMetricDirty 标记成员指标待落库。
func markMetricDirty(groupID, itemID int) {
	persistDirtyMu.Lock()
	metricDirty[memberMetricKey{groupID: groupID, itemID: itemID}] = true
	persistDirtyMu.Unlock()
	startPersistFlusher()
}

func startPersistFlusher() {
	persistOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				FlushRoutePersist()
			}
		}()
	})
}

// FlushRoutePersist 将脏的路由状态、冷却与成员指标快照落库; 退出或测试时可显式调用。
func FlushRoutePersist() {
	persistDirtyMu.Lock()
	groups := make([]int, 0, len(routeDirty))
	for id := range routeDirty {
		groups = append(groups, id)
	}
	for key := range metricDirty {
		if !routeDirty[key.groupID] {
			groups = append(groups, key.groupID)
			routeDirty[key.groupID] = true
		}
	}
	routeDirty = make(map[int]bool)
	metricDirty = make(map[memberMetricKey]bool)
	persistDirtyMu.Unlock()

	for _, groupID := range groups {
		routeMu.Lock()
		route := routes[groupID]
		var state model.RouteStateRecord
		var cooldowns []model.RouteCooldownRecord
		if route != nil {
			state = model.RouteStateRecord{
				GroupID:       groupID,
				CurrentItemID: route.CurrentItemID,
				ProbeItemID:   route.ProbeItemID,
				AffinityUntil: route.AffinityUntil,
				AffinityArmed: route.affinityArmed,
			}
			for itemID, until := range route.Cooldowns {
				cooldowns = append(cooldowns, model.RouteCooldownRecord{GroupID: groupID, ItemID: itemID, CooldownUntil: until})
			}
		}
		routeMu.Unlock()

		metricMu.Lock()
		var metrics []model.RouteMetricRecord
		for key, metric := range memberMetrics {
			if key.groupID != groupID {
				continue
			}
			metrics = append(metrics, model.RouteMetricRecord{
				GroupID:    groupID,
				ItemID:     key.itemID,
				EmaWaitMs:  metric.emaWaitMs,
				EmaSuccess: metric.emaSuccess,
				Samples:    metric.samples,
			})
		}
		metricMu.Unlock()

		if err := op.RouteSnapshotSave(state, cooldowns, metrics); err != nil {
			log.Warnf("route snapshot persist failed for group %d: %v", groupID, err)
		}
	}
}

// RestorePersist 启动时恢复路由状态与成员指标, 并把上次进程遗留的未完成请求标记为中断。
// 探测占用不恢复: 探测对应的是已随进程结束的在途请求, 重启后清零由新请求重新探测。
func RestorePersist() error {
	if err := op.RelayInterruptStale(); err != nil {
		log.Warnf("interrupt stale relay requests failed: %v", err)
	}
	states, cooldowns, metrics, err := op.RouteSnapshotLoadAll()
	if err != nil {
		return err
	}
	routeMu.Lock()
	for _, state := range states {
		cooldownMap := make(map[int]int64)
		for _, cooldown := range cooldowns {
			if cooldown.GroupID == state.GroupID && cooldown.CooldownUntil > time.Now().UnixMilli() {
				cooldownMap[cooldown.ItemID] = cooldown.CooldownUntil
			}
		}
		routes[state.GroupID] = &RouteState{
			GroupID:       state.GroupID,
			CurrentItemID: state.CurrentItemID,
			AffinityUntil: state.AffinityUntil,
			Cooldowns:     cooldownMap,
			affinityArmed: state.AffinityArmed,
		}
	}
	routeMu.Unlock()
	metricMu.Lock()
	for _, metric := range metrics {
		if metric.Samples <= 0 {
			continue
		}
		memberMetrics[memberMetricKey{groupID: metric.GroupID, itemID: metric.ItemID}] = &memberMetric{
			emaWaitMs:  metric.EmaWaitMs,
			emaSuccess: metric.EmaSuccess,
			samples:    metric.Samples,
		}
	}
	metricMu.Unlock()
	log.Infof("route state restored: %d groups, %d metrics", len(states), len(metrics))
	return nil
}

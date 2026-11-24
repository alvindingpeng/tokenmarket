package op

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// AutoRoutingTaskSummary 汇总一个任务分类在观测窗口内的请求结果。
type AutoRoutingTaskSummary struct {
	Task          string  `json:"task"`
	Requests      int64   `json:"requests"`
	Success       int64   `json:"success"`
	Failed        int64   `json:"failed"`
	SuccessRate   float64 `json:"success_rate"`
	AvgDurationMs float64 `json:"avg_duration_ms"`
}

// AutoRoutingModelSummary 按任务与实际落地模型汇总成功率和延迟。
type AutoRoutingModelSummary struct {
	Task          string  `json:"task"`
	Model         string  `json:"model"`
	Attempts      int64   `json:"attempts"`
	Success       int64   `json:"success"`
	SuccessRate   float64 `json:"success_rate"`
	AvgDurationMs float64 `json:"avg_duration_ms"`
	AvgCostUSD    float64 `json:"avg_cost_usd"`
}

// AutoRoutingReasonSummary 是排除原因、运行事件或自动路由决策的计数。
type AutoRoutingReasonSummary struct {
	Reason string `json:"reason"`
	Count  int64  `json:"count"`
}

// AutoRoutingPredictionSummary 对照影子预测和实际落地结果。只在两边都记录了模型时
// 才计为可比较样本，避免把没有预测或尚未成功的请求算成误判。
type AutoRoutingPredictionSummary struct {
	Task             string  `json:"task"`
	Compared         int64   `json:"compared"`
	SameModel        int64   `json:"same_model"`
	SameModelRate    float64 `json:"same_model_rate"`
	PredictedCostUSD float64 `json:"predicted_cost_usd"`
	ActualCostUSD    float64 `json:"actual_cost_usd"`
	CostDeltaUSD     float64 `json:"cost_delta_usd"`
}

// AutoRoutingReadiness 是任务级灰度晋级建议所需的证据汇总。它只读取调用日志和
// 分组配置，不会自动改写 active 模式或流量比例。真实样本不足、候选未验证或预测
// 一致率低时都会返回保守建议。
type AutoRoutingReadiness struct {
	Task                   string   `json:"task"`
	State                  string   `json:"state"`
	RecommendedRampPercent int      `json:"recommended_ramp_percent"`
	ConfidenceLevelPercent int      `json:"confidence_level_percent"`
	Classified             int64    `json:"classified"`
	ShadowRequests         int64    `json:"shadow_requests"`
	ShadowCompared         int64    `json:"shadow_compared"`
	ShadowCoverage         float64  `json:"shadow_coverage"`
	PredictionAgreement    float64  `json:"prediction_agreement"`
	PredictionAgreementLow float64  `json:"prediction_agreement_lower"`
	ActiveRequests         int64    `json:"active_requests"`
	ActiveSuccessRate      float64  `json:"active_success_rate"`
	ActiveSuccessLow       float64  `json:"active_success_lower"`
	PredictedCostUSD       float64  `json:"predicted_cost_usd"`
	ActualCostUSD          float64  `json:"actual_cost_usd"`
	CostDeltaPercent       float64  `json:"cost_delta_percent"`
	CostRatioP90Percent    float64  `json:"cost_ratio_p90_percent"`
	CostSamples            int      `json:"cost_samples"`
	ConfiguredCandidates   int      `json:"configured_candidates"`
	VerifiedCandidates     int      `json:"verified_candidates"`
	Reasons                []string `json:"reasons"`
}

// AutoRoutingSummary 是近期调用日志的自动路由实验聚合。数据来自现有请求和尝试日志，
// 不新增高频写入的独立报表表，也不需要迁移数据库。
type AutoRoutingSummary struct {
	Days           int                            `json:"days"`
	GroupID        int                            `json:"group_id"`
	GeneratedAt    time.Time                      `json:"generated_at"`
	WindowStart    time.Time                      `json:"window_start"`
	Requests       int64                          `json:"requests"`
	TaskClassified int64                          `json:"task_classified"`
	Success        int64                          `json:"success"`
	Failed         int64                          `json:"failed"`
	SuccessRate    float64                        `json:"success_rate"`
	Tasks          []AutoRoutingTaskSummary       `json:"tasks"`
	Models         []AutoRoutingModelSummary      `json:"models"`
	Exclusions     []AutoRoutingReasonSummary     `json:"exclusions"`
	Events         []AutoRoutingReasonSummary     `json:"events"`
	Predictions    []AutoRoutingPredictionSummary `json:"predictions"`
	Readiness      []AutoRoutingReadiness         `json:"readiness"`
	SampleLimit    int                            `json:"sample_limit"`
}

type autoRequestSample struct {
	RequestID   uint64
	Status      string
	DurationMs  int64
	Cost        float64
	TargetModel string
	RouteEvents string
	Model       string
	GroupID     int
	AutoTask    string
	AutoMode    string
}

type autoPredictionEvidence struct {
	Task    string
	Model   string
	CostUSD float64
}

func autoTaskFromDecision(reason string) string {
	switch reason {
	case "auto_general":
		return "general"
	case "auto_summary":
		return "summary"
	case "auto_coding":
		return "coding"
	case "auto_tool":
		return "tool"
	case "auto_extraction":
		return "extraction"
	default:
		return ""
	}
}

func autoTaskFromEvents(raw string) string {
	var events []struct {
		Phase  string `json:"phase"`
		Reason string `json:"reason"`
	}
	if json.Unmarshal([]byte(raw), &events) != nil {
		return ""
	}
	for _, event := range events {
		if event.Phase != "auto_shadow" && event.Phase != "auto_routing" {
			continue
		}
		task := strings.SplitN(event.Reason, ":", 2)[0]
		switch task {
		case "general", "summary", "coding", "tool", "extraction":
			return task
		default:
			return "unsupported"
		}
	}
	return ""
}

func autoGrantSet(raw string) map[int]bool {
	grants := make(map[int]bool)
	for _, part := range strings.Split(raw, ",") {
		id := 0
		for _, ch := range strings.TrimSpace(part) {
			if ch < '0' || ch > '9' {
				id = 0
				break
			}
			id = id*10 + int(ch-'0')
		}
		if id > 0 {
			grants[id] = true
		}
	}
	return grants
}

func autoTaskGrantConfig(config model.GroupRelayConfig, task string) string {
	switch task {
	case model.AutoRoutingTaskSummary:
		if config.AutoSummary != "" {
			return config.AutoSummary
		}
		return config.AutoGeneral
	case model.AutoRoutingTaskCoding:
		return config.AutoCoding
	case model.AutoRoutingTaskTool:
		return config.AutoTool
	case model.AutoRoutingTaskExtraction:
		return config.AutoExtract
	default:
		return config.AutoGeneral
	}
}

type autoReadinessEvidence struct {
	classified, shadowRequests, activeRequests, activeSuccess int64
	compared, same, predictedCost, actualCost                 float64
	costRatios                                                []float64
	configuredCandidates, verifiedCandidates                  int
	labelingEnabled                                           bool
}

const autoReadinessConfidenceZ = 1.96

// autoWilsonLower 返回二项成功率的 Wilson 双侧 95% 置信区间下界。小样本即使
// 暂时全部通过，下界仍会明显低于原始比例，避免凭几十次成功就快速扩大灰度。
func autoWilsonLower(success, total int64) float64 {
	if total <= 0 {
		return 0
	}
	n := float64(total)
	p := float64(success) / n
	z2 := autoReadinessConfidenceZ * autoReadinessConfidenceZ
	center := p + z2/(2*n)
	margin := autoReadinessConfidenceZ * math.Sqrt(p*(1-p)/n+z2/(4*n*n))
	return max(0, (center-margin)/(1+z2/n))
}

// autoPercentile 使用线性插值计算样本分位数。成本采用 P90，既保留异常放大的
// 风险信号，又不会被单次结算噪声直接决定灰度。
func autoPercentile(values []float64, percentile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	position := clampFloat(percentile, 0, 1) * float64(len(sorted)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return sorted[lower]
	}
	weight := position - float64(lower)
	return sorted[lower]*(1-weight) + sorted[upper]*weight
}

func clampFloat(value, minimum, maximum float64) float64 {
	return min(max(value, minimum), maximum)
}

func autoReadinessFor(task string, evidence autoReadinessEvidence) AutoRoutingReadiness {
	readiness := AutoRoutingReadiness{
		Task: task, ConfidenceLevelPercent: 95,
		Classified: evidence.classified, ShadowRequests: evidence.shadowRequests,
		ShadowCompared: int64(evidence.compared), ActiveRequests: evidence.activeRequests,
		PredictionAgreementLow: autoWilsonLower(int64(evidence.same), int64(evidence.compared)),
		ActiveSuccessLow:       autoWilsonLower(evidence.activeSuccess, evidence.activeRequests),
		PredictedCostUSD:       evidence.predictedCost, ActualCostUSD: evidence.actualCost,
		CostSamples:          len(evidence.costRatios),
		ConfiguredCandidates: evidence.configuredCandidates, VerifiedCandidates: evidence.verifiedCandidates,
		Reasons: make([]string, 0),
	}
	if evidence.shadowRequests > 0 {
		readiness.ShadowCoverage = evidence.compared / float64(evidence.shadowRequests)
	}
	if evidence.compared > 0 {
		readiness.PredictionAgreement = evidence.same / evidence.compared
	}
	if evidence.activeRequests > 0 {
		readiness.ActiveSuccessRate = float64(evidence.activeSuccess) / float64(evidence.activeRequests)
	}
	if evidence.predictedCost > 0 {
		readiness.CostDeltaPercent = (evidence.actualCost/evidence.predictedCost - 1) * 100
	}
	if len(evidence.costRatios) > 0 {
		readiness.CostRatioP90Percent = (autoPercentile(evidence.costRatios, 0.9) - 1) * 100
	}
	if readiness.CostRatioP90Percent > 25 {
		readiness.Reasons = append(readiness.Reasons, "actual_cost_above_prediction")
	}

	switch {
	case !evidence.labelingEnabled:
		readiness.State = "labeling_disabled"
		readiness.Reasons = append(readiness.Reasons, "task_candidate_selection_not_enabled")
	case evidence.classified < 20 || evidence.shadowRequests < 20:
		readiness.State = "collect_shadow"
		readiness.Reasons = append(readiness.Reasons, "need_more_classified_samples")
	case readiness.ShadowCoverage < 0.8:
		readiness.State = "improve_shadow_coverage"
		readiness.Reasons = append(readiness.Reasons, "shadow_coverage_below_80_percent")
	case readiness.PredictionAgreement < 0.65:
		readiness.State = "investigate_prediction"
		readiness.Reasons = append(readiness.Reasons, "prediction_agreement_below_65_percent")
	case readiness.PredictionAgreementLow < 0.60:
		readiness.State = "collect_shadow"
		readiness.Reasons = append(readiness.Reasons, "prediction_confidence_below_60_percent")
	case evidence.configuredCandidates > 0 && evidence.verifiedCandidates == 0:
		readiness.State = "prepare_candidates"
		readiness.Reasons = append(readiness.Reasons, "no_verified_task_candidates")
	case task == model.AutoRoutingTaskTool && evidence.verifiedCandidates == 0:
		readiness.State = "prepare_candidates"
		readiness.Reasons = append(readiness.Reasons, "tool_requires_explicit_verified_candidate")
	case evidence.activeRequests == 0:
		if evidence.verifiedCandidates == 0 {
			readiness.State = "prepare_candidates"
			readiness.Reasons = append(readiness.Reasons, "no_verified_task_candidates")
			break
		}
		readiness.State = "ready_for_active"
		readiness.RecommendedRampPercent = 5
		readiness.Reasons = append(readiness.Reasons, "shadow_evidence_passed_start_at_5_percent")
	case readiness.ActiveSuccessRate < 0.95:
		readiness.State = "pause_active"
		readiness.Reasons = append(readiness.Reasons, "active_success_rate_below_95_percent")
	case evidence.activeRequests < 20:
		readiness.State = "hold_active"
		readiness.RecommendedRampPercent = 5
		readiness.Reasons = append(readiness.Reasons, "need_more_active_samples")
	case readiness.ActiveSuccessLow < 0.90:
		readiness.State = "hold_active"
		readiness.RecommendedRampPercent = 5
		readiness.Reasons = append(readiness.Reasons, "active_confidence_below_90_percent")
	case len(evidence.costRatios) > 0 && readiness.CostRatioP90Percent > 50:
		readiness.State = "hold_active"
		readiness.RecommendedRampPercent = 5
		readiness.Reasons = append(readiness.Reasons, "cost_p90_above_50_percent")
	case readiness.ActiveSuccessRate >= 0.99 && readiness.PredictionAgreement >= 0.75:
		if readiness.PredictionAgreementLow < 0.65 {
			readiness.State = "hold_active"
			readiness.RecommendedRampPercent = 5
			readiness.Reasons = append(readiness.Reasons, "prediction_confidence_below_65_percent")
			break
		}
		if len(evidence.costRatios) == 0 {
			readiness.State = "hold_active"
			readiness.RecommendedRampPercent = 5
			readiness.Reasons = append(readiness.Reasons, "need_cost_comparison_samples")
			break
		}
		if !autoReadinessCostWithin(evidence, 25) {
			readiness.State = "hold_active"
			readiness.RecommendedRampPercent = 5
			readiness.Reasons = append(readiness.Reasons, "cost_p90_above_25_percent")
			break
		}
		readiness.State = "scale_active"
		readiness.RecommendedRampPercent = 10
		readiness.Reasons = append(readiness.Reasons, "active_evidence_passed_scale_to_10_percent")
	default:
		readiness.State = "hold_active"
		readiness.RecommendedRampPercent = 5
		readiness.Reasons = append(readiness.Reasons, "active_evidence_needs_more_observation")
	}
	return readiness
}

func autoReadinessCostWithin(evidence autoReadinessEvidence, percent float64) bool {
	if len(evidence.costRatios) == 0 {
		return true
	}
	return (autoPercentile(evidence.costRatios, 0.9)-1)*100 <= percent
}

func applyReadinessCandidateConfig(
	scope model.Scope,
	groupID int,
	evidence map[string]*autoReadinessEvidence,
) {
	if groupID <= 0 {
		return
	}
	group, err := GroupGet(groupID, scope)
	if err != nil {
		return
	}
	verified := autoGrantSet(group.RelayConfig.AutoToolVerified)
	for _, task := range model.AutoRoutingTasks {
		actual := evidence[task]
		if actual == nil {
			actual = &autoReadinessEvidence{}
			evidence[task] = actual
		}
		grants := autoGrantSet(autoTaskGrantConfig(group.RelayConfig, task))
		actual.labelingEnabled = len(grants) > 0
		for _, item := range group.Items {
			if !grants[item.ChannelGrantID] {
				continue
			}
			actual.configuredCandidates++
			if task == model.AutoRoutingTaskTool {
				if verified[item.ChannelGrantID] {
					actual.verifiedCandidates++
				}
				continue
			}
			if capability, ok := TaskCapabilityGet(item.ModelName, task); ok && capability.Verified {
				actual.verifiedCandidates++
			}
		}
	}
}

func readinessByConfig(scope model.Scope, groupID int) []AutoRoutingReadiness {
	if groupID <= 0 {
		return nil
	}
	evidence := make(map[string]*autoReadinessEvidence)
	applyReadinessCandidateConfig(scope, groupID, evidence)
	result := make([]AutoRoutingReadiness, 0, len(model.AutoRoutingTasks))
	for _, task := range model.AutoRoutingTasks {
		item := evidence[task]
		if item == nil {
			item = &autoReadinessEvidence{}
		}
		result = append(result, autoReadinessFor(task, *item))
	}
	return result
}

// AutoRoutingAggregate 聚合指定分组在最近 days 天内的自动路由效果，scope 与调用日志的
// 可见范围一致，普通用户只能看到自己的请求。
func AutoRoutingAggregate(scope model.Scope, groupID, days int) (AutoRoutingSummary, error) {
	if days != 1 && days != 7 && days != 30 {
		days = 7
	}
	if groupID < 0 {
		return AutoRoutingSummary{}, fmt.Errorf("invalid group id")
	}
	now := time.Now()
	summary := AutoRoutingSummary{
		Days: days, GroupID: groupID, GeneratedAt: now,
		WindowStart: now.AddDate(0, 0, -days),
		SampleLimit: 5000,
		Tasks:       make([]AutoRoutingTaskSummary, 0), Models: make([]AutoRoutingModelSummary, 0),
		Exclusions: make([]AutoRoutingReasonSummary, 0), Events: make([]AutoRoutingReasonSummary, 0),
		Predictions: make([]AutoRoutingPredictionSummary, 0), Readiness: make([]AutoRoutingReadiness, 0),
	}
	query := db.GetDB().Model(&model.RelayRequest{}).
		Where("started_at >= ?", summary.WindowStart)
	if !scope.IsAdmin() {
		query = query.Where("user_id = ?", scope.ID)
	}
	if groupID > 0 {
		query = query.Where("group_id = ?", groupID)
	}
	var requests []autoRequestSample
	// 日志留存由系统生命周期管理；上限只约束交互式看板查询，避免一次把多年历史拉进内存。
	if err := query.Select("id AS request_id, status, duration_ns / 1000000 AS duration_ms, cost, target_model, route_events, model, group_id, auto_task, auto_mode").
		Order("id DESC").Limit(summary.SampleLimit).Scan(&requests).Error; err != nil {
		return summary, fmt.Errorf("failed to aggregate relay requests: %w", err)
	}
	if len(requests) == 0 {
		summary.Readiness = readinessByConfig(scope, groupID)
		return summary, nil
	}

	ids := make([]uint64, 0, len(requests))
	requestByID := make(map[uint64]autoRequestSample, len(requests))
	for _, request := range requests {
		ids = append(ids, request.RequestID)
		requestByID[request.RequestID] = request
		summary.Requests++
		if request.Status == "success" {
			summary.Success++
		} else if request.Status == "failed" {
			summary.Failed++
		}
	}
	var attempts []model.RelayRequestAttempt
	if err := db.GetDB().Where("request_id IN ?", ids).Order("request_id ASC, round ASC").Find(&attempts).Error; err != nil {
		return summary, fmt.Errorf("failed to aggregate relay attempts: %w", err)
	}

	type taskCount struct {
		requests, success, failed int64
		duration                  int64
	}
	tasks := make(map[string]*taskCount)
	modelKey := func(task, name string) string { return task + "\x00" + name }
	type modelCount struct {
		attempts, success int64
		duration          int64
		cost              float64
	}
	models := make(map[string]*modelCount)
	exclusions := make(map[string]int64)
	events := make(map[string]int64)
	type predictionCount struct {
		compared, same            int64
		predictedCost, actualCost float64
	}
	predictions := make(map[string]*predictionCount)
	shadowPredictions := make(map[uint64]autoPredictionEvidence)
	requestTasks := make(map[uint64]string)
	readinessByTask := make(map[string]*autoReadinessEvidence)
	applyReadinessCandidateConfig(scope, groupID, readinessByTask)
	activeReadinessRequests := make(map[uint64]bool)
	for _, request := range requests {
		task := request.AutoTask
		if task == "" {
			task = autoTaskFromEvents(request.RouteEvents)
		}
		if task == "" {
			continue
		}
		requestTasks[request.RequestID] = task
		tc := tasks[task]
		if tc == nil {
			tc = &taskCount{}
			tasks[task] = tc
		}
		readiness := readinessByTask[task]
		if readiness == nil {
			readiness = &autoReadinessEvidence{}
			readinessByTask[task] = readiness
		}
		readiness.classified++
		switch request.AutoMode {
		case "shadow":
			readiness.shadowRequests++
		}
		tc.requests++
		tc.duration += request.DurationMs
		if request.Status == "success" {
			tc.success++
		} else if request.Status == "failed" {
			tc.failed++
		}
		var routeEvents []struct {
			Phase  string `json:"phase"`
			Reason string `json:"reason"`
		}
		if json.Unmarshal([]byte(request.RouteEvents), &routeEvents) == nil {
			for _, event := range routeEvents {
				if event.Phase != "auto_shadow" && event.Phase != "auto_routing" {
					continue
				}
				if event.Phase == "auto_routing" && event.Reason == "auto_circuit_open" {
					events["circuit_open"]++
				} else if strings.Contains(event.Reason, ":no_candidate") {
					events["no_candidate"]++
				} else if strings.Contains(event.Reason, ":item=") || strings.Contains(event.Reason, ":model=") {
					events["predicted"]++
				} else {
					events[event.Reason]++
				}
			}
		}
	}
	for _, attempt := range attempts {
		var decision model.RelayRouteDecision
		if json.Unmarshal([]byte(attempt.Decision), &decision) != nil {
			continue
		}
		request := requestByID[attempt.RequestID]
		task := autoTaskFromDecision(decision.Reason)
		if task == "" {
			task = requestTasks[attempt.RequestID]
		}
		if task == "" {
			continue
		}
		name := attempt.TargetModel
		if name == "" {
			name = request.TargetModel
		}
		if name == "" {
			continue
		}
		key := modelKey(task, name)
		mc := models[key]
		if mc == nil {
			mc = &modelCount{}
			models[key] = mc
		}
		mc.attempts++
		mc.duration += attempt.DurationMs
		if attempt.Status == "success" {
			mc.success++
			// Request-level cost is booked once for the final successful route.
			// Counting it for every try of the same successful request would inflate
			// the model's average cost when a retry preceded the successful attempt.
			if request.Status == "success" && request.TargetModel == name {
				mc.cost += request.Cost
			}
		}
		for _, candidate := range decision.Candidates {
			if strings.HasPrefix(candidate.Reason, "auto_") {
				exclusions[candidate.Reason]++
			}
		}
		var evidence struct {
			Prediction struct {
				Model   string  `json:"model"`
				CostUSD float64 `json:"cost_usd"`
			} `json:"prediction"`
			ActualModel string `json:"actual_model"`
			RouteAction string `json:"route_action"`
			Sticky      *struct {
				ItemID  int    `json:"item_id"`
				Model   string `json:"model"`
				Outcome string `json:"outcome"`
			} `json:"sticky"`
		}
		if json.Unmarshal([]byte(decision.Details), &evidence) == nil {
			switch evidence.RouteAction {
			case "active":
				events["active_selected"]++
			case "fallback":
				events["fallback_selected"]++
			case "reject":
				events["rejected"]++
			case "explore":
				events["explored"]++
			}
			if (evidence.RouteAction == "active" || evidence.RouteAction == "explore") &&
				!activeReadinessRequests[attempt.RequestID] {
				activeReadinessRequests[attempt.RequestID] = true
				readiness := readinessByTask[task]
				if readiness != nil {
					readiness.activeRequests++
					if request.Status == "success" {
						readiness.activeSuccess++
					}
				}
			}
			if evidence.Sticky != nil {
				events["sticky_"+evidence.Sticky.Outcome]++
			}
		}
		if request.AutoMode == "shadow" && json.Unmarshal([]byte(decision.Details), &evidence) == nil &&
			evidence.Prediction.Model != "" {
			shadowPredictions[attempt.RequestID] = autoPredictionEvidence{Task: task, Model: evidence.Prediction.Model, CostUSD: evidence.Prediction.CostUSD}
		}
	}
	for requestID, predicted := range shadowPredictions {
		request := requestByID[requestID]
		actual := request.TargetModel
		if actual == "" || request.Status != "success" {
			continue
		}
		pc := predictions[predicted.Task]
		if pc == nil {
			pc = &predictionCount{}
			predictions[predicted.Task] = pc
		}
		pc.compared++
		if actual == predicted.Model {
			pc.same++
		}
		pc.predictedCost += predicted.CostUSD
		pc.actualCost += request.Cost
		readiness := readinessByTask[predicted.Task]
		if readiness != nil {
			readiness.compared++
			readiness.predictedCost += predicted.CostUSD
			readiness.actualCost += request.Cost
			if predicted.CostUSD > 0 {
				readiness.costRatios = append(readiness.costRatios, request.Cost/predicted.CostUSD)
			}
			if actual == predicted.Model {
				readiness.same++
			}
		}
	}
	for _, task := range tasks {
		summary.TaskClassified += task.requests
	}
	if completed := summary.Success + summary.Failed; completed > 0 {
		summary.SuccessRate = float64(summary.Success) / float64(completed)
	}
	for task, count := range tasks {
		item := AutoRoutingTaskSummary{
			Task: task, Requests: count.requests, Success: count.success, Failed: count.failed,
		}
		if count.requests > 0 {
			item.AvgDurationMs = float64(count.duration) / float64(count.requests)
		}
		if completed := count.success + count.failed; completed > 0 {
			item.SuccessRate = float64(count.success) / float64(completed)
		}
		summary.Tasks = append(summary.Tasks, item)
	}
	for key, count := range models {
		parts := strings.SplitN(key, "\x00", 2)
		item := AutoRoutingModelSummary{Task: parts[0], Model: parts[1], Attempts: count.attempts, Success: count.success}
		if count.attempts > 0 {
			item.AvgDurationMs = float64(count.duration) / float64(count.attempts)
		}
		item.SuccessRate = float64(count.success) / float64(count.attempts)
		if count.success > 0 {
			item.AvgCostUSD = count.cost / float64(count.success)
		}
		summary.Models = append(summary.Models, item)
	}
	for reason, count := range exclusions {
		summary.Exclusions = append(summary.Exclusions, AutoRoutingReasonSummary{Reason: reason, Count: count})
	}
	for reason, count := range events {
		summary.Events = append(summary.Events, AutoRoutingReasonSummary{Reason: reason, Count: count})
	}
	for task, evidence := range readinessByTask {
		if task == "unsupported" {
			continue
		}
		summary.Readiness = append(summary.Readiness, autoReadinessFor(task, *evidence))
	}
	for task, count := range predictions {
		item := AutoRoutingPredictionSummary{Task: task, Compared: count.compared, SameModel: count.same,
			PredictedCostUSD: count.predictedCost, ActualCostUSD: count.actualCost,
			CostDeltaUSD: count.actualCost - count.predictedCost}
		if count.compared > 0 {
			item.SameModelRate = float64(count.same) / float64(count.compared)
		}
		summary.Predictions = append(summary.Predictions, item)
	}
	sort.Slice(summary.Tasks, func(i, j int) bool { return summary.Tasks[i].Requests > summary.Tasks[j].Requests })
	sort.Slice(summary.Models, func(i, j int) bool {
		if summary.Models[i].Task != summary.Models[j].Task {
			return summary.Models[i].Task < summary.Models[j].Task
		}
		return summary.Models[i].Attempts > summary.Models[j].Attempts
	})
	sort.Slice(summary.Exclusions, func(i, j int) bool { return summary.Exclusions[i].Count > summary.Exclusions[j].Count })
	sort.Slice(summary.Events, func(i, j int) bool { return summary.Events[i].Count > summary.Events[j].Count })
	sort.Slice(summary.Predictions, func(i, j int) bool {
		return summary.Predictions[i].Compared > summary.Predictions[j].Compared
	})
	sort.Slice(summary.Readiness, func(i, j int) bool {
		return summary.Readiness[i].Classified > summary.Readiness[j].Classified
	})
	return summary, nil
}

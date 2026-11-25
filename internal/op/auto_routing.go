package op

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// AutoRoutingTaskSummary 汇总一个任务分类在观测窗口内的请求结果。
type AutoRoutingTaskSummary struct {
	Task                     string  `json:"task"`
	Requests                 int64   `json:"requests"`
	Success                  int64   `json:"success"`
	Failed                   int64   `json:"failed"`
	SuccessRate              float64 `json:"success_rate"`
	AvgDurationMs            float64 `json:"avg_duration_ms"`
	P90DurationMs            float64 `json:"p90_duration_ms"`
	FirstTokenSamples        int64   `json:"first_token_samples"`
	AvgFirstTokenMs          float64 `json:"avg_first_token_ms"`
	P90FirstTokenMs          float64 `json:"p90_first_token_ms"`
	StreamStarted            int64   `json:"stream_started"`
	StreamFailedAfterFirst   int64   `json:"stream_failed_after_first"`
	StreamCanceledAfterFirst int64   `json:"stream_canceled_after_first"`
	StreamIncompleteRate     float64 `json:"stream_incomplete_rate"`
}

// AutoRoutingModelSummary 按任务与实际落地模型汇总成功率和延迟。
type AutoRoutingModelSummary struct {
	Task              string  `json:"task"`
	Model             string  `json:"model"`
	Attempts          int64   `json:"attempts"`
	Success           int64   `json:"success"`
	SuccessRate       float64 `json:"success_rate"`
	AvgDurationMs     float64 `json:"avg_duration_ms"`
	P90DurationMs     float64 `json:"p90_duration_ms"`
	FirstTokenSamples int64   `json:"first_token_samples"`
	AvgFirstTokenMs   float64 `json:"avg_first_token_ms"`
	P90FirstTokenMs   float64 `json:"p90_first_token_ms"`
	AvgCostUSD        float64 `json:"avg_cost_usd"`
}

// AutoRoutingErrorSummary 按任务和尝试错误类别汇总失败次数。
type AutoRoutingErrorSummary struct {
	Task  string `json:"task"`
	Class string `json:"class"`
	Count int64  `json:"count"`
}

// AutoRoutingCostBandSummary 展示不同输入长度档位的用户实付成本。
type AutoRoutingCostBandSummary struct {
	Task            string  `json:"task"`
	Band            string  `json:"band"`
	Requests        int64   `json:"requests"`
	Success         int64   `json:"success"`
	AvgPromptTokens float64 `json:"avg_prompt_tokens"`
	AvgCostUSD      float64 `json:"avg_cost_usd"`
	P90CostUSD      float64 `json:"p90_cost_usd"`
}

// AutoRoutingContextWindowHint 是上游上下文超限后对候选窗口的可执行复核建议。
// PromptTokens 是失败尝试实际请求的输入长度，而不是目录预埋值；Samples 与
// ObservedMinInputTokens / ObservedMaxInputTokens 只统计明确分类为 context 的失败，
// 不把普通 400 混入。SuggestedContextLength 以最小失败输入为紧上界保留 25% 安全余量。
type AutoRoutingContextWindowHint struct {
	GroupID                       int    `json:"group_id"`
	GroupItemID                   int    `json:"group_item_id,omitempty"`
	Task                          string `json:"task"`
	Model                         string `json:"model"`
	ChannelCode                   string `json:"channel_code"`
	ChannelGrantID                int    `json:"channel_grant_id,omitempty"`
	Samples                       int64  `json:"samples"`
	EstimatedSamples              int64  `json:"estimated_samples,omitempty"`
	ObservedMinInputTokens        int64  `json:"observed_min_input_tokens"`
	ObservedMaxInputTokens        int64  `json:"observed_max_input_tokens"`
	ObservedMaxSuccessInputTokens int64  `json:"observed_max_success_input_tokens,omitempty"`
	ConfiguredContextLength       int64  `json:"configured_context_length,omitempty"`
	HasContextOverride            bool   `json:"has_context_override,omitempty"`
	SuggestedContextLength        int64  `json:"suggested_context_length"`
	ConfigStatus                  string `json:"config_status,omitempty"`
	Reason                        string `json:"reason"`
}

// AutoRoutingModelRisk 是主动路由真实落地模型的样本级风险卡。
// 样本不足时只展示观测值，不参与扩容阻断。
type AutoRoutingModelRisk struct {
	Task                     string                     `json:"task"`
	Model                    string                     `json:"model"`
	State                    string                     `json:"state"`
	ActiveRequests           int64                      `json:"active_requests"`
	Success                  int64                      `json:"success"`
	Failed                   int64                      `json:"failed"`
	SuccessRate              float64                    `json:"success_rate"`
	SuccessLow               float64                    `json:"success_lower"`
	ChannelFailures          int64                      `json:"channel_failures"`
	RequestFailures          int64                      `json:"request_failures"`
	CandidateFailures        int64                      `json:"candidate_failures"`
	OtherFailures            int64                      `json:"other_failures"`
	ErrorClassCounts         []AutoRoutingReasonSummary `json:"error_class_counts"`
	BandRisks                []AutoRoutingModelBandRisk `json:"band_risks"`
	EarlierSamples           int64                      `json:"earlier_samples"`
	EarlierSuccess           int64                      `json:"earlier_success"`
	EarlierSuccessRate       float64                    `json:"earlier_success_rate"`
	RecentSamples            int64                      `json:"recent_samples"`
	RecentSuccess            int64                      `json:"recent_success"`
	RecentSuccessRate        float64                    `json:"recent_success_rate"`
	SuccessTrend             string                     `json:"success_trend"`
	SuccessRateDelta         float64                    `json:"success_rate_delta"`
	FirstTokenSamples        int64                      `json:"first_token_samples"`
	FirstTokenP90Ms          float64                    `json:"first_token_p90_ms"`
	EarlierFirstTokenSamples int64                      `json:"earlier_first_token_samples"`
	EarlierFirstTokenP90Ms   float64                    `json:"earlier_first_token_p90_ms"`
	RecentFirstTokenSamples  int64                      `json:"recent_first_token_samples"`
	RecentFirstTokenP90Ms    float64                    `json:"recent_first_token_p90_ms"`
	FirstTokenTrend          string                     `json:"first_token_trend"`
	FirstTokenP90DeltaMs     float64                    `json:"first_token_p90_delta_ms"`
	StreamStarted            int64                      `json:"stream_started"`
	StreamIncomplete         int64                      `json:"stream_incomplete"`
	StreamIncompleteRate     float64                    `json:"stream_incomplete_rate"`
	EarlierStreamStarted     int64                      `json:"earlier_stream_started"`
	EarlierStreamIncomplete  int64                      `json:"earlier_stream_incomplete"`
	EarlierStreamRate        float64                    `json:"earlier_stream_incomplete_rate"`
	RecentStreamStarted      int64                      `json:"recent_stream_started"`
	RecentStreamIncomplete   int64                      `json:"recent_stream_incomplete"`
	RecentStreamRate         float64                    `json:"recent_stream_incomplete_rate"`
	StreamTrend              string                     `json:"stream_trend"`
	StreamRateDelta          float64                    `json:"stream_incomplete_rate_delta"`
	EligibleForPromotion     bool                       `json:"eligible_for_promotion"`
	Reasons                  []string                   `json:"reasons"`
}

// AutoRoutingModelBandRisk 是模型在输入长度档位内的风险证据。长上下文请求
// 可能先于短请求出现失败或延迟退化，分档统计避免总体平均掩盖这一差异。
type AutoRoutingModelBandRisk struct {
	Task                 string                     `json:"task"`
	Model                string                     `json:"model"`
	Band                 string                     `json:"band"`
	State                string                     `json:"state"`
	Samples              int64                      `json:"samples"`
	Success              int64                      `json:"success"`
	SuccessRate          float64                    `json:"success_rate"`
	SuccessLow           float64                    `json:"success_lower"`
	AvgPromptTokens      float64                    `json:"avg_prompt_tokens"`
	P90PromptTokens      float64                    `json:"p90_prompt_tokens"`
	FirstTokenSamples    int64                      `json:"first_token_samples"`
	FirstTokenP90Ms      float64                    `json:"first_token_p90_ms"`
	StreamStarted        int64                      `json:"stream_started"`
	StreamIncomplete     int64                      `json:"stream_incomplete"`
	StreamIncompleteRate float64                    `json:"stream_incomplete_rate"`
	ErrorClassCounts     []AutoRoutingReasonSummary `json:"error_class_counts"`
	EligibleForPromotion bool                       `json:"eligible_for_promotion"`
	Reasons              []string                   `json:"reasons"`
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
	Task                      string                 `json:"task"`
	State                     string                 `json:"state"`
	RecommendedRampPercent    int                    `json:"recommended_ramp_percent"`
	ConfidenceLevelPercent    int                    `json:"confidence_level_percent"`
	Classified                int64                  `json:"classified"`
	EvidenceUsers             int                    `json:"evidence_users"`
	EvidenceBuckets           int                    `json:"evidence_buckets"`
	RequiredBuckets           int                    `json:"required_buckets"`
	AdminMultiUserEvidence    bool                   `json:"admin_multi_user_evidence"`
	ShadowRequests            int64                  `json:"shadow_requests"`
	ShadowCompared            int64                  `json:"shadow_compared"`
	ShadowCoverage            float64                `json:"shadow_coverage"`
	PredictionAgreement       float64                `json:"prediction_agreement"`
	PredictionAgreementLow    float64                `json:"prediction_agreement_lower"`
	ActiveRequests            int64                  `json:"active_requests"`
	ActiveSuccessRate         float64                `json:"active_success_rate"`
	ActiveSuccessLow          float64                `json:"active_success_lower"`
	PredictedCostUSD          float64                `json:"predicted_cost_usd"`
	ActualCostUSD             float64                `json:"actual_cost_usd"`
	CostDeltaPercent          float64                `json:"cost_delta_percent"`
	CostRatioP90Percent       float64                `json:"cost_ratio_p90_percent"`
	CostSamples               int                    `json:"cost_samples"`
	ConfiguredCandidates      int                    `json:"configured_candidates"`
	VerifiedCandidates        int                    `json:"verified_candidates"`
	AnthropicStreamCandidates int                    `json:"anthropic_stream_candidates"`
	AnthropicStreamVerified   int                    `json:"anthropic_stream_verified"`
	ModelTeams                int                    `json:"model_teams"`
	ModelSamples              int                    `json:"model_samples"`
	ModelEligibleTeams        int                    `json:"model_eligible_teams"`
	ModelSuccessRate          float64                `json:"model_success_rate"`
	ModelTeamsLow             float64                `json:"model_teams_lower"`
	ModelMinSuccessRate       float64                `json:"model_min_success_rate"`
	ModelRisks                []AutoRoutingModelRisk `json:"model_risks"`
	FirstTokenSamples         int64                  `json:"first_token_samples"`
	FirstTokenP90Ms           float64                `json:"first_token_p90_ms"`
	StreamStarted             int64                  `json:"stream_started"`
	StreamIncomplete          int64                  `json:"stream_incomplete"`
	StreamIncompleteRate      float64                `json:"stream_incomplete_rate"`
	Reasons                   []string               `json:"reasons"`
}

// AutoRoutingSummary 是近期调用日志的自动路由实验聚合。数据来自现有请求和尝试日志，
// 不新增高频写入的独立报表表，也不需要迁移数据库。
type AutoRoutingSummary struct {
	ProbeCandidates    []AutoProbeCandidate           `json:"probe_candidates,omitempty"`
	Days               int                            `json:"days"`
	GroupID            int                            `json:"group_id"`
	GeneratedAt        time.Time                      `json:"generated_at"`
	WindowStart        time.Time                      `json:"window_start"`
	Requests           int64                          `json:"requests"`
	TaskClassified     int64                          `json:"task_classified"`
	Success            int64                          `json:"success"`
	Failed             int64                          `json:"failed"`
	SuccessRate        float64                        `json:"success_rate"`
	Tasks              []AutoRoutingTaskSummary       `json:"tasks"`
	Models             []AutoRoutingModelSummary      `json:"models"`
	Exclusions         []AutoRoutingReasonSummary     `json:"exclusions"`
	Events             []AutoRoutingReasonSummary     `json:"events"`
	Predictions        []AutoRoutingPredictionSummary `json:"predictions"`
	Errors             []AutoRoutingErrorSummary      `json:"errors"`
	CostBands          []AutoRoutingCostBandSummary   `json:"cost_bands"`
	ErrorClassCounts   []AutoRoutingReasonSummary     `json:"error_class_counts"`
	ContextWindowHints []AutoRoutingContextWindowHint `json:"context_window_hints"`
	Readiness          []AutoRoutingReadiness         `json:"readiness"`
	SampleLimit        int                            `json:"sample_limit"`
}

type autoRequestSample struct {
	RequestID    uint64
	UserID       uint
	Status       string
	StartedAt    time.Time
	DurationMs   int64
	FirstTokenMs int64
	PromptTokens int64
	Cost         float64
	TargetModel  string
	RouteEvents  string
	Model        string
	GroupID      int
	AutoTask     string
	AutoMode     string
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
	users                                                     map[uint]bool
	timeBuckets                                               map[int64]bool
	modelSuccess, modelSuccessful                             map[string]int64
	firstTokenDurations                                       []float64
	streamStarted, streamIncomplete                           int64
	modelFirstToken                                           map[string][]float64
	modelStreamStarted, modelStreamIncomplete                 map[string]int64
	modelErrorClasses                                         map[string]map[string]int64
	modelBands                                                map[string]map[string]*autoModelBandEvidence
	modelTimeline                                             map[string][]autoModelOutcomeSample
	configuredCandidates, verifiedCandidates                  int
	anthropicStreamCandidates, anthropicStreamVerified        int
	labelingEnabled                                           bool
	requireMultiUserEvidence                                  bool
}

type autoModelBandEvidence struct {
	samples, success                int64
	promptTokenTotal                int64
	promptTokens                    []float64
	firstTokenDurations             []float64
	streamStarted, streamIncomplete int64
	errorClasses                    map[string]int64
}

type autoModelOutcomeSample struct {
	StartedAt        time.Time
	Success          bool
	FirstTokenMs     int64
	StreamStarted    bool
	StreamIncomplete bool
}

const autoReadinessMinimumBuckets = 4
const autoReadinessMinimumModelSamples = 10
const autoReadinessMinimumModelBandSamples = 5
const autoReadinessMinimumFirstTokenSamples = 10
const autoReadinessMinimumStreamSamples = 10
const autoReadinessMinimumTrendSamplesPerHalf = 5
const autoReadinessMaxSuccessRateDrop = 0.10
const autoReadinessMaxFirstTokenP90Rise = 0.25
const autoReadinessMaxFirstTokenP90Ms = 30000
const autoReadinessMaxStreamIncompleteRate = 0.05
const autoReadinessStreamRateRiseTrigger = 0.02

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

func autoAttemptErrorClass(status string) string {
	switch status {
	case "limited":
		return "rate"
	case "timeout":
		return "timeout"
	case "client_error":
		return "client"
	case "context_length":
		return "context"
	case "policy_blocked":
		return "policy"
	case "auth_error":
		return "auth"
	case "quota":
		return "quota"
	case "overloaded":
		return "overload"
	case "model_missing":
		return "model"
	case "compatibility_error":
		return "compatibility"
	case "canceled":
		return "canceled"
	default:
		return "unknown"
	}
}

func autoModelFailureScope(class string) string {
	switch class {
	case "rate", "auth", "quota", "overload", "compatibility", "timeout", "stream_incomplete", "unknown":
		return "channel"
	case "client", "policy", "canceled":
		return "request"
	case "model":
		return "candidate"
	default:
		return "other"
	}
}

func autoFailureClassCounts(counts map[string]int64) []AutoRoutingReasonSummary {
	result := make([]AutoRoutingReasonSummary, 0, len(counts))
	for class, count := range counts {
		if count <= 0 {
			continue
		}
		result = append(result, AutoRoutingReasonSummary{Reason: class, Count: count})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}
		return result[i].Reason < result[j].Reason
	})
	return result
}

var autoInputCostBand = model.AutoInputBand

func autoModelBandRiskFor(task, model, band string, evidence *autoModelBandEvidence) AutoRoutingModelBandRisk {
	risk := AutoRoutingModelBandRisk{
		Task: task, Model: model, Band: band, State: "collect_samples",
		ErrorClassCounts: make([]AutoRoutingReasonSummary, 0),
		Reasons:          make([]string, 0),
	}
	if evidence == nil {
		return risk
	}
	risk.Samples = evidence.samples
	risk.Success = evidence.success
	risk.AvgPromptTokens = float64(evidence.promptTokenTotal) / float64(evidence.samples)
	risk.P90PromptTokens = autoPercentile(evidence.promptTokens, 0.9)
	risk.FirstTokenSamples = int64(len(evidence.firstTokenDurations))
	risk.FirstTokenP90Ms = autoPercentile(evidence.firstTokenDurations, 0.9)
	risk.StreamStarted = evidence.streamStarted
	risk.StreamIncomplete = evidence.streamIncomplete
	risk.ErrorClassCounts = autoFailureClassCounts(evidence.errorClasses)
	if evidence.samples > 0 {
		risk.SuccessRate = float64(evidence.success) / float64(evidence.samples)
		risk.SuccessLow = autoWilsonLower(evidence.success, evidence.samples)
	}
	if evidence.streamStarted > 0 {
		risk.StreamIncompleteRate = float64(evidence.streamIncomplete) / float64(evidence.streamStarted)
	}
	if evidence.samples < autoReadinessMinimumModelBandSamples {
		risk.Reasons = append(risk.Reasons, "band_sample_below_5")
		return risk
	}
	if risk.SuccessRate < 0.80 {
		risk.State = "blocked"
		risk.Reasons = append(risk.Reasons, "band_success_below_80_percent")
	} else if evidence.streamStarted >= autoReadinessMinimumModelBandSamples &&
		risk.StreamIncompleteRate > autoReadinessMaxStreamIncompleteRate {
		risk.State = "blocked"
		risk.Reasons = append(risk.Reasons, "band_stream_incomplete_above_5_percent")
	} else if len(evidence.firstTokenDurations) >= autoReadinessMinimumModelBandSamples &&
		risk.FirstTokenP90Ms > autoReadinessMaxFirstTokenP90Ms {
		risk.State = "blocked"
		risk.Reasons = append(risk.Reasons, "band_first_token_p90_above_30_seconds")
	} else {
		risk.State = "healthy"
		risk.EligibleForPromotion = true
	}
	for _, item := range risk.ErrorClassCounts {
		// 任一上下文超限都说明该渠道没有兑现目录中的窗口值。此类请求不会
		// 进入成功样本，因此也不能要求先凑齐普通失败的成功率门槛。
		if item.Reason == "context" {
			risk.State = "blocked"
			risk.EligibleForPromotion = false
			risk.Reasons = append(risk.Reasons, "band_context_length_error")
			break
		}
	}
	return risk
}

func autoBlockedModelBands(risks []AutoRoutingModelBandRisk) []string {
	bands := make([]string, 0)
	for _, risk := range risks {
		if risk.State != "healthy" {
			bands = append(bands, risk.Band)
		}
	}
	sort.Strings(bands)
	return bands
}

func autoModelRiskFor(
	task, model string,
	attempts, success int64,
	earlierSamples, earlierSuccess, recentSamples, recentSuccess int64,
	firstTokenDurations []float64,
	earlierFirstToken, recentFirstToken []float64,
	streamStarted, streamIncomplete,
	earlierStreamStarted, earlierStreamIncomplete, recentStreamStarted, recentStreamIncomplete int64,
) AutoRoutingModelRisk {
	risk := AutoRoutingModelRisk{
		Task: task, Model: model, ActiveRequests: attempts, Success: success,
		Failed:         attempts - success,
		EarlierSamples: earlierSamples, EarlierSuccess: earlierSuccess,
		RecentSamples: recentSamples, RecentSuccess: recentSuccess,
		FirstTokenSamples: int64(len(firstTokenDurations)),
		StreamStarted:     streamStarted, StreamIncomplete: streamIncomplete,
		ErrorClassCounts: make([]AutoRoutingReasonSummary, 0),
		BandRisks:        make([]AutoRoutingModelBandRisk, 0),
		Reasons:          make([]string, 0),
	}
	if attempts > 0 {
		risk.SuccessRate = float64(success) / float64(attempts)
		risk.SuccessLow = autoWilsonLower(success, attempts)
	}
	if len(firstTokenDurations) > 0 {
		risk.FirstTokenP90Ms = autoPercentile(firstTokenDurations, 0.9)
	}
	risk.EarlierFirstTokenSamples = int64(len(earlierFirstToken))
	risk.RecentFirstTokenSamples = int64(len(recentFirstToken))
	if len(earlierFirstToken) > 0 {
		risk.EarlierFirstTokenP90Ms = autoPercentile(earlierFirstToken, 0.9)
	}
	if len(recentFirstToken) > 0 {
		risk.RecentFirstTokenP90Ms = autoPercentile(recentFirstToken, 0.9)
	}
	if streamStarted > 0 {
		risk.StreamIncompleteRate = float64(streamIncomplete) / float64(streamStarted)
	}
	risk.EarlierStreamStarted = earlierStreamStarted
	risk.EarlierStreamIncomplete = earlierStreamIncomplete
	risk.RecentStreamStarted = recentStreamStarted
	risk.RecentStreamIncomplete = recentStreamIncomplete
	if earlierStreamStarted > 0 {
		risk.EarlierStreamRate = float64(earlierStreamIncomplete) / float64(earlierStreamStarted)
	}
	if recentStreamStarted > 0 {
		risk.RecentStreamRate = float64(recentStreamIncomplete) / float64(recentStreamStarted)
	}
	if earlierSamples > 0 {
		risk.EarlierSuccessRate = float64(earlierSuccess) / float64(earlierSamples)
	}
	if recentSamples > 0 {
		risk.RecentSuccessRate = float64(recentSuccess) / float64(recentSamples)
	}
	if earlierSamples >= autoReadinessMinimumTrendSamplesPerHalf &&
		recentSamples >= autoReadinessMinimumTrendSamplesPerHalf {
		risk.SuccessRateDelta = risk.RecentSuccessRate - risk.EarlierSuccessRate
		if risk.SuccessRateDelta <= -autoReadinessMaxSuccessRateDrop {
			risk.SuccessTrend = "declining"
		} else {
			risk.SuccessTrend = "stable"
		}
	} else {
		risk.SuccessTrend = "insufficient"
	}
	if risk.SuccessTrend == "declining" {
		risk.Reasons = append(risk.Reasons, "model_recent_success_drop_above_10_percent")
	}
	if len(earlierFirstToken) >= autoReadinessMinimumTrendSamplesPerHalf &&
		len(recentFirstToken) >= autoReadinessMinimumTrendSamplesPerHalf &&
		risk.EarlierFirstTokenP90Ms > 0 {
		risk.FirstTokenP90DeltaMs = risk.RecentFirstTokenP90Ms - risk.EarlierFirstTokenP90Ms
		rise := risk.FirstTokenP90DeltaMs / risk.EarlierFirstTokenP90Ms
		if rise >= autoReadinessMaxFirstTokenP90Rise {
			risk.FirstTokenTrend = "degrading"
			risk.Reasons = append(risk.Reasons, "model_first_token_p90_rise_above_25_percent")
		} else {
			risk.FirstTokenTrend = "stable"
		}
	} else {
		risk.FirstTokenTrend = "insufficient"
	}
	if earlierStreamStarted >= autoReadinessMinimumTrendSamplesPerHalf &&
		recentStreamStarted >= autoReadinessMinimumTrendSamplesPerHalf {
		risk.StreamRateDelta = risk.RecentStreamRate - risk.EarlierStreamRate
		if risk.RecentStreamRate > autoReadinessMaxStreamIncompleteRate &&
			risk.RecentStreamRate >= risk.EarlierStreamRate+autoReadinessStreamRateRiseTrigger {
			risk.StreamTrend = "degrading"
			risk.Reasons = append(risk.Reasons, "model_recent_stream_incomplete_rise")
		} else {
			risk.StreamTrend = "stable"
		}
	} else {
		risk.StreamTrend = "insufficient"
	}
	if attempts < autoReadinessMinimumModelSamples {
		risk.State = "collect_samples"
		risk.Reasons = append(risk.Reasons, "model_sample_below_10")
	} else if risk.SuccessRate < 0.85 {
		risk.State = "blocked"
		risk.Reasons = append(risk.Reasons, "model_success_below_85_percent")
	} else if streamStarted >= autoReadinessMinimumStreamSamples &&
		risk.StreamIncompleteRate > autoReadinessMaxStreamIncompleteRate {
		risk.State = "blocked"
		risk.Reasons = append(risk.Reasons, "model_stream_incomplete_above_5_percent")
	} else if len(firstTokenDurations) >= autoReadinessMinimumFirstTokenSamples &&
		risk.FirstTokenP90Ms > autoReadinessMaxFirstTokenP90Ms {
		risk.State = "blocked"
		risk.Reasons = append(risk.Reasons, "model_first_token_p90_above_30_seconds")
	} else if risk.SuccessTrend == "declining" {
		risk.State = "blocked"
	} else if risk.FirstTokenTrend == "degrading" {
		risk.State = "blocked"
	} else if risk.StreamTrend == "degrading" {
		risk.State = "blocked"
	} else {
		risk.State = "healthy"
		risk.EligibleForPromotion = true
	}
	return risk
}

func autoReadinessFor(task string, evidence autoReadinessEvidence) AutoRoutingReadiness {
	readiness := AutoRoutingReadiness{
		Task: task, ConfidenceLevelPercent: 95,
		Classified: evidence.classified, ShadowRequests: evidence.shadowRequests,
		ShadowCompared: int64(evidence.compared), ActiveRequests: evidence.activeRequests,
		PredictionAgreementLow: autoWilsonLower(int64(evidence.same), int64(evidence.compared)),
		ActiveSuccessLow:       autoWilsonLower(evidence.activeSuccess, evidence.activeRequests),
		PredictedCostUSD:       evidence.predictedCost, ActualCostUSD: evidence.actualCost,
		CostSamples:            len(evidence.costRatios),
		EvidenceUsers:          len(evidence.users),
		EvidenceBuckets:        len(evidence.timeBuckets),
		RequiredBuckets:        autoReadinessMinimumBuckets,
		AdminMultiUserEvidence: evidence.requireMultiUserEvidence,
		ConfiguredCandidates:   evidence.configuredCandidates, VerifiedCandidates: evidence.verifiedCandidates,
		AnthropicStreamCandidates: evidence.anthropicStreamCandidates, AnthropicStreamVerified: evidence.anthropicStreamVerified,
		FirstTokenSamples: int64(len(evidence.firstTokenDurations)),
		StreamStarted:     evidence.streamStarted,
		StreamIncomplete:  evidence.streamIncomplete,
		ModelRisks:        make([]AutoRoutingModelRisk, 0),
		Reasons:           make([]string, 0),
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
	if len(evidence.firstTokenDurations) > 0 {
		readiness.FirstTokenP90Ms = autoPercentile(evidence.firstTokenDurations, 0.9)
	}
	if evidence.streamStarted > 0 {
		readiness.StreamIncompleteRate = float64(evidence.streamIncomplete) / float64(evidence.streamStarted)
	}
	var minModelSuccessRate float64
	for name, attempts := range evidence.modelSuccess {
		if attempts == 0 {
			continue
		}
		readiness.ModelTeams++
		readiness.ModelSamples += int(attempts)
		teamRate := float64(evidence.modelSuccessful[name]) / float64(attempts)
		if minModelSuccessRate == 0 || teamRate < minModelSuccessRate {
			minModelSuccessRate = teamRate
		}
		trend := autoSplitOutcomeSamples(evidence.modelTimeline[name])
		risk := autoModelRiskFor(task, name, attempts, evidence.modelSuccessful[name],
			trend.EarlierSamples, trend.EarlierSuccess, trend.RecentSamples, trend.RecentSuccess,
			evidence.modelFirstToken[name], trend.EarlierFirstToken, trend.RecentFirstToken,
			evidence.modelStreamStarted[name], evidence.modelStreamIncomplete[name],
			trend.EarlierStreamStarted, trend.EarlierStreamIncomplete,
			trend.RecentStreamStarted, trend.RecentStreamIncomplete)
		risk.ErrorClassCounts = autoFailureClassCounts(evidence.modelErrorClasses[name])
		bands := evidence.modelBands[name]
		for _, band := range []string{"short", "medium", "long", "xlong"} {
			item := autoModelBandRiskFor(task, name, band, bands[band])
			if item.Samples == 0 {
				continue
			}
			risk.BandRisks = append(risk.BandRisks, item)
		}
		if risk.BandRisks == nil {
			risk.BandRisks = make([]AutoRoutingModelBandRisk, 0)
		}
		for _, item := range risk.ErrorClassCounts {
			switch autoModelFailureScope(item.Reason) {
			case "channel":
				risk.ChannelFailures += item.Count
			case "request":
				risk.RequestFailures += item.Count
			case "candidate":
				risk.CandidateFailures += item.Count
			default:
				risk.OtherFailures += item.Count
			}
		}
		if risk.SuccessRate < 0.85 {
			dominantScope, dominantCount := "", int64(0)
			for _, scope := range []string{"channel", "request", "candidate", "other"} {
				count := map[string]int64{
					"channel":   risk.ChannelFailures,
					"request":   risk.RequestFailures,
					"candidate": risk.CandidateFailures,
					"other":     risk.OtherFailures,
				}[scope]
				if count > dominantCount {
					dominantScope, dominantCount = scope, count
				}
			}
			switch dominantScope {
			case "channel":
				risk.Reasons = append(risk.Reasons, "model_failures_are_channel_operational")
			case "request":
				risk.Reasons = append(risk.Reasons, "model_failures_are_request_side")
			case "candidate":
				risk.Reasons = append(risk.Reasons, "model_failures_are_candidate_side")
			}
		}
		blockedBands := autoBlockedModelBands(risk.BandRisks)
		if risk.ActiveRequests >= autoReadinessMinimumModelSamples && len(blockedBands) > 0 {
			risk.State = "blocked"
			risk.EligibleForPromotion = false
			risk.Reasons = append(risk.Reasons, "model_input_band_risk_blocks_scale")
		}
		readiness.ModelRisks = append(readiness.ModelRisks, risk)
		if risk.EligibleForPromotion {
			readiness.ModelEligibleTeams++
		}
	}
	if minModelSuccessRate > 0 {
		readiness.ModelMinSuccessRate = minModelSuccessRate
	}
	sort.Slice(readiness.ModelRisks, func(i, j int) bool {
		return readiness.ModelRisks[i].Model < readiness.ModelRisks[j].Model
	})
	if readiness.ModelSamples > 0 {
		var modelSuccess int64
		for _, success := range evidence.modelSuccessful {
			modelSuccess += success
		}
		readiness.ModelSuccessRate = float64(modelSuccess) / float64(readiness.ModelSamples)
		readiness.ModelTeamsLow = autoWilsonLower(modelSuccess, int64(readiness.ModelSamples))
	}
	if readiness.CostRatioP90Percent > 25 {
		readiness.Reasons = append(readiness.Reasons, "actual_cost_above_prediction")
	}
	if evidence.streamStarted >= autoReadinessMinimumStreamSamples &&
		readiness.StreamIncompleteRate > autoReadinessMaxStreamIncompleteRate {
		readiness.Reasons = append(readiness.Reasons, "stream_incomplete_rate_above_5_percent")
	}
	if evidence.firstTokenDurations != nil &&
		int64(len(evidence.firstTokenDurations)) >= autoReadinessMinimumFirstTokenSamples &&
		readiness.FirstTokenP90Ms > autoReadinessMaxFirstTokenP90Ms {
		readiness.Reasons = append(readiness.Reasons, "first_token_p90_above_30_seconds")
	}

	switch {
	case !evidence.labelingEnabled:
		readiness.State = "labeling_disabled"
		readiness.Reasons = append(readiness.Reasons, "task_candidate_selection_not_enabled")
	case evidence.classified < 20 || evidence.shadowRequests < 20:
		readiness.State = "collect_shadow"
		readiness.Reasons = append(readiness.Reasons, "need_more_classified_samples")
	case evidence.requireMultiUserEvidence && readiness.EvidenceUsers < 3:
		readiness.State = "collect_diverse_shadow"
		readiness.Reasons = append(readiness.Reasons, "need_more_users")
	case readiness.EvidenceBuckets < autoReadinessMinimumBuckets:
		readiness.State = "collect_diverse_shadow"
		readiness.Reasons = append(readiness.Reasons, "need_more_time_buckets")
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
	case readiness.ModelTeams > 0 && readiness.ModelEligibleTeams < readiness.ModelTeams:
		readiness.State = "hold_active"
		readiness.RecommendedRampPercent = 5
		readiness.Reasons = append(readiness.Reasons, "active_model_risk_blocks_scale")
	case readiness.StreamStarted >= autoReadinessMinimumStreamSamples &&
		readiness.StreamIncompleteRate > autoReadinessMaxStreamIncompleteRate:
		readiness.State = "hold_active"
		readiness.RecommendedRampPercent = 5
		readiness.Reasons = append(readiness.Reasons, "stream_incomplete_rate_above_5_percent")
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
		if readiness.FirstTokenSamples >= autoReadinessMinimumFirstTokenSamples &&
			readiness.FirstTokenP90Ms > autoReadinessMaxFirstTokenP90Ms {
			readiness.State = "hold_active"
			readiness.RecommendedRampPercent = 5
			readiness.Reasons = append(readiness.Reasons, "first_token_p90_above_30_seconds")
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

// autoSplitOutcomeSamples 将一个模型按请求开始时间从早到晚分成前后两段。
// 这用于识别“总体成功率仍高，但最近持续恶化”的模型，避免累计平均值掩盖退化。
type autoModelTrendEvidence struct {
	EarlierSamples          int64
	EarlierSuccess          int64
	RecentSamples           int64
	RecentSuccess           int64
	EarlierFirstToken       []float64
	RecentFirstToken        []float64
	EarlierStreamStarted    int64
	EarlierStreamIncomplete int64
	RecentStreamStarted     int64
	RecentStreamIncomplete  int64
}

func autoSplitOutcomeSamples(samples []autoModelOutcomeSample) autoModelTrendEvidence {
	if len(samples) == 0 {
		return autoModelTrendEvidence{}
	}
	sorted := append([]autoModelOutcomeSample(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].StartedAt.Equal(sorted[j].StartedAt) {
			return !sorted[i].Success && sorted[j].Success
		}
		return sorted[i].StartedAt.Before(sorted[j].StartedAt)
	})
	middle := len(sorted) / 2
	result := autoModelTrendEvidence{
		EarlierSamples: int64(middle),
		RecentSamples:  int64(len(sorted) - middle),
	}
	for _, sample := range sorted[:middle] {
		if sample.Success {
			result.EarlierSuccess++
		}
		if sample.FirstTokenMs > 0 {
			result.EarlierFirstToken = append(result.EarlierFirstToken, float64(sample.FirstTokenMs))
		}
		if sample.StreamStarted {
			result.EarlierStreamStarted++
			if sample.StreamIncomplete {
				result.EarlierStreamIncomplete++
			}
		}
	}
	for _, sample := range sorted[middle:] {
		if sample.Success {
			result.RecentSuccess++
		}
		if sample.FirstTokenMs > 0 {
			result.RecentFirstToken = append(result.RecentFirstToken, float64(sample.FirstTokenMs))
		}
		if sample.StreamStarted {
			result.RecentStreamStarted++
			if sample.StreamIncomplete {
				result.RecentStreamIncomplete++
			}
		}
	}
	return result
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
) *model.Group {
	if groupID <= 0 {
		return nil
	}
	group, err := GroupGet(groupID, scope)
	if err != nil {
		return nil
	}
	verified := autoGrantSet(group.RelayConfig.AutoToolVerified)
	responsesVerified := autoGrantSet(group.RelayConfig.AutoResponsesToolVerified)
	anthropicVerified := autoGrantSet(group.RelayConfig.AutoAnthropicToolVerified)
	streamVerified := autoGrantSet(group.RelayConfig.AutoAnthropicStreamVerified)
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
				id := item.ChannelGrantID
				anthropic := item.Protocols&model.ProtocolAnthropicMessage != 0
				if anthropic {
					actual.anthropicStreamCandidates++
					if streamVerified[id] {
						actual.anthropicStreamVerified++
					}
				}
				if verified[id] && item.Protocols&model.ProtocolOpenAIChatCompletion != 0 ||
					responsesVerified[id] && item.Protocols&model.ProtocolOpenAIResponse != 0 ||
					anthropic && (anthropicVerified[id] || streamVerified[id]) {
					actual.verifiedCandidates++
				}
				continue
			}
			if capability, ok := TaskCapabilityGet(item.ModelName, task); ok && capability.Verified {
				actual.verifiedCandidates++
			}
		}
	}
	return &group
}

func autoEffectiveGroupItemContextLength(group *model.Group, item model.GroupItem) (int64, bool) {
	limit := item.ContextLength
	if limit == 0 {
		limit = ModelReferenceGet(item.ModelName).ContextLength
	}
	if capability, ok := CapabilityGet(item.ModelName); ok &&
		capability.Source == model.CapabilitySourceManual &&
		strings.EqualFold(capability.Name, item.ModelName) &&
		capability.Context > 0 {
		limit = capability.Context
	}
	override := group.RelayConfig.AutoContextOverrides[strconv.Itoa(item.ChannelGrantID)]
	if override > 0 {
		return int64(override), true
	}
	return int64(limit), false
}

func matchContextWindowHintItem(group *model.Group, groupItemID, channelID int, channelCode, modelName string) (model.GroupItem, bool) {
	if group == nil {
		return model.GroupItem{}, false
	}
	var matched model.GroupItem
	for _, item := range group.Items {
		if item.ModelName != modelName || (groupItemID > 0 && item.ID != groupItemID) {
			continue
		}
		if channelID > 0 && channelID != item.ChannelID {
			continue
		}
		if groupItemID == 0 && channelID == 0 && (channelCode == "" || item.ChannelName != channelCode) {
			continue
		}
		if matched.ID != 0 {
			return model.GroupItem{}, false
		}
		matched = item
	}
	return matched, matched.ID != 0
}

func contextWindowHintStatusRank(status string) int {
	switch status {
	case "exceeds_observed_failure":
		return 0
	case "review_margin":
		return 1
	case "covered":
		return 3
	default:
		return 2
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
		Errors: make([]AutoRoutingErrorSummary, 0), CostBands: make([]AutoRoutingCostBandSummary, 0),
		ErrorClassCounts:   make([]AutoRoutingReasonSummary, 0),
		ContextWindowHints: make([]AutoRoutingContextWindowHint, 0),
	}
	if scope.IsAdmin() && groupID > 0 {
		var err error
		summary.ProbeCandidates, err = AutoProbeCandidates(scope, groupID, now)
		if err != nil {
			return summary, fmt.Errorf("failed to load probe evidence: %w", err)
		}
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
	if err := query.Select("id AS request_id, user_id, status, started_at, duration_ns / 1000000 AS duration_ms, first_token_duration_ns / 1000000 AS first_token_ms, prompt_tokens, cost, target_model, route_events, model, group_id, auto_task, auto_mode").
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
	finalAttemptRound := make(map[uint64]int, len(requests))
	for _, attempt := range attempts {
		finalAttemptRound[attempt.RequestID] = attempt.Round
	}

	type taskCount struct {
		requests, success, failed                        int64
		duration                                         int64
		durations                                        []float64
		firstTokenSamples, streamStarted                 int64
		streamFailedAfterFirst, streamCanceledAfterFirst int64
		firstTokenDuration                               int64
		firstTokenDurations                              []float64
	}
	tasks := make(map[string]*taskCount)
	modelKey := func(task, name string) string { return task + "\x00" + name }
	type modelCount struct {
		attempts, success   int64
		duration            int64
		durations           []float64
		firstTokenSamples   int64
		firstTokenDuration  int64
		firstTokenDurations []float64
		cost                float64
	}
	models := make(map[string]*modelCount)
	errorsByTask := make(map[string]int64)
	errorClassCounts := make(map[string]int64)
	type costBandCount struct {
		requests, success int64
		promptTokens      int64
		costTotal         float64
		costs             []float64
	}
	costBands := make(map[string]*costBandCount)
	costBandKey := func(task, band string) string { return task + "\x00" + band }
	exclusions := make(map[string]int64)
	events := make(map[string]int64)
	type predictionCount struct {
		compared, same            int64
		predictedCost, actualCost float64
	}
	predictions := make(map[string]*predictionCount)
	type contextWindowIdentity struct {
		task, model, channelCode        string
		groupID, groupItemID, channelID int
	}
	type contextWindowCount struct {
		samples        int64
		estimated      int64
		minInputTokens int64
		maxInputTokens int64
	}
	contextWindows := make(map[contextWindowIdentity]*contextWindowCount)
	maxSuccessInputByWindow := make(map[contextWindowIdentity]int64)
	shadowPredictions := make(map[uint64]autoPredictionEvidence)
	requestTasks := make(map[uint64]string)
	readinessByTask := make(map[string]*autoReadinessEvidence)
	groupConfig := applyReadinessCandidateConfig(scope, groupID, readinessByTask)
	for _, evidence := range readinessByTask {
		evidence.requireMultiUserEvidence = scope.IsAdmin()
	}
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
			readiness.requireMultiUserEvidence = scope.IsAdmin()
		}
		if readiness.users == nil {
			readiness.users = make(map[uint]bool)
		}
		readiness.users[request.UserID] = true
		if readiness.timeBuckets == nil {
			readiness.timeBuckets = make(map[int64]bool)
		}
		readiness.timeBuckets[request.StartedAt.Truncate(6*time.Hour).Unix()] = true
		readiness.classified++
		if request.AutoMode == "shadow" {
			readiness.shadowRequests++
		}
		tc.requests++
		tc.duration += request.DurationMs
		tc.durations = append(tc.durations, float64(request.DurationMs))
		if request.Status == "success" {
			tc.success++
		} else if request.Status == "failed" {
			tc.failed++
		}
		if request.FirstTokenMs > 0 {
			tc.firstTokenSamples++
			tc.firstTokenDuration += request.FirstTokenMs
			tc.firstTokenDurations = append(tc.firstTokenDurations, float64(request.FirstTokenMs))
			readiness.firstTokenDurations = append(readiness.firstTokenDurations, float64(request.FirstTokenMs))
		}
		if request.AutoMode == "active" && request.FirstTokenMs > 0 {
			readiness.streamStarted++
			switch request.Status {
			case "failed":
				readiness.streamIncomplete++
			case "canceled":
				readiness.streamIncomplete++
			}
		}
		if request.FirstTokenMs > 0 {
			tc.streamStarted++
			switch request.Status {
			case "failed":
				tc.streamFailedAfterFirst++
			case "canceled":
				tc.streamCanceledAfterFirst++
			}
		}
		if band := autoInputCostBand(request.PromptTokens); band != "" {
			key := costBandKey(task, band)
			bc := costBands[key]
			if bc == nil {
				bc = &costBandCount{}
				costBands[key] = bc
			}
			bc.requests++
			bc.promptTokens += request.PromptTokens
			bc.costTotal += request.Cost
			bc.costs = append(bc.costs, request.Cost)
			if request.Status == "success" {
				bc.success++
			}
		}
		var routeEvents []struct {
			Phase  string `json:"phase"`
			Reason string `json:"reason"`
		}
		if json.Unmarshal([]byte(request.RouteEvents), &routeEvents) == nil {
			for _, event := range routeEvents {
				if event.Phase == "auto_classification" && strings.HasPrefix(event.Reason, "unsupported_") {
					events[event.Reason]++
					continue
				}
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
		windowKey := contextWindowIdentity{task, name, attempt.ChannelCode, request.GroupID, attempt.GroupItemID, attempt.ChannelID}
		mc.duration += attempt.DurationMs
		mc.durations = append(mc.durations, float64(attempt.DurationMs))
		if attempt.FirstTokenMs > 0 {
			mc.firstTokenSamples++
			mc.firstTokenDuration += attempt.FirstTokenMs
			mc.firstTokenDurations = append(mc.firstTokenDurations, float64(attempt.FirstTokenMs))
		}
		if attempt.Status != "success" && attempt.Status != "running" {
			class := autoAttemptErrorClass(attempt.Status)
			errorsByTask[modelKey(task, class)]++
			errorClassCounts[class]++
			if class == "context" {
				tokens := request.PromptTokens
				estimated := tokens <= 0 || attempt.Round != finalAttemptRound[attempt.RequestID]
				if estimated {
					var details struct {
						EstimatedInput int64 `json:"estimated_input"`
					}
					if json.Unmarshal([]byte(decision.Details), &details) == nil {
						tokens = details.EstimatedInput
					} else {
						tokens = 0
					}
				}
				if tokens > 0 {
					hint := contextWindows[windowKey]
					if hint == nil {
						hint = &contextWindowCount{
							minInputTokens: tokens,
						}
						contextWindows[windowKey] = hint
					}
					hint.samples++
					if estimated {
						hint.estimated++
					}
					hint.minInputTokens = min(hint.minInputTokens, tokens)
					hint.maxInputTokens = max(hint.maxInputTokens, tokens)
				}
			}
		}
		if attempt.Status == "success" {
			mc.success++
			if request.Status == "success" && attempt.Round == finalAttemptRound[attempt.RequestID] {
				maxSuccessInputByWindow[windowKey] = max(maxSuccessInputByWindow[windowKey], request.PromptTokens)
			}
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
			if request.AutoMode == "active" &&
				(evidence.RouteAction == "active" || evidence.RouteAction == "explore") &&
				!activeReadinessRequests[attempt.RequestID] {
				activeReadinessRequests[attempt.RequestID] = true
				readiness := readinessByTask[task]
				if readiness != nil {
					if readiness.modelSuccess == nil {
						readiness.modelSuccess = make(map[string]int64)
						readiness.modelSuccessful = make(map[string]int64)
						readiness.modelFirstToken = make(map[string][]float64)
						readiness.modelStreamStarted = make(map[string]int64)
						readiness.modelStreamIncomplete = make(map[string]int64)
						readiness.modelErrorClasses = make(map[string]map[string]int64)
						readiness.modelBands = make(map[string]map[string]*autoModelBandEvidence)
						readiness.modelTimeline = make(map[string][]autoModelOutcomeSample)
					}
					readiness.modelSuccess[name]++
					readiness.activeRequests++
					modelSuccess := request.Status == "success" && attempt.Status == "success"
					band := autoInputCostBand(request.PromptTokens)
					if band != "" {
						if readiness.modelBands[name] == nil {
							readiness.modelBands[name] = make(map[string]*autoModelBandEvidence)
						}
						be := readiness.modelBands[name][band]
						if be == nil {
							be = &autoModelBandEvidence{}
							readiness.modelBands[name][band] = be
						}
						be.samples++
						if modelSuccess {
							be.success++
						}
						be.promptTokenTotal += request.PromptTokens
						be.promptTokens = append(be.promptTokens, float64(request.PromptTokens))
						if !modelSuccess {
							if be.errorClasses == nil {
								be.errorClasses = make(map[string]int64)
							}
							class := autoAttemptErrorClass(attempt.Status)
							if attempt.Status == "success" {
								class = "stream_incomplete"
							}
							be.errorClasses[class]++
						}
						if attempt.FirstTokenMs > 0 {
							be.firstTokenDurations = append(be.firstTokenDurations, float64(attempt.FirstTokenMs))
							be.streamStarted++
							if request.Status == "failed" || request.Status == "canceled" {
								be.streamIncomplete++
							}
						}
					}
					if !modelSuccess {
						if readiness.modelErrorClasses[name] == nil {
							readiness.modelErrorClasses[name] = make(map[string]int64)
						}
						class := autoAttemptErrorClass(attempt.Status)
						if attempt.Status == "success" {
							class = "stream_incomplete"
						}
						readiness.modelErrorClasses[name][class]++
					}
					readiness.modelTimeline[name] = append(readiness.modelTimeline[name], autoModelOutcomeSample{
						StartedAt:     request.StartedAt,
						Success:       modelSuccess,
						FirstTokenMs:  attempt.FirstTokenMs,
						StreamStarted: attempt.FirstTokenMs > 0,
						StreamIncomplete: attempt.FirstTokenMs > 0 &&
							(request.Status == "failed" || request.Status == "canceled"),
					})
					if attempt.FirstTokenMs > 0 {
						readiness.modelFirstToken[name] = append(readiness.modelFirstToken[name], float64(attempt.FirstTokenMs))
						readiness.modelStreamStarted[name]++
						if request.Status == "failed" || request.Status == "canceled" {
							readiness.modelStreamIncomplete[name]++
						}
					}
					if modelSuccess {
						readiness.activeSuccess++
						readiness.modelSuccessful[name]++
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
			item.P90DurationMs = autoPercentile(count.durations, 0.9)
		}
		item.FirstTokenSamples = count.firstTokenSamples
		if count.firstTokenSamples > 0 {
			item.AvgFirstTokenMs = float64(count.firstTokenDuration) / float64(count.firstTokenSamples)
			item.P90FirstTokenMs = autoPercentile(count.firstTokenDurations, 0.9)
		}
		item.StreamStarted = count.streamStarted
		item.StreamFailedAfterFirst = count.streamFailedAfterFirst
		item.StreamCanceledAfterFirst = count.streamCanceledAfterFirst
		if count.streamStarted > 0 {
			item.StreamIncompleteRate = float64(count.streamFailedAfterFirst+count.streamCanceledAfterFirst) /
				float64(count.streamStarted)
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
			item.P90DurationMs = autoPercentile(count.durations, 0.9)
		}
		item.FirstTokenSamples = count.firstTokenSamples
		if count.firstTokenSamples > 0 {
			item.AvgFirstTokenMs = float64(count.firstTokenDuration) / float64(count.firstTokenSamples)
			item.P90FirstTokenMs = autoPercentile(count.firstTokenDurations, 0.9)
		}
		item.SuccessRate = float64(count.success) / float64(count.attempts)
		if count.success > 0 {
			item.AvgCostUSD = count.cost / float64(count.success)
		}
		summary.Models = append(summary.Models, item)
	}
	for key, count := range errorsByTask {
		parts := strings.SplitN(key, "\x00", 2)
		summary.Errors = append(summary.Errors, AutoRoutingErrorSummary{
			Task: parts[0], Class: parts[1], Count: count,
		})
	}
	for key, count := range costBands {
		parts := strings.SplitN(key, "\x00", 2)
		item := AutoRoutingCostBandSummary{
			Task: parts[0], Band: parts[1], Requests: count.requests, Success: count.success,
		}
		if count.requests > 0 {
			item.AvgPromptTokens = float64(count.promptTokens) / float64(count.requests)
			item.AvgCostUSD = count.costTotal / float64(count.requests)
			item.P90CostUSD = autoPercentile(count.costs, 0.9)
		}
		summary.CostBands = append(summary.CostBands, item)
	}
	for reason, count := range exclusions {
		summary.Exclusions = append(summary.Exclusions, AutoRoutingReasonSummary{Reason: reason, Count: count})
	}
	for reason, count := range events {
		summary.Events = append(summary.Events, AutoRoutingReasonSummary{Reason: reason, Count: count})
	}
	for class, count := range errorClassCounts {
		summary.ErrorClassCounts = append(summary.ErrorClassCounts, AutoRoutingReasonSummary{
			Reason: class, Count: count,
		})
	}
	for key, count := range contextWindows {
		hint := AutoRoutingContextWindowHint{
			GroupID:                       key.groupID,
			GroupItemID:                   key.groupItemID,
			Task:                          key.task,
			Model:                         key.model,
			ChannelCode:                   key.channelCode,
			Samples:                       count.samples,
			EstimatedSamples:              count.estimated,
			ObservedMinInputTokens:        count.minInputTokens,
			ObservedMaxInputTokens:        count.maxInputTokens,
			ObservedMaxSuccessInputTokens: maxSuccessInputByWindow[key],
			Reason:                        "context_length_error_observed",
		}
		// 最小失败输入长度是该渠道真实窗口的最紧上界。建议值在此基础上保留 25% 安全余量，
		// 避免把大样本失败抬高到已经触发超限的较小输入之上；管理员确认后再写入上下文覆盖。
		if count.minInputTokens > 0 {
			hint.SuggestedContextLength = max(1, count.minInputTokens/4*3+count.minInputTokens%4*3/4)
		}
		if item, ok := matchContextWindowHintItem(groupConfig, key.groupItemID, key.channelID, key.channelCode, key.model); ok {
			hint.ChannelGrantID = item.ChannelGrantID
			configured, hasOverride := autoEffectiveGroupItemContextLength(groupConfig, item)
			hint.ConfiguredContextLength = configured
			hint.HasContextOverride = hasOverride
			if configured > 0 && hint.SuggestedContextLength > 0 {
				switch {
				case configured <= hint.SuggestedContextLength:
					hint.ConfigStatus = "covered"
				case hint.ObservedMinInputTokens > 0 && configured >= hint.ObservedMinInputTokens:
					hint.ConfigStatus = "exceeds_observed_failure"
				default:
					hint.ConfigStatus = "review_margin"
				}
			}
		}
		summary.ContextWindowHints = append(summary.ContextWindowHints, hint)
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
	sort.Slice(summary.Errors, func(i, j int) bool { return summary.Errors[i].Count > summary.Errors[j].Count })
	sort.Slice(summary.ErrorClassCounts, func(i, j int) bool {
		return summary.ErrorClassCounts[i].Count > summary.ErrorClassCounts[j].Count
	})
	sort.Slice(summary.CostBands, func(i, j int) bool {
		if summary.CostBands[i].Task != summary.CostBands[j].Task {
			return summary.CostBands[i].Task < summary.CostBands[j].Task
		}
		return summary.CostBands[i].Band < summary.CostBands[j].Band
	})
	sort.Slice(summary.ContextWindowHints, func(i, j int) bool {
		leftRank := contextWindowHintStatusRank(summary.ContextWindowHints[i].ConfigStatus)
		rightRank := contextWindowHintStatusRank(summary.ContextWindowHints[j].ConfigStatus)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		if summary.ContextWindowHints[i].Samples != summary.ContextWindowHints[j].Samples {
			return summary.ContextWindowHints[i].Samples > summary.ContextWindowHints[j].Samples
		}
		if summary.ContextWindowHints[i].ObservedMinInputTokens != summary.ContextWindowHints[j].ObservedMinInputTokens {
			return summary.ContextWindowHints[i].ObservedMinInputTokens < summary.ContextWindowHints[j].ObservedMinInputTokens
		}
		if summary.ContextWindowHints[i].Task != summary.ContextWindowHints[j].Task {
			return summary.ContextWindowHints[i].Task < summary.ContextWindowHints[j].Task
		}
		if summary.ContextWindowHints[i].Model != summary.ContextWindowHints[j].Model {
			return summary.ContextWindowHints[i].Model < summary.ContextWindowHints[j].Model
		}
		left, right := summary.ContextWindowHints[i], summary.ContextWindowHints[j]
		if left.ChannelCode != right.ChannelCode {
			return left.ChannelCode < right.ChannelCode
		}
		if left.GroupID != right.GroupID {
			return left.GroupID < right.GroupID
		}
		return left.GroupItemID < right.GroupItemID
	})
	sort.Slice(summary.Predictions, func(i, j int) bool {
		return summary.Predictions[i].Compared > summary.Predictions[j].Compared
	})
	sort.Slice(summary.Readiness, func(i, j int) bool {
		return summary.Readiness[i].Classified > summary.Readiness[j].Classified
	})
	return summary, nil
}

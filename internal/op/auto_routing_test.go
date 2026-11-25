package op

import (
	"context"
	"math"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

func TestAutoRoutingAggregateUsesTaskSnapshotAndLegacyEvents(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "auto-routing.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	now := time.Now()
	rows := []model.RelayRequest{
		{ID: 101, UserID: 7, GroupID: 37, Status: "success", StartedAt: now.Add(-time.Hour), DurationNs: int64(500 * time.Millisecond), Cost: 0.02, TargetModel: "model-a", AutoTask: "coding", AutoMode: "shadow", RouteEvents: `[{"phase":"auto_shadow","reason":"coding:item=9"}]`},
		{ID: 102, UserID: 7, GroupID: 37, Status: "failed", StartedAt: now.Add(-2 * time.Hour), DurationNs: int64(900 * time.Millisecond), TargetModel: "model-a", AutoTask: "coding", AutoMode: "shadow", RouteEvents: `[{"phase":"auto_shadow","reason":"coding:no_candidate"}]`},
		// Pre-v1.16.7 rows have no snapshot columns. Their route events must still be counted.
		{ID: 103, UserID: 7, GroupID: 37, Status: "success", StartedAt: now.Add(-3 * time.Hour), TargetModel: "model-b", AutoTask: "", AutoMode: "", RouteEvents: `[{"phase":"auto_shadow","reason":"summary:item=10"}]`},
	}
	if err := db.GetDB().Create(&rows).Error; err != nil {
		t.Fatalf("create requests: %v", err)
	}
	attempts := []model.RelayRequestAttempt{
		{RequestID: 101, Round: 1, TargetModel: "model-a", Status: "success", DurationMs: 500, Decision: `{"mode":"score","reason":"auto_coding","candidates":[{"reason":"auto_not_qualified"}]}`},
		{RequestID: 102, Round: 1, TargetModel: "model-a", Status: "failed", DurationMs: 900, Decision: `{"mode":"score","reason":"auto_coding","candidates":[{"reason":"auto_context_unknown"}]}`},
		{RequestID: 103, Round: 1, TargetModel: "model-b", Status: "success", DurationMs: 300, Decision: `{"mode":"score","reason":"auto_summary"}`},
	}
	if err := db.GetDB().Create(&attempts).Error; err != nil {
		t.Fatalf("create attempts: %v", err)
	}

	summary, err := AutoRoutingAggregate(model.Scope{ID: 7, Role: model.RoleUser}, 37, 7)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if summary.Requests != 3 || summary.TaskClassified != 3 || summary.Success != 2 || summary.Failed != 1 {
		t.Fatalf("unexpected totals: %+v", summary)
	}
	if len(summary.Tasks) != 2 || len(summary.Models) != 2 {
		t.Fatalf("tasks=%+v models=%+v", summary.Tasks, summary.Models)
	}
	for _, modelSummary := range summary.Models {
		if modelSummary.Task == "coding" && modelSummary.Model == "model-a" &&
			modelSummary.P90DurationMs <= modelSummary.AvgDurationMs {
			t.Fatalf("model P90 should expose the slow tail: %+v", modelSummary)
		}
	}
	for _, task := range summary.Tasks {
		if task.Task == "coding" && (task.Requests != 2 || task.Success != 1 || task.Failed != 1) {
			t.Fatalf("coding summary: %+v", task)
		}
		if task.Task == "coding" && task.P90DurationMs == 0 {
			t.Fatalf("coding P90 duration was not calculated: %+v", task)
		}
	}
	if len(summary.Exclusions) != 2 || summary.Exclusions[0].Count != 1 {
		t.Fatalf("exclusions: %+v", summary.Exclusions)
	}
	if len(summary.Errors) != 1 || summary.Errors[0].Class != "unknown" || summary.Errors[0].Count != 1 {
		t.Fatalf("attempt errors: %+v", summary.Errors)
	}
	if len(summary.CostBands) != 0 {
		t.Fatalf("missing input cost bands: %+v", summary.CostBands)
	}
}

func TestAutoRoutingAggregateScopeHidesOtherUsers(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "auto-routing-scope.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	now := time.Now()
	rows := []model.RelayRequest{
		{ID: 201, UserID: 1, GroupID: 37, Status: "success", StartedAt: now, AutoTask: "general", RouteEvents: `[]`},
		{ID: 202, UserID: 2, GroupID: 37, Status: "success", StartedAt: now, AutoTask: "general", RouteEvents: `[]`},
	}
	if err := db.GetDB().Create(&rows).Error; err != nil {
		t.Fatalf("create requests: %v", err)
	}
	summary, err := AutoRoutingAggregate(model.Scope{ID: 1, Role: model.RoleUser}, 37, 7)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if summary.Requests != 1 {
		t.Fatalf("user saw %d requests; cross-account rows leaked", summary.Requests)
	}
}

func TestAutoRoutingAggregateComparesShadowPrediction(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "auto-routing-prediction.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	now := time.Now()
	requests := []model.RelayRequest{
		{ID: 301, UserID: 7, GroupID: 37, Status: "success", StartedAt: now, Cost: 0.03, TargetModel: "model-fast", AutoTask: "coding", AutoMode: "shadow"},
	}
	if err := db.GetDB().Create(&requests).Error; err != nil {
		t.Fatalf("create request: %v", err)
	}
	attempt := model.RelayRequestAttempt{
		RequestID: 301, Round: 1, TargetModel: "model-fast", Status: "success",
		Decision: `{"mode":"score","phase":"success","reason":"strategy_score","details":"{\"prediction\":{\"model\":\"model-quality\",\"cost_usd\":0.05},\"actual_model\":\"model-fast\"}"}`,
	}
	if err := db.GetDB().Create(&attempt).Error; err != nil {
		t.Fatalf("create attempt: %v", err)
	}
	summary, err := AutoRoutingAggregate(model.Scope{ID: 7, Role: model.RoleUser}, 37, 7)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if len(summary.Predictions) != 1 {
		t.Fatalf("predictions: %+v", summary.Predictions)
	}
	prediction := summary.Predictions[0]
	if prediction.Compared != 1 || prediction.SameModel != 0 || prediction.PredictedCostUSD != 0.05 || prediction.ActualCostUSD != 0.03 {
		t.Fatalf("prediction comparison: %+v", prediction)
	}
}

func TestAutoRoutingReadinessStartsConservatively(t *testing.T) {
	users := map[uint]bool{1: true, 2: true, 3: true}
	buckets := map[int64]bool{1: true, 2: true, 3: true, 4: true}
	readiness := autoReadinessFor(model.AutoRoutingTaskCoding, autoReadinessEvidence{
		classified: 40, shadowRequests: 40, compared: 35, same: 28,
		users: users, timeBuckets: buckets,
		configuredCandidates: 2, verifiedCandidates: 2, labelingEnabled: true,
	})
	if readiness.State != "ready_for_active" || readiness.RecommendedRampPercent != 5 {
		t.Fatalf("unexpected readiness: %+v", readiness)
	}

	readiness = autoReadinessFor(model.AutoRoutingTaskCoding, autoReadinessEvidence{
		classified: 10, shadowRequests: 10, users: users, timeBuckets: buckets,
		configuredCandidates: 2, verifiedCandidates: 2, labelingEnabled: true,
	})
	if readiness.State != "collect_shadow" || readiness.RecommendedRampPercent != 0 {
		t.Fatalf("small sample did not collect shadow: %+v", readiness)
	}

	readiness = autoReadinessFor(model.AutoRoutingTaskCoding, autoReadinessEvidence{
		classified: 240, shadowRequests: 240, compared: 240, same: 158,
		users: users, timeBuckets: buckets,
		configuredCandidates: 2, verifiedCandidates: 2, labelingEnabled: true,
	})
	if readiness.PredictionAgreement < 0.65 || readiness.PredictionAgreementLow >= 0.60 {
		t.Fatalf("fixture should isolate a confidence lower bound below 60%%: %+v", readiness)
	}
	if readiness.State != "collect_shadow" ||
		!slices.Contains(readiness.Reasons, "prediction_confidence_below_60_percent") {
		t.Fatalf("weak confidence interval should keep collecting: %+v", readiness)
	}

	readiness = autoReadinessFor(model.AutoRoutingTaskCoding, autoReadinessEvidence{
		classified: 40, shadowRequests: 40, compared: 35, same: 28,
		users: map[uint]bool{1: true}, timeBuckets: buckets,
		configuredCandidates: 2, verifiedCandidates: 2, labelingEnabled: true,
		requireMultiUserEvidence: true,
	})
	if readiness.State != "collect_diverse_shadow" || !slices.Contains(readiness.Reasons, "need_more_users") {
		t.Fatalf("single-user evidence should not start a ramp: %+v", readiness)
	}

	readiness = autoReadinessFor(model.AutoRoutingTaskCoding, autoReadinessEvidence{
		classified: 40, shadowRequests: 40, compared: 35, same: 28,
		users: map[uint]bool{1: true}, timeBuckets: buckets,
		configuredCandidates: 2, verifiedCandidates: 2, labelingEnabled: true,
	})
	if readiness.State != "ready_for_active" || slices.Contains(readiness.Reasons, "need_more_users") {
		t.Fatalf("user-scoped evidence should not require inaccessible cross-user logs: %+v", readiness)
	}

	readiness = autoReadinessFor(model.AutoRoutingTaskCoding, autoReadinessEvidence{
		classified: 40, shadowRequests: 40, compared: 35, same: 28,
		users: users, timeBuckets: map[int64]bool{1: true, 2: true},
		configuredCandidates: 2, verifiedCandidates: 2, labelingEnabled: true,
	})
	if readiness.State != "collect_diverse_shadow" || !slices.Contains(readiness.Reasons, "need_more_time_buckets") {
		t.Fatalf("one-session evidence should not start a ramp: %+v", readiness)
	}
}

func TestAutoRoutingReadinessHoldsAndScalesActiveSafely(t *testing.T) {
	base := autoReadinessEvidence{
		classified: 80, shadowRequests: 80, compared: 70, same: 56,
		users:                map[uint]bool{1: true, 2: true, 3: true},
		timeBuckets:          map[int64]bool{1: true, 2: true, 3: true, 4: true},
		configuredCandidates: 2, verifiedCandidates: 2, labelingEnabled: true,
	}
	base.activeRequests = 10
	base.activeSuccess = 10
	readiness := autoReadinessFor(model.AutoRoutingTaskTool, base)
	if readiness.State != "hold_active" || readiness.RecommendedRampPercent != 5 {
		t.Fatalf("small active sample should hold at 5 percent: %+v", readiness)
	}

	base.activeRequests = 40
	base.activeSuccess = 40
	base.same = 60
	base.predictedCost = 1
	base.actualCost = 1
	base.costRatios = []float64{1}
	base.firstTokenDurations = make([]float64, autoReadinessMinimumFirstTokenSamples)
	for i := range base.firstTokenDurations {
		base.firstTokenDurations[i] = 500
	}
	base.streamStarted = autoReadinessMinimumStreamSamples
	readiness = autoReadinessFor(model.AutoRoutingTaskTool, base)
	if readiness.State != "scale_active" || readiness.RecommendedRampPercent != 10 {
		t.Fatalf("strong active evidence should scale to 10 percent: %+v", readiness)
	}

	base.activeSuccess = 30
	readiness = autoReadinessFor(model.AutoRoutingTaskTool, base)
	if readiness.State != "pause_active" || readiness.RecommendedRampPercent != 0 {
		t.Fatalf("weak active success should pause: %+v", readiness)
	}

	base.activeSuccess = 40
	base.costRatios = []float64{2}
	base.modelSuccess = nil
	base.modelSuccessful = nil
	readiness = autoReadinessFor(model.AutoRoutingTaskTool, base)
	if readiness.State != "hold_active" || !slices.Contains(readiness.Reasons, "cost_p90_above_50_percent") {
		t.Fatalf("high active cost should stop scaling: %+v", readiness)
	}

	base.costRatios = []float64{1.3}
	readiness = autoReadinessFor(model.AutoRoutingTaskTool, base)
	if readiness.State != "hold_active" || !slices.Contains(readiness.Reasons, "cost_p90_above_25_percent") {
		t.Fatalf("cost above safe expansion range should stop scaling: %+v", readiness)
	}

	base.actualCost = 1
	base.costRatios = nil
	readiness = autoReadinessFor(model.AutoRoutingTaskTool, base)
	if readiness.State != "hold_active" || !slices.Contains(readiness.Reasons, "need_cost_comparison_samples") {
		t.Fatalf("missing cost samples must not permit expansion: %+v", readiness)
	}

	base.actualCost = 1
	base.costRatios = []float64{1}
	base.modelSuccess = map[string]int64{"model-a": 40, "model-b": 40}
	base.modelSuccessful = map[string]int64{"model-a": 33, "model-b": 39}
	base.activeRequests = 80
	base.activeSuccess = 78
	readiness = autoReadinessFor(model.AutoRoutingTaskTool, base)
	if readiness.State != "hold_active" || !slices.Contains(readiness.Reasons, "active_model_risk_blocks_scale") {
		t.Fatalf("one underperforming model should stop a ramp: %+v", readiness)
	}
	if readiness.ModelTeams != 2 || readiness.ModelEligibleTeams != 1 ||
		len(readiness.ModelRisks) != 2 ||
		readiness.ModelRisks[0].Model != "model-a" ||
		readiness.ModelRisks[0].State != "blocked" ||
		!slices.Contains(readiness.ModelRisks[0].Reasons, "model_success_below_85_percent") ||
		readiness.ModelRisks[1].State != "healthy" {
		t.Fatalf("per-model risk cards should explain the blocked ramp: %+v", readiness.ModelRisks)
	}

	base.modelSuccess = nil
	base.modelSuccessful = nil
	base.streamIncomplete = 1
	readiness = autoReadinessFor(model.AutoRoutingTaskTool, base)
	if readiness.State != "hold_active" || !slices.Contains(readiness.Reasons, "stream_incomplete_rate_above_5_percent") {
		t.Fatalf("stream failures after the first token should stop scaling: %+v", readiness)
	}

	base.streamIncomplete = 0
	base.firstTokenDurations = make([]float64, autoReadinessMinimumFirstTokenSamples)
	for i := range base.firstTokenDurations {
		base.firstTokenDurations[i] = float64(autoReadinessMaxFirstTokenP90Ms + 100)
	}
	readiness = autoReadinessFor(model.AutoRoutingTaskTool, base)
	if readiness.State != "hold_active" || !slices.Contains(readiness.Reasons, "first_token_p90_above_30_seconds") {
		t.Fatalf("slow first-token tail should stop scaling: %+v", readiness)
	}
}

func TestAutoModelRiskScorecardsUseMinimumSamples(t *testing.T) {
	collecting := autoModelRiskFor(model.AutoRoutingTaskCoding, "small-model", 9, 9, 4, 4, 5, 5, nil, nil, nil, 0, 0, 0, 0, 0, 0)
	if collecting.State != "collect_samples" || collecting.EligibleForPromotion ||
		!slices.Contains(collecting.Reasons, "model_sample_below_10") {
		t.Fatalf("small model sample should only collect evidence: %+v", collecting)
	}

	healthy := autoModelRiskFor(model.AutoRoutingTaskCoding, "healthy-model", 10, 9, 5, 4, 5, 5, nil, nil, nil, 0, 0, 0, 0, 0, 0)
	if healthy.State != "healthy" || !healthy.EligibleForPromotion {
		t.Fatalf("90 percent success at ten samples should pass the risk threshold: %+v", healthy)
	}

	unstable := autoModelRiskFor(model.AutoRoutingTaskCoding, "unstable-model", 10, 8, 5, 4, 5, 4, nil, nil, nil, 0, 0, 0, 0, 0, 0)
	if unstable.State != "blocked" ||
		!slices.Contains(unstable.Reasons, "model_success_below_85_percent") {
		t.Fatalf("80 percent success at ten samples should block expansion: %+v", unstable)
	}

	atLimit := autoModelRiskFor(model.AutoRoutingTaskCoding, "stream-limit", 20, 20, 10, 10, 10, 10, nil, nil, nil, 20, 1, 10, 1, 10, 1)
	if atLimit.State != "healthy" || !atLimit.EligibleForPromotion {
		t.Fatalf("five percent incomplete streams are inside the configured limit: %+v", atLimit)
	}

	streaming := autoModelRiskFor(model.AutoRoutingTaskCoding, "stream-unstable", 20, 20, 10, 10, 10, 10, nil, nil, nil, 20, 2, 10, 0, 10, 2)
	if streaming.State != "blocked" ||
		!slices.Contains(streaming.Reasons, "model_stream_incomplete_above_5_percent") {
		t.Fatalf("ten percent incomplete streams should block expansion: %+v", streaming)
	}

	slow := make([]float64, autoReadinessMinimumFirstTokenSamples)
	for i := range slow {
		slow[i] = float64(autoReadinessMaxFirstTokenP90Ms + 100)
	}
	latency := autoModelRiskFor(model.AutoRoutingTaskCoding, "slow-model", 10, 10, 5, 5, 5, 5, slow, nil, nil, 0, 0, 0, 0, 0, 0)
	if latency.State != "blocked" ||
		!slices.Contains(latency.Reasons, "model_first_token_p90_above_30_seconds") {
		t.Fatalf("slow first-token tail should block expansion: %+v", latency)
	}

	declining := autoModelRiskFor(model.AutoRoutingTaskCoding, "declining-model", 10, 10, 5, 5, 5, 4, nil, nil, nil, 0, 0, 0, 0, 0, 0)
	if declining.State != "blocked" || declining.SuccessTrend != "declining" ||
		declining.SuccessRateDelta > -autoReadinessMaxSuccessRateDrop ||
		!slices.Contains(declining.Reasons, "model_recent_success_drop_above_10_percent") {
		t.Fatalf("recent success deterioration should block expansion: %+v", declining)
	}

	insufficientTrend := autoModelRiskFor(model.AutoRoutingTaskCoding, "trend-small", 7, 6, 3, 3, 4, 3, nil, nil, nil, 0, 0, 0, 0, 0, 0)
	if insufficientTrend.State != "collect_samples" || insufficientTrend.SuccessTrend != "insufficient" {
		t.Fatalf("small trend halves must not claim deterioration: %+v", insufficientTrend)
	}

	earlierLatency := []float64{200, 200, 200, 200, 200}
	recentLatency := []float64{300, 300, 300, 300, 300}
	degradedLatency := autoModelRiskFor(model.AutoRoutingTaskCoding, "latency-degraded", 10, 10,
		5, 5, 5, 5, append(earlierLatency, recentLatency...), earlierLatency, recentLatency, 0, 0, 0, 0, 0, 0)
	if degradedLatency.State != "blocked" || degradedLatency.FirstTokenTrend != "degrading" ||
		degradedLatency.FirstTokenP90DeltaMs != 100 ||
		!slices.Contains(degradedLatency.Reasons, "model_first_token_p90_rise_above_25_percent") {
		t.Fatalf("recent first-token slowdown should block expansion: %+v", degradedLatency)
	}

	stableLatency := autoModelRiskFor(model.AutoRoutingTaskCoding, "latency-stable", 10, 10,
		5, 5, 5, 5, append(earlierLatency, recentLatency...), earlierLatency, earlierLatency, 0, 0, 0, 0, 0, 0)
	if stableLatency.State != "healthy" || stableLatency.FirstTokenTrend != "stable" {
		t.Fatalf("latency within the trend threshold should pass: %+v", stableLatency)
	}

	insufficientLatency := autoModelRiskFor(model.AutoRoutingTaskCoding, "latency-small", 8, 8,
		4, 4, 4, 4, []float64{200, 200, 200, 200, 300, 300, 300, 300},
		[]float64{200, 200, 200, 200}, []float64{300, 300, 300, 300}, 0, 0, 0, 0, 0, 0)
	if insufficientLatency.FirstTokenTrend != "insufficient" || insufficientLatency.State != "collect_samples" {
		t.Fatalf("small latency halves must not claim degradation: %+v", insufficientLatency)
	}

	degradedStream := autoModelRiskFor(model.AutoRoutingTaskCoding, "stream-trend-degraded", 10, 10,
		5, 5, 5, 5, nil, nil, nil, 20, 2, 10, 0, 10, 2)
	if degradedStream.State != "blocked" || degradedStream.StreamTrend != "degrading" ||
		degradedStream.RecentStreamRate != 0.2 || degradedStream.StreamRateDelta != 0.2 ||
		!slices.Contains(degradedStream.Reasons, "model_recent_stream_incomplete_rise") {
		t.Fatalf("recent stream failures should block expansion: %+v", degradedStream)
	}

	stableStream := autoModelRiskFor(model.AutoRoutingTaskCoding, "stream-trend-stable", 10, 10,
		5, 5, 5, 5, nil, nil, nil, 10, 0, 5, 0, 5, 0)
	if stableStream.State != "healthy" || stableStream.StreamTrend != "stable" {
		t.Fatalf("stable stream completion should pass: %+v", stableStream)
	}
}

func TestAutoModelBandRiskSeparatesLongContextFailures(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "auto-routing-model-bands.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	now := time.Now()
	requests := make([]model.RelayRequest, 0, 15)
	attempts := make([]model.RelayRequestAttempt, 0, 15)
	for i := 0; i < 15; i++ {
		status, promptTokens := "success", int64(1200)
		if i >= 10 {
			status, promptTokens = "failed", int64(20000)
		}
		id := uint64(5510 + i)
		requests = append(requests, model.RelayRequest{
			ID: id, UserID: 7, GroupID: 37, Status: status,
			StartedAt:    now.Add(time.Duration(i) * time.Minute),
			PromptTokens: promptTokens, TargetModel: "model-a",
			AutoTask: "coding", AutoMode: "active",
		})
		attempts = append(attempts, model.RelayRequestAttempt{
			RequestID: id, Round: 1, TargetModel: "model-a", Status: status,
			FirstTokenMs: 300,
			Decision:     `{"reason":"auto_coding","details":"{\"route_action\":\"active\"}"}`,
		})
	}
	if err := db.GetDB().Create(&requests).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().Create(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	summary, err := AutoRoutingAggregate(model.Scope{ID: 7, Role: model.RoleAdmin}, 37, 7)
	if err != nil {
		t.Fatal(err)
	}
	var coding *AutoRoutingReadiness
	for i := range summary.Readiness {
		if summary.Readiness[i].Task == model.AutoRoutingTaskCoding {
			coding = &summary.Readiness[i]
			break
		}
	}
	if coding == nil || len(coding.ModelRisks) != 1 {
		t.Fatalf("coding risk card missing: %+v", coding)
	}
	risk := coding.ModelRisks[0]
	if risk.SuccessRate != 2.0/3.0 || len(risk.BandRisks) != 2 {
		t.Fatalf("total and band evidence mismatch: %+v", risk)
	}
	if risk.BandRisks[0].Band != "short" || risk.BandRisks[0].State != "healthy" ||
		risk.BandRisks[1].Band != "long" || risk.BandRisks[1].State != "blocked" ||
		!slices.Contains(risk.BandRisks[1].Reasons, "band_success_below_80_percent") {
		t.Fatalf("long-context failure should be isolated from healthy short requests: %+v", risk.BandRisks)
	}
	if !slices.Contains(risk.Reasons, "model_input_band_risk_blocks_scale") {
		t.Fatalf("blocked band must be surfaced on the model risk card: %+v", risk)
	}
}

func TestAutoModelBandRiskRequiresEnoughSamples(t *testing.T) {
	small := autoModelBandRiskFor(model.AutoRoutingTaskCoding, "model-a", "long", &autoModelBandEvidence{
		samples: 4, success: 1, promptTokenTotal: 80000,
		promptTokens:        []float64{20000, 20000, 20000, 20000},
		firstTokenDurations: []float64{30000},
		streamStarted:       4, streamIncomplete: 3,
	})
	if small.State != "collect_samples" || small.EligibleForPromotion ||
		!slices.Contains(small.Reasons, "band_sample_below_5") {
		t.Fatalf("a single bad long request must not block expansion: %+v", small)
	}

	healthy := autoModelBandRiskFor(model.AutoRoutingTaskCoding, "model-a", "long", &autoModelBandEvidence{
		samples: 5, success: 4, promptTokenTotal: 100000,
		promptTokens:        []float64{20000, 20000, 20000, 20000, 20000},
		firstTokenDurations: []float64{500, 500, 500, 500, 500},
		streamStarted:       5, streamIncomplete: 0,
	})
	if healthy.State != "healthy" || !healthy.EligibleForPromotion {
		t.Fatalf("healthy long-context evidence should pass: %+v", healthy)
	}
}

func TestAutoModelBandRiskSeparatesContextLengthFailures(t *testing.T) {
	band := autoModelBandRiskFor(model.AutoRoutingTaskCoding, "model-a", "long", &autoModelBandEvidence{
		samples: 5, success: 4, promptTokenTotal: 100000,
		promptTokens:        []float64{20000, 20000, 20000, 20000, 20000},
		firstTokenDurations: []float64{300, 300, 300, 300},
		streamStarted:       4, streamIncomplete: 0,
		errorClasses: map[string]int64{"context": 1},
	})
	if band.State != "blocked" || band.EligibleForPromotion ||
		len(band.ErrorClassCounts) != 1 || band.ErrorClassCounts[0].Reason != "context" ||
		!slices.Contains(band.Reasons, "band_context_length_error") {
		t.Fatalf("context-length failures should expose an effective-window risk: %+v", band)
	}
}

func TestAutoReadinessWilsonAndCostPercentile(t *testing.T) {
	if low := autoWilsonLower(40, 40); low >= 40.0/40.0 {
		t.Fatalf("perfect success needs a conservative lower bound: %.4f", low)
	}
	if low := autoWilsonLower(1, 1); low >= 0.5 {
		t.Fatalf("one sample must not imply broad confidence: %.4f", low)
	}
	if p90 := autoPercentile([]float64{0.8, 1, 1.2, 1.4, 2}, 0.9); p90 < 1.7 || p90 > 2 {
		t.Fatalf("unexpected linear percentile: %.4f", p90)
	}
}

func TestAutoRoutingReadinessUsesActualActiveRequests(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "auto-routing-active.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	now := time.Now()
	requests := []model.RelayRequest{
		{ID: 501, UserID: 7, GroupID: 37, Status: "success", StartedAt: now, AutoTask: "coding", AutoMode: "active"},
		{ID: 502, UserID: 7, GroupID: 37, Status: "success", StartedAt: now, AutoTask: "coding", AutoMode: "active"},
		{ID: 503, UserID: 7, GroupID: 37, Status: "failed", StartedAt: now, AutoTask: "coding", AutoMode: "active"},
	}
	if err := db.GetDB().Create(&requests).Error; err != nil {
		t.Fatal(err)
	}
	attempts := []model.RelayRequestAttempt{
		{RequestID: 501, Round: 1, TargetModel: "model-a", Status: "success", Decision: `{"reason":"auto_coding","details":"{\"route_action\":\"active\"}"}`},
		{RequestID: 502, Round: 1, TargetModel: "model-a", Status: "success", Decision: `{"reason":"auto_coding","details":"{\"route_action\":\"fallback\"}"}`},
		{RequestID: 503, Round: 1, TargetModel: "model-a", Status: "failed", Decision: `{"reason":"auto_coding","details":"{\"route_action\":\"reject\"}"}`},
	}
	if err := db.GetDB().Create(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	summary, err := AutoRoutingAggregate(model.Scope{ID: 7, Role: model.RoleUser}, 37, 7)
	if err != nil {
		t.Fatal(err)
	}
	var coding *AutoRoutingReadiness
	for i := range summary.Readiness {
		if summary.Readiness[i].Task == "coding" {
			coding = &summary.Readiness[i]
			break
		}
	}
	if coding == nil {
		t.Fatal("coding readiness missing")
	}
	if coding.Classified != 3 {
		t.Fatalf("classified request count was not preserved: %+v", coding)
	}
	if coding.ActiveRequests != 1 || coding.ActiveSuccessRate != 1 || coding.ModelTeams != 1 ||
		coding.ModelMinSuccessRate != 1 ||
		math.Abs(coding.ActiveSuccessLow-autoWilsonLower(1, 1)) > 1e-12 {
		t.Fatalf("fallback and rejected requests polluted active evidence: %+v", coding)
	}
}

func TestAutoRoutingModelUsesActualAttemptResultAndFailureScope(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "auto-routing-model-failures.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	now := time.Now()
	requests := []model.RelayRequest{
		{ID: 551, UserID: 7, GroupID: 37, Status: "success", StartedAt: now, TargetModel: "model-a", AutoTask: "coding", AutoMode: "active"},
		{ID: 552, UserID: 7, GroupID: 37, Status: "failed", StartedAt: now.Add(time.Minute), TargetModel: "model-a", AutoTask: "coding", AutoMode: "active"},
		{ID: 553, UserID: 7, GroupID: 37, Status: "failed", StartedAt: now.Add(2 * time.Minute), TargetModel: "model-a", AutoTask: "coding", AutoMode: "active"},
		{ID: 554, UserID: 7, GroupID: 37, Status: "failed", StartedAt: now.Add(3 * time.Minute), TargetModel: "model-a", AutoTask: "coding", AutoMode: "active"},
	}
	if err := db.GetDB().Create(&requests).Error; err != nil {
		t.Fatal(err)
	}
	attempts := []model.RelayRequestAttempt{
		{RequestID: 551, Round: 1, TargetModel: "model-a", Status: "success", Decision: `{"reason":"auto_coding","details":"{\"route_action\":\"active\"}"}`},
		{RequestID: 552, Round: 1, TargetModel: "model-a", Status: "overloaded", Decision: `{"reason":"auto_coding","details":"{\"route_action\":\"active\"}"}`},
		{RequestID: 553, Round: 1, TargetModel: "model-a", Status: "model_missing", Decision: `{"reason":"auto_coding","details":"{\"route_action\":\"active\"}"}`},
		{RequestID: 554, Round: 1, TargetModel: "model-a", Status: "client_error", Decision: `{"reason":"auto_coding","details":"{\"route_action\":\"active\"}"}`},
	}
	if err := db.GetDB().Create(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	summary, err := AutoRoutingAggregate(model.Scope{ID: 7, Role: model.RoleAdmin}, 37, 7)
	if err != nil {
		t.Fatal(err)
	}
	var coding *AutoRoutingReadiness
	for i := range summary.Readiness {
		if summary.Readiness[i].Task == model.AutoRoutingTaskCoding {
			coding = &summary.Readiness[i]
			break
		}
	}
	if coding == nil || len(coding.ModelRisks) != 1 {
		t.Fatalf("coding risk card missing: %+v", coding)
	}
	risk := coding.ModelRisks[0]
	if risk.ActiveRequests != 4 || risk.Success != 1 || risk.Failed != 3 ||
		risk.ChannelFailures != 1 || risk.CandidateFailures != 1 || risk.RequestFailures != 1 ||
		len(risk.ErrorClassCounts) != 3 {
		t.Fatalf("actual failed attempts were not attributed to the model: %+v", risk)
	}
	if risk.ErrorClassCounts[0].Count != 1 ||
		slices.Contains(risk.Reasons, "model_failures_are_candidate_side") {
		t.Fatalf("mixed failure sources must not claim one dominant cause: %+v", risk)
	}
}

func TestAutoRoutingReadinessModelTeamsUseActiveEvidenceOnly(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "auto-routing-model-teams.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	now := time.Now()
	requests := []model.RelayRequest{
		{ID: 601, UserID: 7, GroupID: 37, Status: "failed", StartedAt: now, TargetModel: "shadow-only", AutoTask: "coding", AutoMode: "shadow"},
		{ID: 602, UserID: 7, GroupID: 37, Status: "success", StartedAt: now, TargetModel: "model-a", AutoTask: "coding", AutoMode: "active"},
		{ID: 603, UserID: 7, GroupID: 37, Status: "failed", StartedAt: now, TargetModel: "model-a", AutoTask: "coding", AutoMode: "active"},
	}
	if err := db.GetDB().Create(&requests).Error; err != nil {
		t.Fatal(err)
	}
	attempts := []model.RelayRequestAttempt{
		{RequestID: 601, Round: 1, TargetModel: "shadow-only", Status: "failed", Decision: `{"reason":"auto_coding","details":"{\"route_action\":\"active\"}"}`},
		{RequestID: 602, Round: 1, TargetModel: "model-a", Status: "success", Decision: `{"reason":"auto_coding","details":"{\"route_action\":\"active\"}"}`},
		{RequestID: 603, Round: 1, TargetModel: "model-a", Status: "failed", Decision: `{"reason":"auto_coding","details":"{\"route_action\":\"explore\"}"}`},
	}
	if err := db.GetDB().Create(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	summary, err := AutoRoutingAggregate(model.Scope{ID: 7, Role: model.RoleUser}, 37, 7)
	if err != nil {
		t.Fatal(err)
	}
	var coding *AutoRoutingReadiness
	for i := range summary.Readiness {
		if summary.Readiness[i].Task == "coding" {
			coding = &summary.Readiness[i]
			break
		}
	}
	if coding == nil {
		t.Fatal("coding readiness missing")
	}
	if coding.ModelTeams != 1 || coding.ModelSamples != 2 || coding.ModelMinSuccessRate != 0.5 {
		t.Fatalf("model stability must use only active or explored landings: %+v", coding)
	}
}

func TestAutoRoutingModelRiskUsesTimeOrderedTrend(t *testing.T) {
	now := time.Now()
	samples := make([]autoModelOutcomeSample, 0, 20)
	for i := 0; i < 10; i++ {
		samples = append(samples, autoModelOutcomeSample{StartedAt: now.Add(time.Duration(i) * time.Minute), Success: true})
	}
	for i := 10; i < 20; i++ {
		samples = append(samples, autoModelOutcomeSample{StartedAt: now.Add(time.Duration(i) * time.Minute), Success: i < 15})
	}
	trend := autoSplitOutcomeSamples(samples)
	if trend.EarlierSamples != 10 || trend.EarlierSuccess != 10 || trend.RecentSamples != 10 || trend.RecentSuccess != 5 {
		t.Fatalf("outcome samples were not split by request time: %d/%d then %d/%d",
			trend.EarlierSuccess, trend.EarlierSamples, trend.RecentSuccess, trend.RecentSamples)
	}
	risk := autoModelRiskFor(model.AutoRoutingTaskCoding, "ordered-model", 20, 15,
		trend.EarlierSamples, trend.EarlierSuccess, trend.RecentSamples, trend.RecentSuccess,
		nil, trend.EarlierFirstToken, trend.RecentFirstToken, 0, 0,
		trend.EarlierStreamStarted, trend.EarlierStreamIncomplete,
		trend.RecentStreamStarted, trend.RecentStreamIncomplete)
	if risk.EarlierSuccessRate != 1 || risk.RecentSuccessRate != 0.5 ||
		risk.SuccessRateDelta != -0.5 || risk.SuccessTrend != "declining" || risk.State != "blocked" ||
		!slices.Contains(risk.Reasons, "model_recent_success_drop_above_10_percent") {
		t.Fatalf("time-ordered evidence should expose the recent regression: %+v", risk)
	}
}

func TestAutoRoutingAggregatePerformanceAndInputCostEvidence(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "auto-routing-performance.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	now := time.Now()
	requests := []model.RelayRequest{
		{ID: 701, UserID: 7, GroupID: 37, Status: "success", StartedAt: now, FirstTokenDurationNs: int64(200 * time.Millisecond), PromptTokens: 1200, Cost: 0.01, TargetModel: "model-a", AutoTask: "coding", AutoMode: "active", RouteEvents: `[{"phase":"auto_routing","reason":"coding:item=9"}]`},
		{ID: 702, UserID: 7, GroupID: 37, Status: "failed", StartedAt: now, FirstTokenDurationNs: int64(400 * time.Millisecond), PromptTokens: 2400, Cost: 0.02, TargetModel: "model-a", AutoTask: "coding", AutoMode: "active", RouteEvents: `[{"phase":"auto_routing","reason":"coding:item=9"}]`},
		{ID: 703, UserID: 7, GroupID: 37, Status: "success", StartedAt: now, FirstTokenDurationNs: int64(600 * time.Millisecond), PromptTokens: 20000, Cost: 0.12, TargetModel: "model-a", AutoTask: "coding", AutoMode: "active", RouteEvents: `[{"phase":"auto_routing","reason":"coding:item=9"}]`},
	}
	if err := db.GetDB().Create(&requests).Error; err != nil {
		t.Fatal(err)
	}
	attempts := []model.RelayRequestAttempt{
		{RequestID: 701, Round: 1, TargetModel: "model-a", Status: "success", FirstTokenMs: 200, Decision: `{"reason":"auto_coding","details":"{\"route_action\":\"active\"}"}`},
		{RequestID: 702, Round: 1, TargetModel: "model-a", Status: "overloaded", FirstTokenMs: 400, Decision: `{"reason":"auto_coding","details":"{\"route_action\":\"active\"}"}`},
		{RequestID: 703, Round: 1, TargetModel: "model-a", Status: "success", FirstTokenMs: 600, Decision: `{"reason":"auto_coding","details":"{\"route_action\":\"active\"}"}`},
	}
	if err := db.GetDB().Create(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	summary, err := AutoRoutingAggregate(model.Scope{ID: 7, Role: model.RoleAdmin}, 37, 7)
	if err != nil {
		t.Fatal(err)
	}
	var coding *AutoRoutingTaskSummary
	for i := range summary.Tasks {
		if summary.Tasks[i].Task == "coding" {
			coding = &summary.Tasks[i]
			break
		}
	}
	if coding == nil || coding.FirstTokenSamples != 3 || coding.StreamStarted != 3 ||
		coding.StreamFailedAfterFirst != 1 || math.Abs(coding.StreamIncompleteRate-1.0/3.0) > 1e-12 ||
		coding.P90FirstTokenMs <= coding.AvgFirstTokenMs {
		t.Fatalf("first-token and incomplete-stream evidence: %+v", coding)
	}
	if len(summary.Errors) != 1 || summary.Errors[0].Class != "overload" || summary.Errors[0].Count != 1 {
		t.Fatalf("attempt error classes: %+v", summary.Errors)
	}
	if len(summary.ErrorClassCounts) != 1 || summary.ErrorClassCounts[0].Reason != "overload" {
		t.Fatalf("global error class counts: %+v", summary.ErrorClassCounts)
	}
	if len(summary.CostBands) != 3 {
		t.Fatalf("input length cost bands: %+v", summary.CostBands)
	}
	var codingReadiness *AutoRoutingReadiness
	for i := range summary.Readiness {
		if summary.Readiness[i].Task == "coding" {
			codingReadiness = &summary.Readiness[i]
			break
		}
	}
	if codingReadiness == nil || codingReadiness.StreamStarted != 3 || codingReadiness.StreamIncomplete != 1 {
		t.Fatalf("active stream risk was not included in readiness: %+v", codingReadiness)
	}
	if codingReadiness.ModelEligibleTeams != 0 ||
		len(codingReadiness.ModelRisks) != 1 ||
		codingReadiness.ModelRisks[0].Model != "model-a" ||
		codingReadiness.ModelRisks[0].State != "collect_samples" ||
		codingReadiness.ModelRisks[0].ActiveRequests != 3 ||
		codingReadiness.ModelRisks[0].Success != 2 ||
		codingReadiness.ModelRisks[0].FirstTokenSamples != 3 ||
		codingReadiness.ModelRisks[0].StreamStarted != 3 ||
		codingReadiness.ModelRisks[0].StreamIncomplete != 1 {
		t.Fatalf("active model risk card did not preserve measured evidence: %+v", codingReadiness.ModelRisks)
	}
}

func TestAutoRoutingModelFirstTokenTrendUsesChronologicalActiveEvidence(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "auto-routing-first-token-trend.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	now := time.Now()
	requests := make([]model.RelayRequest, 0, 12)
	attempts := make([]model.RelayRequestAttempt, 0, 12)
	for i := 0; i < 12; i++ {
		firstToken := 200
		if i >= 6 {
			firstToken = 400
		}
		id := uint64(801 + i)
		requests = append(requests, model.RelayRequest{
			ID: id, UserID: 7, GroupID: 37, Status: "success",
			StartedAt:   now.Add(time.Duration(i) * time.Minute),
			TargetModel: "model-a", AutoTask: "coding", AutoMode: "active",
		})
		attempts = append(attempts, model.RelayRequestAttempt{
			RequestID: id, Round: 1, TargetModel: "model-a", Status: "success",
			FirstTokenMs: int64(firstToken),
			Decision:     `{"reason":"auto_coding","details":"{\"route_action\":\"active\"}"}`,
		})
	}
	if err := db.GetDB().Create(&requests).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().Create(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	summary, err := AutoRoutingAggregate(model.Scope{ID: 7, Role: model.RoleAdmin}, 37, 7)
	if err != nil {
		t.Fatal(err)
	}
	var coding *AutoRoutingReadiness
	for i := range summary.Readiness {
		if summary.Readiness[i].Task == model.AutoRoutingTaskCoding {
			coding = &summary.Readiness[i]
			break
		}
	}
	if coding == nil || len(coding.ModelRisks) != 1 {
		t.Fatalf("coding model risk card missing: %+v", coding)
	}
	risk := coding.ModelRisks[0]
	if risk.EarlierFirstTokenSamples != 6 || risk.RecentFirstTokenSamples != 6 ||
		risk.EarlierFirstTokenP90Ms != 200 || risk.RecentFirstTokenP90Ms != 400 ||
		risk.FirstTokenTrend != "degrading" || risk.FirstTokenP90DeltaMs != 200 ||
		!slices.Contains(risk.Reasons, "model_first_token_p90_rise_above_25_percent") {
		t.Fatalf("aggregate did not expose chronological first-token degradation: %+v", risk)
	}
}

func TestAutoRoutingModelStreamTrendUsesChronologicalActiveEvidence(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "auto-routing-stream-trend.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	now := time.Now()
	requests := make([]model.RelayRequest, 0, 12)
	attempts := make([]model.RelayRequestAttempt, 0, 12)
	for i := 0; i < 12; i++ {
		status := "success"
		if i >= 8 {
			status = "failed"
		}
		id := uint64(901 + i)
		requests = append(requests, model.RelayRequest{
			ID: id, UserID: 7, GroupID: 37, Status: status,
			StartedAt:   now.Add(time.Duration(i) * time.Minute),
			TargetModel: "model-a", AutoTask: "coding", AutoMode: "active",
		})
		attempts = append(attempts, model.RelayRequestAttempt{
			RequestID: id, Round: 1, TargetModel: "model-a", Status: status,
			FirstTokenMs: 100,
			Decision:     `{"reason":"auto_coding","details":"{\"route_action\":\"active\"}"}`,
		})
	}
	if err := db.GetDB().Create(&requests).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().Create(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	summary, err := AutoRoutingAggregate(model.Scope{ID: 7, Role: model.RoleAdmin}, 37, 7)
	if err != nil {
		t.Fatal(err)
	}
	var coding *AutoRoutingReadiness
	for i := range summary.Readiness {
		if summary.Readiness[i].Task == model.AutoRoutingTaskCoding {
			coding = &summary.Readiness[i]
			break
		}
	}
	if coding == nil || len(coding.ModelRisks) != 1 {
		t.Fatalf("coding model risk card missing: %+v", coding)
	}
	risk := coding.ModelRisks[0]
	if risk.EarlierStreamStarted != 6 || risk.EarlierStreamIncomplete != 0 ||
		risk.RecentStreamStarted != 6 || risk.RecentStreamIncomplete != 4 ||
		risk.StreamTrend != "degrading" ||
		!slices.Contains(risk.Reasons, "model_recent_stream_incomplete_rise") {
		t.Fatalf("aggregate did not expose chronological stream degradation: %+v", risk)
	}
}

func TestAutoRoutingReadinessIncludesConfiguredCandidateCoverage(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "auto-routing-readiness.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	if err := CapabilitySaveManual(model.ModelCapability{Name: "model-a", Context: 10000}, context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := TaskCapabilitySave(model.ModelTaskCapability{
		ModelName: "model-a", Task: model.AutoRoutingTaskCoding, Score: 90, Verified: true,
	}, context.Background()); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	config := model.DefaultGroupRelayConfigForMode(model.GroupModeScore)
	config.AutoRouting = "shadow"
	config.AutoCoding = "31"
	group, err := GroupCreate(&model.GroupCreateRequest{
		Name: "readiness-group", Mode: model.GroupModeScore, RelayConfig: config,
	}, model.Scope{ID: 7, Role: model.RoleAdmin}, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// GroupCreate cannot accept an item whose grant is not persisted in this isolated
	// fixture. Readiness accounting is still tested directly through its pure helper.
	readiness := autoReadinessFor(model.AutoRoutingTaskCoding, autoReadinessEvidence{
		classified: 20, shadowRequests: 20, compared: 20, same: 20,
		users:                map[uint]bool{1: true, 2: true, 3: true},
		timeBuckets:          map[int64]bool{1: true, 2: true, 3: true, 4: true},
		configuredCandidates: 1, verifiedCandidates: 1, labelingEnabled: true,
	})
	if readiness.State != "ready_for_active" {
		t.Fatalf("verified candidate did not qualify for first ramp: %+v", readiness)
	}
	_ = group
	rows := []model.RelayRequest{
		{ID: 401, UserID: 7, GroupID: group.ID, Status: "success", StartedAt: now, AutoTask: "coding", AutoMode: "shadow"},
	}
	if err := db.GetDB().Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	summary, err := AutoRoutingAggregate(model.Scope{ID: 7, Role: model.RoleAdmin}, group.ID, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Readiness) == 0 {
		t.Fatal("task readiness was not included")
	}
	var coding *AutoRoutingReadiness
	for i := range summary.Readiness {
		if summary.Readiness[i].Task == model.AutoRoutingTaskCoding {
			coding = &summary.Readiness[i]
			break
		}
	}
	if coding == nil || coding.Classified != 1 || coding.State != "collect_shadow" {
		t.Fatalf("unexpected readiness with no configured grant member: %+v", summary.Readiness)
	}
}

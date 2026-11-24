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
	for _, task := range summary.Tasks {
		if task.Task == "coding" && (task.Requests != 2 || task.Success != 1 || task.Failed != 1) {
			t.Fatalf("coding summary: %+v", task)
		}
	}
	if len(summary.Exclusions) != 2 || summary.Exclusions[0].Count != 1 {
		t.Fatalf("exclusions: %+v", summary.Exclusions)
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
	readiness := autoReadinessFor(model.AutoRoutingTaskCoding, autoReadinessEvidence{
		classified: 40, shadowRequests: 40, compared: 35, same: 28,
		configuredCandidates: 2, verifiedCandidates: 2, labelingEnabled: true,
	})
	if readiness.State != "ready_for_active" || readiness.RecommendedRampPercent != 5 {
		t.Fatalf("unexpected readiness: %+v", readiness)
	}

	readiness = autoReadinessFor(model.AutoRoutingTaskCoding, autoReadinessEvidence{
		classified: 10, shadowRequests: 10, configuredCandidates: 2, verifiedCandidates: 2, labelingEnabled: true,
	})
	if readiness.State != "collect_shadow" || readiness.RecommendedRampPercent != 0 {
		t.Fatalf("small sample did not collect shadow: %+v", readiness)
	}

	readiness = autoReadinessFor(model.AutoRoutingTaskCoding, autoReadinessEvidence{
		classified: 240, shadowRequests: 240, compared: 240, same: 158,
		configuredCandidates: 2, verifiedCandidates: 2, labelingEnabled: true,
	})
	if readiness.PredictionAgreement < 0.65 || readiness.PredictionAgreementLow >= 0.60 {
		t.Fatalf("fixture should isolate a confidence lower bound below 60%%: %+v", readiness)
	}
	if readiness.State != "collect_shadow" ||
		!slices.Contains(readiness.Reasons, "prediction_confidence_below_60_percent") {
		t.Fatalf("weak confidence interval should keep collecting: %+v", readiness)
	}
}

func TestAutoRoutingReadinessHoldsAndScalesActiveSafely(t *testing.T) {
	base := autoReadinessEvidence{
		classified: 80, shadowRequests: 80, compared: 70, same: 56,
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
	if coding.ActiveRequests != 1 || coding.ActiveSuccessRate != 1 ||
		math.Abs(coding.ActiveSuccessLow-autoWilsonLower(1, 1)) > 1e-12 {
		t.Fatalf("fallback and rejected requests polluted active evidence: %+v", coding)
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

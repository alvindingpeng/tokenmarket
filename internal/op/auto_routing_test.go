package op

import (
	"path/filepath"
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

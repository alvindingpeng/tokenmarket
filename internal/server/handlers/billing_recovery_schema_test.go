package handlers

import (
	"encoding/json"
	"github.com/alvindingpeng/switchyard/internal/model"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRecoveryTaskJSONHidesSnapshotAndPreservesRequestID(t *testing.T) {
	task := model.BillingSettlementTask{RequestID: 9223372036854775807, Snapshot: "private-price-and-usage"}
	body, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "snapshot") || strings.Contains(string(body), "private-price") {
		t.Fatalf("snapshot leaked: %s", body)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["request_id"] != "9223372036854775807" {
		t.Fatalf("request ID lost precision/type: %s", body)
	}
}

func TestRecoveryOpenAPIIncludesListAndRun(t *testing.T) {
	paths := openAPIAdminPathsOps()
	for path, method := range map[string]string{"/api/v1/ops/billing-recovery/list": "get", "/api/v1/ops/billing-recovery/run": "post"} {
		route, ok := paths[path].(gin.H)
		if !ok {
			t.Fatalf("missing OpenAPI path %s", path)
		}
		operation, ok := route[method].(gin.H)
		if !ok || operation["summary"] == nil {
			t.Fatalf("missing operation %s %s", method, path)
		}
		responses, ok := operation["responses"].(gin.H)
		if !ok || responses["403"] == nil || operation["security"] == nil {
			t.Fatalf("missing authentication or role denial documentation for %s", path)
		}
	}
}

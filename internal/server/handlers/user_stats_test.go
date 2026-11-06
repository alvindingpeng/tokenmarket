package handlers

import (
	"encoding/json"
	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestUserStatsRoutesAuthenticatedScope(t *testing.T) {
	token, otherToken, _, _, user, other, _, _ := ticketAPIEnv(t)
	engine := ticketEngine(t)
	at := time.Date(2026, 10, 29, 12, 0, 0, 0, time.Local)
	rows := []model.BillingRecord{
		{UserID: user.ID, ModelName: "own-model", Status: model.BillingStatusSettled, UserCost: 2, InputToken: 11, CreatedAt: at},
		{UserID: other.ID, ModelName: "other-model", Status: model.BillingStatusSettled, UserCost: 99, InputToken: 999, CreatedAt: at},
	}
	if err := db.GetDB().Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	// 调用日志: 成功率的唯一来源。失败的调用不产生计费行, 只有这张表能反映真实失败;
	// 顺带验证 HTTP 层不会把其他用户的日志算进来。
	requests := []model.RelayRequest{
		{ID: 1, UserID: user.ID, Status: "success", StartedAt: at},
		{ID: 2, UserID: user.ID, Status: "failed", StartedAt: at.Add(time.Minute)},
		{ID: 3, UserID: other.ID, Status: "failed", StartedAt: at.Add(2 * time.Minute)},
		{ID: 4, UserID: other.ID, Status: "failed", StartedAt: at.Add(3 * time.Minute)},
	}
	if err := db.GetDB().Create(&requests).Error; err != nil {
		t.Fatal(err)
	}
	suffix := "?start_date=2026-10-29&end_date=2026-10-29"
	for _, part := range []string{"overview", "trend", "models"} {
		path := "/api/v1/user-stats/" + part
		if w := ticketAPI(t, engine, "GET", path, "", ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous %s: %d", path, w.Code)
		}
		// Query identities cannot override the authenticated user.
		w := ticketAPI(t, engine, "GET", path+suffix+"&user_id=999&role=admin", token, "")
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		switch part {
		case "overview":
			var result model.UserStatsOverview
			if err := json.Unmarshal(payload["data"], &result); err != nil {
				t.Fatal(err)
			}
			if result.TotalRequests != 1 || result.TotalCost != 2 || result.TotalTokens != 11 {
				t.Fatalf("scope: %+v", result)
			}
			// 本人 1 成功 1 失败 → 50%; 他人的两条失败不得拉低本人的成功率。
			if result.SuccessRate != 50 || result.RequestSuccess != 1 || result.RequestFailed != 1 {
				t.Fatalf("success rate scope: %+v", result)
			}
			if result.RequestCoverageStart != "2026-10-29" || result.RequestRetentionDays <= 0 {
				t.Fatalf("coverage: %+v", result)
			}
		case "trend":
			var result []model.UserStatsTrend
			if err := json.Unmarshal(payload["data"], &result); err != nil {
				t.Fatal(err)
			}
			if len(result) != 1 || result[0].Requests != 1 || result[0].Cost != 2 {
				t.Fatalf("scope: %+v", result)
			}
		case "models":
			var result []model.UserStatsModel
			if err := json.Unmarshal(payload["data"], &result); err != nil {
				t.Fatal(err)
			}
			if len(result) != 1 || result[0].ModelName != "own-model" {
				t.Fatalf("scope: %+v", result)
			}
		}
		if w := ticketAPI(t, engine, "GET", path+"?start_date=not-a-date", token, ""); w.Code != 400 {
			t.Fatalf("invalid date: %d", w.Code)
		}
		// 粒度白名单: hour 是本轮新增的合法值, 拼错必须 400 而不是静默回落到按天。
		if part == "trend" {
			if w := ticketAPI(t, engine, "GET", path+"?granularity=hour", token, ""); w.Code != 200 {
				t.Fatalf("hour granularity: %d %s", w.Code, w.Body.String())
			}
			if w := ticketAPI(t, engine, "GET", path+"?granularity=minute", token, ""); w.Code != 400 {
				t.Fatalf("invalid granularity should 400, got %d", w.Code)
			}
		}
	}
	w := ticketAPI(t, engine, "GET", "/api/v1/user-stats/models"+suffix, otherToken, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "other-model") || strings.Contains(w.Body.String(), "own-model") {
		t.Fatalf("other identity: %d %s", w.Code, w.Body.String())
	}
}

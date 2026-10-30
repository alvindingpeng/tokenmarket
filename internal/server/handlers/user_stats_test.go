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
	}
	w := ticketAPI(t, engine, "GET", "/api/v1/user-stats/models"+suffix, otherToken, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "other-model") || strings.Contains(w.Body.String(), "own-model") {
		t.Fatalf("other identity: %d %s", w.Code, w.Body.String())
	}
}

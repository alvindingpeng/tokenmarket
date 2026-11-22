package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-gonic/gin"
)

func TestBalanceAlertHistoryRouteIsAuthenticatedAndUserScoped(t *testing.T) {
	user, session := stepupTestEnv(t)
	second, err := op.UserCreate("balance-history-other", "", "password123", model.RoleUser, model.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	row := model.BalanceAlertHistory{UserID: user.ID, TriggeredAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Source: "balance_alert"}
	if err := db.GetDB().Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	row2 := model.BalanceAlertHistory{UserID: second.ID, TriggeredAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), Source: "balance_alert"}
	if err := db.GetDB().Create(&row2).Error; err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	if err := router.RegisterAll(engine); err != nil {
		t.Fatal(err)
	}
	request := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/user/balance-alert/history", nil)
		if token != "" {
			req.AddCookie(&http.Cookie{Name: "auth", Value: token})
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}
	if w := request(""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d %s", w.Code, w.Body.String())
	}
	w := request(session)
	if w.Code != http.StatusOK {
		t.Fatalf("history: %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Data struct {
			Items []model.BalanceAlertHistory `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.Items) != 1 || body.Data.Items[0].UserID != 0 {
		t.Fatalf("unexpected/unredacted history: %s", w.Body.String())
	}
	if body.Data.Items[0].Source != "balance_alert" {
		t.Fatalf("source missing: %s", w.Body.String())
	}
}

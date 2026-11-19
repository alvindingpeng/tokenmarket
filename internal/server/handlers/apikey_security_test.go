package handlers

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateAPIKeyRejectsCallerChosenSecretBeforeSideEffects(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/create", createAPIKey)
	req := httptest.NewRequest(http.MethodPost, "/create", strings.NewReader(`{"name":"test","api_key":"weak-caller-chosen-secret"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("credential creation response must prohibit caching")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "weak-caller-chosen-secret") {
		t.Fatal("response disclosed rejected secret")
	}
}

package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-gonic/gin"
)

// Exercise actual route registration anonymously, not only the handler function.
func TestPublicStatusPageAnonymousRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	if err := router.RegisterAll(engine); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/status", "/status/"} {
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest("GET", path, nil))
		if recorder.Code != 200 {
			t.Fatalf("%s: %d %s", path, recorder.Code, recorder.Body.String())
		}
		if !strings.Contains(recorder.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("%s must serve HTML", path)
		}
		if !strings.Contains(recorder.Body.String(), "/api/v1/public/channel/stats") {
			t.Fatalf("%s: wrong public template", path)
		}
	}
}

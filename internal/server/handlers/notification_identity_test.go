package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/gin-gonic/gin"
)

func TestNotificationHandlersUseAuthenticatedIntegerIdentity(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "notification-identity.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	for _, uid := range []uint{41, 42} {
		if err := op.NotificationCreate(ctx, uid, model.NotificationTypeTicket, fmt.Sprintf("user-%d", uid), "text", "", 1); err != nil {
			t.Fatal(err)
		}
	}
	own, _, err := op.NotificationList(ctx, 41, model.NotificationListRequest{})
	if err != nil || len(own) != 1 {
		t.Fatalf("seed: %v %+v", err, own)
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("user_id", int(41)); c.Set("role", "user"); c.Next() })
	r.GET("/list", notificationList)
	r.GET("/unread", notificationUnreadCount)
	r.POST("/read", notificationMarkRead)
	r.POST("/all", notificationMarkAllRead)
	r.DELETE("/:id", notificationDelete)
	request := func(method, path, body string) string {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	listed := request("GET", "/list", "")
	if !strings.Contains(listed, "user-41") || strings.Contains(listed, "user-42") {
		t.Fatalf("identity scope: %s", listed)
	}
	if body := request("GET", "/unread", ""); !strings.Contains(body, `"unread_count":1`) {
		t.Fatal(body)
	}
	request("POST", "/read", fmt.Sprintf(`{"ids":[%d]}`, own[0].ID))
	if n, _ := op.NotificationUnreadCount(ctx, 41); n != 0 {
		t.Fatalf("mark read failed: %d", n)
	}
	request("POST", "/all", "")
	request("DELETE", fmt.Sprintf("/%d", own[0].ID), "")
	_, total, err := op.NotificationList(ctx, 41, model.NotificationListRequest{})
	if err != nil || total != 0 {
		t.Fatalf("delete: %d %v", total, err)
	}
	if n, _ := op.NotificationUnreadCount(ctx, 42); n != 1 {
		t.Fatalf("other user changed: %d", n)
	}
}

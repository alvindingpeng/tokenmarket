package op

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

func TestClassifyHealthStatus(t *testing.T) {
	cases := map[int]string{
		http.StatusUnauthorized:        "authentication_failed",
		http.StatusForbidden:           "authorization_failed",
		http.StatusNotFound:            "models_endpoint_not_found",
		http.StatusMethodNotAllowed:    "models_endpoint_method_not_allowed",
		http.StatusInternalServerError: "upstream_http_error",
	}
	for status, want := range cases {
		if got := classifyHealthStatus(status); !strings.HasPrefix(got, want) {
			t.Errorf("status %d: got %q, want prefix %q", status, got, want)
		}
	}
}

func TestHealthProbeRequestUsesCachedKeyAuthWithoutLeakingKey(t *testing.T) {
	const secret = "sk-health-probe-secret"
	target := model.ChannelConfig{BaseURL: "https://api.example.test", OpenAIResponsePath: "/v1/responses"}
	req, err := newOpenAIModelsRequest(context.Background(), target, secret, "", "https://api.example.test/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer "+secret {
		t.Fatalf("authorization header = %q", got)
	}
	if strings.Contains(req.URL.String(), secret) || strings.Contains(classifyHealthStatus(http.StatusUnauthorized), secret) {
		t.Fatal("probe URL or status error leaked channel key")
	}
}

// channelHealthNotificationTestDB 给状态边沿测试提供独立的通知、渠道和告警表。
func channelHealthNotificationTestDB(t *testing.T) {
	t.Helper()
	opTestDBSetup(t, "channel-health-notification.db")
	db.GetDB().Config.DisableForeignKeyConstraintWhenMigrating = true
	if err := db.GetDB().AutoMigrate(&model.Channel{}, &model.ChannelHealthRecord{}, &model.ChannelAlert{}, &model.Notification{}, &model.Webhook{}, &model.WebhookDelivery{}); err != nil {
		t.Fatalf("migrate channel health notification tables: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
}

// TestProbeUnknown 探测「未知」口径: 4xx 与缺凭据不算故障, 5xx/传输层/健康必须参与判定。
func TestProbeUnknown(t *testing.T) {
	cases := []struct {
		name   string
		record model.ChannelHealthRecord
		want   bool
	}{
		{"健康", model.ChannelHealthRecord{StatusCode: 200, Healthy: true}, false},
		{"401 凭据被拒但非故障证据", model.ChannelHealthRecord{StatusCode: 401, Error: "authentication_failed (HTTP 401)"}, true},
		{"403", model.ChannelHealthRecord{StatusCode: 403, Error: "authorization_failed (HTTP 403)"}, true},
		{"404 未实现 models", model.ChannelHealthRecord{StatusCode: 404, Error: "models_endpoint_not_found (HTTP 404)"}, true},
		{"405", model.ChannelHealthRecord{StatusCode: 405, Error: "models_endpoint_method_not_allowed (HTTP 405)"}, true},
		{"无可用凭据", model.ChannelHealthRecord{StatusCode: 0, Error: "no enabled channel key available"}, true},
		{"5xx 真故障", model.ChannelHealthRecord{StatusCode: 502, Error: "upstream_http_error (HTTP 502)"}, false},
		{"传输层失败", model.ChannelHealthRecord{StatusCode: 0, Error: "dial tcp: connection refused"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := probeUnknown(tc.record); got != tc.want {
				t.Fatalf("probeUnknown = %v, want %v (record=%+v)", got, tc.want, tc.record)
			}
		})
	}
}

func TestEvaluateAlertsNotifiesOwnerOnNewDownAndRecoveryTransitions(t *testing.T) {
	defer alertDispatchWait()
	channelHealthNotificationTestDB(t)
	channel := model.Channel{UserID: 51, ChannelConfig: model.ChannelConfig{Name: "通知渠道", BaseURL: "https://example.test"}}
	if err := db.GetDB().Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().Create(&model.ChannelHealthRecord{ChannelID: channel.ID, Healthy: false, Error: "connection refused"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := SettingUpsert(model.SettingKeyAlertFailStreak, "1"); err != nil {
		t.Fatal(err)
	}
	if err := SettingUpsert(model.SettingKeyAlertDedupMinutes, "0"); err != nil {
		t.Fatal(err)
	}
	record := model.ChannelHealthRecord{ChannelID: channel.ID, Healthy: false, Error: "connection refused", CheckedAt: time.Now()}
	evaluateAlerts(context.Background(), channel, record, false)
	alertDispatchWait()
	NotificationDispatchDrain() // 告警派发器触发的站内通知也必须落库后才能断言条数: 排空而非睡 30ms。
	var notifications []model.Notification
	if err := db.GetDB().Where("user_id = ?", channel.UserID).Find(&notifications).Error; err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 1 || notifications[0].Type != model.NotificationTypeChannelDown {
		t.Fatalf("故障边沿通知=%+v, want one channel_down", notifications)
	}
	// 同一故障在阈值内再次被评估时, 告警去重结果不能重复创建通知。
	evaluateAlerts(context.Background(), channel, record, false)
	if err := db.GetDB().Where("user_id = ?", channel.UserID).Find(&notifications).Error; err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 1 {
		t.Fatalf("重复故障不应重复通知, got %d", len(notifications))
	}
	// 将 down 之后写入一条健康探测记录，触发恢复状态边沿。
	if err := db.GetDB().Create(&model.ChannelHealthRecord{ChannelID: channel.ID, Healthy: true, StatusCode: 200}).Error; err != nil {
		t.Fatal(err)
	}
	evaluateAlerts(context.Background(), channel, model.ChannelHealthRecord{ChannelID: channel.ID, Healthy: true, StatusCode: 200, CheckedAt: time.Now()}, false)
	alertDispatchWait()
	NotificationDispatchDrain() // 恢复通知同样排空后再断言, 替代原来的固定 30ms。
	if err := db.GetDB().Where("user_id = ?", channel.UserID).Find(&notifications).Error; err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 2 {
		t.Fatalf("故障恢复应单独产生通知, got %d", len(notifications))
	}
	types := map[model.NotificationType]bool{}
	for _, n := range notifications {
		types[n.Type] = true
	}
	if !types[model.NotificationTypeChannelDown] || !types[model.NotificationTypeChannelRecovered] {
		t.Fatalf("通知类型应包含故障和恢复: %v", types)
	}
	// 最新事件为 recovered 后继续健康探测不得重复产生恢复提醒。
	evaluateAlerts(context.Background(), channel, model.ChannelHealthRecord{ChannelID: channel.ID, Healthy: true, StatusCode: 200, CheckedAt: time.Now()}, false)
	var count int64
	if err := db.GetDB().Model(&model.Notification{}).Where("user_id = ?", channel.UserID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("重复健康探测不应重复恢复通知, got %d", count)
	}
}

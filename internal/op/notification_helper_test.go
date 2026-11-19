package op

import (
	"context"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/alert"
)

// TestNotificationDispatchDrainWaitsForAlertsAndNotifies 钉住 drain 的边界: 告警首发的外发回填
// 和它触发的异步通知都必须完成后才返回, 否则测试切换全局 db 时仍会有后台写入。
func TestNotificationDispatchDrainWaitsForAlertsAndNotifies(t *testing.T) {
	opTestDBSetup(t, "notification-drain.db")
	original := AlertDispatcher
	defer func() { AlertDispatcher = original }()
	started := make(chan struct{})
	release := make(chan struct{})
	AlertDispatcher = func(context.Context, alert.Alert) bool { close(started); <-release; return true }
	if !RaiseAlertFacts(context.Background(), AlertFacts{Kind: "test", Reason: "drain"}) {
		t.Fatal("alert must be recorded")
	}
	<-started
	done := make(chan struct{})
	go func() { NotificationDispatchDrain(); close(done) }()
	select {
	case <-done:
		close(release)
		t.Fatal("drain returned while alert dispatch was blocked")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("drain did not finish after alert dispatch was released")
	}
}

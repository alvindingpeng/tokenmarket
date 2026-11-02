package op

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/alert"
	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// alertTestEnv 建独立库, 返回 context 与清理函数。
func alertTestEnv(t *testing.T) context.Context {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "alert.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	return context.Background()
}

// seedAlert 造一条未送达的告警记录并直接指定排期。
func seedAlert(t *testing.T, kind string, attempts int, next *time.Time) model.ChannelAlert {
	t.Helper()
	record := model.ChannelAlert{
		Kind:           kind,
		Subject:        "测试渠道",
		Reason:         "probe failed",
		Detail:         "渠道: 测试渠道\n状态: HTTP 503",
		NotifyAttempts: attempts,
		NextNotifyAt:   next,
		CreatedAt:      time.Now().Add(-time.Hour),
	}
	if err := db.GetDB().Create(&record).Error; err != nil {
		t.Fatalf("seed alert: %v", err)
	}
	return record
}

// TestNextAlertNotifyDelay 补发退避必须单调递增且有上限, 否则积压时会拖到几天后才重试。
func TestNextAlertNotifyDelay(t *testing.T) {
	cases := []struct {
		attempts int
		want     time.Duration
	}{
		{0, 2 * time.Minute}, // 非法输入按第 1 次处理
		{1, 2 * time.Minute},
		{2, 4 * time.Minute},
		{3, 8 * time.Minute},
		{4, 16 * time.Minute},
	}
	for _, tc := range cases {
		if got := nextAlertNotifyDelay(tc.attempts); got != tc.want {
			t.Errorf("nextAlertNotifyDelay(%d) = %v, want %v", tc.attempts, got, tc.want)
		}
	}
	// 大次数必须收敛到上限而不是溢出成负数。
	for _, attempts := range []int{10, 30, 64, 200} {
		got := nextAlertNotifyDelay(attempts)
		if got != alertRedeliverMaxDelay {
			t.Errorf("nextAlertNotifyDelay(%d) = %v, want cap %v", attempts, got, alertRedeliverMaxDelay)
		}
	}
}

// TestMarkAlertDeliverySuccess 送达成功要回填时刻、清空排期与失败原因。
func TestMarkAlertDeliverySuccess(t *testing.T) {
	ctx := alertTestEnv(t)
	record := seedAlert(t, "down", 2, ptrTime(time.Now()))
	db.GetDB().Model(&model.ChannelAlert{}).Where("id = ?", record.ID).
		Update("last_notify_error", "telegram api 400")

	markAlertDelivery(record.ID, true, nil)

	got := model.ChannelAlert{}
	if err := db.GetDB().Where("id = ?", record.ID).First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if !got.Notified || got.NotifiedAt == nil {
		t.Fatalf("送达后应标记 notified 并记录时刻: %+v", got)
	}
	if got.NextNotifyAt != nil {
		t.Fatalf("送达后应清空补发排期: %v", got.NextNotifyAt)
	}
	if got.LastNotifyError != "" {
		t.Fatalf("送达后应清空失败原因: %q", got.LastNotifyError)
	}
	_ = ctx
}

// TestMarkAlertDeliveryFailureSchedulesRetry 失败要累计次数、记原因并排下一次。
func TestMarkAlertDeliveryFailureSchedulesRetry(t *testing.T) {
	alertTestEnv(t)
	record := seedAlert(t, "down", 0, nil)

	markAlertDelivery(record.ID, false, errors.New("telegram api 400: can't parse entities"))

	got := model.ChannelAlert{}
	db.GetDB().Where("id = ?", record.ID).First(&got)
	if got.NotifyAttempts != 1 {
		t.Fatalf("失败应累计尝试次数, got %d", got.NotifyAttempts)
	}
	if got.Notified {
		t.Fatal("失败不应标记为已送达")
	}
	if got.NextNotifyAt == nil {
		t.Fatal("失败后应排出下一次补发时间")
	}
	if got.LastNotifyError == "" {
		t.Fatal("失败必须留下原因")
	}
	// 排期应在未来且大致等于首次退避(base)量级。
	if wait := time.Until(*got.NextNotifyAt); wait <= 0 || wait > alertRedeliverBase+time.Minute {
		t.Fatalf("首次补发退避异常: %v", wait)
	}
}

// TestMarkAlertDeliveryGivesUpAtLimit 达到上限后不再排期, 但保留记录与原因。
func TestMarkAlertDeliveryGivesUpAtLimit(t *testing.T) {
	alertTestEnv(t)
	record := seedAlert(t, "down", alertRedeliverMaxAttempts-1, ptrTime(time.Now()))

	markAlertDelivery(record.ID, false, errors.New("still failing"))

	got := model.ChannelAlert{}
	db.GetDB().Where("id = ?", record.ID).First(&got)
	if got.NotifyAttempts != alertRedeliverMaxAttempts {
		t.Fatalf("attempts = %d, want %d", got.NotifyAttempts, alertRedeliverMaxAttempts)
	}
	if got.NextNotifyAt != nil {
		t.Fatalf("达到上限后不应再排期: %v", got.NextNotifyAt)
	}
	if got.LastNotifyError == "" {
		t.Fatal("放弃时必须保留最后失败原因")
	}
}

// TestAlertRedeliverPendingDeliversDueAlerts 只补发到点的记录, 成功者标记送达。
func TestAlertRedeliverPendingDeliversDueAlerts(t *testing.T) {
	alertTestEnv(t)
	past := time.Now().Add(-time.Minute)
	due := seedAlert(t, "down", 1, &past)
	// 还没到点的记录本轮不该被动到。
	future := time.Now().Add(time.Hour)
	notDue := seedAlert(t, "degraded", 1, &future)

	var delivered []string
	restore := swapDispatcher(t, func(_ context.Context, a alert.Alert) (bool, error) {
		delivered = append(delivered, a.Kind)
		return true, nil
	})
	defer restore()

	sent, gaveUp, err := AlertRedeliverPending(context.Background())
	if err != nil {
		t.Fatalf("redeliver: %v", err)
	}
	if sent != 1 || gaveUp != 0 {
		t.Fatalf("sent=%d gaveUp=%d, want 1/0", sent, gaveUp)
	}
	if len(delivered) != 1 || delivered[0] != "down" {
		t.Fatalf("只应补发到点的那条 down, got %v", delivered)
	}
	got := model.ChannelAlert{}
	db.GetDB().Where("id = ?", due.ID).First(&got)
	if !got.Notified {
		t.Fatal("补发成功后应标记送达")
	}
	untouched := model.ChannelAlert{}
	db.GetDB().Where("id = ?", notDue.ID).First(&untouched)
	if untouched.Notified {
		t.Fatal("未到点的记录不应被补发")
	}
}

// TestAlertRedeliverPendingRecordsFailureReason 补发失败要留下可查原因。
func TestAlertRedeliverPendingRecordsFailureReason(t *testing.T) {
	alertTestEnv(t)
	past := time.Now().Add(-time.Minute)
	record := seedAlert(t, "down", 1, &past)

	restore := swapDispatcher(t, func(_ context.Context, _ alert.Alert) (bool, error) {
		return false, errors.New("telegram api 400: can't parse entities")
	})
	defer restore()

	if _, _, err := AlertRedeliverPending(context.Background()); err != nil {
		t.Fatalf("redeliver: %v", err)
	}
	got := model.ChannelAlert{}
	db.GetDB().Where("id = ?", record.ID).First(&got)
	if got.Notified {
		t.Fatal("失败不应标记送达")
	}
	if got.LastNotifyError == "" {
		t.Fatal("补发失败必须留下原因")
	}
	if got.NextNotifyAt == nil {
		t.Fatal("补发失败应继续排期")
	}
}

// TestAlertRedeliverSkipsWithoutDispatcher 未接线时如实报错, 不假装补发成功。
func TestAlertRedeliverSkipsWithoutDispatcher(t *testing.T) {
	alertTestEnv(t)
	restore := swapDispatcher(t, nil)
	defer restore()
	if _, _, err := AlertRedeliverPending(context.Background()); err == nil {
		t.Fatal("未接线时应返回错误")
	}
}

// TestAlertDeliveryStats 概览要能区分待补发与已放弃。
func TestAlertDeliveryStats(t *testing.T) {
	alertTestEnv(t)
	future := time.Now().Add(time.Hour)
	seedAlert(t, "down", 1, &future)                         // 待补发
	seedAlert(t, "degraded", alertRedeliverMaxAttempts, nil) // 已放弃
	delivered := seedAlert(t, "recovered", 1, nil)
	db.GetDB().Model(&model.ChannelAlert{}).Where("id = ?", delivered.ID).
		Updates(map[string]any{"notified": true, "notified_at": time.Now()})

	stats, err := AlertDeliveryStatsGet(context.Background())
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Pending != 1 {
		t.Errorf("pending = %d, want 1", stats.Pending)
	}
	if stats.GaveUp != 1 {
		t.Errorf("gaveUp = %d, want 1", stats.GaveUp)
	}
	if stats.Delivered24h != 1 {
		t.Errorf("delivered24h = %d, want 1", stats.Delivered24h)
	}
}

// TestAlertUndeliveredListOrdersBySchedule 未送达列表按排期排序, 已放弃排最后。
func TestAlertUndeliveredListOrdersBySchedule(t *testing.T) {
	alertTestEnv(t)
	soon := time.Now().Add(time.Minute)
	later := time.Now().Add(time.Hour)
	seedAlert(t, "down", 1, &later)
	seedAlert(t, "degraded", 1, &soon)
	seedAlert(t, "test", alertRedeliverMaxAttempts, nil) // 已放弃

	rows, err := AlertUndeliveredList(context.Background(), 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 undelivered, got %d", len(rows))
	}
	if rows[0].Kind != "degraded" {
		t.Errorf("最早排期的应在最前, got %s", rows[0].Kind)
	}
	if rows[len(rows)-1].NextNotifyAt != nil {
		t.Errorf("已放弃的应排最后, got %s", rows[len(rows)-1].Kind)
	}
}

// swapDispatcher 替换细致分发器, 返回还原函数。nil 表示未接线。
func swapDispatcher(t *testing.T, fn func(context.Context, alert.Alert) (bool, error)) func() {
	t.Helper()
	origDetailed := AlertDispatcherDetailed
	origPlain := AlertDispatcher
	AlertDispatcherDetailed = fn
	AlertDispatcher = nil
	return func() {
		AlertDispatcherDetailed = origDetailed
		AlertDispatcher = origPlain
	}
}

// ptrTime 取时间指针, 便于构造排期。
func ptrTime(t time.Time) *time.Time { return &t }

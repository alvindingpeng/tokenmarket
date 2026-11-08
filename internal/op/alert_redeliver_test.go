package op

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/alert"
	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// alertTestEnv 建独立库, 返回 context 与清理函数。
func alertTestEnv(t *testing.T) context.Context {
	t.Helper()
	opTestDBSetup(t, "alert.db")
	// 播种默认设置: 补发窗口从设置表读, 不播种时把窗口改成 0 的用例写不进去
	// (SettingSetString 报 setting not found), 窗口关闭这条路径就测不到。
	for _, seed := range model.DefaultSettings() {
		if err := SettingUpsert(seed.Key, seed.Value); err != nil {
			t.Fatalf("seed setting %s: %v", seed.Key, err)
		}
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

// TestAlertDeliveryStatsCountsExpiredSeparately 看板三态必须覆盖全部未送达无排期的行。
//
// 线上坑: 补发窗口上线后, 几百条"从未尝试、无排期、超出窗口"的历史告警既不计入 Pending
// (要求有排期) 也不计入 GaveUp(要求达到上限), 看板上完全不存在, 却在未送达列表里
// 显示成"已放弃" —— 前后端口径自相矛盾。Expired 就是补上的这一格。
func TestAlertDeliveryStatsCountsExpiredSeparately(t *testing.T) {
	alertTestEnv(t)
	soon := time.Now().Add(time.Minute)
	seedAlert(t, "down", 1, &soon) // 待补发

	// 超窗: 未送达 + 无排期 + 从未尝试 + 创建于 72 小时前。
	if err := db.GetDB().Create(&model.ChannelAlert{
		Kind: "degraded", Subject: "旧渠道", Reason: "probe failed",
		NotifyAttempts: 0, NextNotifyAt: nil, CreatedAt: time.Now().Add(-72 * time.Hour),
	}).Error; err != nil {
		t.Fatal(err)
	}
	// 已放弃: 达到上限。
	seedAlert(t, "test", alertRedeliverMaxAttempts, nil)

	stats, err := AlertDeliveryStatsGet(context.Background())
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Pending != 1 {
		t.Errorf("pending = %d, want 1", stats.Pending)
	}
	if stats.Expired != 1 {
		t.Errorf("expired = %d, want 1 (超窗历史告警必须单独可见)", stats.Expired)
	}
	if stats.GaveUp != 1 {
		t.Errorf("gave_up = %d, want 1", stats.GaveUp)
	}
	if stats.RedeliverWindowHours != alertRedeliverDefaultWindowHours {
		t.Errorf("window = %d, want %d", stats.RedeliverWindowHours, alertRedeliverDefaultWindowHours)
	}
}

// TestAlertDeliveryStatsWindowZeroDisablesExpired 窗口设为 0(不限) 时不存在"超窗"这一类,
// 不能把全部历史告警算成 Expired —— 那会让看板显示一个永远不会被处理的巨大数字。
func TestAlertDeliveryStatsWindowZeroDisablesExpired(t *testing.T) {
	alertTestEnv(t)
	if err := SettingSetString(model.SettingKeyAlertRedeliverWindow, "0"); err != nil {
		t.Fatalf("set window: %v", err)
	}
	if err := db.GetDB().Create(&model.ChannelAlert{
		Kind: "degraded", Subject: "旧渠道", Reason: "probe failed",
		NotifyAttempts: 0, CreatedAt: time.Now().Add(-720 * time.Hour),
	}).Error; err != nil {
		t.Fatal(err)
	}
	stats, err := AlertDeliveryStatsGet(context.Background())
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Expired != 0 {
		t.Errorf("窗口关闭时 expired 应为 0, got %d", stats.Expired)
	}
	if stats.RedeliverWindowHours != 0 {
		t.Errorf("window = %d, want 0", stats.RedeliverWindowHours)
	}
}

// TestAlertRedeliverPendingSkipsExpired 补发任务本身也必须遵守窗口:
// 即使某条超窗告警被排了期, 也不应再外发(它的语境已经过去了)。
func TestAlertRedeliverPendingSkipsExpired(t *testing.T) {
	alertTestEnv(t)
	// 注入 dispatcher: 未注入时函数会在过滤之前就报错, 那样测不到窗口逻辑。
	restore := swapDispatcher(t, func(context.Context, alert.Alert) (bool, error) { return true, nil })
	defer restore()
	due := time.Now().Add(-time.Minute)
	// 超窗但已排期: 不应被补发。
	if err := db.GetDB().Create(&model.ChannelAlert{
		Kind: "down", Subject: "旧渠道", Reason: "probe failed",
		NotifyAttempts: 1, NextNotifyAt: &due, CreatedAt: time.Now().Add(-72 * time.Hour),
	}).Error; err != nil {
		t.Fatal(err)
	}
	sent, gaveUp, err := AlertRedeliverPending(context.Background())
	if err != nil {
		t.Fatalf("redeliver: %v", err)
	}
	if sent != 0 || gaveUp != 0 {
		t.Fatalf("超窗告警不应被补发, sent=%d gaveUp=%d", sent, gaveUp)
	}
}

// TestAlertRequeueReschedulesExpired 手动放行: 超窗告警被重新排期, 计数清零, 且能真的被补发。
func TestAlertRequeueReschedulesExpired(t *testing.T) {
	alertTestEnv(t)
	sentAlerts := &[]alert.Alert{}
	restore := swapDispatcher(t, func(_ context.Context, a alert.Alert) (bool, error) {
		*sentAlerts = append(*sentAlerts, a)
		return true, nil
	})
	defer restore()

	old := model.ChannelAlert{
		Kind: "down", Subject: "旧渠道", Reason: "probe failed",
		NotifyAttempts: alertRedeliverMaxAttempts, CreatedAt: time.Now().Add(-72 * time.Hour),
		LastNotifyError: "dial tcp: connection refused",
	}
	if err := db.GetDB().Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	if err := AlertRequeue(context.Background(), old.ID); err != nil {
		t.Fatalf("requeue: %v", err)
	}

	reloaded := model.ChannelAlert{}
	if err := db.GetDB().Where("id = ?", old.ID).First(&reloaded).Error; err != nil {
		t.Fatal(err)
	}
	if reloaded.NotifyAttempts != 0 {
		t.Errorf("requeue 应清零尝试次数(重新给足重试预算), got %d", reloaded.NotifyAttempts)
	}
	if reloaded.NextNotifyAt == nil {
		t.Fatal("requeue 后应有排期")
	}
	if reloaded.LastNotifyError != "" {
		t.Errorf("requeue 应清除旧的失败原因, got %q", reloaded.LastNotifyError)
	}

	// 放行之后, 补发任务要真的能取到它 —— 否则"手动放行"只是改了个字段。
	// 窗口仍会挡它(72 小时前), 所以这里临时把窗口关掉, 验证放行链路是通的。
	if err := SettingSetString(model.SettingKeyAlertRedeliverWindow, "0"); err != nil {
		t.Fatal(err)
	}
	sent, _, err := AlertRedeliverPending(context.Background())
	if err != nil {
		t.Fatalf("redeliver: %v", err)
	}
	if sent != 1 {
		t.Fatalf("放行后应补发 1 条, got %d", sent)
	}
	if len(*sentAlerts) != 1 {
		t.Fatalf("dispatcher 应收到 1 条, got %d", len(*sentAlerts))
	}
}

// TestAlertRequeueRejectsDelivered 已送达的告警不允许重排, 否则等于制造重复通知。
func TestAlertRequeueRejectsDelivered(t *testing.T) {
	alertTestEnv(t)
	delivered := seedAlert(t, "down", 1, nil)
	if err := db.GetDB().Model(&model.ChannelAlert{}).Where("id = ?", delivered.ID).Update("notified", true).Error; err != nil {
		t.Fatal(err)
	}
	if err := AlertRequeue(context.Background(), delivered.ID); err == nil {
		t.Fatal("已送达的告警不应被重新排期")
	}
}

// TestAlertRequeueMissingID 不存在的 id 必须明确报错, 不能静默成功。
func TestAlertRequeueMissingID(t *testing.T) {
	alertTestEnv(t)
	if err := AlertRequeue(context.Background(), 999999); err == nil {
		t.Fatal("不存在的告警应报错")
	}
}

// TestAlertWithinWindow 窗口判定: 边界与"0 表示不限"。
func TestAlertWithinWindow(t *testing.T) {
	if !alertWithinWindow(time.Now().Add(-time.Hour), 48*time.Hour) {
		t.Error("1 小时前的告警应在 48h 窗口内")
	}
	if alertWithinWindow(time.Now().Add(-72*time.Hour), 48*time.Hour) {
		t.Error("72 小时前的告警不应在 48h 窗口内")
	}
	if !alertWithinWindow(time.Now().Add(-720*time.Hour), 0) {
		t.Error("窗口为 0 表示不限, 任何时间都应放行")
	}
}

// TestNormalizeFailureReason 归一必须让"同一故障的不同实例"汇聚成一条。
//
// 真实数据里同一种故障的文本几乎每次都不同(HTTP 状态码、字节偏移、端口), 原文分组
// 会把一个系统性问题拆成几十条各出现一次, 排序后什么也看不出来。
func TestNormalizeFailureReason(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"字节偏移归一", "telegram: telegram api 400: Bad Request: can't parse entities: Can't find end", "telegram: telegram api 400: Bad Request: can't parse entities: Can't find end"},
		{"只留首行", "webhook: connection refused\nrequest: POST /x\nretry: 1", "webhook: connection refused"},
		{"首句截断", "feishu: code=19024. detail: key words not found", "feishu: code=19024."},
		{"空串", "   ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeFailureReason(tc.raw); got != tc.want {
				t.Fatalf("normalize(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}

	// 超长文本必须被截断, 否则一行"原因"会把界面撑破。
	long := strings.Repeat("x", 400)
	if got := normalizeFailureReason(long); len(got) > alertFailureReasonMaxLen+3 {
		t.Fatalf("超长原因未截断, len=%d", len(got))
	}
}

// TestTopFailureReasons 排序稳定: 次数相同时按文本排序, 不能每次刷新换顺序。
func TestTopFailureReasons(t *testing.T) {
	counts := map[string]int64{"b": 2, "a": 2, "c": 5, "zero": 0, "": 3}
	result := topFailureReasons(counts, 2)
	if len(result) != 2 {
		t.Fatalf("want 2 items, got %d", len(result))
	}
	if result[0].Reason != "c" || result[0].Count != 5 {
		t.Fatalf("最高次数应排最前, got %+v", result[0])
	}
	if result[1].Reason != "a" {
		t.Fatalf("次数相同时应按文本排序, got %q", result[1].Reason)
	}

	// 零次与空原因不得出现在结果里。
	for _, item := range topFailureReasons(counts, 10) {
		if item.Count == 0 || item.Reason == "" {
			t.Fatalf("结果不应包含空原因或零次数: %+v", item)
		}
	}
}

// TestAlertDeliveryStatsAggregatesFailureReasons 端到端: 24 小时内的失败原因要聚合出来,
// 且同一种故障的不同文本实例必须合并计数。
func TestAlertDeliveryStatsAggregatesFailureReasons(t *testing.T) {
	alertTestEnv(t)
	now := time.Now()
	seed := func(reason string) {
		if err := db.GetDB().Create(&model.ChannelAlert{
			Kind: "down", Subject: "渠道A", Reason: "probe failed",
			LastNotifyError: reason, CreatedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	seed("telegram: 400 can't parse entities: byte offset 275")
	seed("telegram: 400 can't parse entities: byte offset 512")
	seed("webhook: connection refused")
	// 超过 24 小时的失败不应进入聚合。
	if err := db.GetDB().Create(&model.ChannelAlert{
		Kind: "down", Subject: "渠道A", Reason: "probe failed",
		LastNotifyError: "telegram: 400 can't parse entities: byte offset 999",
		CreatedAt:       now.Add(-48 * time.Hour),
	}).Error; err != nil {
		t.Fatal(err)
	}

	stats, err := AlertDeliveryStatsGet(context.Background())
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if len(stats.FailureReasons) != 2 {
		t.Fatalf("want 2 aggregated reasons, got %+v", stats.FailureReasons)
	}
	if stats.FailureReasons[0].Count != 2 {
		t.Fatalf("相同故障应合并计数, got %+v", stats.FailureReasons[0])
	}
	if !strings.Contains(stats.FailureReasons[0].Reason, "can't parse entities") {
		t.Fatalf("聚合原因应是解析失败: %q", stats.FailureReasons[0].Reason)
	}
}

package op

import (
	"context"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/alert"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 告警外发与主体解析的回归测试。
// 这里曾是静默失效点: sendMultiChannelAlert 是空占位函数, 配好的 Telegram/Email 一条都发不出去,
// 而设置页的连通性测试却是通的, 故障只会在"该收到告警时收不到"时才暴露。

func TestSendMultiChannelAlertUsesInjectedDispatcher(t *testing.T) {
	original := AlertDispatcher
	defer func() { AlertDispatcher = original }()

	var got alert.Alert
	calls := 0
	AlertDispatcher = func(_ context.Context, a alert.Alert) bool {
		calls++
		got = a
		return true
	}

	out := alert.Alert{
		Event:     alert.EventBalanceLow,
		Kind:      "low_balance",
		Scope:     "生产主渠道",
		Message:   "user balance is low",
		Fields:    []alert.Field{{Label: "可用余额", Value: "1.20 元"}},
		Timestamp: time.Unix(1700000000, 0),
	}
	if ok := sendMultiChannelAlert(context.Background(), out); !ok {
		t.Fatalf("分发成功但返回值未透传")
	}
	if calls != 1 {
		t.Fatalf("分发器调用次数 = %d, want 1", calls)
	}
	// 结构化事实必须原样送达: 中间任何"重新拼装"都会把渠道名或明细丢掉。
	if got.Event != alert.EventBalanceLow || got.Kind != out.Kind || got.Scope != out.Scope {
		t.Fatalf("分发内容被改写: event=%q kind=%q scope=%q", got.Event, got.Kind, got.Scope)
	}
	if len(got.Fields) != 1 || got.Fields[0].Label != "可用余额" {
		t.Fatalf("明细行丢失: %+v", got.Fields)
	}
}

// TestSendMultiChannelAlertReportsFailure 校验分发失败如实返回 false。
// 该返回值决定 notified 标记是否回填, 谎报成功会让界面显示"已通知"而实际没发出去。
func TestSendMultiChannelAlertReportsFailure(t *testing.T) {
	original := AlertDispatcher
	defer func() { AlertDispatcher = original }()
	AlertDispatcher = func(context.Context, alert.Alert) bool { return false }

	if ok := sendMultiChannelAlert(context.Background(), alert.Alert{Event: alert.EventChannelFailed}); ok {
		t.Fatalf("分发失败却返回 true")
	}
}

// TestSendMultiChannelAlertWithoutDispatcherIsSafe 校验未接线时静默跳过而不是 panic。
// 单测与未经 cmd 启动的进程都不该因为告警外发未接线而崩。
func TestSendMultiChannelAlertWithoutDispatcherIsSafe(t *testing.T) {
	original := AlertDispatcher
	defer func() { AlertDispatcher = original }()
	AlertDispatcher = nil

	if ok := sendMultiChannelAlert(context.Background(), alert.Alert{Event: alert.EventChannelFailed}); ok {
		t.Fatalf("未接线却报告发送成功")
	}
}

// TestDetermineAlertEvent 校验 kind 到事件名的映射: 未知 kind 不能掉成空串,
// 也不能伪装成 channel_degraded —— 改造后"备份失败"曾在消费端被当成"渠道延迟降级"处理。
func TestDetermineAlertEvent(t *testing.T) {
	cases := map[string]string{
		"down":           "channel_failed",
		"recovered":      "channel_recovered",
		"degraded":       "channel_degraded",
		"key_disabled":   "channel_key_disabled",
		"model_disabled": "channel_model_disabled",
		"low_balance":    "balance_low",
		"quota_exceeded": "quota_exceeded",
		"mismatch":       "billing_mismatch",
		"billing_failed": "billing_failed",
		"backup_failed":  "backup_failed",
		"archive_failed": "archive_failed",
		"task_failed":    "task_failed",
		"task_recovered": "task_recovered",
		"test":           "test",
		"some_new_kind":  "system_alert",
	}
	for kind, want := range cases {
		if got := determineAlertEvent(kind); got != want {
			t.Fatalf("determineAlertEvent(%q) = %q, want %q", kind, got, want)
		}
	}
	// 事件名必须是 alert 包认识的常量, 否则外发 payload 会带一个没人解释得了的值。
	known := map[string]bool{}
	for _, e := range []alert.Event{
		alert.EventChannelFailed, alert.EventChannelRecovered, alert.EventChannelDegraded,
		alert.EventChannelKeyDisabled, alert.EventChannelModelDisabled, alert.EventBalanceLow,
		alert.EventQuotaExceeded, alert.EventBillingMismatch, alert.EventBillingFailed,
		alert.EventBackupFailed, alert.EventArchiveFailed, alert.EventTaskFailed,
		alert.EventTaskRecovered, alert.EventUpdateAvailable, alert.EventTest, alert.EventSystemAlert,
	} {
		known[string(e)] = true
	}
	for _, want := range cases {
		if !known[want] {
			t.Fatalf("事件名 %q 未在 alert 包登记", want)
		}
	}
}

// alertSubjectTestDB 建一个内存库, 只迁移告警链路用到的表。
func alertSubjectTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := conn.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1) // 内存库单连接, 避免各协程拿到独立实例。
	if err := conn.AutoMigrate(&model.Channel{}, &model.ChannelAlert{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return conn
}

// TestResolveSubjectUsesRealChannelName 钉住本次告警改造的核心诉求:
// 管理员看到的是渠道名, 不是发布时生成的随机共享码。
func TestResolveSubjectUsesRealChannelName(t *testing.T) {
	conn := alertSubjectTestDB(t)
	channel := model.Channel{
		ShareCode: "8fKd2xQ",
		ChannelConfig: model.ChannelConfig{
			Name:    "生产主渠道",
			BaseURL: "http://up",
		},
	}
	if err := conn.Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	channelCache.Set(channel.ID, channel)

	defer channelCache.Del(channel.ID)

	subject, shareCode := resolveSubject(channel.ID, "")
	if subject != "生产主渠道" {
		t.Fatalf("主体应为真实渠道名, got %q", subject)
	}
	if shareCode != "8fKd2xQ" {
		t.Fatalf("共享编码仍应进 ChannelCode 列, got %q", shareCode)
	}

	// 旧调用把共享码当主体传进来, 同样换成渠道名: 两处调用点(健康探测/告警列表)不必一起改也不会漏。
	subject, _ = resolveSubject(channel.ID, "8fKd2xQ")
	if subject != "生产主渠道" {
		t.Fatalf("ShareCode 作为主体时应被换成渠道名, got %q", subject)
	}

	// 渠道已删除: 退化成 ID 而不是留空, 免得告警变成"未知主体"。
	subject, shareCode = resolveSubject(4242, "")
	if subject != "渠道 #4242" || shareCode != "" {
		t.Fatalf("缺失渠道应退化, got subject=%q code=%q", subject, shareCode)
	}

	// 系统级告警: "system" 是历史占位值, 一律显示为中文"系统"。
	if got, _ := resolveSubject(0, "system"); got != "系统" {
		t.Fatalf("系统级主体应为\"系统\", got %q", got)
	}
	if got, _ := resolveSubject(0, ""); got != "系统" {
		t.Fatalf("空主体应落回\"系统\", got %q", got)
	}
}

// TestErrorClassLabel 校验上游错误分类的中文说明: 熔断告警里管理员不该看到裸的 "auth"。
func TestErrorClassLabel(t *testing.T) {
	if got := errorClassLabel("auth"); got == "" || got == "auth" {
		t.Fatalf("auth 应有中文说明, got %q", got)
	}
	// 未知分类原样返回, 便于发现 relay 新增而未登记的值。
	if got := errorClassLabel("brand_new_class"); got != "brand_new_class" {
		t.Fatalf("未知分类应原样返回, got %q", got)
	}
	if got := errorClassLabel(""); got != "未分类错误" {
		t.Fatalf("空分类应说明为未分类, got %q", got)
	}
}

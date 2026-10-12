package op

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/alert"
	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// raiseTestDB 初始化一个临时 SQLite 库, 让"产生 -> 去重 -> 落库 -> 外发"这条真链路在单测里跑通。
// 与 alertSubjectTestDB 不同, 这里必须走 db.InitDB: RaiseAlertFacts 直接读 db.GetDB(), 自建连接注入不进去。
func raiseTestDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "raise.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
}

// captureAlertDispatcher 把外发接到测试通道上并返回接收端。
// 外发在独立协程里进行(不跟着请求 ctx 一起被取消), 因此用带缓冲通道取, 顺序无关。
func captureAlertDispatcher(t *testing.T) chan alert.Alert {
	t.Helper()
	original := AlertDispatcher
	out := make(chan alert.Alert, 8)
	AlertDispatcher = func(_ context.Context, a alert.Alert) bool {
		out <- a
		return true
	}
	t.Cleanup(func() { AlertDispatcher = original })
	return out
}

// TestTaskFailureAlertDedupsWithinWindow 钉住任务告警最关键的一条约束:
// 后台任务连续失败达到阈值后每一轮都会回调一次, 去重键里绝不能掺入次数或错误原文 ——
// 否则第 4、5、6 次失败各生成一条新告警, 十分钟之内把管理员的手机刷爆。
func TestTaskFailureAlertDedupsWithinWindow(t *testing.T) {
	raiseTestDB(t)
	captureAlertDispatcher(t)

	RaiseTaskFailureAlert("auto_backup", 3, "disk full")
	RaiseTaskFailureAlert("auto_backup", 4, "disk full while copying")
	RaiseTaskFailureAlert("auto_backup", 5, "permission denied")

	var rows []model.ChannelAlert
	if err := db.GetDB().Find(&rows).Error; err != nil {
		t.Fatalf("query alerts: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("同一任务三轮失败应只落一条告警, got %d", len(rows))
	}
	row := rows[0]
	if row.Kind != "task_failed" || row.Subject != "系统" {
		t.Fatalf("任务告警应记为系统级 task_failed, got kind=%q subject=%q", row.Kind, row.Subject)
	}
	// 明细给的是管理员看得懂的内容: 中文名, 原始标识, 次数, 错误原文, 影响与建议。
	for _, want := range []string{"任务: 自动备份", "任务标识: auto_backup", "连续失败: 3 次", "最近错误: disk full", "影响: 数据库没有新备份", "建议:"} {
		if !strings.Contains(row.Detail, want) {
			t.Fatalf("明细缺少 %q:\n%s", want, row.Detail)
		}
	}

	// 另一个任务失败是另一件事, 不能被上一个任务的去重窗口吞掉。
	RaiseTaskFailureAlert("stats_save", 3, "database is locked")
	if err := db.GetDB().Find(&rows).Error; err != nil {
		t.Fatalf("query alerts: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("不同任务应各自成一条, got %d", len(rows))
	}
}

// TestTaskRecoveredAlertIsDistinct 校验恢复告警不与故障告警共用去重键:
// 两者 kind 与 reason 都不同, 否则"恢复了"这条会被前面的"失败了"吃掉。
func TestTaskRecoveredAlertIsDistinct(t *testing.T) {
	raiseTestDB(t)
	captureAlertDispatcher(t)

	RaiseTaskFailureAlert("channel_health", 3, "probe panic")
	RaiseTaskRecoveredAlert("channel_health", 7)

	var rows []model.ChannelAlert
	if err := db.GetDB().Order("id").Find(&rows).Error; err != nil {
		t.Fatalf("query alerts: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("失败与恢复应各落一条, got %d", len(rows))
	}
	if rows[1].Kind != "task_recovered" {
		t.Fatalf("第二条应为 task_recovered, got %q", rows[1].Kind)
	}
	if !strings.Contains(rows[1].Detail, "此前连续失败: 7 次") {
		t.Fatalf("恢复告警应给出此前失败次数:\n%s", rows[1].Detail)
	}
}

// TestRaiseAlertFactsRendersChannelDetail 校验渠道级告警的落库形状: 主体是真实渠道名,
// 明细按"标签: 值"逐行, 值为空的行丢掉; 外发带上全部明细行。站内列表与 Telegram 文本都直接吃这个字段。
func TestRaiseAlertFactsRendersChannelDetail(t *testing.T) {
	raiseTestDB(t)
	dispatched := captureAlertDispatcher(t)

	channel := model.Channel{ShareCode: "abc123", ChannelConfig: model.ChannelConfig{Name: "生产主渠道", BaseURL: "http://up"}}
	if err := db.GetDB().Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	channelCache.Set(channel.ID, channel)
	defer channelCache.Del(channel.ID)

	if !RaiseAlertFacts(context.Background(), AlertFacts{
		ChannelID: channel.ID,
		Kind:      "key_disabled",
		Reason:    "channel key key-A disabled by circuit breaker: auth",
		Fields: []alert.Field{
			{Label: "渠道", Value: "生产主渠道"},
			{Label: "凭据", Value: "key-A (#1)"},
			{Label: "错误类型", Value: "鉴权失败(密钥无效或已被撤销)"},
			{Label: "熔断时长", Value: ""}, // 空值行不该出现
		},
	}) {
		t.Fatalf("首次告警应落库")
	}

	var row model.ChannelAlert
	if err := db.GetDB().First(&row).Error; err != nil {
		t.Fatalf("query alert: %v", err)
	}
	if row.Subject != "生产主渠道" {
		t.Fatalf("主体应为真实渠道名, got %q", row.Subject)
	}
	if row.ChannelCode != "abc123" {
		t.Fatalf("共享编码仍应进 ChannelCode 列, got %q", row.ChannelCode)
	}
	lines := strings.Split(row.Detail, "\n")
	if len(lines) != 3 {
		t.Fatalf("空值行应被丢掉, 明细应为 3 行:\n%s", row.Detail)
	}
	if !strings.HasPrefix(lines[0], "渠道: ") {
		t.Fatalf("首行应为渠道, got %q", lines[0])
	}

	select {
	case got := <-dispatched:
		if got.Event != alert.EventChannelKeyDisabled {
			t.Fatalf("外发事件应为凭据熔断, got %q", got.Event)
		}
		if got.Scope != "生产主渠道" || len(got.Fields) != 4 {
			t.Fatalf("外发应带主体与全部明细行: scope=%q fields=%d", got.Scope, len(got.Fields))
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("未收到外发告警")
	}

	// 空 kind 或空 reason 一律拒绝: 去重按 (渠道, kind, reason) 精确匹配,
	// 空值会让后续真实告警被一条无意义记录吞掉。
	if RaiseAlertFacts(context.Background(), AlertFacts{ChannelID: channel.ID, Kind: "", Reason: "x"}) {
		t.Fatalf("空 kind 不应落库")
	}
	if RaiseAlertFacts(context.Background(), AlertFacts{ChannelID: channel.ID, Kind: "down", Reason: "   "}) {
		t.Fatalf("空 reason 不应落库")
	}
}

// TestHealthFieldsOmitsProbeErrorWhenSucceeded 钉住一次真实运行中发现的内容缺陷:
// 降级探测只是"慢", 并没有失败, 而旧实现无条件写下一行占位的"探测错误: ...",
// 管理员据此以为探测本身出故障。没有错误就不该出现这一行。
func TestHealthFieldsOmitsProbeErrorWhenSucceeded(t *testing.T) {
	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "生产主渠道", BaseURL: "http://up"}}
	record := model.ChannelHealthRecord{
		StatusCode: 200,
		LatencyMs:  61,
		CheckedAt:  time.Date(2026, 10, 13, 12, 0, 0, 0, time.Local),
	}
	labels := func(fields []alert.Field) []string {
		out := make([]string, 0, len(fields))
		for _, f := range fields {
			out = append(out, f.Label)
		}
		return out
	}

	got := labels(healthFields(channel, record, nil))
	for _, want := range []string{"渠道", "探测地址", "延迟", "状态", "探测时间"} {
		if !strings.Contains(strings.Join(got, ","), want) {
			t.Fatalf("成功探测的明细应含 %q, got %v", want, got)
		}
	}
	if strings.Contains(strings.Join(got, ","), "探测错误") {
		t.Fatalf("成功探测不该写探测错误行: %v", got)
	}

	// 真失败时这一行必须回来, 而且要带上原始错误文本。
	record.Error = "dial tcp: connection refused"
	record.StatusCode = 0
	fields := healthFields(channel, record, []alert.Field{{Label: "连续失败", Value: "3 次"}})
	var errorLine, statusLine string
	for _, f := range fields {
		if f.Label == "探测错误" {
			errorLine = f.Value
		}
		if f.Label == "状态" {
			statusLine = f.Value
		}
	}
	if errorLine != "dial tcp: connection refused" {
		t.Fatalf("失败探测应带上原始错误, got %q", errorLine)
	}
	if statusLine != "无响应" {
		t.Fatalf("无状态码应显示无响应, got %q", statusLine)
	}
	// extra 始终追加在公共行之后: 故障类告警把"连续失败"放在最后一段。
	if fields[len(fields)-1].Label != "连续失败" {
		t.Fatalf("附加明细应排在末尾, got %q", fields[len(fields)-1].Label)
	}
}

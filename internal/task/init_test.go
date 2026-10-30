package task

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
)

// taskTestSetup 初始化隔离测试库, 让 Init() 里的设置读取(周期计算)有库可查。
func taskTestSetup(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "task.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
}

// TestBillingRecoveryRegistered 计费结算恢复任务必须登记为"启动即跑 + 有界周期跑":
// 重启后先补历史积压, 之后每轮重试持久入队未完成的结算。任务名是独立常量, 不与设置键耦合。
func TestBillingRecoveryRegistered(t *testing.T) {
	taskTestSetup(t)

	Init()

	tasksMu.Lock()
	spec, ok := specs[TaskBillingRecovery]
	tasksMu.Unlock()
	if !ok {
		t.Fatalf("task %s is not defined", TaskBillingRecovery)
	}
	if spec.Fn == nil {
		t.Fatal("billing recovery has no task body")
	}
	if !spec.RunOnStart {
		t.Fatal("billing recovery must run on start to drain backlog after restart")
	}
	if d := spec.Interval(); d <= 0 || d > time.Hour {
		t.Fatalf("billing recovery interval = %v, want a bounded periodic interval in (0, 1h]", d)
	}
	if TaskBillingRecovery != "billing_recovery" {
		t.Fatalf("TaskBillingRecovery = %q, want %q", TaskBillingRecovery, "billing_recovery")
	}
}

// TestTaskNameConstantsStable 任务名是告警与运维面板的稳定标识, 不许悄悄改名。
func TestTaskNameConstantsStable(t *testing.T) {
	want := map[string]string{
		TaskBillingRollback:  "billing_rollback",
		TaskBillingReconcile: "billing_reconcile",
		TaskSettlementDaily:  "settlement_daily",
		TaskBillingRecovery:  "billing_recovery",
		TaskStatsSave:        "stats_save",
		TaskLogArchive:       "log_archive",
		TaskTicketAutoClose:  "ticket_auto_close",
	}
	for got, name := range want {
		if got != name {
			t.Fatalf("task name = %q, want %q", got, name)
		}
	}
}

// TestTicketAutoCloseRegistered 工单自动关闭任务必须登记: 启动即跑(补停机期间跨过阈值的工单),
// 周期每小时; 走调度器而不是裸 goroutine。
func TestTicketAutoCloseRegistered(t *testing.T) {
	taskTestSetup(t)

	Init()

	tasksMu.Lock()
	spec, ok := specs[TaskTicketAutoClose]
	tasksMu.Unlock()
	if !ok {
		t.Fatalf("task %s is not defined", TaskTicketAutoClose)
	}
	if spec.Fn == nil {
		t.Fatal("ticket auto-close has no task body")
	}
	if !spec.RunOnStart {
		t.Fatal("ticket auto-close must run on start to catch tickets resolved during downtime")
	}
	if d := spec.Interval(); d != time.Hour {
		t.Fatalf("ticket auto-close interval = %v, want 1h", d)
	}
	if TaskTicketAutoClose != "ticket_auto_close" {
		t.Fatalf("TaskTicketAutoClose = %q, want %q", TaskTicketAutoClose, "ticket_auto_close")
	}
}

// TestTaskNamesAndDefinitionsInSync 注册任务名与调度定义双向锁死(数量 + 名单):
// names.go 的常量除注明的历史遗留外都要有 Define, Init() 注册的名字都要有常量。
// 新增任务时两头各漏一边都会在这里按数量先炸, 免得任务悄悄从调度里消失或名字漂移。
func TestTaskNamesAndDefinitionsInSync(t *testing.T) {
	taskTestSetup(t)

	Init()

	// TaskCleanLLM 是历史遗留常量, 从未 Define(无对应任务体), 在此明确豁免;
	// 除此之外 names.go 常量与 Init() 定义必须一一对应。
	constants := []string{
		TaskPriceUpdate,
		TaskStatsSave,
		TaskBillingRollback,
		TaskBillingReconcile,
		TaskBillingRecovery,
		TaskLogArchive,
		TaskAutoBackup,
		TaskChannelHealth,
		TaskChannelTrustEval,
		TaskChannelQuality,
		TaskChannelAutoAdd,
		TaskSettlementDaily,
		TaskUpdateCheck,
		TaskVideoJobCleanup,
		TaskBreakerProbe,
		TaskTicketAutoClose,
		TaskWebhookDispatch,
		TaskWebhookCleanup,
	}

	tasksMu.Lock()
	defer tasksMu.Unlock()
	if len(specs) != len(constants) {
		defined := make([]string, 0, len(specs))
		for name := range specs {
			defined = append(defined, name)
		}
		t.Fatalf("registered specs = %d %v, want %d (与 names.go 常量同步)", len(specs), defined, len(constants))
	}
	for _, name := range constants {
		if _, ok := specs[name]; !ok {
			t.Fatalf("task %s is not defined", name)
		}
	}
}

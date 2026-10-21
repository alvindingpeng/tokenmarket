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
		TaskBillingRecovery:  "billing_recovery",
		TaskStatsSave:        "stats_save",
		TaskLogArchive:       "log_archive",
	}
	for got, name := range want {
		if got != name {
			t.Fatalf("task name = %q, want %q", got, name)
		}
	}
}

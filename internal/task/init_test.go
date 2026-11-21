package task

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/op"
)

// taskTestSetup 初始化隔离测试库, 让 Init() 里的设置读取(周期计算)有库可查。
func taskTestSetup(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "task.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	// 任务注册表是进程级全局 map, 不跟着库走: 用例注册的假任务(如 snap-ok/snap-off)
	// 会残留给下一个用例, 于是 "names.go 常量与定义必须一一对应" 这条断言
	// 在多注册了几个假任务后失败 —— 只在 -shuffle=on 时暴露。
	resetTaskRegistry()
	t.Cleanup(func() {
		_ = db.Close()
		resetTaskRegistry()
	})
}

// resetTaskRegistry 清空进程级任务注册表, 让每个用例都从干净状态开始。
// 集中在这里而不是逐用例 t.Cleanup: 后者正是问题反复复发的原因 ——
// 默认顺序下全绿, 失败信号极弱, 下一次加用例时又会漏。
func resetTaskRegistry() {
	tasksMu.Lock()
	tasks = make(map[string]*taskEntry)
	specs = make(map[string]Spec)
	tasksMu.Unlock()
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

// TestSettlementCatchupRegistered 保留任务名和任务告警出口, 每小时及启动补偿。
func TestSettlementCatchupRegistered(t *testing.T) {
	taskTestSetup(t)
	Init()
	tasksMu.Lock()
	spec, ok := specs[TaskSettlementDaily]
	tasksMu.Unlock()
	if !ok || spec.Fn == nil || !spec.RunOnStart {
		t.Fatal("settlement catchup must be registered and run on start")
	}
	if d := spec.Interval(); d != time.Hour {
		t.Fatalf("settlement interval = %v, want 1h", d)
	}
	// Missing checkpoint table must surface as a task error (not swallowed by the task body).
	if err := db.GetDB().Exec("DROP TABLE settlement_runs").Error; err != nil {
		t.Fatal(err)
	}
	if err := spec.Fn(); err == nil {
		t.Fatal("settlement task swallowed catchup error")
	}
}

// TestWithdrawApproveTimeoutRegistered 提现审批超时撤销任务必须登记:
// 启动即跑(补上停机期间跨过阈值的单), 周期有界(不能一年跑一次, 否则钱照样冻结很久)。
// 阈值走 op.WithdrawApproveTimeout 常量而不是新增设置项, 因此 settingTasks 里没有它 ——
// 这条用例同时钉住"没有为了它偷偷加设置键": 加了而不登记会让周期热更新静默失效。
func TestWithdrawApproveTimeoutRegistered(t *testing.T) {
	taskTestSetup(t)

	Init()

	tasksMu.Lock()
	spec, ok := specs[TaskWithdrawApproveTimeout]
	tasksMu.Unlock()
	if !ok {
		t.Fatalf("task %s is not defined", TaskWithdrawApproveTimeout)
	}
	if spec.Fn == nil {
		t.Fatal("withdraw approve timeout has no task body")
	}
	if !spec.RunOnStart {
		t.Fatal("withdraw approve timeout must run on start to release orders stuck across downtime")
	}
	// 周期必须小于时限本身: 否则超时单要等下一代阈值才被撤销。
	if d := spec.Interval(); d <= 0 || d >= op.WithdrawApproveTimeout {
		t.Fatalf("withdraw approve timeout interval = %v, want a bounded period inside (0, %v)", d, op.WithdrawApproveTimeout)
	}
	if TaskWithdrawApproveTimeout != "withdraw_approve_timeout" {
		t.Fatalf("TaskWithdrawApproveTimeout = %q, want %q", TaskWithdrawApproveTimeout, "withdraw_approve_timeout")
	}
}

// TestRechargeExpireRegistered 充值单定时关闭任务必须登记: 启动即跑 + 分钟级周期。
// 以前只在"下单"和"订单列表/导出"时懒执行, 没人翻那页到期的单就一直是 pending。
func TestRechargeExpireRegistered(t *testing.T) {
	taskTestSetup(t)

	Init()

	tasksMu.Lock()
	spec, ok := specs[TaskRechargeExpire]
	tasksMu.Unlock()
	if !ok {
		t.Fatalf("task %s is not defined", TaskRechargeExpire)
	}
	if spec.Fn == nil {
		t.Fatal("recharge expire has no task body")
	}
	if !spec.RunOnStart {
		t.Fatal("recharge expire must run on start to close orders that expired during downtime")
	}
	if d := spec.Interval(); d <= 0 || d > time.Hour {
		t.Fatalf("recharge expire interval = %v, want a bounded periodic interval in (0, 1h]", d)
	}
	if TaskRechargeExpire != "recharge_expire" {
		t.Fatalf("TaskRechargeExpire = %q, want %q", TaskRechargeExpire, "recharge_expire")
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
		TaskAuditArchive,
		TaskAutoBackup,
		TaskChannelHealth,
		TaskAlertRedeliver,
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
		TaskRateLimitUsageRetention,
		TaskArchiveRetention,
		TaskBillingRetention,
		TaskWatchdogAlert,
		TaskFxRateUpdate,
		TaskAccountTokenRefresh,
		TaskWithdrawApproveTimeout,
		TaskRechargeExpire,
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

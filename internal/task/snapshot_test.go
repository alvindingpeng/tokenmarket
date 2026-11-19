package task

import (
	"testing"
	"time"
)

// TaskStatus 快照与调度状态的一致性。
// 用一个真实跑过的任务验证 lastStart/lastFinish/lastSuccess 被记录、失败原因进 lastError;
// 未注册的 spec(Interval<=0 只登记未装配)必须出现在快照里且 scheduled=false。
func TestSnapshotReportsRunFacts(t *testing.T) {
	t.Cleanup(func() { Unregister("snap-ok"); Unregister("snap-off") })
	Unregister("snap-ok")
	Unregister("snap-off")

	Define(Spec{
		Name:     "snap-ok",
		Interval: func() time.Duration { return time.Hour },
		Fn:       func() error { return errSnapBoom{} },
	})
	// 未调度的任务: 周期为 0, 只进 specs 不进 tasks。
	Define(Spec{
		Name:     "snap-off",
		Interval: func() time.Duration { return 0 },
		Fn:       func() error { return nil },
	})

	go func() { startRun(tasks["snap-ok"]) }()
	waitFor(t, func() bool {
		for _, st := range Snapshot() {
			if st.Name == "snap-ok" && !st.LastFinish.IsZero() {
				return true
			}
		}
		return false
	})

	byName := map[string]TaskStatus{}
	for _, st := range Snapshot() {
		byName[st.Name] = st
	}
	okSt, hasOK := byName["snap-ok"]
	if !hasOK {
		t.Fatal("snap-ok missing from snapshot")
	}
	if !okSt.Scheduled || okSt.Interval != time.Hour {
		t.Fatalf("scheduled=%v interval=%v, want true/1h", okSt.Scheduled, okSt.Interval)
	}
	if okSt.LastError == "" {
		t.Fatal("last_error not recorded for failing run")
	}
	if okSt.LastStart.IsZero() || okSt.LastFinish.IsZero() {
		t.Fatalf("run facts not recorded: start=%v finish=%v", okSt.LastStart, okSt.LastFinish)
	}
	if !okSt.LastSuccess.IsZero() {
		// 失败的任务不应有成功时刻 —— 用显式断言表达语义。
		t.Fatalf("failed task recorded last_success=%v", okSt.LastSuccess)
	}
	if okSt.Consecutive != 1 {
		t.Fatalf("consecutive=%d, want 1", okSt.Consecutive)
	}

	offSt, hasOff := byName["snap-off"]
	if !hasOff {
		t.Fatal("unscheduled spec missing from snapshot")
	}
	if offSt.Scheduled || offSt.Interval != 0 {
		t.Fatalf("unscheduled task shows scheduled=%v interval=%v", offSt.Scheduled, offSt.Interval)
	}
	if offSt.Running || !offSt.LastFinish.IsZero() {
		t.Fatalf("unscheduled task shows activity: running=%v finish=%v", offSt.Running, offSt.LastFinish)
	}
}

type errSnapBoom struct{}

func (errSnapBoom) Error() string { return "snapshot test failure" }

// waitFor 轮询条件成立, 最多 1 秒; 后台任务体是异步 goroutine, 不能假设同步完成。
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within 1s")
}

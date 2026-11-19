package op

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/alert"
)

// 本文件是"异步告警外发的生命周期同步"竞争回归:
//   - 在途派发未完成时 alertDispatchWait 不得提前返回, 送达完成后必须返回;
//   - 分发器在 spawn 之前捕获: 之后改写 AlertDispatcher 全局不改道在途派发,
//     派发协程全程不读写该全局(否则 -race 直接报出竞争)。

// TestAlertDispatchLifecycleWaitAndCapture 派发被卡住时等待必须挂起, 放行后收尾:
// 中途把 AlertDispatcher 换成别的分发器, 在途派发仍走 spawn 时捕获的那一个。
func TestAlertDispatchLifecycleWaitAndCapture(t *testing.T) {
	raiseTestDB(t)
	original := AlertDispatcher
	t.Cleanup(func() {
		alertDispatchWait()
		AlertDispatcher = original
	})

	started := make(chan struct{})
	blocked := make(chan struct{})
	var unblockOnce sync.Once
	unblock := func() { unblockOnce.Do(func() { close(blocked) }) }
	defer unblock()    // 任何 t.Fatal/panic 路径都先放行派发, 否则收尾的 alertDispatchWait 会死锁
	t.Cleanup(unblock) // LIFO 兜底: unblock 先于 waiting 清理执行
	out := make(chan alert.Alert, 1)
	AlertDispatcher = func(_ context.Context, a alert.Alert) bool {
		close(started)
		<-blocked
		out <- a
		return true
	}

	if !RaiseAlertFacts(context.Background(), AlertFacts{Kind: "test", Reason: "dispatch lifecycle"}) {
		t.Fatal("first alert must be recorded")
	}

	// 派发协程已经 spawn 但多半还没被调度: 立刻改写全局, 在途派发不得被改道。
	// (spawn 后再读全局的写法会在这里解析到 swapped 那一个, started 永远不来。)
	swapped := make(chan alert.Alert, 1)
	AlertDispatcher = func(_ context.Context, a alert.Alert) bool {
		swapped <- a
		return true
	}

	select {
	case <-started: // 外发协程已进入 spawn 时捕获的分发器
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch must run against the dispatcher captured before spawn")
	}

	waited := make(chan struct{})
	go func() {
		alertDispatchWait()
		close(waited)
	}()
	select {
	case <-waited:
		t.Fatal("alertDispatchWait must not return while a dispatch is pending")
	case <-time.After(150 * time.Millisecond):
	}

	unblock()
	select {
	case <-waited:
	case <-time.After(2 * time.Second):
		t.Fatal("alertDispatchWait must return once the pending dispatch completes")
	}

	select {
	case got := <-out:
		if got.Kind != "test" || got.Scope != subjectSystem {
			t.Fatalf("dispatch payload mismatch: kind=%q scope=%q", got.Kind, got.Scope)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending dispatch must be delivered")
	}
	select {
	case <-swapped:
		t.Fatal("in-flight dispatch must not be rerouted to a dispatcher swapped in after spawn")
	default:
	}
}

// TestAlertDispatchWaitCoversNotYetStartedDispatch 专抓"派发协程起步晚于 Wait"这一漏:
// 在途计数在 spawn 之前登记(Add 先于 go), 于是协程哪怕还没被调度, Wait 也必须等到它跑完。
// 协程内 Add 的写法下 Wait 会赶在协程起步前返回, 末尾的非阻塞断言直接抓到。
func TestAlertDispatchWaitCoversNotYetStartedDispatch(t *testing.T) {
	raiseTestDB(t)
	original := AlertDispatcher
	out := make(chan alert.Alert, 1)
	AlertDispatcher = func(_ context.Context, a alert.Alert) bool {
		out <- a
		return true
	}
	t.Cleanup(func() {
		alertDispatchWait()
		AlertDispatcher = original
	})

	// 单核调度: RaiseAlertFacts 一返回, 派发协程大概率还没被调度 —— 正是"起步晚于 Wait"的场景。
	procs := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(procs) })

	if !RaiseAlertFacts(context.Background(), AlertFacts{Kind: "test", Reason: "late dispatch"}) {
		t.Fatal("alert must be recorded")
	}
	alertDispatchWait() // 必须等到这条派发真正跑完, 即使协程此刻还没起步

	select {
	case <-out:
	default:
		t.Fatal("dispatch must have completed before alertDispatchWait returned")
	}
}

// TestAlertDispatchLifecycleUnderConcurrentRaises 并发产生告警后, 等待必须收全每一条外发:
// 在途计数在 spawn 之前登记, 无论派发协程何时起步, alertDispatchWait 返回时一条都不能少。
// (协程内 Add 的旧写法在这里就会漏: Wait 可能赶在所有 Add 之前返回。)
func TestAlertDispatchLifecycleUnderConcurrentRaises(t *testing.T) {
	raiseTestDB(t)
	original := AlertDispatcher
	out := make(chan alert.Alert, 32)
	AlertDispatcher = func(_ context.Context, a alert.Alert) bool {
		out <- a
		return true
	}
	t.Cleanup(func() {
		alertDispatchWait()
		AlertDispatcher = original
	})

	const workers = 16
	start := make(chan struct{}) // 起跑栅栏: 16 个 goroutine 同一时刻真并发进入。
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			RaiseAlertFacts(context.Background(), AlertFacts{
				Kind:   "test",
				Reason: fmt.Sprintf("concurrent dispatch %d", i), // 去重键各不相同
			})
		}(i)
	}
	close(start)
	wg.Wait()
	alertDispatchWait()

	got := 0
drain:
	for {
		select {
		case <-out:
			got++
		default:
			break drain
		}
	}
	if got != workers {
		t.Fatalf("dispatched %d alerts after wait, want %d (wait must cover every pending dispatch)", got, workers)
	}
}

package relay

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/op"
)

// 续租心跳的确定性测试: 续租结果可编程, 心跳节拍由测试手动 tick, 失败/停止顺序由显式信号编排。
// 全程不 sleep、不忙等; time.After 只作失败保险(正常路径永远先收到显式信号)。

// fakeLeaseTimer 假心跳节拍: 测试手动发 tick, stop 被调用即关闭 stopped。
type fakeLeaseTimer struct {
	ticks   chan time.Time
	stopped chan struct{}
	once    sync.Once
}

func (t *fakeLeaseTimer) stop() { t.once.Do(func() { close(t.stopped) }) }

// fakeBillingLease 可控的续租环境: 记录每次续租、告警与取消, 并接管计时器。
type fakeBillingLease struct {
	mu       sync.Mutex
	renewals []uint64          // 每次续租的预扣 ID, 按发生顺序。
	renewErr error             // 非 nil 时续租返回它。
	gate     chan struct{}     // 非 nil 时续租先等它关闭(制造"停心跳时续租正在进行")。
	renewed  chan struct{}     // 每次续租非阻塞发一个信号。
	timers   []*fakeLeaseTimer // 按创建顺序的心跳计时器。
	alerts   []op.AlertFacts
	cancels  []uint64 // 失效关闭取消动作的调用记录(请求 ID)。
}

func (f *fakeBillingLease) setRenewErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renewErr = err
}

func (f *fakeBillingLease) setGate(gate chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gate = gate
}

// cancelFunc 返回记录调用的请求级取消句柄, 与 RequestState.requestCancel 同型。
func (f *fakeBillingLease) cancelFunc(requestID uint64) context.CancelFunc {
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.cancels = append(f.cancels, requestID)
	}
}

func (f *fakeBillingLease) renewCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.renewals)
}

func (f *fakeBillingLease) renewalIDs() []uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uint64(nil), f.renewals...)
}

func (f *fakeBillingLease) alertRecords() []op.AlertFacts {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]op.AlertFacts(nil), f.alerts...)
}

func (f *fakeBillingLease) cancelRecords() []uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uint64(nil), f.cancels...)
}

func (f *fakeBillingLease) timer(t *testing.T, index int) *fakeLeaseTimer {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if index >= len(f.timers) {
		t.Fatalf("heartbeat timer %d not created (have %d)", index, len(f.timers))
	}
	return f.timers[index]
}

func (f *fakeBillingLease) timerCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.timers)
}

// installFakeBillingLease 注入假续租/计时/告警, 测试结束自动还原生产入口。
// 取消动作走每条心跳自带的请求级 cancel, 不经包级入口。
func installFakeBillingLease(t *testing.T) *fakeBillingLease {
	t.Helper()
	fake := &fakeBillingLease{renewed: make(chan struct{}, 64)}
	prevRenew, prevTimer, prevAlert := billingLeaseRenew, billingLeaseTimer, billingLeaseAlert
	billingLeaseRenew = func(reservationID uint64, ttl time.Duration) error {
		fake.mu.Lock()
		gate := fake.gate
		fake.mu.Unlock()
		if gate != nil {
			<-gate
		}
		fake.mu.Lock()
		fake.renewals = append(fake.renewals, reservationID)
		err := fake.renewErr
		fake.mu.Unlock()
		select {
		case fake.renewed <- struct{}{}:
		default:
		}
		return err
	}
	billingLeaseTimer = func(interval time.Duration) (<-chan time.Time, func()) {
		timer := &fakeLeaseTimer{ticks: make(chan time.Time), stopped: make(chan struct{})}
		fake.mu.Lock()
		fake.timers = append(fake.timers, timer)
		fake.mu.Unlock()
		return timer.ticks, timer.stop
	}
	billingLeaseAlert = func(facts op.AlertFacts) {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		fake.alerts = append(fake.alerts, facts)
	}
	t.Cleanup(func() {
		// 先停掉所有仍在跑的心跳再还原入口: 泄漏的协程若在还原后读回生产入口,
		// 会与测试假实现互踩(数据竞争), 也证明用例没管好自己的生命周期。
		mu.Lock()
		running := make([]*billingLease, 0, len(billingLeases))
		for id := range billingLeases {
			running = append(running, takeBillingLeaseLocked(id))
		}
		mu.Unlock()
		for _, lease := range running {
			lease.stop()
		}
		billingLeaseRenew, billingLeaseTimer, billingLeaseAlert = prevRenew, prevTimer, prevAlert
	})
	return fake
}

// waitSignal 等一个显式信号; 超时只是失败保险。
func waitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for %s", what)
	}
}

// tick 推进一个心跳节拍。
func tick(t *testing.T, timer *fakeLeaseTimer) {
	t.Helper()
	select {
	case timer.ticks <- time.Time{}:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout sending heartbeat tick")
	}
}

// TestBillingLeaseHeartbeatRenewsUntilStopped 启动即续租, 每个节拍续一次; 停止后协程退出且不再续租。
func TestBillingLeaseHeartbeatRenewsUntilStopped(t *testing.T) {
	fake := installFakeBillingLease(t)

	mu.Lock()
	startBillingLeaseLocked(101, 42, fake.cancelFunc(101))
	lease := billingLeases[101]
	mu.Unlock()

	waitSignal(t, fake.renewed, "initial renew")
	timer := fake.timer(t, 0)
	tick(t, timer)
	waitSignal(t, fake.renewed, "renew after first tick")
	tick(t, timer)
	waitSignal(t, fake.renewed, "renew after second tick")

	lease.stop() // 停止 + join

	if got := fake.renewCount(); got != 3 {
		t.Fatalf("renewals = %d, want 3 (start + 2 ticks)", got)
	}
	for i, id := range fake.renewalIDs() {
		if id != 42 {
			t.Fatalf("renewal %d reservation = %d, want 42", i, id)
		}
	}
	select {
	case <-lease.doneCh:
	default:
		t.Fatal("lease goroutine still running after stop")
	}
}

// TestBillingLeaseRenewFailureAlertsAndCancels 续租失败即失效关闭: 告警 + 用请求级 cancel
// 取消请求, 只发生一次, 且全程不经过全局请求表。
func TestBillingLeaseCancelsBeforeBlockedAlert(t *testing.T) {
	fake := installFakeBillingLease(t)
	fake.setRenewErr(errors.New("lease lost"))
	entered := make(chan struct{})
	release := make(chan struct{})
	original := billingLeaseAlert
	billingLeaseAlert = func(facts op.AlertFacts) { close(entered); <-release; original(facts) }
	mu.Lock()
	startBillingLeaseLocked(999, 999, fake.cancelFunc(999))
	lease := billingLeases[999]
	mu.Unlock()
	waitSignal(t, entered, "alert entered")
	got := fake.cancelRecords()
	if len(got) != 1 || got[0] != 999 {
		close(release)
		lease.wait()
		t.Fatalf("request was not canceled before alert: %v", got)
	}
	close(release)
	lease.wait()
}

func TestBillingLeaseRenewFailureAlertsAndCancels(t *testing.T) {
	fake := installFakeBillingLease(t)
	fake.setRenewErr(errors.New("lease lost"))

	mu.Lock()
	startBillingLeaseLocked(102, 43, fake.cancelFunc(102))
	lease := billingLeases[102]
	mu.Unlock()

	lease.wait() // 失效关闭路径完成后协程退出

	if got := fake.renewCount(); got != 1 {
		t.Fatalf("renewals = %d, want 1 (fail closed on first failure)", got)
	}
	alerts := fake.alertRecords()
	if len(alerts) != 1 || alerts[0].Kind != "billing_failed" {
		t.Fatalf("alerts = %+v, want exactly one billing_failed", alerts)
	}
	cancels := fake.cancelRecords()
	if len(cancels) != 1 || cancels[0] != 102 {
		t.Fatalf("cancels = %v, want exactly [102]", cancels)
	}
}

// TestBillingLeaseFailureAfterStopIsSilent 停止先于失败被观察到: 不告警、不取消请求。
// 编排: 续租阻塞在 gate 上 -> 发停止信号 -> 放行续租(失败) -> 失效关闭路径看到已停止, 静默退出。
func TestBillingLeaseFailureAfterStopIsSilent(t *testing.T) {
	fake := installFakeBillingLease(t)
	gate := make(chan struct{})
	fake.setGate(gate)
	fake.setRenewErr(errors.New("lease lost"))

	mu.Lock()
	startBillingLeaseLocked(103, 44, fake.cancelFunc(103))
	lease := billingLeases[103]
	mu.Unlock()

	lease.stopSignal() // 先发停止信号
	close(gate)        // 再放行正在进行的续租, 让失败晚于停止被观察到
	lease.wait()

	if got := fake.alertRecords(); len(got) != 0 {
		t.Fatalf("alerts after stop = %+v, want none", got)
	}
	if got := fake.cancelRecords(); len(got) != 0 {
		t.Fatalf("cancels after stop = %v, want none", got)
	}
}

// TestSetReservationManagesLeaseLifecycle SetReservation 启动心跳、重复登记换心跳并停旧的、
// 预扣归零即停; 旧协程退出由计时器 stop 信号证明, 无泄漏。
func TestSetReservationManagesLeaseLifecycle(t *testing.T) {
	fake := installFakeBillingLease(t)
	request := &RequestState{ID: 201, requestCancel: fake.cancelFunc(201)}

	request.SetReservation(51)
	waitSignal(t, fake.renewed, "initial renew for reservation 51") // 首次续租后计时器必然已建好
	first := fake.timer(t, 0)
	mu.Lock()
	lease := billingLeases[201]
	mu.Unlock()
	if lease == nil || lease.reservationID != 51 {
		t.Fatalf("lease = %+v, want reservation 51", lease)
	}

	request.SetReservation(52) // 换心跳: 旧的被停止并 join
	waitSignal(t, first.stopped, "old heartbeat goroutine exit")
	waitSignal(t, fake.renewed, "initial renew for reservation 52") // 新心跳首轮续租后其计时器已建好
	mu.Lock()
	replaced := billingLeases[201]
	mu.Unlock()
	if replaced == nil || replaced.reservationID != 52 {
		t.Fatalf("lease = %+v, want reservation 52", replaced)
	}
	second := fake.timer(t, 1)
	tick(t, second)
	waitSignal(t, fake.renewed, "renew for reservation 52")
	ids := fake.renewalIDs()
	if ids[len(ids)-1] != 52 {
		t.Fatalf("last renewal = %d, want 52", ids[len(ids)-1])
	}

	request.SetReservation(0) // 无预扣: 心跳同样停止
	waitSignal(t, second.stopped, "second heartbeat goroutine exit")
	mu.Lock()
	remaining := billingLeases[201]
	mu.Unlock()
	if remaining != nil {
		t.Fatalf("lease still registered after reservation cleared: %+v", remaining)
	}
}

// TestSetReservationAfterFinalizeStartsNoHeartbeat 已定稿的请求不再启动心跳: 续租没人停就是泄漏。
func TestSetReservationAfterFinalizeStartsNoHeartbeat(t *testing.T) {
	fake := installFakeBillingLease(t)
	request := &RequestState{ID: 202, finalized: true, requestCancel: fake.cancelFunc(202)}

	request.SetReservation(53)

	mu.Lock()
	lease := billingLeases[202]
	mu.Unlock()
	if lease != nil {
		t.Fatalf("heartbeat started for finalized request: %+v", lease)
	}
	if got := fake.timerCount(); got != 0 {
		t.Fatalf("heartbeat timers = %d, want 0", got)
	}
}

// TestSetReservationWithoutCancelHandleStartsNoHeartbeat 请求取消句柄已被取走(上下文已取消):
// 失效关闭没有取消对象, 不启心跳, 请求照常收尾。
func TestSetReservationWithoutCancelHandleStartsNoHeartbeat(t *testing.T) {
	fake := installFakeBillingLease(t)
	request := &RequestState{ID: 204} // requestCancel == nil

	request.SetReservation(55)

	mu.Lock()
	lease := billingLeases[204]
	mu.Unlock()
	if lease != nil {
		t.Fatalf("heartbeat started without cancel handle: %+v", lease)
	}
	if got := fake.timerCount(); got != 0 {
		t.Fatalf("heartbeat timers = %d, want 0", got)
	}
}

// TestTakeBillingLeaseOnlyOnce 摘心跳幂等: 重复收尾只有一方拿到, 不会两条协程抢同一笔预扣。
func TestTakeBillingLeaseOnlyOnce(t *testing.T) {
	fake := installFakeBillingLease(t)

	mu.Lock()
	startBillingLeaseLocked(203, 54, fake.cancelFunc(203))
	first := takeBillingLeaseLocked(203)
	second := takeBillingLeaseLocked(203)
	mu.Unlock()

	if first == nil {
		t.Fatal("first take returned nil")
	}
	if second != nil {
		t.Fatalf("second take returned %+v, want nil", second)
	}
	first.stop()
}

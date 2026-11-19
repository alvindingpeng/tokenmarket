package relay

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/alvindingpeng/switchyard/internal/alert"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/charmbracelet/log"
)

// 计费预扣租约心跳(phase2A relay 侧)。
//
// 预扣记录带租约: 在途请求按 op.BillingLeaseHeartbeatInterval 续 op.BillingLeaseDuration 时长的租约
// (op.BillingRenewLease), 让"请求还在跑"与"这笔预扣别被按泄漏清理"保持一致。
//
// 失效关闭(fail closed): 任何一次续租失败都按"租约不可信"处理 —— 记日志、发告警、取消请求。
// 请求不允许在租约可能已经过期之后继续消耗: 那会变成钱没冻结住却在向上游付费。
//
// 生命周期: SetReservation 启动; 请求定稿且结算意图已被受理(零用量即显式释放)之后停止。
// 停止分两步 —— 锁内只摘心跳、发停止信号, join(等协程退出)在全局锁外做(stopBillingLease):
// 心跳的失效关闭路径要拿全局锁, 持锁等待它就是死锁。
//
// 心跳登记在独立注册表(按请求 ID)而不是 RequestState 字段上: 状态流与日志会对 RequestState
// 做值拷贝(publishRequestLocked / OpenRequestStream), 挂指针句柄会把可变状态带进快照副本,
// 副本上误用句柄就会停掉别的地方还在用的续租。
//
// 视频请求不在本文件改变口径: 生视频按"请求响应创建"时的用量在定稿时结算(见 settleBillingLocked),
// 后台生成完成不计费, 因此视频请求的续租同样随请求定稿而止。
type billingLease struct {
	requestID     uint64
	reservationID uint64
	// cancel 请求级取消句柄(requestCancel)在启动时捕获的函数值: 失效关闭直接调它,
	// 不回查全局请求表 —— 续租协程因此全程不碰全局锁, 也不会拿到请求表变更后的过期指针。
	// 捕获的是函数值而非 *RequestState, 状态流快照拷贝带不走它。
	cancel   context.CancelFunc
	stopCh   chan struct{} // 停止信号; 关闭即心跳不再续租。
	doneCh   chan struct{} // 协程退出信号; 关闭保证不再续租、不再告警、不再取消请求。
	stopOnce sync.Once
}

// 续租/计时/告警的可注入入口: 生产分别指向 op.BillingRenewLease、真实计时器与站内告警。
// 测试换成假实现, 用显式信号推进心跳节拍 —— 不靠 sleep 或忙等制造时序。
// 取消动作不在这里注入: 每条心跳自带启动时捕获的请求级 cancel(见 billingLease.cancel)。
var (
	billingLeaseRenew = op.BillingRenewLease
	billingLeaseTimer = realBillingLeaseTimer
	billingLeaseAlert = raiseBillingLeaseAlert
)

// raiseBillingLeaseAlert 生产告警出口: 走站内告警 + 外发, 与结算失败告警同一条路。
func raiseBillingLeaseAlert(facts op.AlertFacts) {
	op.RaiseAlertFacts(context.Background(), facts)
}

// realBillingLeaseTimer 生产计时器: 每个节拍触发一次续租, 返回的 stop 关闭计时器。
func realBillingLeaseTimer(interval time.Duration) (<-chan time.Time, func()) {
	ticker := time.NewTicker(interval)
	return ticker.C, ticker.Stop
}

// run 续租循环: 启动即续一次(租约立刻对齐到本请求), 之后按心跳节拍续; 任一失败即失效关闭并退出。
//
// 不变量: 本协程全程不持全局锁。续租(op.BillingRenewLease)是 DB 调用, 告警(RaiseAlertFacts)
// 也是 DB 调用, 都发生在无锁状态; 取消走启动时捕获的请求级 cancel, 同样不碰全局锁。
func (l *billingLease) run() {
	defer close(l.doneCh)

	ttl := time.Duration(op.BillingLeaseDuration)
	interval := time.Duration(op.BillingLeaseHeartbeatInterval)
	if ttl <= 0 || interval <= 0 {
		l.fail(fmt.Errorf("invalid billing lease duration ttl=%v interval=%v", ttl, interval))
		return
	}
	ticks, stopTicks := billingLeaseTimer(interval)
	defer stopTicks()

	for {
		select {
		case <-l.stopCh:
			return
		default:
		}
		if err := billingLeaseRenew(l.reservationID, ttl); err != nil {
			l.fail(err)
			return
		}
		select {
		case <-l.stopCh:
			return
		case <-ticks:
		}
	}
}

// fail 续租失败的失效关闭: 日志 + 告警 + 取消请求, 至多执行一次。
// 请求收尾已经停掉心跳时不再打搅: 定稿的请求本就要结束, 不该再被取消, 也不该发过期告警。
func (l *billingLease) fail(err error) {
	select {
	case <-l.stopCh:
		return
	default:
	}
	log.Warnf("billing lease renew failed: request=%d reservation=%d: %v", l.requestID, l.reservationID, err)
	l.cancelRequest() // Cancel before any database-dependent alert can block.
	billingLeaseAlert(op.AlertFacts{
		Kind:   "billing_failed",
		Reason: fmt.Sprintf("billing lease renew failed: reservation=%d", l.reservationID),
		Fields: []alert.Field{
			{Label: "调用请求", Value: fmt.Sprintf("#%d", l.requestID)},
			{Label: "预扣记录", Value: fmt.Sprintf("#%d", l.reservationID)},
			{Label: "错误", Value: err.Error()},
			{Label: "影响", Value: "租约不可信, 请求已被取消(fail closed), 不允许在租约可能过期后继续消耗"},
			{Label: "建议", Value: "检查计费库与结算恢复任务; 该请求会按实际/零用量走结算或释放"},
		},
	})
}

// cancelRequest 失效关闭的取消动作: 直接调用启动时捕获的请求级 cancel, 不回查全局请求表。
// cancel 为 nil 说明请求上下文已被别处取消(句柄已被取走), 请求本就在收尾, 只记日志。
func (l *billingLease) cancelRequest() {
	if l.cancel == nil {
		log.Warnf("billing lease fail-closed cancel unavailable: request %d context already canceled", l.requestID)
		return
	}
	l.cancel()
}

// stopSignal 只发停止信号, 不等待协程退出。
func (l *billingLease) stopSignal() {
	l.stopOnce.Do(func() { close(l.stopCh) })
}

// wait 等待心跳协程退出(join)。
func (l *billingLease) wait() {
	<-l.doneCh
}

// stop 停止心跳并等待协程退出。绝不能在持有全局锁时调用: 心跳的失效关闭路径要拿同一把锁。
func (l *billingLease) stop() {
	l.stopSignal()
	l.wait()
}

// stopBillingLease 停止(并 join)一条心跳; nil 安全。调用方必须已在全局锁外。
func stopBillingLease(lease *billingLease) {
	if lease != nil {
		lease.stop()
	}
}

// billingLeases 请求 ID -> 在途预扣续租心跳。只在全局锁下读写, 从不随 RequestState 拷贝。
var billingLeases = make(map[uint64]*billingLease)

// startBillingLeaseLocked 为请求启动(或替换)预扣续租心跳, 返回被换下、需在锁外停止的旧心跳。
// cancel 是请求级取消句柄(即 RequestState.requestCancel)的函数值快照, 供失效关闭直接取消请求。
// 调用方必须持有全局锁; 返回值交给 stopBillingLease 在锁外 join, 绝不持锁等待。
func startBillingLeaseLocked(requestID, reservationID uint64, cancel context.CancelFunc) *billingLease {
	previous := billingLeases[requestID]
	if previous != nil {
		previous.stopSignal()
	}
	lease := &billingLease{
		requestID:     requestID,
		reservationID: reservationID,
		cancel:        cancel,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
	billingLeases[requestID] = lease
	go lease.run()
	return previous
}

// takeBillingLeaseLocked 摘下并返回本请求的续租心跳(可能为 nil), 供请求定稿时在锁外停止。
// 摘下即幂等: 重复定稿、重复收尾都只会拿到一次, 不会出现两条协程抢同一笔预扣的续租。
func takeBillingLeaseLocked(requestID uint64) *billingLease {
	lease := billingLeases[requestID]
	if lease != nil {
		lease.stopSignal()
	}
	delete(billingLeases, requestID)
	return lease
}

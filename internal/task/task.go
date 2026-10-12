package task

import (
	"sync"
	"time"

	"github.com/charmbracelet/log"
)

// 定时任务调度。
//
// 本次改造补上两件事:
//   1. 配置热更新: 间隔类设置改动后立即生效, 不必重启进程(见 Apply / ApplySetting)。
//   2. 失败可见: 任务连续失败达到阈值时经告警出口通知管理员。改造前任务失败只有 log.Warnf,
//      备份不再产出、归档停在原地都没人知道, 直到磁盘被日志写满才暴露。

// taskAlertAfter 连续失败多少次后开始告警。
//
// 取 3 而不是 1: 备份、归档、健康探测都带外部依赖(磁盘、上游、网络), 单次抖动不代表故障;
// 逢失败就推必然让管理员对告警脱敏, 那比不告警更糟。达到阈值后每轮都调回调,
// 实际推送频率由 op 侧的去重窗口(alert_dedup_minutes)收敛。
const taskAlertAfter = 3

// OnTaskFailure / OnTaskRecovered 由启动时注入(见 cmd/start.go), task 包不依赖 op/alert。
// 均为 nil 安全: 未接线(单元测试)时只保留日志。
var (
	OnTaskFailure   func(name string, consecutive int, reason string)
	OnTaskRecovered func(name string, consecutive int)
)

// Spec 一个任务的可重复装配说明: 间隔怎么算、要不要启动即跑、任务体。
//
// 间隔做成函数而不是常量值, 才能"改了设置立即重算": 定时任务的实际周期取决于读取时机,
// 把读取权留在调度侧, 设置处理器就无需知道每个任务怎么算周期。
type Spec struct {
	Name       string
	Interval   func() time.Duration // 返回 0 表示该任务当前不应运行(功能被关闭)
	RunOnStart bool
	Fn         func() error
}

type taskEntry struct {
	name     string
	spec     Spec
	interval time.Duration

	// tickerMu 保护 ticker 指针: 换周期来自设置处理器所在的 goroutine,
	// 读它的是任务自己的调度 goroutine, 不加锁就是数据竞争。
	tickerMu sync.Mutex
	ticker   *time.Ticker
	stopOnce sync.Once
	stopCh   chan struct{}
	updateCh chan time.Duration

	// failures 当前连续失败次数, 成功即清零; alerted 标记本轮故障是否已就此告警过,
	// 用于"恢复时补一条 recovered"而不是每轮都补。
	failures int
	alerted  bool
}

var (
	tasks   = make(map[string]*taskEntry)
	specs   = make(map[string]Spec)
	tasksMu sync.Mutex
	running bool // RUN 是否已执行: 决定 Define 自己起 goroutine 还是留给 RUN
)

// Register 注册一个定时任务(固定间隔, 兼容旧调用)。
// runOnStart: 是否在启动时立即执行一次。
func Register(name string, interval time.Duration, runOnStart bool, fn func()) {
	Define(Spec{
		Name:       name,
		Interval:   func() time.Duration { return interval },
		RunOnStart: runOnStart,
		Fn:         func() error { fn(); return nil },
	})
}

// Define 登记任务规格并按当前配置装配。同名重复定义时以最后一次为准(只可能出现在测试)。
func Define(spec Spec) {
	if spec.Name == "" || spec.Fn == nil || spec.Interval == nil {
		log.Warnf("task %s defined without name/interval/fn, skipping", spec.Name)
		return
	}
	tasksMu.Lock()
	specs[spec.Name] = spec
	entry := tasks[spec.Name]
	if entry != nil {
		entry.spec = spec
	}
	started := running && entry == nil
	intended := spec.Interval()
	tasksMu.Unlock()

	if entry != nil {
		// 已被 RUN 拉起: 立刻应用新周期(可能为 0, 即就地停任务)而不是重建 goroutine,
		// 避免同一个任务出现两个 ticker 并发跑。
		applyInterval(entry, intended)
		log.Debugf("task %s redefined, interval %v", spec.Name, intended)
		return
	}
	if intended <= 0 {
		log.Debugf("task %s defined but not scheduled: interval is 0", spec.Name)
		return
	}
	e := &taskEntry{
		name:     spec.Name,
		spec:     spec,
		interval: intended,
		stopCh:   make(chan struct{}),
		updateCh: make(chan time.Duration, 1),
	}
	tasksMu.Lock()
	tasks[spec.Name] = e
	tasksMu.Unlock()
	if started {
		go runTask(e)
	}
	log.Debugf("task %s registered with interval %v, runOnStart: %v", spec.Name, intended, spec.RunOnStart)
}

// Apply 按已登记规格重算间隔并生效, 是"改设置后热更新"的唯一入口。
// 三种结果都正常: 周期变了(热更新)、功能关了(停任务)、功能开了(现场注册)。
func Apply(name string) error {
	tasksMu.Lock()
	spec, ok := specs[name]
	entry := tasks[name]
	tasksMu.Unlock()
	if !ok {
		return ErrNotRegistered
	}
	if entry == nil {
		Define(spec)
		return nil
	}
	intended := spec.Interval()
	applyInterval(entry, intended)
	log.Infof("task %s interval applied: %v", name, intended)
	return nil
}

// applyInterval 把期望周期落到运行中的任务上。
// <=0 时停掉 ticker 并从表里摘除(功能关闭), 但保留 specs 记录, 以便重新开启时现场装配。
func applyInterval(entry *taskEntry, interval time.Duration) {
	if interval <= 0 {
		tasksMu.Lock()
		if tasks[entry.name] == entry {
			delete(tasks, entry.name)
		}
		tasksMu.Unlock()
		entry.stop()
		log.Infof("task %s unscheduled (interval <= 0)", entry.name)
		return
	}
	// updateCh 有缓冲且非阻塞发送: 调度 goroutine 可能正在执行任务, 持锁等待会拖住整个任务表。
	select {
	case entry.updateCh <- interval:
	default:
		log.Warnf("task %s update channel full, applying interval directly", entry.name)
		entry.swapTicker(interval)
	}
}

// Unregister 立即停用一个任务(不留待下一轮周期生效), 供"关掉开关"这类明确要求停任务的场景。
func Unregister(name string) {
	tasksMu.Lock()
	entry := tasks[name]
	if entry != nil {
		delete(tasks, name)
	}
	tasksMu.Unlock()
	if entry == nil {
		return
	}
	entry.stop()
}

// ErrNotRegistered 表示要热更新的任务从未 Define 过: 配置改了但没人负责应用。
// 设置处理器把它回给界面, 比"只写日志、界面显示保存成功"更容易发现漏配。
var ErrNotRegistered = errNotRegistered{}

type errNotRegistered struct{}

func (errNotRegistered) Error() string { return "task not defined" }

// Registered 判断任务当前是否处于调度中。
func Registered(name string) bool {
	tasksMu.Lock()
	defer tasksMu.Unlock()
	_, ok := tasks[name]
	return ok
}

// RUN 启动所有注册的任务
func RUN() {
	tasksMu.Lock()
	running = true
	entries := make([]*taskEntry, 0, len(tasks))
	for _, entry := range tasks {
		entries = append(entries, entry)
	}
	tasksMu.Unlock()

	for _, entry := range entries {
		go runTask(entry)
	}

	// 阻塞主协程
	select {}
}

func runTask(entry *taskEntry) {
	if entry.spec.RunOnStart {
		go entry.run()
	}

	entry.swapTicker(entry.interval)

	for {
		// 每轮重新取 ticker: 换周期后 select 必须跟着指向新的计时器。
		ticker := entry.currentTicker()
		if ticker == nil {
			return // 已被停用
		}
		select {
		case <-ticker.C:
			go entry.run()
		case newInterval := <-entry.updateCh:
			if newInterval <= 0 {
				entry.stopTicker()
				return
			}
			entry.swapTicker(newInterval)
		case <-entry.stopCh:
			entry.stopTicker()
			return
		}
	}
}

// currentTicker 取当前周期的 ticker; 停用后返回 nil, 调度循环据此退出。
func (entry *taskEntry) currentTicker() *time.Ticker {
	entry.tickerMu.Lock()
	defer entry.tickerMu.Unlock()
	return entry.ticker
}

// swapTicker 换掉周期(ticker 首次创建也走这里)。旧 ticker 必须 Stop,
// 否则 runtime 会一直为它计时; 设置反复改周期会漏出计时器。
// 加锁是因为换周期的调用方在 HTTP 处理协程里, 而读它的是任务自己的调度协程。
func (entry *taskEntry) swapTicker(interval time.Duration) {
	entry.tickerMu.Lock()
	defer entry.tickerMu.Unlock()
	if entry.ticker != nil {
		entry.ticker.Stop()
	}
	entry.interval = interval
	entry.ticker = time.NewTicker(interval)
}

// stopTicker 停掉计时器并摘除指针。
func (entry *taskEntry) stopTicker() {
	entry.tickerMu.Lock()
	defer entry.tickerMu.Unlock()
	if entry.ticker != nil {
		entry.ticker.Stop()
		entry.ticker = nil
	}
}

// stop 发出停止信号。sync.Once 保证并发调用(管理员连点两次开关、Apply 与 Unregister 撞车)
// 不会重复 close 而把进程 panic 出来。
func (entry *taskEntry) stop() {
	entry.stopOnce.Do(func() {
		close(entry.stopCh)
		entry.stopTicker()
	})
}

// run 执行一轮任务, 并把结果计入连续失败统计。
func (entry *taskEntry) run() {
	reason := runBody(entry.spec.Fn)
	if reason == "" {
		entry.markSuccess()
		return
	}
	consecutive := entry.markFailure()
	log.Warnf("task %s failed (%d consecutive): %s", entry.name, consecutive, reason)
	if consecutive >= taskAlertAfter {
		entry.alerted = true
		if OnTaskFailure != nil {
			OnTaskFailure(entry.name, consecutive, reason)
		}
	}
}

// runBody 执行任务体, 返回失败原因(空串表示成功)。
//
// panic 也折算成原因: 任务体是 `go entry.run()` 起的独立 goroutine,
// 改造前一处 panic 会直接带崩整个进程, 网关上所有在途请求一起消失;
// 现在恢复它并按一次失败计数, 让问题进告警而不是进进程退出码。
func runBody(fn func() error) (reason string) {
	defer func() {
		if r := recover(); r != nil {
			reason = "panic: " + panicText(r)
		}
	}()
	if err := fn(); err != nil {
		return err.Error()
	}
	return ""
}

type panicError struct{ value any }

func (e panicError) Error() string { return "panic: " + panicText(e.value) }

func panicText(value any) string {
	switch v := value.(type) {
	case error:
		return v.Error()
	case string:
		return v
	default:
		return "unexpected failure"
	}
}

// markFailure 累加连续失败次数并返回累计值。
func (entry *taskEntry) markFailure() int {
	tasksMu.Lock()
	defer tasksMu.Unlock()
	entry.failures++
	return entry.failures
}

// markSuccess 清零连续失败计数; 若此前已就此告过警, 补一条恢复通知,
// 免得管理员盯着"备份失败"的告警猜它后来好了没有。
func (entry *taskEntry) markSuccess() {
	tasksMu.Lock()
	wasAlerted := entry.alerted && entry.failures >= taskAlertAfter
	prev := entry.failures
	entry.failures = 0
	entry.alerted = false
	tasksMu.Unlock()
	if wasAlerted && OnTaskRecovered != nil {
		OnTaskRecovered(entry.name, prev)
	}
}

// FailureCount 返回任务当前连续失败次数, 供测试与运维面板读取。
func FailureCount(name string) int {
	tasksMu.Lock()
	defer tasksMu.Unlock()
	if entry, ok := tasks[name]; ok {
		return entry.failures
	}
	return 0
}

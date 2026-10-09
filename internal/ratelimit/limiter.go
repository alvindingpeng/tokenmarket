package ratelimit

import (
	"math"
	"sync"
	"time"
)

const windowSeconds = 60 // 固定窗口长度(秒)。

// Scope 限流主体: 一个可独立计量的范围, 如用户、API Key 或渠道凭据。
type Scope struct {
	Kind  string // model.RateScope* 的范围标识。
	ID    int    // 范围主键。
	Model string // 模型名; 空表示全部模型。
}

// Limits 生效限流值; 0 表示该维度不限。
type Limits struct {
	RPM        int64
	TPM        int64
	Concurrent int64 // 同一范围同时进行中的请求上限, 0 表示不限。
}

// Decision 一次额度预留的结果; 未放行时 RetryAfter 给出窗口刷新前的建议等待。
type Decision struct {
	Allowed           bool
	RetryAfter        time.Duration
	RemainingRPM      int64        // -1 表示不限。
	RemainingTPM      int64        // -1 表示不限。
	ConcurrentBlocked bool         // 并发占满, 无固定窗口重试时间。
	Reservation       *Reservation // 放行后必须用此票据结算或释放一次。
}

// Reservation 保存预留时的范围与分钟窗口; 结算严格作用于原窗口且只能执行一次。
type Reservation struct {
	limiter   *Limiter
	window    int64
	scopes    []Scope
	estTokens int64
	buckets   []*bucket
	settled   bool
}

type bucket struct {
	window   int64 // 窗口序号 = Unix 秒 / 60。
	requests int64
	tokens   int64
	forced   bool // 上游 429 强制触顶标记, 与是否配置策略无关, 随窗口过期。
}

// Limiter 进程内固定窗口限流器: 按 60 秒窗口计量请求数与 token 数, 预留后按真实用量结算。
// 多实例部署时以同一接口换成共享存储实现即可, 调用方无需改动。
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	active  map[string]int64   // 不依赖分钟窗口的活跃请求/流数。
	policy  func(Scope) Limits // 策略解析: 返回该范围生效的限流值。
	ops     int
	sweeper bool
	now     func() time.Time
	resolve func(Scope) []Scope
}

// New 创建限流器; policy 为策略解析函数(越具体越优先的语义由实现方保证)。
func New(policy func(Scope) Limits) *Limiter {
	return &Limiter{buckets: make(map[string]*bucket), active: make(map[string]int64), policy: policy, now: time.Now}
}

// NewResolved uses canonical scopes so global policies share counters across models.
func NewResolved(policy func(Scope) Limits, resolve func(Scope) []Scope) *Limiter {
	l := New(policy)
	l.resolve = resolve
	return l
}

func (l *Limiter) scopes(scopes []Scope) []Scope {
	result := make([]Scope, 0, len(scopes))
	seen := make(map[Scope]bool)
	for _, scope := range scopes {
		expanded := []Scope{scope}
		if l.resolve != nil {
			expanded = l.resolve(scope)
		}
		for _, s := range expanded {
			if !seen[s] {
				seen[s] = true
				result = append(result, s)
			}
		}
	}
	return result
}

func (l *Limiter) key(scope Scope, window int64) string {
	return scope.Kind + "\x00" + itoa(scope.ID) + "\x00" + scope.Model + "\x00" + itoa64(window)
}

func (l *Limiter) activeKey(scope Scope) string {
	return scope.Kind + "\x00" + itoa(scope.ID) + "\x00" + scope.Model
}

func (l *Limiter) bucketLocked(scope Scope, window int64) *bucket {
	key := l.key(scope, window)
	b := l.buckets[key]
	if b == nil {
		b = &bucket{window: window}
		l.buckets[key] = b
	}
	return b
}

// Reserve 为一批范围一次性预留额度: 全部范围均放行才扣减, 任一触顶则整批不扣。
// estInput/estOutput 为本次请求的 token 预估, 结束时以 Commit 按真实用量结算。
func (l *Limiter) Reserve(scopes []Scope, estInput, estOutput int64) Decision {
	scopes = l.scopes(scopes)
	estInput, estOutput = max(0, estInput), max(0, estOutput)
	estimated := tokenSum(estInput, estOutput)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepLocked()

	now := l.now()
	window := now.Unix() / windowSeconds
	type entry struct {
		b      *bucket
		limits Limits
	}
	entries := make([]entry, 0, len(scopes))
	for _, scope := range scopes {
		limits := l.policy(scope)
		b := l.bucketLocked(scope, window)
		if b.forced {
			return Decision{RetryAfter: windowEnd(window, now)}
		}
		if limits.Concurrent > 0 && l.active[l.activeKey(scope)] >= limits.Concurrent {
			return Decision{ConcurrentBlocked: true}
		}
		if limits.RPM > 0 && b.requests+1 > limits.RPM {
			return Decision{RetryAfter: windowEnd(window, now), RemainingTPM: remaining(limits.TPM, b.tokens)}
		}
		if limits.TPM > 0 && estimated > max(0, limits.TPM-b.tokens) {
			return Decision{RetryAfter: windowEnd(window, now), RemainingRPM: remaining(limits.RPM, b.requests)}
		}
		entries = append(entries, entry{b: b, limits: limits})
	}

	minRPM, minTPM := int64(-1), int64(-1)
	for i, e := range entries {
		l.active[l.activeKey(scopes[i])]++
		e.b.requests++
		e.b.tokens = tokenSum(e.b.tokens, estimated)
		if e.limits.RPM > 0 {
			if rem := e.limits.RPM - e.b.requests; minRPM < 0 || rem < minRPM {
				minRPM = rem
			}
		}
		if e.limits.TPM > 0 {
			if rem := e.limits.TPM - e.b.tokens; minTPM < 0 || rem < minTPM {
				minTPM = rem
			}
		}
	}
	refs := make([]*bucket, 0, len(entries))
	for _, e := range entries {
		refs = append(refs, e.b)
	}
	return Decision{Allowed: true, RemainingRPM: minRPM, RemainingTPM: minTPM, Reservation: &Reservation{limiter: l, window: window, scopes: append([]Scope(nil), scopes...), buckets: refs, estTokens: estimated}}
}

// Settle 按预留时的窗口结算且仅结算一次; 无用量时 release=true 归还 RPM/TPM。
func (r *Reservation) Settle(actualInput, actualOutput int64, release bool) {
	if r == nil || r.limiter == nil {
		return
	}
	l := r.limiter
	l.mu.Lock()
	defer l.mu.Unlock()
	if r.settled {
		return
	}
	r.settled = true
	for i, scope := range r.scopes {
		key := l.activeKey(scope)
		if l.active[key] > 1 {
			l.active[key]--
		} else {
			delete(l.active, key)
		}
		b := r.buckets[i]
		if release {
			b.requests = max(0, b.requests-1)
			b.tokens = max(0, b.tokens-r.estTokens)
		} else {
			b.tokens = tokenSum(max(0, b.tokens-r.estTokens), tokenSum(actualInput, actualOutput))
		}
	}
}

// Blocked 判断某范围当前窗口是否已触顶(用于选路时跳过受限成员)。
func (l *Limiter) Blocked(scope Scope) bool {
	for _, s := range l.scopes([]Scope{scope}) {
		if l.blocked(s) {
			return true
		}
	}
	return false
}

func (l *Limiter) blocked(scope Scope) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	window := now.Unix() / windowSeconds
	b := l.buckets[l.key(scope, window)]
	limits := l.policy(scope)
	if limits.Concurrent > 0 && l.active[l.activeKey(scope)] >= limits.Concurrent {
		return true
	}
	if b == nil {
		return false
	}
	// 上游 429 强制标记优先于策略: 即使没有配置限流, 被标记的范围本窗口也不再使用。
	if b.forced {
		return true
	}
	if limits.RPM <= 0 && limits.TPM <= 0 {
		return false
	}
	if limits.RPM > 0 && b.requests >= limits.RPM {
		return true
	}
	return limits.TPM > 0 && b.tokens >= limits.TPM
}

// MarkBlocked 将范围标记为当前窗口触顶(上游返回 429 时使用), 使其余请求直接跳过。
func (l *Limiter) MarkBlocked(scopes []Scope, estInput, estOutput int64) {
	scopes = l.scopes(scopes)
	l.mu.Lock()
	defer l.mu.Unlock()
	window := l.now().Unix() / windowSeconds
	for _, scope := range scopes {
		b := l.bucketLocked(scope, window)
		b.forced = true // 上游 429: 本窗口内跳过该范围, 与策略配置无关。
		// Forced blocking is independent of counters; do not manufacture token usage.
	}
}

// ConcurrentUsage 返回该范围目前持有的活跃租约数。
func (l *Limiter) ConcurrentUsage(scope Scope) int64 {
	return l.ConcurrentUsageExact(l.scopes([]Scope{scope})[0])
}

// ConcurrentUsageExact inspects one canonical counter without policy expansion.
func (l *Limiter) ConcurrentUsageExact(scope Scope) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.active[l.activeKey(scope)]
}

// Usage 返回某范围当前窗口的用量与窗口刷新时间。
func (l *Limiter) Usage(scope Scope) (requests, tokens int64, resetAt time.Time) {
	return l.UsageExact(l.scopes([]Scope{scope})[0])
}

// UsageExact inspects one canonical shared/model-specific counter.
func (l *Limiter) UsageExact(scope Scope) (requests, tokens int64, resetAt time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	window := now.Unix() / windowSeconds
	if b := l.buckets[l.key(scope, window)]; b != nil {
		requests, tokens = b.requests, b.tokens
	}
	return requests, tokens, time.Unix((window+1)*windowSeconds, 0)
}

// sweepLocked 定期清理过期窗口; 每 1024 次操作触发一次。
func (l *Limiter) sweepLocked() {
	l.ops++
	if l.ops%1024 != 0 {
		return
	}
	current := l.now().Unix() / windowSeconds
	for key, b := range l.buckets {
		if b.window < current-1 {
			delete(l.buckets, key)
		}
	}
}

func windowEnd(window int64, now time.Time) time.Duration {
	end := time.Unix((window+1)*windowSeconds, 0)
	d := end.Sub(now)
	if d < 0 {
		return 0
	}
	return d
}

func tokenSum(a, b int64) int64 {
	a, b = max(0, a), max(0, b)
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

func remaining(limit, used int64) int64 {
	if limit <= 0 {
		return -1
	}
	if rem := limit - used; rem > 0 {
		return rem
	}
	return 0
}

func itoa(v int) string { return itoa64(int64(v)) }
func itoa64(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [24]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

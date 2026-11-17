package ratelimit

import (
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
	"time"
)

// fixedPolicy 返回恒定限流值的策略解析器。
func fixedPolicy(rpm, tpm int64) func(Scope) Limits {
	return func(Scope) Limits { return Limits{RPM: rpm, TPM: tpm} }
}

func TestReserveEnforcesRPM(t *testing.T) {
	limiter := New(fixedPolicy(2, 0))
	scope := Scope{Kind: "user", ID: 1}
	for i := 0; i < 2; i++ {
		if decision := limiter.Reserve([]Scope{scope}, 10, 10); !decision.Allowed {
			t.Fatalf("第 %d 次预留应放行", i+1)
		}
	}
	decision := limiter.Reserve([]Scope{scope}, 10, 10)
	if decision.Allowed {
		t.Fatal("超出 RPM 的预留应被拒绝")
	}
	if decision.RetryAfter <= 0 || decision.RetryAfter > time.Duration(windowSeconds)*time.Second {
		t.Fatalf("RetryAfter 应落在窗口剩余时间内, got %v", decision.RetryAfter)
	}
	if decision.BlockedScope != scope {
		t.Fatalf("阻断范围=%+v, want %+v", decision.BlockedScope, scope)
	}
}

func TestReserveEnforcesTPM(t *testing.T) {
	limiter := New(fixedPolicy(0, 100))
	scope := Scope{Kind: "user", ID: 1}
	if decision := limiter.Reserve([]Scope{scope}, 60, 40); !decision.Allowed {
		t.Fatal("恰好用满 TPM 的预留应放行")
	}
	if decision := limiter.Reserve([]Scope{scope}, 1, 0); decision.Allowed {
		t.Fatal("超出 TPM 的预留应被拒绝")
	}
}

func TestReserveAtomicAcrossScopes(t *testing.T) {
	limiter := New(func(scope Scope) Limits {
		if scope.Kind == "api_key" {
			return Limits{RPM: 1}
		}
		return Limits{RPM: 0, TPM: 0}
	})
	scopes := []Scope{{Kind: "user", ID: 1}, {Kind: "api_key", ID: 2}}
	if decision := limiter.Reserve(scopes, 1, 1); !decision.Allowed {
		t.Fatal("首次批量预留应放行")
	}
	decision := limiter.Reserve(scopes, 1, 1)
	if decision.Allowed {
		t.Fatal("api_key 触顶时整批应拒绝")
	}
	if decision.BlockedScope != (Scope{Kind: "api_key", ID: 2}) {
		t.Fatalf("阻断范围=%+v, want api_key #2", decision.BlockedScope)
	}
	// 被拒批次不得扣减任何范围的额度: user 维度仍只有一次预留。
	requests, _, _ := limiter.Usage(Scope{Kind: "user", ID: 1})
	if requests != 1 {
		t.Fatalf("拒绝的批次不应扣减额度, got requests=%d", requests)
	}
}

func TestCommitSettlesTokens(t *testing.T) {
	limiter := New(fixedPolicy(0, 100))
	scope := Scope{Kind: "user", ID: 1}
	decision := limiter.Reserve([]Scope{scope}, 10, 40)
	if !decision.Allowed {
		t.Fatal("预留应放行")
	}
	decision.Reservation.Settle(5, 5, false) // 实际低于估算, 退还 40。
	_, tokens, _ := limiter.Usage(scope)
	if tokens != 10 {
		t.Fatalf("结算后 token 应为真实用量 10, got %d", tokens)
	}
	// 退还后应可再次预留满额度。
	if decision := limiter.Reserve([]Scope{scope}, 50, 40); !decision.Allowed {
		t.Fatal("结算退还后应可继续预留")
	}
}

func TestReleaseRefunds(t *testing.T) {
	limiter := New(fixedPolicy(1, 10))
	scope := Scope{Kind: "user", ID: 1}
	decision := limiter.Reserve([]Scope{scope}, 4, 4)
	if !decision.Allowed {
		t.Fatal("预留应放行")
	}
	decision.Reservation.Settle(0, 0, true)
	requests, tokens, _ := limiter.Usage(scope)
	if requests != 0 || tokens != 0 {
		t.Fatalf("退还后用量应清零, got requests=%d tokens=%d", requests, tokens)
	}
	if decision := limiter.Reserve([]Scope{scope}, 4, 4); !decision.Allowed {
		t.Fatal("退还后应可再次预留")
	}
}

func TestBlockedAndMarkBlocked(t *testing.T) {
	limiter := New(fixedPolicy(2, 0))
	scope := Scope{Kind: "channel_key", ID: 3}
	if limiter.Blocked(scope) {
		t.Fatal("无用量时不应判定触顶")
	}
	limiter.MarkBlocked([]Scope{scope}, 0, 0)
	if !limiter.Blocked(scope) {
		t.Fatal("标记后应判定触顶")
	}
	if decision := limiter.Reserve([]Scope{scope}, 1, 1); decision.Allowed {
		t.Fatal("触顶后的预留应被拒绝")
	}
}

func TestMarkBlockedWithoutPolicy(t *testing.T) {
	limiter := New(fixedPolicy(0, 0)) // 无任何策略也不放行被上游 429 标记的范围。
	scope := Scope{Kind: "channel", ID: 9}
	limiter.MarkBlocked([]Scope{scope}, 0, 0)
	if !limiter.Blocked(scope) {
		t.Fatal("无策略时上游 429 标记仍应触顶")
	}
	if decision := limiter.Reserve([]Scope{scope}, 1, 1); decision.Allowed {
		t.Fatal("被标记范围的预留应被拒绝")
	}
}

func TestNoLimitsAlwaysAllows(t *testing.T) {
	limiter := New(fixedPolicy(0, 0))
	scope := Scope{Kind: "system", ID: 0}
	for i := 0; i < 50; i++ {
		if decision := limiter.Reserve([]Scope{scope}, 1000, 1000); !decision.Allowed {
			t.Fatal("无限制时应始终放行")
		}
	}
}

// TestReserveProbeSkipsForcedButStillCountsQuota 钉住"探测只豁免上游 429 标记"。
//
// 两件事必须同时成立: 持有探测名额的请求能真的打到上游(否则标记永远清不掉),
// 但它仍要计入 RPM/并发额度(否则探测就成了绕过配额的后门)。
func TestReserveProbeSkipsForcedButStillCountsQuota(t *testing.T) {
	l := New(func(scope Scope) Limits { return Limits{} })
	scope := Scope{Kind: model.RateScopeChannel, ID: 1}
	l.MarkBlocked([]Scope{scope}, 1, 1)

	if d := l.Reserve([]Scope{scope}, 1, 1); d.Allowed {
		t.Fatal("普通预留必须被上游 429 标记挡住")
	}
	if d := l.ReserveProbe([]Scope{scope}, 1, 1); !d.Allowed {
		t.Fatal("探测预留必须放行: 否则探测打不到上游, 标记永远清不掉")
	}
	// 放行之后额度照算: 并发占用应已 +1。
	if n := l.ConcurrentUsage(scope); n != 1 {
		t.Fatalf("探测请求仍应计入并发额度: got %d", n)
	}
}

// TestClearBlockedLiftsNormalAdmission 是边界: 探测成功后普通请求也要能进。
func TestClearBlockedLiftsNormalAdmission(t *testing.T) {
	l := New(func(scope Scope) Limits { return Limits{} })
	scope := Scope{Kind: model.RateScopeChannel, ID: 1}
	l.MarkBlocked([]Scope{scope}, 1, 1)
	l.ClearBlocked([]Scope{scope})
	if d := l.Reserve([]Scope{scope}, 1, 1); !d.Allowed {
		t.Fatal("撤销标记后普通预留应放行: 否则探测成功也解不开锁")
	}
}

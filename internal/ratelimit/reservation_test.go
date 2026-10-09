package ratelimit

import (
	"sync"
	"testing"
	"time"
)

func TestReservationCrossWindowIdempotent(t *testing.T) {
	l := New(fixedPolicy(10, 1000))
	now := time.Unix(600, 0)
	l.now = func() time.Time { return now }
	s := Scope{Kind: "user", ID: 1}
	d := l.Reserve([]Scope{s}, 10, 90)
	now = now.Add(61 * time.Second)
	next := l.Reserve([]Scope{s}, 10, 10)
	d.Reservation.Settle(5, 5, false)
	d.Reservation.Settle(0, 0, true)
	if got := l.buckets[l.key(s, 10)].tokens; got != 10 {
		t.Fatalf("old window tokens=%d", got)
	}
	_, tokens, _ := l.Usage(s)
	if tokens != 20 {
		t.Fatalf("current window changed: %d", tokens)
	}
	if got := l.ConcurrentUsage(s); got != 1 {
		t.Fatalf("active=%d", got)
	}
	next.Reservation.Settle(0, 0, true)
}

func TestReservationAtomicConcurrencyAcrossWindows(t *testing.T) {
	l := New(func(s Scope) Limits {
		if s.Kind == "api_key" {
			return Limits{Concurrent: 1}
		}
		return Limits{}
	})
	now := time.Unix(600, 0)
	l.now = func() time.Time { return now }
	scopes := []Scope{{Kind: "user", ID: 1}, {Kind: "api_key", ID: 2}}
	first := l.Reserve(scopes, 5, 5)
	now = now.Add(61 * time.Second)
	if d := l.Reserve(scopes, 5, 5); d.Allowed || !d.ConcurrentBlocked {
		t.Fatal("concurrency reset on minute boundary")
	}
	req, _, _ := l.Usage(scopes[0])
	if req != 0 {
		t.Fatal("denied reservation debited scope")
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); first.Reservation.Settle(0, 0, true) }()
	}
	wg.Wait()
	if l.ConcurrentUsage(scopes[1]) != 0 {
		t.Fatal("lease leaked")
	}
	if !l.Reserve(scopes, 5, 5).Allowed {
		t.Fatal("lease not released")
	}
}

func TestSharedModelBudgetAndDeduplication(t *testing.T) {
	l := NewResolved(fixedPolicy(2, 0), func(s Scope) []Scope { s.Model = ""; return []Scope{s} })
	a := Scope{Kind: "user", ID: 1, Model: "a"}
	b := a
	b.Model = "b"
	d := l.Reserve([]Scope{a, a}, 1, 1)
	if !d.Allowed {
		t.Fatal("first")
	}
	if !l.Reserve([]Scope{b}, 1, 1).Allowed {
		t.Fatal("second")
	}
	if l.Reserve([]Scope{a}, 1, 1).Allowed {
		t.Fatal("shared limit split across models")
	}
	requests, _, _ := l.Usage(b)
	if requests != 2 {
		t.Fatalf("duplicate scope debited twice: %d", requests)
	}
}

func TestHugeTokenBudgetCannotOverflowLimit(t *testing.T) {
	l := New(fixedPolicy(0, 100))
	if l.Reserve([]Scope{{Kind: "user", ID: 1}}, 1, 1<<63-1).Allowed {
		t.Fatal("overflow bypassed TPM")
	}
}

func TestSettlementSurvivesSweep(t *testing.T) {
	l := New(fixedPolicy(2, 100))
	now := time.Unix(600, 0)
	l.now = func() time.Time { return now }
	d := l.Reserve([]Scope{{Kind: "user", ID: 1}}, 5, 5)
	now = now.Add(5 * time.Minute)
	l.ops = 1023
	l.sweepLocked()
	d.Reservation.Settle(2, 3, false)
	if d.Reservation.buckets[0].tokens != 5 {
		t.Fatal("original bucket lost")
	}
	if l.ConcurrentUsage(Scope{Kind: "user", ID: 1}) != 0 {
		t.Fatal("lease leaked after sweep")
	}
}

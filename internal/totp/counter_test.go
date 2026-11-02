package totp

import (
	"testing"
	"time"
)

func TestMatchStepReturnsCounter(t *testing.T) {
	now := time.Unix(1234567890, 0)
	for _, delta := range []int64{-30, 0, 30} {
		code, err := Generate(rfcSecret, now.Unix()+delta)
		if err != nil {
			t.Fatal(err)
		}
		step, ok := MatchStep(rfcSecret, code, 1, now)
		if !ok || step != (now.Unix()+delta)/Period {
			t.Fatalf("counter %d %v", step, ok)
		}
	}
	if _, ok := MatchStep("", "123456", 1, now); ok {
		t.Fatal("empty secret accepted")
	}
}

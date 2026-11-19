package op

import (
	"context"
	"errors"
	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"sync/atomic"
	"testing"
	"time"
)

func recoveryKey(t *testing.T) model.ChannelKey {
	t.Helper()
	breakerTestSetup(t)
	until := time.Now().Add(-time.Minute)
	key := model.ChannelKey{ChannelID: createBreakerTestChannel(t), ChannelKeyConfig: model.ChannelKeyConfig{Name: "probe-key", Key: "secret", Enabled: true, DisabledUntil: &until, ErrorCount: 2, BreakerTrips: 3}}
	if err := db.GetDB().Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	channelKeyCache.Set(key.ID, key)
	old := ProbeBreakerTarget
	t.Cleanup(func() { ProbeBreakerTarget = old; channelKeyCache.Del(key.ID) })
	return key
}

func TestBreakerRecoveryRequiresExecutor(t *testing.T) {
	key := recoveryKey(t)
	ProbeBreakerTarget = nil
	if err := BreakerRecoveryProbe(context.Background()); err == nil {
		t.Fatal("missing executor accepted")
	}
	var got model.ChannelKey
	db.GetDB().First(&got, key.ID)
	if got.DisabledUntil == nil || got.ProbeCount != 0 {
		t.Fatal("missing executor mutated breaker")
	}
}

func TestBreakerRecoveryClaimSingleFlight(t *testing.T) {
	key := recoveryKey(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	var calls atomic.Int32
	ProbeBreakerTarget = func(ctx context.Context, ch, k, m int) error {
		if ch != key.ChannelID || k != key.ID || m != 0 {
			t.Errorf("wrong target")
		}
		calls.Add(1)
		close(entered)
		<-release
		return nil
	}
	go func() { done <- BreakerRecoveryProbe(context.Background()) }()
	<-entered
	// Both calls use the same stale snapshot; only the first conditional DB update may claim it.
	if err := recoverBreakerTarget(context.Background(), &model.ChannelKey{}, true, keySnapshot(key)); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate probe")
	}
	cached, _ := channelKeyCache.Get(key.ID)
	if cached.DisabledUntil == nil || !cached.DisabledUntil.After(time.Now()) {
		t.Fatal("claim not cached")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var got model.ChannelKey
	db.GetDB().First(&got, key.ID)
	if got.DisabledUntil != nil || got.ProbeAt != nil || got.ProbeCount != 0 || got.ErrorCount != 0 || got.BreakerTrips != 0 {
		t.Fatalf("not reset: %+v", got)
	}
	cached, _ = channelKeyCache.Get(key.ID)
	if cached.DisabledUntil != nil || cached.ErrorCount != 0 {
		t.Fatal("success cache stale")
	}
}

func TestBreakerRecoveryFailureKeepsIsolation(t *testing.T) {
	key := recoveryKey(t)
	ProbeBreakerTarget = func(context.Context, int, int, int) error { return errors.New("bad auth") }
	if err := BreakerRecoveryProbe(context.Background()); err != nil {
		t.Fatal(err)
	}
	var got model.ChannelKey
	db.GetDB().First(&got, key.ID)
	if got.DisabledUntil == nil || !got.DisabledUntil.After(time.Now()) || got.ProbeCount != 1 || got.BreakerTrips != 3 || got.ErrorCount != 2 {
		t.Fatalf("failure cleared breaker: %+v", got)
	}
}

func TestBreakerRecoveryCASPreservesNewFailure(t *testing.T) {
	key := recoveryKey(t)
	ProbeBreakerTarget = func(context.Context, int, int, int) error { RecordChannelKeyError(key.ID, "auth"); return nil }
	if err := BreakerRecoveryProbe(context.Background()); err != nil {
		t.Fatal(err)
	}
	var got model.ChannelKey
	db.GetDB().First(&got, key.ID)
	if got.DisabledUntil == nil || got.BreakerTrips != 4 || got.LastErrorAt == nil {
		t.Fatal("probe overwrote newer failure")
	}
	cached, _ := channelKeyCache.Get(key.ID)
	if cached.DisabledUntil == nil || cached.BreakerTrips != 4 {
		t.Fatal("probe overwrote newer cached failure")
	}
}

func TestBreakerRecoveryModelClaimAndCAS(t *testing.T) {
	breakerTestSetup(t)
	until := time.Now().Add(-time.Minute)
	cm := model.ChannelModel{ChannelID: createBreakerTestChannel(t), Name: "text", DisabledUntil: &until, BreakerTrips: 2}
	if err := db.GetDB().Create(&cm).Error; err != nil {
		t.Fatal(err)
	}
	channelModelCache.Set(cm.ID, cm)
	t.Cleanup(func() { channelModelCache.Del(cm.ID) })
	old := ProbeBreakerTarget
	t.Cleanup(func() { ProbeBreakerTarget = old })
	ProbeBreakerTarget = func(ctx context.Context, ch, k, m int) error {
		if ch != cm.ChannelID || k != 0 || m != cm.ID {
			t.Fatal("wrong model target")
		}
		return nil
	}
	if err := BreakerRecoveryProbe(context.Background()); err != nil {
		t.Fatal(err)
	}
	var got model.ChannelModel
	db.GetDB().First(&got, cm.ID)
	if got.DisabledUntil != nil || got.BreakerTrips != 0 {
		t.Fatal("model not recovered")
	}
}

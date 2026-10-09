package relay

import (
	"context"
	"errors"
	"github.com/bestruirui/octopus/internal/model"
	"testing"
	"time"
)

func TestQueueCapacityFIFOAndCleanup(t *testing.T) {
	q := relayWaitQueue{groups: make(map[int][]*waitTicket)}
	config := model.GroupRelayConfig{MaxWaitingRequests: 2, MaxWaitSeconds: 1}
	a := requestWait{queue: &q, group: 1}
	b := requestWait{queue: &q, group: 1}
	c := requestWait{queue: &q, group: 1}
	if a.join(config) != nil || b.join(config) != nil {
		t.Fatal("enqueue failed")
	}
	if !errors.Is(c.join(config), errQueueFull) {
		t.Fatal("capacity exceeded")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if b.turn(ctx) != context.Canceled {
		t.Fatal("cancel not honored")
	}
	if a.turn(context.Background()) != nil {
		t.Fatal("head not eligible")
	}
	a.leave()
	if b.turn(context.Background()) != nil {
		t.Fatal("next not promoted")
	}
	b.leave()
	a.leave()
	if q.pending(1) {
		t.Fatal("queue slot leaked")
	}
}

func TestQueueDisabledExpiryAndCumulativeWait(t *testing.T) {
	q := relayWaitQueue{groups: make(map[int][]*waitTicket)}
	w := requestWait{queue: &q, group: 2}
	if !errors.Is(w.join(model.GroupRelayConfig{}), errQueueDisabled) {
		t.Fatal("disabled queue admitted")
	}
	config := model.GroupRelayConfig{MaxWaitingRequests: 1, MaxWaitSeconds: 1}
	if w.join(config) != nil {
		t.Fatal("join")
	}
	w.ticket.deadline = time.Now().Add(-time.Second)
	if !errors.Is(w.turn(context.Background()), errQueueTimeout) {
		t.Fatal("expired wait accepted")
	}
	w.leave()
	w.waited = 2 * time.Second
	if !errors.Is(w.join(config), errQueueTimeout) {
		t.Fatal("wait budget reset on retry")
	}
}

package relay

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

var errQueueDisabled = errors.New("no available upstream; waiting disabled")
var errQueueFull = errors.New("upstream waiting queue full")
var errQueueTimeout = errors.New("upstream waiting deadline exceeded")

type relayWaitQueue struct {
	mu     sync.Mutex
	groups map[int][]*waitTicket
}
type waitTicket struct{ deadline time.Time }

var waiting = relayWaitQueue{groups: make(map[int][]*waitTicket)}

type requestWait struct {
	queue   *relayWaitQueue
	group   int
	ticket  *waitTicket
	waited  time.Duration
	entered time.Time
}

func (q *relayWaitQueue) pending(group int) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.groups[group]) > 0
}
func (w *requestWait) leave() {
	if w.ticket == nil {
		return
	}
	w.queue.mu.Lock()
	items := w.queue.groups[w.group]
	for i, t := range items {
		if t == w.ticket {
			items = append(items[:i], items[i+1:]...)
			break
		}
	}
	if len(items) == 0 {
		delete(w.queue.groups, w.group)
	} else {
		w.queue.groups[w.group] = items
	}
	w.queue.mu.Unlock()
	w.waited += time.Since(w.entered)
	w.ticket = nil
}
func (w *requestWait) join(config model.GroupRelayConfig) error {
	if w.ticket != nil {
		return nil
	}
	if config.MaxWaitSeconds <= 0 || config.MaxWaitingRequests <= 0 {
		return errQueueDisabled
	}
	remaining := time.Duration(config.MaxWaitSeconds)*time.Second - w.waited
	if remaining <= 0 {
		return errQueueTimeout
	}
	w.queue.mu.Lock()
	defer w.queue.mu.Unlock()
	if len(w.queue.groups[w.group]) >= config.MaxWaitingRequests {
		return errQueueFull
	}
	w.entered = time.Now()
	w.ticket = &waitTicket{deadline: w.entered.Add(remaining)}
	w.queue.groups[w.group] = append(w.queue.groups[w.group], w.ticket)
	return nil
}
func (w *requestWait) turn(ctx context.Context) error {
	if w.ticket == nil {
		return nil
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !time.Now().Before(w.ticket.deadline) {
			return errQueueTimeout
		}
		w.queue.mu.Lock()
		head := len(w.queue.groups[w.group]) > 0 && w.queue.groups[w.group][0] == w.ticket
		w.queue.mu.Unlock()
		if head {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
func (w *requestWait) pause(ctx context.Context, config model.GroupRelayConfig) error {
	if err := w.join(config); err != nil {
		return err
	}
	delay := min(100*time.Millisecond, time.Until(w.ticket.deadline))
	if delay <= 0 {
		return errQueueTimeout
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	return w.turn(ctx)
}

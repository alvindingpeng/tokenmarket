package relay

import (
	"sync"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
)

type autoCircuitKey struct {
	groupID int
	task    string
}

type autoCircuitState struct {
	Failures    int
	OpenUntil   time.Time
	LastFailure time.Time
}

var (
	autoCircuitMu sync.Mutex
	autoCircuits  = make(map[autoCircuitKey]autoCircuitState)
)

func autoCircuitFailureLimit(group model.Group) int {
	if group.RelayConfig.AutoFallbackFailures > 0 {
		return group.RelayConfig.AutoFallbackFailures
	}
	return 3
}

func autoCircuitOpenFor(group model.Group) time.Duration {
	if group.RelayConfig.AutoFallbackSeconds > 0 {
		return time.Duration(group.RelayConfig.AutoFallbackSeconds) * time.Second
	}
	return time.Minute
}

// autoRampHit applies the configured percentage to a stable per-key bucket.
// Zero preserves v1.16.6's 100% behavior for already-active groups.
func autoRampHit(groupID, keyID, userID, percent int) bool {
	if percent <= 0 || percent >= 100 {
		return true
	}
	seed := uint64(groupID) * 0x9E3779B97F4A7C15
	if keyID > 0 {
		seed ^= uint64(keyID) * 0xC2B2AE3D27D4EB4F
	} else {
		seed ^= uint64(userID) * 0x165667B19E3779F9
	}
	bucket := int((seed ^ (seed >> 29)) % 100)
	return bucket < percent
}

func autoCircuitOpen(groupID int, task string) bool {
	key := autoCircuitKey{groupID: groupID, task: task}
	now := time.Now()
	autoCircuitMu.Lock()
	defer autoCircuitMu.Unlock()
	state := autoCircuits[key]
	if state.OpenUntil.After(now) {
		return true
	}
	if !state.OpenUntil.IsZero() {
		delete(autoCircuits, key)
	}
	return false
}

func autoCircuitRecordSuccess(groupID int, task string) {
	key := autoCircuitKey{groupID: groupID, task: task}
	autoCircuitMu.Lock()
	delete(autoCircuits, key)
	autoCircuitMu.Unlock()
}

func autoCircuitRecordFailure(group model.Group, task string) bool {
	key := autoCircuitKey{groupID: group.ID, task: task}
	now := time.Now()
	autoCircuitMu.Lock()
	defer autoCircuitMu.Unlock()
	state := autoCircuits[key]
	state.Failures++
	state.LastFailure = now
	if state.Failures >= autoCircuitFailureLimit(group) {
		state.OpenUntil = now.Add(autoCircuitOpenFor(group))
	}
	autoCircuits[key] = state
	return state.OpenUntil.After(now)
}

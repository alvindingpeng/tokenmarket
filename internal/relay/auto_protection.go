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

type autoStickyKey struct {
	GroupID  int
	KeyID    int
	UserID   uint
	Task     string
	Protocol model.Protocol
}

type autoStickyState struct {
	ItemID    int
	ExpiresAt time.Time
}

var (
	autoCircuitMu sync.Mutex
	autoCircuits  = make(map[autoCircuitKey]autoCircuitState)
	autoStickyMu  sync.Mutex
	autoSticky    = make(map[autoStickyKey]autoStickyState)
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

func autoTaskRampHit(group model.Group, task string, keyID int, userID uint) bool {
	percent, configured := group.RelayConfig.AutoTaskRampPercent[task]
	if !configured {
		percent = group.RelayConfig.AutoRampPercent
	}
	return (!configured || percent > 0) && autoRampHit(group.ID, keyID, int(userID), percent)
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

func autoStickyKeyFor(groupID, keyID int, userID uint, task string, protocol model.Protocol) autoStickyKey {
	return autoStickyKey{GroupID: groupID, KeyID: keyID, UserID: userID, Task: task, Protocol: protocol}
}

// autoStickyGet returns the recently successful member for this task. Zero or
// expired state is a miss, including legacy groups that do not enable sticky
// routing. The caller must still pass the returned item through the current
// candidate filters.
func autoStickyGet(group model.Group, keyID int, userID uint, task string, protocol model.Protocol) (int, bool) {
	if group.RelayConfig.AutoStickySeconds <= 0 {
		return 0, false
	}
	key := autoStickyKeyFor(group.ID, keyID, userID, task, protocol)
	now := time.Now()
	autoStickyMu.Lock()
	defer autoStickyMu.Unlock()
	state, ok := autoSticky[key]
	if !ok || !state.ExpiresAt.After(now) {
		delete(autoSticky, key)
		return 0, false
	}
	return state.ItemID, true
}

func autoStickyRecordSuccess(group model.Group, keyID int, userID uint, task string, protocol model.Protocol, itemID int) {
	if group.RelayConfig.AutoStickySeconds <= 0 || itemID == 0 {
		return
	}
	key := autoStickyKeyFor(group.ID, keyID, userID, task, protocol)
	autoStickyMu.Lock()
	autoSticky[key] = autoStickyState{
		ItemID: itemID, ExpiresAt: time.Now().Add(time.Duration(group.RelayConfig.AutoStickySeconds) * time.Second),
	}
	autoStickyMu.Unlock()
}

func autoStickyClear(groupID, keyID int, userID uint, task string, protocol model.Protocol) {
	key := autoStickyKeyFor(groupID, keyID, userID, task, protocol)
	autoStickyMu.Lock()
	delete(autoSticky, key)
	autoStickyMu.Unlock()
}

func autoStickyReset(groupID int) {
	autoStickyMu.Lock()
	for key := range autoSticky {
		if key.GroupID == groupID {
			delete(autoSticky, key)
		}
	}
	autoStickyMu.Unlock()
}

func autoItemByID(items []model.GroupItem, id int) (model.GroupItem, bool) {
	for _, item := range items {
		if item.ID == id {
			return item, true
		}
	}
	return model.GroupItem{}, false
}

// autoExploreHit is a stable per-key sampling decision. Exploration deliberately
// caps at 20% at configuration normalization; callers only use it for tasks that
// have safe protocol/context eligibility but lack verified quality evidence.
func autoExploreHit(groupID, keyID, userID, percent int, task string) bool {
	if percent <= 0 {
		return false
	}
	if percent > 20 {
		percent = 20
	}
	seed := uint64(groupID) * 0x9E3779B97F4A7C15
	if keyID > 0 {
		seed ^= uint64(keyID) * 0xC2B2AE3D27D4EB4F
	} else {
		seed ^= uint64(userID) * 0x165667B19E3779F9
	}
	for index := 0; index < len(task); index++ {
		seed ^= uint64(task[index]) << ((index % 8) * 8)
	}
	bucket := int((seed ^ (seed >> 29)) % 100)
	return bucket < percent
}

func autoResetForGroup(groupID int) {
	autoPerformanceReset(groupID)
	autoCircuitMu.Lock()
	for key := range autoCircuits {
		if key.groupID == groupID {
			delete(autoCircuits, key)
		}
	}
	autoCircuitMu.Unlock()
	autoStickyReset(groupID)
}

// ResetAutoState clears circuit and sticky state for a group after its routing
// mode changes or it is deleted. Tests also use it to keep process-local keys
// isolated between SQLite fixtures.
func ResetAutoState(groupID int) {
	autoResetForGroup(groupID)
}

package relay

import (
	"sync"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
)

type autoPerformanceKey struct {
	groupID, itemID int
	task, band      string
	protocol        model.Protocol
	stream          bool
}

type autoPerformanceSample struct {
	success    bool
	firstToken time.Duration
}

type autoPerformanceWindow struct {
	samples      [20]autoPerformanceSample
	total        int
	failures     int
	contextUntil time.Time
	retryUntil   time.Time
	updated      time.Time
}

var autoPerformance = struct {
	sync.Mutex
	windows   map[autoPerformanceKey]autoPerformanceWindow
	lastPrune time.Time
}{windows: make(map[autoPerformanceKey]autoPerformanceWindow)}

func autoPerformanceKeyFor(groupID, itemID int, task autoTask) autoPerformanceKey {
	return autoPerformanceKey{groupID, itemID, task.name, model.AutoInputBand(task.input), task.protocol, task.stream}
}

func recordAutoPerformance(group model.Group, itemID int, task autoTask, class ErrorClass, success bool, firstToken time.Duration) {
	if task.input <= 0 || task.name == AutoTaskUnsupported || itemID == 0 {
		return
	}
	if !success && (class == ErrorClassClient || class == ErrorClassPolicy || class == ErrorClassRate) {
		return
	}
	key := autoPerformanceKeyFor(group.ID, itemID, task)
	now := time.Now()
	autoPerformance.Lock()
	defer autoPerformance.Unlock()
	if len(autoPerformance.windows) >= 4096 && now.Sub(autoPerformance.lastPrune) >= time.Minute {
		for oldKey, value := range autoPerformance.windows {
			if now.Sub(value.updated) >= time.Hour {
				delete(autoPerformance.windows, oldKey)
			}
		}
		autoPerformance.lastPrune = now
	}
	window, exists := autoPerformance.windows[key]
	if len(autoPerformance.windows) >= 4096 && !exists {
		return
	}
	if now.Sub(window.updated) >= time.Hour {
		window = autoPerformanceWindow{}
	}
	window.samples[window.total%len(window.samples)] = autoPerformanceSample{success, firstToken}
	window.total++
	window.updated = now
	if success {
		window.failures = 0
	} else {
		window.failures++
		if class == ErrorClassContext {
			window.contextUntil = now.Add(10 * time.Minute)
		}
		if window.failures >= autoCircuitFailureLimit(group) {
			window.retryUntil = now.Add(autoCircuitOpenFor(group))
		}
	}
	autoPerformance.windows[key] = window
}

func autoPerformancePenalty(groupID, itemID int, task autoTask) float64 {
	autoPerformance.Lock()
	window := autoPerformance.windows[autoPerformanceKeyFor(groupID, itemID, task)]
	autoPerformance.Unlock()
	now := time.Now()
	if now.Sub(window.updated) >= time.Hour {
		return 1
	}
	if window.retryUntil.After(now) || window.contextUntil.After(now) {
		return 0.50
	}
	count := min(window.total, len(window.samples))
	if count < 5 {
		return 1
	}
	var failures, slowOrIncomplete, streams int
	for _, sample := range window.samples[:count] {
		if !sample.success {
			failures++
		}
		if sample.firstToken > 0 {
			streams++
			if !sample.success || sample.firstToken > 30*time.Second {
				slowOrIncomplete++
			}
		}
	}
	penalty := 1 - 0.4*float64(failures)/float64(count)
	if streams >= 5 {
		penalty *= 1 - 0.3*float64(slowOrIncomplete)/float64(streams)
	}
	return penalty
}

func autoPerformanceReset(groupID int) {
	autoPerformance.Lock()
	defer autoPerformance.Unlock()
	for key := range autoPerformance.windows {
		if key.groupID == groupID {
			delete(autoPerformance.windows, key)
		}
	}
}

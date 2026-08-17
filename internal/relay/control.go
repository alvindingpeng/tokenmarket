package relay

import (
	"context"
	"sync"
)

// attemptControl 保存活动请求当前可中止的 Attempt。
type attemptControl struct {
	index  int                // 当前尝试序号。
	cancel context.CancelFunc // 用于中止当前尝试。
}

var (
	attemptMu sync.Mutex                        // attemptMu 保护当前运行 Attempt 索引。
	attempts  = make(map[uint64]attemptControl) // attempts 按请求 ID 保存当前运行的 Attempt。
)

// InterruptAttempt 中止指定请求当前仍在运行且序号匹配的上游尝试，找不到匹配尝试时不执行操作。
func InterruptAttempt(requestID uint64, attemptIndex int) {
	attemptMu.Lock()
	attempt, ok := attempts[requestID]
	if !ok || attempt.index != attemptIndex {
		attemptMu.Unlock()
		return
	}
	delete(attempts, requestID)
	attemptMu.Unlock()
	attempt.cancel()
}

// setAttempt 发布当前请求新建的可中止 Attempt。
func setAttempt(requestID uint64, attemptIndex int, cancel context.CancelFunc) {
	attemptMu.Lock()
	attempts[requestID] = attemptControl{index: attemptIndex, cancel: cancel}
	attemptMu.Unlock()
}

// clearAttempt 原子移除序号匹配的 Attempt，返回 false 表示中止操作已经先发生。
func clearAttempt(requestID uint64, attemptIndex int) bool {
	attemptMu.Lock()
	defer attemptMu.Unlock()
	attempt, ok := attempts[requestID]
	if !ok || attempt.index != attemptIndex {
		return false
	}
	delete(attempts, requestID)
	return true
}

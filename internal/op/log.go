package op

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/utils/snowflake"
)

const relayLogMaxSize = 20

var relayLogCache = make([]model.RelayLog, 0, relayLogMaxSize)
var relayLogCacheLock sync.Mutex

var relayLogSubscribers = make(map[chan model.RelayLog]struct{})
var relayLogSubscribersLock sync.RWMutex

var relayLogStreamTokens = make(map[string]struct{})
var relayLogStreamTokensLock sync.RWMutex

func RelayLogStreamTokenCreate() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	token := hex.EncodeToString(bytes)

	relayLogStreamTokensLock.Lock()
	relayLogStreamTokens[token] = struct{}{}
	relayLogStreamTokensLock.Unlock()

	return token, nil
}

func RelayLogStreamTokenVerify(token string) bool {
	relayLogStreamTokensLock.RLock()
	_, ok := relayLogStreamTokens[token]
	relayLogStreamTokensLock.RUnlock()
	return ok
}

func RelayLogStreamTokenRevoke(token string) {
	relayLogStreamTokensLock.Lock()
	delete(relayLogStreamTokens, token)
	relayLogStreamTokensLock.Unlock()
}

func RelayLogSubscribe() chan model.RelayLog {
	ch := make(chan model.RelayLog, 10)
	relayLogSubscribersLock.Lock()
	relayLogSubscribers[ch] = struct{}{}
	relayLogSubscribersLock.Unlock()
	return ch
}

func RelayLogUnsubscribe(ch chan model.RelayLog) {
	relayLogSubscribersLock.Lock()
	delete(relayLogSubscribers, ch)
	relayLogSubscribersLock.Unlock()
	close(ch)
}

func notifySubscribers(relayLog model.RelayLog) {
	relayLogSubscribersLock.RLock()
	defer relayLogSubscribersLock.RUnlock()

	for ch := range relayLogSubscribers {
		select {
		case ch <- relayLog:
		default:
		}
	}
}

func RelayLogAdd(ctx context.Context, relayLog model.RelayLog) error {
	relayLog.ID = snowflake.GenerateID()

	relayLogCacheLock.Lock()
	defer relayLogCacheLock.Unlock()
	relayLogCache = append(relayLogCache, relayLog)
	go notifySubscribers(relayLog)
	if len(relayLogCache) >= relayLogMaxSize {
		return relayLogSaveDBLocked(ctx)
	}
	return nil
}

func RelayLogSaveDBTask(ctx context.Context) error {
	relayLogCacheLock.Lock()
	defer relayLogCacheLock.Unlock()

	if len(relayLogCache) > 0 {
		if err := relayLogSaveDBLocked(ctx); err != nil {
			return err
		}
	}

	return relayLogCleanup(ctx)
}

func relayLogCleanup(ctx context.Context) error {
	keepPeriod, err := SettingGetInt(model.SettingKeyRelayLogKeepPeriod)
	if err != nil {
		return err
	}

	if keepPeriod <= 0 {
		return nil
	}

	cutoffTime := time.Now().Add(-time.Duration(keepPeriod) * 24 * time.Hour).Unix()
	return db.GetDB().WithContext(ctx).Where("time < ?", cutoffTime).Delete(&model.RelayLog{}).Error
}

func relayLogSaveDBLocked(ctx context.Context) error {
	if len(relayLogCache) == 0 {
		return nil
	}

	result := db.GetDB().WithContext(ctx).Create(&relayLogCache)
	if result.Error != nil {
		return result.Error
	}

	relayLogCache = make([]model.RelayLog, 0, relayLogMaxSize)
	return nil
}

func RelayLogList(ctx context.Context, page, pageSize int) ([]model.RelayLog, error) {
	// 获取缓存中的日志（尚未保存到数据库的，这些是最新的）
	relayLogCacheLock.Lock()
	cachedLogs := make([]model.RelayLog, len(relayLogCache))
	copy(cachedLogs, relayLogCache)
	relayLogCacheLock.Unlock()

	// 反转缓存日志顺序（原本新的在末尾，反转后新的在前面，方便分页）
	for i, j := 0, len(cachedLogs)-1; i < j; i, j = i+1, j-1 {
		cachedLogs[i], cachedLogs[j] = cachedLogs[j], cachedLogs[i]
	}

	cacheCount := len(cachedLogs)
	offset := (page - 1) * pageSize

	var result []model.RelayLog

	// 先从缓存中取（缓存是最新的日志）
	if offset < cacheCount {
		cacheEnd := offset + pageSize
		if cacheEnd > cacheCount {
			cacheEnd = cacheCount
		}
		result = append(result, cachedLogs[offset:cacheEnd]...)
	}

	// 缓存不够，从数据库补充
	remaining := pageSize - len(result)
	if remaining > 0 {
		// 计算数据库的偏移量：如果 offset 超过了缓存数量，需要跳过数据库中的一些记录
		dbOffset := 0
		if offset > cacheCount {
			dbOffset = offset - cacheCount
		}

		var dbLogs []model.RelayLog
		if err := db.GetDB().WithContext(ctx).Order("id DESC").Offset(dbOffset).Limit(remaining).Find(&dbLogs).Error; err != nil {
			return nil, err
		}
		result = append(result, dbLogs...)
	}

	return result, nil
}

func RelayLogListByTime(ctx context.Context, startTime, endTime int, page, pageSize int) ([]model.RelayLog, error) {
	// 获取缓存中符合时间范围的日志
	relayLogCacheLock.Lock()
	var cachedLogs []model.RelayLog
	for _, log := range relayLogCache {
		if log.Time >= int64(startTime) && log.Time <= int64(endTime) {
			cachedLogs = append(cachedLogs, log)
		}
	}
	relayLogCacheLock.Unlock()

	// 反转缓存日志顺序（原本新的在末尾，反转后新的在前面，方便分页）
	for i, j := 0, len(cachedLogs)-1; i < j; i, j = i+1, j-1 {
		cachedLogs[i], cachedLogs[j] = cachedLogs[j], cachedLogs[i]
	}

	cacheCount := len(cachedLogs)
	offset := (page - 1) * pageSize

	var result []model.RelayLog

	// 先从缓存中取（缓存是最新的日志）
	if offset < cacheCount {
		cacheEnd := offset + pageSize
		if cacheEnd > cacheCount {
			cacheEnd = cacheCount
		}
		result = append(result, cachedLogs[offset:cacheEnd]...)
	}

	// 缓存不够，从数据库补充
	remaining := pageSize - len(result)
	if remaining > 0 {
		dbOffset := 0
		if offset > cacheCount {
			dbOffset = offset - cacheCount
		}

		var dbLogs []model.RelayLog
		query := db.GetDB().WithContext(ctx).Where("time >= ? AND time <= ?", startTime, endTime)
		if err := query.Order("id DESC").Offset(dbOffset).Limit(remaining).Find(&dbLogs).Error; err != nil {
			return nil, err
		}
		result = append(result, dbLogs...)
	}

	return result, nil
}

func RelayLogClear(ctx context.Context) error {
	return db.GetDB().WithContext(ctx).Where("1 = 1").Delete(&model.RelayLog{}).Error
}

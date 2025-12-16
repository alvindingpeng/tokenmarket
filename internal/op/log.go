package op

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
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
			// channel 已满，跳过
		}
	}
}

func RelayLogAdd(ctx context.Context, relayLog model.RelayLog) error {
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

	// 清理历史日志
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
	var logs []model.RelayLog

	offset := (page - 1) * pageSize
	if err := db.GetDB().WithContext(ctx).Order("id DESC").Offset(offset).Limit(pageSize).Find(&logs).Error; err != nil {
		return nil, err
	}

	return logs, nil
}

func RelayLogListByTime(ctx context.Context, startTime, endTime int, page, pageSize int) ([]model.RelayLog, error) {
	var logs []model.RelayLog

	query := db.GetDB().WithContext(ctx).Where("time >= ? AND time <= ?", startTime, endTime)
	offset := (page - 1) * pageSize
	if err := query.Order("id DESC").Offset(offset).Limit(pageSize).Find(&logs).Error; err != nil {
		return nil, err
	}

	return logs, nil
}

func RelayLogClear(ctx context.Context) error {
	return db.GetDB().WithContext(ctx).Where("1 = 1").Delete(&model.RelayLog{}).Error
}

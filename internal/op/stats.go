package op

import (
	"context"
	"sync"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/utils/cache"
	"github.com/alvindingpeng/switchyard/internal/utils/log"
	"github.com/alvindingpeng/switchyard/internal/utils/timeo"
)

var statsDailyCache model.StatsDaily
var statsDailyCacheLock sync.RWMutex

var statsTotalCache model.StatsTotal
var statsTotalCacheLock sync.RWMutex

var statsChannelCache = cache.New[int, model.StatsChannel](16)
var statsChannelCacheNeedUpdate = make(map[int]struct{})

func StatsSaveDBTask() {
	interval, err := SettingGetInt(model.SettingKeyStatsSaveInterval)
	if err != nil {
		return
	}
	for {
		time.Sleep(time.Duration(interval) * time.Minute)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := StatsSaveDB(ctx); err != nil {
				log.Errorf("stats save db error: %v", err)
			}
		}()
	}
}

func StatsSaveDB(ctx context.Context) error {
	db := db.GetDB().WithContext(ctx)
	statsTotalCacheLock.Lock()
	defer statsTotalCacheLock.Unlock()
	result := db.Save(&statsTotalCache)
	if result.Error != nil {
		return result.Error
	}
	statsDailyCacheLock.Lock()
	defer statsDailyCacheLock.Unlock()
	result = db.Save(&statsDailyCache)
	if result.Error != nil {
		return result.Error
	}
	for id := range statsChannelCacheNeedUpdate {
		delete(statsChannelCacheNeedUpdate, id)
		cache, ok := statsChannelCache.Get(id)
		if !ok {
			continue
		}
		result = db.Save(&cache)
		if result.Error != nil {
			return result.Error
		}
	}
	return nil
}

func StatsDailyUpdate(ctx context.Context, stats model.StatsDaily) error {
	statsDailyCacheLock.Lock()
	defer statsDailyCacheLock.Unlock()
	if timeo.ToMidnight(statsDailyCache.Date) != timeo.GetMidnight() {
		if err := StatsSaveDB(ctx); err != nil {
			return err
		}
		statsDailyCache = model.StatsDaily{
			Date: timeo.GetMidnight(),
		}
	}
	statsDailyCache.InputToken += stats.InputToken
	statsDailyCache.OutputToken += stats.OutputToken
	statsDailyCache.RequestCount += stats.RequestCount
	statsDailyCache.InputCost += stats.InputCost
	statsDailyCache.OutputCost += stats.OutputCost
	statsDailyCache.WaitTime += stats.WaitTime
	return nil
}

func StatsTotalUpdate(stats model.StatsTotal) error {
	statsTotalCacheLock.Lock()
	defer statsTotalCacheLock.Unlock()
	statsTotalCache.InputToken += stats.InputToken
	statsTotalCache.OutputToken += stats.OutputToken
	statsTotalCache.RequestCount += stats.RequestCount
	statsTotalCache.InputCost += stats.InputCost
	statsTotalCache.OutputCost += stats.OutputCost
	statsTotalCache.WaitTime += stats.WaitTime
	return nil
}

func StatsChannelUpdate(ctx context.Context, stats model.StatsChannel) error {
	channelCache, ok := statsChannelCache.Get(stats.ChannelID)
	if !ok {
		channelCache = model.StatsChannel{
			ChannelID: stats.ChannelID,
		}
	}
	channelCache.InputToken += stats.InputToken
	channelCache.OutputToken += stats.OutputToken
	channelCache.InputCost += stats.InputCost
	channelCache.OutputCost += stats.OutputCost
	channelCache.RequestSuccess += stats.RequestSuccess
	channelCache.RequestFailed += stats.RequestFailed
	statsChannelCache.Set(stats.ChannelID, channelCache)
	statsChannelCacheNeedUpdate[stats.ChannelID] = struct{}{}
	return nil
}

func StatsChannelDel(id int) error {
	if _, ok := statsChannelCache.Get(id); !ok {
		return nil
	}
	statsChannelCache.Del(id)
	delete(statsChannelCacheNeedUpdate, id)
	return db.GetDB().Delete(&model.StatsChannel{}, id).Error
}

func StatsTotalGet() model.StatsTotal {
	statsTotalCacheLock.RLock()
	defer statsTotalCacheLock.RUnlock()
	return statsTotalCache
}

func StatsTodayGet() model.StatsDaily {
	statsDailyCacheLock.RLock()
	defer statsDailyCacheLock.RUnlock()
	return statsDailyCache
}

func StatsChannelGet(id int) model.StatsChannel {
	stats, ok := statsChannelCache.Get(id)
	if !ok {
		tmp := model.StatsChannel{
			ChannelID: id,
		}
		statsChannelCache.Set(id, tmp)
		statsChannelCacheNeedUpdate[id] = struct{}{}
		return tmp
	}
	return stats
}

func StatsGetDaily(ctx context.Context) ([]model.StatsDaily, error) {
	var statsDaily []model.StatsDaily
	result := db.GetDB().WithContext(ctx).Find(&statsDaily)
	if result.Error != nil {
		return nil, result.Error
	}
	return statsDaily, nil
}

func statsRefreshCache(ctx context.Context) error {
	db := db.GetDB().WithContext(ctx)
	statsDailyCacheLock.Lock()
	defer statsDailyCacheLock.Unlock()
	result := db.Last(&statsDailyCache)
	if result.RowsAffected == 0 {
		statsDailyCache = model.StatsDaily{
			Date: timeo.GetMidnight(),
		}
		return nil
	}

	if result.Error != nil {
		return result.Error
	}
	if timeo.ToMidnight(statsDailyCache.Date) != timeo.GetMidnight() {
		statsDailyCache = model.StatsDaily{
			Date: timeo.GetMidnight(),
		}
	}
	statsTotalCacheLock.Lock()
	defer statsTotalCacheLock.Unlock()
	result = db.First(&statsTotalCache)
	if result.RowsAffected == 0 {
		statsTotalCache = model.StatsTotal{
			ID: 1,
		}
		return nil
	}
	if result.Error != nil {
		return result.Error
	}
	var statsChannel []model.StatsChannel
	result = db.Find(&statsChannel)
	if result.Error != nil {
		return result.Error
	}
	for _, v := range statsChannel {
		statsChannelCache.Set(v.ChannelID, v)
	}
	return nil
}

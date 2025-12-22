package op

import (
	"context"
	"sync"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/utils/cache"
	"github.com/alvindingpeng/switchyard/internal/utils/log"
	"gorm.io/gorm/clause"
)

var statsDailyCache model.StatsDaily
var statsDailyCacheLock sync.RWMutex

var statsTotalCache model.StatsTotal
var statsTotalCacheLock sync.RWMutex

var statsHourlyCache [24]model.StatsHourly
var statsHourlyCacheLock sync.RWMutex

var statsChannelCache = cache.New[int, model.StatsChannel](16)
var statsChannelCacheNeedUpdate = make(map[int]struct{})

var statsModelCache = cache.New[int, model.StatsModel](16)
var statsModelCacheNeedUpdate = make(map[int]struct{})

func StatsSaveDBTask() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	log.Debugf("stats save db task started")
	startTime := time.Now()
	defer func() {
		log.Debugf("stats save db task finished, save time: %s", time.Since(startTime))
	}()
	if err := StatsSaveDB(ctx); err != nil {
		log.Errorf("stats save db error: %v", err)
		return
	}
}

func StatsSaveDB(ctx context.Context) error {
	db := db.GetDB().WithContext(ctx)

	// 保存总计数据
	statsTotalCacheLock.Lock()
	defer statsTotalCacheLock.Unlock()
	result := db.Save(&statsTotalCache)
	if result.Error != nil {
		return result.Error
	}

	// 保存每日数据
	statsDailyCacheLock.Lock()
	defer statsDailyCacheLock.Unlock()
	result = db.Save(&statsDailyCache)
	if result.Error != nil {
		return result.Error
	}

	// 保存所有24小时的数据
	statsHourlyCacheLock.RLock()
	todayDate := time.Now().Format("20060102")
	hourlyStats := make([]model.StatsHourly, 0, 24)
	for hour := 0; hour < 24; hour++ {
		if statsHourlyCache[hour].Date == todayDate {
			hourlyStats = append(hourlyStats, statsHourlyCache[hour])
		}
	}
	statsHourlyCacheLock.RUnlock()

	if len(hourlyStats) > 0 {
		result = db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "hour"}},
			UpdateAll: true,
		}).Create(&hourlyStats)
		if result.Error != nil {
			return result.Error
		}
	}

	// 保存渠道数据
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

func StatsDailyUpdate(ctx context.Context, metrics model.StatsMetrics) error {
	statsDailyCacheLock.Lock()
	defer statsDailyCacheLock.Unlock()
	if statsDailyCache.Date != time.Now().Format("20060102") {
		if err := StatsSaveDB(ctx); err != nil {
			return err
		}
		statsDailyCache = model.StatsDaily{
			Date: time.Now().Format("20060102"),
		}
	}
	statsDailyCache.StatsMetrics.Add(metrics)
	return nil
}

func StatsTotalUpdate(metrics model.StatsMetrics) error {
	statsTotalCacheLock.Lock()
	defer statsTotalCacheLock.Unlock()
	statsTotalCache.StatsMetrics.Add(metrics)
	return nil
}

func StatsChannelUpdate(stats model.StatsChannel) error {
	channelCache, ok := statsChannelCache.Get(stats.ChannelID)
	if !ok {
		channelCache = model.StatsChannel{
			ChannelID: stats.ChannelID,
		}
	}
	channelCache.StatsMetrics.Add(stats.StatsMetrics)
	statsChannelCache.Set(stats.ChannelID, channelCache)
	statsChannelCacheNeedUpdate[stats.ChannelID] = struct{}{}
	return nil
}

func StatsHourlyUpdate(metrics model.StatsMetrics) error {
	now := time.Now()
	nowHour := now.Hour()
	todayDate := time.Now().Format("20060102")

	statsHourlyCacheLock.Lock()
	defer statsHourlyCacheLock.Unlock()

	if statsHourlyCache[nowHour].Date != todayDate {
		statsHourlyCache[nowHour] = model.StatsHourly{
			Hour: nowHour,
			Date: todayDate,
		}
	}

	statsHourlyCache[nowHour].StatsMetrics.Add(metrics)
	return nil
}

func StatsModelUpdate(stats model.StatsModel) error {
	modelCache, ok := statsModelCache.Get(stats.ID)
	if !ok {
		modelCache = model.StatsModel{
			ID: stats.ID,
		}
	}
	modelCache.StatsMetrics.Add(stats.StatsMetrics)
	statsModelCache.Set(stats.ID, modelCache)
	statsModelCacheNeedUpdate[stats.ID] = struct{}{}
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

func StatsHourlyGet() []model.StatsHourly {
	now := time.Now()
	currentHour := now.Hour()
	todayDate := time.Now().Format("20060102")

	statsHourlyCacheLock.RLock()
	defer statsHourlyCacheLock.RUnlock()

	result := make([]model.StatsHourly, 0, currentHour+1)

	for hour := 0; hour <= currentHour; hour++ {
		if statsHourlyCache[hour].Date == todayDate {
			result = append(result, statsHourlyCache[hour])
		} else {
			result = append(result, model.StatsHourly{
				Hour: hour,
				Date: todayDate,
			})
		}
	}

	return result
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
			Date: time.Now().Format("20060102"),
		}
		return nil
	}

	if result.Error != nil {
		return result.Error
	}
	if statsDailyCache.Date != time.Now().Format("20060102") {
		statsDailyCache = model.StatsDaily{
			Date: time.Now().Format("20060102"),
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
	var statsHourly []model.StatsHourly
	result = db.Find(&statsHourly)
	if result.Error != nil {
		return result.Error
	}
	// 加载所有24小时的数据到缓存
	// 更新数据时会自动判断日期，如果是昨天的数据会清空重新统计
	statsHourlyCacheLock.Lock()
	for _, v := range statsHourly {
		if v.Hour >= 0 && v.Hour < 24 {
			statsHourlyCache[v.Hour] = v
		}
	}
	statsHourlyCacheLock.Unlock()
	return nil
}

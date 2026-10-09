package op

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/utils/cache"
	"github.com/charmbracelet/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var statsDailyCache model.StatsDaily
var statsDailyCacheLock sync.RWMutex

var statsTotalCache model.StatsTotal
var statsTotalCacheLock sync.RWMutex

var statsHourlyCache [24]model.StatsHourly
var statsHourlyCacheLock sync.RWMutex

var channelStatsNeedUpdate = make(map[int]struct{}) // 等待持久化的渠道 ID。
var channelStatsNeedUpdateLock sync.Mutex           // 保护渠道统计累加和待写集合。

var channelModelStatsNeedUpdate = make(map[int]struct{}) // 等待持久化的渠道模型 ID。
var channelModelStatsNeedUpdateLock sync.Mutex           // 保护渠道模型统计累加和待写集合。

var channelKeyStatsNeedUpdate = make(map[int]struct{}) // 等待持久化的渠道凭据 ID。
var channelKeyStatsNeedUpdateLock sync.Mutex           // 保护渠道凭据统计累加和待写集合。

var statsAPIKeyCache = cache.New[int, model.StatsAPIKey](16)
var statsAPIKeyCacheNeedUpdate = make(map[int]struct{})
var statsAPIKeyCacheNeedUpdateLock sync.Mutex

// 渠道与渠道模型维度的按天统计: 内存累加, 随统计任务全量落库; 缓存值即当日权威累计,
// 覆盖写使重试安全; 重启时 statsRefreshCache 把当天的行装回缓存, 重启前的累计不会丢。
type channelDailyKey struct {
	channelID int
	date      string
}

type channelModelDailyKey struct {
	channelModelID int
	date           string
}

var channelDailyStatsCache = make(map[channelDailyKey]model.StatsChannelDaily)
var channelDailyStatsDirty = make(map[channelDailyKey]struct{})
var channelDailyStatsLock sync.Mutex

var channelModelDailyStatsCache = make(map[channelModelDailyKey]model.StatsChannelModelDaily)
var channelModelDailyStatsDirty = make(map[channelModelDailyKey]struct{})
var channelModelDailyStatsLock sync.Mutex

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
	statsTotalCacheLock.RLock()
	totalSnap := statsTotalCache
	statsTotalCacheLock.RUnlock()
	if totalSnap.ID == 0 {
		totalSnap.ID = 1
	}

	statsDailyCacheLock.RLock()
	dailySnap := statsDailyCache
	statsDailyCacheLock.RUnlock()

	statsHourlyCacheLock.RLock()
	hourlyAll := statsHourlyCache
	statsHourlyCacheLock.RUnlock()

	channelIDs := drainDirtySet(&channelStatsNeedUpdateLock, channelStatsNeedUpdate)
	modelIDs := drainDirtySet(&channelModelStatsNeedUpdateLock, channelModelStatsNeedUpdate)
	keyIDs := drainDirtySet(&channelKeyStatsNeedUpdateLock, channelKeyStatsNeedUpdate)
	apiKeyIDs := drainDirtySet(&statsAPIKeyCacheNeedUpdateLock, statsAPIKeyCacheNeedUpdate)

	if err := persistStatsSnapshots(ctx, totalSnap, dailySnap, hourlyAll, channelIDs, modelIDs, keyIDs, apiKeyIDs); err != nil {
		restoreStatsDirty(channelIDs, modelIDs, keyIDs, apiKeyIDs)
		return err
	}

	// 按天统计在主统计之后落库: 失败只恢复自己的待写标记, 不影响已落库的主统计。
	channelDailyKeys, channelDailyEntries := drainDailyEntries(&channelDailyStatsLock, channelDailyStatsDirty, channelDailyStatsCache)
	channelModelDailyKeys, channelModelDailyEntries := drainDailyEntries(&channelModelDailyStatsLock, channelModelDailyStatsDirty, channelModelDailyStatsCache)
	if err := persistDailyStats(ctx, channelDailyEntries, channelModelDailyEntries); err != nil {
		restoreDailyEntries(&channelDailyStatsLock, channelDailyStatsDirty, channelDailyKeys)
		restoreDailyEntries(&channelModelDailyStatsLock, channelModelDailyStatsDirty, channelModelDailyKeys)
		return err
	}
	return nil
}

// drainDailyEntries 取出并清空按天统计的待写条目, 返回键与当前值的配对。
func drainDailyEntries[K comparable, V any](lock *sync.Mutex, dirty map[K]struct{}, cache map[K]V) ([]K, []V) {
	lock.Lock()
	defer lock.Unlock()
	keys := make([]K, 0, len(dirty))
	entries := make([]V, 0, len(dirty))
	for key := range dirty {
		if value, ok := cache[key]; ok {
			keys = append(keys, key)
			entries = append(entries, value)
		}
		delete(dirty, key)
	}
	return keys, entries
}

// restoreDailyEntries 在按天统计落库失败后恢复待写标记。
func restoreDailyEntries[K comparable](lock *sync.Mutex, dirty map[K]struct{}, keys []K) {
	lock.Lock()
	defer lock.Unlock()
	for _, key := range keys {
		dirty[key] = struct{}{}
	}
}

// persistDailyStats 全量覆盖写入按天统计并裁剪过期行; 缓存值即权威累计, 覆盖写使重试安全。
func persistDailyStats(ctx context.Context, channelDaily []model.StatsChannelDaily, channelModelDaily []model.StatsChannelModelDaily) error {
	dbConn := db.GetDB().WithContext(ctx)
	if len(channelDaily) > 0 {
		if result := dbConn.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "channel_id"}, {Name: "date"}},
			UpdateAll: true,
		}).Create(&channelDaily); result.Error != nil {
			return result.Error
		}
	}
	if len(channelModelDaily) > 0 {
		if result := dbConn.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "channel_model_id"}, {Name: "date"}},
			UpdateAll: true,
		}).Create(&channelModelDaily); result.Error != nil {
			return result.Error
		}
	}
	// 日期为 20060102 文本, 直接比较即可; 保留近 92 天, 每次落库顺手裁剪。
	cutoff := time.Now().AddDate(0, 0, -92).Format("20060102")
	dbConn.Where("date < ?", cutoff).Delete(&model.StatsChannelDaily{})
	dbConn.Where("date < ?", cutoff).Delete(&model.StatsChannelModelDaily{})
	return nil
}

// drainDirtySet 取出并清空一个待写集合。
func drainDirtySet(lock *sync.Mutex, set map[int]struct{}) []int {
	lock.Lock()
	defer lock.Unlock()
	ids := make([]int, 0, len(set))
	for id := range set {
		ids = append(ids, id)
		delete(set, id)
	}
	return ids
}

// restoreDirtySet 把一批主键放回待写集合, 用于持久化失败后重试。
func restoreDirtySet(lock *sync.Mutex, set map[int]struct{}, ids []int) {
	lock.Lock()
	defer lock.Unlock()
	for _, id := range ids {
		set[id] = struct{}{}
	}
}

// restoreStatsDirty 在统计持久化失败后恢复本批待写标记。
func restoreStatsDirty(channelIDs, modelIDs, keyIDs, apiKeyIDs []int) {
	restoreDirtySet(&channelStatsNeedUpdateLock, channelStatsNeedUpdate, channelIDs)
	restoreDirtySet(&channelModelStatsNeedUpdateLock, channelModelStatsNeedUpdate, modelIDs)
	restoreDirtySet(&channelKeyStatsNeedUpdateLock, channelKeyStatsNeedUpdate, keyIDs)
	restoreDirtySet(&statsAPIKeyCacheNeedUpdateLock, statsAPIKeyCacheNeedUpdate, apiKeyIDs)
}

func persistStatsSnapshots(
	ctx context.Context,
	totalSnap model.StatsTotal,
	dailySnap model.StatsDaily,
	hourlyAll [24]model.StatsHourly,
	channelIDs []int,
	modelIDs []int,
	keyIDs []int,
	apiKeyIDs []int,
) error {
	dbConn := db.GetDB().WithContext(ctx)

	if result := dbConn.Save(&totalSnap); result.Error != nil {
		return result.Error
	}
	if result := dbConn.Save(&dailySnap); result.Error != nil {
		return result.Error
	}

	todayDate := time.Now().Format("20060102")
	hourlyStats := make([]model.StatsHourly, 0, 24)
	for hour := 0; hour < 24; hour++ {
		if hourlyAll[hour].Date == todayDate {
			hourlyStats = append(hourlyStats, hourlyAll[hour])
		}
	}
	if len(hourlyStats) > 0 {
		if result := dbConn.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "hour"}},
			UpdateAll: true,
		}).Create(&hourlyStats); result.Error != nil {
			return result.Error
		}
	}

	for _, id := range channelIDs {
		channel, ok := channelCache.Get(id)
		if !ok {
			continue
		}
		if result := dbConn.Model(&model.Channel{}).
			Where("id = ?", channel.ID).
			Select("input_token", "output_token", "input_cost", "output_cost", "wait_time", "request_success", "request_failed").
			Updates(&channel); result.Error != nil {
			return result.Error
		}
	}

	for _, id := range modelIDs {
		channelModel, ok := channelModelCache.Get(id)
		if !ok {
			continue
		}
		if result := dbConn.Model(&model.ChannelModel{}).
			Where("id = ?", channelModel.ID).
			Select("input_token", "output_token", "input_cost", "output_cost", "wait_time", "request_success", "request_failed").
			Updates(&channelModel); result.Error != nil {
			return result.Error
		}
	}

	for _, id := range keyIDs {
		channelKey, ok := channelKeyCache.Get(id)
		if !ok {
			continue
		}
		if result := dbConn.Model(&model.ChannelKey{}).
			Where("id = ?", channelKey.ID).
			Select("input_token", "output_token", "input_cost", "output_cost", "wait_time", "request_success", "request_failed").
			Updates(&channelKey); result.Error != nil {
			return result.Error
		}
	}

	for _, id := range apiKeyIDs {
		ak, ok := statsAPIKeyCache.Get(id)
		if !ok {
			continue
		}
		if result := dbConn.Save(&ak); result.Error != nil {
			return result.Error
		}
	}

	return nil
}

func statsSaveDBWithDailyOverride(ctx context.Context, dailyOverride model.StatsDaily) error {
	statsTotalCacheLock.RLock()
	totalSnap := statsTotalCache
	statsTotalCacheLock.RUnlock()
	if totalSnap.ID == 0 {
		totalSnap.ID = 1
	}

	statsHourlyCacheLock.RLock()
	hourlyAll := statsHourlyCache
	statsHourlyCacheLock.RUnlock()

	channelIDs := drainDirtySet(&channelStatsNeedUpdateLock, channelStatsNeedUpdate)
	modelIDs := drainDirtySet(&channelModelStatsNeedUpdateLock, channelModelStatsNeedUpdate)
	keyIDs := drainDirtySet(&channelKeyStatsNeedUpdateLock, channelKeyStatsNeedUpdate)
	apiKeyIDs := drainDirtySet(&statsAPIKeyCacheNeedUpdateLock, statsAPIKeyCacheNeedUpdate)

	if err := persistStatsSnapshots(ctx, totalSnap, dailyOverride, hourlyAll, channelIDs, modelIDs, keyIDs, apiKeyIDs); err != nil {
		restoreStatsDirty(channelIDs, modelIDs, keyIDs, apiKeyIDs)
		return err
	}
	return nil
}

func StatsDailyUpdate(ctx context.Context, metrics model.StatsMetrics) error {
	today := time.Now().Format("20060102")

	statsDailyCacheLock.Lock()
	if statsDailyCache.Date == today {
		statsDailyCache.StatsMetrics.Add(metrics)
		statsDailyCacheLock.Unlock()
		return nil
	}

	prevDaily := statsDailyCache
	statsDailyCache = model.StatsDaily{Date: today}
	statsDailyCache.StatsMetrics.Add(metrics)
	statsDailyCacheLock.Unlock()

	return statsSaveDBWithDailyOverride(ctx, prevDaily)
}

func StatsTotalUpdate(metrics model.StatsMetrics) error {
	statsTotalCacheLock.Lock()
	defer statsTotalCacheLock.Unlock()
	if statsTotalCache.ID == 0 {
		statsTotalCache.ID = 1
	}
	statsTotalCache.StatsMetrics.Add(metrics)
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

// ChannelModelStatsUpdate 累加渠道模型统计并标记对应模型待持久化。
func ChannelModelStatsUpdate(channelModelID int, metrics model.StatsMetrics) error {
	channelModelStatsNeedUpdateLock.Lock()
	defer channelModelStatsNeedUpdateLock.Unlock()
	channelModel, ok := channelModelCache.Get(channelModelID)
	if !ok {
		return nil
	}
	channelModel.StatsMetrics.Add(metrics)
	channelModelCache.Set(channelModelID, channelModel)
	channelModelStatsNeedUpdate[channelModelID] = struct{}{}
	return nil
}

// ChannelKeyStatsUpdate 累加渠道凭据统计并标记对应凭据待持久化。
func ChannelKeyStatsUpdate(channelKeyID int, metrics model.StatsMetrics) error {
	channelKeyStatsNeedUpdateLock.Lock()
	defer channelKeyStatsNeedUpdateLock.Unlock()
	channelKey, ok := channelKeyCache.Get(channelKeyID)
	if !ok {
		return nil
	}
	channelKey.StatsMetrics.Add(metrics)
	channelKeyCache.Set(channelKeyID, channelKey)
	channelKeyStatsNeedUpdate[channelKeyID] = struct{}{}
	return nil
}

// ChannelStatsUpdate 累加渠道统计并标记对应渠道待持久化。
func ChannelStatsUpdate(channelID int, metrics model.StatsMetrics) error {
	channelStatsNeedUpdateLock.Lock()
	defer channelStatsNeedUpdateLock.Unlock()
	channel, ok := channelCache.Get(channelID)
	if !ok {
		return nil
	}
	channel.StatsMetrics.Add(metrics)
	channelCache.Set(channelID, channel)
	channelStatsNeedUpdate[channelID] = struct{}{}
	return nil
}

// ChannelDailyStatsUpdate 累加渠道当日统计并标记待落库; 统计不可用时静默丢弃, 避免影响转发主流程。
func ChannelDailyStatsUpdate(channelID int, metrics model.StatsMetrics) {
	key := channelDailyKey{channelID: channelID, date: time.Now().Format("20060102")}
	channelDailyStatsLock.Lock()
	defer channelDailyStatsLock.Unlock()
	entry, ok := channelDailyStatsCache[key]
	if !ok {
		entry = model.StatsChannelDaily{ChannelID: channelID, Date: key.date}
	}
	entry.StatsMetrics.Add(metrics)
	channelDailyStatsCache[key] = entry
	channelDailyStatsDirty[key] = struct{}{}
}

// ChannelModelDailyStatsUpdate 累加渠道模型当日统计并标记待落库。
func ChannelModelDailyStatsUpdate(channelModelID int, metrics model.StatsMetrics) {
	key := channelModelDailyKey{channelModelID: channelModelID, date: time.Now().Format("20060102")}
	channelModelDailyStatsLock.Lock()
	defer channelModelDailyStatsLock.Unlock()
	entry, ok := channelModelDailyStatsCache[key]
	if !ok {
		entry = model.StatsChannelModelDaily{ChannelModelID: channelModelID, Date: key.date}
	}
	entry.StatsMetrics.Add(metrics)
	channelModelDailyStatsCache[key] = entry
	channelModelDailyStatsDirty[key] = struct{}{}
}

// ChannelModelNames 返回渠道下各模型的名称映射(渠道模型主键 → 上游模型名)。
func ChannelModelNames(channelID int) (map[int]string, error) {
	var channelModels []model.ChannelModel
	if err := db.GetDB().Where("channel_id = ?", channelID).Find(&channelModels).Error; err != nil {
		return nil, err
	}
	names := make(map[int]string, len(channelModels))
	for _, cm := range channelModels {
		names[cm.ID] = cm.Name
	}
	return names, nil
}

// ChannelDailyStatsGet 返回渠道自身与各模型近 days 天的按天统计; 渠道不存在或无权访问时报错。
func ChannelDailyStatsGet(channelID int, days int, scope model.Scope) ([]model.StatsChannelDaily, []model.StatsChannelModelDaily, error) {
	channel, ok := channelCache.Get(channelID)
	if !ok || !scope.Owns(channel.UserID) {
		return nil, nil, fmt.Errorf("channel not found")
	}
	if days < 1 || days > 90 {
		days = 14
	}
	since := time.Now().AddDate(0, 0, -(days - 1)).Format("20060102")
	dbConn := db.GetDB()
	var channelDaily []model.StatsChannelDaily
	if err := dbConn.Where("channel_id = ? AND date >= ?", channelID, since).Order("date ASC").Find(&channelDaily).Error; err != nil {
		return nil, nil, err
	}
	var channelModels []model.ChannelModel
	if err := dbConn.Where("channel_id = ?", channelID).Find(&channelModels).Error; err != nil {
		return nil, nil, err
	}
	modelIDs := make([]int, 0, len(channelModels))
	for _, cm := range channelModels {
		modelIDs = append(modelIDs, cm.ID)
	}
	var modelDaily []model.StatsChannelModelDaily
	if len(modelIDs) > 0 {
		if err := dbConn.Where("channel_model_id IN ? AND date >= ?", modelIDs, since).Order("date ASC").Find(&modelDaily).Error; err != nil {
			return nil, nil, err
		}
	}
	return channelDaily, modelDaily, nil
}

func StatsAPIKeyUpdate(apiKeyID int, metrics model.StatsMetrics) error {
	statsAPIKeyCacheNeedUpdateLock.Lock()
	defer statsAPIKeyCacheNeedUpdateLock.Unlock()
	apiKeyCache, ok := statsAPIKeyCache.Get(apiKeyID)
	if !ok {
		apiKeyCache = model.StatsAPIKey{
			APIKeyID: apiKeyID,
		}
	}
	apiKeyCache.StatsMetrics.Add(metrics)
	statsAPIKeyCache.Set(apiKeyID, apiKeyCache)
	statsAPIKeyCacheNeedUpdate[apiKeyID] = struct{}{}
	return nil
}

func StatsAPIKeyDel(id int) error {
	statsAPIKeyCacheNeedUpdateLock.Lock()
	if _, ok := statsAPIKeyCache.Get(id); !ok {
		statsAPIKeyCacheNeedUpdateLock.Unlock()
		return nil
	}
	statsAPIKeyCache.Del(id)
	delete(statsAPIKeyCacheNeedUpdate, id)
	statsAPIKeyCacheNeedUpdateLock.Unlock()
	return db.GetDB().Delete(&model.StatsAPIKey{}, id).Error
}

func StatsTotalGet() model.StatsTotal {
	statsTotalCacheLock.RLock()
	defer statsTotalCacheLock.RUnlock()
	return statsTotalCache
}

func StatsAPIKeyGet(id int) model.StatsAPIKey {
	if stats, ok := statsAPIKeyCache.Get(id); ok {
		return stats
	}
	statsAPIKeyCacheNeedUpdateLock.Lock()
	defer statsAPIKeyCacheNeedUpdateLock.Unlock()
	stats, ok := statsAPIKeyCache.Get(id)
	if !ok {
		tmp := model.StatsAPIKey{
			APIKeyID: id,
		}
		statsAPIKeyCache.Set(id, tmp)
		statsAPIKeyCacheNeedUpdate[id] = struct{}{}
		return tmp
	}
	return stats
}

func StatsAPIKeyList() []model.StatsAPIKey {
	apiKeys := make([]model.StatsAPIKey, 0, statsAPIKeyCache.Len())
	for _, v := range statsAPIKeyCache.GetAll() {
		apiKeys = append(apiKeys, v)
	}
	return apiKeys
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

// StatsGetDaily 返回 since 当天及其之后的每日统计, since 为 20060102 格式。
// 只取窗口内的数据: 界面上的热力图与趋势图都有固定跨度, 全量返回会随运行时长无界增长。
func StatsGetDaily(ctx context.Context, since string) ([]model.StatsDaily, error) {
	var statsDaily []model.StatsDaily
	result := db.GetDB().WithContext(ctx).Where("date >= ?", since).Order("date").Find(&statsDaily)
	if result.Error != nil {
		return nil, result.Error
	}
	return statsDaily, nil
}

func statsRefreshCache(ctx context.Context) error {
	dbConn := db.GetDB().WithContext(ctx)
	today := time.Now().Format("20060102")

	var loadedDaily model.StatsDaily
	result := dbConn.Last(&loadedDaily)
	if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return fmt.Errorf("failed to get daily stats: %v", result.Error)
	}
	if result.RowsAffected == 0 || loadedDaily.Date != today {
		loadedDaily = model.StatsDaily{Date: today}
	}

	var loadedTotal model.StatsTotal
	result = dbConn.First(&loadedTotal)
	if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return fmt.Errorf("failed to get total stats: %v", result.Error)
	}
	if result.RowsAffected == 0 {
		loadedTotal = model.StatsTotal{ID: 1}
	} else if loadedTotal.ID == 0 {
		loadedTotal.ID = 1
	}

	var loadedHourly []model.StatsHourly
	result = dbConn.Find(&loadedHourly)
	if result.Error != nil {
		return fmt.Errorf("failed to get hourly stats: %v", result.Error)
	}

	statsDailyCacheLock.Lock()
	statsDailyCache = loadedDaily
	statsDailyCacheLock.Unlock()

	statsTotalCacheLock.Lock()
	statsTotalCache = loadedTotal
	statsTotalCacheLock.Unlock()

	var loadedAPIKeys []model.StatsAPIKey
	result = dbConn.Find(&loadedAPIKeys)
	if result.Error != nil {
		return fmt.Errorf("failed to get api key stats: %v", result.Error)
	}

	statsAPIKeyCache.Clear()
	// 就地清空而非重新赋值: drainDirtySet 与 restoreDirtySet 持有该 map 的引用, 换 map 会让它们写到旧对象上。
	statsAPIKeyCacheNeedUpdateLock.Lock()
	clear(statsAPIKeyCacheNeedUpdate)
	statsAPIKeyCacheNeedUpdateLock.Unlock()
	for _, v := range loadedAPIKeys {
		statsAPIKeyCache.Set(v.APIKeyID, v)
	}

	statsHourlyCacheLock.Lock()
	statsHourlyCache = [24]model.StatsHourly{}
	for _, v := range loadedHourly {
		if v.Hour >= 0 && v.Hour < 24 {
			statsHourlyCache[v.Hour] = v
		}
	}
	statsHourlyCacheLock.Unlock()

	// 当天的按天统计装回缓存: 缓存值即权威累计, 不装载会让首次落库覆盖掉重启前的部分。
	var loadedChannelDaily []model.StatsChannelDaily
	if err := dbConn.Where("date = ?", today).Find(&loadedChannelDaily).Error; err != nil {
		return fmt.Errorf("failed to get channel daily stats: %v", err)
	}
	var loadedChannelModelDaily []model.StatsChannelModelDaily
	if err := dbConn.Where("date = ?", today).Find(&loadedChannelModelDaily).Error; err != nil {
		return fmt.Errorf("failed to get channel model daily stats: %v", err)
	}
	channelDailyStatsLock.Lock()
	clear(channelDailyStatsCache)
	clear(channelDailyStatsDirty)
	for _, v := range loadedChannelDaily {
		channelDailyStatsCache[channelDailyKey{channelID: v.ChannelID, date: v.Date}] = v
	}
	channelDailyStatsLock.Unlock()
	channelModelDailyStatsLock.Lock()
	clear(channelModelDailyStatsCache)
	clear(channelModelDailyStatsDirty)
	for _, v := range loadedChannelModelDaily {
		channelModelDailyStatsCache[channelModelDailyKey{channelModelID: v.ChannelModelID, date: v.Date}] = v
	}
	channelModelDailyStatsLock.Unlock()

	return nil
}

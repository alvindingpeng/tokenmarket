package migrate

import (
	"fmt"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/charmbracelet/log"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{Version: 43, Up: Migration043_ChannelDimensionGrossTokens})
}

// Migration043_ChannelDimensionGrossTokens 把渠道维度的累计统计与每日统计从旧口径(净读)
// 对齐到计费明细的毛输入口径。
//
// 背景: 迁移 041/042 只回填了全局总计与时间桶(stats_totals / stats_dailies / stats_hourlies /
// stats_api_keys); 渠道列表、渠道详情、首页渠道榜与模型榜、上游账单核对读的却是另一批表:
// stats_channel_dailies / stats_channel_model_dailies / channels / channel_models / channel_keys。
// 这些行的 input_token 停留在净读口径, 生产实测缺口约 19 亿词元(占毛输入 61%),
// 排行榜切到"词元"维度时排序系统性偏向缓存命中率低的渠道。
//
// 为什么用"取大"而不是"加缓存词元": 生产库里的现状并不统一 —— 有的渠道行已是毛口径,
// 有的还是净读, 还有少量行的值高于计费明细(早期日界漂移、明细被清理)。加法定会重复计数,
// 而 SET 成明细值又会把高于明细的历史值打回去。取 max(现有, 明细毛输入) 同时满足:
// 只补不回退、绝不重复计数、也天然幂等(重放两次结果一样)。
//
// 归属口径:
//   - 渠道/渠道模型用 billing_records 的 channel_id 与 model_name 归集;
//     (channel_id, model_name) 在 channel_models 上带唯一索引, 历史改名/删除的组合找不到行, 跳过不猜。
//   - 凭据维度 billing_records 没有外键, 只能把渠道的缺口按现有凭据词元比例摊分
//     (全为 0 时等分), 只保证"同渠道各凭据之和 = 渠道毛输入", 不预设单个凭据的归属。
//
// 不变量: 渠道累计值不小于其下各渠道模型的累计值之和。
// 逐模型取大会保留某些模型高于明细的历史值, 若渠道值只按明细取大, 就可能出现
// "各模型之和 > 渠道合计"的自相矛盾(实测 25 个渠道里有 2 个, 超出 0.02%~2.7%)。
// 界面把模型榜与渠道榜并排展示, 这种不一致比"渠道值略高于明细"更难解释,
// 所以渠道值再取一次 max(现有, 明细, 各模型取大后之和) —— 仍然只升不降。
//
// 时区: 与迁移 042 同口径, 按明细写入时的本地壁钟日期归日 —— 明细里存的是带偏移的时间串,
// 直接交给 SQL 的 date() 会先折成 UTC, 凌晨时段会被算到前一天。
//
// 恰好一次: 由 migration_records 保证; 取大逻辑本身也幂等, 重放无副作用。
func Migration043_ChannelDimensionGrossTokens(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("billing_records") {
		return nil
	}

	rows := make([]struct {
		ChannelID       int
		ModelName       string
		InputToken      int64
		CacheReadToken  int64
		CacheWriteToken int64
		CreatedAt       time.Time
	}, 0, 4096)
	if err := db.Table("billing_records").
		Select("channel_id, model_name, input_token, cache_read_token, cache_write_token, created_at").
		Scan(&rows).Error; err != nil {
		return fmt.Errorf("load billing records: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}

	// 计费明细的毛输入 = 净读 + 缓存读 + 缓存写, 与 relay 侧 usageMetrics 的口径一致。
	channelTotal := map[int]int64{}
	channelDaily := map[channelDailyKey]int64{}
	modelTotal := map[modelKey]int64{}
	modelDaily := map[modelDailyKey]int64{}
	for _, r := range rows {
		if r.ChannelID == 0 {
			continue
		}
		gross := r.InputToken + r.CacheReadToken + r.CacheWriteToken
		if gross <= 0 {
			continue
		}
		date := r.CreatedAt.Local().Format("20060102")
		channelTotal[r.ChannelID] += gross
		channelDaily[channelDailyKey{channelID: r.ChannelID, date: date}] += gross
		modelTotal[modelKey{channelID: r.ChannelID, model: r.ModelName}] += gross
		modelDaily[modelDailyKey{channelID: r.ChannelID, model: r.ModelName, date: date}] += gross
	}

	channelModelID := loadChannelModelIDs(db)
	// 反向表: channel_models 主键 -> 计费明细的组合键, 回填累计表时按它取值。
	modelIDToKey := make(map[int]modelKey, len(channelModelID))
	for key, id := range channelModelID {
		modelIDToKey[id] = key
	}

	var channelDailyDone, channelModelDailyDone, channelDone, channelModelDone int
	var channelDailyGap, channelModelDailyGap, channelGap, channelModelGap int64

	var dailyRows []model.StatsChannelDaily
	if err := db.Select("channel_id", "date", "input_token").Find(&dailyRows).Error; err != nil {
		return fmt.Errorf("load channel daily stats: %w", err)
	}
	for _, row := range dailyRows {
		target := channelDaily[channelDailyKey{channelID: row.ChannelID, date: row.Date}]
		if target <= row.InputToken {
			continue
		}
		if err := db.Model(&model.StatsChannelDaily{}).
			Where("channel_id = ? AND date = ?", row.ChannelID, row.Date).
			UpdateColumn("input_token", target).Error; err != nil {
			return fmt.Errorf("backfill channel daily %d/%s: %w", row.ChannelID, row.Date, err)
		}
		channelDailyDone++
		channelDailyGap += target - row.InputToken
	}

	var modelDailyRows []model.StatsChannelModelDaily
	if err := db.Select("channel_model_id", "date", "input_token").Find(&modelDailyRows).Error; err != nil {
		return fmt.Errorf("load channel model daily stats: %w", err)
	}
	for _, row := range modelDailyRows {
		key, ok := modelIDToKey[row.ChannelModelID]
		if !ok {
			continue
		}
		target := modelDaily[modelDailyKey{channelID: key.channelID, model: key.model, date: row.Date}]
		if target <= row.InputToken {
			continue
		}
		if err := db.Model(&model.StatsChannelModelDaily{}).
			Where("channel_model_id = ? AND date = ?", row.ChannelModelID, row.Date).
			UpdateColumn("input_token", target).Error; err != nil {
			return fmt.Errorf("backfill channel model daily %d/%s: %w", row.ChannelModelID, row.Date, err)
		}
		channelModelDailyDone++
		channelModelDailyGap += target - row.InputToken
	}

	var channelModelRows []model.ChannelModel
	if err := db.Select("id", "channel_id", "input_token").Find(&channelModelRows).Error; err != nil {
		return fmt.Errorf("load channel models: %w", err)
	}
	// 每个渠道模型的目标值= max(现有, 明细毛输入); 同时按渠道汇总, 给渠道累计值兜底。
	modelAfter := make(map[int]int64, len(channelModelRows))
	modelsPerChannel := make(map[int]int64, len(channelModelRows))
	for _, row := range channelModelRows {
		// 找不到组合键的模型(改名/删除)只能保留现有值: 明细里没有它的桶, 猜归属就是造假数据。
		target := row.InputToken
		if key, ok := modelIDToKey[row.ID]; ok {
			if gross := modelTotal[key]; gross > target {
				target = gross
			}
		}
		modelAfter[row.ID] = target
		modelsPerChannel[row.ChannelID] += target
	}

	var channelRows []model.Channel
	if err := db.Select("id", "input_token").Find(&channelRows).Error; err != nil {
		return fmt.Errorf("load channels: %w", err)
	}
	channelAfter := make(map[int]int64, len(channelRows))
	for _, row := range channelRows {
		target := channelTotal[row.ID]
		if target < row.InputToken {
			target = row.InputToken
		}
		// 渠道合计必须盖住其模型的取大结果, 否则模型榜之和会超过渠道榜。
		if models := modelsPerChannel[row.ID]; models > target {
			target = models
		}
		channelAfter[row.ID] = target
		if target <= row.InputToken {
			continue
		}
		if err := db.Model(&model.Channel{}).Where("id = ?", row.ID).
			UpdateColumn("input_token", target).Error; err != nil {
			return fmt.Errorf("backfill channel %d: %w", row.ID, err)
		}
		channelDone++
		channelGap += target - row.InputToken
	}

	for _, row := range channelModelRows {
		target := modelAfter[row.ID]
		if target <= row.InputToken {
			continue
		}
		if err := db.Model(&model.ChannelModel{}).Where("id = ?", row.ID).
			UpdateColumn("input_token", target).Error; err != nil {
			return fmt.Errorf("backfill channel model %d: %w", row.ID, err)
		}
		channelModelDone++
		channelModelGap += target - row.InputToken
	}

	keyShares, err := backfillChannelKeyShares(db, channelAfter)
	if err != nil {
		return err
	}

	log.Infof("migrate 43: channel dims gross tokens (channel_daily=%d/%d channel_model_daily=%d/%d channels=%d/%d channel_models=%d/%d channel_keys=%d)",
		channelDailyDone, channelDailyGap, channelModelDailyDone, channelModelDailyGap,
		channelDone, channelGap, channelModelDone, channelModelGap, keyShares)
	return nil
}

// loadChannelModelIDs 把 (channel_id, model_name) 翻译成 channel_models 主键。
// 该组合在 channel_models 上带唯一索引, 但历史改名/删除会让同一组合找不到行 —— 这类桶跳过。
func loadChannelModelIDs(db *gorm.DB) map[modelKey]int {
	if !db.Migrator().HasTable("channel_models") {
		return nil
	}
	var models []model.ChannelModel
	if err := db.Select("id", "channel_id", "name").Find(&models).Error; err != nil {
		return nil
	}
	ids := make(map[modelKey]int, len(models))
	for _, m := range models {
		ids[modelKey{channelID: m.ChannelID, model: m.Name}] = m.ID
	}
	return ids
}

// backfillChannelKeyShares 把渠道级的毛输入缺口摊到该渠道的各凭据上。
// 计费明细不记凭据, 唯一可用的归属证据是转发日志里的 "渠道名 · 凭据名" 标签, 但它只覆盖
// 有日志留存的近期请求; 与其只补一小段, 不如按已有凭据词元比例摊分, 保证
// "同渠道各凭据之和 = 渠道毛输入" 这个可核对的不变量。
// 返回被摊分的凭据数。
func backfillChannelKeyShares(db *gorm.DB, channelAfter map[int]int64) (int, error) {
	if !db.Migrator().HasTable("channel_keys") {
		return 0, nil
	}
	var keys []model.ChannelKey
	if err := db.Select("id", "channel_id", "input_token").Find(&keys).Error; err != nil {
		return 0, fmt.Errorf("load channel keys: %w", err)
	}
	if len(keys) == 0 {
		return 0, nil
	}
	byChannel := make(map[int][]model.ChannelKey, len(keys))
	for _, k := range keys {
		byChannel[k.ChannelID] = append(byChannel[k.ChannelID], k)
	}

	touched := 0
	for channelID, group := range byChannel {
		target, ok := channelAfter[channelID]
		if !ok {
			continue
		}
		var sum int64
		for _, k := range group {
			sum += k.InputToken
		}
		deficit := target - sum
		if deficit <= 0 {
			continue
		}
		shares := make([]int64, len(group))
		if sum == 0 {
			// 渠道从未记录过凭据级词元: 等分, 余数给第一个, 至少保证总和精确。
			base := deficit / int64(len(group))
			for i := range shares {
				shares[i] = base
			}
			shares[0] += deficit - base*int64(len(group))
		} else {
			var assigned int64
			largest := 0
			for i, k := range group {
				share := deficit * k.InputToken / sum
				shares[i] = share
				assigned += share
				if k.InputToken > group[largest].InputToken {
					largest = i
				}
			}
			// 取整余数给词元最多的凭据, 避免把误差摊给零值凭据。
			shares[largest] += deficit - assigned
		}
		for i, k := range group {
			if shares[i] == 0 {
				continue
			}
			if err := db.Model(&model.ChannelKey{}).Where("id = ?", k.ID).
				UpdateColumn("input_token", k.InputToken+shares[i]).Error; err != nil {
				return touched, fmt.Errorf("backfill channel key %d: %w", k.ID, err)
			}
			touched++
		}
	}
	return touched, nil
}

// channelDailyKey / modelKey / modelDailyKey 是回填聚合用的复合键。
// modelKey 以 (channel_id, model_name) 表达渠道模型, 因为计费明细只记这两列,
// 落库前再翻译成 channel_models 主键。
type channelDailyKey struct {
	channelID int
	date      string
}

type modelKey struct {
	channelID int
	model     string
}

type modelDailyKey struct {
	channelID int
	model     string
	date      string
}

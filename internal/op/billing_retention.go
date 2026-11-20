package op

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/alert"
	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/charmbracelet/log"
	"gorm.io/gorm"
)

// 四张核心资金表的留存。
//
// 为什么这件事必须单独设计, 而不是照着审计归档抄一遍:
//   - billing_records 是**钱**的原始凭证, 但它同时是若干"终身口径"读口的唯一数据源
//     (用户累计消费、提现可提额度、平台全期毛利)。直接删, 这些数字会随删除一起缩水 ——
//     提现额度凭空变少、看板收入比实际低, 那是算错钱, 不是少显示几行。
//   - balance_reservations 不能随便删: BillingReleaseStale 用
//     NOT EXISTS (SELECT 1 FROM billing_settlement_tasks WHERE reservation_id = ...) 判断
//     "这笔预扣到底有没有结算意图"。删掉任务行, 一笔仍在等结算的预扣会被当成泄漏释放 ——
//     冻结额退了, 结算再落账就是重复扣/重复退。
//   - billing_records 的某一行可能正被 disputes 引用: DisputeReview 批准时 tx.First(&record,
//     dispute.RecordID) 找不到行就没法退款; SettlementRun 也按 dispute.RecordID 回查明细。
//
// 因此本文件的删除动作只有三条路, 每条都先满足充分条件:
//   1. 归档: 先把要删的行原样写成 NDJSON 并 Sync 落盘, 归档失败则本轮一条都不删;
//   2. 固化: 删除前把当天明细的聚合值写进 billing_rollups, 终身口径查询 = 汇总表 ∪ 明细表;
//   3. 排除: 被申诉引用的明细、仍有结算意图的预扣、非 completed 的结算任务一律不删。

const (
	// billingRetentionDefaultDays 默认留存天数; 与审计的 180 天对齐, 让"审计里有的账"和
	// "账上查得到的记录"不会出现半年这道裂缝。
	billingRetentionDefaultDays = 180
	// billingRetentionMaxDays 上限: 与 archive_retention_days / audit_retention_days 一致,
	// 避免出现"留存 100 年"这种实际等于永不清理的配置。
	billingRetentionMaxDays = 3650
	// billingRetentionBatch 每批读/删的行数。分批是为了不把一次清理做成一个超长写事务:
	// 明细表可能一次过期几十万行, 一个事务全包会把转发路径的写入一起卡住。
	billingRetentionBatch = 5000
	// billingRetentionMaxDaysPerRun 单轮最多处理的自然日数。正常情况下每天只多过期一天,
	// 这个上限只在"首次启用留存"时起作用: 老库一次补 180 天, 分批跑完即可, 不必卡死一次。
	billingRetentionMaxDaysPerRun = 400
	// billingRetentionMaxRecordsPerRun 单轮最多删除的明细行数。超出的留到下一轮 ——
	// 留存是幂等收敛的, 慢一天没有代价, 把线上写事务拖住有代价。
	billingRetentionMaxRecordsPerRun = 200000
)

// BillingRetentionDays 返回四张资金表的留存天数, 非法值回退 180 天。
func BillingRetentionDays() int {
	days, err := SettingGetInt(model.SettingKeyBillingRetentionDays)
	if err != nil || days < 1 {
		return billingRetentionDefaultDays
	}
	if days > billingRetentionMaxDays {
		return billingRetentionMaxDays
	}
	return days
}

// BillingArchiveEnabled 判断删除前是否归档; 读不到设置时按 true 处理,
// 让"配置缺失"退化成"多留一份文件"而不是"直接删钱账"。
func BillingArchiveEnabled() bool {
	enabled, err := SettingGetBool(model.SettingKeyBillingArchiveEnabled)
	if err != nil {
		return true
	}
	return enabled
}

// BillingArchiveDir 资金账归档目录, 与调用日志/审计归档并列。
func BillingArchiveDir() string {
	return filepath.Join(ArchiveDir(), "billing")
}

// BillingRetentionResult 一次资金表留存清理的结果。
//
// 刻意把"归档了几条"和"删了几条"分开报: 两者不相等时(归档成功但删除只删了一部分,
// 或归档失败删除为 0)运维一眼能看出本轮停在哪一步, 不必去翻日志顺序。
type BillingRetentionResult struct {
	Cutoff        time.Time `json:"cutoff"`
	RetentionDays int       `json:"retention_days"`
	ArchiveOn     bool      `json:"archive_enabled"`
	ArchiveFile   string    `json:"archive_file,omitempty"`
	Archived      int       `json:"archived"`
	ArchiveError  string    `json:"archive_error,omitempty"`

	SealedDays      int   `json:"sealed_days"`
	DeletedRecords  int64 `json:"deleted_records"`
	RetainedRecords int64 `json:"retained_records"`
	DeletedLedgers  int64 `json:"deleted_ledgers"`
	DeletedReserves int64 `json:"deleted_reservations"`
	DeletedTasks    int64 `json:"deleted_settlement_tasks"`
	Truncated       bool  `json:"truncated,omitempty"`
}

// billingRecordDeletable 判定"这条明细可以删"的 SQL 片段。
//
// 条件只有两条: 超期 + 没有被任何未驳回的申诉引用。
// 为什么申诉要单独排除: 批准退款那条路径会按 dispute.RecordID 回查明细并改它的 status,
// 行被删了就退不了款; 日结单也要按 record_id 回查做退款冲销。已驳回的行不再有任何读者,
// 可以删 —— 这就是判据用 "<> rejected" 而不是"存在任何申诉"的原因。
const billingRecordDeletable = "created_at < ? AND NOT EXISTS (SELECT 1 FROM disputes WHERE disputes.record_id = billing_records.id AND disputes.status <> ?)"

// billingRetentionCutoff 把留存天数换成删除截止点, 并对齐到自然日 00:00。
//
// 为什么对齐到整天: 固化汇总的粒度是"天"。如果截止点落在一天中间, 那一天的明细会被删一半,
// 汇总表只能记半天的数, 剩下的半天既在明细里又在"不完整的汇总"里, 两者相加就对不上。
// 整天边界让"这一天要么全在明细里, 要么全在汇总里", 加法才成立。
func billingRetentionCutoff(days int) time.Time {
	now := time.Now()
	cutoff := now.AddDate(0, 0, -days)
	return time.Date(cutoff.Year(), cutoff.Month(), cutoff.Day(), 0, 0, 0, 0, time.Local)
}

// dayStart 返回某个时刻所在自然日的 00:00(本地挂钟); 与 ReconcileDay 同一口径。
func dayStart(t time.Time) time.Time {
	local := t.In(time.Local)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.Local)
}

// BillingRetentionRun 归档并清理四张核心资金表的超期记录, 幂等可重复执行。
//
// 顺序不可颠倒: 归档并落盘 → 固化当天汇总 → 删除明细 → 归档并删除流水/预扣/结算任务。
// 任何一步的失败都不会让"归档没写成、记录已经删掉"这种状态出现。
func BillingRetentionRun(ctx context.Context) (BillingRetentionResult, error) {
	days := BillingRetentionDays()
	cutoff := billingRetentionCutoff(days)
	result := BillingRetentionResult{Cutoff: cutoff, RetentionDays: days, ArchiveOn: BillingArchiveEnabled()}
	if err := ctx.Err(); err != nil {
		return result, err
	}

	limits := billingArchiveLimits{}
	// 归档关闭时没有"已归档"上界; 此时留存语义是"直接删超期", 删除侧跳过上界判断(0 = 不限)。
	if result.ArchiveOn {
		file, counts, archived, err := archiveBillingBefore(ctx, cutoff)
		limits = archived
		result.ArchiveFile = file
		result.Archived = counts.total()
		if err != nil {
			result.ArchiveError = err.Error()
			RaiseAlertFacts(ctx, AlertFacts{
				Kind:   "billing_archive_failed",
				Reason: "billing table archive failed",
				Fields: []alert.Field{
					{Label: "清理截止", Value: cutoff.Format("2006-01-02")},
					{Label: "归档文件", Value: file},
					{Label: "已归档", Value: fmt.Sprintf("%d 条", counts.total())},
					{Label: "错误", Value: err.Error()},
					{Label: "保护动作", Value: "归档失败时本轮不删除任何资金账记录"},
					{Label: "影响", Value: "四张资金表继续膨胀; 自动备份体积随存量增长"},
					{Label: "建议", Value: "检查归档目录可写性与磁盘余量后重试"},
				},
			})
			return result, nil
		}
	}

	sealed, deleted, retained, truncated, err := sealAndDeleteExpiredRecords(ctx, cutoff, limits.Records)
	result.SealedDays, result.DeletedRecords, result.RetainedRecords, result.Truncated = sealed, deleted, retained, truncated
	if err != nil {
		return result, fmt.Errorf("delete expired billing records: %w", err)
	}

	ledgers, err := deleteExpiredLedgers(ctx, cutoff, limits.Ledgers)
	result.DeletedLedgers = ledgers
	if err != nil {
		return result, fmt.Errorf("delete expired balance ledgers: %w", err)
	}

	reserves, err := deleteExpiredReservations(ctx, cutoff, limits.Reservations)
	result.DeletedReserves = reserves
	if err != nil {
		return result, fmt.Errorf("delete expired reservations: %w", err)
	}

	tasks, err := deleteExpiredSettlementTasks(ctx, cutoff, limits.Tasks)
	result.DeletedTasks = tasks
	if err != nil {
		return result, fmt.Errorf("delete expired settlement tasks: %w", err)
	}

	if result.DeletedRecords > 0 || result.DeletedLedgers > 0 || result.DeletedReserves > 0 || result.DeletedTasks > 0 {
		log.Infof("billing retention: cutoff=%s records=%d ledgers=%d reservations=%d tasks=%d sealed_days=%d truncated=%v",
			cutoff.Format("2006-01-02"), result.DeletedRecords, result.DeletedLedgers, result.DeletedReserves, result.DeletedTasks, result.SealedDays, result.Truncated)
	}
	return result, nil
}

// sealAndDeleteExpiredRecords 逐日固化并删除超期明细, 返回固化天数、删除行数、因申诉保留的行数、是否被单轮上限截断。
//
// 一天一个事务: 读当天明细 → 算四个维度的汇总 → 写汇总表 → 删这些明细, 四步同进同退。
// 事务边界选"天"而不是"整轮", 是为了让转发路径的写入能在天与天之间插进来。
//
// archiveLimit 是本次归档覆盖到的最大明细主键(0 表示不设上界, 即归档关闭);
// 删除侧拿它当硬上界, 保证"删掉的每一条都已经写进归档文件"。这个上界不能只靠 created_at:
// 归档读与删除之间存在时间差, 期间新写入的行可能也落进同一个截止点之前 —— 按时间戳删就会删到
// 没归档的那条, 而删除不可逆。
func sealAndDeleteExpiredRecords(ctx context.Context, cutoff time.Time, archiveLimit uint64) (sealedDays int, deleted int64, retained int64, truncated bool, err error) {
	for processed := 0; processed < billingRetentionMaxDaysPerRun; processed++ {
		if err := ctx.Err(); err != nil {
			return sealedDays, deleted, retained, truncated, err
		}
		if deleted >= billingRetentionMaxRecordsPerRun {
			return sealedDays, deleted, retained, true, nil
		}
		var head []model.BillingRecord
		headQuery := db.GetDB().WithContext(ctx).Model(&model.BillingRecord{}).
			Where(billingRecordDeletable, cutoff, model.DisputeStatusRejected)
		if archiveLimit > 0 {
			headQuery = headQuery.Where("id <= ?", archiveLimit)
		}
		if err := headQuery.Order("created_at ASC").Limit(1).Find(&head).Error; err != nil {
			return sealedDays, deleted, retained, truncated, err
		}
		if len(head) == 0 {
			break
		}
		done, err := sealAndDeleteBillingDay(ctx, dayStart(head[0].CreatedAt), cutoff, archiveLimit)
		if err != nil {
			return sealedDays, deleted, retained, truncated, err
		}
		if done > 0 {
			sealedDays++
			deleted += done
		} else {
			// 当天明细都在截止点之后或全被申诉保护: 不该再循环到同一天, 直接结束。
			break
		}
	}
	if err := db.GetDB().WithContext(ctx).Model(&model.BillingRecord{}).
		Where("created_at < ? AND EXISTS (SELECT 1 FROM disputes WHERE disputes.record_id = billing_records.id AND disputes.status <> ?)", cutoff, model.DisputeStatusRejected).
		Count(&retained).Error; err != nil {
		return sealedDays, deleted, retained, truncated, fmt.Errorf("count dispute-protected records: %w", err)
	}
	return sealedDays, deleted, retained, truncated, nil
}

// sealAndDeleteBillingDay 固化并删除某一天的超期明细; 返回删除行数(0 表示这一天没有可删的行)。
func sealAndDeleteBillingDay(ctx context.Context, day, cutoff time.Time, archiveLimit uint64) (int64, error) {
	var deleted int64
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		days := day.AddDate(0, 0, 1)
		rows := make([]model.BillingRecord, 0)
		rowQuery := tx.Model(&model.BillingRecord{}).
			Where(billingRecordDeletable+" AND created_at >= ? AND created_at < ?", cutoff, model.DisputeStatusRejected, day, days)
		if archiveLimit > 0 {
			rowQuery = rowQuery.Where("id <= ?", archiveLimit)
		}
		if err := rowQuery.Order("id ASC").Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}

		agg := billingDayRollups(rows, day)
		for i := range agg {
			if err := sealBillingRollup(tx, &agg[i]); err != nil {
				return err
			}
		}

		ids := make([]uint64, 0, len(rows))
		for i := range rows {
			ids = append(ids, rows[i].ID)
		}
		// 删除语句仍然带申诉保护: 读与删之间理论上不新增申诉, 但把保护条件写进 DELETE
		// 让"即使将来有第二个写入者"也不会删到受保护行 —— 删除是不可逆的, 多一道保险值。
		del := tx.Exec("DELETE FROM billing_records WHERE id IN ? AND NOT EXISTS (SELECT 1 FROM disputes WHERE disputes.record_id = billing_records.id AND disputes.status <> ?)", ids, model.DisputeStatusRejected)
		if del.Error != nil {
			return fmt.Errorf("delete expired billing records: %w", del.Error)
		}
		deleted = del.RowsAffected
		return nil
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}

// billingDayRollups 把一天的明细按四个维度(user / owner / channel / platform)聚合成汇总行。
//
// 为什么四个维度各自成行: 各维度的口径不同 —— 用户维度看自己花了多少, 渠道维度看这个渠道
// 贡献了多少毛利, 平台维度是全体合计。混成一行就说不清"这行是谁的和", 而把四个维度分别存,
// 每个读口只取自己那一维, 语义单义。
//
// 退款拆成单独一组列: 有的读口计全部明细(累计消费), 有的只计未退款部分(提现可提额度、渠道毛利)。
// 只存一个合计, 必有一方算错; 存了"合计 + 其中退款", 另一方用减法即可, 且加法仍然精确。
func billingDayRollups(rows []model.BillingRecord, day time.Time) []model.BillingRollup {
	keys := make([]string, 0, 4)
	byKey := make(map[string]*model.BillingRollup, 4)
	ref := func(dimension string, id uint) *model.BillingRollup {
		key := fmt.Sprintf("%s/%d", dimension, id)
		if row, ok := byKey[key]; ok {
			return row
		}
		row := &model.BillingRollup{Dimension: dimension, DimensionID: id, Day: day.Format("20060102")}
		byKey[key] = row
		keys = append(keys, key)
		return row
	}
	accumulate := func(row *model.BillingRollup, record *model.BillingRecord) {
		row.InputToken += record.InputToken
		row.OutputToken += record.OutputToken
		row.CacheReadToken += record.CacheReadToken
		row.CacheWriteToken += record.CacheWriteToken
		row.RequestCount++
		row.UserCost += record.UserCost
		row.OwnerRevenue += record.OwnerRevenue
		row.PlatformRevenue += record.PlatformRevenue
		row.SourceRecords++
		if record.Status == model.BillingStatusRefunded {
			row.RefundedRecords++
			row.RefundedUserCost += record.UserCost
			row.RefundedOwnerRevenue += record.OwnerRevenue
			row.RefundedPlatformRevenue += record.PlatformRevenue
		}
	}
	for i := range rows {
		record := &rows[i]
		// user/owner/channel 的 id 为 0 表示"没有归属"(计数器/历史脏数据), 给它建汇总行
		// 只会在全平台合计里制造一个语义不明的桶; 平台维度已经单独统计全体, 故跳过。
		if record.UserID != 0 {
			accumulate(ref(model.BillingRollupUser, record.UserID), record)
		}
		if record.OwnerID != 0 {
			accumulate(ref(model.BillingRollupOwner, record.OwnerID), record)
		}
		if record.ChannelID != 0 {
			accumulate(ref(model.BillingRollupChannel, uint(record.ChannelID)), record)
		}
		accumulate(ref(model.BillingRollupPlatform, 0), record)
	}
	rollups := make([]model.BillingRollup, 0, len(keys))
	for _, key := range keys {
		rollups = append(rollups, *byKey[key])
	}
	return rollups
}

// sealBillingRollup 把一行当天的聚合累加进汇总表: 没有就插入, 有就累加。
//
// 为什么是"累加"而不是"覆盖": 同一天可能被固化多次 —— 一条明细先前因未驳回的申诉被保留,
// 申诉被驳回后它才变成可删, 那一轮会带着"剩下的那些明细"再固化一次。覆盖会把上一轮的数抹掉,
// 累加才能让"汇总表里的数 = 已经删掉的明细之和"这个恒等式一直成立。
//
// 为什么读-改-写不需要 SELECT FOR UPDATE: 本函数只在 sealAndDeleteBillingDay 的事务里被调用,
// 事务从第一条写语句起就持有该库的写锁(SQLite 单写者, MySQL/PG 由行锁与唯一索引兜底)。
// 多实例部署时要靠唯一索引 + ON CONFLICT 才安全 —— 那属于多活改造, 当前明确不做。
func sealBillingRollup(tx *gorm.DB, row *model.BillingRollup) error {
	var existing model.BillingRollup
	err := tx.Where("dimension = ? AND dimension_id = ? AND day = ?", row.Dimension, row.DimensionID, row.Day).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		now := time.Now()
		row.SealedAt = now
		if err := tx.Create(row).Error; err != nil {
			return fmt.Errorf("create billing rollup %s/%d/%s: %w", row.Dimension, row.DimensionID, row.Day, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read billing rollup: %w", err)
	}
	existing.InputToken += row.InputToken
	existing.OutputToken += row.OutputToken
	existing.CacheReadToken += row.CacheReadToken
	existing.CacheWriteToken += row.CacheWriteToken
	existing.RequestCount += row.RequestCount
	existing.UserCost += row.UserCost
	existing.OwnerRevenue += row.OwnerRevenue
	existing.PlatformRevenue += row.PlatformRevenue
	existing.RefundedRecords += row.RefundedRecords
	existing.RefundedUserCost += row.RefundedUserCost
	existing.RefundedOwnerRevenue += row.RefundedOwnerRevenue
	existing.RefundedPlatformRevenue += row.RefundedPlatformRevenue
	existing.SourceRecords += row.SourceRecords
	existing.SealedAt = time.Now()
	if err := tx.Save(&existing).Error; err != nil {
		return fmt.Errorf("update billing rollup %s/%d/%s: %w", row.Dimension, row.DimensionID, row.Day, err)
	}
	return nil
}

// deleteExpiredLedgers 归档后按留存期删除余额流水。
//
// 流水是"余额为什么变了"的证据链: 删掉之后, 超期的历史变动只剩归档 NDJSON 一份可查。
// 这正是必须先归档再删的原因, 也是留存天数与审计对齐的原因。
//
// archiveLimit 是本次归档覆盖到的最大主键(0 表示归档关闭, 不设上界): 删除只能落在已归档的
// 范围内, 否则会出现"删了但没归档"的行, 而删除不可逆。
func deleteExpiredLedgers(ctx context.Context, cutoff time.Time, archiveLimit uint64) (int64, error) {
	var deleted int64
	last := uint64(0)
	for {
		if err := ctx.Err(); err != nil {
			return deleted, err
		}
		var ids []uint64
		query := db.GetDB().WithContext(ctx).Model(&model.BillingLedger{}).
			Where("created_at < ? AND id > ?", cutoff, last)
		if archiveLimit > 0 {
			query = query.Where("id <= ?", archiveLimit)
		}
		if err := query.Order("id ASC").Limit(billingRetentionBatch).Pluck("id", &ids).Error; err != nil {
			return deleted, err
		}
		if len(ids) == 0 {
			break
		}
		del := db.GetDB().WithContext(ctx).Where("id IN ?", ids).Delete(&model.BillingLedger{})
		if del.Error != nil {
			return deleted, del.Error
		}
		deleted += del.RowsAffected
		last = ids[len(ids)-1]
	}
	return deleted, nil
}

// deleteExpiredReservations 删除"已结算 + 超期 + 没有结算意图"的预扣行。
//
// 三个条件缺一不可:
//   - settled = true: 未结算的预扣仍然是"待处理的资金占用", 删了会让那笔钱永远解冻不了;
//   - created_at < cutoff: 只清超期;
//   - 无 billing_settlement_tasks 行: BillingReleaseStale 用 NOT EXISTS 判断结算意图,
//     删掉任务行会让它在下一轮把一笔仍在等结算的预扣当成泄漏释放 —— 冻结额退了,
//     随后结算再落账, 就是同一笔钱既退又扣。
//
// archiveLimit 语义同 deleteExpiredLedgers。
func deleteExpiredReservations(ctx context.Context, cutoff time.Time, archiveLimit uint64) (int64, error) {
	var deleted int64
	last := uint64(0)
	for {
		if err := ctx.Err(); err != nil {
			return deleted, err
		}
		var ids []uint64
		query := db.GetDB().WithContext(ctx).Model(&model.BalanceReservation{}).
			Where("settled = ? AND created_at < ? AND id > ?", true, cutoff, last).
			Where("NOT EXISTS (SELECT 1 FROM billing_settlement_tasks WHERE billing_settlement_tasks.reservation_id = balance_reservations.id)")
		if archiveLimit > 0 {
			query = query.Where("id <= ?", archiveLimit)
		}
		if err := query.Order("id ASC").Limit(billingRetentionBatch).Pluck("id", &ids).Error; err != nil {
			return deleted, err
		}
		if len(ids) == 0 {
			break
		}
		del := db.GetDB().WithContext(ctx).Where("id IN ? AND settled = ?", ids, true).Delete(&model.BalanceReservation{})
		if del.Error != nil {
			return deleted, del.Error
		}
		deleted += del.RowsAffected
		last = ids[len(ids)-1]
	}
	return deleted, nil
}

// deleteExpiredSettlementTasks 只删除"completed + 超期 + 对应预扣已结算"的结算任务行。
//
// 为什么只删 completed: pending/retry 还没跑完, manual 是人工对账的唯一证据
// (自动路径已经放弃判断, 人要看的就是那张快照)。completed 意味着钱已经动完,
// 权威留痕在 billing_records 与 balance_ledgers, 任务行里的快照早就被清空了。
//
// 为什么还要再查一次预扣: completed 正常意味着预扣已结算, 但"正常"不等于"一定"。
// 多加一条 EXISTS(settled = false) 的排除, 保证即使出现异常行序也不会让
// BillingReleaseStale 把一笔未结算的预扣误判成"没有结算意图"。
//
// archiveLimit 语义同 deleteExpiredLedgers。
func deleteExpiredSettlementTasks(ctx context.Context, cutoff time.Time, archiveLimit uint64) (int64, error) {
	var deleted int64
	last := uint64(0)
	for {
		if err := ctx.Err(); err != nil {
			return deleted, err
		}
		var ids []uint64
		query := db.GetDB().WithContext(ctx).Model(&model.BillingSettlementTask{}).
			Where("status = ? AND updated_at < ? AND id > ?", model.BillingSettlementStatusCompleted, cutoff, last).
			Where("NOT EXISTS (SELECT 1 FROM balance_reservations WHERE balance_reservations.id = billing_settlement_tasks.reservation_id AND balance_reservations.settled = ?)", false)
		if archiveLimit > 0 {
			query = query.Where("id <= ?", archiveLimit)
		}
		if err := query.Order("id ASC").Limit(billingRetentionBatch).Pluck("id", &ids).Error; err != nil {
			return deleted, err
		}
		if len(ids) == 0 {
			break
		}
		del := db.GetDB().WithContext(ctx).Where("id IN ? AND status = ?", ids, model.BillingSettlementStatusCompleted).Delete(&model.BillingSettlementTask{})
		if del.Error != nil {
			return deleted, del.Error
		}
		deleted += del.RowsAffected
		last = ids[len(ids)-1]
	}
	return deleted, nil
}

// billingArchiveCounts 四张表各自归档了多少条; total 供日志与告警用。
type billingArchiveCounts struct {
	Records      int `json:"records"`
	Ledgers      int `json:"ledgers"`
	Reservations int `json:"reservations"`
	Tasks        int `json:"tasks"`
}

func (c billingArchiveCounts) total() int {
	return c.Records + c.Ledgers + c.Reservations + c.Tasks
}

// billingArchiveLimits 本次归档覆盖到的四张表最大主键; 删除侧用它做硬上界。
// 0 表示该表本次没有归档任何行, 删除侧相应地也一条都不删。
type billingArchiveLimits struct {
	Records      uint64
	Ledgers      uint64
	Reservations uint64
	Tasks        uint64
}

// archiveBillingBefore 把超期的四张资金表记录原样追加写入当日 NDJSON, 返回路径、条数与归档上界。
//
// 一次写一个文件而不是每张表一个文件: 这四张表的同一条业务事件被拆在四张表里
// (预扣 → 明细 → 流水 → 结算任务), 分开归档后要靠 request_id 手工拼接才看得懂。
// 一条记录一行、带 table 字段, 既能整份回放, 也能按表过滤。
//
// 落盘顺序与审计归档一致: 先编码写出, 再 file.Sync()。不 Sync 就返回成功的话,
// 后续删库把记录清掉, 而归档可能还在页缓存里 —— 掉电就同时失去两份。
func archiveBillingBefore(ctx context.Context, cutoff time.Time) (string, billingArchiveCounts, billingArchiveLimits, error) {
	counts := billingArchiveCounts{}
	limits := billingArchiveLimits{}
	var records []model.BillingRecord
	if err := db.GetDB().WithContext(ctx).Model(&model.BillingRecord{}).
		Where(billingRecordDeletable, cutoff, model.DisputeStatusRejected).
		Order("id ASC").Limit(billingRetentionMaxRecordsPerRun).Find(&records).Error; err != nil {
		return "", counts, limits, fmt.Errorf("load billing records for archive: %w", err)
	}
	var ledgers []model.BillingLedger
	if err := db.GetDB().WithContext(ctx).Model(&model.BillingLedger{}).
		Where("created_at < ?", cutoff).Order("id ASC").Find(&ledgers).Error; err != nil {
		return "", counts, limits, fmt.Errorf("load balance ledgers for archive: %w", err)
	}
	var reservations []model.BalanceReservation
	if err := db.GetDB().WithContext(ctx).Model(&model.BalanceReservation{}).
		Where("settled = ? AND created_at < ?", true, cutoff).
		Where("NOT EXISTS (SELECT 1 FROM billing_settlement_tasks WHERE billing_settlement_tasks.reservation_id = balance_reservations.id)").
		Order("id ASC").Find(&reservations).Error; err != nil {
		return "", counts, limits, fmt.Errorf("load reservations for archive: %w", err)
	}
	var tasks []model.BillingSettlementTask
	if err := db.GetDB().WithContext(ctx).Model(&model.BillingSettlementTask{}).
		Where("status = ? AND updated_at < ?", model.BillingSettlementStatusCompleted, cutoff).
		Order("id ASC").Find(&tasks).Error; err != nil {
		return "", counts, limits, fmt.Errorf("load settlement tasks for archive: %w", err)
	}
	// 四张表各记一个"本次归档覆盖到的最大主键": 删除侧用它做硬上界, 保证"删掉的每一条都已归档"。
	// 只按 created_at 上界是不够的 —— 归档读与删除之间存在时间差, 期间新写入的行也可能落进
	// 同一个截止点之前; 按时间戳删就会删到没归档的那条, 而删除不可逆。
	for i := range records {
		if records[i].ID > limits.Records {
			limits.Records = records[i].ID
		}
	}
	for i := range ledgers {
		if ledgers[i].ID > limits.Ledgers {
			limits.Ledgers = ledgers[i].ID
		}
	}
	for i := range reservations {
		if reservations[i].ID > limits.Reservations {
			limits.Reservations = reservations[i].ID
		}
	}
	for i := range tasks {
		if tasks[i].ID > limits.Tasks {
			limits.Tasks = tasks[i].ID
		}
	}
	counts = billingArchiveCounts{Records: len(records), Ledgers: len(ledgers), Reservations: len(reservations), Tasks: len(tasks)}
	if counts.total() == 0 {
		return "", counts, limits, nil
	}
	if err := os.MkdirAll(BillingArchiveDir(), 0o750); err != nil {
		return "", counts, limits, fmt.Errorf("create billing archive dir: %w", err)
	}
	path := filepath.Join(BillingArchiveDir(), "billing-"+time.Now().Format("20060102")+".ndjson")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return "", counts, limits, fmt.Errorf("open billing archive file: %w", err)
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	encode := func(table string, row any) error {
		return encoder.Encode(map[string]any{"table": table, "row": row})
	}
	for i := range records {
		if err := encode("billing_records", records[i]); err != nil {
			return "", counts, limits, fmt.Errorf("write billing archive record: %w", err)
		}
	}
	for i := range ledgers {
		if err := encode("balance_ledgers", ledgers[i]); err != nil {
			return "", counts, limits, fmt.Errorf("write billing archive ledger: %w", err)
		}
	}
	for i := range reservations {
		if err := encode("balance_reservations", reservations[i]); err != nil {
			return "", counts, limits, fmt.Errorf("write billing archive reservation: %w", err)
		}
	}
	for i := range tasks {
		if err := encode("billing_settlement_tasks", tasks[i]); err != nil {
			return "", counts, limits, fmt.Errorf("write billing archive task: %w", err)
		}
	}
	if err := file.Sync(); err != nil {
		return "", counts, limits, fmt.Errorf("sync billing archive file: %w", err)
	}
	return path, counts, limits, nil
}

// billingRollupTotal 从汇总表读某维度某 id 的全期合计。
//
// refundedOnly 为 true 时返回"其中已退款"的部分; 两类读口靠它区分口径,
// 不必为两种口径各写一条查询。
func billingRollupTotal(ctx context.Context, dimension string, id uint, refundedOnly bool) (BillingAggregate, error) {
	return billingRollupTotalRange(ctx, dimension, id, refundedOnly, "", "")
}

// billingRollupTotalRange 是 billingRollupTotal 的带日期区间版本; from/to 为空表示不限。
// 区间用 day(YYYYMMDD 定长串)比较, 与截止点对齐到整天的口径一致, 也避免了方言专属的日期函数。
func billingRollupTotalRange(ctx context.Context, dimension string, id uint, refundedOnly bool, from, to string) (BillingAggregate, error) {
	total := BillingAggregate{}
	columns := "COALESCE(SUM(input_token), 0) AS input_token," +
		" COALESCE(SUM(output_token), 0) AS output_token," +
		" COALESCE(SUM(cache_read_token), 0) AS cache_read_token," +
		" COALESCE(SUM(cache_write_token), 0) AS cache_write_token," +
		" COALESCE(SUM(request_count), 0) AS request_count," +
		" COALESCE(SUM(user_cost), 0) AS user_cost," +
		" COALESCE(SUM(owner_revenue), 0) AS owner_revenue," +
		" COALESCE(SUM(platform_revenue), 0) AS platform_revenue"
	if refundedOnly {
		// 退款行没有独立的 token 列: 退的是钱, 用量早就发生了。这里把 token/次数一并置 0,
		// 让"全期合计 - 退款部分"只对金额有意义, 不会被误用成"退款了多少 token"。
		columns = "0 AS input_token, 0 AS output_token, 0 AS cache_read_token, 0 AS cache_write_token," +
			" COALESCE(SUM(refunded_records), 0) AS request_count," +
			" COALESCE(SUM(refunded_user_cost), 0) AS user_cost," +
			" COALESCE(SUM(refunded_owner_revenue), 0) AS owner_revenue," +
			" COALESCE(SUM(refunded_platform_revenue), 0) AS platform_revenue"
	}
	query := db.GetDB().WithContext(ctx).Model(&model.BillingRollup{}).
		Select(columns).
		Where("dimension = ? AND dimension_id = ?", dimension, id)
	if from != "" {
		query = query.Where("day >= ?", from)
	}
	if to != "" {
		query = query.Where("day <= ?", to)
	}
	if err := query.Scan(&total).Error; err != nil {
		return BillingAggregate{}, err
	}
	return total, nil
}

// billingRollupAggregates 从汇总表读某维度在 since 之后的日/月聚合, 供曲线类读口与明细并集。
//
// 在 Go 侧归月桶而不是在 SQL 里拼: 汇总表一天最多 4 行(四个维度), 一年也就一千多行,
// 取回成本可忽略; 而"日期归桶"的字符串拼接在 SQLite(||) 与 MySQL(CONCAT) 上写法不同,
// 为省这点数据去引一条方言分支, 收益远小于维护成本。
func billingRollupAggregates(ctx context.Context, dimension string, id uint, since time.Time, granularity string) ([]BillingAggregate, error) {
	rows := make([]model.BillingRollup, 0)
	if err := db.GetDB().WithContext(ctx).Model(&model.BillingRollup{}).
		Where("dimension = ? AND dimension_id = ? AND day >= ?", dimension, id, dayKey(since)).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	byDate := make(map[string]*BillingAggregate, len(rows))
	keys := make([]string, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		if len(row.Day) != 8 {
			continue
		}
		date := row.Day
		if granularity == "month" {
			date = row.Day[:6] + "01"
		}
		agg, ok := byDate[date]
		if !ok {
			agg = &BillingAggregate{Date: date}
			byDate[date] = agg
			keys = append(keys, date)
		}
		agg.InputToken += row.InputToken
		agg.OutputToken += row.OutputToken
		agg.CacheReadToken += row.CacheReadToken
		agg.CacheWriteToken += row.CacheWriteToken
		agg.RequestCount += row.RequestCount
		agg.UserCost += row.UserCost
		agg.OwnerRevenue += row.OwnerRevenue
		agg.PlatformRevenue += row.PlatformRevenue
	}
	sort.Strings(keys)
	out := make([]BillingAggregate, 0, len(keys))
	for _, key := range keys {
		out = append(out, *byDate[key])
	}
	return out, nil
}

// dayKey 把一个时刻换成汇总表的日键 YYYYMMDD(本地挂钟), 与 BillingAggregate.Date 同一口径。
func dayKey(t time.Time) string {
	return t.In(time.Local).Format("20060102")
}

// mergeBillingAggregates 合并明细桶与汇总桶, 按日期升序。
//
// 两者不会重复计数: 某天的明细被删之前已经整日固化进汇总表, 同一天不可能既在汇总里
// 又在明细里(因申诉保护而留下的明细不进汇总)。合并只是把"半年内"和"半年外"拼成一条连续曲线。
func mergeBillingAggregates(live, rollups []BillingAggregate) []BillingAggregate {
	if len(rollups) == 0 {
		return live
	}
	byDate := make(map[string]*BillingAggregate, len(live)+len(rollups))
	keys := make([]string, 0, len(live)+len(rollups))
	add := func(rows []BillingAggregate) {
		for i := range rows {
			row := rows[i]
			agg, ok := byDate[row.Date]
			if !ok {
				copyRow := row
				byDate[row.Date] = &copyRow
				keys = append(keys, row.Date)
				continue
			}
			agg.InputToken += row.InputToken
			agg.OutputToken += row.OutputToken
			agg.CacheReadToken += row.CacheReadToken
			agg.CacheWriteToken += row.CacheWriteToken
			agg.RequestCount += row.RequestCount
			agg.UserCost += row.UserCost
			agg.OwnerRevenue += row.OwnerRevenue
			agg.PlatformRevenue += row.PlatformRevenue
		}
	}
	add(live)
	add(rollups)
	sort.Strings(keys)
	out := make([]BillingAggregate, 0, len(keys))
	for _, key := range keys {
		out = append(out, *byDate[key])
	}
	return out
}

// billingRollupSettledDaily 读某维度在 [since, until) 内的"未退款部分"逐日聚合。
//
// 只计 settled 的读口(渠道毛利)不能用 billingRollupAggregates: 它给的是全部明细的合计。
// 这里用"合计 - 其中退款"还原出与明细侧 status = settled 完全相同的口径, 合并后曲线才不会
// 在留存边界上突然跳变(边界内只算 settled, 边界外算成含退款, 会凭空多出一截成本)。
func billingRollupSettledDaily(ctx context.Context, dimension string, id uint, since, until time.Time) ([]BillingAggregate, error) {
	rows := make([]model.BillingRollup, 0)
	query := db.GetDB().WithContext(ctx).Model(&model.BillingRollup{}).
		Where("dimension = ? AND dimension_id = ? AND day >= ?", dimension, id, dayKey(since))
	if !until.IsZero() {
		query = query.Where("day < ?", dayKey(until))
	}
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]BillingAggregate, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		out = append(out, BillingAggregate{
			Date:            row.Day,
			InputToken:      row.InputToken,
			OutputToken:     row.OutputToken,
			CacheReadToken:  row.CacheReadToken,
			CacheWriteToken: row.CacheWriteToken,
			RequestCount:    row.RequestCount - row.RefundedRecords,
			UserCost:        row.UserCost - row.RefundedUserCost,
			OwnerRevenue:    row.OwnerRevenue - row.RefundedOwnerRevenue,
			PlatformRevenue: row.PlatformRevenue - row.RefundedPlatformRevenue,
		})
	}
	return out, nil
}

// billingWindowArchived 报告某个时间窗(按自然日闭区间)的明细是否已经被留存清理归档过。
//
// 判据是"该窗口内存在任何汇总行": 汇总行只在删除明细之前才会写, 所以有汇总行就意味着
// "这里曾经有明细, 现在明细不在了"。返回 true 时调用方必须把它当成数据不完整来处理,
// 而不是当成"这段时间没有用量" —— 后者会让对账页安静地算出一个假差额, 人再据此去解释,
// 错就进了人工结论。
func billingWindowArchived(ctx context.Context, dimension string, id uint, start, end time.Time) (bool, error) {
	var count int64
	if err := db.GetDB().WithContext(ctx).Model(&model.BillingRollup{}).
		Where("dimension = ? AND dimension_id = ? AND day >= ? AND day <= ?", dimension, id, dayKey(start), dayKey(end)).
		Count(&count).Error; err != nil {
		return false, fmt.Errorf("check billing rollup window: %w", err)
	}
	return count > 0, nil
}

// ErrBillingWindowArchived 表示所查时间窗的明细已因留存策略被归档清理, 结果不可用于对账。
var ErrBillingWindowArchived = errors.New("billing detail for this period has been archived by the retention policy; the window no longer supports this calculation")

// BillingWindowArchived 供 HTTP 层判断"这个月的明细是不是已经被留存清理掉"。
// 导出而不是让 handler 直接查汇总表: 判定口径(哪个维度、日键怎么比)只应有一处定义。
func BillingWindowArchived(ctx context.Context, dimension string, id uint, start, end time.Time) (bool, error) {
	return billingWindowArchived(ctx, dimension, id, start, end)
}

// BillingArchiveList 返回资金账归档目录下的 NDJSON 文件清单。
// 与审计/日志归档清单同一形状(name/size/mod_time), 前端运维页可以复用同一张表。
func BillingArchiveList() ([]map[string]any, error) {
	entries, err := os.ReadDir(BillingArchiveDir())
	if err != nil {
		// 目录不存在 = 还没清理过, 不是错误。返回空清单而不是报错, 前端才能正常显示"暂无归档"。
		if os.IsNotExist(err) {
			return []map[string]any{}, nil
		}
		return nil, err
	}
	files := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".ndjson") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, map[string]any{
			"name":     entry.Name(),
			"size":     info.Size(),
			"mod_time": info.ModTime(),
		})
	}
	return files, nil
}

// BillingRetentionStatus 供运维页只读展示当前留存配置与实际存量。
//
// 为什么要带 rows 而不是只回配置: 管理员改留存天数时最关心的是"到底还有多少行在占空间",
// 只给一个数字档位是看不出效果的。这里只做 COUNT(走索引), 不扫全表。
func BillingRetentionStatus(ctx context.Context) (map[string]any, error) {
	type counts struct {
		Records      int64
		Ledgers      int64
		Reservations int64
		Tasks        int64
		Rollups      int64
	}
	var c counts
	table := func(model any, field *int64) error {
		return db.GetDB().WithContext(ctx).Model(model).Count(field).Error
	}
	for _, pair := range []struct {
		model any
		field *int64
	}{
		{&model.BillingRecord{}, &c.Records},
		{&model.BillingLedger{}, &c.Ledgers},
		{&model.BalanceReservation{}, &c.Reservations},
		{&model.BillingSettlementTask{}, &c.Tasks},
		{&model.BillingRollup{}, &c.Rollups},
	} {
		if err := table(pair.model, pair.field); err != nil {
			return nil, err
		}
	}
	cutoff := billingRetentionCutoff(BillingRetentionDays())
	return map[string]any{
		"retention_days":  BillingRetentionDays(),
		"archive_enabled": BillingArchiveEnabled(),
		"cutoff":          cutoff.Format("2006-01-02"),
		"counts": map[string]any{
			"billing_records":          c.Records,
			"balance_ledgers":          c.Ledgers,
			"balance_reservations":     c.Reservations,
			"billing_settlement_tasks": c.Tasks,
			"billing_rollups":          c.Rollups,
		},
	}, nil
}

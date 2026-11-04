package op

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
)

const (
	BillingLeaseDuration            = 5 * time.Minute
	BillingLeaseHeartbeatInterval   = time.Minute
	BillingSettlementMaxAttempts    = 8
	BillingSettlementRetryBaseDelay = 2 * time.Second
	BillingSettlementRetryMaxDelay  = 5 * time.Minute
)

var ErrSettlementIntentPending = errors.New("settlement intent pending")
var ErrSettlementQueued = errors.New("settlement persisted for recovery")

// billingValidateSnapshot is shared by enqueue and recovery; stored JSON is not trusted.
func billingValidateSnapshot(r *model.BillingRecord) error {
	values := []float64{r.UserCost, r.OwnerRevenue, r.MediaUnits.Seconds, r.MarkupRatio, r.SupplyPrice.Input, r.SupplyPrice.Output, r.SupplyPrice.CacheRead, r.SupplyPrice.CacheWrite, r.UserPrice.Input, r.UserPrice.Output, r.UserPrice.CacheRead, r.UserPrice.CacheWrite}
	for _, price := range []model.MediaPrice{r.MediaSupply, r.MediaUser} {
		values = append(values, price.PerImage, price.PerVideo, price.PerSecond)
		for _, value := range price.Resolution {
			values = append(values, value)
		}
	}
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return errors.New("invalid billing snapshot value")
		}
	}
	if r.InputToken < 0 || r.OutputToken < 0 || r.CacheReadToken < 0 || r.CacheWriteToken < 0 || r.MediaUnits.Images < 0 || r.MediaUnits.Videos < 0 {
		return errors.New("negative billing usage")
	}
	if !billingMoneyEqual(r.PlatformRevenue, r.UserCost-r.OwnerRevenue) {
		return errors.New("unbalanced billing snapshot")
	}
	return nil
}

func BillingRenewLease(id uint64, ttl time.Duration) error {
	if ttl <= 0 {
		return errors.New("lease duration must be positive")
	}
	now := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result := db.GetDB().WithContext(ctx).Model(&model.BalanceReservation{}).
		Where("id = ? AND settled = ? AND (lease_expires_at IS NULL OR lease_expires_at > ?)", id, false, now).
		Update("lease_expires_at", now.Add(ttl))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("reservation lease unavailable")
	}
	return nil
}

// BillingSettle persists an immutable intent before money is moved. A queued error means durable, not settled.
func BillingSettle(id uint64, record *model.BillingRecord) error {
	if record == nil {
		return errors.New("billing record is required")
	}
	snapshot := *record
	snapshot.ID = 0
	snapshot.PlatformRevenue = snapshot.UserCost - snapshot.OwnerRevenue
	snapshot.Status = model.BillingStatusSettled
	if snapshot.CreatedAt.IsZero() {
		snapshot.CreatedAt = time.Now()
	}
	if err := billingValidateSnapshot(&snapshot); err != nil {
		return err
	}
	for _, value := range []float64{snapshot.UserCost, snapshot.OwnerRevenue, snapshot.PlatformRevenue, snapshot.MediaUnits.Seconds} {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return errors.New("invalid billing amount")
		}
	}
	if snapshot.UserCost < 0 || snapshot.OwnerRevenue < 0 || snapshot.InputToken < 0 || snapshot.OutputToken < 0 || snapshot.CacheReadToken < 0 || snapshot.CacheWriteToken < 0 || snapshot.MediaUnits.Images < 0 || snapshot.MediaUnits.Videos < 0 || snapshot.MediaUnits.Seconds < 0 {
		return errors.New("negative billing usage or cost")
	}
	body, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode billing snapshot: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var task model.BillingSettlementTask
	finalized := false
	err = db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// First statement takes the same row writer lock as release/settle (including MySQL RR).
		if err := tx.Exec("UPDATE balance_reservations SET settled = settled WHERE id = ?", id).Error; err != nil {
			return err
		}
		var reservation model.BalanceReservation
		if err := tx.First(&reservation, id).Error; err != nil {
			return err
		}
		if err := billingRecordMatches(&reservation, &snapshot); err != nil {
			return err
		}
		if reservation.Settled {
			finalized = true
			return nil
		}
		err := tx.Where("reservation_id = ?", id).First(&task).Error
		if err == nil {
			var original model.BillingRecord
			if json.Unmarshal([]byte(task.Snapshot), &original) != nil {
				return errors.New("stored billing snapshot corrupt; manual review required")
			}
			if !billingSameUsage(&original, &snapshot) {
				return ErrSettlementMismatch
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		task = model.BillingSettlementTask{ReservationID: id, RequestID: snapshot.RequestID, UserID: snapshot.UserID, Status: model.BillingSettlementStatusPending, Snapshot: string(body), NextAttemptAt: time.Now()}
		return tx.Create(&task).Error
	})
	if err != nil {
		return err
	}
	if finalized {
		return billingSettleReplay(id, &snapshot)
	}
	if task.Status == model.BillingSettlementStatusManual {
		return fmt.Errorf("%w: manual review", ErrSettlementQueued)
	}
	err = billingRecoveryAttempt(ctx, task.ID)
	if err != nil {
		// Another worker may have completed between our observation and claim.
		var reservation model.BalanceReservation
		if readErr := db.GetDB().WithContext(ctx).First(&reservation, id).Error; readErr == nil && reservation.Settled {
			if replayErr := billingSettleReplay(id, &snapshot); replayErr == nil {
				return nil
			}
		}
		return fmt.Errorf("%w: %w", ErrSettlementQueued, err)
	}
	// Populate caller metadata only after execution; do not substitute current prices on retry.
	var applied model.BillingRecord
	if err := db.GetDB().WithContext(ctx).Where("request_id = ? AND user_id = ?", snapshot.RequestID, snapshot.UserID).First(&applied).Error; err == nil {
		*record = applied
		return nil
	}
	return fmt.Errorf("%w: task awaits scheduled execution", ErrSettlementQueued)
}

func billingRecoveryAttempt(ctx context.Context, id uint64) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var task model.BillingSettlementTask
	if err := db.GetDB().WithContext(ctx).First(&task, id).Error; err != nil {
		return err
	}
	if task.Status == model.BillingSettlementStatusCompleted {
		return nil
	}
	if task.Status != model.BillingSettlementStatusPending && task.Status != model.BillingSettlementStatusRetry {
		return errors.New("task requires manual review")
	}
	now := time.Now()
	if task.NextAttemptAt.After(now) {
		return errors.New("task not due or executing")
	}
	if task.Attempts >= BillingSettlementMaxAttempts {
		status, message := model.BillingSettlementStatusManual, "retry limit reached; inspect original request and ledger"
		// A last-attempt commit followed by a crash needs bookkeeping, not another debit.
		var record model.BillingRecord
		if json.Unmarshal([]byte(task.Snapshot), &record) == nil && billingValidateSnapshot(&record) == nil && record.RequestID == task.RequestID && record.UserID == task.UserID {
			if replayErr := billingSettleReplay(task.ReservationID, &record); replayErr == nil {
				status, message = model.BillingSettlementStatusCompleted, ""
			}
		}
		updates := map[string]any{"status": status, "last_error": message}
		for field, value := range billingSettlementTerminalFields(status) {
			updates[field] = value
		}
		result := db.GetDB().WithContext(ctx).Model(&task).Where("status IN ? AND attempts = ?", []string{model.BillingSettlementStatusPending, model.BillingSettlementStatusRetry}, task.Attempts).Updates(updates)
		if result.Error == nil && result.RowsAffected == 1 && status == model.BillingSettlementStatusManual {
			RaiseAlert(ctx, 0, "系统", "billing_failed", fmt.Sprintf("settlement recovery exhausted: reservation=%d", task.ReservationID))
		}
		return result.Error
	}
	// The execution lease recovers abandoned workers. Money transaction remains independently idempotent.
	claim := db.GetDB().WithContext(ctx).Model(&model.BillingSettlementTask{}).
		Where("id = ? AND status IN ? AND attempts = ? AND next_attempt_at <= ?", id, []string{model.BillingSettlementStatusPending, model.BillingSettlementStatusRetry}, task.Attempts, now).
		Updates(map[string]any{"attempts": task.Attempts + 1, "next_attempt_at": now.Add(2 * time.Minute), "status": model.BillingSettlementStatusRetry})
	if claim.Error != nil {
		return claim.Error
	}
	if claim.RowsAffected != 1 {
		return errors.New("task claimed concurrently")
	}
	task.Attempts++
	var record model.BillingRecord
	err := json.Unmarshal([]byte(task.Snapshot), &record)
	if err == nil {
		err = billingValidateSnapshot(&record)
	}
	if err == nil && (record.UserID != task.UserID || record.RequestID != task.RequestID || record.ID != 0 || record.CreatedAt.IsZero()) {
		err = errors.New("stored snapshot identity invalid")
	}
	corrupt := err != nil
	if err == nil {
		err = billingSettleApply(ctx, task.ReservationID, &record)
	}
	status := model.BillingSettlementStatusCompleted
	message := ""
	next := now
	if err != nil {
		status = model.BillingSettlementStatusRetry
		message = "settlement execution failed; original intent retained"
		if corrupt || errors.Is(err, ErrSettlementMismatch) || errors.Is(err, ErrReservationReleased) || task.Attempts >= BillingSettlementMaxAttempts {
			status = model.BillingSettlementStatusManual
			message = "manual review required; inspect original snapshot, reservation and ledger"
		}
		next = time.Now().Add(billingSettlementBackoff(task.Attempts))
	}
	// A canceled worker must leave a retryable execution lease; do not use an unbounded detached database write.
	updates := map[string]any{"status": status, "last_error": message, "next_attempt_at": next}
	for field, value := range billingSettlementTerminalFields(status) {
		updates[field] = value
	}
	update := db.GetDB().WithContext(ctx).Model(&model.BillingSettlementTask{}).Where("id = ? AND attempts = ? AND status = ?", id, task.Attempts, model.BillingSettlementStatusRetry).
		Updates(updates)
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return errors.New("recovery ownership changed")
	}
	if status == model.BillingSettlementStatusManual {
		RaiseAlert(ctx, 0, "系统", "billing_failed", fmt.Sprintf("settlement recovery requires manual review: reservation=%d", task.ReservationID))
	}
	return err
}

func BillingRecoveryRun(ctx context.Context, limit int) error {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var tasks []model.BillingSettlementTask
	if err := db.GetDB().WithContext(ctx).Where("status IN ? AND next_attempt_at <= ?", []string{model.BillingSettlementStatusPending, model.BillingSettlementStatusRetry}, time.Now()).Order("next_attempt_at ASC").Limit(limit).Find(&tasks).Error; err != nil {
		return err
	}
	var failures []error
	for _, task := range tasks {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := billingRecoveryAttempt(ctx, task.ID); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// BillingRecoveryList 按"先看需要人管的、再看正在重试的、其余已完成"的优先级取前 limit 条。
//
// 为什么分成三次查询而不是一个 CASE 排序: 原写法 ORDER BY CASE ... END, updated_at DESC
// 里第一项不是表里的列, 数据库只能全表扫描再建临时 B 树排序 —— 任何索引都用不上。线上
// 实测 9700 行 14.4 ms, 且这个开销随任务数线性增长(每笔结算一行, 只增不减)。拆成三个
// 等值的单状态查询后, 每一个都能走 (status, updated_at) 复合索引的 SEARCH + 逆序扫描,
// 实测 0.2-0.6 ms。三段的先后顺序就是原 CASE 的优先级, 拼接后再截断到 limit, 结果与
// 原查询逐行一致。
//
// 第二段用 IN ('pending','retry') 而不是 NOT IN ('manual','completed'): 后者语义上更
// "未来安全", 但 NOT IN 会让索引失效(实测退回全表扫描 5.9 ms)。取 IN 的前提是这张表
// 的 status 只有这四个取值 —— 全仓所有写入点都只写 model.BillingSettlementStatus* 常量,
// 没有任何地方拼字符串状态。**若将来新增状态值, 必须同步加进这里**, 否则新状态的行不会
// 出现在恢复列表上。
func BillingRecoveryList(ctx context.Context, limit int) ([]model.BillingSettlementTask, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	tasks := make([]model.BillingSettlementTask, 0, limit)
	for _, statuses := range [][]string{
		{model.BillingSettlementStatusManual},
		{model.BillingSettlementStatusPending, model.BillingSettlementStatusRetry},
		{model.BillingSettlementStatusCompleted},
	} {
		if len(tasks) >= limit {
			break
		}
		batch := []model.BillingSettlementTask{}
		if err := db.GetDB().WithContext(ctx).Omit("snapshot").
			Where("status IN ?", statuses).
			Order("updated_at DESC").
			Limit(limit - len(tasks)).
			Find(&batch).Error; err != nil {
			return nil, err
		}
		tasks = append(tasks, batch...)
	}
	return tasks, nil
}

// billingSettlementTerminalFields 返回"转为该终态时要顺带写入的字段"。
//
// 目前只有一条: 结算成功后清空 snapshot。
//
// 为什么可以清: snapshot 是 773 字节的经济快照, 只在任务待执行时被读 —— 本文件里每一处
// json.Unmarshal(task.Snapshot) 前面都有"status 必须是 pending/retry"的状态门(见 173 行、
// 196 行的 CAS 条件), 而 BillingSettle 那条路径先判 reservation.Settled、终态直接短路,
// 根本走不到读 snapshot。置 completed 意味着 billingSettleApply 已经成功、钱已经动完,
// 权威留痕是 billing_records 与 balance_ledgers, 任务行里的这份副本再没有读者。
//
// 为什么必须清: 这张表每笔结算一行、completed 是绝大多数(线上 8824 行里只有少数非终态),
// 每行 773 字节, 而且自动备份是 VACUUM INTO 物理快照 —— 留着的死字节会被每一份备份整份复制。
//
// 为什么不能分开写: 必须与 status 进同一个 UPDATE。先写 status 再清 snapshot 会开出一个
// "已 completed 但快照还在"的中间态, 而 BillingSettle 里"判 settled"与"读 snapshot"之间存在窗口。
//
// 为什么 manual 不清: 那张快照是人工对账的唯一证据 —— 自动路径已经放弃判断, 人要看的就是它。
//
// 为什么只清列、不删行: BillingReleaseStale 靠 NOT EXISTS 判断"这笔预扣有没有结算意图",
// 删掉任务行会让一笔仍在等待结算的预扣被当成可释放的泄漏。行本身必须留着。
func billingSettlementTerminalFields(status string) map[string]any {
	if status != model.BillingSettlementStatusCompleted {
		return nil
	}
	return map[string]any{"snapshot": ""}
}

// billingSettlementBackoff 有界指数退避: base 起按尝试次数倍增, 封顶 max —— 重试间隔永不无界膨胀。
func billingSettlementBackoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delay := BillingSettlementRetryBaseDelay
	for i := 1; i < attempts; i++ {
		if delay >= BillingSettlementRetryMaxDelay/2 {
			return BillingSettlementRetryMaxDelay
		}
		delay *= 2
	}
	if delay > BillingSettlementRetryMaxDelay {
		return BillingSettlementRetryMaxDelay
	}
	return delay
}

// billingSanitizeError 把落地错误整理成可入库的 LastError 摘要, 供人工排障:
// 裁掉驱动/GORM 附带的 SQL 文本(SQL 常内嵌参数值乃至整段计费快照), 去控制字符, 按字符数限长截断。
// LastError 绝不能变成第二份计费快照, 也不能把 SQL 载荷带出数据库。
func billingSanitizeError(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	for _, marker := range []string{" (SQL:", "(SQL:", " SQL [", "SQL ["} {
		if i := strings.Index(text, marker); i >= 0 {
			text = text[:i]
		}
	}
	text = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, text)
	text = strings.TrimSpace(text)
	const maxErrorRunes = 300
	if runes := []rune(text); len(runes) > maxErrorRunes {
		text = string(runes[:maxErrorRunes])
	}
	return text
}

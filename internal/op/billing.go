package op

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/alert"
	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
)

// 计费规则(需求 8):
//   - 供货价 supply: 发布模型的四类单价(读/写/缓存读/缓存写, 每百万 token), 未设置即 0;
//   - 用户价 user_price = supply × (1 + markup_ratio), markup_ratio 为管理员全局配置;
//   - 用户支出 = 用量 × 用户价; 发布者收入 = 用量 × 供货价; 平台收入 = 两者差额;
//   - 转发前按估算预扣(BillingReserve), 结束按实际用量结算(BillingSettle), 无用量即释放(BillingRelease);
//   - 超时未结算的预扣由 BillingReleaseStale 回滚, 防进程崩溃导致冻结额泄漏;
//   - 全量计费不区分角色(2026-10-08 起取消 admin 豁免): 余额扣减必须与调用费用严格一致。

// errInsufficientBalance 预扣失败的哨兵错误: 供调用方与告警逻辑精确区分"余额不足"与其他数据库错误。
var errInsufficientBalance = errors.New("insufficient balance")

// 终态竞争与重复结算的哨兵错误: 供调用方精确区分"重复投递"与"账目冲突"。
var (
	// errReservationFinalized 预扣已被别的调用终态化(结算或释放)的内部哨兵,
	// 只在事务内用于短路抢占失败, 不会透传给调用方。
	errReservationFinalized = errors.New("reservation already finalized")

	// ErrSettlementMismatch 对已结算预扣的再次结算携带了不同用量或费用: 拒绝入账。
	// 已定稿的账不能被第二次结算静默改写或叠加扣款, 交由告警与人工对账处理。
	ErrSettlementMismatch = errors.New("reservation already settled with different usage")

	// ErrReservationReleased 预扣已被释放(未计费)后再结算: 拒绝入账。
	// 冻结额早已退回可用余额, 此时补扣会把冻结额扣成负数、把退款当欠费再收一遍。
	ErrReservationReleased = errors.New("reservation already released")
)

// BillingReserve 预扣估算费用: 原子冻结 estimate 并登记预扣记录, 余额不足返回错误。
func BillingReserve(userID uint, requestID uint64, estimate float64) (uint64, error) {
	if math.IsNaN(estimate) || math.IsInf(estimate, 0) {
		return 0, fmt.Errorf("reservation estimate must be finite")
	}
	if estimate < 0 {
		estimate = 0
	}
	// 预扣后的可用余额不得低于 min_balance: 余额下限是拒绝阈值, 不是可花额度。
	required := estimate + SettingGetFloat(model.SettingKeyMinBalance)
	if required < 0 {
		required = 0
	}
	var reservationID uint64
	err := db.GetDB().Transaction(func(tx *gorm.DB) error {
		result := tx.Exec(
			"UPDATE users SET frozen = frozen + ? WHERE id = ? AND balance - frozen >= ?",
			estimate, userID, required,
		)
		if result.Error != nil {
			return fmt.Errorf("failed to reserve balance: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return errInsufficientBalance
		}
		expires := time.Now().Add(BillingLeaseDuration)
		reservation := model.BalanceReservation{UserID: userID, RequestID: requestID, Amount: estimate, CreatedAt: time.Now(), LeaseExpiresAt: &expires}
		if err := tx.Create(&reservation).Error; err != nil {
			return fmt.Errorf("failed to create reservation: %w", err)
		}
		reservationID = reservation.ID
		// 预扣流水: 可用余额减 estimate, 冻结额加 estimate。
		return ledgerRecord(tx, model.BillingLedger{
			UserID:       userID,
			Kind:         model.LedgerKindReserve,
			Amount:       -estimate,
			Frozen:       estimate,
			BalanceAfter: balanceOf(tx, userID),
			RequestID:    requestID,
		})
	})
	if err != nil {
		// 撞墙时刻是用户第一次"感知"到余额不足的时机: 结算侧的提前预警覆盖不到
		// "本来就不够、一次都没结算过"的账号, 所以预扣失败这里必须也报一次。
		if errors.Is(err, errInsufficientBalance) {
			maybeRaiseLowBalanceAlert(context.Background(), userID, estimate)
		}
		return 0, err
	}
	UserTouchBalance(userID)
	return reservationID, nil
}

// BillingSettle 按实际用量结算一笔预扣: 解冻预估、扣实际支出、记发布者收入并写计费明细。
// record 的价格与用量快照由调用方按本次落地渠道算好; 三方金额在此定稿并落库。
//
// 终态与幂等(一笔预扣至多结算一次): 预扣终态用条件迁移(settled=false → true)抢占,
// 抢占是事务首个语句(写锁先行), 结算与释放竞争时只有一方改到行、只有一方动钱,
// 失败事务连同状态与流水一并回滚。抢输的调用拿已落账的结算比对经济内容:
//   - 同一笔用量/费用的重复投递 → 幂等返回 nil, 不再动钱、不再记账、不再告警;
//   - 用量或费用不一致 → ErrSettlementMismatch, 拒绝入账, 不静默吞掉不同用量;
//   - 预扣是被释放的(无计费明细) → ErrReservationReleased, 钱已退回, 不能补扣。
func billingSettleApply(ctx context.Context, reservationID uint64, record *model.BillingRecord) error {
	if record == nil {
		return fmt.Errorf("billing record is required")
	}
	// 三方账目对平: 平台收入 = 用户支出 - 发布者收入。
	record.PlatformRevenue = record.UserCost - record.OwnerRevenue
	record.Status = model.BillingStatusSettled
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now()
	}

	var reserveAmount float64 // 本笔预估, 供结算完成后判断"下一单是否够付"。
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 终态抢占: 条件迁移 settled=false → true, RowsAffected 先验、后动钱。
		// 事务首个语句即写, 拿到写锁再读, 避免读-写升级与并发事务相撞。
		claim := tx.Model(&model.BalanceReservation{}).
			Where("id = ? AND settled = ?", reservationID, false).
			Update("settled", true)
		if claim.Error != nil {
			return fmt.Errorf("failed to settle reservation: %w", claim.Error)
		}
		if claim.RowsAffected != 1 {
			return errReservationFinalized
		}
		var reservation model.BalanceReservation
		if err := tx.First(&reservation, reservationID).Error; err != nil {
			return fmt.Errorf("reservation not found: %w", err)
		}
		if err := billingRecordMatches(&reservation, record); err != nil {
			return err
		}
		reserveAmount = reservation.Amount
		// 解冻预估 + 扣实际支出: 同一原子更新, 余额不足时允许扣成 min_balance 之上不再检查 ——
		// 预扣时已保证 balance - frozen >= estimate, 而实际支出按真实用量结算, 一般不超过预估。
		charge := tx.Exec(
			"UPDATE users SET frozen = frozen - ?, balance = balance - ?, total_spent = total_spent + ? WHERE id = ?",
			reservation.Amount, record.UserCost, record.UserCost, record.UserID,
		)
		if charge.Error != nil {
			return fmt.Errorf("failed to charge user: %w", charge.Error)
		}
		// RowsAffected 先验再记账: 用户行不存在必须整笔回滚, 不留"账单在、钱没动"的幽灵账。
		// 只在确有金额变动时校验: MySQL 按"值是否变化"计行数, 全零空更新会报 0 行但用户行还在。
		if (reservation.Amount != 0 || record.UserCost != 0) && charge.RowsAffected != 1 {
			return fmt.Errorf("failed to charge user: user %d not found", record.UserID)
		}
		// 发布者收入按供货价计入; 渠道归属 admin 时同样计入其累计(平台口径)。
		if record.OwnerRevenue != 0 {
			credit := tx.Exec(
				"UPDATE users SET total_revenue = total_revenue + ? WHERE id = ?",
				record.OwnerRevenue, record.OwnerID,
			)
			if credit.Error != nil {
				return fmt.Errorf("failed to credit owner: %w", credit.Error)
			}
			if credit.RowsAffected != 1 {
				return fmt.Errorf("failed to credit owner: user %d not found", record.OwnerID)
			}
		}
		if err := tx.Create(record).Error; err != nil {
			return fmt.Errorf("failed to create billing record: %w", err)
		}
		// 新账目落库后失效 max_cost 兜底聚合缓存, 让下一次判定立即看到新结算额
		// (否则 TTL 窗口内兜底还停在旧值, 判定只靠内存增量)。
		apiKeyCostAggInvalidate(record.APIKeyID)
		// 结算流水: 净变动 = 解冻的预估 - 实际费用, 冻结额回到预扣前。
		return ledgerRecord(tx, model.BillingLedger{
			UserID:       record.UserID,
			Kind:         model.LedgerKindSettle,
			Amount:       reservation.Amount - record.UserCost,
			Frozen:       -reservation.Amount,
			Cost:         record.UserCost,
			BalanceAfter: balanceOf(tx, record.UserID),
			RequestID:    record.RequestID,
			ModelName:    record.ModelName,
		})
	})
	if errors.Is(err, errReservationFinalized) {
		// 预扣已终态: 判定是同一笔结算的重复投递, 还是用量冲突/被释放。
		return billingSettleReplay(reservationID, record)
	}
	if err != nil {
		return err
	}
	UserTouchBalance(record.UserID)
	UserTouchBalance(record.OwnerID)
	// 结算扣款后按同一笔预估测算下一单是否够付: 这是能提前告知本人的唯一时机, 过了这一笔就只能靠请求报错。
	maybeRaiseLowBalanceAlert(context.Background(), record.UserID, reserveAmount)
	// 用户自定义阈值预警: 与管理员侧的 low_balance 告警互补 —— 管理员关心全局,
	// 用户关心自己设的那条线。检查失败不影响结算结果(余额已经动完了)。
	if user, uerr := UserGetByID(record.UserID); uerr == nil {
		BalanceAlertCheck(context.Background(), record.UserID, user.Balance-user.Frozen)
	}
	return nil
}

// maybeRaiseLowBalanceAlert 判断用户是否已进入"下一次同规格调用会被拒绝"的状态, 是则发 low_balance 告警。
// 真实门槛 = min_balance + estimate(BillingReserve 要求 available 不低于两者之和); 只拿 min_balance 比较恒不成立 ——
// 预扣阶段就保证了 available >= min_balance + estimate, 结算后自然 > min_balance, 判错会变成永远不报。
// 两个触发点: 结算后按本笔预估提前预警; 预扣被拒时(用户已实际撞墙)兜底补报。两处 reason 相同, 由去重合并。
// reason 刻意不含余额数字: 去重按 reason 匹配, 混入余额会导致每次结算都生成新记录、绕过去重变成骚扰推送。
func maybeRaiseLowBalanceAlert(ctx context.Context, userID uint, estimate float64) {
	threshold := SettingGetFloat(model.SettingKeyMinBalance)
	user, err := UserGetByID(userID)
	if err != nil {
		return
	}
	available := user.Balance - user.Frozen
	if available >= threshold+estimate {
		return
	}
	// reason 刻意不含余额数字: 去重按 reason 匹配, 混入余额会导致每次结算都生成新记录、
	// 绕过去重变成骚扰推送。数字进明细行, 每次外发都带最新余额。
	RaiseAlertFacts(ctx, AlertFacts{
		Subject: "用户 " + user.Username,
		Kind:    "low_balance",
		Reason:  fmt.Sprintf("user %s (id=%d) low balance: next request would be rejected", user.Username, userID),
		Fields: []alert.Field{
			{Label: "用户", Value: fmt.Sprintf("%s (#%d)", user.Username, userID)},
			{Label: "可用余额", Value: fmt.Sprintf("%.6f", available)},
			{Label: "账户余额", Value: fmt.Sprintf("%.6f", user.Balance)},
			{Label: "冻结额", Value: fmt.Sprintf("%.6f", user.Frozen)},
			{Label: "起扣门槛", Value: fmt.Sprintf("%.6f = 最低余额 %.6f + 本笔预估 %.6f", threshold+estimate, threshold, estimate)},
			{Label: "影响", Value: "该用户下一次同规格调用会被拒绝"},
			{Label: "建议", Value: "通知用户充值, 或调整最低余额与额度"},
		},
	})
	// 用户侧站内通知: 管理员告警与用户通知同源触发, 用户不进钱包页也能被铃铛提醒。
	notifyAsync(ctx, userID, model.NotificationTypeBalanceLow, "余额不足",
		fmt.Sprintf("您的可用余额 (%.2f 元) 已接近起扣门槛，下一次调用可能会被拒绝，请及时充值。", available),
		"", 0)
}

// BillingRelease 释放预扣不扣费, 用于失败或取消且没有产生用量的请求。
//
// 终态抢占与 BillingSettle 同一把钥匙(条件迁移 settled=false → true, 抢占是事务首个语句):
// 结算与释放竞争时只有一方改到行、只有一方动冻结额。已被终态化的预扣重复释放按幂等成功返回 ——
// 超时清理与请求收尾都会重试释放, 冻结额只能退一次; 释放也绝不会给已结算的预扣退款。
// 预扣记录不存在仍报错, 防调用方拿着错误的 ID 静默装成功。
func BillingRelease(reservationID uint64) error { return billingRelease(reservationID, nil) }

func billingRelease(reservationID uint64, staleCutoff *time.Time) error {
	var reservation model.BalanceReservation
	var released bool
	err := db.GetDB().Transaction(func(tx *gorm.DB) error {
		// Atomically recheck expiry under the same writer lock as intent creation.
		query := tx.Model(&model.BalanceReservation{}).Where("id = ? AND settled = ?", reservationID, false)
		if staleCutoff != nil {
			query = query.Where("(lease_expires_at IS NOT NULL AND lease_expires_at <= ?) OR (lease_expires_at IS NULL AND created_at < ?)", time.Now(), *staleCutoff)
		}
		claim := query.Update("settled", true)
		if claim.Error != nil {
			return fmt.Errorf("failed to release reservation: %w", claim.Error)
		}
		if claim.RowsAffected != 1 {
			return errReservationFinalized
		}
		if err := tx.First(&reservation, reservationID).Error; err != nil {
			return fmt.Errorf("reservation not found: %w", err)
		}
		var intents int64
		if err := tx.Model(&model.BillingSettlementTask{}).Where("reservation_id = ?", reservationID).Count(&intents).Error; err != nil {
			return err
		}
		if intents > 0 {
			return ErrSettlementIntentPending
		}
		unfreeze := tx.Exec("UPDATE users SET frozen = frozen - ? WHERE id = ?", reservation.Amount, reservation.UserID)
		if unfreeze.Error != nil {
			return fmt.Errorf("failed to unfreeze balance: %w", unfreeze.Error)
		}
		// RowsAffected 先验再记流水: 用户行不存在整笔回滚。零额解冻是空更新, 不代表行缺失。
		if reservation.Amount != 0 && unfreeze.RowsAffected != 1 {
			return fmt.Errorf("failed to unfreeze balance: user %d not found", reservation.UserID)
		}
		released = true
		// 释放流水: 冻结额全额退回可用余额。
		return ledgerRecord(tx, model.BillingLedger{
			UserID:       reservation.UserID,
			Kind:         model.LedgerKindRelease,
			Amount:       reservation.Amount,
			Frozen:       -reservation.Amount,
			BalanceAfter: balanceOf(tx, reservation.UserID),
			RequestID:    reservation.RequestID,
		})
	})
	if errors.Is(err, errReservationFinalized) {
		// 已被结算或释放: 幂等成功, 绝不二次解冻; 记录不存在则照旧报错。
		var existing model.BalanceReservation
		if err := db.GetDB().First(&existing, reservationID).Error; err != nil {
			return fmt.Errorf("reservation not found: %w", err)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if released {
		UserTouchBalance(reservation.UserID)
	}
	return nil
}

// billingRecordMatches 校验结算明细与预扣记录的归属一致: 冻结额从预扣行的用户解冻,
// 扣款落在明细行的用户, 两者必须是同一个人、同一笔请求, 否则资金会被拆进两个账户。
func billingRecordMatches(reservation *model.BalanceReservation, record *model.BillingRecord) error {
	if reservation.UserID != record.UserID {
		return fmt.Errorf("billing record user %d does not match reservation user %d", record.UserID, reservation.UserID)
	}
	if reservation.RequestID != record.RequestID {
		return fmt.Errorf("billing record request %d does not match reservation request %d", record.RequestID, reservation.RequestID)
	}
	return nil
}

// billingSettleReplay 终态抢占失败后的收场判定(事务外自动提交读, 只看已提交终态):
//   - 已落账的结算与本次经济内容一致 → 同一笔结算的重复投递, 幂等成功;
//   - 已落账但用量/费用不一致 → ErrSettlementMismatch, 不静默接受不同的用量;
//   - 没有计费明细 → 预扣是被释放的, ErrReservationReleased。
func billingSettleReplay(reservationID uint64, record *model.BillingRecord) error {
	var reservation model.BalanceReservation
	if err := db.GetDB().First(&reservation, reservationID).Error; err != nil {
		return fmt.Errorf("reservation not found: %w", err)
	}
	if err := billingRecordMatches(&reservation, record); err != nil {
		return err
	}
	settled := []model.BillingRecord{}
	if err := db.GetDB().
		Where("request_id = ? AND user_id = ?", reservation.RequestID, reservation.UserID).
		Find(&settled).Error; err != nil {
		return fmt.Errorf("failed to read settled usage: %w", err)
	}
	for i := range settled {
		if billingSameUsage(record, &settled[i]) {
			return nil
		}
	}
	if len(settled) == 0 {
		return fmt.Errorf("%w: reservation %d was released without charge", ErrReservationReleased, reservationID)
	}
	return fmt.Errorf("%w: reservation %d was already settled", ErrSettlementMismatch, reservationID)
}

// billingMoneyEpsilon 金额比较容差: 库内 decimal(18,6) 会把小数舍到 6 位,
// 重放明细与历史明细的最大差是 5e-7; 两个不同的 6 位小数至少差 1e-6, 不会被误判为相同。
const billingMoneyEpsilon = 5e-7

// billingSameUsage 判断两份结算的经济内容是否一致: 收付款三方归属与金额、token 用量、媒体用量。
// 一致才允许按幂等重放处理; 任何一项不同都算另一笔账。模型名/渠道等展示快照不参与比较。
func billingSameUsage(a, b *model.BillingRecord) bool {
	return a.UserID == b.UserID &&
		a.OwnerID == b.OwnerID &&
		billingMoneyEqual(a.UserCost, b.UserCost) &&
		billingMoneyEqual(a.OwnerRevenue, b.OwnerRevenue) &&
		billingMoneyEqual(a.PlatformRevenue, b.PlatformRevenue) &&
		a.InputToken == b.InputToken &&
		a.OutputToken == b.OutputToken &&
		a.CacheReadToken == b.CacheReadToken &&
		a.CacheWriteToken == b.CacheWriteToken &&
		a.MediaUnits.Images == b.MediaUnits.Images &&
		a.MediaUnits.Videos == b.MediaUnits.Videos &&
		billingMoneyEqual(a.MediaUnits.Seconds, b.MediaUnits.Seconds) &&
		a.MediaUnits.Resolution == b.MediaUnits.Resolution
}

// billingMoneyEqual 金额在容差内相等。
func billingMoneyEqual(a, b float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff <= billingMoneyEpsilon
}

// BillingReleaseStale 回滚超过 olderThan 仍未结算的预扣, 由定时任务调用。
func BillingReleaseStale(olderThan time.Duration) error {
	cutoff := time.Now().Add(-olderThan)
	stale := []model.BalanceReservation{}
	if err := db.GetDB().Where("settled = ? AND ((lease_expires_at IS NOT NULL AND lease_expires_at <= ?) OR (lease_expires_at IS NULL AND created_at < ?))", false, time.Now(), cutoff).Where("NOT EXISTS (SELECT 1 FROM billing_settlement_tasks WHERE billing_settlement_tasks.reservation_id = balance_reservations.id)").Order("id ASC").Limit(200).Find(&stale).Error; err != nil {
		return err
	}
	for _, reservation := range stale {
		if err := billingRelease(reservation.ID, &cutoff); err != nil && !errors.Is(err, ErrSettlementIntentPending) {
			return err
		}
	}
	return nil
}

// BillingHourly 按小时(0-23)聚合计费明细, 供非管理员的 24 小时视图; 口径同 BillingDaily。
func BillingHourly(ctx context.Context, kind string, id uint, since time.Time) ([24]BillingAggregate, error) {
	column := "user_id"
	if kind == "owner" {
		column = "owner_id"
	}
	records := []model.BillingRecord{}
	if err := db.GetDB().WithContext(ctx).
		Where(column+" = ? AND created_at >= ?", id, since).
		Find(&records).Error; err != nil {
		return [24]BillingAggregate{}, err
	}
	var buckets [24]BillingAggregate
	for _, record := range records {
		bucket := &buckets[record.CreatedAt.Hour()]
		bucket.Date = record.CreatedAt.Format("20060102")
		bucket.InputToken += record.InputToken
		bucket.OutputToken += record.OutputToken
		bucket.CacheReadToken += record.CacheReadToken
		bucket.CacheWriteToken += record.CacheWriteToken
		bucket.RequestCount++
		bucket.UserCost += record.UserCost
		bucket.OwnerRevenue += record.OwnerRevenue
		bucket.PlatformRevenue += record.PlatformRevenue
	}
	return buckets, nil
}

// BillingTotal 全期汇总; kind 口径同 BillingDaily。
//
// 聚合交给数据库做, 不再把明细整行取回 Go 侧累加。慢在"取回"而不在"累加":
// billing_records 一行 26 列, 全期汇总却只用得上 8 个 —— 而在 SQLite 上"整行取回"的真实
// 代价远高于裸 SELECT: 生产库副本实测(16197 行同一账户, 中位数 n=15)旧写法 571.79 ms,
// 其中 gorm 按整行结构体逐字段反射填充就占掉大头; 同库同条件只取 8 列的裸 SQL 是 12.34 ms。
// 换成下面这条聚合后应用层实测 17.48 ms。
//
// 方言: 只用 SUM/COUNT, 三种数据库通用(日期函数才有方言差异, 故 BillingDaily/Monthly
// 的日期桶走 billingBucketExpr 的方言分支; BillingHourly 仍保留 Go 侧分桶)。零行时 SUM
// 返回 NULL 而非 0, 必须 COALESCE 兜底, 否则扫描 NULL 到 float64 会报错。
//
// 精度: SQLite 的 SUM 对浮点做补偿求和(Kahan-Babuska-Neumaier), 累加顺序也与 Go 侧
// 逐行相加不同, 故末位可能有 1e-13 量级差异。金额展示到 6 位小数, 该量级不可见;
// UserCost = OwnerRevenue + PlatformRevenue 的账面恒等式在两侧同样成立(差值同源同量级)。
func BillingTotal(ctx context.Context, kind string, id uint) (BillingAggregate, error) {
	column := "user_id"
	if kind == "owner" {
		column = "owner_id"
	}
	total := BillingAggregate{}
	err := db.GetDB().WithContext(ctx).Model(&model.BillingRecord{}).
		Select("COALESCE(SUM(input_token), 0) AS input_token,"+
			" COALESCE(SUM(output_token), 0) AS output_token,"+
			" COALESCE(SUM(cache_read_token), 0) AS cache_read_token,"+
			" COALESCE(SUM(cache_write_token), 0) AS cache_write_token,"+
			" COUNT(*) AS request_count,"+
			" COALESCE(SUM(user_cost), 0) AS user_cost,"+
			" COALESCE(SUM(owner_revenue), 0) AS owner_revenue,"+
			" COALESCE(SUM(platform_revenue), 0) AS platform_revenue").
		Where(column+" = ?", id).
		Scan(&total).Error
	if err != nil {
		return BillingAggregate{}, err
	}
	return total, nil
}

// BillingQuery 计费明细查询条件; 零值表示该维度不限。
type BillingQuery struct {
	UserID   uint      // 付费方; 0 表示不限(仅管理员可查他人)。
	OwnerID  uint      // 发布者; 0 表示不限。
	Model    string    // 模型模糊匹配(客户端模型名或上游落地模型名)。
	Channel  string    // 渠道模糊匹配(发布编码)。
	APIKeyID int       // 指定 API Key。
	Status   string    // settled | refunded。
	PriceSrc string    // 价格口径, 如 channel_model。
	FxDay    string    // 折算采集日: YYYYMMDD 精确匹配; 特殊值 unavailable 匹配 fx_day=unavailable(采集失败按原价入账); 空表示不限。
	From     time.Time // 起始时间(含)。
	To       time.Time // 结束时间(含)。
	Limit    int
	Offset   int
}

// BillingSearch 按条件倒序分页返回计费明细与命中总数; 计数与分页共用同一套条件保证一致。
func BillingSearch(ctx context.Context, filter BillingQuery) ([]model.BillingRecord, int64, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 20
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	build := func() *gorm.DB {
		query := db.GetDB().WithContext(ctx).Model(&model.BillingRecord{})
		if filter.UserID != 0 {
			query = query.Where("user_id = ?", filter.UserID)
		}
		if filter.OwnerID != 0 {
			query = query.Where("owner_id = ?", filter.OwnerID)
		}
		if filter.Model != "" {
			like := "%" + strings.TrimSpace(filter.Model) + "%"
			query = query.Where("group_model LIKE ? OR model_name LIKE ?", like, like)
		}
		if filter.Channel != "" {
			// 编码展示带 # 前缀(#1001), 检索时剥掉前缀按裸编码匹配。
			code := strings.TrimPrefix(strings.TrimSpace(filter.Channel), "#")
			query = query.Where("share_code LIKE ?", "%"+code+"%")
		}
		if filter.APIKeyID != 0 {
			query = query.Where("api_key_id = ?", filter.APIKeyID)
		}
		if filter.Status != "" {
			query = query.Where("status = ?", filter.Status)
		}
		if filter.PriceSrc != "" {
			query = query.Where("price_source = ?", filter.PriceSrc)
		}
		if fxDay := strings.TrimSpace(filter.FxDay); fxDay != "" {
			// 对账下钻: 特殊值 unavailable(大小写不敏感)筛采集缺失账单, 其余按采集日精确匹配。
			if strings.EqualFold(fxDay, "unavailable") {
				query = query.Where("fx_day = ?", "unavailable")
			} else if strings.EqualFold(fxDay, "none") {
				query = query.Where("COALESCE(fx_day, '') = ''")
			} else {
				query = query.Where("fx_day = ?", strings.ToUpper(fxDay))
			}
		}
		if !filter.From.IsZero() {
			query = query.Where("created_at >= ?", filter.From)
		}
		if !filter.To.IsZero() {
			query = query.Where("created_at <= ?", filter.To)
		}
		return query
	}
	var total int64
	if err := build().Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count billing records: %w", err)
	}
	records := []model.BillingRecord{}
	if err := build().Order("id DESC").Limit(limit).Offset(offset).Find(&records).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to list billing records: %w", err)
	}
	return records, total, nil
}

// BillingList 返回计费明细; userID 为 0 表示不限付费方, ownerID 为 0 表示不限发布者。
func BillingList(ctx context.Context, userID, ownerID uint, limit int) ([]model.BillingRecord, error) {
	records, _, err := BillingSearch(ctx, BillingQuery{UserID: userID, OwnerID: ownerID, Limit: limit})
	return records, err
}

// BillingAggregate 按 created_at 归桶的聚合行(日粒度见 BillingDaily, 月粒度见 BillingMonthly);
// 供用户消费曲线与渠道商收入曲线使用。Date 为本地挂钟日期 YYYYMMDD, 月桶取当月 1 日。
type BillingAggregate struct {
	Date            string  `json:"date"`
	InputToken      int64   `json:"input_token"`
	OutputToken     int64   `json:"output_token"`
	CacheReadToken  int64   `json:"cache_read_token"`
	CacheWriteToken int64   `json:"cache_write_token"`
	RequestCount    int64   `json:"request_count"`
	UserCost        float64 `json:"user_cost"`
	OwnerRevenue    float64 `json:"owner_revenue"`
	PlatformRevenue float64 `json:"platform_revenue"`
}

// billingBucketExpr 逐方言构造把 created_at 归到**本地挂钟**日/月桶的 SQL 表达式, 结果统一为
// YYYYMMDD(日桶即当天, 月桶取当月 1 日)。
//
// 为什么不能图省事直接用 DATE(created_at): SQLite 的 date 系列函数会先把带偏移的时间串归一到
// UTC 再做日历运算, 2026-11-08 00:30+08:00 会被算成 11-07 —— 跨日错位。聚合的契约是旧实现的
// record.CreatedAt.Format("20060102"), 即"按写入偏移看挂钟日期", 所以必须显式做本地化, 绝不能用
// UTC 简化。这里复用 statsBucketExprOn 的方言桶口径(它已按各库的存储形态做本地化: SQLite 取
// 写入串的日期前缀、不做时区换算, MySQL 按 DATETIME 本地挂钟, PG 按会话 TimeZone), 再去掉分隔符
// 以沿用 BillingAggregate.Date 的 YYYYMMDD 契约。与趋势图共用同一套桶口径, 两张图才不会按不同
// 日期切片。
func billingBucketExpr(granularity string) string {
	return "REPLACE(" + statsBucketExprOn(statsDialect(), granularity, "created_at") + ", '-', '')"
}

// billingAggregateByBucket 数据库侧按日期桶聚合; dateExpr 决定日/月粒度, 聚合字段与旧 Go 侧
// 逐行累加严格同集(缓存读写两列不能漏, 消费页按四项求和)。
//
// 整行取回改成 GROUP BY: billing_records 一行 26 列, 曲线却只用得上 8 个数字, 旧写法把时间窗内
// 全部明细搬进内存, 占用随历史线性增长(生产 23828 行已到必须换的体量)。
//
// 方言: SUM/COUNT 三库通用; 日期桶由 billingBucketExpr 方言化。零行时 SUM 返回 NULL, 必须
// COALESCE 兜底, 否则扫描 NULL 到 float64 报错。空结果返回非 nil 的空切片, 调用方按 len 判断。
// 精度: SQLite/MySQL 的浮点 SUM 与 Go 逐行相加的顺序不同(生产库副本实测末位差 ≤1.07e-14),
// 金额展示 6 位小数不可见; 整数字段与行数逐桶完全相等。
func billingAggregateByBucket(ctx context.Context, kind string, id uint, since time.Time, granularity string) ([]BillingAggregate, error) {
	column := "user_id"
	if kind == "owner" {
		column = "owner_id"
	}
	dateExpr := billingBucketExpr(granularity)
	rows := make([]BillingAggregate, 0)
	if err := db.GetDB().WithContext(ctx).Model(&model.BillingRecord{}).
		Select(dateExpr+" AS date,"+
			" COALESCE(SUM(input_token), 0) AS input_token,"+
			" COALESCE(SUM(output_token), 0) AS output_token,"+
			" COALESCE(SUM(cache_read_token), 0) AS cache_read_token,"+
			" COALESCE(SUM(cache_write_token), 0) AS cache_write_token,"+
			" COUNT(*) AS request_count,"+
			" COALESCE(SUM(user_cost), 0) AS user_cost,"+
			" COALESCE(SUM(owner_revenue), 0) AS owner_revenue,"+
			" COALESCE(SUM(platform_revenue), 0) AS platform_revenue").
		Where(column+" = ? AND created_at >= ?", id, since).
		Group(dateExpr).
		Order("date ASC").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// BillingDaily 按日聚合计费明细; kind 取 "user" 或 "owner", id 为对应用户主键。
// 输出按日期升序, Date 为 YYYYMMDD 的本地挂钟日期。
func BillingDaily(ctx context.Context, kind string, id uint, since time.Time) ([]BillingAggregate, error) {
	return billingAggregateByBucket(ctx, kind, id, since, "day")
}

// BillingMonthly 按"月"聚合计费明细, 口径与 BillingDaily 完全一致: 同一时间窗、同一组聚合字段、
// 同一时区归一规则, 只是桶换成当月 1 日(YYYYMMDD, 如 20261101)。日期升序, 空结果为非 nil 空切片。
// 月的边界同样按本地挂钟判定, 跨月的那笔请求不会因为 UTC 归一滑到上个月。
func BillingMonthly(ctx context.Context, kind string, id uint, since time.Time) ([]BillingAggregate, error) {
	return billingAggregateByBucket(ctx, kind, id, since, "month")
}

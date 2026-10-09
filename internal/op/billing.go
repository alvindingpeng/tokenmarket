package op

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
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

// BillingReserve 预扣估算费用: 原子冻结 estimate 并登记预扣记录, 余额不足返回错误。
func BillingReserve(userID uint, requestID uint64, estimate float64) (uint64, error) {
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
		reservation := model.BalanceReservation{UserID: userID, RequestID: requestID, Amount: estimate, CreatedAt: time.Now()}
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
func BillingSettle(reservationID uint64, record *model.BillingRecord) error {
	if record == nil {
		return fmt.Errorf("billing record is required")
	}
	// 三方账目对平: 平台收入 = 用户支出 - 发布者收入。
	record.PlatformRevenue = record.UserCost - record.OwnerRevenue
	record.Status = model.BillingStatusSettled
	record.CreatedAt = time.Now()

	var reserveAmount float64 // 本笔预估, 供结算完成后判断"下一单是否够付"。
	err := db.GetDB().Transaction(func(tx *gorm.DB) error {
		var reservation model.BalanceReservation
		if err := tx.First(&reservation, reservationID).Error; err != nil {
			return fmt.Errorf("reservation not found: %w", err)
		}
		if reservation.Settled {
			return fmt.Errorf("reservation already settled")
		}
		if err := tx.Model(&model.BalanceReservation{}).Where("id = ?", reservationID).
			Update("settled", true).Error; err != nil {
			return fmt.Errorf("failed to settle reservation: %w", err)
		}
		reserveAmount = reservation.Amount
		// 解冻预估 + 扣实际支出: 同一原子更新, 余额不足时允许扣成 min_balance 之上不再检查 ——
		// 预扣时已保证 balance - frozen >= estimate, 而实际支出按真实用量结算, 一般不超过预估。
		if err := tx.Exec(
			"UPDATE users SET frozen = frozen - ?, balance = balance - ?, total_spent = total_spent + ? WHERE id = ?",
			reservation.Amount, record.UserCost, record.UserCost, record.UserID,
		).Error; err != nil {
			return fmt.Errorf("failed to charge user: %w", err)
		}
		// 发布者收入按供货价计入; 渠道归属 admin 时同样计入其累计(平台口径)。
		if record.OwnerRevenue != 0 {
			if err := tx.Exec(
				"UPDATE users SET total_revenue = total_revenue + ? WHERE id = ?",
				record.OwnerRevenue, record.OwnerID,
			).Error; err != nil {
				return fmt.Errorf("failed to credit owner: %w", err)
			}
		}
		if err := tx.Create(record).Error; err != nil {
			return fmt.Errorf("failed to create billing record: %w", err)
		}
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
	if err != nil {
		return err
	}
	UserTouchBalance(record.UserID)
	UserTouchBalance(record.OwnerID)
	// 结算扣款后按同一笔预估测算下一单是否够付: 这是能提前告知本人的唯一时机, 过了这一笔就只能靠请求报错。
	maybeRaiseLowBalanceAlert(context.Background(), record.UserID, reserveAmount)
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
	RaiseAlert(ctx, 0, "system", "low_balance",
		fmt.Sprintf("user %s (id=%d) low balance: available below min_balance %.6f + estimate %.6f, next request would be rejected",
			user.Username, userID, threshold, estimate))
}

// BillingRelease 释放预扣不扣费, 用于失败或取消且没有产生用量的请求。
func BillingRelease(reservationID uint64) error {
	var reservation model.BalanceReservation
	if err := db.GetDB().First(&reservation, reservationID).Error; err != nil {
		return fmt.Errorf("reservation not found: %w", err)
	}
	if reservation.Settled {
		return nil
	}
	err := db.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.BalanceReservation{}).Where("id = ?", reservationID).
			Update("settled", true).Error; err != nil {
			return err
		}
		if err := tx.Exec("UPDATE users SET frozen = frozen - ? WHERE id = ?", reservation.Amount, reservation.UserID).Error; err != nil {
			return err
		}
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
	if err != nil {
		return err
	}
	UserTouchBalance(reservation.UserID)
	return nil
}

// BillingReleaseStale 回滚超过 olderThan 仍未结算的预扣, 由定时任务调用。
func BillingReleaseStale(olderThan time.Duration) error {
	cutoff := time.Now().Add(-olderThan)
	stale := []model.BalanceReservation{}
	if err := db.GetDB().Where("settled = ? AND created_at < ?", false, cutoff).Find(&stale).Error; err != nil {
		return err
	}
	for _, reservation := range stale {
		if err := BillingRelease(reservation.ID); err != nil {
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
func BillingTotal(ctx context.Context, kind string, id uint) (BillingAggregate, error) {
	column := "user_id"
	if kind == "owner" {
		column = "owner_id"
	}
	records := []model.BillingRecord{}
	if err := db.GetDB().WithContext(ctx).Where(column+" = ?", id).Find(&records).Error; err != nil {
		return BillingAggregate{}, err
	}
	total := BillingAggregate{}
	for _, record := range records {
		total.InputToken += record.InputToken
		total.OutputToken += record.OutputToken
		total.CacheReadToken += record.CacheReadToken
		total.CacheWriteToken += record.CacheWriteToken
		total.RequestCount++
		total.UserCost += record.UserCost
		total.OwnerRevenue += record.OwnerRevenue
		total.PlatformRevenue += record.PlatformRevenue
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
			query = query.Where("share_code LIKE ?", "%"+strings.TrimSpace(filter.Channel)+"%")
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

// BillingAggregate 日粒度聚合, 按 created_at 归日; 供用户消费曲线与渠道商收入曲线使用。
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

// BillingDaily 按日聚合计费明细; kind 取 "user" 或 "owner", id 为对应用户主键。
// 聚合在 Go 侧完成: 三种数据库的日期函数方言不同, 而按天分组的数据量有限。
func BillingDaily(ctx context.Context, kind string, id uint, since time.Time) ([]BillingAggregate, error) {
	column := "user_id"
	if kind == "owner" {
		column = "owner_id"
	}
	records := []model.BillingRecord{}
	if err := db.GetDB().WithContext(ctx).
		Where(column+" = ? AND created_at >= ?", id, since).
		Order("created_at").
		Find(&records).Error; err != nil {
		return nil, err
	}
	byDate := map[string]*BillingAggregate{}
	order := make([]string, 0)
	for _, record := range records {
		date := record.CreatedAt.Format("20060102")
		row, ok := byDate[date]
		if !ok {
			row = &BillingAggregate{Date: date}
			byDate[date] = row
			order = append(order, date)
		}
		row.InputToken += record.InputToken
		row.OutputToken += record.OutputToken
		row.RequestCount++
		row.UserCost += record.UserCost
		row.OwnerRevenue += record.OwnerRevenue
		row.PlatformRevenue += record.PlatformRevenue
	}
	rows := make([]BillingAggregate, 0, len(order))
	for _, date := range order {
		rows = append(rows, *byDate[date])
	}
	return rows, nil
}

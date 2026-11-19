package op

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
)

// 租户钱包资金语义(与用户钱包一一对应, 但资金池在租户层):
//
//   - 充值/调账只动 Balance 与 TotalRecharged, 冻结额不受影响;
//   - 请求预扣冻结 Frozen(不动 Balance): 租户配额与用户配额是两级闸门,
//     用户扣款仍然照旧走 users 表, 租户层只负责"这个客户的钱够不够";
//   - 结算扣除实际支出并把冻结额退回, 同时累计 TotalSpent;
//   - 释放把冻结额原样退回, 不产生任何支出。
//
// 三处必须与 users 侧结算在同一次事务里提交: 否则会出现"用户扣了钱、租户没扣"
// 或"租户扣了钱、账单没落库"的账目缺口, 事后无法用流水解释差额。

// errTenantInsufficientBalance 租户余额不足的哨兵错误, 与用户侧 errInsufficientBalance 对称。
var errTenantInsufficientBalance = errors.New("insufficient tenant balance")

// ErrTenantWalletNotFound 租户钱包不存在(租户被删或尚未初始化)。
var ErrTenantWalletNotFound = errors.New("tenant wallet not found")

// tenantWalletOf 读取事务内的租户钱包; 不存在时按未初始化处理。
func tenantWalletOf(tx *gorm.DB, tenantID uint) (model.TenantWallet, error) {
	var wallet model.TenantWallet
	err := tx.Where("tenant_id = ?", tenantID).First(&wallet).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.TenantWallet{}, ErrTenantWalletNotFound
	}
	if err != nil {
		return model.TenantWallet{}, fmt.Errorf("failed to load tenant wallet: %w", err)
	}
	return wallet, nil
}

// TenantWalletEnsure 确保租户钱包存在; 缺失时按零额初始化。
// 幂等: 已存在则原样返回, 不重置任何余额。
func TenantWalletEnsure(tenantID uint) (model.TenantWallet, error) {
	if tenantID == 0 {
		return model.TenantWallet{}, fmt.Errorf("tenant id is required")
	}
	if wallet, err := tenantWalletOf(db.GetDB(), tenantID); err == nil {
		return wallet, nil
	} else if !errors.Is(err, ErrTenantWalletNotFound) {
		return model.TenantWallet{}, err
	}
	wallet := model.TenantWallet{TenantID: tenantID}
	if err := db.GetDB().Create(&wallet).Error; err != nil {
		// 并发初始化可能同时插入: 唯一主键冲突时回读已存在的那条。
		if existing, readErr := tenantWalletOf(db.GetDB(), tenantID); readErr == nil {
			return existing, nil
		}
		return model.TenantWallet{}, fmt.Errorf("failed to create tenant wallet: %w", err)
	}
	return wallet, nil
}

// TenantBillingEnforced 返回当前是否按租户余额拦截请求(off 模式下只记账不拦截)。
// 读不到设置按 off 处理: 拦截是"会挡人"的动作, 配置缺失时必须往放行方向兜底,
// 否则一次配置读失败就能让整站停摆。
func TenantBillingEnforced() bool {
	mode, err := SettingGetString(model.SettingKeyTenantBillingMode)
	if err != nil {
		return false
	}
	return mode == model.TenantBillingModeEnforce
}

// tenantWalletReserve 在调用方事务内冻结租户预估额度。
// 与用户侧预扣同语义: 可用余额(不含已冻结)必须覆盖 estimate + minBalance。
//
// off 模式(默认)下只记账不拦截: 存量部署的默认租户余额为 0, 拦截等于让所有人停用。
// 账目仍然完整记录冻结额, 后续切换到 enforce 时不会出现历史缺口。
func tenantWalletReserve(tx *gorm.DB, tenantID uint, estimate float64) error {
	if tenantID == 0 || estimate <= 0 {
		return nil
	}
	enforced := TenantBillingEnforced()
	if math.IsNaN(estimate) || math.IsInf(estimate, 0) {
		return fmt.Errorf("tenant reservation estimate must be finite")
	}
	required := estimate + SettingGetFloat(model.SettingKeyMinBalance)
	if required < 0 {
		required = 0
	}
	if !enforced {
		// 只记账: 冻结额照记, 但不做余额判定。钱包不存在时也不报错 ——
		// 老库/最小化 fixture 可能没有这张表, 记账失败不该拖垮转发。
		_ = tx.Exec("UPDATE tenant_wallets SET frozen = frozen + ? WHERE tenant_id = ?", estimate, tenantID).Error
		return nil
	}
	result := tx.Exec(
		"UPDATE tenant_wallets SET frozen = frozen + ? WHERE tenant_id = ? AND balance - frozen >= ?",
		estimate, tenantID, required,
	)
	if result.Error != nil {
		return fmt.Errorf("failed to reserve tenant balance: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		// 区分"钱包不存在"与"钱不够": 前者是配置问题, 后者是业务拒绝。
		if _, err := tenantWalletOf(tx, tenantID); errors.Is(err, ErrTenantWalletNotFound) {
			return ErrTenantWalletNotFound
		}
		return errTenantInsufficientBalance
	}
	return nil
}

// tenantWalletSettle 在调用方事务内结算租户额度: 解冻预估、扣实际支出、累计支出。
// 支出可能小于预估(典型情况: 输出比上限短), 差额自动退回可用余额。
func tenantWalletSettle(tx *gorm.DB, tenantID uint, reserveAmount, actualCost float64) error {
	if tenantID == 0 {
		return nil
	}
	result := tx.Exec(
		"UPDATE tenant_wallets SET frozen = frozen - ?, balance = balance - ?, total_spent = total_spent + ? WHERE tenant_id = ?",
		reserveAmount, actualCost, actualCost, tenantID,
	)
	if result.Error != nil {
		return fmt.Errorf("failed to settle tenant balance: %w", result.Error)
	}
	if (reserveAmount != 0 || actualCost != 0) && result.RowsAffected != 1 {
		return ErrTenantWalletNotFound
	}
	return nil
}

// tenantWalletRelease 在调用方事务内退回租户冻结额(未计费路径)。
func tenantWalletRelease(tx *gorm.DB, tenantID uint, reserveAmount float64) error {
	if tenantID == 0 || reserveAmount == 0 {
		return nil
	}
	result := tx.Exec(
		"UPDATE tenant_wallets SET frozen = frozen - ? WHERE tenant_id = ?",
		reserveAmount, tenantID,
	)
	if result.Error != nil {
		return fmt.Errorf("failed to release tenant balance: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrTenantWalletNotFound
	}
	return nil
}

// TenantWalletRecharge 给租户充值: 增加可用余额与累计充值, 并写一条租户流水。
// actorID/actorName 记录操作者, 供审计与对账追溯(平台管理员手工充值/线下打款)。
func TenantWalletRecharge(ctx context.Context, tenantID uint, amount float64, actorID uint, actorName, note string) (model.TenantWallet, error) {
	if tenantID == 0 {
		return model.TenantWallet{}, fmt.Errorf("tenant id is required")
	}
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount <= 0 {
		return model.TenantWallet{}, fmt.Errorf("recharge amount must be a positive finite number")
	}
	if _, err := TenantWalletEnsure(tenantID); err != nil {
		return model.TenantWallet{}, err
	}
	var wallet model.TenantWallet
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Exec(
			"UPDATE tenant_wallets SET balance = balance + ?, total_recharged = total_recharged + ? WHERE tenant_id = ?",
			amount, amount, tenantID,
		)
		if result.Error != nil {
			return fmt.Errorf("failed to recharge tenant wallet: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return ErrTenantWalletNotFound
		}
		if err := tenantLedgerRecord(tx, model.TenantLedger{
			TenantID:  tenantID,
			Kind:      model.TenantLedgerKindRecharge,
			Amount:    amount,
			ActorID:   actorID,
			ActorName: actorName,
			Note:      note,
		}); err != nil {
			return err
		}
		var readErr error
		wallet, readErr = tenantWalletOf(tx, tenantID)
		return readErr
	})
	if err != nil {
		return model.TenantWallet{}, err
	}
	return wallet, nil
}

// TenantWalletAdjust 调整租户余额(可正可负), 用于线下打款、退款冲正与纠错。
// 与充值分开: 充值计入累计充值口径, 调账不计入, 否则统计会把补偿款当成营业收入。
func TenantWalletAdjust(ctx context.Context, tenantID uint, delta float64, actorID uint, actorName, note string) (model.TenantWallet, error) {
	if tenantID == 0 {
		return model.TenantWallet{}, fmt.Errorf("tenant id is required")
	}
	if math.IsNaN(delta) || math.IsInf(delta, 0) || delta == 0 {
		return model.TenantWallet{}, fmt.Errorf("adjust amount must be a non-zero finite number")
	}
	if _, err := TenantWalletEnsure(tenantID); err != nil {
		return model.TenantWallet{}, err
	}
	var wallet model.TenantWallet
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 扣减不允许把可用余额打成负数: 负余额会让后续预扣的"余额足够"判断永远失败,
		// 客户既充不进也花不出, 只能靠人工修库 —— 这里直接拒绝并让人用正确的金额。
		result := tx.Exec(
			"UPDATE tenant_wallets SET balance = balance + ? WHERE tenant_id = ? AND balance + ? >= 0",
			delta, tenantID, delta,
		)
		if result.Error != nil {
			return fmt.Errorf("failed to adjust tenant wallet: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			if _, err := tenantWalletOf(tx, tenantID); errors.Is(err, ErrTenantWalletNotFound) {
				return ErrTenantWalletNotFound
			}
			return fmt.Errorf("adjustment would make tenant balance negative")
		}
		if err := tenantLedgerRecord(tx, model.TenantLedger{
			TenantID:  tenantID,
			Kind:      model.TenantLedgerKindAdjust,
			Amount:    delta,
			ActorID:   actorID,
			ActorName: actorName,
			Note:      note,
		}); err != nil {
			return err
		}
		var readErr error
		wallet, readErr = tenantWalletOf(tx, tenantID)
		return readErr
	})
	if err != nil {
		return model.TenantWallet{}, err
	}
	return wallet, nil
}

// tenantLedgerRecord 在调用方事务内写一条租户流水。
// 与用户侧 balance_ledgers 同理: 流水与余额同生同灭, 否则对账无法解释差额。
func tenantLedgerRecord(tx *gorm.DB, entry model.TenantLedger) error {
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}
	if err := tx.Create(&entry).Error; err != nil {
		return fmt.Errorf("failed to write tenant ledger: %w", err)
	}
	return nil
}

// TenantLedgerQuery 租户流水查询条件; 零值表示不限。
type TenantLedgerQuery struct {
	TenantID uint
	Kind     string
	Limit    int
	Offset   int
}

// TenantLedgerList 倒序分页返回租户流水与命中总数。
func TenantLedgerList(ctx context.Context, query TenantLedgerQuery) ([]model.TenantLedger, int64, error) {
	if query.TenantID == 0 {
		return nil, 0, fmt.Errorf("tenant id is required")
	}
	limit := query.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset := query.Offset
	if offset < 0 {
		offset = 0
	}
	where := "tenant_id = ?"
	args := []any{query.TenantID}
	if query.Kind != "" {
		where += " AND kind = ?"
		args = append(args, query.Kind)
	}
	rows := make([]model.TenantLedger, 0, limit)
	base := db.GetDB().WithContext(ctx).Model(&model.TenantLedger{}).Where(where, args...)
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count tenant ledger: %w", err)
	}
	if err := base.Order("id DESC").Limit(limit).Offset(offset).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list tenant ledger: %w", err)
	}
	return rows, total, nil
}

// TenantWalletAvailable 返回租户可用余额(扣除冻结额); 租户钱包不存在时返回 0。
func TenantWalletAvailable(tenantID uint) float64 {
	wallet, err := tenantWalletOf(db.GetDB(), tenantID)
	if err != nil {
		return 0
	}
	return wallet.Balance - wallet.Frozen
}

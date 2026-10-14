package op

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
)

// ErrRedeemLocked 兑换尝试次数过多的哨兵错误; 调用方据此区别对待, 不计入失败次数。
var ErrRedeemLocked = errors.New("too many failed attempts, try again later")

// errRedeemInvalid 码不存在的哨兵错误: 只有猜码(探不存在的码)计入防爆破;
// 已兑/已禁用/已过期是已知码的状态变化, 不代表猜码, 不计入。
var errRedeemInvalid = errors.New("invalid redemption code")

// 兑换码防爆破参数: 同一用户或同一来源 IP 连续错码达上限即锁定一段时间。
const (
	redeemMaxFails   = 5
	redeemLockWindow = 10 * time.Minute
)

// redeemGuard 内存防爆破计数; 单实例部署够用, 多实例需换共享存储。
var redeemGuard = struct {
	sync.Mutex
	fails map[string]int
	locks map[string]time.Time
}{fails: map[string]int{}, locks: map[string]time.Time{}}

// redeemGuardCheck 锁定期内返回 ErrRedeemLocked, 锁过期自动解除。
func redeemGuardCheck(keys ...string) error {
	redeemGuard.Lock()
	defer redeemGuard.Unlock()
	now := time.Now()
	for _, key := range keys {
		if until, ok := redeemGuard.locks[key]; ok {
			if now.Before(until) {
				return ErrRedeemLocked
			}
			delete(redeemGuard.locks, key)
			delete(redeemGuard.fails, key)
		}
	}
	return nil
}

// redeemGuardFail 记一次失败, 达到上限即锁定。
func redeemGuardFail(keys ...string) {
	redeemGuard.Lock()
	defer redeemGuard.Unlock()
	now := time.Now()
	for _, key := range keys {
		if until, ok := redeemGuard.locks[key]; ok && now.Before(until) {
			continue
		}
		redeemGuard.fails[key]++
		if redeemGuard.fails[key] >= redeemMaxFails {
			redeemGuard.locks[key] = now.Add(redeemLockWindow)
			delete(redeemGuard.fails, key)
		}
	}
}

// redeemGuardReset 兑换成功后清零计数。
func redeemGuardReset(keys ...string) {
	redeemGuard.Lock()
	defer redeemGuard.Unlock()
	for _, key := range keys {
		delete(redeemGuard.fails, key)
	}
}

// codeAlphabet 兑换码字母表: 去掉易混淆的 0/O、1/I/L, 用户手抄不出错。
const codeAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

// randomCode 生成 length 位随机码字符。
func randomCode(length int) (string, error) {
	var sb strings.Builder
	sb.Grow(length)
	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(codeAlphabet))))
		if err != nil {
			return "", fmt.Errorf("failed to generate code: %w", err)
		}
		sb.WriteByte(codeAlphabet[n.Int64()])
	}
	return sb.String(), nil
}

// RedemptionBatchSpec 兑换码批次生成参数。
type RedemptionBatchSpec struct {
	Name         string     `json:"name"`
	Credits      float64    `json:"credits"`
	Count        int        `json:"count"`
	Prefix       string     `json:"prefix"`
	PerUserLimit int        `json:"per_user_limit"`
	ExpiresAt    *time.Time `json:"expires_at"`
	CreatedBy    uint       `json:"-"`
}

// RedemptionBatchCreate 生成批次与兑换码; 码唯一性靠唯一索引, 撞码换字符重试。
func RedemptionBatchCreate(ctx context.Context, spec RedemptionBatchSpec) (*model.RedemptionBatch, []model.RedemptionCode, error) {
	spec.Name = strings.TrimSpace(spec.Name)
	spec.Prefix = strings.ToUpper(strings.TrimSpace(spec.Prefix))
	if spec.Name == "" {
		return nil, nil, fmt.Errorf("batch name is required")
	}
	if spec.Credits <= 0 {
		return nil, nil, fmt.Errorf("credits must be positive")
	}
	if spec.Count <= 0 || spec.Count > 5000 {
		return nil, nil, fmt.Errorf("count must be between 1 and 5000")
	}
	if len(spec.Prefix) > 8 {
		return nil, nil, fmt.Errorf("prefix must be at most 8 characters")
	}
	if spec.PerUserLimit < 0 {
		return nil, nil, fmt.Errorf("per user limit cannot be negative")
	}
	if spec.ExpiresAt != nil && !spec.ExpiresAt.After(time.Now()) {
		return nil, nil, fmt.Errorf("expiry must be in the future")
	}

	batch := model.RedemptionBatch{
		Name:         spec.Name,
		Credits:      spec.Credits,
		Count:        spec.Count,
		Prefix:       spec.Prefix,
		PerUserLimit: spec.PerUserLimit,
		ExpiresAt:    spec.ExpiresAt,
		CreatedBy:    spec.CreatedBy,
	}
	codes := make([]model.RedemptionCode, 0, spec.Count)
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&batch).Error; err != nil {
			return fmt.Errorf("failed to create batch: %w", err)
		}
		seen := map[string]bool{}
		for i := 0; i < spec.Count; i++ {
			var value string
			for attempt := 0; attempt < 8; attempt++ {
				raw, err := randomCode(20)
				if err != nil {
					return err
				}
				candidate := spec.Prefix + raw
				if seen[candidate] {
					continue
				}
				var count int64
				if err := tx.Model(&model.RedemptionCode{}).Where("code = ?", candidate).Count(&count).Error; err != nil {
					return fmt.Errorf("failed to check code: %w", err)
				}
				if count > 0 {
					continue
				}
				value = candidate
				break
			}
			if value == "" {
				return fmt.Errorf("failed to allocate unique code")
			}
			seen[value] = true
			entry := model.RedemptionCode{BatchID: batch.ID, Code: value, Credits: spec.Credits, Status: model.RedemptionStatusEnabled}
			if err := tx.Create(&entry).Error; err != nil {
				return fmt.Errorf("failed to create code: %w", err)
			}
			codes = append(codes, entry)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return &batch, codes, nil
}

// RedemptionRedeem 兑换: 状态条件更新抢占兑换码, 同事务入账, 并发重复兑换至多一次成功。
func RedemptionRedeem(ctx context.Context, userID uint, code, clientIP string) (model.RedemptionCode, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return model.RedemptionCode{}, fmt.Errorf("%w: code is required", errRedeemInvalid)
	}
	userKey := "user:" + strconv.FormatUint(uint64(userID), 10)
	ipKey := "ip:" + clientIP
	if err := redeemGuardCheck(userKey, ipKey); err != nil {
		return model.RedemptionCode{}, err
	}

	var claimed model.RedemptionCode
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var found model.RedemptionCode
		if err := tx.Where("code = ?", code).Take(&found).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: code not found", errRedeemInvalid)
			}
			return fmt.Errorf("failed to look up code: %w", err)
		}
		if found.Status != model.RedemptionStatusEnabled {
			// 已兑/已禁用是已知码的状态, 不是猜码迹象, 不计入防爆破。
			return fmt.Errorf("code is already used or disabled")
		}
		var batch model.RedemptionBatch
		if err := tx.Where("id = ?", found.BatchID).Take(&batch).Error; err != nil {
			return fmt.Errorf("failed to look up batch: %w", err)
		}
		if batch.ExpiresAt != nil && time.Now().After(*batch.ExpiresAt) {
			return fmt.Errorf("code has expired")
		}
		if batch.PerUserLimit > 0 {
			var used int64
			if err := tx.Model(&model.RedemptionCode{}).
				Where("batch_id = ? AND used_by = ? AND status = ?", batch.ID, userID, model.RedemptionStatusUsed).
				Count(&used).Error; err != nil {
				return fmt.Errorf("failed to count redemptions: %w", err)
			}
			if used >= int64(batch.PerUserLimit) {
				return fmt.Errorf("per user limit reached for this batch")
			}
		}
		// 抢占: 状态条件更新, 并发场景至多一个事务改到行。
		result := tx.Model(&model.RedemptionCode{}).
			Where("id = ? AND status = ?", found.ID, model.RedemptionStatusEnabled).
			Updates(map[string]any{"status": model.RedemptionStatusUsed, "used_by": userID, "used_at": time.Now()})
		if result.Error != nil {
			return fmt.Errorf("failed to claim code: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("code is already used or disabled")
		}
		if err := walletCreditTx(tx, userID, found.Credits, model.LedgerKindRedeem, "兑换码 "+code, 0, ""); err != nil {
			return err
		}
		now := time.Now()
		claimed = found
		claimed.Status = model.RedemptionStatusUsed
		claimed.UsedBy = &userID
		claimed.UsedAt = &now
		return nil
	})
	if err != nil {
		if errors.Is(err, errRedeemInvalid) {
			redeemGuardFail(userKey, ipKey)
		}
		return model.RedemptionCode{}, err
	}
	redeemGuardReset(userKey, ipKey)
	UserTouchBalance(userID)
	return claimed, nil
}

// RedemptionHistory 用户自己的兑换记录, 倒序。
func RedemptionHistory(ctx context.Context, userID uint, limit int) ([]model.RedemptionCode, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	entries := []model.RedemptionCode{}
	if err := db.GetDB().WithContext(ctx).
		Where("used_by = ?", userID).
		Order("id DESC").Limit(limit).Find(&entries).Error; err != nil {
		return nil, fmt.Errorf("failed to list redemptions: %w", err)
	}
	return entries, nil
}

// RedemptionBatchView 批次列表行: 批次信息加各状态张数, 管理页一屏看清发放进度。
type RedemptionBatchView struct {
	model.RedemptionBatch
	Enabled  int64 `json:"enabled"`
	Used     int64 `json:"used"`
	Disabled int64 `json:"disabled"`
}

// RedemptionBatchList 批次列表(倒序)与各状态张数。
func RedemptionBatchList(ctx context.Context) ([]RedemptionBatchView, error) {
	batches := []model.RedemptionBatch{}
	if err := db.GetDB().WithContext(ctx).Order("id DESC").Find(&batches).Error; err != nil {
		return nil, fmt.Errorf("failed to list batches: %w", err)
	}
	type row struct {
		BatchID uint
		Status  string
		Total   int64
	}
	rows := []row{}
	if err := db.GetDB().WithContext(ctx).Model(&model.RedemptionCode{}).
		Select("batch_id, status, count(*) as total").
		Group("batch_id, status").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to count codes: %w", err)
	}
	counts := map[uint]map[string]int64{}
	for _, item := range rows {
		if counts[item.BatchID] == nil {
			counts[item.BatchID] = map[string]int64{}
		}
		counts[item.BatchID][item.Status] = item.Total
	}
	views := make([]RedemptionBatchView, 0, len(batches))
	for _, batch := range batches {
		view := RedemptionBatchView{RedemptionBatch: batch}
		view.Enabled = counts[batch.ID][model.RedemptionStatusEnabled]
		view.Used = counts[batch.ID][model.RedemptionStatusUsed]
		view.Disabled = counts[batch.ID][model.RedemptionStatusDisabled]
		views = append(views, view)
	}
	return views, nil
}

// RedemptionCodeQuery 兑换码列表条件; 零值表示不限。
type RedemptionCodeQuery struct {
	BatchID uint
	Status  string
	Keyword string
	Limit   int
	Offset  int
}

// RedemptionCodeList 兑换码分页列表与命中总数。
func RedemptionCodeList(ctx context.Context, query RedemptionCodeQuery) ([]model.RedemptionCode, int64, error) {
	limit := query.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	if query.Offset < 0 {
		query.Offset = 0
	}
	build := func() *gorm.DB {
		statement := db.GetDB().WithContext(ctx).Model(&model.RedemptionCode{})
		if query.BatchID != 0 {
			statement = statement.Where("batch_id = ?", query.BatchID)
		}
		if query.Status != "" {
			statement = statement.Where("status = ?", query.Status)
		}
		if query.Keyword != "" {
			statement = statement.Where("code LIKE ?", "%"+query.Keyword+"%")
		}
		return statement
	}
	var total int64
	if err := build().Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count codes: %w", err)
	}
	entries := []model.RedemptionCode{}
	if err := build().Order("id DESC").Limit(limit).Offset(query.Offset).Find(&entries).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to list codes: %w", err)
	}
	return entries, total, nil
}

// RedemptionCodeDisable 禁用未兑兑换码; 已兑的不允许翻改, 兑换结果不可逆。
func RedemptionCodeDisable(ctx context.Context, id uint) error {
	result := db.GetDB().WithContext(ctx).Model(&model.RedemptionCode{}).
		Where("id = ? AND status = ?", id, model.RedemptionStatusEnabled).
		Update("status", model.RedemptionStatusDisabled)
	if result.Error != nil {
		return fmt.Errorf("failed to disable code: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("code is not in a disableable state")
	}
	return nil
}

package op

import (
	"context"
	"fmt"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
)

// balanceAlertTriggerWindow 触发去重窗口: 同一条预警在窗口内只提醒一次。
// 余额不足往往持续很久, 用户看到一次就够了 —— 反复提醒既不能解决问题, 只会
// 让人把通知当成噪音。窗口取 1 小时, 兼顾"提醒别太密"和"用户充值后再次跌破能再提醒"。
const balanceAlertTriggerWindow = time.Hour

// BalanceAlertGet 读取用户的余额预警配置; 没有配置时返回默认视图(禁用)。
func BalanceAlertGet(ctx context.Context, userID uint) (model.BalanceAlertView, error) {
	var alert model.BalanceAlert
	err := db.GetDB().WithContext(ctx).Where("user_id = ?", userID).Take(&alert).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			// 没有配置不是错误: 默认禁用, 由前端引导用户设置。
			return model.BalanceAlertView{Enabled: false}, nil
		}
		return model.BalanceAlertView{}, fmt.Errorf("failed to get balance alert: %w", err)
	}
	return model.BalanceAlertView{
		ThresholdCNY:    alert.ThresholdCNY,
		Enabled:         alert.Enabled,
		LastTriggeredAt: alert.LastTriggeredAt,
	}, nil
}

// BalanceAlertSet 保存余额预警配置(覆盖语义)。
// 阈值为 0 或显式关闭时, 仍然保留这条记录, 便于前端回显用户上次的设置。
func BalanceAlertSet(ctx context.Context, userID uint, req model.BalanceAlertUpsert) (model.BalanceAlertView, error) {
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	// 阈值与开关必须一致: 阈值为 0 时即使 enabled=true 也没有意义(可用余额永远不低于 0),
	// 统一归一成"关闭", 免得用户看到"已启用"却永远收不到提醒。
	if req.ThresholdCNY <= 0 {
		enabled = false
	}

	alert := model.BalanceAlert{
		UserID:       userID,
		ThresholdCNY: req.ThresholdCNY,
		Enabled:      enabled,
	}
	err := db.GetDB().WithContext(ctx).Where("user_id = ?", userID).
		Assign(map[string]any{
			"threshold_cny": req.ThresholdCNY,
			"enabled":       enabled,
		}).
		FirstOrCreate(&alert).Error
	if err != nil {
		return model.BalanceAlertView{}, fmt.Errorf("failed to save balance alert: %w", err)
	}
	return model.BalanceAlertView{
		ThresholdCNY:    alert.ThresholdCNY,
		Enabled:         alert.Enabled,
		LastTriggeredAt: alert.LastTriggeredAt,
	}, nil
}

// BalanceAlertDelete 取消余额预警。
func BalanceAlertDelete(ctx context.Context, userID uint) error {
	if err := db.GetDB().WithContext(ctx).Where("user_id = ?", userID).
		Delete(&model.BalanceAlert{}).Error; err != nil {
		return fmt.Errorf("failed to delete balance alert: %w", err)
	}
	return nil
}

// BalanceAlertCheck 在余额变化后检查是否需要提醒用户。
//
// 返回是否真的发起了提醒(被去重或未配置时为 false), 供调用方判断。
// available 是可用余额(Balance - Frozen), threshold 是配置的阈值;
// 只有 available < threshold 才提醒 —— 恰好等于阈值不算跌破。
func BalanceAlertCheck(ctx context.Context, userID uint, available float64) (bool, error) {
	var alert model.BalanceAlert
	err := db.GetDB().WithContext(ctx).Where("user_id = ?", userID).Take(&alert).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return false, nil
		}
		return false, fmt.Errorf("failed to load balance alert: %w", err)
	}
	if !alert.Enabled || alert.ThresholdCNY <= 0 {
		return false, nil
	}
	if available >= alert.ThresholdCNY {
		return false, nil
	}
	// 去重: 窗口内已提醒过就不再打扰。
	if alert.LastTriggeredAt != nil && time.Since(*alert.LastTriggeredAt) < balanceAlertTriggerWindow {
		return false, nil
	}

	notification := balanceLowNotification(userID, available)
	targets, err := webhookEnqueueTargets(ctx, userID, string(notification.Type))
	if err != nil {
		return false, fmt.Errorf("failed to load balance alert webhook targets: %w", err)
	}
	// Allocate the dedup window only when the history, notification and webhook
	// delivery rows can all commit. Acquire the write lock before reading in the transaction.
	now := time.Now()
	triggered := false
	err = db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.BalanceAlert{}).
			Where("user_id = ? AND enabled = ? AND threshold_cny > 0 AND threshold_cny > ?", userID, true, available).
			Where("(last_triggered_at IS NULL OR last_triggered_at <= ?)", now.Add(-balanceAlertTriggerWindow)).
			Update("last_triggered_at", now)
		if result.Error != nil {
			return fmt.Errorf("failed to update balance alert trigger time: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return nil
		}
		if err := tx.Create(&model.BalanceAlertHistory{UserID: userID, TriggeredAt: now, Source: "balance_alert"}).Error; err != nil {
			return fmt.Errorf("failed to record balance alert history: %w", err)
		}
		// 不仅 API 只返回最近 10 条, 数据库也只保留最近 10 条/用户。
		// 否则一个持续使用多年的账号每小时一条会无限增长; 历史定义本来就是"最近 10 次",
		// 比它更早的行没有产品价值。查询与删除在同事务, 首条语句仍是上面的条件 UPDATE。
		var staleIDs []uint
		if err := tx.Model(&model.BalanceAlertHistory{}).Where("user_id = ?", userID).
			Order("triggered_at DESC, id DESC").Offset(10).Pluck("id", &staleIDs).Error; err != nil {
			return fmt.Errorf("failed to identify expired balance alert history: %w", err)
		}
		if len(staleIDs) > 0 {
			if err := tx.Where("id IN ?", staleIDs).Delete(&model.BalanceAlertHistory{}).Error; err != nil {
				return fmt.Errorf("failed to prune balance alert history: %w", err)
			}
		}
		if err := notificationCreateTx(tx, &notification, targets); err != nil {
			return err
		}
		triggered = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return triggered, nil
}

// BalanceAlertHistoryList returns the current user's newest actual triggers.
func BalanceAlertHistoryList(ctx context.Context, userID uint) ([]model.BalanceAlertHistory, error) {
	var rows []model.BalanceAlertHistory
	err := db.GetDB().WithContext(ctx).Where("user_id = ?", userID).
		Order("triggered_at DESC, id DESC").Limit(10).Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list balance alert history: %w", err)
	}
	return rows, nil
}

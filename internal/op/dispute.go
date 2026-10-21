package op

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
)

// TradeDetail 聚合单笔调用的交易全貌: 计费明细、请求上下文与余额流水
type TradeDetail struct {
	Record   model.BillingRecord   `json:"record"`
	Request  *model.RelayRequest   `json:"request,omitempty"`
	Ledgers  []model.BillingLedger `json:"ledgers"`
	Disputes []model.Dispute       `json:"disputes"`
}

// TradeDetailGet 依据 BillingRecord.ID 获取统一交易详情并执行权限校验
func TradeDetailGet(ctx context.Context, recordID uint64, viewerID uint, viewerRole model.Role) (*TradeDetail, error) {
	var record model.BillingRecord
	if err := db.GetDB().WithContext(ctx).First(&record, recordID).Error; err != nil {
		return nil, fmt.Errorf("billing record not found: %w", err)
	}
	// 权限边界: 普通用户只能看自己的消费交易; 渠道商只能看自己渠道的供货交易; 管理员可看全量
	if viewerRole != model.RoleAdmin && record.UserID != viewerID && record.OwnerID != viewerID {
		return nil, errors.New("permission denied to access this trade")
	}

	detail := &TradeDetail{Record: record}
	if record.RequestID > 0 {
		var req model.RelayRequest
		if err := db.GetDB().WithContext(ctx).First(&req, record.RequestID).Error; err == nil {
			// 若是渠道商查看，隐去客户端敏感请求正文与客户端 IP
			if viewerRole != model.RoleAdmin && record.UserID != viewerID {
				req.RequestBody = ""
				req.ClientIP = ""
			}
			detail.Request = &req
		}
		var ledgers []model.BillingLedger
		_ = db.GetDB().WithContext(ctx).Where("request_id = ?", record.RequestID).Order("id ASC").Find(&ledgers).Error
		// 渠道商不能看付费用户的可用余额变动
		if viewerRole != model.RoleAdmin && record.UserID != viewerID {
			for i := range ledgers {
				ledgers[i].BalanceAfter = 0
			}
		}
		detail.Ledgers = ledgers
	}

	var disputes []model.Dispute
	_ = db.GetDB().WithContext(ctx).Where("record_id = ?", recordID).Order("id DESC").Find(&disputes).Error
	detail.Disputes = disputes

	return detail, nil
}

// DisputeCreate 用户对已结算的计费明细发起退款申诉
func DisputeCreate(ctx context.Context, userID uint, recordID uint64, reason string) (*model.Dispute, error) {
	if reason == "" {
		return nil, errors.New("dispute reason is required")
	}
	var record model.BillingRecord
	if err := db.GetDB().WithContext(ctx).First(&record, recordID).Error; err != nil {
		return nil, fmt.Errorf("billing record not found: %w", err)
	}
	if record.UserID != userID {
		return nil, errors.New("can only dispute your own billing record")
	}
	if record.Status == model.BillingStatusRefunded {
		return nil, errors.New("this billing record is already refunded")
	}
	var existing model.Dispute
	if err := db.GetDB().WithContext(ctx).Where("record_id = ? AND status = ?", recordID, model.DisputeStatusPending).First(&existing).Error; err == nil {
		return nil, errors.New("a pending dispute already exists for this transaction")
	}

	dispute := model.Dispute{
		RecordID:  record.ID,
		RequestID: record.RequestID,
		UserID:    record.UserID,
		OwnerID:   record.OwnerID,
		ChannelID: record.ChannelID,
		ModelName: record.ModelName,
		UserCost:  record.UserCost,
		Reason:    reason,
		Status:    model.DisputeStatusPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.GetDB().WithContext(ctx).Create(&dispute).Error; err != nil {
		return nil, fmt.Errorf("failed to create dispute: %w", err)
	}
	return &dispute, nil
}

// DisputeQuery 申诉列表筛选参数
type DisputeQuery struct {
	UserID  uint
	OwnerID uint
	Status  string
	Limit   int
	Offset  int
}

// DisputeList 分页查询申诉列表
func DisputeList(ctx context.Context, filter DisputeQuery) ([]model.Dispute, int64, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	build := func() *gorm.DB {
		q := db.GetDB().WithContext(ctx).Model(&model.Dispute{})
		if filter.UserID != 0 {
			q = q.Where("user_id = ?", filter.UserID)
		}
		if filter.OwnerID != 0 {
			q = q.Where("owner_id = ?", filter.OwnerID)
		}
		if filter.Status != "" {
			q = q.Where("status = ?", filter.Status)
		}
		return q
	}
	var total int64
	if err := build().Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []model.Dispute
	if err := build().Order("id DESC").Limit(limit).Offset(offset).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// DisputeReview 管理员审核申诉 (approve: 退款并冲正; reject: 驳回并留备注)
func DisputeReview(ctx context.Context, disputeID uint64, approve bool, reviewNote string, reviewerID uint, reviewerName string) error {
	return db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var dispute model.Dispute
		if err := tx.First(&dispute, disputeID).Error; err != nil {
			return fmt.Errorf("dispute not found: %w", err)
		}
		if dispute.Status != model.DisputeStatusPending {
			return errors.New("dispute is already reviewed")
		}

		now := time.Now()
		if !approve {
			dispute.Status = model.DisputeStatusRejected
			dispute.ReviewNote = reviewNote
			dispute.ReviewedBy = reviewerID
			dispute.ReviewedAt = &now
			dispute.UpdatedAt = now
			return tx.Save(&dispute).Error
		}

		// 批准退款: 必须原子执行 (1) 计费记录置 refunded (2) 返还用户余额与冲正流水 (3) 扣减发布者收入 (4) 更新申诉单
		var record model.BillingRecord
		if err := tx.First(&record, dispute.RecordID).Error; err != nil {
			return fmt.Errorf("associated billing record not found: %w", err)
		}
		if record.Status == model.BillingStatusRefunded {
			return errors.New("billing record has already been refunded")
		}

		record.Status = model.BillingStatusRefunded
		if err := tx.Save(&record).Error; err != nil {
			return fmt.Errorf("failed to update billing status: %w", err)
		}

		// 1. 退款给付费用户
		if record.UserCost > 0 {
			userUpdates := map[string]any{
				"balance":     gorm.Expr("balance + ?", record.UserCost),
				"total_spent": gorm.Expr("CASE WHEN total_spent >= ? THEN total_spent - ? ELSE 0 END", record.UserCost, record.UserCost),
			}
			if err := tx.Model(&model.User{}).Where("id = ?", record.UserID).Updates(userUpdates).Error; err != nil {
				return fmt.Errorf("failed to refund user balance: %w", err)
			}
			note := fmt.Sprintf("申诉退款 (单号 #%d, 请求 #%d): %s", dispute.ID, record.RequestID, reviewNote)
			if err := ledgerRecord(tx, model.BillingLedger{
				UserID:       record.UserID,
				Kind:         model.LedgerKindRefund,
				Amount:       record.UserCost,
				BalanceAfter: balanceOf(tx, record.UserID),
				RequestID:    record.RequestID,
				ModelName:    record.ModelName,
				ActorID:      reviewerID,
				ActorName:    reviewerName,
				Note:         note,
			}); err != nil {
				return fmt.Errorf("failed to record refund ledger: %w", err)
			}
		}

		// 2. 扣减渠道商应得收入
		if record.OwnerRevenue > 0 && record.OwnerID > 0 {
			if err := tx.Model(&model.User{}).Where("id = ?", record.OwnerID).
				Update("total_revenue", gorm.Expr("CASE WHEN total_revenue >= ? THEN total_revenue - ? ELSE 0 END", record.OwnerRevenue, record.OwnerRevenue)).Error; err != nil {
				return fmt.Errorf("failed to deduct owner revenue: %w", err)
			}
		}

		dispute.Status = model.DisputeStatusApproved
		dispute.ReviewNote = reviewNote
		dispute.ReviewedBy = reviewerID
		dispute.ReviewedAt = &now
		dispute.UpdatedAt = now
		return tx.Save(&dispute).Error
	})
}

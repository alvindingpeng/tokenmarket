package op

import (
	"context"
	"fmt"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
)

// notificationDedupWindow 同类未读通知的去重窗口。
//
// 余额不足、限流触发这类事件会在短时间内高频重复: 每一次结算都可能命中同一个条件。
// 如果每次都落一条, 用户打开通知中心看到的就是几十条一模一样的提醒, 真正的信息被淹没。
// 窗口内已有同类未读通知时不再新增 —— 用户读掉之后, 下一次事件仍会正常提醒。
const notificationDedupWindow = time.Hour

// NotificationCreateDedup 创建通知, 窗口内已有同类未读通知时跳过。
// 返回是否真的写入了一条(被去重时为 false), 供调用方判断是否需要后续动作。
func NotificationCreateDedup(ctx context.Context, userID uint, notifType model.NotificationType, title, content, link string, relatedID uint64) (bool, error) {
	var existing int64
	err := db.GetDB().WithContext(ctx).Model(&model.Notification{}).
		Where("user_id = ? AND type = ? AND status = ? AND created_at >= ?",
			userID, notifType, model.NotificationStatusUnread, time.Now().Add(-notificationDedupWindow)).
		Count(&existing).Error
	if err != nil {
		return false, fmt.Errorf("failed to check notification dedup: %w", err)
	}
	if existing > 0 {
		return false, nil
	}
	return true, NotificationCreate(ctx, userID, notifType, title, content, link, relatedID)
}

// NotificationCreate 创建通知。
//
// Webhook 入队与通知写入同事务(webhookEnqueueForNotificationTx): 通知落了库, 该用户
// 订阅了此事件的 webhook 就一定在队里; 真实入队写入失败则整单回滚并上抛(不吞错),
// 否则用户收到了通知、订阅方永远等不到。
// 但 webhook 基建问题只抑制出站、绝不连累通知本身: 缺表(旧测试 fixture/老库)与
// 用户不存在(旧用例虚设 userID)都只是"无处可发/不该发", 通知照常成功;
// 出站抑制由 worker 收件人闸门负责。损坏的订阅事件行仍报错可见(数据损坏需暴露)。
func NotificationCreate(ctx context.Context, userID uint, notifType model.NotificationType, title, content, link string, relatedID uint64) error {
	// 目标预读放在事务外(只读): 事务里只留写(通知行+投递行), 尽量短持锁,
	// 避免长事务加剧 SQLite 写锁竞争。损坏事件行在此即报错, 不会创建半截通知。
	targets, err := webhookEnqueueTargets(ctx, userID, string(notifType))
	if err != nil {
		return fmt.Errorf("failed to enqueue webhook deliveries: %w", err)
	}
	return db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		notification := model.Notification{
			UserID:    userID,
			Type:      notifType,
			Title:     title,
			Content:   content,
			Link:      link,
			RelatedID: relatedID,
			Status:    model.NotificationStatusUnread,
		}
		if err := tx.Create(&notification).Error; err != nil {
			return fmt.Errorf("failed to create notification: %w", err)
		}
		if err := webhookEnqueueForNotificationTx(tx, &notification, targets); err != nil {
			return fmt.Errorf("failed to enqueue webhook deliveries: %w", err)
		}
		return nil
	})
}

// NotificationList 获取通知列表
func NotificationList(ctx context.Context, userID uint, req model.NotificationListRequest) ([]model.Notification, int64, error) {
	page := req.Page
	if page < 1 {
		page = 1
	}
	pageSize := req.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	query := db.GetDB().WithContext(ctx).Model(&model.Notification{}).Where("user_id = ?", userID)
	if req.Status != "" {
		query = query.Where("status = ?", req.Status)
	}
	if req.Type != "" {
		query = query.Where("type = ?", req.Type)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count notifications: %w", err)
	}

	var notifications []model.Notification
	if err := query.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&notifications).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to list notifications: %w", err)
	}

	return notifications, total, nil
}

// NotificationMarkRead 标记通知为已读
func NotificationMarkRead(ctx context.Context, userID uint, ids []uint64) error {
	now := time.Now()
	if err := db.GetDB().WithContext(ctx).Model(&model.Notification{}).
		Where("user_id = ? AND id IN ? AND status = ?", userID, ids, model.NotificationStatusUnread).
		Updates(map[string]interface{}{
			"status":  model.NotificationStatusRead,
			"read_at": now,
		}).Error; err != nil {
		return fmt.Errorf("failed to mark notifications as read: %w", err)
	}
	return nil
}

// NotificationMarkAllRead 标记所有通知为已读
func NotificationMarkAllRead(ctx context.Context, userID uint) error {
	now := time.Now()
	if err := db.GetDB().WithContext(ctx).Model(&model.Notification{}).
		Where("user_id = ? AND status = ?", userID, model.NotificationStatusUnread).
		Updates(map[string]interface{}{
			"status":  model.NotificationStatusRead,
			"read_at": now,
		}).Error; err != nil {
		return fmt.Errorf("failed to mark all notifications as read: %w", err)
	}
	return nil
}

// NotificationUnreadCount 获取未读数量
func NotificationUnreadCount(ctx context.Context, userID uint) (int64, error) {
	var count int64
	if err := db.GetDB().WithContext(ctx).Model(&model.Notification{}).
		Where("user_id = ? AND status = ?", userID, model.NotificationStatusUnread).
		Count(&count).Error; err != nil {
		return 0, fmt.Errorf("failed to count unread notifications: %w", err)
	}
	return count, nil
}

// NotificationDelete 删除通知
func NotificationDelete(ctx context.Context, userID uint, id uint64) error {
	result := db.GetDB().WithContext(ctx).
		Where("user_id = ? AND id = ?", userID, id).
		Delete(&model.Notification{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete notification: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("notification not found")
	}
	return nil
}

package op

import (
	"context"
	"fmt"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// NotificationCreate 创建通知
func NotificationCreate(ctx context.Context, userID uint, notifType model.NotificationType, title, content, link string, relatedID uint64) error {
	notification := model.Notification{
		UserID:    userID,
		Type:      notifType,
		Title:     title,
		Content:   content,
		Link:      link,
		RelatedID: relatedID,
		Status:    model.NotificationStatusUnread,
	}
	if err := db.GetDB().WithContext(ctx).Create(&notification).Error; err != nil {
		return fmt.Errorf("failed to create notification: %w", err)
	}
	return nil
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

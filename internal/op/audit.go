package op

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

// AuditLogAdd 写入一条审计记录; 操作者名称取自账号快照。
func AuditLogAdd(ctx context.Context, actorID uint, action, target, detail string) error {
	username := ""
	if user, err := UserGetByID(actorID); err == nil {
		username = user.Username
	}
	return db.GetDB().WithContext(ctx).Create(&model.AuditLog{
		UserID:   actorID,
		Username: username,
		Action:   action,
		Target:   target,
		Detail:   detail,
	}).Error
}

// AuditLogFilter 审计记录筛选条件; 零值表示该维度不限。
type AuditLogFilter struct {
	Keyword  string    // 模糊匹配操作者、目标与明细。
	Action   string    // 动作标识精确匹配。
	Operator string    // 操作者用户名模糊匹配。
	From     time.Time // 起始时间(含)。
	To       time.Time // 结束时间(含)。
}

// applyAuditFilter 追加审计筛选条件到查询; 与计数查询共用, 保证总数与列表一致。
func applyAuditFilter(db *gorm.DB, filter AuditLogFilter) *gorm.DB {
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		like := "%" + keyword + "%"
		db = db.Where("username LIKE ? OR target LIKE ? OR detail LIKE ?", like, like, like)
	}
	if action := strings.TrimSpace(filter.Action); action != "" {
		db = db.Where("action = ?", action)
	}
	if operator := strings.TrimSpace(filter.Operator); operator != "" {
		db = db.Where("username LIKE ?", "%"+operator+"%")
	}
	if !filter.From.IsZero() {
		db = db.Where("created_at >= ?", filter.From)
	}
	if !filter.To.IsZero() {
		db = db.Where("created_at <= ?", filter.To)
	}
	return db
}

// AuditActions 返回已有审计记录中出现过的动作标识(去重后按字典序), 供筛选下拉展示。
func AuditActions() ([]string, error) {
	var actions []string
	if err := db.GetDB().Model(&model.AuditLog{}).Distinct().Order("action ASC").Pluck("action", &actions).Error; err != nil {
		return nil, fmt.Errorf("failed to list audit actions: %w", err)
	}
	if actions == nil {
		actions = make([]string, 0)
	}
	return actions, nil
}

// AuditLogList 按筛选条件倒序分页返回审计记录与总数。
func AuditLogList(filter AuditLogFilter, limit, offset int) ([]model.AuditLog, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var total int64
	if err := applyAuditFilter(db.GetDB().Model(&model.AuditLog{}), filter).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count audit logs: %w", err)
	}
	var logs []model.AuditLog
	query := applyAuditFilter(db.GetDB().Model(&model.AuditLog{}), filter)
	if err := query.Order("id DESC").Limit(limit).Offset(offset).Find(&logs).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to list audit logs: %w", err)
	}
	// 读取侧承诺不为 null。
	if logs == nil {
		logs = make([]model.AuditLog, 0)
	}
	return logs, total, nil
}

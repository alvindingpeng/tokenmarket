package op

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
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

// TicketAuditHistoryList 返回单张工单的操作历史。默认工单只属于提交用户，不能直接复用管理员全局
// AuditLogList：它的查询面包含所有用户和任意动作，开放给普通用户会变成全站审计侧信道。
// 本函数先通过 ticketOwned 校验归属，再把查询严格限制到 target="#<ticketID>" 与 ticket.* 动作，
// 并且只向调用方返回结构化的动作字段，不返回 detail（避免未来 detail 意外写入正文/敏感内容）。
func TicketAuditHistoryList(ctx context.Context, userID uint, isAdmin bool, ticketID uint64, limit int) ([]model.AuditLog, error) {
	if _, err := ticketOwned(ctx, userID, isAdmin, ticketID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var rows []model.AuditLog
	err := db.GetDB().WithContext(ctx).
		Where("target = ? AND action IN ?", fmt.Sprintf("#%d", ticketID), []string{"ticket.create", "ticket.reply", "ticket.close", "ticket.reopen", "ticket.assign", "ticket.status"}).
		Order("id ASC").Limit(limit).Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list ticket audit history: %w", err)
	}
	if rows == nil {
		rows = make([]model.AuditLog, 0)
	}
	return rows, nil
}

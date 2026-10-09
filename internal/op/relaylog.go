package op

import (
	"fmt"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

// RelayRequestFilter 调用日志筛选条件; 零值表示该维度不限。
type RelayRequestFilter struct {
	Keyword  string
	Status   string // 逗号分隔的终态列表。
	From     time.Time
	To       time.Time
	ClientIP string // 客户端 IP 模糊匹配。
	Model    string // 模型名模糊匹配(客户端模型或上游落地模型)。
	Channel  string // 渠道模糊匹配(渠道与 Key 名称或发布编码)。
}

// applyRelayFilter 追加调用日志筛选条件; 计数与列表共用保证一致。
func applyRelayFilter(query *gorm.DB, filter RelayRequestFilter, scope model.Scope) *gorm.DB {
	if !scope.IsAdmin() {
		query = query.Where("user_id = ?", scope.ID)
	}
	if ip := strings.TrimSpace(filter.ClientIP); ip != "" {
		query = query.Where("client_ip LIKE ?", "%"+ip+"%")
	}
	if modelName := strings.TrimSpace(filter.Model); modelName != "" {
		like := "%" + modelName + "%"
		query = query.Where("model LIKE ? OR target_model LIKE ?", like, like)
	}
	if channel := strings.TrimSpace(filter.Channel); channel != "" {
		like := "%" + channel + "%"
		query = query.Where("target_channel_key LIKE ? OR target_channel_code LIKE ?", like, like)
	}
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where("model LIKE ? OR api_key_name LIKE ? OR target_channel_key LIKE ? OR target_channel_code LIKE ? OR target_model LIKE ? OR error LIKE ?",
			like, like, like, like, like, like)
	}
	if statuses := strings.TrimSpace(filter.Status); statuses != "" {
		parts := strings.Split(statuses, ",")
		clean := make([]string, 0, len(parts))
		for _, part := range parts {
			if part = strings.TrimSpace(part); part != "" {
				clean = append(clean, part)
			}
		}
		if len(clean) > 0 {
			query = query.Where("status IN ?", clean)
		}
	}
	if !filter.From.IsZero() {
		query = query.Where("started_at >= ?", filter.From)
	}
	if !filter.To.IsZero() {
		query = query.Where("started_at <= ?", filter.To)
	}
	return query
}

// RelayRequestSave 按主键写入或覆盖请求快照; 由异步持久化写手调用。
func RelayRequestSave(row model.RelayRequest) error {
	database := db.GetDB()
	var count int64
	if err := database.Model(&model.RelayRequest{}).Where("id = ?", row.ID).Count(&count).Error; err != nil {
		return fmt.Errorf("failed to check relay request: %w", err)
	}
	if count == 0 {
		return database.Create(&row).Error
	}
	return database.Save(&row).Error
}

// RelayAttemptSave 按 (request_id, round) 写入或覆盖一次上游尝试。
func RelayAttemptSave(row model.RelayRequestAttempt) error {
	database := db.GetDB()
	var count int64
	if err := database.Model(&model.RelayRequestAttempt{}).
		Where("request_id = ? AND round = ?", row.RequestID, row.Round).Count(&count).Error; err != nil {
		return fmt.Errorf("failed to check relay attempt: %w", err)
	}
	if count == 0 {
		return database.Create(&row).Error
	}
	// 覆盖已有行必须带上主键, 否则 Save 按零主键走插入并撞唯一约束。
	var existing model.RelayRequestAttempt
	if err := database.Where("request_id = ? AND round = ?", row.RequestID, row.Round).First(&existing).Error; err == nil {
		row.ID = existing.ID
	}
	return database.Save(&row).Error
}

// RelayRequestList 按筛选条件倒序分页返回请求与总数。
func RelayRequestList(scope model.Scope, filter RelayRequestFilter, limit, offset int) ([]model.RelayRequest, int64, error) {
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	var total int64
	if err := applyRelayFilter(db.GetDB().Model(&model.RelayRequest{}), filter, scope).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count relay requests: %w", err)
	}
	var rows []model.RelayRequest
	query := applyRelayFilter(db.GetDB().Model(&model.RelayRequest{}), filter, scope)
	if err := query.Order("id DESC").Limit(limit).Offset(offset).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to list relay requests: %w", err)
	}
	if rows == nil {
		rows = make([]model.RelayRequest, 0)
	}
	return rows, total, nil
}

// RelayRequestGet 按主键取一条请求。
func RelayRequestGet(id uint64) (model.RelayRequest, error) {
	var row model.RelayRequest
	if err := db.GetDB().Where("id = ?", id).First(&row).Error; err != nil {
		return model.RelayRequest{}, err
	}
	return row, nil
}

// RelayAttemptsByRequest 取一条请求的全部上游尝试, 按轮次升序。
func RelayAttemptsByRequest(requestID uint64) ([]model.RelayRequestAttempt, error) {
	var rows []model.RelayRequestAttempt
	if err := db.GetDB().Where("request_id = ?", requestID).Order("round ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	if rows == nil {
		rows = make([]model.RelayRequestAttempt, 0)
	}
	return rows, nil
}

// RelayRequestClear 清空调用日志与尝试记录。
func RelayRequestClear() error {
	if err := db.GetDB().Where("1 = 1").Delete(&model.RelayRequestAttempt{}).Error; err != nil {
		return err
	}
	return db.GetDB().Where("1 = 1").Delete(&model.RelayRequest{}).Error
}

// RelayRequestClearUser 清空指定用户的调用日志与尝试记录。
func RelayRequestClearUser(userID uint) error {
	if err := db.GetDB().Where("request_id IN (SELECT id FROM relay_requests WHERE user_id = ?)", userID).Delete(&model.RelayRequestAttempt{}).Error; err != nil {
		return err
	}
	return db.GetDB().Where("user_id = ?", userID).Delete(&model.RelayRequest{}).Error
}

// RelayInterruptStale 把上次进程遗留的未完成请求标为中断, 服务重启后日志不再显示运行中。
func RelayInterruptStale() error {
	now := time.Now()
	return db.GetDB().Model(&model.RelayRequest{}).
		Where("status IN ?", []string{"running", "committed"}).
		Updates(map[string]interface{}{
			"status":      "failed",
			"error":       "interrupted_by_restart",
			"finished_at": now,
		}).Error
}

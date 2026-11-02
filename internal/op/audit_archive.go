package op

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/alert"
	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// 审计归档与调用日志归档分开的原因: 审计是合规证据, 留存期远长于调用日志,
// 且"谁删了审计"本身要被审计。混在 log_retention_days 上会让 7 天保留顺手销毁合规证据。

// AuditRetentionDays 返回审计日志留存天数, 非法值回退 180 天。
func AuditRetentionDays() int {
	days, err := SettingGetInt(model.SettingKeyAuditRetentionDays)
	if err != nil || days < 1 {
		return 180
	}
	if days > 3650 {
		return 3650
	}
	return days
}

// AuditArchiveEnabled 判断清理前是否归档; 读不到设置时按 true 处理,
// 让"配置缺失"退化成"多留一份文件"而不是"直接删"。
func AuditArchiveEnabled() bool {
	enabled, err := SettingGetBool(model.SettingKeyAuditArchiveEnabled)
	if err != nil {
		return true
	}
	return enabled
}

// AuditArchiveDir 审计归档目录, 与调用日志归档并列。
func AuditArchiveDir() string {
	return filepath.Join(ArchiveDir(), "audit")
}

// AuditArchiveResult 一次审计归档与清理的结果。
type AuditArchiveResult struct {
	Cutoff       time.Time `json:"cutoff"`
	Archived     int       `json:"archived"`
	File         string    `json:"file"`
	Deleted      int64     `json:"deleted"`
	RetentionDay int       `json:"retention_days"`
	ArchiveError string    `json:"archive_error,omitempty"`
	Enabled      bool      `json:"archive_enabled"`
}

// AuditLifecycleRun 归档并清理超期审计记录。
//
// 顺序不可颠倒: 先归档并确认落盘, 再删除。归档失败时本轮一条都不删 ——
// 审计证据一旦删掉就无法恢复, 磁盘空间不足是能修的问题, 证据缺失不是。
func AuditLifecycleRun(ctx context.Context) (AuditArchiveResult, error) {
	retention := AuditRetentionDays()
	cutoff := time.Now().AddDate(0, 0, -retention)
	result := AuditArchiveResult{Cutoff: cutoff, RetentionDay: retention, Enabled: AuditArchiveEnabled()}

	if result.Enabled {
		file, archived, err := archiveAuditBefore(ctx, cutoff)
		result.File, result.Archived = file, archived
		if err != nil {
			result.ArchiveError = err.Error()
			RaiseAlertFacts(ctx, AlertFacts{
				Kind:   "audit_archive_failed",
				Reason: "audit archive failed",
				Fields: []alert.Field{
					{Label: "清理截止", Value: cutoff.Format("2006-01-02")},
					{Label: "归档文件", Value: file},
					{Label: "已归档", Value: fmt.Sprintf("%d 条", archived)},
					{Label: "错误", Value: err.Error()},
					{Label: "保护动作", Value: "归档失败时本轮不删除任何审计记录"},
					{Label: "影响", Value: "审计表持续膨胀; 合规证据清理被卡住"},
					{Label: "建议", Value: "检查归档目录可写性与磁盘余量后重试"},
				},
			})
			return result, nil
		}
	}

	deleted := db.GetDB().WithContext(ctx).Where("created_at < ?", cutoff).Delete(&model.AuditLog{})
	if deleted.Error != nil {
		return result, fmt.Errorf("delete expired audit logs: %w", deleted.Error)
	}
	result.Deleted = deleted.RowsAffected
	return result, nil
}

// archiveAuditBefore 把超期审计记录追加写入当日 NDJSON 文件, 返回路径与条数。
func archiveAuditBefore(ctx context.Context, cutoff time.Time) (string, int, error) {
	var rows []model.AuditLog
	if err := db.GetDB().WithContext(ctx).Where("created_at < ?", cutoff).Order("id ASC").Find(&rows).Error; err != nil {
		return "", 0, fmt.Errorf("load audit logs for archive: %w", err)
	}
	if len(rows) == 0 {
		return "", 0, nil
	}
	if err := os.MkdirAll(AuditArchiveDir(), 0o750); err != nil {
		return "", 0, fmt.Errorf("create audit archive dir: %w", err)
	}
	path := filepath.Join(AuditArchiveDir(), "audit-"+time.Now().Format("20060102")+".ndjson")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return "", 0, fmt.Errorf("open audit archive file: %w", err)
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	for _, row := range rows {
		if err := encoder.Encode(row); err != nil {
			return "", 0, fmt.Errorf("write audit archive entry: %w", err)
		}
	}
	// 落盘后才允许调用方删除: 只在 PageCache 里的归档文件, 断电后就是零条证据。
	if err := file.Sync(); err != nil {
		return "", 0, fmt.Errorf("sync audit archive file: %w", err)
	}
	return path, len(rows), nil
}

// AuditArchiveList 返回审计归档文件清单, 用于运维页查看留存情况。
func AuditArchiveList() ([]map[string]any, error) {
	entries, err := os.ReadDir(AuditArchiveDir())
	if err != nil {
		if os.IsNotExist(err) {
			return []map[string]any{}, nil
		}
		return nil, err
	}
	files := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".ndjson") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, map[string]any{
			"name":     entry.Name(),
			"size":     info.Size(),
			"mod_time": info.ModTime(),
		})
	}
	return files, nil
}

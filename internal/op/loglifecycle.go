package op

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// 默认脱敏字段: 这些键的值在保存正文前替换为固定掩码。
var defaultMaskFields = []string{"api_key", "authorization", "password", "secret", "token", "access_token", "refresh_token", "cookie"}

// MaskFields 返回生效的脱敏字段集合: 系统默认并集用户自定义。
func MaskFields() []string {
	fields := append([]string(nil), defaultMaskFields...)
	if custom, err := SettingGetString(model.SettingKeyLogMaskFields); err == nil && strings.TrimSpace(custom) != "" {
		for _, field := range strings.Split(custom, ",") {
			if field = strings.TrimSpace(field); field != "" {
				fields = append(fields, field)
			}
		}
	}
	return fields
}

// MaskJSONBody 对 JSON 正文按字段名脱敏; 非 JSON 或解析失败时原样返回。
// 递归处理嵌套对象与数组, 键名大小写不敏感。
func MaskJSONBody(body string) string {
	if strings.TrimSpace(body) == "" {
		return body
	}
	var parsed any
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return body
	}
	masked := maskValue(parsed, MaskFields())
	encoded, err := json.Marshal(masked)
	if err != nil {
		return body
	}
	return string(encoded)
}

// maskValue 递归替换敏感键的值。
func maskValue(value any, fields []string) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if isSensitiveKey(key, fields) {
				typed[key] = "***"
				continue
			}
			typed[key] = maskValue(item, fields)
		}
		return typed
	case []any:
		for index, item := range typed {
			typed[index] = maskValue(item, fields)
		}
		return typed
	default:
		return value
	}
}

// isSensitiveKey 判断键名是否命中脱敏列表, 大小写不敏感并忽略连字符与下划线差异。
func isSensitiveKey(key string, fields []string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(key))
	for _, field := range fields {
		if normalized == strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(field)) {
			return true
		}
	}
	return false
}

// ShouldStoreBody 判断本次是否保存请求/响应正文; 关闭后仅保留概览与用量。
func ShouldStoreBody() bool {
	enabled, err := SettingGetBool(model.SettingKeyLogStoreBody)
	return err != nil || enabled
}

// LogRetentionDays 返回调用日志留存天数, 非法值回退 7 天。
func LogRetentionDays() int {
	days, err := SettingGetInt(model.SettingKeyLogRetentionDays)
	if err != nil || days < 1 {
		return 7
	}
	if days > 3650 {
		return 3650
	}
	return days
}

// ArchiveDir 返回归档目录, 与数据库同级的 archives 子目录。
func ArchiveDir() string {
	return filepath.Join("data", "archives")
}

// ArchiveResult 一次归档与清理的结果。
type ArchiveResult struct {
	Cutoff       time.Time `json:"cutoff"`
	Archived     int       `json:"archived"`
	File         string    `json:"file"`
	RequestsDel  int64     `json:"requests_deleted"`
	AttemptsDel  int64     `json:"attempts_deleted"`
	BodyCleared  int64     `json:"body_cleared"`
	ArchiveError string    `json:"archive_error,omitempty"`
	Enabled      bool      `json:"archive_enabled"`
}

// LogLifecycleRun 执行一次日志生命周期处理: 先归档再清理超出留存期的记录。
// 运行中的请求不在清理范围内, 避免删除仍在写入的行。
func LogLifecycleRun(ctx context.Context) (ArchiveResult, error) {
	retention := LogRetentionDays()
	cutoff := time.Now().AddDate(0, 0, -retention)
	result := ArchiveResult{Cutoff: cutoff}
	archiveEnabled, err := SettingGetBool(model.SettingKeyLogArchiveEnabled)
	result.Enabled = err == nil && archiveEnabled

	// 正文在留存期满前保留: 留存期内仍可追溯原文, 满期后清空正文释放空间。
	bodyCutoff := cutoff
	bodyResult := db.GetDB().WithContext(ctx).Model(&model.RelayRequest{}).
		Where("started_at < ? AND (request_body <> '' OR response_body <> '')", bodyCutoff).
		Updates(map[string]interface{}{"request_body": "", "response_body": ""})
	if bodyResult.Error != nil {
		return result, fmt.Errorf("clear expired bodies: %w", bodyResult.Error)
	}
	result.BodyCleared = bodyResult.RowsAffected

	if result.Enabled {
		file, archived, err := archiveBefore(ctx, cutoff)
		result.File, result.Archived = file, archived
		if err != nil {
			// 归档失败不继续删除: 先保留数据, 保证不丢未归档记录。
			result.ArchiveError = err.Error()
			RaiseAlert(ctx, 0, "system", "archive_failed", "log archive failed: "+err.Error())
			return result, nil
		}
	}

	attempts := db.GetDB().WithContext(ctx).
		Where("request_id IN (SELECT id FROM relay_requests WHERE started_at < ?)", cutoff).
		Delete(&model.RelayRequestAttempt{})
	if attempts.Error != nil {
		return result, fmt.Errorf("delete expired attempts: %w", attempts.Error)
	}
	result.AttemptsDel = attempts.RowsAffected
	requests := db.GetDB().WithContext(ctx).Where("started_at < ?", cutoff).Delete(&model.RelayRequest{})
	if requests.Error != nil {
		return result, fmt.Errorf("delete expired requests: %w", requests.Error)
	}
	result.RequestsDel = requests.RowsAffected
	return result, nil
}

// archiveBefore 把超期记录导出为 NDJSON 归档文件, 返回文件路径与记录数。
// 同一天的归档文件按日期命名, 重复执行追加写入, 便于离线留档。
func archiveBefore(ctx context.Context, cutoff time.Time) (string, int, error) {
	rows := []model.RelayRequest{}
	if err := db.GetDB().WithContext(ctx).Where("started_at < ?", cutoff).Order("id ASC").Find(&rows).Error; err != nil {
		return "", 0, fmt.Errorf("load requests for archive: %w", err)
	}
	if len(rows) == 0 {
		return "", 0, nil
	}
	existing := []model.RelayRequestAttempt{}
	ids := make([]uint64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	if err := db.GetDB().WithContext(ctx).Where("request_id IN ?", ids).Order("request_id ASC, round ASC").Find(&existing).Error; err != nil {
		return "", 0, fmt.Errorf("load attempts for archive: %w", err)
	}
	attemptsByRequest := map[uint64][]model.RelayRequestAttempt{}
	for _, attempt := range existing {
		attemptsByRequest[attempt.RequestID] = append(attemptsByRequest[attempt.RequestID], attempt)
	}

	if err := os.MkdirAll(ArchiveDir(), 0o750); err != nil {
		return "", 0, fmt.Errorf("create archive dir: %w", err)
	}
	name := "relay-" + time.Now().Format("20060102") + ".ndjson"
	path := filepath.Join(ArchiveDir(), name)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return "", 0, fmt.Errorf("open archive file: %w", err)
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	for _, row := range rows {
		entry := map[string]any{"request": row, "attempts": attemptsByRequest[row.ID]}
		if err := encoder.Encode(entry); err != nil {
			return "", 0, fmt.Errorf("write archive entry: %w", err)
		}
	}
	return path, len(rows), nil
}

// ArchiveList 返回已有归档文件及其大小, 新的在前。
func ArchiveList() ([]map[string]any, error) {
	entries, err := os.ReadDir(ArchiveDir())
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

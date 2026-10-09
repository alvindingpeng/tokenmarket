package op

import (
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

var (
	ratePolicyMu    sync.RWMutex
	ratePolicyCache []model.RateLimitPolicy
)

// ratePolicyCacheReload 重建限流策略内存缓存; 策略写入后即刻刷新, 读取走缓存不触库。
func ratePolicyCacheReload() error {
	var rows []model.RateLimitPolicy
	if err := db.GetDB().Find(&rows).Error; err != nil {
		return err
	}
	ratePolicyMu.Lock()
	ratePolicyCache = rows
	ratePolicyMu.Unlock()
	return nil
}

// RatePolicyList 返回全部限流策略。
func RatePolicyList() []model.RateLimitPolicy {
	ratePolicyMu.RLock()
	defer ratePolicyMu.RUnlock()
	rows := make([]model.RateLimitPolicy, len(ratePolicyCache))
	copy(rows, ratePolicyCache)
	return rows
}

// RatePolicySave 新建或更新限流策略, 并刷新缓存。
func RatePolicySave(policy *model.RateLimitPolicy) error {
	if policy.ID == 0 {
		if err := db.GetDB().Create(policy).Error; err != nil {
			return err
		}
	} else {
		if err := db.GetDB().Save(policy).Error; err != nil {
			return err
		}
	}
	return ratePolicyCacheReload()
}

// RatePolicyDelete 删除限流策略, 并刷新缓存。
func RatePolicyDelete(id uint) error {
	if err := db.GetDB().Delete(&model.RateLimitPolicy{}, id).Error; err != nil {
		return err
	}
	return ratePolicyCacheReload()
}

// RatePolicyInit 启动时加载策略缓存。
func RatePolicyInit() error { return ratePolicyCacheReload() }

// RateLimitFor 兼容现有 RPM/TPM 调用方。
func RateLimitFor(scopeType string, scopeID int, modelName string) (rpm int64, tpm int64) {
	rpm, tpm, _ = RateLimitsFor(scopeType, scopeID, modelName)
	return
}

// RateLimitsFor 解析某个范围生效的 RPM/TPM/并发上限。
func RateLimitsFor(scopeType string, scopeID int, modelName string) (rpm int64, tpm int64, concurrent int64) {
	ratePolicyMu.RLock()
	defer ratePolicyMu.RUnlock()
	bestScore := -1
	for _, policy := range ratePolicyCache {
		if !policy.Enabled {
			continue
		}
		score := rateScopeScore(policy, scopeType, scopeID, modelName)
		if score > bestScore {
			bestScore = score
			rpm, tpm, concurrent = policy.RPM, policy.TPM, policy.Concurrent
		}
	}
	return rpm, tpm, concurrent
}

// RateScopeModels identifies independent counters to enforce. An all-model policy
// is a shared budget, not a copy for every model. A specific policy also applies
// when present; system defaults remain fallback per entity, not a platform cap.
func RateScopeModels(kind string, id int, name string) []string {
	ratePolicyMu.RLock()
	defer ratePolicyMu.RUnlock()
	shared, specific := false, false
	systemShared, systemSpecific := false, false
	for _, p := range ratePolicyCache {
		if !p.Enabled {
			continue
		}
		if p.ScopeType == kind && p.ScopeID == id {
			if p.ModelName == "" {
				shared = true
			}
			if name != "" && p.ModelName == name {
				specific = true
			}
		}
		if p.ScopeType == model.RateScopeSystem {
			if p.ModelName == "" {
				systemShared = true
			}
			if name != "" && p.ModelName == name {
				systemSpecific = true
			}
		}
	}
	var result []string
	if shared {
		result = append(result, "")
	}
	if specific {
		result = append(result, name)
	}
	if len(result) > 0 {
		return result
	}
	if systemSpecific {
		return []string{name}
	}
	if systemShared {
		return []string{""}
	}
	return []string{""}
}

// rateScopeScore 计算策略与查询范围的匹配度; 越大越具体, -1 表示不匹配。
func rateScopeScore(policy model.RateLimitPolicy, scopeType string, scopeID int, modelName string) int {
	if policy.ScopeType != scopeType {
		// 只有系统兜底可跨范围命中。
		if policy.ScopeType != model.RateScopeSystem {
			return -1
		}
		base := 0
		if policy.ModelName != "" {
			if policy.ModelName != modelName {
				return -1
			}
			base = 1
		}
		return base
	}
	if policy.ScopeID != scopeID {
		return -1
	}
	if policy.ModelName != "" {
		if policy.ModelName != modelName {
			return -1
		}
		return 4
	}
	return 3
}

// RateUsageAdd 聚合小时级限流用量; 触顶拒绝计入 rejected。
func RateUsageAdd(scopeType string, scopeID int, requests, inputTokens, outputTokens, rejected int64) {
	hour := time.Now().Truncate(time.Hour)
	row := model.RateLimitUsageHourly{ScopeType: scopeType, ScopeID: scopeID, Hour: hour}
	database := db.GetDB()
	var count int64
	if err := database.Model(&model.RateLimitUsageHourly{}).
		Where("scope_type = ? AND scope_id = ? AND hour = ?", scopeType, scopeID, hour).Count(&count).Error; err != nil {
		return
	}
	if count == 0 {
		row.Requests = requests
		row.InputTokens = inputTokens
		row.OutputTokens = outputTokens
		row.Rejected = rejected
		_ = database.Create(&row).Error
		return
	}
	_ = database.Model(&model.RateLimitUsageHourly{}).
		Where("scope_type = ? AND scope_id = ? AND hour = ?", scopeType, scopeID, hour).
		Updates(map[string]interface{}{
			"requests":      gorm.Expr("requests + ?", requests),
			"input_tokens":  gorm.Expr("input_tokens + ?", inputTokens),
			"output_tokens": gorm.Expr("output_tokens + ?", outputTokens),
			"rejected":      gorm.Expr("rejected + ?", rejected),
		}).Error
}

// RateUsageList 返回最近若干小时的限流用量聚合。
func RateUsageList(scopeType string, scopeID int, hours int) ([]model.RateLimitUsageHourly, error) {
	if hours <= 0 || hours > 168 {
		hours = 24
	}
	since := time.Now().Add(-time.Duration(hours) * time.Hour).Truncate(time.Hour)
	var rows []model.RateLimitUsageHourly
	query := db.GetDB().Where("hour >= ?", since)
	if scopeType != "" {
		query = query.Where("scope_type = ?", scopeType)
	}
	if scopeID != 0 {
		query = query.Where("scope_id = ?", scopeID)
	}
	if err := query.Order("hour DESC").Limit(500).Find(&rows).Error; err != nil {
		return nil, err
	}
	if rows == nil {
		rows = make([]model.RateLimitUsageHourly, 0)
	}
	return rows, nil
}

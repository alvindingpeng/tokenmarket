package op

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm/clause"
)

type taskCapabilityCacheState struct {
	mu     sync.RWMutex
	items  map[string]model.ModelTaskCapability
	byBare map[string][]model.ModelTaskCapability
}

var taskCapabilityCache taskCapabilityCacheState

func taskCapabilityKey(modelName, task string) string {
	return normalizeCapabilityKey(modelName) + "\x00" + task
}

// TaskCapabilityGet 按精确模型名与任务读取管理员登记的能力。未知能力不能从价格、
// 渠道或模型名推断，调用方必须把 !ok 当作“没有质量证据”处理。
func TaskCapabilityGet(modelName, task string) (model.ModelTaskCapability, bool) {
	modelName = normalizeCapabilityKey(modelName)
	task = strings.ToLower(strings.TrimSpace(task))
	taskCapabilityCache.mu.RLock()
	defer taskCapabilityCache.mu.RUnlock()
	if item, ok := taskCapabilityCache.items[taskCapabilityKey(modelName, task)]; ok {
		return item, true
	}
	// 渠道登记常用裸名，而管理员可能按 models.dev 的带厂商名称登记。只在去前缀后
	// 唯一命中时回落，避免把不同厂商的同名模型质量混在一起。
	_, bare := splitCapabilityKey(modelName)
	matches := make([]model.ModelTaskCapability, 0, 2)
	for _, item := range taskCapabilityCache.byBare[bare+"\x00"+task] {
		matches = append(matches, item)
	}
	if len(matches) != 1 {
		return model.ModelTaskCapability{}, false
	}
	return matches[0], true
}

// TaskCapabilityList 返回当前全部任务能力登记，按模型与任务排序。
func TaskCapabilityList() []model.ModelTaskCapability {
	taskCapabilityCache.mu.RLock()
	defer taskCapabilityCache.mu.RUnlock()
	items := make([]model.ModelTaskCapability, 0, len(taskCapabilityCache.items))
	for _, item := range taskCapabilityCache.items {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ModelName != items[j].ModelName {
			return items[i].ModelName < items[j].ModelName
		}
		return items[i].Task < items[j].Task
	})
	return items
}

// TaskCapabilitySave 保存一条管理员验证结果。score=0 同时表示明确不达标，因此
// verified=false 的记录仍会保留，供路由给出“能力未达标”而不是“未登记”的诊断。
func TaskCapabilitySave(item model.ModelTaskCapability, ctx context.Context) error {
	item.ModelName = normalizeCapabilityKey(item.ModelName)
	item.Task = strings.ToLower(strings.TrimSpace(item.Task))
	item.Notes = strings.TrimSpace(item.Notes)
	item.Source = model.CapabilitySourceManual
	if item.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	if !model.IsValidAutoRoutingTask(item.Task) {
		return fmt.Errorf("unsupported auto-routing task: %s", item.Task)
	}
	if item.Score < 0 || item.Score > 100 {
		return fmt.Errorf("score must be between 0 and 100")
	}
	if len(item.Notes) > 1024 {
		return fmt.Errorf("notes must not exceed 1024 characters")
	}
	now := time.Now()
	item.UpdatedAt = now
	if item.Verified {
		item.VerifiedAt = now
	} else {
		item.VerifiedAt = time.Time{}
	}
	if err := db.GetDB().WithContext(ctx).
		Clauses(clause.OnConflict{UpdateAll: true}).
		Create(&item).Error; err != nil {
		return fmt.Errorf("failed to save task capability: %w", err)
	}
	return taskCapabilityRefreshCache(ctx)
}

// TaskCapabilityDelete 删除模型的单任务登记。
func TaskCapabilityDelete(modelName, task string, ctx context.Context) error {
	modelName = normalizeCapabilityKey(modelName)
	task = strings.ToLower(strings.TrimSpace(task))
	if modelName == "" || !model.IsValidAutoRoutingTask(task) {
		return fmt.Errorf("invalid model name or task")
	}
	if err := db.GetDB().WithContext(ctx).
		Where("model_name = ? AND task = ?", modelName, task).
		Delete(&model.ModelTaskCapability{}).Error; err != nil {
		return err
	}
	return taskCapabilityRefreshCache(ctx)
}

// TaskCapabilityRefreshCache 从数据库重建进程内索引。启动时和每次增删改后调用。
func TaskCapabilityRefreshCache(ctx context.Context) error {
	return taskCapabilityRefreshCache(ctx)
}

func taskCapabilityRefreshCache(ctx context.Context) error {
	rows := []model.ModelTaskCapability{}
	if err := db.GetDB().WithContext(ctx).Find(&rows).Error; err != nil {
		return fmt.Errorf("failed to load task capabilities: %w", err)
	}
	index := make(map[string]model.ModelTaskCapability, len(rows))
	byBare := make(map[string][]model.ModelTaskCapability)
	for _, row := range rows {
		if row.ModelName == "" || !model.IsValidAutoRoutingTask(row.Task) {
			continue
		}
		index[taskCapabilityKey(row.ModelName, row.Task)] = row
		_, bare := splitCapabilityKey(row.ModelName)
		byBare[bare+"\x00"+row.Task] = append(byBare[bare+"\x00"+row.Task], row)
	}
	taskCapabilityCache.mu.Lock()
	taskCapabilityCache.items = index
	taskCapabilityCache.byBare = byBare
	taskCapabilityCache.mu.Unlock()
	return nil
}

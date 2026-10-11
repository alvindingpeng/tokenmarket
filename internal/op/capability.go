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

// capabilityIndex 是模型能力的内存索引: 键为精确模型名, 值为按厂商前缀索引的候选。
// 上游给的名字形如 openai/gpt-image-1, 而渠道里注册的常是 gpt-image-1, 故两套键都要能用。
type capabilityIndex struct {
	exact     map[string][]model.ModelCapability
	byVendor  map[string][]model.ModelCapability // vendor -> 该厂商下的条目
	byName    map[string][]model.ModelCapability // 去前缀后的裸名 -> 条目
	manual    map[string]model.ModelCapability   // 来源为 manual 的条目, 同步时原样保留。
	hasModels bool
}

var capabilityCache = struct {
	mu    sync.RWMutex
	index capabilityIndex
}{}

// CapabilityGet 按模型名查能力登记。
// 查询顺序: 精确匹配 -> 裸名匹配 -> 唯一候选的宽松匹配(名字里含该模型名或反之)。
// 命中多个不同能力时不猜: 宁可不预勾选, 也不要把文本模型错判成生图而走按张计费。
func CapabilityGet(name string) (model.ModelCapability, bool) {
	key := normalizeCapabilityKey(name)
	if key == "" {
		return model.ModelCapability{}, false
	}
	capabilityCache.mu.RLock()
	defer capabilityCache.mu.RUnlock()
	index := capabilityCache.index
	// 精确匹配可能命中多条(同一裸名在多个厂商下), 只有它们形态一致时才接受。
	if matched, ok := pickConsistent(index.exact[key]); ok {
		return matched, true
	}
	if strings.Contains(key, "/") {
		vendor, bare := splitCapabilityKey(key)
		if matched, ok := pickConsistent(filterCapability(index.byName[bare], vendor)); ok {
			return matched, true
		}
	}
	if matched, ok := pickConsistent(index.byName[key]); ok {
		return matched, true
	}
	if matched, ok := pickConsistent(fuzzyCapability(index, key)); ok {
		return matched, true
	}
	return model.ModelCapability{}, false
}

// CapabilityList 返回全部能力登记, 按名称定序, 供管理接口与界面展示。
func CapabilityList() []model.ModelCapability {
	capabilityCache.mu.RLock()
	defer capabilityCache.mu.RUnlock()
	all := make([]model.ModelCapability, 0, len(capabilityCache.index.exact))
	seen := make(map[string]struct{}, len(capabilityCache.index.byName))
	for _, items := range capabilityCache.index.byName {
		for _, item := range items {
			if _, ok := seen[item.Name]; ok {
				continue
			}
			seen[item.Name] = struct{}{}
			all = append(all, item)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	return all
}

// CapabilityCount 返回当前登记条数。
func CapabilityCount() int {
	capabilityCache.mu.RLock()
	defer capabilityCache.mu.RUnlock()
	return len(capabilityCache.index.byName)
}

// CapabilityReplaceFromModelsDev 用官方能力登记整体替换非人工条目。
// 人工补录(source=manual)的行始终保留: 官方没收录的小众供应商靠它们兜底, 不能被一次同步抹掉。
func CapabilityReplaceFromModelsDev(capabilities []model.ModelCapability, ctx context.Context) error {
	if len(capabilities) == 0 {
		return nil
	}
	now := time.Now()
	rows := make([]model.ModelCapability, 0, len(capabilities))
	for _, capability := range capabilities {
		name := normalizeCapabilityKey(capability.Name)
		if name == "" || capability.Empty() {
			continue
		}
		capability.Name = name
		capability.Source = model.CapabilitySourceModelsDev
		capability.UpdatedAt = now
		rows = append(rows, capability)
	}
	if len(rows) == 0 {
		return nil
	}
	// 去重: 同一模型在多个厂商下重复出现时以后写的为准(数据源内部已按名称合并)。
	deduped := make([]model.ModelCapability, 0, len(rows))
	seen := make(map[string]int, len(rows))
	for _, row := range rows {
		if at, ok := seen[row.Name]; ok {
			deduped[at] = row
			continue
		}
		seen[row.Name] = len(deduped)
		deduped = append(deduped, row)
	}
	conn := db.GetDB().WithContext(ctx)
	// 先删官方来源的旧行再整批写入: 官方下架的模型不应继续留在库里误导探测。
	if err := conn.Where("source = ?", model.CapabilitySourceModelsDev).Delete(&model.ModelCapability{}).Error; err != nil {
		return fmt.Errorf("failed to clear model capabilities: %w", err)
	}
	if err := conn.Clauses(clause.OnConflict{UpdateAll: true}).CreateInBatches(&deduped, 200).Error; err != nil {
		return fmt.Errorf("failed to save model capabilities: %w", err)
	}
	return capabilityRefreshCache(ctx)
}

// CapabilityDelete 删除一条能力登记, 供管理界面清理错误条目。
func CapabilityDelete(name string, ctx context.Context) error {
	key := normalizeCapabilityKey(name)
	if key == "" {
		return fmt.Errorf("model name is required")
	}
	if err := db.GetDB().WithContext(ctx).Where("name = ?", key).Delete(&model.ModelCapability{}).Error; err != nil {
		return err
	}
	return capabilityRefreshCache(ctx)
}

// CapabilitySaveManual 人工补录或覆盖一条能力登记, 来源固定记为 manual。
func CapabilitySaveManual(capability model.ModelCapability, ctx context.Context) error {
	name := normalizeCapabilityKey(capability.Name)
	if name == "" {
		return fmt.Errorf("model name is required")
	}
	capability.Name = name
	capability.Source = model.CapabilitySourceManual
	capability.UpdatedAt = time.Now()
	if err := db.GetDB().WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(&capability).Error; err != nil {
		return err
	}
	return capabilityRefreshCache(ctx)
}

// capabilityRefreshCache 从数据库重建能力索引。
func capabilityRefreshCache(ctx context.Context) error {
	rows := []model.ModelCapability{}
	if err := db.GetDB().WithContext(ctx).Find(&rows).Error; err != nil {
		return fmt.Errorf("failed to load model capabilities: %w", err)
	}
	index := capabilityIndex{
		exact:    make(map[string][]model.ModelCapability, len(rows)),
		byVendor: make(map[string][]model.ModelCapability, len(rows)),
		byName:   make(map[string][]model.ModelCapability, len(rows)),
		manual:   make(map[string]model.ModelCapability),
	}
	for _, row := range rows {
		if row.Empty() {
			continue
		}
		index.exact[row.Name] = append(index.exact[row.Name], row)
		_, bare := splitCapabilityKey(row.Name)
		index.byName[bare] = append(index.byName[bare], row)
		if vendor, _ := splitCapabilityKey(row.Name); vendor != "" {
			index.byVendor[vendor] = append(index.byVendor[vendor], row)
		}
		if row.Source == model.CapabilitySourceManual {
			index.manual[row.Name] = row
		}
	}
	index.hasModels = len(rows) > 0
	capabilityCache.mu.Lock()
	capabilityCache.index = index
	capabilityCache.mu.Unlock()
	return nil
}

// normalizeCapabilityKey 规范化模型名: 统一小写并去掉两端空白。
func normalizeCapabilityKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// splitCapabilityKey 拆出厂商前缀与裸名; 没有前缀时厂商为空串。
func splitCapabilityKey(key string) (string, string) {
	if at := strings.LastIndex(key, "/"); at >= 0 {
		return key[:at], key[at+1:]
	}
	return "", key
}

// pickConsistent 从多条候选里取形态一致的唯一结论:
// 全部候选同形态则返回第一条; 出现分歧(同一裸名在多家厂商下能力不同)时放弃, 交给下一级匹配。
func pickConsistent(items []model.ModelCapability) (model.ModelCapability, bool) {
	if len(items) == 0 {
		return model.ModelCapability{}, false
	}
	first := items[0].MediaKind()
	for _, item := range items[1:] {
		if item.MediaKind() != first {
			return model.ModelCapability{}, false
		}
	}
	return items[0], true
}

// filterCapability 只在指定厂商的候选里挑, 用于 modelID 带前缀的场景。
func filterCapability(items []model.ModelCapability, vendor string) []model.ModelCapability {
	if vendor == "" {
		return items
	}
	filtered := make([]model.ModelCapability, 0, len(items))
	for _, item := range items {
		if itemVendor, _ := splitCapabilityKey(item.Name); itemVendor == vendor {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

// fuzzyCapability 宽松匹配: 模型名互相包含且方向唯一时命中。
// 只作为最后手段, 候选之间形态不一致就放弃 —— 宁可让管理员手动勾选, 也不要错判计费形态。
func fuzzyCapability(index capabilityIndex, key string) []model.ModelCapability {
	bare := key
	if _, name := splitCapabilityKey(key); name != "" {
		bare = name
	}
	matches := make([]model.ModelCapability, 0, 2)
	for name, items := range index.byName {
		if name == bare || len(name) < 4 {
			continue
		}
		if strings.Contains(name, bare) || strings.Contains(bare, name) {
			matches = append(matches, items...)
			if len(matches) > 4 {
				return nil
			}
		}
	}
	return matches
}

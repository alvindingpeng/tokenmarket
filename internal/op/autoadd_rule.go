package op

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/utils/cache"
	"github.com/charmbracelet/log"
	"github.com/dlclark/regexp2"
	"gorm.io/gorm/clause"
)

// modelAutoAddRuleCache 是模型级自动添加条件的进程内副本, 键为 "用户主键|小写模型名"。
// 探测并入发生在后台任务里, 每轮都要对每个候选模型查一次条件; 走缓存而不是逐条查库,
// 免得一轮复查对同一个(用户, 模型)反复打 SQL。键用字符串拼接而非结构体:
// 缓存实现按 %v 打哈希键, 结构体键的字符串形式不可控, 拆开来最稳。
var modelAutoAddRuleCache = cache.New[string, model.ModelAutoAddRule](16)

// ruleCacheKey 拼缓存键。
func ruleCacheKey(userID uint, name string) string {
	return strconv.FormatUint(uint64(userID), 10) + "|" + strings.ToLower(strings.TrimSpace(name))
}

// AutoAddRuleList 返回某个用户设置过的全部模型级条件, 供模型页逐条展示与编辑。
func AutoAddRuleList(userID uint) []model.ModelAutoAddRule {
	rules := make([]model.ModelAutoAddRule, 0, modelAutoAddRuleCache.Len())
	for _, rule := range modelAutoAddRuleCache.GetAll() {
		if rule.UserID == userID {
			rules = append(rules, rule)
		}
	}
	return rules
}

// AutoAddRuleGet 取某个用户为某个模型设的条件; 未设置时返回零值与 false。
func AutoAddRuleGet(userID uint, name string) (model.ModelAutoAddRule, bool) {
	return modelAutoAddRuleCache.Get(ruleCacheKey(userID, name))
}

// AutoAddRuleSave 保存一条模型级条件, 返回归一后的结果(调用方回显与记审计都该用这一份)。
// 全空的条件会被删行: 一条什么都不限制的记录与「没有这条记录」行为完全一致,
// 留着只会在模型页堆一堆看不懂的空条目。
func AutoAddRuleSave(rule model.ModelAutoAddRule, ctx context.Context) (model.ModelAutoAddRule, error) {
	if rule.UserID == 0 {
		return rule, fmt.Errorf("user id is required")
	}
	if err := rule.Normalize(); err != nil {
		return rule, err
	}
	if rule.Empty() {
		return rule, AutoAddRuleDelete(rule.UserID, rule.Name, ctx)
	}
	if err := db.GetDB().WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).
		Create(&rule).Error; err != nil {
		return rule, fmt.Errorf("failed to save model auto-add rule: %w", err)
	}
	modelAutoAddRuleCache.Set(ruleCacheKey(rule.UserID, rule.Name), rule)
	return rule, nil
}

// AutoAddRuleDelete 删除一条模型级条件, 让该模型回到全局默认; 不存在时视为成功(幂等)。
func AutoAddRuleDelete(userID uint, name string, ctx context.Context) error {
	if userID == 0 {
		return fmt.Errorf("user id is required")
	}
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return fmt.Errorf("model name is required")
	}
	if err := db.GetDB().WithContext(ctx).
		Where("user_id = ? AND name = ?", userID, name).
		Delete(&model.ModelAutoAddRule{}).Error; err != nil {
		return fmt.Errorf("failed to delete model auto-add rule: %w", err)
	}
	modelAutoAddRuleCache.Del(ruleCacheKey(userID, name))
	return nil
}

// autoAddRuleRefreshCache 从数据库刷新模型级条件缓存, 启动时调用一次。
func autoAddRuleRefreshCache(ctx context.Context) error {
	var rules []model.ModelAutoAddRule
	if err := db.GetDB().WithContext(ctx).Find(&rules).Error; err != nil {
		return err
	}
	modelAutoAddRuleCache.Clear()
	for _, rule := range rules {
		modelAutoAddRuleCache.Set(ruleCacheKey(rule.UserID, rule.Name), rule)
	}
	return nil
}

// 模型级条件里「留空回落全局」的字段解析: 空串返回 (0, false) 表示没有覆盖, 由调用方取全局值。
// 解析失败同样按未设置处理并记日志: 保存入口已校验过, 走到这里只可能是历史脏数据,
// 让一条坏记录挡掉整轮自动添加不值得, 但必须留痕。
func ruleFloat(raw string) (float64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		log.Warnf("auto-add: invalid model rule number %q, falling back to global: %v", raw, err)
		return 0, false
	}
	return parsed, true
}

// ruleFloatOr 取模型级数值, 未设置时回落全局默认值。
func ruleFloatOr(raw string, global float64) float64 {
	if parsed, ok := ruleFloat(raw); ok {
		return parsed
	}
	return global
}

// ruleBoolOr 取模型级三态开关, 未设置时回落全局默认值。
// 非法值(历史脏数据)按未设置处理, 与 ruleFloat 同一策略。
func ruleBoolOr(raw string, global bool) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case model.AutoAddRuleModeOn:
		return true
	case model.AutoAddRuleModeOff:
		return false
	default:
		return global
	}
}

// autoAddRuleRegexCache 缓存已编译的模型级正则, 键为原始表达式。
// 正则编译是整个条件判定里最贵的一步, 而一轮探测会对成百个候选模型反复取同一条规则,
// 不缓存等于每判一个模型就重编译一次。
var autoAddRuleRegexCache = cache.New[string, *regexp2.Regexp](16)

// compileRuleRegex 编译模型级正则; 非法表达式返回 nil 并记日志。
// 保存入口已校验过, 这里失败只可能是历史脏数据或写库绕过接口, 不该让自动添加整个失败。
func compileRuleRegex(pattern, field string) *regexp2.Regexp {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil
	}
	if compiled, ok := autoAddRuleRegexCache.Get(pattern); ok {
		return compiled
	}
	compiled, err := regexp2.Compile(pattern, regexp2.ECMAScript)
	if err != nil {
		log.Warnf("auto-add: invalid %s regex %q in model rule, ignoring: %v", field, pattern, err)
		return nil
	}
	autoAddRuleRegexCache.Set(pattern, compiled)
	return compiled
}

package op

import (
	"github.com/alvindingpeng/switchyard/internal/model"
)

// ResetProcessCachesForTest 清空全部"活在新库上、属于旧库"的进程级缓存。
//
// 这些缓存都是包级全局变量, 生命周期跟着进程而不是跟着数据库。测试每换一次库,
// 它们就变成脏数据。不清理的后果不是"偶尔报个错", 而是**测试信号整体不可信**:
// 默认顺序下全绿, -shuffle 下随机红, 于是没人再拿它当回归保护。
//
// 覆盖清单(按泄漏后表现分组):
//   - userCache / userNameIndex   → 撞"用户名已存在", 或 UserGetByID 读到旧库账号;
//   - settingCache                → 新用例拿到别人的开关(限流、计费模式);
//   - ratePolicyCache             → UserQuotaUsage 的 source 从 tier 变 policy;
//   - channelCache/Key/Model/Grant → 旧库的 DisabledUntil(含额度暂停)带进新库,
//     表现为 "channel N quota is temporarily exhausted" —— 这个最难查,
//     因为报错说的是"额度", 而真正的原因是上一个用例留下的渠道快照;
//   - groupCache / groupNameIndex → 旧分组与名字索引;
//   - llmModelCache               → 旧模型价格;
//   - apiKeyCache/FingerprintMap/statsAPIKeyCache → 旧密钥与其统计;
//   - capabilityCache             → 旧模型能力索引;
//   - 统计内存累计(map)           → 旧库的按天/按小时汇总;
//   - 汇率快照                    → 折算结果对不上期望值。
//
// 导出给其它包的测试用(跨包测试无法访问本包未导出的标识符)。
func ResetProcessCachesForTest() { resetProcessCaches() }

func resetProcessCaches() {
	userCache.Clear()
	userNameIndex.Clear()
	settingCache.Clear()

	ratePolicyMu.Lock()
	ratePolicyCache = nil
	ratePolicyMu.Unlock()

	channelCache.Clear()
	channelKeyCache.Clear()
	channelModelCache.Clear()
	channelGrantCache.Clear()

	groupCache.Clear()
	groupNameIndex.Clear()

	llmModelCache.Clear()
	apiKeyCache.Clear()
	apiKeyFingerprintMap.Clear()
	statsAPIKeyCache.Clear()

	modelAutoAddRuleCache.Clear()
	autoAddRuleRegexCache.Clear()

	capabilityCache.mu.Lock()
	capabilityCache.index = capabilityIndex{}
	capabilityCache.mu.Unlock()
	taskCapabilityCache.mu.Lock()
	taskCapabilityCache.items = make(map[string]model.ModelTaskCapability)
	taskCapabilityCache.byBare = make(map[string][]model.ModelTaskCapability)
	taskCapabilityCache.mu.Unlock()

	// 三个 map 类缓存各有配对锁与 dirty 标记。dirty 必须一起清:
	// 只清数据不减 dirty 集合, 下一次落库会按旧 dirty 去读已清空的 map,
	// 读到零值后把**空值写回库** —— 那是静默丢统计, 比不清理更糟。
	statsHourlyCacheLock.Lock()
	statsHourlyCache = make(map[hourlyKey]model.StatsHourly)
	hourlyCacheDate = ""
	statsHourlyCacheLock.Unlock()

	channelDailyStatsLock.Lock()
	channelDailyStatsCache = make(map[channelDailyKey]model.StatsChannelDaily)
	channelDailyStatsDirty = make(map[channelDailyKey]struct{})
	channelDailyStatsLock.Unlock()

	channelModelDailyStatsLock.Lock()
	channelModelDailyStatsCache = make(map[channelModelDailyKey]model.StatsChannelModelDaily)
	channelModelDailyStatsDirty = make(map[channelModelDailyKey]struct{})
	channelModelDailyStatsLock.Unlock()

	FxRateCacheReset()
	fxLastSource.Store(fxRateSourceName)
}

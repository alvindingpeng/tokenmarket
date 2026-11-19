package relay

import "github.com/alvindingpeng/switchyard/internal/model"

// 冷启动探索配额: 让还没有观测的成员拿到第一批样本。
//
// 为什么必须有它: score 模式把延迟与成功率在候选集内做最小-最大归一化, 冷成员只能按中性 0.5 参与,
// 分数因此被结构性封顶 —— 即使价格维度满分, 总分也只有 priceW*1 + (latW+succW)*0.5。
// 只要组里存在一个"耗时与成功率都还行"的老成员, 新成员永远排不到第一; 拿不到样本就永远补不齐评分,
// 补不齐评分就永远拿不到样本, 于是 无样本 → 排不上 → 无样本 自锁。延迟与成功率模式同理:
// 两者只在"有实测"的成员之间比较, 冷成员连比较资格都没有。
//
// 生产实证: 分组 cheap(score) 新增的渠道 1032(api.95znai.com) 用户价最低, 成员还排在优先级第一,
// 上线一周零调用; 每个请求的选路轨迹都是策略选路命中同一台老渠道, 该成员至今没有任何观测。
//
// 为什么是"配额"而不是直接把冷成员排到前面:
//   - 不伪造分数: 冷成员的分数照旧按中性值算, 配置页显示的分数与选路口径仍然一致,
//     额外给的只是一小部分确定性的流量;
//   - 份额有界: 每 exploreQuotaEvery 次选路最多放行一次, 与流量大小无关, 也可复现;
//   - 总量有界: 攒够 coldStartSamples 个样本后不再享受配额, 回到与老成员同一套评分公平竞争。
//     故一个新成员总共只会拿走几十个试探请求, 不会变成长期的流量税。
const (
	// exploreQuotaEvery 是探索节奏: 每这么多次选路至多放行一次冷启动探索(约 1/8 流量)。
	exploreQuotaEvery = 8
	// coldStartSamples 是冷启动的样本上限: 达到后不再享受探索配额。
	// 与评分用的 EMA 窗口(默认 30)同量级, 足以让延迟与成功率离开中性值。
	coldStartSamples = 20
)

// exploreGroupMode 判断该模式是否需要冷启动探索: 只有"按实测成绩择优"的模式才会让
// 缺少实测的成员结构性出局。价格优先天然选中最便宜的成员, 故障转移按优先级,
// 随机按均匀抽样, 都不存在这个问题。
func exploreGroupMode(mode model.GroupMode) bool {
	switch mode {
	case model.GroupModeScore, model.GroupModeLatency, model.GroupModeSuccess:
		return true
	default:
		return false
	}
}

// memberSamples 返回成员的观测样本数, 口径与评分完全一致(渠道模型全局指标优先, 回落本分组观测)。
func memberSamples(group model.Group, item model.GroupItem) int {
	_, _, samples := memberScopeMetric(group, item)
	return samples
}

// needsExploration 判断成员是否还需要探索流量: 观测样本不足以参与评分比较。
// 不额外考察成功率: 早期一两条样本本就是噪声; 真正失败的成员会被打分折减并被冷却移出候选,
// 冷却已经把"坏渠道长期占用配额"这条路堵住了。
func needsExploration(group model.Group, item model.GroupItem) bool {
	return memberSamples(group, item) < coldStartSamples
}

// explorationCandidate 返回可作为探索目标的成员: 仍缺观测, 且没有冷却条目。
// 带冷却条目的成员只能经恢复探测的单飞通道回池, 若被探索直接选中就绕过了探测记账,
// 一个尚未证明恢复的成员会因此拿到正常流量, 破坏"同一分组同时只探测一个成员"的语义。
func explorationCandidate(route *RouteState, item model.GroupItem) bool {
	if _, cooling := route.Cooldowns[item.ID]; cooling {
		return false
	}
	return true
}

// pickExplorationItem 在候选里挑本轮承接探索的成员, 没有可探索成员时返回零值(成员 ID 为 0)。
// 优先选观测最少的成员, 并列时取候选顺序(优先级)靠前者; 每探索一次它就多一条观测,
// 于是多个新成员自然轮流, 不会出现某个成员独占探索流量。
//
// 冷却中的成员必须排除: 它们只能走恢复探测那条单飞通道, 若被探索直接选中就绕过了探测记账,
// 一个尚未证明恢复的成员会因此拿到正常流量。
func pickExplorationItem(group model.Group, candidates []model.GroupItem, route *RouteState) model.GroupItem {
	var target model.GroupItem
	least := 0
	for _, item := range candidates {
		if !needsExploration(group, item) || !explorationCandidate(route, item) {
			continue
		}
		samples := memberSamples(group, item)
		if target.ID == 0 || samples < least {
			target, least = item, samples
		}
	}
	return target
}

// coldStartExplore 判断本轮是否该把一个尚缺观测的成员顶上去, 而不是按评分择优; 命中时返回该成员。
//
// 保持评分结果不变的三种情况:
//   - 该模式不按实测成绩择优, 不存在结构性饿死;
//   - 候选不足两个: 没有择优可言, 也就不需要额外配额;
//   - 评分选中的成员本身还没攒够样本: 正常选路已经照顾到它, 不必再插一次。
//
// 调用方必须持有 routeMu: 本函数会推进分组的探索节奏并记下本轮是否由探索决定。
func coldStartExplore(group model.Group, candidates []model.GroupItem, chosen model.GroupItem, route *RouteState) (model.GroupItem, bool) {
	if !exploreGroupMode(group.Mode) || len(candidates) < 2 || chosen.ID == 0 {
		return model.GroupItem{}, false
	}
	if needsExploration(group, chosen) {
		return model.GroupItem{}, false
	}
	route.exploreCounter++
	if route.exploreCounter%exploreQuotaEvery != 0 {
		return model.GroupItem{}, false
	}
	target := pickExplorationItem(group, candidates, route)
	if target.ID == 0 {
		return model.GroupItem{}, false
	}
	route.exploreItemID = target.ID
	return target, true
}

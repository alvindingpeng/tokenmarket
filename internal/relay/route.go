package relay

import (
	"maps"
	"math"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
)

// RouteState 是一个分组的进程内路由状态; 跨该分组的全部请求共享。
// 同时作为路由流的消息形状与分组读取响应中的 runtime 字段: 冷却, 探测与亲和都是本包路由算法的概念,
// 故状态形状由本包定义, 分组的持久化配置不含它; 内部标志未导出, 不会随消息出到 JSON。
// 两种模式共用 CurrentItemID: 手动模式下即人工指定的成员, 故障转移模式下由路由决定,
// 前端由此只读这一个字段即可知道当前承载请求的成员, 无需再按模式分支。
type RouteState struct {
	GroupID       int           `json:"group_id"`        // 状态所属的分组 ID, 供状态流按分组定位。
	CurrentItemID int           `json:"current_item_id"` // 当前承载请求的成员 ID, 0 表示尚未建立路由或未人工指定。
	ProbeItemID   int           `json:"probe_item_id"`   // 当前占用恢复探测的成员 ID, 同一分组同时只允许一个成员被探测; 手动模式恒为 0。
	AffinityUntil int64         `json:"affinity_until"`  // 当前路由的亲和截止 Unix 毫秒时间, 0 表示无亲和; 手动模式恒为 0。
	Cooldowns     map[int]int64 `json:"cooldowns"`       // 失败成员 ID 对应的冷却截止 Unix 毫秒时间, 已到期的条目由前端按当前时间忽略。

	affinityArmed bool // 当前路由下一次成功后是否开始亲和, 仅故障切换后为真。
}

const routeStreamBuffer = 16 // 单个路由流连接的非阻塞消息缓冲容量。

var (
	routeMu      sync.Mutex                           // routeMu 保护全部分组路由状态。
	routes       = make(map[int]*RouteState)          // routes 按分组 ID 保存路由状态。
	routeStreams = make(map[chan RouteState]struct{}) // 全部路由 SSE 连接。
)

// RouteStateOf 返回分组当前的实时路由状态, 供读取接口随分组一并返回。
// 手动模式没有进程内路由: 当前成员即人工指定的成员, 冷却与亲和均不适用, 故直接由分组配置得出。
func RouteStateOf(group model.Group) RouteState {
	if group.Mode == model.GroupModeManual {
		return RouteState{
			GroupID:       group.ID,
			CurrentItemID: group.ActiveItemID,
			Cooldowns:     map[int]int64{},
		}
	}

	routeMu.Lock()
	defer routeMu.Unlock()

	route := routes[group.ID]
	if route == nil {
		return RouteState{GroupID: group.ID, Cooldowns: map[int]int64{}}
	}
	state := *route
	state.Cooldowns = maps.Clone(route.Cooldowns)
	return state
}

// ResetRouteState 丢弃分组的进程内路由状态, 用于分组切换选择模式或被删除。
// 不丢弃的话冷却与亲和会在 failover 切到 manual 再切回来之后复活并继续影响选路, 分组删除后其状态也会永久残留。
func ResetRouteState(groupID int) {
	routeMu.Lock()
	defer routeMu.Unlock()

	delete(routes, groupID)
}

// pickGroupItem 按分组模式选择本轮目标成员, 没有可用成员时返回零值; group.Items 已按 Priority 升序排列。
// 手动模式固定人工成员; 其余模式为动态选路, 共用冷却, 探测与亲和机制:
// 先在未冷却成员中按模式策略择优, 优先级高于全部候选的冷却到期成员获得一个探测请求。
// 渠道是否可用不在此判断: 渠道禁用或缺少密钥由调用方发现并作为一轮失败上报, 该成员随即进入冷却而在后续轮次被跳过。
func pickGroupItem(group model.Group) model.GroupItem {
	if group.Mode == model.GroupModeManual {
		for _, item := range group.Items {
			if item.ID == group.ActiveItemID && item.Available && !upstreamItemLimited(item) {
				return item
			}
		}
		return model.GroupItem{}
	}

	routeMu.Lock()
	defer routeMu.Unlock()

	route := groupRouteLocked(group)
	now := time.Now().UnixMilli()
	if route.AffinityUntil <= now {
		route.AffinityUntil = 0
	}

	// 亲和期内沿用当前成员, 不提前探测已恢复的高优先级成员。
	if route.CurrentItemID != 0 && route.AffinityUntil > now {
		current := itemOf(group, route.CurrentItemID)
		if current.Available && !upstreamItemLimited(current) {
			return current
		}
	}

	// 按优先级扫描: 未冷却成员进入候选, 首个冷却到期的成员登记为探测候选。
	candidates := make([]model.GroupItem, 0, len(group.Items))
	var probeCandidate *model.GroupItem
	for i := range group.Items {
		item := &group.Items[i]
		// 上游限流触顶的成员直接出局: 与冷却不同, 不计失败率也不设恢复惩罚。
		if !item.Available || upstreamItemLimited(*item) {
			continue
		}
		deadline, cooling := route.Cooldowns[item.ID]
		if cooling && deadline > now {
			continue
		}
		if cooling {
			if probeCandidate == nil {
				probeCandidate = item
			}
			continue
		}
		candidates = append(candidates, *item)
	}

	// 冷却到期的成员优先级高于全部候选时放行一个探测请求(与故障转移原语义一致);
	// 没有任何候选时也探测, 避免全组冷却时请求永久等待。探测占用被其它请求持有时跳过。
	if probeCandidate != nil && route.ProbeItemID == 0 &&
		(len(candidates) == 0 || probeCandidate.Priority < candidates[0].Priority) {
		route.ProbeItemID = probeCandidate.ID
		publishRouteLocked(route)
		return *probeCandidate
	}

	if len(candidates) == 0 {
		return model.GroupItem{}
	}

	chosen := selectGroupItem(group, candidates)
	route.CurrentItemID = chosen.ID
	publishRouteLocked(route)
	return chosen
}

// routeExplanation records observable constraints at selection time; admission
// remains authoritative because concurrent callers may consume capacity afterwards.
func routeExplanation(group model.Group, chosen model.GroupItem) model.RelayRouteDecision {
	state := RouteStateOf(group)
	now := time.Now().UnixMilli()
	reason := "strategy_" + string(group.Mode)
	if state.ProbeItemID == chosen.ID && chosen.ID != 0 {
		reason = "recovery_probe"
	}
	if state.AffinityUntil > now && state.CurrentItemID == chosen.ID && chosen.ID != 0 {
		reason = "affinity"
	}
	result := model.RelayRouteDecision{Mode: string(group.Mode), Phase: "selected", Reason: reason}
	for _, item := range group.Items {
		why := "eligible"
		switch {
		case !item.Available:
			why = "unavailable"
		case upstreamItemLimited(item):
			why = "rate_or_concurrency_limit"
		case group.Mode == model.GroupModeManual && item.ID != group.ActiveItemID:
			why = "not_manual_target"
		case state.Cooldowns[item.ID] > now:
			why = "cooldown"
		case item.ID == chosen.ID:
			why = "selected"
		}
		result.Candidates = append(result.Candidates, model.RelayRouteCandidate{ItemID: item.ID, ChannelCode: item.ChannelName, Model: item.ModelName, Reason: why})
	}
	return result
}

// selectGroupItem 在未冷却候选中按模式策略选出本轮成员; candidates 已按优先级升序。
// 故障转移维持优先级顺序; 其余动态策略在候选集内按各自维度择优, 平局回落优先级。
func selectGroupItem(group model.Group, candidates []model.GroupItem) model.GroupItem {
	switch group.Mode {
	case model.GroupModePrice:
		best, bestPrice := candidates[0], memberPrice(group, candidates[0])
		for _, item := range candidates[1:] {
			if p := memberPrice(group, item); p < bestPrice {
				best, bestPrice = item, p
			}
		}
		return best
	case model.GroupModeLatency:
		best := candidates[0]
		bestWait, bestKnown := memberWait(group, candidates[0])
		for _, item := range candidates[1:] {
			w, known := memberWait(group, item)
			// 有实测耗时者优先于冷成员; 同为冷成员时按优先级保持原顺序。
			if known && (!bestKnown || w < bestWait) {
				best, bestWait, bestKnown = item, w, true
			}
		}
		return best
	case model.GroupModeSuccess:
		best := candidates[0]
		bestRate, bestKnown := memberRate(group, candidates[0])
		for _, item := range candidates[1:] {
			r, known := memberRate(group, item)
			if known && (!bestKnown || r > bestRate) {
				best, bestRate, bestKnown = item, r, true
			}
		}
		return best
	case model.GroupModeScore:
		return selectByScore(group, candidates)
	case model.GroupModeRandom:
		return candidates[rand.IntN(len(candidates))]
	default: // 故障转移及未知模式: 按优先级取第一个。
		return candidates[0]
	}
}

// memberPrice 返回成员在配置口径下的用户价(每百万 token)。
func memberPrice(group model.Group, item model.GroupItem) float64 {
	price := item.UserPrice
	switch group.RelayConfig.PriceMetric {
	case model.PriceMetricInput:
		return price.Input
	case model.PriceMetricOutput:
		return price.Output
	default: // blended: 读写均值。
		return (price.Input + price.Output) / 2
	}
}

// memberWait 返回成员用于排名的耗时(毫秒)。
// 指标按上游渠道模型全局聚合, 使同一模型的成绩在任意分组与用户下一致。
// 冷成员(无观测)返回中性中性值而非 0: 若把冷成员当最优, 一个尚无成绩的成员会无条件压过
// 有真实成绩的成员; 中性值只在其它维度胜出时才可能被选中, 兼顾新成员试用与老成员降级。
func memberWait(group model.Group, item model.GroupItem) (float64, bool) {
	waitMs, _, samples := memberScopeMetric(group, item)
	if samples == 0 {
		return 0, false
	}
	return waitMs, true
}

// memberRate 返回成员用于排名的成功率; ok 为 false 表示冷成员(无观测)。
func memberRate(group model.Group, item model.GroupItem) (float64, bool) {
	_, success, samples := memberScopeMetric(group, item)
	if samples == 0 {
		return 0, false
	}
	return success, true
}

// memberScopeMetric 取成员的评分指标: 优先用渠道模型全局指标, 缺失时回落本分组观测。
// 全局指标冷启动时用本分组已有观测播种, 保证重启后评分仍从历史成绩起步而非全部归零。
func memberScopeMetric(group model.Group, item model.GroupItem) (waitMs float64, success float64, samples int) {
	groupWait, groupSuccess, groupSamples := memberMetricOf(group.ID, item.ID)
	if item.ChannelModelID != 0 {
		waitMs, success, samples = modelMetricOf(item.ChannelModelID)
		if samples > 0 {
			return waitMs, success, samples
		}
		if groupSamples > 0 {
			seedModelMetric(item.ChannelModelID, groupWait, groupSuccess, groupSamples)
			return groupWait, groupSuccess, groupSamples
		}
	}
	return groupWait, groupSuccess, groupSamples
}

// selectByScore 综合评分: 价格(低好), 延迟(低好)与成功率(高好)各自在候选集内归一化后加权求和。
// 权重为相对值, 总和为分母; 冷成员在延迟与成功率维度按最优值参与, 让新成员先获得一次试用。
func selectByScore(group model.Group, candidates []model.GroupItem) model.GroupItem {
	config := group.RelayConfig
	priceWeight, latencyWeight, successWeight := effectiveScoreWeights(config)
	weightSum := float64(priceWeight + latencyWeight + successWeight)
	if weightSum <= 0 {
		return candidates[0]
	}

	prices := make([]float64, len(candidates))
	waits := make([]float64, len(candidates))
	rates := make([]float64, len(candidates))
	known := make([]bool, len(candidates))
	for i, item := range candidates {
		prices[i] = memberPrice(group, item)
		waits[i], known[i] = memberWait(group, item)
		rates[i], _ = memberRate(group, item)
	}

	// 可靠性优先: 成功率低于下限的成员无条件排在全部达标成员之后。
	// 价格是静态属性, 若不加此护栏, 最便宜的成员会因价格权重长期固守首位, 即便它频繁失败。
	floor := effectiveReliabilityFloor(config)
	best, bestScore, bestHealthy := candidates[0], math.Inf(-1), false
	for i, item := range candidates {
		// 冷成员在成功率维度按中性值参与, 不因无样本被判为不健康。
		healthy := !known[i] || rates[i] >= floor
		score := memberScore(priceWeight, latencyWeight, successWeight, i, prices, waits, rates, known)
		if healthy && !bestHealthy {
			best, bestScore, bestHealthy = item, score, true
			continue
		}
		if healthy != bestHealthy {
			continue
		}
		if score > bestScore {
			best, bestScore = item, score
		}
	}
	return best
}

// reliabilityFloorDefault 是成员可靠性下限的内置兜底: 成功率(EMA)低于该值视为不健康成员。
const reliabilityFloorDefault = 0.7

// effectiveReliabilityFloor 解析可靠性下限: 分组自定义优先, 否则用系统设置, 均缺失回落内置值。
func effectiveReliabilityFloor(config model.GroupRelayConfig) float64 {
	if config.ReliabilityFloor > 0 {
		return float64(config.ReliabilityFloor) / 100
	}
	if system, err := op.SettingGetInt(model.SettingKeyScoreReliabilityFloor); err == nil && system > 0 {
		return float64(system) / 100
	}
	return reliabilityFloorDefault
}

// 综合评分的内置兜底配比: 系统设置缺失且分组未自定义时使用。
const (
	defaultScorePriceWeight   = 40
	defaultScoreLatencyWeight = 30
	defaultScoreSuccessWeight = 30
)

// effectiveScoreWeights 解析综合评分的有效权重。
// 分组内某维度权重为 0 表示未自定义, 按系统默认设置(设置页"综合评分默认参数")执行;
// 分组自定义的维度(>0)保持不变。分组与系统都未给出有效配比时回落内置 40/30/30。
func effectiveScoreWeights(config model.GroupRelayConfig) (int, int, int) {
	priceWeight, latencyWeight, successWeight := config.ScorePriceWeight, config.ScoreLatencyWeight, config.ScoreSuccessWeight
	if priceWeight <= 0 {
		if system, err := op.SettingGetInt(model.SettingKeyScorePriceWeight); err == nil && system > 0 {
			priceWeight = system
		}
	}
	if latencyWeight <= 0 {
		if system, err := op.SettingGetInt(model.SettingKeyScoreLatencyWeight); err == nil && system > 0 {
			latencyWeight = system
		}
	}
	if successWeight <= 0 {
		if system, err := op.SettingGetInt(model.SettingKeyScoreSuccessWeight); err == nil && system > 0 {
			successWeight = system
		}
	}
	if priceWeight <= 0 && latencyWeight <= 0 && successWeight <= 0 {
		return defaultScorePriceWeight, defaultScoreLatencyWeight, defaultScoreSuccessWeight
	}
	return priceWeight, latencyWeight, successWeight
}

// memberScore 计算候选集中第 i 个成员的综合评分; 各维度在候选集内最小-最大归一化, 区间退化时并列。
// known 标记该成员是否有实测数据: 冷成员的延迟与成功率按中性值 0.5 参与,
// 既不因无样本被当成最优而抢占首位, 也不因无样本被误判为最差。
func memberScore(priceWeight, latencyWeight, successWeight, i int, prices, waits, rates []float64, known []bool) float64 {
	priceScore := normalizedDim(prices, i, false) // 价格低者得分高。
	latScore, succScore := 0.5, 0.5
	if known[i] {
		latScore = normalizedDim(waits, i, false) // 延迟低者得分高。
		succScore = normalizedDim(rates, i, true) // 成功率高者得分高。
	}
	return (float64(priceWeight)*priceScore +
		float64(latencyWeight)*latScore +
		float64(successWeight)*succScore) /
		float64(priceWeight+latencyWeight+successWeight)
}

// normalizedDim 把维度值归一到 0..1; higherBetter 为假时反向; 全体同值时并列返回 1。
func normalizedDim(values []float64, i int, higherBetter bool) float64 {
	minV, maxV := values[0], values[0]
	for _, v := range values {
		minV = min(minV, v)
		maxV = max(maxV, v)
	}
	if maxV == minV {
		return 1
	}
	norm := (values[i] - minV) / (maxV - minV)
	if higherBetter {
		return norm
	}
	return 1 - norm
}

// recordRouteSuccess 上报一轮成功: 结束该成员的冷却与探测占用, 并在故障切换后按配置开始亲和。
func recordRouteSuccess(group model.Group, itemID int) {
	if group.Mode == model.GroupModeManual {
		return
	}

	routeMu.Lock()
	defer routeMu.Unlock()

	route := routes[group.ID]
	if route == nil {
		return
	}
	now := time.Now().UnixMilli()
	changed := false

	// 探测成功说明该成员已恢复, 解除冷却; 若当前路由不在亲和期内则立即切回该成员。
	if route.ProbeItemID == itemID {
		route.ProbeItemID = 0
		delete(route.Cooldowns, itemID)
		if route.CurrentItemID == 0 || route.AffinityUntil <= now {
			route.CurrentItemID = itemID
			route.AffinityUntil = 0
		}
		changed = true
	}
	// 亲和只在故障切换后的首次成功时开始, 使请求在一段时间内稳定留在备用成员上。
	if route.CurrentItemID == itemID && route.affinityArmed {
		route.affinityArmed = false
		if group.RelayConfig.MemberAffinitySeconds > 0 {
			route.AffinityUntil = now + int64(group.RelayConfig.MemberAffinitySeconds)*1000
			changed = true
		}
	}
	if changed {
		publishRouteLocked(route)
	}
}

// recordRouteFailure 上报一轮失败: 达到配置的总尝试次数后将该成员打入冷却并让出当前路由, 返回是否已冷却。
// failures 为该成员在本请求内包含首次请求的连续失败次数, 由调用方累计。
func recordRouteFailure(group model.Group, itemID, failures int) bool {
	if group.Mode == model.GroupModeManual {
		return false
	}

	routeMu.Lock()
	defer routeMu.Unlock()

	route := routes[group.ID]
	if route == nil {
		return false
	}
	// 探测请求只有一次机会, 常规成员达到配置的总尝试次数后进入冷却。
	if route.ProbeItemID != itemID && failures < group.RelayConfig.MemberMaxAttempts {
		return false
	}

	now := time.Now().UnixMilli()
	route.Cooldowns[itemID] = now + int64(group.RelayConfig.MemberCooldownSeconds)*1000
	if route.ProbeItemID == itemID {
		route.ProbeItemID = 0
	}
	// 当前路由失败才需要下一个成员开始亲和; 独立探测失败不影响当前路由。
	if route.CurrentItemID == itemID {
		route.CurrentItemID = 0
		route.AffinityUntil = 0
		route.affinityArmed = true
	}
	publishRouteLocked(route)
	return true
}

// releaseRouteProbe 归还未产生成败结论的探测占用, 用于请求被人工中止或客户端断开。
func releaseRouteProbe(group model.Group, itemID int) {
	routeMu.Lock()
	defer routeMu.Unlock()

	if route := routes[group.ID]; route != nil && route.ProbeItemID == itemID {
		route.ProbeItemID = 0
		publishRouteLocked(route)
	}
}

// hasOtherEligible 检查同分组内除了 excludeItemID 之外是否还有可用成员(未冷却、未限流、可用)。
// 用于决定当前成员失败后是否立即换组, 还是等待重试当前成员。
func hasOtherEligible(group model.Group, excludeItemID int) bool {
	routeMu.Lock()
	route := routes[group.ID]
	routeMu.Unlock()

	now := time.Now().UnixMilli()
	for _, item := range group.Items {
		if item.ID == excludeItemID {
			continue
		}
		if !item.Available || upstreamItemLimited(item) {
			continue
		}
		if route != nil {
			if deadline, cooling := route.Cooldowns[item.ID]; cooling && deadline > now {
				continue
			}
		}
		return true
	}
	return false
}

// upstreamItemLimited 判断成员的任一上游维度当前窗口是否已限流触顶。
func upstreamItemLimited(item model.GroupItem) bool {
	for _, scope := range upstreamItemScopes(item) {
		if limiter.Blocked(scope) {
			return true
		}
	}
	return false
}

// groupRouteLocked 取出分组路由状态并清理已删除成员的残留; 调用方必须持有锁。
func groupRouteLocked(group model.Group) *RouteState {
	route := routes[group.ID]
	if route == nil {
		route = &RouteState{GroupID: group.ID, Cooldowns: make(map[int]int64)}
		routes[group.ID] = route
	}
	items := make(map[int]bool, len(group.Items))
	for _, item := range group.Items {
		items[item.ID] = true
	}
	for itemID := range route.Cooldowns {
		if !items[itemID] {
			delete(route.Cooldowns, itemID)
		}
	}
	if route.ProbeItemID != 0 && !items[route.ProbeItemID] {
		route.ProbeItemID = 0
	}
	if route.CurrentItemID != 0 && !items[route.CurrentItemID] {
		route.CurrentItemID = 0
		route.AffinityUntil = 0
		route.affinityArmed = false
	}
	return route
}

// itemOf 返回分组内指定 ID 的成员, 不存在时返回零值。
func itemOf(group model.Group, itemID int) model.GroupItem {
	for _, item := range group.Items {
		if item.ID == itemID {
			return item
		}
	}
	return model.GroupItem{}
}

// publishRouteLocked 非阻塞发布路由状态, 连接拥塞时关闭它并交给客户端重连获取全量快照; 冷却表按值复制以免前端读到后续变更; 调用方必须持有锁。
func publishRouteLocked(route *RouteState) {
	markRouteDirty(route.GroupID) // 路由状态变更后由后台批量落库, 重启可恢复。
	message := *route
	message.Cooldowns = maps.Clone(route.Cooldowns)
	for stream := range routeStreams {
		select {
		case stream <- message:
		default:
			delete(routeStreams, stream)
			close(stream)
		}
	}
}

// OpenRouteStream 注册路由流连接, 返回后续增量通道。
// 不再返回快照: 分组读取接口已随分组带回当前路由状态, 前端由此拿到的初始值即全量, 连接只负责增量。
func OpenRouteStream() chan RouteState {
	routeMu.Lock()
	defer routeMu.Unlock()

	stream := make(chan RouteState, routeStreamBuffer)
	routeStreams[stream] = struct{}{}
	return stream
}

// CloseRouteStream 注销并关闭指定路由流连接。
func CloseRouteStream(stream chan RouteState) {
	routeMu.Lock()
	defer routeMu.Unlock()

	if _, exists := routeStreams[stream]; exists {
		delete(routeStreams, stream)
		close(stream)
	}
}

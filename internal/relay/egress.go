package relay

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
)

// egressState 是单个出口的运行时状态。
//
// 指标部分直接复用成员指标的 EMA 结构与样本窗口: 出口好坏与成员好坏的判断方式完全一致,
// 没有理由再造一套; 额外只多一份失败冷却。
//
// 只存进程内, 不落库: 出口成绩是"几秒钟就能重新学到"的东西 —— 每个出口各被选中一次
// 就有样本了, 而一个不通的出口最多让一次请求失败(随后进冷却)。为它做持久化快照
// 要动 route_metrics 的格式, 收益不成比例。
type egressState struct {
	memberMetric
	cooldownUntil int64 // 冷却截止(Unix 毫秒); 0 表示未冷却。
}

var (
	egressMu     sync.Mutex
	egressStates = make(map[int]*egressState)
)

// pickEgress 在渠道的出口池里为本轮挑一个出口。
//
// 返回 hasPool=false 表示该渠道没有配置出口池, 调用方沿用渠道自身的代理配置(向后兼容)。
// 返回 error 只在"配了出口但一个都没启用"时出现: 此时既不该悄悄直连(可能违背渠道的访问要求),
// 也不该随便挑一个停用的, 让本轮明确失败比默默改变出口更安全。
func pickEgress(channel model.Channel, clientCountry string) (model.ChannelEgress, bool, error) {
	if len(channel.Egresses) == 0 {
		return model.ChannelEgress{}, false, nil
	}

	egressMu.Lock()
	defer egressMu.Unlock()

	now := time.Now().UnixMilli()
	neutral := neutralEgressWaitLocked()

	enabled := 0
	var (
		best      model.ChannelEgress
		bestScore float64
		hasBest   bool
		probe     model.ChannelEgress
		probeAt   int64
	)
	for _, egress := range channel.Egresses {
		if !egress.Enabled {
			continue
		}
		enabled++
		state := egressStates[egress.ID]
		if state != nil && state.cooldownUntil > now {
			// 全部冷却时探最早恢复的那个: 拒绝请求必然失败, 试一次仍有成功机会。
			if probeAt == 0 || state.cooldownUntil < probeAt {
				probe, probeAt = egress, state.cooldownUntil
			}
			continue
		}
		score := egressScoreLocked(egress, state, neutral, clientCountry)
		// 同分时权重大的先上: 权重就是"我给这条线路的偏好", 冷出口的采样顺序也由它决定。
		if !hasBest || score < bestScore || (score == bestScore && egress.Weight > best.Weight) {
			best, bestScore, hasBest = egress, score, true
		}
	}
	if enabled == 0 {
		return model.ChannelEgress{}, true, fmt.Errorf("channel %s has no enabled egress", channel.Name)
	}
	if !hasBest {
		return probe, true, nil
	}
	return best, true, nil
}

// egressScoreLocked 返回出口的排序分数(越小越好), 调用方须持有 egressMu。
func egressScoreLocked(egress model.ChannelEgress, state *egressState, neutral float64, clientCountry string) float64 {
	// 冷出口按"已知出口的平均水平"参与: 既不会因为没数据被无条件优先(那会让新出口抢走全部流量),
	// 也不会因为没数据永远轮不到 —— 它只在已知出口都比平均更慢时才胜出。
	score := neutral
	if state != nil && state.samples > 0 {
		score = state.emaWaitMs
	}
	// 区域亲和: 调用方地区与出口地区一致时减免一个固定值。默认 0 即完全关闭,
	// 因为地区码来自反代头, 未配可信代理时它恒为空, 开着也不会有任何效果。
	if bonus := op.EgressRegionBonusMs(); bonus > 0 && clientCountry != "" &&
		egress.Region != "" && strings.EqualFold(egress.Region, clientCountry) {
		score -= float64(bonus)
	}
	return score
}

// neutralEgressWaitLocked 返回已观测出口的平均耗时; 没有任何观测时返回 0(全部并列, 由权重定序)。
func neutralEgressWaitLocked() float64 {
	sum, count := 0.0, 0
	for _, state := range egressStates {
		if state.samples > 0 {
			sum += state.emaWaitMs
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

// recordEgressSample 把一次出口的实测结果并入 EMA; 成功时解除冷却。
// egressID 为 0(未走出口池)时直接返回, 不产生任何状态。
func recordEgressSample(egressID, window int, waitMs int64, success bool) {
	if egressID == 0 {
		return
	}
	egressMu.Lock()
	defer egressMu.Unlock()

	state := egressStates[egressID]
	if state == nil {
		state = &egressState{}
		egressStates[egressID] = state
	}
	applySample(&state.memberMetric, window, waitMs, success)
	if success {
		state.cooldownUntil = 0
	}
}

// coolEgress 让某个出口在一段时间内不被选中; seconds<=0 时忽略。
// 与成员冷却同源: 只防"本轮立刻又选中同一个", 不做长惩罚 —— 长惩罚由出口自己的成功率体现。
func coolEgress(egressID, seconds int) {
	if egressID == 0 || seconds <= 0 {
		return
	}
	egressMu.Lock()
	defer egressMu.Unlock()

	state := egressStates[egressID]
	if state == nil {
		state = &egressState{}
		egressStates[egressID] = state
	}
	state.cooldownUntil = time.Now().UnixMilli() + int64(seconds)*1000
}

// egressCooling 判断出口当前是否处于冷却中, 供排查与测试使用。
func egressCooling(egressID int) bool {
	egressMu.Lock()
	defer egressMu.Unlock()

	state := egressStates[egressID]
	return state != nil && state.cooldownUntil > time.Now().UnixMilli()
}

// resetEgressStates 丢弃全部出口状态, 供测试隔离使用。
func resetEgressStates() {
	egressMu.Lock()
	defer egressMu.Unlock()
	egressStates = make(map[int]*egressState)
}

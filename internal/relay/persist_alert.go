package relay

// 路由持久化失败的告警与去重。
//
// 为什么必须告警: 路由状态(当前成员、冷却到什么时候)、成员指标、出口成绩都只在内存里,
// 靠每 5 秒一次的批量刷盘落库。落库一直失败时日志里只有一行 Warnf, 而现象是「渠道刚选过又被选到」
// 「熔断中的成员还在被选」「出口成绩重启后回到零样本」—— 每一个都很难从现象反推回「写库失败」。
// 生产实测 60 天内这两条 Warnf 计数都是 0, 也就是说这是潜在缺陷而非正在发生的故障;
// 但正因为从没触发过, 一旦触发运维没有任何线索, 所以现在补上告警出口。

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/alvindingpeng/switchyard/internal/alert"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/charmbracelet/log"
)

const (
	// persistFailAlertAfter 连续失败多少轮才告警。
	// 取 3 与 internal/task 的 taskAlertAfter 同一口径: 刷盘每 5 秒一轮, 单轮失败多半是
	// SQLite 忙(生产实测 30 天内 13 次 database is locked, 全部自动恢复), 连败 3 轮
	// (约 15 秒)才说明是持续性故障。
	persistFailAlertAfter = 3
)

// persistFailState 一类落库失败(同一环节)的连续计数状态。
type persistFailState struct {
	consecutive int  // 连续失败轮数; 任一轮成功即清零。
	alerted     bool // 本轮故障是否已经告过一次, 用于「只告一次 + 恢复时补一条」。
}

// persistFailStats 按失败环节分别计数的连续失败状态。
//
// 按环节(而不是全局一个计数器)分开: 出口成绩写失败与某个分组的路由快照写失败是两件事,
// 混在一个计数里会互相把对方的连续失败清零, 两边都到不了告警阈值。
var (
	persistFailMu    sync.Mutex
	persistFailStats = make(map[string]*persistFailState)
)

// groupFailKey 某个分组的路由快照失败: 每个分组各自计数。
func groupFailKey(groupID int) string { return fmt.Sprintf("route snapshot group %d", groupID) }

// egressFailKey 出口成绩整表失败。
const egressFailKey = "egress metrics"

// routePersistSettle 处理一轮落库结果: 失败时累加计数并在阈值处告警, 成功时判恢复。
//
// 恢复只认「一轮成功」而不是「连续若干轮成功」: 这个函数只在真的有脏数据要写时才会被调用,
// 没有脏数据时它根本不会被叫到, 用连续成功计数会在「修好之后就再没流量」的分组上永远停在
// 已告警状态 —— 下一次故障反而不会告警。刷屏风险交给告警本身的去重窗口(alert_dedup_minutes)
// 兜底: 同一 (kind, reason) 在窗口内只落一条。
func routePersistSettle(key string, err error) {
	persistFailMu.Lock()
	state := persistFailStats[key]
	if state == nil {
		state = &persistFailState{}
		persistFailStats[key] = state
	}
	if err != nil {
		state.consecutive++
		consecutive := state.consecutive
		shouldAlert := consecutive >= persistFailAlertAfter && !state.alerted
		if shouldAlert {
			state.alerted = true
		}
		persistFailMu.Unlock()
		if shouldAlert {
			raiseRoutePersistFailedAlert(key, consecutive, err.Error())
		}
		return
	}

	// 成功: 清零连续失败计数, 只在之前真的告过警时才补一条恢复通知。
	wasAlerted := state.alerted
	state.consecutive = 0
	state.alerted = false
	persistFailMu.Unlock()
	if !wasAlerted {
		return
	}
	log.Infof("route persist recovered: %s", key)
	op.RaiseAlertFacts(context.Background(), op.AlertFacts{
		Kind:   "route_persist_recovered",
		Reason: fmt.Sprintf("route persist recovered: %s", key),
		Fields: []alert.Field{
			{Label: "恢复环节", Value: key},
			{Label: "恢复时间", Value: time.Now().Format("2006-01-02 15:04:05")},
		},
	})
}

// raiseRoutePersistFailedAlert 往外发一条「路由状态刷盘持续失败」的告警。
func raiseRoutePersistFailedAlert(key string, consecutive int, detail string) {
	log.Warnf("route persist failing (%s): %d consecutive rounds, alerting: %s", key, consecutive, detail)
	op.RaiseAlertFacts(context.Background(), op.AlertFacts{
		Kind: "route_persist_failed",
		// 去重键只到「哪个环节失败」: 把连续次数或错误原文拼进去会让每一次都成为新告警。
		Reason: fmt.Sprintf("route persist failed: %s", key),
		Fields: []alert.Field{
			{Label: "失败环节", Value: key},
			{Label: "连续失败", Value: fmt.Sprintf("%d 轮", consecutive)},
			{Label: "最近错误", Value: detail},
			{Label: "说明", Value: "路由状态、冷却、成员指标与出口成绩只存在内存里, 靠后台每 5 秒批量刷盘落库"},
			{Label: "影响", Value: "刷盘持续失败时这些状态不落库: 重启后冷却与成员指标回到初始值, 出口成绩变回零样本, 选路会在刚失败的成员上重试"},
			{Label: "建议", Value: "检查磁盘余量与数据库是否被长时间独占; 若错误为 database is locked, 对照同一时刻的结算任务日志"},
		},
	})
}

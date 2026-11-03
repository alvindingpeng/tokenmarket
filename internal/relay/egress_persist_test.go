package relay

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
)

// 出口成绩的持久化。
//
// 改动前 egressStates 只是进程内的 map, 重启即清空: 所有出口回到"零样本",
// 选路退化成按权重定序 —— 权重最高的线路哪怕早已不通, 也会在重启后的第一批请求里
// 被优先选中, 用户吃 502。线上每天部署数次, 这个窗口一直在。

// egressPersistDB 独立 sqlite 库: 落库与恢复都真的读写它, 而不是打桩。
func egressPersistDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "egress_metrics.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
}

// flushEgressNow 立刻把出口成绩落库, 不等 5 秒的后台刷盘。
func flushEgressNow(t *testing.T) {
	t.Helper()
	markEgressDirty()
	FlushRoutePersist()
}

// TestEgressMetricsSurviveRestart 成绩与冷却落库后, 新进程能原样恢复。
//
// 这是本改动的核心断言: 恢复后的第一批请求按上一轮进程的实测择优,
// 而不是按权重把一个已知不通的出口推上前。
func TestEgressMetricsSurviveRestart(t *testing.T) {
	egressPersistDB(t)
	resetEgressStates()
	t.Cleanup(resetEgressStates)

	// 快出口 100ms 两次成功, 慢出口 900ms 一次失败并被冷却。
	recordEgressSample(1001, 20, 100, true)
	recordEgressSample(1001, 20, 100, true)
	recordEgressSample(1002, 20, 900, false)
	coolEgress(1002, 60)
	flushEgressNow(t)

	// 模拟重启: 进程内状态全丢, 再从库里恢复。
	resetEgressStates()
	if len(egressMetricRecords()) != 0 {
		t.Fatal("reset 之后仍有进程内状态, 模拟重启失败")
	}
	records, err := op.EgressMetricsLoadAll()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("落库行数不符: %d", len(records))
	}
	restoreEgressMetrics(records)

	views := EgressRuntimeViews([]model.ChannelEgress{{ID: 1001}, {ID: 1002}})
	if !views[0].HasData || views[0].Samples != 2 || views[0].AvgWaitMs != 100 {
		t.Fatalf("快出口成绩未恢复: %#v", views[0])
	}
	if !views[1].HasData || views[1].Samples != 1 || views[1].SuccessRate != 0 {
		t.Fatalf("失败样本未恢复: %#v", views[1])
	}
	if !views[1].Cooling {
		t.Fatalf("冷却未恢复: 重启会立刻重试刚刚判定不可用的线路 %#v", views[1])
	}
}

// TestRestoredEgressMetricsDriveRouting 恢复之后选路真的按恢复的成绩走。
func TestRestoredEgressMetricsDriveRouting(t *testing.T) {
	egressPersistDB(t)
	resetEgressStates()
	t.Cleanup(resetEgressStates)

	// 权重更高的 2002 已知很慢; 权重为 0 的 2001 已知很快。
	recordEgressSample(2001, 20, 50, true)
	recordEgressSample(2002, 20, 2000, true)
	flushEgressNow(t)

	resetEgressStates()
	records, err := op.EgressMetricsLoadAll()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	restoreEgressMetrics(records)

	channel := egressFixture(testEgress(2001, "fast", 0), testEgress(2002, "slow-preferred", 9))
	picked, hasPool, err := pickEgress(channel, "")
	if err != nil || !hasPool {
		t.Fatalf("pick: %v hasPool=%v", err, hasPool)
	}
	if picked.ID != 2001 {
		t.Fatalf("恢复成绩后仍按权重挑了慢出口: 选中 %d", picked.ID)
	}
}

// TestExpiredCooldownIsNotRestored 已过期的冷却不恢复: 免得给早已恢复的出口留下无意义的行。
func TestExpiredCooldownIsNotRestored(t *testing.T) {
	egressPersistDB(t)
	resetEgressStates()
	t.Cleanup(resetEgressStates)

	restoreEgressMetrics([]model.EgressMetricRecord{
		{EgressID: 3001, Samples: 0, CooldownUntil: time.Now().Add(-time.Hour).UnixMilli()},
	})
	if views := EgressRuntimeViews([]model.ChannelEgress{{ID: 3001}}); views[0].HasData || views[0].Cooling {
		t.Fatalf("过期冷却被恢复成状态: %#v", views[0])
	}
	if len(egressMetricRecords()) != 0 {
		t.Fatal("过期冷却留在了进程内状态里, 下次落库会把它写回库")
	}
}

// TestEmptyStatesAreNotPersisted fail closed 会留下"只有冷却、没有样本"或全空的状态,
// 全空的没必要进库。
func TestEmptyStatesAreNotPersisted(t *testing.T) {
	resetEgressStates()
	t.Cleanup(resetEgressStates)

	egressMu.Lock()
	egressStates[4001] = &egressState{}
	egressMu.Unlock()

	if records := egressMetricRecords(); len(records) != 0 {
		t.Fatalf("空状态被导出: %#v", records)
	}
}

// TestStaleEgressMetricsArePruned 出口删掉后残留的成绩行要清掉。
func TestStaleEgressMetricsArePruned(t *testing.T) {
	egressPersistDB(t)
	resetEgressStates()
	t.Cleanup(resetEgressStates)

	recordEgressSample(5001, 20, 120, true)
	recordEgressSample(5002, 20, 130, true)
	flushEgressNow(t)

	// 库里只保留 5001。
	channel := model.Channel{UserID: 0, ChannelConfig: model.ChannelConfig{Name: "c1"}}
	if err := db.GetDB().Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if err := db.GetDB().Create(&model.ChannelEgress{ID: 5001, ChannelID: channel.ID, Name: "keep", Kind: model.EgressKindDirect, Enabled: true}).Error; err != nil {
		t.Fatalf("create egress: %v", err)
	}
	ids, err := op.EgressIDs()
	if err != nil {
		t.Fatalf("ids: %v", err)
	}
	if err := op.EgressMetricsDeleteMissing(ids); err != nil {
		t.Fatalf("prune: %v", err)
	}

	records, err := op.EgressMetricsLoadAll()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(records) != 1 || records[0].EgressID != 5001 {
		t.Fatalf("残留成绩未清掉: %#v", records)
	}
}

// TestFlushWithoutNewSamplesDoesNotRewrite 脏标记取走之后, 空转的刷盘不该重复写同一条成绩
// (后台 5 秒一轮, 每轮都写整表就是纯浪费)。
func TestFlushWithoutNewSamplesDoesNotRewrite(t *testing.T) {
	egressPersistDB(t)
	resetEgressStates()
	t.Cleanup(resetEgressStates)

	recordEgressSample(6001, 20, 100, true)
	flushEgressNow(t)
	first, err := op.EgressMetricsLoadAll()
	if err != nil || len(first) != 1 {
		t.Fatalf("首次落库: %v %#v", err, first)
	}

	// 第二三轮没有新样本: updated_at 不应被重写。
	FlushRoutePersist()
	FlushRoutePersist()
	again, err := op.EgressMetricsLoadAll()
	if err != nil || len(again) != 1 {
		t.Fatalf("再次落库: %v %#v", err, again)
	}
	if !again[0].UpdatedAt.Equal(first[0].UpdatedAt) {
		t.Fatalf("没有新样本却重写了成绩行: %s -> %s", first[0].UpdatedAt, again[0].UpdatedAt)
	}
}

// TestRestorePersistLoadsEgressMetrics 走真正的启动入口 RestorePersist,
// 而不是直接调 restoreEgressMetrics —— 否则"接上了没有"这件事没被验证过
// (变异校验: 把 RestorePersist 里那行调用删掉, 本测试必须失败)。
func TestRestorePersistLoadsEgressMetrics(t *testing.T) {
	egressPersistDB(t)
	resetEgressStates()
	t.Cleanup(resetEgressStates)

	recordEgressSample(7001, 20, 80, true)
	coolEgress(7002, 30)
	recordEgressSample(7002, 20, 400, false)
	flushEgressNow(t)

	resetEgressStates()
	if err := RestorePersist(); err != nil {
		t.Fatalf("RestorePersist: %v", err)
	}

	views := EgressRuntimeViews([]model.ChannelEgress{{ID: 7001}, {ID: 7002}})
	if !views[0].HasData || views[0].Samples != 1 || views[0].AvgWaitMs != 80 {
		t.Fatalf("启动入口没把出口成绩恢复进来: %#v", views[0])
	}
	if !views[1].Cooling {
		t.Fatalf("启动入口没把冷却恢复进来: %#v", views[1])
	}
}

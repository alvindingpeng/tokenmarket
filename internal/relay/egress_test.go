package relay

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/rhttp"
)

// egressFixture 构造一个带出口池的渠道; 出口 ID 由调用方给定, 便于在断言里直接引用。
func egressFixture(egresses ...model.ChannelEgress) model.Channel {
	channel := model.Channel{}
	channel.Name = "test-channel"
	channel.Egresses = egresses
	return channel
}

func testEgress(id int, name string, weight int) model.ChannelEgress {
	return model.ChannelEgress{
		ID:      id,
		Name:    name,
		Kind:    model.EgressKindHTTP,
		URL:     "http://127.0.0.1:9" + name,
		Enabled: true,
		Weight:  weight,
	}
}

// TestEgressRuntimeViews verifies cold, sampled, and cooling snapshots.
func TestEgressRuntimeViews(t *testing.T) {
	resetEgressStates()
	t.Cleanup(resetEgressStates)
	egress := testEgress(9001, "snapshot", 1)
	views := EgressRuntimeViews([]model.ChannelEgress{egress, testEgress(9002, "cold", 1)})
	if len(views) != 2 || views[0].EgressID != 9001 || views[0].HasData || views[0].Samples != 0 {
		t.Fatalf("unexpected cold snapshot: %#v", views)
	}
	recordEgressSample(9001, 20, 125, true)
	views = EgressRuntimeViews([]model.ChannelEgress{egress})
	if len(views) != 1 || !views[0].HasData || views[0].Samples != 1 || views[0].SuccessRate <= 0 || views[0].AvgWaitMs != 125 {
		t.Fatalf("unexpected sampled snapshot: %#v", views)
	}
	coolEgress(9001, 10)
	views = EgressRuntimeViews([]model.ChannelEgress{egress})
	if !views[0].Cooling || views[0].CooldownRemainingMs <= 0 || views[0].CooldownUntil <= time.Now().UnixMilli() {
		t.Fatalf("unexpected cooldown snapshot: %#v", views[0])
	}
}

func TestPickEgressWithoutPool(t *testing.T) {
	resetEgressStates()

	_, hasPool, err := pickEgress(model.Channel{}, "")
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if hasPool {
		t.Fatalf("没有出口时 hasPool 应为 false")
	}
}

// TestPickEgressPrefersFastest 按实测耗时择优: 快出口胜出。
func TestPickEgressPrefersFastest(t *testing.T) {
	resetEgressStates()

	slow, fast := testEgress(1, "slow", 0), testEgress(2, "fast", 0)
	recordEgressSample(slow.ID, 20, 900, true)
	recordEgressSample(fast.ID, 20, 200, true)

	picked, hasPool, err := pickEgress(egressFixture(slow, fast), "")
	if err != nil || !hasPool {
		t.Fatalf("选择失败: err=%v hasPool=%v", err, hasPool)
	}
	if picked.ID != fast.ID {
		t.Fatalf("应选中最快的出口 %d, 实得 %d", fast.ID, picked.ID)
	}
}

// TestPickEgressColdUsesNeutral 冷出口按已观测出口的平均水平参与, 同分时权重大的先上。
// 若把冷出口当 0 毫秒, 新加的出口会无条件抢走全部流量。
func TestPickEgressColdUsesNeutral(t *testing.T) {
	resetEgressStates()

	known, cold := testEgress(1, "known", 0), testEgress(2, "cold", 5)
	recordEgressSample(known.ID, 20, 900, true)

	picked, _, err := pickEgress(egressFixture(known, cold), "")
	if err != nil {
		t.Fatalf("选择失败: %v", err)
	}
	if picked.ID != cold.ID {
		t.Fatalf("同分时权重大的冷出口应胜出, 实得 %d", picked.ID)
	}

	// 反向: 冷出口权重低时不该压过已知出口。
	known2, cold2 := testEgress(3, "known2", 9), testEgress(4, "cold2", 0)
	recordEgressSample(known2.ID, 20, 900, true)
	picked, _, err = pickEgress(egressFixture(known2, cold2), "")
	if err != nil {
		t.Fatalf("选择失败: %v", err)
	}
	if picked.ID != known2.ID {
		t.Fatalf("权重低时已知出口应胜出, 实得 %d", picked.ID)
	}
}

// TestPickEgressSkipsCooling 冷却中的出口不参与选择。
func TestPickEgressSkipsCooling(t *testing.T) {
	resetEgressStates()

	fast, slow := testEgress(1, "fast", 0), testEgress(2, "slow", 0)
	recordEgressSample(fast.ID, 20, 100, true)
	recordEgressSample(slow.ID, 20, 900, true)
	coolEgress(fast.ID, 30)

	picked, _, err := pickEgress(egressFixture(fast, slow), "")
	if err != nil {
		t.Fatalf("选择失败: %v", err)
	}
	if picked.ID != slow.ID {
		t.Fatalf("最快出口冷却中, 应选 %d, 实得 %d", slow.ID, picked.ID)
	}
}

// TestPickEgressProbesWhenAllCooling 全部冷却时探最早恢复的那个:
// 拒绝请求必然失败, 试一次仍有成功机会。
func TestPickEgressProbesWhenAllCooling(t *testing.T) {
	resetEgressStates()

	first, second := testEgress(1, "first", 0), testEgress(2, "second", 0)
	coolEgress(first.ID, 300)
	coolEgress(second.ID, 5)

	picked, _, err := pickEgress(egressFixture(first, second), "")
	if err != nil {
		t.Fatalf("选择失败: %v", err)
	}
	if picked.ID != second.ID {
		t.Fatalf("应探最早恢复的出口 %d, 实得 %d", second.ID, picked.ID)
	}
}

// TestPickEgressAllDisabled 配了出口但全部停用时明确报错, 不能悄悄直连。
func TestPickEgressAllDisabled(t *testing.T) {
	resetEgressStates()

	disabled := testEgress(1, "off", 0)
	disabled.Enabled = false

	if _, hasPool, err := pickEgress(egressFixture(disabled), ""); err == nil || !hasPool {
		t.Fatalf("应返回错误且 hasPool=true, 实得 err=%v hasPool=%v", err, hasPool)
	}
}

// TestRecordEgressSampleClearsCooldown 成功一次即解除冷却。
func TestRecordEgressSampleClearsCooldown(t *testing.T) {
	resetEgressStates()

	coolEgress(1, 30)
	if !egressCooling(1) {
		t.Fatalf("冷却应生效")
	}
	recordEgressSample(1, 20, 120, true)
	if egressCooling(1) {
		t.Fatalf("成功后应解除冷却")
	}
}

// TestPickEgressRegionBonus 区域亲和加成足以翻转选择; 加成为 0(默认)时不影响任何决策。
func TestPickEgressRegionBonus(t *testing.T) {
	resetEgressStates()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "egress.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}

	near, far := testEgress(1, "near", 0), testEgress(2, "far", 0)
	near.Region, far.Region = "HK", "US"
	recordEgressSample(near.ID, 20, 600, true)
	recordEgressSample(far.ID, 20, 400, true)

	// 默认加成为 0: 实测更快的 far 胜出。
	if picked, _, err := pickEgress(egressFixture(near, far), "HK"); err != nil || picked.ID != far.ID {
		t.Fatalf("默认应选实测更快的 %d, 实得 %d (err=%v)", far.ID, picked.ID, err)
	}

	if err := op.SettingUpsert(model.SettingKeyEgressRegionBonusMs, "500"); err != nil {
		t.Fatalf("设置加成: %v", err)
	}
	t.Cleanup(func() { _ = op.SettingUpsert(model.SettingKeyEgressRegionBonusMs, "0") })

	// 500ms 加成后: near 600-500=100 < far 400, 香港调用方应走香港出口。
	if picked, _, err := pickEgress(egressFixture(near, far), "HK"); err != nil || picked.ID != near.ID {
		t.Fatalf("加成后应选同地区出口 %d, 实得 %d (err=%v)", near.ID, picked.ID, err)
	}
	// 其它地区的调用方不享受加成, 仍按实测耗时选。
	if picked, _, err := pickEgress(egressFixture(near, far), "US"); err != nil || picked.ID != far.ID {
		t.Fatalf("其它地区调用方应选 %d, 实得 %d (err=%v)", far.ID, picked.ID, err)
	}
}

// TestResolveUpstreamClientUsesEgress 出口优先于渠道自身的代理配置:
// 两者同时存在时, 流量必须从出口出去, 否则管理员分不清到底走了哪条线路。
func TestResolveUpstreamClientUsesEgress(t *testing.T) {
	channel := model.Channel{}
	channel.ChannelConfig = model.ChannelConfig{Proxy: true, ChannelProxy: "http://127.0.0.1:18080"}

	egress := model.ChannelEgress{ID: 7, Name: "hk", Kind: model.EgressKindHTTP, URL: "http://127.0.0.1:18081", Enabled: true}
	client, err := resolveUpstreamClient(channel, egress)
	if err != nil {
		t.Fatalf("取客户端失败: %v", err)
	}
	expected, err := rhttp.Shared("http://127.0.0.1:18081")
	if err != nil {
		t.Fatalf("取共享客户端失败: %v", err)
	}
	if client != expected {
		t.Fatalf("应使用出口的连接池, 实得非出口客户端")
	}

	// 直连出口: 用共享直连客户端。
	direct, err := resolveUpstreamClient(channel, model.ChannelEgress{ID: 8, Kind: model.EgressKindDirect, Enabled: true})
	if err != nil {
		t.Fatalf("取客户端失败: %v", err)
	}
	directClient, err := rhttp.Direct()
	if err != nil {
		t.Fatalf("取直连客户端失败: %v", err)
	}
	if direct != directClient {
		t.Fatalf("直连出口应使用共享直连客户端")
	}
}

// TestPickEgressWeightIsTieBreakerNotTrafficRatio 钉死权重的**语义边界**:
// 它只在分数完全相等时参与, 分数不同时再大的权重也压不过 0.1ms 的差距。
//
// 这条用例存在的理由: 界面标签写着「权重」, 管理员填 80/20 会以为在做流量配比。
// 它并不是 —— 选路先按实测 EMA 耗时排序, 权重只是同分时的次序(以及冷出口的采样顺序)。
// 若将来有人把这里改成真正的加权分流, 这条会立刻失败, 逼他先解释清楚为什么值得
// 把请求主动分给已知更慢的线路。
func TestPickEgressWeightIsTieBreakerNotTrafficRatio(t *testing.T) {
	resetEgressStates()

	// 同分: 除权重外完全一致的两个出口 → 权重大的先上。
	lowWeight, highWeight := testEgress(11, "low", 1), testEgress(12, "high", 99)
	picked, _, err := pickEgress(egressFixture(lowWeight, highWeight), "")
	if err != nil {
		t.Fatalf("同分选择失败: %v", err)
	}
	if picked.ID != highWeight.ID {
		t.Fatalf("同分时应选权重大的 %d, 实得 %d", highWeight.ID, picked.ID)
	}

	// 分数不同: 权重 0 的出口快 1ms, 权重 1000 的慢 → 仍然选快的那个。
	// 这就是"80/20 不是八二开"的直接证据: 差一点点就完全不看权重。
	fastNoWeight, slowHugeWeight := testEgress(13, "fast", 0), testEgress(14, "slow", 1000)
	recordEgressSample(fastNoWeight.ID, 20, 150, true)
	recordEgressSample(slowHugeWeight.ID, 20, 151, true)
	picked, _, err = pickEgress(egressFixture(fastNoWeight, slowHugeWeight), "")
	if err != nil {
		t.Fatalf("分差选择失败: %v", err)
	}
	if picked.ID != fastNoWeight.ID {
		t.Fatalf("应选实际更快的出口 %d(哪怕权重为 0), 实得 %d —— 权重不该变成流量配比", fastNoWeight.ID, picked.ID)
	}

	// 反向再确认一次: 分数相同时权重确实能翻转结果, 排除上一条"恰好总是选第一个"的可能。
	slowFirst, fastSecond := testEgress(15, "slow-first", 500), testEgress(16, "fast-second", 0)
	recordEgressSample(slowFirst.ID, 20, 300, true)
	recordEgressSample(fastSecond.ID, 20, 300, true)
	picked, _, err = pickEgress(egressFixture(slowFirst, fastSecond), "")
	if err != nil {
		t.Fatalf("同分反向选择失败: %v", err)
	}
	if picked.ID != slowFirst.ID {
		t.Fatalf("同分(300ms)时应选权重大的 %d, 实得 %d", slowFirst.ID, picked.ID)
	}

	// 单出口场景不受权重影响: 加权分流最容易在这里误伤既有行为。
	only := testEgress(17, "only", 7)
	picked, _, err = pickEgress(egressFixture(only), "")
	if err != nil || picked.ID != only.ID {
		t.Fatalf("单出口应恒选它, err=%v picked=%d", err, picked.ID)
	}
}

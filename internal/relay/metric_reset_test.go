package relay

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// resetMetricsForTest 清空全部运行指标, 保证用例间互不串扰。
func resetMetricsForTest(t *testing.T) {
	t.Helper()
	metricMu.Lock()
	memberMetrics = make(map[memberMetricKey]*memberMetric)
	modelMetrics = make(map[int]*memberMetric)
	metricMu.Unlock()
	t.Cleanup(func() {
		metricMu.Lock()
		memberMetrics = make(map[memberMetricKey]*memberMetric)
		modelMetrics = make(map[int]*memberMetric)
		metricMu.Unlock()
	})
}

// TestRestoreSeedsModelFromFullestRow 复现生产事故(K8 渠道模型评分归零):
// 同一渠道模型下有两条持久化观测 —— 163 样本全部成功与 1 样本恰好失败;
// 重启恢复必须按样本数最大的行播种全局指标, 不能被 1 样本的陈旧行钉死在 0 分。
func TestRestoreSeedsModelFromFullestRow(t *testing.T) {
	resetMetricsForTest(t)
	records := []model.RouteMetricRecord{
		{GroupID: 5, ItemID: 124, EmaWaitMs: 2708, EmaSuccess: 1, Samples: 163, UpdatedAt: time.Now().Add(-time.Hour)},
		{GroupID: 8, ItemID: 125, EmaWaitMs: 52, EmaSuccess: 0, Samples: 1, UpdatedAt: time.Now()},
	}
	restoreMemberMetrics(records, map[int]int{124: 350, 125: 350})

	waitMs, success, samples := modelMetricOf(350)
	if samples != 163 || success != 1 || math.Abs(waitMs-2708) > 0.5 {
		t.Fatalf("全局指标应取 163 样本的成功行: wait=%.1f success=%.2f samples=%d", waitMs, success, samples)
	}
	// 受害成员(1 样本陈旧失败)选路时也应拿到全局成绩, 而非自己的陈旧观测。
	group := model.Group{ID: 8}
	item := model.GroupItem{ID: 125, ChannelModelID: 350}
	gotWait, gotSuccess, gotSamples := memberScopeMetric(group, item)
	if gotSamples != 163 || gotSuccess != 1 {
		t.Fatalf("受害成员应继承全局成绩: wait=%.1f success=%.2f samples=%d", gotWait, gotSuccess, gotSamples)
	}
}

// TestRestoreSeedPrefersMostSamplesOverRecency 样本数优先于新鲜度: 1 样本的新失败
// 不得压过 163 样本的旧成功 —— 生产上 (8,125) 的 updated_at 会被整组快照刷成最新, 时间戳不可作准。
func TestRestoreSeedPrefersMostSamplesOverRecency(t *testing.T) {
	resetMetricsForTest(t)
	records := []model.RouteMetricRecord{
		{GroupID: 5, ItemID: 124, EmaWaitMs: 2708, EmaSuccess: 1, Samples: 163, UpdatedAt: time.Now().Add(-24 * time.Hour)},
		{GroupID: 8, ItemID: 125, EmaWaitMs: 52, EmaSuccess: 0, Samples: 1, UpdatedAt: time.Now()},
	}
	restoreMemberMetrics(records, map[int]int{124: 350, 125: 350})
	_, success, samples := modelMetricOf(350)
	if samples != 163 || success != 1 {
		t.Fatalf("新鲜度不得压过样本数: success=%.2f samples=%d", success, samples)
	}
}

// TestRestoreSeedTieBreaksAreDeterministic 同样本数时按观测更新更近、再按成员 ID 更小定序, 结果可复现。
func TestRestoreSeedTieBreaksAreDeterministic(t *testing.T) {
	resetMetricsForTest(t)
	newer := time.Now()
	older := newer.Add(-time.Hour)
	records := []model.RouteMetricRecord{
		{GroupID: 8, ItemID: 125, EmaWaitMs: 52, EmaSuccess: 0, Samples: 10, UpdatedAt: older},
		{GroupID: 5, ItemID: 124, EmaWaitMs: 2708, EmaSuccess: 1, Samples: 10, UpdatedAt: newer},
	}
	restoreMemberMetrics(records, map[int]int{124: 350, 125: 350})
	_, success, _ := modelMetricOf(350)
	if success != 1 {
		t.Fatalf("同样本数应取观测更新更近的一行: success=%.2f", success)
	}

	// 更新时间也相同时取成员 ID 更小者, 保证任何时刻恢复结果一致。
	resetMetricsForTest(t)
	records = []model.RouteMetricRecord{
		{GroupID: 8, ItemID: 125, EmaWaitMs: 52, EmaSuccess: 0, Samples: 10, UpdatedAt: newer},
		{GroupID: 5, ItemID: 124, EmaWaitMs: 2708, EmaSuccess: 1, Samples: 10, UpdatedAt: newer},
	}
	restoreMemberMetrics(records, map[int]int{124: 350, 125: 350})
	waitMs, _, _ := modelMetricOf(350)
	if math.Abs(waitMs-2708) > 0.5 {
		t.Fatalf("同时间戳应取成员 ID 更小的行: wait=%.1f", waitMs)
	}
}

// TestLazySeedUsesFullestRow 即便跳过恢复直接冷启动, 懒播种也必须择优而非首个被访问者。
func TestLazySeedUsesFullestRow(t *testing.T) {
	resetMetricsForTest(t)
	// 全局指标空, 只有成员观测: (5,124) 163 样本成功, (8,125) 1 样本失败。
	restoreMemberMetrics([]model.RouteMetricRecord{
		{GroupID: 5, ItemID: 124, EmaWaitMs: 2708, EmaSuccess: 1, Samples: 163, UpdatedAt: time.Now()},
		{GroupID: 8, ItemID: 125, EmaWaitMs: 52, EmaSuccess: 0, Samples: 1, UpdatedAt: time.Now()},
	}, map[int]int{}) // 映射缺失: 两条观测都没归到渠道模型。
	metricMu.Lock()
	for key, metric := range memberMetrics {
		metric.channelModelID = 350 // 运行期由 recordMemberMetric 打标, 这里直接还原该状态。
		_ = key
	}
	delete(modelMetrics, 350)
	metricMu.Unlock()

	// 先访问受害成员: 播种必须取 163 样本行。
	waitMs, success, samples := memberScopeMetric(model.Group{ID: 8}, model.GroupItem{ID: 125, ChannelModelID: 350})
	if samples != 163 || success != 1 {
		t.Fatalf("懒播种应取样本数最大的观测: wait=%.1f success=%.2f samples=%d", waitMs, success, samples)
	}
}

// TestReliabilityPenaltyScalesWithSampleConfidence 校验修复 B: 折减强度随样本置信度收敛。
// 一条孤例观测不把分数乘到 0(否则评分归零后永不被选中, 也就永远翻不了身),
// 样本充足的失败模型仍被压到近 0 分垫底。
func TestReliabilityPenaltyScalesWithSampleConfidence(t *testing.T) {
	const floor = 0.7
	if got := reliabilityPenalty(0, 1, floor); got < 0.5 {
		t.Fatalf("1 个样本的失败不应把折减打到近 0: penalty=%.3f", got)
	}
	if got := reliabilityPenalty(0, 1000, floor); got > 0.05 {
		t.Fatalf("样本充足的失败应接近全额折减: penalty=%.3f", got)
	}
	if got := reliabilityPenalty(0, 5, floor); got >= reliabilityPenalty(0, 1, floor) {
		t.Fatalf("样本越多惩罚应越强: 5=%0.3f 1=%0.3f", reliabilityPenalty(0, 5, floor), reliabilityPenalty(0, 1, floor))
	}
	if got := reliabilityPenalty(0.9, 3, floor); got != 1 {
		t.Fatalf("达标成员不应惩罚: penalty=%.3f", got)
	}
	if got := reliabilityPenalty(0.2, 0, floor); got != 1 {
		t.Fatalf("冷成员不惩罚: penalty=%.3f", got)
	}
	if got := reliabilityPenalty(0.2, 3, 0); got != 1 {
		t.Fatalf("未配置下限不惩罚: penalty=%.3f", got)
	}
	if got := reliabilityPenalty(0.4, 20, floor); math.Abs(got-(1-20.0/25.0*(1-0.4/0.7))) > 1e-9 {
		t.Fatalf("penalty=%.3f, 公式不符", got)
	}
}

// TestSingleFailureSampleDoesNotZeroScore 复现 K8 评分归零的第二重保险:
// 即便全局指标真被 1 样本失败观测占据, 该成员的综合分也只能轻度折减而非归零。
func TestSingleFailureSampleDoesNotZeroScore(t *testing.T) {
	resetMetricsForTest(t)
	sick := model.GroupItem{ID: 1, UserPrice: model.LLMPrice{Input: 1, Output: 1}, ChannelModelID: 11}
	sound := model.GroupItem{ID: 2, UserPrice: model.LLMPrice{Input: 1, Output: 1}, ChannelModelID: 12}
	group := model.Group{ID: 934, Items: []model.GroupItem{sick, sound},
		RelayConfig: model.GroupRelayConfig{ReliabilityFloor: 70}}

	metricMu.Lock()
	modelMetrics = map[int]*memberMetric{
		11: {emaWaitMs: 52, emaSuccess: 0, samples: 1, channelModelID: 11}, // 孤例失败。
		12: {emaWaitMs: 100, emaSuccess: 0.95, samples: 20, channelModelID: 12},
	}
	metricMu.Unlock()

	views := MemberScoreViews(group)
	if views[0].Score <= 0 {
		t.Fatalf("1 样本失败的成员得分不得归零: score=%.2f", views[0].Score)
	}
	if views[0].Score >= views[1].Score {
		t.Fatalf("孤例失败仍应轻度落后于健康成员: %.2f vs %.2f", views[0].Score, views[1].Score)
	}
}

// metricResetTestDB 建 SQLite 测试库并预置渠道授权与分组成员, 供持久化行的增删与映射查询。
func metricResetTestDB(t *testing.T) (groupID, itemID, modelID int) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "reset.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	groupID, itemID = 8, 125
	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "reset-test", BaseURL: "http://up"}}
	if err := db.GetDB().Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	key := model.ChannelKey{ChannelID: channel.ID, ChannelKeyConfig: model.ChannelKeyConfig{Name: "k", Key: "sk", Enabled: true}}
	if err := db.GetDB().Create(&key).Error; err != nil {
		t.Fatalf("create key: %v", err)
	}
	cm := model.ChannelModel{ChannelID: channel.ID, Name: "reset-model"}
	if err := db.GetDB().Create(&cm).Error; err != nil {
		t.Fatalf("create channel model: %v", err)
	}
	grant := model.ChannelGrant{ChannelModelID: cm.ID, ChannelKeyID: key.ID}
	if err := db.GetDB().Create(&grant).Error; err != nil {
		t.Fatalf("create grant: %v", err)
	}
	grp := model.Group{Name: "reset-group"}
	if err := db.GetDB().Create(&grp).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	groupID = grp.ID
	item := model.GroupItem{GroupID: groupID, ChannelGrantID: grant.ID}
	if err := db.GetDB().Create(&item).Error; err != nil {
		t.Fatalf("create item: %v", err)
	}
	itemID = item.ID
	return groupID, itemID, cm.ID
}

// TestResetModelMetricClearsMemoryAndPersist 校验修复 C(渠道模型口径): 全局指标、名下成员观测
// 与持久化 route_metrics 行一并清理, 运维一键纠偏后随新请求重新积累。
func TestResetModelMetricClearsMemoryAndPersist(t *testing.T) {
	resetMetricsForTest(t)
	groupID, itemID, modelID := metricResetTestDB(t)
	recordMemberMetric(groupID, itemID, modelID, 20, 52, false)
	FlushRoutePersist()
	if _, _, samples := modelMetricOf(modelID); samples == 0 {
		t.Fatalf("前置条件失败: 全局指标应有观测")
	}

	cleared, err := ResetModelMetric(modelID)
	if err != nil {
		t.Fatalf("reset model metric: %v", err)
	}
	if cleared != 1 {
		t.Fatalf("cleared = %d, want 1", cleared)
	}
	if _, _, samples := modelMetricOf(modelID); samples != 0 {
		t.Fatalf("全局指标应清空: samples=%d", samples)
	}
	var count int64
	db.GetDB().Model(&model.RouteMetricRecord{}).Where("item_id = ?", itemID).Count(&count)
	if count != 0 {
		t.Fatalf("持久化行应清空: %d", count)
	}
}

// TestResetMemberMetricReSeedsFromRemaining 校验修复 C(成员口径): 清掉被污染的成员观测后,
// 全局指标按剩余观测重播 —— 这正是 K8 事故的运维急救路径。
func TestResetMemberMetricReSeedsFromRemaining(t *testing.T) {
	resetMetricsForTest(t)
	groupID, badItemID, modelID := metricResetTestDB(t)

	// 受污染观测: 1 样本失败; 健康观测: 163 样本成功(挂在另一成员上)。
	metricMu.Lock()
	memberMetrics[memberMetricKey{groupID: 5, itemID: 124}] = &memberMetric{
		emaWaitMs: 2708, emaSuccess: 1, samples: 163, channelModelID: modelID, updatedAt: time.Now().UnixMilli()}
	memberMetrics[memberMetricKey{groupID: groupID, itemID: badItemID}] = &memberMetric{
		emaWaitMs: 52, emaSuccess: 0, samples: 1, channelModelID: modelID, updatedAt: time.Now().UnixMilli()}
	modelMetrics[modelID] = &memberMetric{emaWaitMs: 52, emaSuccess: 0, samples: 1, channelModelID: modelID}
	metricMu.Unlock()

	gotModelID, err := ResetMemberMetric(groupID, badItemID)
	if err != nil {
		t.Fatalf("reset member metric: %v", err)
	}
	if gotModelID != modelID {
		t.Fatalf("channel model id = %d, want %d", gotModelID, modelID)
	}
	_, success, samples := modelMetricOf(modelID)
	if samples != 163 || success != 1 {
		t.Fatalf("重播应取剩余观测中样本数最大的一行: success=%.2f samples=%d", success, samples)
	}
	if _, _, own := memberMetricOf(groupID, badItemID); own != 0 {
		t.Fatalf("被重置成员的观测应清空: samples=%d", own)
	}
}

// TestResetRejectsEmptyScope 作用域缺失时必须报错而不是静默清空全量指标。
func TestResetRejectsEmptyScope(t *testing.T) {
	if _, err := ResetModelMetric(0); err == nil {
		t.Fatalf("channel_model_id=0 应被拒绝")
	}
	if _, err := ResetMemberMetric(0, 0); err == nil {
		t.Fatalf("group_id/item_id=0 应被拒绝")
	}
}

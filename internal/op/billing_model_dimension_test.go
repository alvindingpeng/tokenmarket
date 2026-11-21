package op

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
)

// 本文件锁的是「按模型」读口在留存期之外仍可查这条能力:
// billing_rollups 增加第 6 个维度 model(user_id + model_name)之后, 详细明细被清理时
// 那一天按模型的用量/金额必须由汇总补齐, 而不是整段消失。
//
// 用户真正看得见的地方是 UserStatsModelBreakdown 的返回值(统计页"按模型"那一栏),
// 所以断言全部落在它和我们直接暴露的覆盖边界上, 不断言内部 map。

// seedModelRecord 造一条指定模型/时间的计费明细。
func seedModelRecord(t *testing.T, userID uint, modelName string, at time.Time, cost float64, tokens int64) model.BillingRecord {
	t.Helper()
	record := model.BillingRecord{
		UserID:     userID,
		ModelName:  modelName,
		Status:     model.BillingStatusSettled,
		UserCost:   cost,
		InputToken: tokens,
		CreatedAt:  at,
	}
	if err := db.GetDB().Create(&record).Error; err != nil {
		t.Fatalf("seed model record: %v", err)
	}
	return record
}

// modelBreakdownByModel 把结果按模型名索引, 便于逐字段比对"清理前后是否一致"。
func modelBreakdownByModel(rows []model.UserStatsModel) map[string]model.UserStatsModel {
	out := make(map[string]model.UserStatsModel, len(rows))
	for _, row := range rows {
		out[row.ModelName] = row
	}
	return out
}

// TestModelBreakdownSurvivesRetention 超期明细被清理后, 同一区间的"按模型"数字必须一分不少。
//
// 修前形态: model 维度不存在, 明细一删, 这一天的按模型结果整段归零 ——
// 而同一区间的总金额(概览/账单)因为有 user 维度兜底仍然有数, 用户看到的是
// "总金额有, 拆分没了", 且没有任何字段能解释。
func TestModelBreakdownSurvivesRetention(t *testing.T) {
	breakerTestSetup(t)
	_ = chdirTemp(t)
	withBillingSettings(t, 180, false)
	ctx := context.Background()

	const userID = uint(21)
	oldDay := billingRetentionCutoff(180).AddDate(0, 0, -30)
	seedModelRecord(t, userID, "alpha-model", oldDay.Add(9*time.Hour), 5, 40)
	seedModelRecord(t, userID, "alpha-model", oldDay.Add(10*time.Hour), 2, 60)
	seedModelRecord(t, userID, "beta-model", oldDay.Add(11*time.Hour), 3, 50)

	stamp := oldDay.Format("2006-01-02")
	before, err := UserStatsModelBreakdown(ctx, userID, stamp, stamp)
	if err != nil {
		t.Fatalf("model breakdown before: %v", err)
	}
	if len(before) != 2 {
		t.Fatalf("清理前应有 2 个模型, 实际 %+v", before)
	}
	beforeByModel := modelBreakdownByModel(before)
	if got := beforeByModel["alpha-model"]; got.Requests != 2 || got.Cost != 7 || got.Tokens != 100 {
		t.Fatalf("清理前 alpha-model = %+v", got)
	}
	if got := beforeByModel["beta-model"]; got.Requests != 1 || got.Cost != 3 || got.Tokens != 50 {
		t.Fatalf("清理前 beta-model = %+v", got)
	}
	coverageBefore, err := UserStatsModelCoverageStart(ctx, userID, stamp, stamp)
	if err != nil {
		t.Fatalf("model coverage before: %v", err)
	}
	if coverageBefore != stamp {
		t.Fatalf("清理前模型覆盖起点 = %q, want %s", coverageBefore, stamp)
	}

	// 清理: 这一天整日固化后删掉明细。
	result, err := BillingRetentionRun(ctx)
	if err != nil {
		t.Fatalf("billing retention: %v", err)
	}
	if result.DeletedRecords != 3 {
		t.Fatalf("删除明细 = %d, want 3", result.DeletedRecords)
	}
	var live int64
	if err := db.GetDB().Model(&model.BillingRecord{}).Where("user_id = ?", userID).Count(&live).Error; err != nil {
		t.Fatalf("count live: %v", err)
	}
	if live != 0 {
		t.Fatalf("明细应已被清理, 还剩 %d 行", live)
	}

	after, err := UserStatsModelBreakdown(ctx, userID, stamp, stamp)
	if err != nil {
		t.Fatalf("model breakdown after: %v", err)
	}
	afterByModel := modelBreakdownByModel(after)
	if len(after) != len(before) {
		t.Fatalf("清理后按模型结果整段消失(汇总没有 model 维度): before=%+v after=%+v", before, after)
	}
	for name, want := range beforeByModel {
		got, ok := afterByModel[name]
		if !ok {
			t.Fatalf("清理后缺少模型 %s: %+v", name, after)
		}
		if got.Requests != want.Requests || got.Cost != want.Cost || got.Tokens != want.Tokens {
			t.Fatalf("清理后 %s 数字变了: before=%+v after=%+v", name, want, got)
		}
	}
	coverageAfter, err := UserStatsModelCoverageStart(ctx, userID, stamp, stamp)
	if err != nil {
		t.Fatalf("model coverage after: %v", err)
	}
	if coverageAfter != stamp {
		t.Fatalf("清理后模型覆盖起点 = %q, want %s (汇总已覆盖这一天, 边界必须跟着前移)", coverageAfter, stamp)
	}
}

// TestModelRollupSkipsDisputedRecords 被未驳回申诉保护的明细永不固化, 但仍要算进"按模型"结果。
//
// 这是"同一自然日不会既在明细又在汇总里"的必要条件: 若给受保护的行也固化,
// 它既留在明细又进了汇总, 按天相加就会双计 —— 用户看到的金额凭空翻倍。
func TestModelRollupSkipsDisputedRecords(t *testing.T) {
	breakerTestSetup(t)
	_ = chdirTemp(t)
	withBillingSettings(t, 180, false)
	ctx := context.Background()

	const userID = uint(22)
	oldDay := billingRetentionCutoff(180).AddDate(0, 0, -30)
	disputed := seedModelRecord(t, userID, "alpha-model", oldDay.Add(9*time.Hour), 5, 40)
	seedModelRecord(t, userID, "beta-model", oldDay.Add(10*time.Hour), 3, 50)
	if err := db.GetDB().Create(&model.Dispute{
		RecordID: disputed.ID, UserID: userID, Status: model.DisputeStatusPending,
	}).Error; err != nil {
		t.Fatalf("seed dispute: %v", err)
	}

	stamp := oldDay.Format("2006-01-02")
	before, err := UserStatsModelBreakdown(ctx, userID, stamp, stamp)
	if err != nil {
		t.Fatalf("model breakdown before: %v", err)
	}
	beforeByModel := modelBreakdownByModel(before)
	if got := beforeByModel["alpha-model"]; got.Requests != 1 || got.Cost != 5 {
		t.Fatalf("清理前 alpha-model = %+v", got)
	}

	if _, err := BillingRetentionRun(ctx); err != nil {
		t.Fatalf("billing retention: %v", err)
	}
	// 受保护那一行必须还在明细里(退款路径要按 record_id 回查它), beta 被固化并删除。
	var live int64
	if err := db.GetDB().Model(&model.BillingRecord{}).Where("user_id = ?", userID).Count(&live).Error; err != nil {
		t.Fatalf("count live: %v", err)
	}
	if live != 1 {
		t.Fatalf("受申诉保护的行被删了: 剩余 %d 行", live)
	}
	// 受保护行不得出现在 model 汇总里(否则它会同时被明细与汇总各算一次)。
	var sealed int64
	if err := db.GetDB().Model(&model.BillingRollup{}).
		Where("dimension = ? AND dimension_id = ?", model.BillingRollupModel, userID).
		Count(&sealed).Error; err != nil {
		t.Fatalf("count model rollups: %v", err)
	}
	if sealed != 1 {
		t.Fatalf("model 汇总行 = %d, want 1 (只固化可删的 beta-model 那天)", sealed)
	}

	after, err := UserStatsModelBreakdown(ctx, userID, stamp, stamp)
	if err != nil {
		t.Fatalf("model breakdown after: %v", err)
	}
	afterByModel := modelBreakdownByModel(after)
	if got := afterByModel["alpha-model"]; got.Requests != 1 || got.Cost != 5 {
		t.Fatalf("受保护行的 alpha-model 结果变了(疑似被汇总双计): %+v", got)
	}
	if got := afterByModel["beta-model"]; got.Requests != 1 || got.Cost != 3 {
		t.Fatalf("被固化的 beta-model 未从汇总补回: %+v", got)
	}

	// 申诉被驳回后同一窗口变回可删: 再固化成汇总行时是"累加", 数字仍必须一致。
	if err := db.GetDB().Model(&model.Dispute{}).Where("user_id = ? AND record_id = ?", userID, disputed.ID).
		Update("status", model.DisputeStatusRejected).Error; err != nil {
		t.Fatalf("reject dispute: %v", err)
	}
	if _, err := BillingRetentionRun(ctx); err != nil {
		t.Fatalf("billing retention after rejection: %v", err)
	}
	final, err := UserStatsModelBreakdown(ctx, userID, stamp, stamp)
	if err != nil {
		t.Fatalf("model breakdown final: %v", err)
	}
	finalByModel := modelBreakdownByModel(final)
	if got := finalByModel["alpha-model"]; got.Requests != 1 || got.Cost != 5 {
		t.Fatalf("驳回后再固化, alpha-model = %+v", got)
	}
	if got := finalByModel["beta-model"]; got.Requests != 1 || got.Cost != 3 {
		t.Fatalf("驳回后 beta-model = %+v", got)
	}
}

// TestModelBreakdownDoesNotInventMissingHistory 迁移之前就已固化的日子没有 model 维度行,
// 那段历史确实补不回来 —— 读口必须如实缺报, 绝不能把缺失填成 0 或按比例摊到现存模型上。
//
// 用户可观察点: 结果的模型集合里不出现"凭空冒出来的旧模型", 且覆盖起点落在真正有模型数据的那天。
func TestModelBreakdownDoesNotInventMissingHistory(t *testing.T) {
	breakerTestSetup(t)
	_ = chdirTemp(t)
	withBillingSettings(t, 180, false)
	ctx := context.Background()

	const userID = uint(23)
	// 模拟"迁移前已固化"的一天: 只有 user 维度汇总, 没有 model 维度行。
	legacyDay := billingRetentionCutoff(180).AddDate(0, 0, -60)
	legacy := model.BillingRollup{
		Dimension: model.BillingRollupUser, DimensionID: userID,
		Day: legacyDay.Format("20060102"), InputToken: 900, RequestCount: 9, UserCost: 90,
		SealedAt: time.Now(),
	}
	if err := db.GetDB().Create(&legacy).Error; err != nil {
		t.Fatalf("seed legacy rollup: %v", err)
	}
	// 迁移之后才固化的另一天: 有 model 维度行。
	newDay := billingRetentionCutoff(180).AddDate(0, 0, -30)
	seedModelRecord(t, userID, "alpha-model", newDay.Add(9*time.Hour), 4, 60)
	if _, err := BillingRetentionRun(ctx); err != nil {
		t.Fatalf("billing retention: %v", err)
	}

	legacyStamp := legacyDay.Format("2006-01-02")
	legacyRows, err := UserStatsModelBreakdown(ctx, userID, legacyStamp, legacyStamp)
	if err != nil {
		t.Fatalf("legacy window: %v", err)
	}
	if len(legacyRows) != 0 {
		t.Fatalf("迁移前的日子没有 model 明细/汇总, 不得凭空造出模型行: %+v", legacyRows)
	}
	// 覆盖起点必须落在真正有模型数据的那天, 而不是 legacy 那天 —— 前端据此提示
	// "此前无模型明细", 用户不会把缺失读成 0 用量。
	spanStart := legacyStamp
	spanEnd := newDay.Format("2006-01-02")
	coverage, err := UserStatsModelCoverageStart(ctx, userID, spanStart, spanEnd)
	if err != nil {
		t.Fatalf("model coverage: %v", err)
	}
	if coverage != spanEnd {
		t.Fatalf("模型覆盖起点 = %q, want %s(早于它的那段确实补不回来)", coverage, spanEnd)
	}
	newRows, err := UserStatsModelBreakdown(ctx, userID, spanEnd, spanEnd)
	if err != nil {
		t.Fatalf("new window: %v", err)
	}
	if len(newRows) != 1 || newRows[0].ModelName != "alpha-model" || newRows[0].Cost != 4 || newRows[0].Tokens != 60 {
		t.Fatalf("新固化那天的模型行 = %+v", newRows)
	}
}

// TestAPIKeyCostSumDBSurvivesRetention 兜底聚合也必须并上 apikey 维度汇总。
//
// 这把 Key 的"累计支出"是 max_cost 上限的判定依据: 明细被清理后若它跟着缩水,
// 花得越多额度反而越高 —— 超支的 Key 悄悄恢复可用。这是钱的安全边界, 不是显示问题。
func TestAPIKeyCostSumDBSurvivesRetention(t *testing.T) {
	breakerTestSetup(t)
	_ = chdirTemp(t)
	withBillingSettings(t, 180, false)
	ctx := context.Background()

	const apiKeyID = 31
	oldAt := billingRetentionCutoff(180).AddDate(0, 0, -30).Add(9 * time.Hour)
	for i := 0; i < 2; i++ {
		if err := db.GetDB().Create(&model.BillingRecord{
			APIKeyID: apiKeyID, ModelName: "alpha-model", Status: model.BillingStatusSettled,
			UserCost: 5, CreatedAt: oldAt.Add(time.Duration(i) * time.Minute),
		}).Error; err != nil {
			t.Fatalf("seed apikey record: %v", err)
		}
	}
	// 退款行不计入(口径与明细侧 status='settled' 一致): 100 元退款不能被算成花费。
	if err := db.GetDB().Create(&model.BillingRecord{
		APIKeyID: apiKeyID, ModelName: "alpha-model", Status: model.BillingStatusRefunded,
		UserCost: 100, CreatedAt: oldAt.Add(2 * time.Minute),
	}).Error; err != nil {
		t.Fatalf("seed refunded record: %v", err)
	}

	before, err := APIKeyCostSumDB(ctx, apiKeyID)
	if err != nil {
		t.Fatalf("cost sum before: %v", err)
	}
	if before != 10 {
		t.Fatalf("清理前累计支出 = %v, want 10(退款行不计)", before)
	}

	if _, err := BillingRetentionRun(ctx); err != nil {
		t.Fatalf("billing retention: %v", err)
	}
	after, err := APIKeyCostSumDB(ctx, apiKeyID)
	if err != nil {
		t.Fatalf("cost sum after: %v", err)
	}
	if after != before {
		t.Fatalf("清理后累计支出缩水: %v -> %v —— max_cost 兜底失效, 超支 Key 会恢复可用", before, after)
	}
}

// TestModelRollupQueryUsesIndex 「按模型」的读法必须是索引区间扫描, 不能退化成全表扫描。
//
// 为什么值得单独锁: model 维度没有自己的索引, 它靠 (dimension, dimension_id, day, model_name)
// 这个唯一键 + 列序恰好覆盖查询条件。哪天有人调整了唯一键的列序(比如把 model_name 提到第二位),
// 这条查询会静默变成全表扫描 —— 汇总表按天增长, 扫全表会把统计页拖慢到不可用,
// 而这种退化不会报错, 只会慢。
func TestModelRollupQueryUsesIndex(t *testing.T) {
	breakerTestSetup(t)
	_ = chdirTemp(t)
	withBillingSettings(t, 180, false)
	ctx := context.Background()

	const userID = uint(24)
	oldDay := billingRetentionCutoff(180).AddDate(0, 0, -30)
	seedModelRecord(t, userID, "alpha-model", oldDay.Add(9*time.Hour), 5, 40)
	seedModelRecord(t, userID, "beta-model", oldDay.Add(10*time.Hour), 3, 50)
	if _, err := BillingRetentionRun(ctx); err != nil {
		t.Fatalf("billing retention: %v", err)
	}

	day := oldDay.Format("20060102")
	sqlText := db.GetDB().ToSQL(func(tx *gorm.DB) *gorm.DB {
		var rows []model.BillingRollup
		return tx.Model(&model.BillingRollup{}).
			Where("dimension = ? AND dimension_id = ? AND model_name <> ?", model.BillingRollupModel, userID, "").
			Where("day >= ? AND day <= ?", day, day).
			Find(&rows)
	})
	var plan []struct{ Detail string }
	if err := db.GetDB().Raw("EXPLAIN QUERY PLAN " + sqlText).Scan(&plan).Error; err != nil {
		t.Fatalf("explain model rollup query: %v", err)
	}
	joined := ""
	for _, row := range plan {
		joined += row.Detail + "\n"
	}
	t.Logf("model rollup query plan:\n%s", joined)
	if strings.Contains(joined, "SCAN billing_rollups") {
		t.Fatalf("按模型的汇总查询退化成全表扫描:\n%s", joined)
	}
	if !strings.Contains(joined, "SEARCH billing_rollups") {
		t.Fatalf("按模型的汇总查询不是索引 SEARCH:\n%s", joined)
	}
	if !strings.Contains(joined, "idx_billing_rollup_key") && !strings.Contains(joined, "idx_billing_rollup_dim_day") {
		t.Fatalf("按模型的汇总查询没有走汇总表索引:\n%s", joined)
	}
}

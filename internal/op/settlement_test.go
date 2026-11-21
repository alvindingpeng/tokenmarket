package op

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
)

// 本文件钉死日结结算单的账目口径(docs/financial-hardening-phase1.md "日结退款口径"):
//   - 毛额按调用发生日入账(原账单 owner_revenue), 事后退款不改写当日毛额;
//   - 退款按"批准日"冲回原账单的供应商应收份额 owner_revenue, 不是买家支付额 user_cost;
//   - 退款日没有新收入的渠道商也出调整单, 净额为负结转, 不截断为零;
//   - 重新生成只作用于未审核单, 已审核单不被覆盖且差异要报错;
//   - 账期边界与 ReconcileDay 一致: 服务进程本地自然日, 一天按日历加一天。

// settlementRequestSeq 造测试计费明细用的请求号来源。
var settlementRequestSeq uint64 = 900000

// settlementTestDB 初始化隔离库, 建买家/渠道商/管理员各一个。
func settlementTestDB(t *testing.T, name string) (buyer, owner, admin model.User) {
	t.Helper()
	// 换库前清空进程级缓存, 否则 -shuffle 下会读到上一个用例的旧库数据。
	resetProcessCaches()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "settlement.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	buyer = model.User{Username: name + "-buyer", Role: model.RoleUser, Balance: 1000, Status: model.StatusActive}
	if err := db.GetDB().Create(&buyer).Error; err != nil {
		t.Fatalf("create buyer: %v", err)
	}
	owner = model.User{Username: name + "-owner", Role: model.RoleReseller, Status: model.StatusActive}
	if err := db.GetDB().Create(&owner).Error; err != nil {
		t.Fatalf("create owner: %v", err)
	}
	admin = model.User{Username: name + "-admin", Role: model.RoleAdmin, Status: model.StatusActive}
	if err := db.GetDB().Create(&admin).Error; err != nil {
		t.Fatalf("create admin: %v", err)
	}
	return buyer, owner, admin
}

// settlementAddOwner 再建一个渠道商(多渠道商场景)。
func settlementAddOwner(t *testing.T, name string) model.User {
	t.Helper()
	owner := model.User{Username: name, Role: model.RoleReseller, Status: model.StatusActive}
	if err := db.GetDB().Create(&owner).Error; err != nil {
		t.Fatalf("create owner: %v", err)
	}
	return owner
}

// settlementRecord 落一条计费明细: user_cost = owner_revenue + platform_revenue 三方对平。
func settlementRecord(t *testing.T, buyerID, ownerID uint, createdAt time.Time, userCost, ownerRevenue, markupRatio float64) model.BillingRecord {
	t.Helper()
	settlementRequestSeq++
	record := model.BillingRecord{
		RequestID:       settlementRequestSeq,
		UserID:          buyerID,
		OwnerID:         ownerID,
		ModelName:       "gpt-4o",
		GroupModel:      "gpt-4o",
		UserCost:        userCost,
		OwnerRevenue:    ownerRevenue,
		PlatformRevenue: userCost - ownerRevenue,
		MarkupRatio:     markupRatio,
		InputToken:      10,
		OutputToken:     20,
		Status:          model.BillingStatusSettled,
		CreatedAt:       createdAt,
	}
	if err := db.GetDB().Create(&record).Error; err != nil {
		t.Fatalf("create billing record: %v", err)
	}
	return record
}

// settlementRefund 走真实退款链路(申诉 → 管理员批准), 并把批准时间挪到指定的"批准日"。
func settlementRefund(t *testing.T, buyerID, adminID uint, record model.BillingRecord, approvedAt time.Time) {
	t.Helper()
	ctx := context.Background()
	dispute, err := DisputeCreate(ctx, buyerID, record.ID, "输出不达标, 申请退款")
	if err != nil {
		t.Fatalf("DisputeCreate: %v", err)
	}
	if err := DisputeReview(ctx, dispute.ID, true, "核实属实同意退款", adminID, adminUsername); err != nil {
		t.Fatalf("DisputeReview: %v", err)
	}
	if err := db.GetDB().Model(&model.Dispute{}).Where("id = ?", dispute.ID).
		Updates(map[string]any{"reviewed_at": approvedAt, "updated_at": approvedAt}).Error; err != nil {
		t.Fatalf("backdate reviewed_at: %v", err)
	}
}

// adminUsername 退款审核人展示名。
const adminUsername = "settlement-admin"

// settlementMustGet 取某渠道商某账期的结算单。
func settlementMustGet(t *testing.T, ownerID uint, day string) model.SettlementStatement {
	t.Helper()
	var stmt model.SettlementStatement
	if err := db.GetDB().Where("owner_id = ? AND period = ?", ownerID, day).First(&stmt).Error; err != nil {
		t.Fatalf("load statement owner=%d day=%s: %v", ownerID, day, err)
	}
	return stmt
}

// settlementAssertAmounts 断言结算单三金额。
func settlementAssertAmounts(t *testing.T, stmt model.SettlementStatement, gross, refund, net float64) {
	t.Helper()
	if stmt.GrossRevenue != gross || stmt.RefundDeduction != refund || stmt.NetRevenue != net {
		t.Fatalf("statement %s amounts = (%v, %v, %v), want gross=%v refund=%v net=%v",
			stmt.StatementNo, stmt.GrossRevenue, stmt.RefundDeduction, stmt.NetRevenue, gross, refund, net)
	}
}

// TestSettlementRefundDeductsOwnerShareNotUserCost 退款冲回的是供应商应收份额, 不是买家支付额:
// 有上浮(markup)时 user_cost = owner_revenue + platform_revenue, 用 user_cost 扣渠道商应收
// 会把平台的上浮部分也算到渠道商头上(毛额按 owner_revenue 记, 扣减却按 user_cost 记, 口径错配)。
func TestSettlementRefundDeductsOwnerShareNotUserCost(t *testing.T) {
	buyer, owner, admin := settlementTestDB(t, "markup")
	day := time.Date(2025, 3, 1, 0, 0, 0, 0, time.Local)
	// 上浮 50%: 供货 6, 平台毛利 4, 用户支付 10。
	record := settlementRecord(t, buyer.ID, owner.ID, day.Add(10*time.Hour), 10, 6, 0.5)
	settlementRefund(t, buyer.ID, admin.ID, record, day.Add(15*time.Hour))

	if err := SettlementGenerateDay(context.Background(), day.Format("20060102")); err != nil {
		t.Fatalf("SettlementGenerateDay: %v", err)
	}
	stmt := settlementMustGet(t, owner.ID, day.Format("20060102"))
	// 毛额 6(调用日) - 冲回 6(批准日, 原账单 owner_revenue) = 0;买家那 10(user_cost)是用户侧退款, 不进渠道商账。
	settlementAssertAmounts(t, stmt, 6, 6, 0)
	if stmt.RefundDeduction == record.UserCost {
		t.Fatalf("refund deduction must not use user_cost: %+v", stmt)
	}
	if stmt.RequestCount != 1 || stmt.InputToken != 10 || stmt.OutputToken != 20 {
		t.Fatalf("unexpected statement counters: %+v", stmt)
	}
}

// TestSettlementCrossDayRefundAdjustsApprovalDay 跨天退款: 毛额留在调用日, 冲回记在批准日,
// 且批准日的净额可以为负并结转(不截断为零, 否则债务被悄悄丢掉)。
func TestSettlementCrossDayRefundAdjustsApprovalDay(t *testing.T) {
	buyer, owner, admin := settlementTestDB(t, "crossday")
	callDay := time.Date(2025, 3, 2, 0, 0, 0, 0, time.Local)
	refundDay := time.Date(2025, 3, 5, 0, 0, 0, 0, time.Local)
	record := settlementRecord(t, buyer.ID, owner.ID, callDay.Add(9*time.Hour), 10, 6, 0.5)
	settlementRefund(t, buyer.ID, admin.ID, record, refundDay.Add(11*time.Hour))

	ctx := context.Background()
	if err := SettlementGenerateDay(ctx, callDay.Format("20060102")); err != nil {
		t.Fatalf("generate call day: %v", err)
	}
	if err := SettlementGenerateDay(ctx, refundDay.Format("20060102")); err != nil {
		t.Fatalf("generate refund day: %v", err)
	}

	callStmt := settlementMustGet(t, owner.ID, callDay.Format("20060102"))
	// 调用日毛额保持 6: 退款是另一个交易事件, 不回头改写已出账的毛额。
	settlementAssertAmounts(t, callStmt, 6, 0, 6)

	refundStmt := settlementMustGet(t, owner.ID, refundDay.Format("20060102"))
	// 批准日只有冲回: 净额 -6 结转。
	settlementAssertAmounts(t, refundStmt, 0, 6, -6)
	if refundStmt.RequestCount != 0 {
		t.Fatalf("refund-only statement must not carry request counters: %+v", refundStmt)
	}

	// 重跑调用日: 毛额口径不因退款漂移。
	if err := SettlementGenerateDay(ctx, callDay.Format("20060102")); err != nil {
		t.Fatalf("regenerate call day: %v", err)
	}
	settlementAssertAmounts(t, settlementMustGet(t, owner.ID, callDay.Format("20060102")), 6, 0, 6)
}

// TestSettlementRefundOnlyOwnerGetsAdjustment 退款日没有新收入的渠道商也必须有调整单:
// 旧实现只遍历当日有计费明细的渠道商, 退款日只出一张"负净额调整单"的场景被整个丢掉。
func TestSettlementRefundOnlyOwnerGetsAdjustment(t *testing.T) {
	buyer, owner, admin := settlementTestDB(t, "refundonly")
	refundDay := time.Date(2025, 3, 5, 0, 0, 0, 0, time.Local)
	record := settlementRecord(t, buyer.ID, owner.ID, refundDay.AddDate(0, 0, -3).Add(9*time.Hour), 10, 6, 0.5)
	settlementRefund(t, buyer.ID, admin.ID, record, refundDay.Add(11*time.Hour))

	// 只生成退款当天: 渠道商当天没有任何计费明细。
	if err := SettlementGenerateDay(context.Background(), refundDay.Format("20060102")); err != nil {
		t.Fatalf("SettlementGenerateDay: %v", err)
	}
	var count int64
	if err := db.GetDB().Model(&model.SettlementStatement{}).Where("owner_id = ?", owner.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("statement count = %d, want 1 (refund-only owner must get an adjustment statement)", count)
	}
	stmt := settlementMustGet(t, owner.ID, refundDay.Format("20060102"))
	settlementAssertAmounts(t, stmt, 0, 6, -6)
	if stmt.Status != settlementStatusSettled {
		t.Fatalf("unexpected statement status: %s", stmt.Status)
	}
}

// TestSettlementRerunFreezesAuditedStatements 重新生成只作用于未审核单:
//   - 定稿单与重算值一致: 原样保留, 其余未审核单照常刷新;
//   - 定稿单与重算值有差异: 不静默覆盖, 整次生成被拒绝(一行不写), 报错要求人工留调整凭证。
func TestSettlementRerunFreezesAuditedStatements(t *testing.T) {
	buyer, owner, admin := settlementTestDB(t, "audited")
	ownerB := settlementAddOwner(t, "audited-owner-b")
	_ = admin
	day := time.Date(2025, 4, 1, 0, 0, 0, 0, time.Local)
	ctx := context.Background()
	dayKey := day.Format("20060102")

	settlementRecord(t, buyer.ID, owner.ID, day.Add(9*time.Hour), 150, 100, 0.5)
	if err := SettlementGenerateDay(ctx, dayKey); err != nil {
		t.Fatalf("first generate: %v", err)
	}
	settlementAssertAmounts(t, settlementMustGet(t, owner.ID, dayKey), 100, 0, 100)

	// 管理员把该单审核定稿。
	audited := settlementMustGet(t, owner.ID, dayKey)
	if err := db.GetDB().Model(&model.SettlementStatement{}).Where("id = ?", audited.ID).
		Update("status", settlementStatusAudited).Error; err != nil {
		t.Fatalf("mark audited: %v", err)
	}

	// ① 定稿单的数字没变: 重跑照常刷新未审核单(重新生成只作用于未审核单), 定稿单一个字段都不写。
	settlementRecord(t, buyer.ID, ownerB.ID, day.Add(12*time.Hour), 75, 50, 0.5)
	if err := SettlementGenerateDay(ctx, dayKey); err != nil {
		t.Fatalf("rerun without audited difference must succeed: %v", err)
	}
	frozen := settlementMustGet(t, owner.ID, dayKey)
	settlementAssertAmounts(t, frozen, 100, 0, 100)
	if frozen.Status != settlementStatusAudited {
		t.Fatalf("audited statement status changed: %s", frozen.Status)
	}
	settlementAssertAmounts(t, settlementMustGet(t, ownerB.ID, dayKey), 50, 0, 50)

	// ② 又给定稿渠道商补了明细(100 → 130): 重算值与定稿冲突, 不静默覆盖, 整次生成拒绝且一行不写。
	settlementRecord(t, buyer.ID, owner.ID, day.Add(11*time.Hour), 45, 30, 0.5)
	settlementRecord(t, buyer.ID, ownerB.ID, day.Add(13*time.Hour), 30, 20, 0.5)
	err := SettlementGenerateDay(ctx, dayKey)
	if err == nil {
		t.Fatal("regenerating over a changed audited statement must report an error")
	}
	if !strings.Contains(err.Error(), audited.StatementNo) || !strings.Contains(err.Error(), "audited") {
		t.Fatalf("error must name the frozen audited statement: %v", err)
	}
	// 定稿单没动; 审核冲突整批回滚, 其他单据也不得被部分更新/创建。
	settlementAssertAmounts(t, settlementMustGet(t, owner.ID, dayKey), 100, 0, 100)
	settlementAssertAmounts(t, settlementMustGet(t, ownerB.ID, dayKey), 50, 0, 50)
}

// TestSettlementRerunIsIdempotent 未审核单重跑幂等: 数字不漂移, 不重复建单, 不报错。
func TestSettlementRerunIsIdempotent(t *testing.T) {
	buyer, owner, admin := settlementTestDB(t, "rerun")
	day := time.Date(2025, 4, 2, 0, 0, 0, 0, time.Local)
	ctx := context.Background()
	dayKey := day.Format("20060102")

	settlementRecord(t, buyer.ID, owner.ID, day.Add(9*time.Hour), 150, 100, 0.5)
	record := settlementRecord(t, buyer.ID, owner.ID, day.Add(10*time.Hour), 30, 20, 0.5)
	settlementRefund(t, buyer.ID, admin.ID, record, day.Add(16*time.Hour))

	if err := SettlementGenerateDay(ctx, dayKey); err != nil {
		t.Fatalf("first generate: %v", err)
	}
	first := settlementMustGet(t, owner.ID, dayKey)
	settlementAssertAmounts(t, first, 120, 20, 100)

	if err := SettlementGenerateDay(ctx, dayKey); err != nil {
		t.Fatalf("rerun must be idempotent, got: %v", err)
	}
	second := settlementMustGet(t, owner.ID, dayKey)
	settlementAssertAmounts(t, second, 120, 20, 100)
	if second.ID != first.ID || !second.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("rerun must not rebuild the statement: first=%+v second=%+v", first, second)
	}
	var count int64
	if err := db.GetDB().Model(&model.SettlementStatement{}).Where("owner_id = ?", owner.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("statement count = %d, want 1", count)
	}
}

// TestSettlementRerunPicksUpLateRecords 结算单只由管理员手工点「生成」触发, 于是当天生成过
// 一次之后, 再晚进来的调用就永远落在单据之外 —— 生产 20261016 那单就是这么少了 41 笔
// 0.998436396(单据 12:09:29 生成, 当天最后一批计费发生在 13:32)。
// 未审核单必须能被重算刷新到全天真实数字; 这是每日重算任务存在的理由。
func TestSettlementRerunPicksUpLateRecords(t *testing.T) {
	buyer, owner, admin := settlementTestDB(t, "late")
	day := time.Date(2025, 4, 3, 0, 0, 0, 0, time.Local)
	ctx := context.Background()
	dayKey := day.Format("20060102")
	_ = admin

	// 当天 12:09 生成单据时只有前半天的账。
	settlementRecord(t, buyer.ID, owner.ID, day.Add(10*time.Hour), 150, 100, 0.5)
	if err := SettlementGenerateDay(ctx, dayKey); err != nil {
		t.Fatalf("first generate: %v", err)
	}
	first := settlementMustGet(t, owner.ID, dayKey)
	settlementAssertAmounts(t, first, 100, 0, 100)
	if first.RequestCount != 1 {
		t.Fatalf("first statement requests = %d, want 1", first.RequestCount)
	}

	// 生成之后当天仍有调用进来。
	settlementRecord(t, buyer.ID, owner.ID, day.Add(13*time.Hour), 45, 30, 0.5)
	if err := SettlementGenerateDay(ctx, dayKey); err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	second := settlementMustGet(t, owner.ID, dayKey)
	settlementAssertAmounts(t, second, 130, 0, 130)
	if second.RequestCount != 2 {
		t.Fatalf("regenerated statement requests = %d, want 2 (late record must be included)", second.RequestCount)
	}
	// 刷新是就地改写同一张单: 单号与身份字段不动, 渠道商不会被凭空多出一张单。
	if second.StatementNo != first.StatementNo || second.ID != first.ID {
		t.Fatalf("regenerate must refresh in place: first=%+v second=%+v", first, second)
	}
}

// TestSettlementLocalDayBoundaryMatchesReconcile 账期边界与 ReconcileDay 同口径:
// 服务进程本地自然日, 不是 UTC 日界(否则 UTC+8 下凌晨的调用会被切进前一天, 与对账对不上)。
func TestSettlementLocalDayBoundaryMatchesReconcile(t *testing.T) {
	// 固定 +8 时区锁定行为, 不依赖运行环境 TZ。
	original := time.Local
	time.Local = time.FixedZone("CST", 8*3600)
	defer func() { time.Local = original }()

	buyer, owner, _ := settlementTestDB(t, "boundary")
	day := time.Date(2025, 3, 5, 0, 0, 0, 0, time.Local)
	ctx := context.Background()
	dayKey := day.Format("20060102")

	// 本地 01:00(UTC 仍是 3/4)与本地 23:30 都属于 3/5; 本地 3/6 00:30 不属于。
	settlementRecord(t, buyer.ID, owner.ID, day.Add(1*time.Hour), 45, 3, 0.5)
	settlementRecord(t, buyer.ID, owner.ID, day.Add(23*time.Hour+30*time.Minute), 75, 5, 0.5)
	settlementRecord(t, buyer.ID, owner.ID, day.AddDate(0, 0, 1).Add(30*time.Minute), 105, 7, 0.5)

	start, end, err := settlementDayWindow(dayKey)
	if err != nil {
		t.Fatalf("settlementDayWindow: %v", err)
	}
	if !start.Equal(day) || !end.Equal(day.AddDate(0, 0, 1)) {
		t.Fatalf("window = [%v, %v), want local calendar day", start, end)
	}

	if err := SettlementGenerateDay(ctx, dayKey); err != nil {
		t.Fatalf("generate day: %v", err)
	}
	if err := SettlementGenerateDay(ctx, day.AddDate(0, 0, 1).Format("20060102")); err != nil {
		t.Fatalf("generate next day: %v", err)
	}

	today := settlementMustGet(t, owner.ID, dayKey)
	settlementAssertAmounts(t, today, 8, 0, 8)
	if today.RequestCount != 2 {
		t.Fatalf("request_count = %d, want 2 (local day window)", today.RequestCount)
	}
	next := settlementMustGet(t, owner.ID, day.AddDate(0, 0, 1).Format("20060102"))
	settlementAssertAmounts(t, next, 7, 0, 7)
	if next.RequestCount != 1 {
		t.Fatalf("request_count = %d, want 1", next.RequestCount)
	}
}

// 本文件末尾新增: 经营毛利看板的净额口径(已退款不计入 GMV/毛利)与趋势桶的方言无关性。
// 对应 task-34: A 退款冲减、B 利润趋势硬编码 strftime。

// marginApprox 浮点比较(金额来自 SQL 的 SUM, 末位可能有 1e-12 级误差)。
func marginApprox(t *testing.T, label string, got, want float64) {
	t.Helper()
	if got < want-1e-9 || got > want+1e-9 {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
}

// TestProfitMarginNetExcludesRefunds 看板金额必须是"未退款净额":
// 退了款的那笔钱已经回到用户手里, 平台既没收到 GMV 也没有毛利, 只能出现在 refund_amount 里。
// 与渠道毛利(channel_profit.go 的 status = settled)同口径, 两张看板才不会对不上。
func TestProfitMarginNetExcludesRefunds(t *testing.T) {
	buyer, owner, admin := settlementTestDB(t, "margin-net")
	ctx := context.Background()
	day := time.Date(2026, 3, 5, 12, 0, 0, 0, time.Local)

	settlementRecord(t, buyer.ID, owner.ID, day, 10, 6, 0.5)
	refunded := settlementRecord(t, buyer.ID, owner.ID, day, 10, 6, 0.5)
	settlementRefund(t, buyer.ID, admin.ID, refunded, day)

	overall, trends, err := ProfitMarginGet(ctx, ProfitMarginQuery{})
	if err != nil {
		t.Fatalf("ProfitMarginGet: %v", err)
	}
	// 只留 settled 那条: 10/6/4;退款那条(同为 10/6/4)必须整体消失。
	marginApprox(t, "overall.UserCost", overall.UserCost, 10)
	marginApprox(t, "overall.OwnerRevenue", overall.OwnerRevenue, 6)
	marginApprox(t, "overall.PlatformRevenue", overall.PlatformRevenue, 4)
	// 请求数不随退款减少: 调用确实发生过, 退款改的是资金口径。
	if overall.RequestCount != 2 {
		t.Fatalf("overall.RequestCount = %d, want 2 (退款不改变请求发生过的事实)", overall.RequestCount)
	}
	marginApprox(t, "overall.RefundAmount", overall.RefundAmount, 10)
	// 毛利率分母也是净额: 4/10 = 40%, 若仍用毛额 20 会算成 20%。
	marginApprox(t, "overall.MarginRate", overall.MarginRate, 40)

	if len(trends) != 1 {
		t.Fatalf("trends = %d, want 1 (同日两条明细只有一个日桶)", len(trends))
	}
	if trends[0].Period != day.Format("20060102") {
		t.Fatalf("trend day = %q, want YYYYMMDD %q", trends[0].Period, day.Format("20060102"))
	}
	// 趋势侧与合计侧口径必须一致, 否则图上每天的柱子加起来不等于合计。
	marginApprox(t, "trend.UserCost", trends[0].UserCost, 10)
	marginApprox(t, "trend.PlatformRevenue", trends[0].PlatformRevenue, 4)
	marginApprox(t, "trend.MarginRate", trends[0].MarginRate, 40)
}

// TestProfitMarginNetEqualsGrossWithoutRefunds 对照用例: 没有退款时净额必须与全额完全相同,
// 保证"减一次退款"没有变成"减两次"或"漏算"。
func TestProfitMarginNetEqualsGrossWithoutRefunds(t *testing.T) {
	buyer, owner, _ := settlementTestDB(t, "margin-gross")
	ctx := context.Background()
	day := time.Date(2026, 3, 6, 12, 0, 0, 0, time.Local)

	settlementRecord(t, buyer.ID, owner.ID, day, 10, 6, 0.5)
	settlementRecord(t, buyer.ID, owner.ID, day, 20, 12, 0.5)

	overall, _, err := ProfitMarginGet(ctx, ProfitMarginQuery{})
	if err != nil {
		t.Fatalf("ProfitMarginGet: %v", err)
	}
	marginApprox(t, "overall.UserCost", overall.UserCost, 30)
	marginApprox(t, "overall.OwnerRevenue", overall.OwnerRevenue, 18)
	marginApprox(t, "overall.PlatformRevenue", overall.PlatformRevenue, 12)
	marginApprox(t, "overall.RefundAmount", overall.RefundAmount, 0)
	marginApprox(t, "overall.MarginRate", overall.MarginRate, 40)
	if overall.RequestCount != 2 {
		t.Fatalf("overall.RequestCount = %d, want 2", overall.RequestCount)
	}
}

// TestProfitMarginTrendBucketsAreDialectNeutral 趋势只断言"行为"不断言 SQL 字符串:
// 两天数据必须出两个 YYYYMMDD 桶 —— 硬编码 strftime 的旧写法在 MySQL/PG 上会直接失败,
// 那时这里拿到的是空切片(而不是两个桶), 用例就会红。
func TestProfitMarginTrendBucketsAreDialectNeutral(t *testing.T) {
	buyer, owner, admin := settlementTestDB(t, "margin-trend")
	ctx := context.Background()
	day1 := time.Date(2026, 3, 7, 12, 0, 0, 0, time.Local)
	day2 := day1.AddDate(0, 0, 1)

	settlementRecord(t, buyer.ID, owner.ID, day1, 10, 6, 0.5)
	settlementRecord(t, buyer.ID, owner.ID, day2, 10, 6, 0.5)
	refunded := settlementRecord(t, buyer.ID, owner.ID, day2, 10, 6, 0.5)
	settlementRefund(t, buyer.ID, admin.ID, refunded, day2)

	overall, trends, err := ProfitMarginGet(ctx, ProfitMarginQuery{})
	if err != nil {
		t.Fatalf("ProfitMarginGet: %v", err)
	}
	if len(trends) != 2 {
		t.Fatalf("trends = %d, want 2 (两天各一个日桶; 空切片说明日桶表达式在该方言下失败了)", len(trends))
	}
	// 趋势按日倒序(沿用旧契约): 最新的在前。
	if trends[0].Period != day2.Format("20060102") || trends[1].Period != day1.Format("20060102") {
		t.Fatalf("trend periods = [%q %q], want [%q %q] (倒序, YYYYMMDD)",
			trends[0].Period, trends[1].Period, day2.Format("20060102"), day1.Format("20060102"))
	}
	for i, trend := range trends {
		if _, err := time.Parse("20060102", trend.Period); err != nil {
			t.Fatalf("trend[%d].Period = %q 不是 YYYYMMDD: %v", i, trend.Period, err)
		}
	}
	// 日桶也要净额: day2 有两条记录但退了其中一条。
	marginApprox(t, "trend[0].UserCost", trends[0].UserCost, 10)
	marginApprox(t, "trend[1].UserCost", trends[1].UserCost, 10)
	// 两个日桶相加 == 合计(趋势与合计同口径的硬约束)。
	marginApprox(t, "sum(trend.UserCost)", trends[0].UserCost+trends[1].UserCost, overall.UserCost)
	marginApprox(t, "sum(trend.PlatformRevenue)", trends[0].PlatformRevenue+trends[1].PlatformRevenue, overall.PlatformRevenue)
}

// TestProfitMarginNetSurvivesRetention 留存清理把超期明细归档删除之后, 净额不能缩水:
// 汇总表存"合计 + 其中退款"两组列, 读口用 full - refunded 还原净额, 加法在留存边界上才成立。
func TestProfitMarginNetSurvivesRetention(t *testing.T) {
	buyer, owner, admin := settlementTestDB(t, "margin-retention")
	_ = chdirTemp(t)
	withBillingSettings(t, 30, true)
	ctx := context.Background()
	now := time.Now()
	old := now.AddDate(0, 0, -400)

	// 超期的一条 settled + 一条已退款(退款行受申诉保护, 明细不会被删, 留在明细侧算净额 0)。
	settlementRecord(t, buyer.ID, owner.ID, old, 10, 6, 0.5)
	oldRefunded := settlementRecord(t, buyer.ID, owner.ID, old, 10, 6, 0.5)
	settlementRefund(t, buyer.ID, admin.ID, oldRefunded, old)
	// 近期的一条留在明细里, 与汇总侧拼成同一条曲线。
	settlementRecord(t, buyer.ID, owner.ID, now, 5, 3, 0.5)

	before, _, err := ProfitMarginGet(ctx, ProfitMarginQuery{})
	if err != nil {
		t.Fatalf("ProfitMarginGet before retention: %v", err)
	}
	marginApprox(t, "before.UserCost", before.UserCost, 15) // 10(settled) + 0(退款净额) + 5

	result, err := BillingRetentionRun(ctx)
	if err != nil {
		t.Fatalf("BillingRetentionRun: %v", err)
	}
	if result.DeletedRecords != 1 {
		t.Fatalf("DeletedRecords = %d, want 1 (只有超期 settled 那条可删; 退款行有申诉保护)", result.DeletedRecords)
	}

	after, trends, err := ProfitMarginGet(ctx, ProfitMarginQuery{})
	if err != nil {
		t.Fatalf("ProfitMarginGet after retention: %v", err)
	}
	marginApprox(t, "after.UserCost", after.UserCost, before.UserCost)
	marginApprox(t, "after.OwnerRevenue", after.OwnerRevenue, before.OwnerRevenue)
	marginApprox(t, "after.PlatformRevenue", after.PlatformRevenue, before.PlatformRevenue)
	if after.RequestCount != before.RequestCount {
		t.Fatalf("RequestCount shrank: before=%d after=%d (汇总侧没有补上被删的明细)", before.RequestCount, after.RequestCount)
	}
	// 归档掉的那一天必须仍在趋势里(汇总桶补回), 否则图上会出现"那天没有收入"的假空洞。
	found := false
	for _, trend := range trends {
		if trend.Period == dayKey(old) {
			found = true
		}
	}
	if !found {
		t.Fatalf("trend 里缺少已归档的 %s; trends=%v", dayKey(old), trends)
	}
}

// TestProfitMarginRollupSubtractsRefundedColumns 定点验证汇总侧的组合算法:
// 汇总行存 "合计 10 + 其中退款 4", 毛利看板必须读到 6 —— 用生产侧同一个写入口
// (sealBillingRollup) 造行, 而不是手写 SQL, 保证列的含义与真实固化路径一致。
func TestProfitMarginRollupSubtractsRefundedColumns(t *testing.T) {
	settlementTestDB(t, "margin-rollup")
	ctx := context.Background()
	day := time.Date(2026, 3, 8, 0, 0, 0, 0, time.Local)

	row := model.BillingRollup{
		Dimension:               model.BillingRollupPlatform,
		DimensionID:             0,
		Day:                     day.Format("20060102"),
		RequestCount:            2,
		UserCost:                10,
		OwnerRevenue:            6,
		PlatformRevenue:         4,
		RefundedRecords:         1,
		RefundedUserCost:        4,
		RefundedOwnerRevenue:    2.4,
		RefundedPlatformRevenue: 1.6,
	}
	if err := db.GetDB().Transaction(func(tx *gorm.DB) error {
		return sealBillingRollup(tx, &row)
	}); err != nil {
		t.Fatalf("seal rollup: %v", err)
	}

	overall, _, err := ProfitMarginGet(ctx, ProfitMarginQuery{})
	if err != nil {
		t.Fatalf("ProfitMarginGet: %v", err)
	}
	marginApprox(t, "overall.UserCost", overall.UserCost, 6)
	marginApprox(t, "overall.OwnerRevenue", overall.OwnerRevenue, 3.6)
	marginApprox(t, "overall.PlatformRevenue", overall.PlatformRevenue, 2.4)
	// 请求数读全额(与明细侧 COUNT(*) 一致), 不按 RefundedRecords 扣。
	if overall.RequestCount != 2 {
		t.Fatalf("overall.RequestCount = %d, want 2 (汇总侧请求数为全额)", overall.RequestCount)
	}
}

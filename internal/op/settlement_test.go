package op

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
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

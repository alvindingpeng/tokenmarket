package op

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

func TestTradeDisputeAndSettlementLoop(t *testing.T) {
	tempDir := t.TempDir()
	if err := db.InitDB("sqlite", filepath.Join(tempDir, "trade.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}

	ctx := context.Background()
	// 1. 建立测试用户: user1 (买家, 余额 100), owner1 (渠道商, 余额 0, 收入 0), admin1
	user1 := model.User{Username: "buyer1", Role: model.RoleUser, Balance: 100, TotalSpent: 0, Status: model.StatusActive}
	if err := db.GetDB().Create(&user1).Error; err != nil {
		t.Fatal(err)
	}
	owner1 := model.User{Username: "reseller1", Role: model.RoleReseller, Balance: 0, TotalRevenue: 0, Status: model.StatusActive}
	if err := db.GetDB().Create(&owner1).Error; err != nil {
		t.Fatal(err)
	}
	admin1 := model.User{Username: "admin1", Role: model.RoleAdmin, Status: model.StatusActive}
	if err := db.GetDB().Create(&admin1).Error; err != nil {
		t.Fatal(err)
	}

	// 2. 模拟一笔已结算调用: user_cost = 10, owner_revenue = 6, platform_revenue = 4
	reqID := uint64(99901)
	relayReq := model.RelayRequest{
		ID:        reqID,
		UserID:    user1.ID,
		Model:     "gpt-4o",
		Status:    "success",
		StartedAt: time.Now(),
	}
	if err := db.GetDB().Create(&relayReq).Error; err != nil {
		t.Fatal(err)
	}
	record := model.BillingRecord{
		RequestID:       reqID,
		UserID:          user1.ID,
		OwnerID:         owner1.ID,
		ModelName:       "gpt-4o",
		GroupModel:      "gpt-4o",
		UserCost:        10.0,
		OwnerRevenue:    6.0,
		PlatformRevenue: 4.0,
		Status:          model.BillingStatusSettled,
		CreatedAt:       time.Now(),
	}
	if err := db.GetDB().Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	// 模拟扣费用户余额并增加供货者收入
	_ = db.GetDB().Model(&model.User{}).Where("id = ?", user1.ID).Updates(map[string]any{"balance": 90.0, "total_spent": 10.0})
	_ = db.GetDB().Model(&model.User{}).Where("id = ?", owner1.ID).Update("total_revenue", 6.0)

	// 3. 校验统一交易详情 (权限与聚合)
	detailUser, err := TradeDetailGet(ctx, record.ID, user1.ID, model.RoleUser)
	if err != nil {
		t.Fatalf("TradeDetailGet user failed: %v", err)
	}
	if detailUser.Record.UserCost != 10.0 || detailUser.Request.ID != reqID {
		t.Fatalf("unexpected detail: %+v", detailUser)
	}
	// 无关用户查看应被拒绝
	_, err = TradeDetailGet(ctx, record.ID, 8888, model.RoleUser)
	if err == nil {
		t.Fatalf("expected permission denied for third party user")
	}

	// 4. 用户发起退款申诉
	dispute, err := DisputeCreate(ctx, user1.ID, record.ID, "上游输出格式错误未达预期")
	if err != nil {
		t.Fatalf("DisputeCreate failed: %v", err)
	}
	if dispute.Status != model.DisputeStatusPending || dispute.UserCost != 10.0 {
		t.Fatalf("unexpected dispute: %+v", dispute)
	}
	// 重复申诉应被拒绝
	_, err = DisputeCreate(ctx, user1.ID, record.ID, "重复申诉")
	if err == nil {
		t.Fatalf("expected error on duplicate pending dispute")
	}

	// 5. 管理员审核通过退款 (原子冲正)
	err = DisputeReview(ctx, dispute.ID, true, "核实属实同意退款", admin1.ID, admin1.Username)
	if err != nil {
		t.Fatalf("DisputeReview failed: %v", err)
	}

	// 6. 验证冲正与资金不变量
	var u1 model.User
	_ = db.GetDB().First(&u1, user1.ID)
	if u1.Balance != 100.0 || u1.TotalSpent != 0.0 {
		t.Fatalf("expected user balance=100 total_spent=0, got balance=%f spent=%f", u1.Balance, u1.TotalSpent)
	}
	var o1 model.User
	_ = db.GetDB().First(&o1, owner1.ID)
	if o1.TotalRevenue != 0.0 {
		t.Fatalf("expected owner revenue=0 after refund, got %f", o1.TotalRevenue)
	}
	var rec model.BillingRecord
	_ = db.GetDB().First(&rec, record.ID)
	if rec.Status != model.BillingStatusRefunded {
		t.Fatalf("expected billing status=refunded, got %s", rec.Status)
	}
	var refundLedger model.BillingLedger
	if err := db.GetDB().Where("request_id = ? AND kind = ?", reqID, model.LedgerKindRefund).First(&refundLedger).Error; err != nil {
		t.Fatalf("expected refund ledger record, got error: %v", err)
	}
	if refundLedger.Amount != 10.0 || refundLedger.BalanceAfter != 100.0 {
		t.Fatalf("unexpected refund ledger: %+v", refundLedger)
	}

	// 7. 生成日结算单
	today := time.Now().Format("20060102")
	if err := SettlementGenerateDay(ctx, today); err != nil {
		t.Fatalf("SettlementGenerateDay failed: %v", err)
	}
	statements, total, err := SettlementList(ctx, SettlementQuery{OwnerID: owner1.ID})
	if err != nil || total == 0 {
		t.Fatalf("SettlementList failed: total=%d err=%v", total, err)
	}
	// 退款按批准日冲回"原账单的供应商应收份额"(owner_revenue=6), 不是买家支付额(user_cost=10):
	// 毛额与冲回同口径, 同日发生时净额归零, 渠道商不替平台退上浮部分。
	stmt := statements[0]
	if stmt.GrossRevenue != 6.0 || stmt.RefundDeduction != 6.0 || stmt.NetRevenue != 0.0 {
		t.Fatalf("unexpected statement: %+v", stmt)
	}
	if stmt.Status != settlementStatusSettled {
		t.Fatalf("unexpected statement status: %s", stmt.Status)
	}

	// 8. 经营毛利看板汇总
	overall, _, err := ProfitMarginGet(ctx, ProfitMarginQuery{})
	if err != nil {
		t.Fatalf("ProfitMarginGet failed: %v", err)
	}
	if overall.RequestCount != 1 || overall.RefundAmount != 10.0 {
		t.Fatalf("unexpected profit summary: %+v", overall)
	}
}

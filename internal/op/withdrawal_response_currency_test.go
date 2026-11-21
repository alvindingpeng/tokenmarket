package op

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// 提现单响应必须下发 payout_currency / payout_amount —— 前端按"有没有 payout_currency"
// 决定走"到账币种展示"还是回落 legacy ¥ 口径(web/src/api/withdrawal.ts 的 withdrawalPayoutOf)。
//
// 修前: 后端只发 currency(汇率快照的目标币种)/actual_cents, 从不发 payout_*; 前端那个
// `if (!item.payout_currency) return undefined` 恒成立, 于是**所有提现单**都走 '¥' +
// actual_cents/100 分支: EUR 到账 53.10 被显示成 ¥53.10(币种符号全错), JPY 单数值对但
// 被当人民币读。这不是"缺标注", 是"标错"。
//
// 修法只做派生: payout_currency := 已有列 Currency(申请时钉住的到账币种),
// payout_amount := ActualCents/100(与前端自己的兜底算法一致)。不新增数据库列, 不改 any 资金语义。

// withdrawalJSON 把任意值序列化成 map, 用来断言"键名"这一层契约 —— 结构体字段名改错时,
// 只断言结构体的用例仍是绿的, 但前端已经读不到了。
func withdrawalJSON(t *testing.T, value any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return payload
}

// seedEURRate 给本测试库钉一条 EUR 牌价(1 USD = 0.9 EUR), 并清进程内 fx 缓存。
func seedEURRate(t *testing.T) {
	t.Helper()
	FxRateCacheReset()
	t.Cleanup(FxRateCacheReset)
	day := time.Now().Format("20060102")
	if err := db.GetDB().Create(&model.FxRate{From: model.LedgerCurrency, To: "EUR", Day: day, Rate: 0.9, Source: "test"}).Error; err != nil {
		t.Fatalf("seed fx: %v", err)
	}
}

// TestWithdrawListCarriesPayoutCurrency EUR 到账单在列表响应里必须自报到账币种与到账金额。
func TestWithdrawListCarriesPayoutCurrency(t *testing.T) {
	reseller, _ := withdrawalTestSetup(t, "payout-currency-list", 100)
	seedEURRate(t)
	spec := withdrawalSpec(reseller.ID, model.RoleReseller, 60)
	spec.PayoutCurrency = "EUR"
	order, err := WithdrawApply(context.Background(), spec)
	if err != nil {
		t.Fatalf("apply eur: %v", err)
	}
	if order.Currency != "EUR" {
		t.Fatalf("前置失败: order.currency = %q, want EUR", order.Currency)
	}

	views, err := WithdrawList(context.Background(), reseller.ID, "", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("list len = %d, want 1", len(views))
	}
	payload := withdrawalJSON(t, views[0])

	currency, ok := payload["payout_currency"]
	if !ok {
		t.Fatalf("列表 JSON 缺 payout_currency 键, 前端 withdrawalPayoutOf 恒返回 undefined → 所有单走 ¥ 分支; payload=%v", payload)
	}
	if currency != "EUR" {
		t.Fatalf("payout_currency = %v, want EUR (该单申请时钉住的到账币种)", currency)
	}

	amount, ok := payload["payout_amount"]
	if !ok {
		t.Fatalf("列表 JSON 缺 payout_amount 键; payload=%v", payload)
	}
	if want := float64(order.ActualCents) / 100; amount != want {
		t.Fatalf("payout_amount = %v, want %v (= actual_cents/100, 前端兜底同一算法)", amount, want)
	}
	// 到账金额必须与单据的 actual_cents 自洽: 不能凭空算成池口径的 actual。
	if want := float64(order.ActualCents) / 100; math.Abs(amount.(float64)-want) > 1e-9 {
		t.Fatalf("payout_amount 与 actual_cents 不自洽: %v vs %v", amount, want)
	}

	// 旧列语义一个字节都不能变: currency 仍是快照目标币种, actual_cents 仍是整数分。
	if payload["currency"] != "EUR" {
		t.Fatalf("currency 列被改动: %v", payload["currency"])
	}
	if payload["actual_cents"] != float64(order.ActualCents) {
		t.Fatalf("actual_cents 被改动: %v, want %d", payload["actual_cents"], order.ActualCents)
	}
}

// TestWithdrawApplyResponseCarriesPayoutCurrency 申请接口直接回吐单据, 同样要带派生字段
// (它绕过列表口, 不填的话刚提交的那一单仍会被前端当 ¥)。
func TestWithdrawApplyResponseCarriesPayoutCurrency(t *testing.T) {
	reseller, _ := withdrawalTestSetup(t, "payout-currency-apply", 100)
	seedEURRate(t)
	spec := withdrawalSpec(reseller.ID, model.RoleReseller, 60)
	spec.PayoutCurrency = "EUR"
	order, err := WithdrawApply(context.Background(), spec)
	if err != nil {
		t.Fatalf("apply eur: %v", err)
	}
	payload := withdrawalJSON(t, order)
	if payload["payout_currency"] != "EUR" {
		t.Fatalf("apply 响应 payout_currency = %v, want EUR (刚提交的单也不能回落 ¥)", payload["payout_currency"])
	}
	if want := float64(order.ActualCents) / 100; payload["payout_amount"] != want {
		t.Fatalf("apply 响应 payout_amount = %v, want %v", payload["payout_amount"], want)
	}
}

// TestWithdrawListDoesNotFabricatePayoutCurrencyForLegacyRows 旧行(currency 为空串)不许伪造 CNY。
//
// 契约: payout_currency 为空串时前端 `if (!item.payout_currency)` 仍成立 → 继续走 legacy ¥
// 分支(附"旧单, 按当时人民币口径"提示)。若这里用 'CNY' 去填, 前端会认为"这是新单、有明确
// 到账币种", legacy 提示消失, 而库里那行的真实到账币种根本没记录过 —— 等于伪造历史。
func TestWithdrawListDoesNotFabricatePayoutCurrencyForLegacyRows(t *testing.T) {
	reseller, _ := withdrawalTestSetup(t, "payout-currency-legacy", 100)
	seedEURRate(t)
	spec := withdrawalSpec(reseller.ID, model.RoleReseller, 60)
	spec.PayoutCurrency = "EUR"
	order, err := WithdrawApply(context.Background(), spec)
	if err != nil {
		t.Fatalf("apply eur: %v", err)
	}
	// 手工把这行改造成旧行形态: currency 空串 + 旧口径 actual_cents。
	if err := db.GetDB().Model(&model.Withdrawal{}).Where("id = ?", order.ID).
		Update("currency", "").Error; err != nil {
		t.Fatalf("blank currency: %v", err)
	}

	views, err := WithdrawList(context.Background(), reseller.ID, "", 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("list len = %d, want 1", len(views))
	}
	payload := withdrawalJSON(t, views[0])
	currency, ok := payload["payout_currency"]
	if !ok {
		t.Fatalf("列表 JSON 缺 payout_currency 键(旧行也要有这个键, 只是值为空串); payload=%v", payload)
	}
	if currency != "" {
		t.Fatalf("旧行 payout_currency = %v, want 空串(不许用 CNY 伪造, 否则前端 legacy 分支失效)", currency)
	}
	// 旧行金额仍按旧口径由 actual_cents 兜底: payout_amount 不该被填成别的数。
	if amount := payload["payout_amount"]; amount != float64(0) {
		t.Fatalf("旧行 payout_amount = %v, want 0(不伪造到账金额)", amount)
	}
}

// TestWithdrawalDerivedFieldsAreNotPersisted 派生字段必须是只读的: 不建列、不写库。
//
// gorm:"-" 的字段既不会被 AutoMigrate 建列, 也不会被 Create/Updates 写入; 这里用
// 「留下非零派生值后重新查库」来证明它没落盘 —— 若哪次改动把 tag 写漏, 这条用例会红。
func TestWithdrawalDerivedFieldsAreNotPersisted(t *testing.T) {
	reseller, _ := withdrawalTestSetup(t, "payout-derived-readonly", 100)
	spec := withdrawalSpec(reseller.ID, model.RoleReseller, 60)
	order, err := WithdrawApply(context.Background(), spec)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	var reloaded model.Withdrawal
	if err := db.GetDB().First(&reloaded, order.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.PayoutCurrency != "" || reloaded.PayoutAmount != 0 {
		t.Fatalf("派生字段被持久化了: payout_currency=%q payout_amount=%v (必须是 gorm:\"-\" 只读字段)",
			reloaded.PayoutCurrency, reloaded.PayoutAmount)
	}
}

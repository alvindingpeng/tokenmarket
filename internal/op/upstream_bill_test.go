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

func upstreamBillTestDB(t *testing.T) int {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "bill.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(); FxRateCacheReset() })
	channel := model.Channel{UserID: 7, Shared: true, ShareCode: "3001", ChannelConfig: model.ChannelConfig{Name: "bill", BaseURL: "https://up.example"}}
	if err := db.GetDB().Create(&channel).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := channelRefreshCache(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	return channel.ID
}

func seedBillRecord(t *testing.T, channelID int, modelName string, ownerRev float64, at time.Time) {
	t.Helper()
	record := model.BillingRecord{
		RequestID: uint64(at.UnixNano()), UserID: 1, GroupID: 1, GroupModel: modelName,
		ChannelID: channelID, ModelName: modelName, OwnerRevenue: ownerRev, UserCost: ownerRev * 1.2,
		InputToken: 100, OutputToken: 200, Status: "settled", CreatedAt: at,
	}
	if err := db.GetDB().Create(&record).Error; err != nil {
		t.Fatalf("seed billing: %v", err)
	}
}

// 全链路: USD 账单导入 -> 与平台 owner_revenue 对平; 超差行 mismatch; 已解释行不被重算翻掉。
func TestUpstreamBillImportLifecycle(t *testing.T) {
	channelID := upstreamBillTestDB(t)
	now := time.Now()
	seedBillRecord(t, channelID, "gpt-x", 9.9, now)
	// 平台记了 9.9 USD, 账单报 10 USD, 差 1% 恰在容差边界内 -> matched。
	csv := "model,currency,cost\ngpt-x,USD,10\n"
	imported, err := UpstreamBillImport(context.Background(), channelID, now.Format("2006-01"), strings.NewReader(csv))
	if err != nil || imported != 1 {
		t.Fatalf("import: imported=%d err=%v", imported, err)
	}
	rows, err := UpstreamBillList(context.Background(), channelID, now.Format("200601"))
	if err != nil || len(rows) != 1 {
		t.Fatalf("list: rows=%d err=%v", len(rows), err)
	}
	if rows[0].Status != model.UpstreamBillStatusMatched {
		t.Fatalf("status = %q, want matched (diff %.4f)", rows[0].Status, rows[0].DiffRatio)
	}
	// 平台收入 100 vs 账单 10 -> mismatch; 解释后重算不翻掉结论。
	seedBillRecord(t, channelID, "gpt-y", 100, now)
	csv2 := "model,currency,cost\ngpt-y,USD,10\n"
	if _, err := UpstreamBillImport(context.Background(), channelID, now.Format("2006-01"), strings.NewReader(csv2)); err != nil {
		t.Fatalf("import2: %v", err)
	}
	rows, _ = UpstreamBillList(context.Background(), channelID, now.Format("200601"))
	var mismatchID uint64
	for _, row := range rows {
		if row.ModelName == "gpt-y" {
			mismatchID = row.ID
			if row.Status != model.UpstreamBillStatusMismatch {
				t.Fatalf("gpt-y status = %q, want mismatch", row.Status)
			}
		}
	}
	if err := UpstreamBillExplain(context.Background(), mismatchID, "supplier discount not in our records"); err != nil {
		t.Fatalf("explain: %v", err)
	}
	if _, err := UpstreamBillRecompute(context.Background(), channelID, now.Format("200601")); err != nil {
		t.Fatalf("recompute: %v", err)
	}
	rows, _ = UpstreamBillList(context.Background(), channelID, now.Format("200601"))
	for _, row := range rows {
		if row.ID == mismatchID && row.Status != model.UpstreamBillStatusExplained {
			t.Fatalf("explained row flipped to %q by recompute", row.Status)
		}
	}
}

// 非 USD 账单按牌价折算后对单。
func TestUpstreamBillCurrencyConversion(t *testing.T) {
	channelID := upstreamBillTestDB(t)
	now := time.Now()
	day := now.Format("20060102")
	if err := db.GetDB().Create(&model.FxRate{From: "USD", To: "CNY", Day: day, Rate: 7.0, Source: "test"}).Error; err != nil {
		t.Fatalf("seed fx: %v", err)
	}
	seedBillRecord(t, channelID, "claude-z", 10, now)
	// 账单 70 CNY = 10 USD, 平台 10 USD -> matched。
	csv := "model,currency,cost\nclaude-z,CNY,70\n"
	if _, err := UpstreamBillImport(context.Background(), channelID, now.Format("2006-01"), strings.NewReader(csv)); err != nil {
		t.Fatalf("import: %v", err)
	}
	rows, _ := UpstreamBillList(context.Background(), channelID, now.Format("200601"))
	if len(rows) != 1 || rows[0].Status != model.UpstreamBillStatusMatched || rows[0].FxRate < 0.14285 || rows[0].FxRate > 0.14287 {
		t.Fatalf("converted row: %+v", rows)
	}
}

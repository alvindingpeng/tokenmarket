package op

import (
	"context"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// 复现生产 UNIQUE 冲突: 同日已有一行且新值不同时, 若把整行 struct 当查询条件,
// SELECT 不命中 -> INSERT 撞 (from,to,day) 唯一索引。刷新必须幂等更新而非报错。
func TestFxRateRefreshSameDayChangedRateUpdates(t *testing.T) {
	fxRateTestDB(t)
	day := time.Now().Format("20060102")
	seed := model.FxRate{From: model.LedgerCurrency, To: "CNY", Day: day, Rate: 7.20, Source: fxRateSourceName}
	if err := db.GetDB().Create(&seed).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	rates := fxTestRates()
	rates["CNY"] = 7.22
	fxUseTestFetchers(t,
		stashFxFetcher(fxRateSourceName, rates, nil),
		stashFxFetcher(fxRateBackupSourceName, fxTestRates(), nil),
	)
	written, err := FxRateRefresh(context.Background())
	if err != nil || written != len(SupportedQuoteCurrencies()) {
		t.Fatalf("refresh: written=%d err=%v", written, err)
	}
	var stored model.FxRate
	if err := db.GetDB().Where("`from` = ? AND `to` = ? AND `day` = ?", model.LedgerCurrency, "CNY", day).First(&stored).Error; err != nil {
		t.Fatalf("load stored fx: %v", err)
	}
	if stored.Rate != 7.22 {
		t.Fatalf("rate not updated: %+v", stored)
	}
	count := int64(0)
	if err := db.GetDB().Model(&model.FxRate{}).Where("`from` = ? AND `to` = ? AND `day` = ?", model.LedgerCurrency, "CNY", day).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("rows=%d err=%v", count, err)
	}
}

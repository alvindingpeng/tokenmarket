package op

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

func fxRateTestDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "fx.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(); FxRateCacheReset() })
}

// fxUseTestFetchers 注入假牌价源并复位全局状态, 结束后恢复原 fetcher 列表。
func fxUseTestFetchers(t *testing.T, fetchers ...fxRateFetcher) {
	t.Helper()
	prev := fxRateFetchers
	fxRateFetchers = fetchers
	FxRateCacheReset()
	resetFxRefreshStateForTest()
	t.Cleanup(func() {
		fxRateFetchers = prev
		fxRateCacheResetSourceForTest()
		resetFxRefreshStateForTest()
	})
}

func resetFxRefreshStateForTest() {
	fxRefreshState.Lock()
	fxRefreshState.status = FxRefreshStatus{}
	fxRefreshState.Unlock()
}

func fxRateCacheResetSourceForTest() { fxLastSource.Store(fxRateSourceName) }

// stashFxFetcher 声明一个恒定的假牌价源。
func stashFxFetcher(name string, rates map[string]float64, err error) fxRateFetcher {
	return fxRateFetcher{name: name, fetch: func(context.Context) (map[string]float64, error) {
		return rates, err
	}}
}

func fxTestRates() map[string]float64 {
	return map[string]float64{"CNY": 7.2, "EUR": 0.92, "GBP": 0.79, "JPY": 150, "HKD": 7.8, "SGD": 1.35, "AUD": 1.52, "CAD": 1.37, "KRW": 1380, "CHF": 0.88, "THB": 35.2}
}

func TestFxRateLookupAndConvert(t *testing.T) {
	fxRateTestDB(t)
	day := time.Now().Format("20060102")
	if err := db.GetDB().Create(&model.FxRate{From: "USD", To: "CNY", Day: day, Rate: 7.2, Source: "test"}).Error; err != nil {
		t.Fatalf("seed fx: %v", err)
	}
	rate, gotDay, err := FxRateLookup(context.Background(), "CNY", "USD")
	if err != nil || rate < 0.13888 || rate > 0.13889 || gotDay != day {
		t.Fatalf("lookup: rate=%v day=%q err=%v", rate, gotDay, err)
	}
	converted, rate, _, err := FxRateConvert(context.Background(), "CNY", 72)
	if err != nil || converted < 9.999 || converted > 10.001 || rate < 0.13888 || rate > 0.13889 {
		t.Fatalf("convert: amount=%v rate=%v err=%v", converted, rate, err)
	}
	converted, rate, _, err = FxRateConvert(context.Background(), "USD", 10)
	if err != nil || converted != 10 || rate != 1 {
		t.Fatalf("identity: amount=%v rate=%v err=%v", converted, rate, err)
	}
}

func TestFxRateLookupMissingDoesNotReturnOne(t *testing.T) {
	fxRateTestDB(t)
	if _, _, err := FxRateLookup(context.Background(), "CNY", "USD"); err == nil {
		t.Fatal("missing fx must be an error, never silently use rate 1")
	}
}

// 主源覆盖全部币种且通过校验: 直接成功, 不触发备用源。
func TestFxRateRefreshPrimarySourceSucceeds(t *testing.T) {
	fxRateTestDB(t)
	backupCalled := false
	fxUseTestFetchers(t,
		stashFxFetcher(fxRateSourceName, fxTestRates(), nil),
		fxRateFetcher{name: fxRateBackupSourceName, fetch: func(context.Context) (map[string]float64, error) {
			backupCalled = true
			return fxTestRates(), nil
		}},
	)
	written, err := FxRateRefresh(context.Background())
	if err != nil || written != len(SupportedQuoteCurrencies()) {
		t.Fatalf("refresh: written=%d err=%v", written, err)
	}
	if backupCalled {
		t.Fatal("backup source must not be contacted when primary succeeds")
	}
	if got := FxRateSourceName(); got != fxRateSourceName {
		t.Fatalf("source=%q, want %q", got, fxRateSourceName)
	}
	if got := FxRefreshStatusView().Source; got != fxRateSourceName {
		t.Fatalf("status source=%q, want %q", got, fxRateSourceName)
	}
	day := time.Now().Format("20060102")
	var stored model.FxRate
	if err := db.GetDB().Where("`from` = ? AND `to` = ? AND `day` = ?", model.LedgerCurrency, "CNY", day).First(&stored).Error; err != nil {
		t.Fatalf("load stored fx: %v", err)
	}
	if stored.Rate != 7.2 || stored.Source != fxRateSourceName {
		t.Fatalf("stored=%+v", stored)
	}
}

// 主源返回空数据(如错误页被解析成无币种): 回退备用源, 只有备用源被接受。
func TestFxRateRefreshPrimaryEmptyFallsBackToBackup(t *testing.T) {
	fxRateTestDB(t)
	fxUseTestFetchers(t,
		stashFxFetcher(fxRateSourceName, map[string]float64{}, nil),
		stashFxFetcher(fxRateBackupSourceName, fxTestRates(), nil),
	)
	written, err := FxRateRefresh(context.Background())
	if err != nil || written != len(SupportedQuoteCurrencies()) {
		t.Fatalf("refresh: written=%d err=%v", written, err)
	}
	if got := FxRateSourceName(); got != fxRateBackupSourceName {
		t.Fatalf("source=%q, want %q", got, fxRateBackupSourceName)
	}
	status := FxRefreshStatusView()
	if status.Source != fxRateBackupSourceName || status.LastError != "" {
		t.Fatalf("status=%+v", status)
	}
	var stored model.FxRate
	day := time.Now().Format("20060102")
	if err := db.GetDB().Where("`from` = ? AND `to` = ? AND `day` = ?", model.LedgerCurrency, "HKD", day).First(&stored).Error; err != nil {
		t.Fatalf("load stored fx: %v", err)
	}
	if stored.Source != fxRateBackupSourceName {
		t.Fatalf("stored source=%q, want %q", stored.Source, fxRateBackupSourceName)
	}
}

// 主源请求异常: 回退备用源。
func TestFxRateRefreshPrimaryErrorFallsBackToBackup(t *testing.T) {
	fxRateTestDB(t)
	fxUseTestFetchers(t,
		stashFxFetcher(fxRateSourceName, nil, errors.New("boom")),
		stashFxFetcher(fxRateBackupSourceName, fxTestRates(), nil),
	)
	written, err := FxRateRefresh(context.Background())
	if err != nil || written != len(SupportedQuoteCurrencies()) {
		t.Fatalf("refresh: written=%d err=%v", written, err)
	}
	if got := FxRateSourceName(); got != fxRateBackupSourceName {
		t.Fatalf("source=%q, want %q", got, fxRateBackupSourceName)
	}
}

// 主源币种不全: 同样视为失败并回退。
func TestFxRateRefreshPrimaryIncompleteFallsBackToBackup(t *testing.T) {
	fxRateTestDB(t)
	partial := fxTestRates()
	delete(partial, "SGD")
	fxUseTestFetchers(t,
		stashFxFetcher(fxRateSourceName, partial, nil),
		stashFxFetcher(fxRateBackupSourceName, fxTestRates(), nil),
	)
	if _, err := FxRateRefresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got := FxRateSourceName(); got != fxRateBackupSourceName {
		t.Fatalf("source=%q, want %q", got, fxRateBackupSourceName)
	}
}

// 主源空 + 备用源币种不全(缺 HKD/SGD): 全部失败, 返回错误且不动快照与库。
func TestFxRateRefreshAllSourcesFailKeepsSnapshot(t *testing.T) {
	fxRateTestDB(t)
	ctx := context.Background()
	oldDay := "20200101"
	if err := db.GetDB().Create(&model.FxRate{From: model.LedgerCurrency, To: "CNY", Day: oldDay, Rate: 7.2, Source: "test"}).Error; err != nil {
		t.Fatalf("seed fx: %v", err)
	}
	fxCache.Store(&fxSnapshot{rates: map[string]float64{"CNY": 7.2}, day: oldDay})

	incomplete := fxTestRates()
	delete(incomplete, "HKD")
	delete(incomplete, "SGD")
	fxUseTestFetchers(t,
		stashFxFetcher(fxRateSourceName, map[string]float64{}, nil),
		stashFxFetcher(fxRateBackupSourceName, incomplete, nil),
	)
	// 重新种入快照(helper 复位了缓存), 以验证失败路径不改快照。
	fxCache.Store(&fxSnapshot{rates: map[string]float64{"CNY": 7.2}, day: oldDay})

	written, err := FxRateRefresh(ctx)
	if err == nil || written != 0 {
		t.Fatalf("refresh: written=%d err=%v, want failure", written, err)
	}
	status := FxRefreshStatusView()
	if status.LastError == "" || !strings.Contains(status.LastError, fxRateBackupSourceName) {
		t.Fatalf("LastError=%q must name the failing source", status.LastError)
	}
	if status.FailureCount < 1 {
		t.Fatalf("FailureCount=%d, want >=1", status.FailureCount)
	}
	snap := fxCache.Load()
	if snap == nil || snap.day != oldDay || snap.rates["CNY"] != 7.2 {
		t.Fatalf("snapshot changed: %#v", snap)
	}
	var stored model.FxRate
	if err := db.GetDB().Where("`from` = ? AND `to` = ? AND `day` = ?", model.LedgerCurrency, "CNY", oldDay).First(&stored).Error; err != nil {
		t.Fatalf("load stored fx: %v", err)
	}
	if stored.Rate != 7.2 {
		t.Fatalf("stored rate changed: %v", stored.Rate)
	}
	var todayCount int64
	if err := db.GetDB().Model(&model.FxRate{}).Where("day = ?", time.Now().Format("20060102")).Count(&todayCount).Error; err != nil {
		t.Fatalf("count today rows: %v", err)
	}
	if todayCount != 0 {
		t.Fatalf("failure wrote %d rows for today", todayCount)
	}
	// 失败不改变"最近成功源"。
	if got := FxRateSourceName(); got != fxRateSourceName {
		t.Fatalf("source=%q, want unchanged default %q", got, fxRateSourceName)
	}
}

// 阈值校验作用于最终被接受的那一批: 备用源数据超过阈值同样被拒。
func TestFxRateRefreshRejectsAbnormalMoveWithoutChangingSnapshotOrDB(t *testing.T) {
	fxRateTestDB(t)
	ctx := context.Background()
	day := time.Now().Format("20060102")
	if err := db.GetDB().Create(&model.FxRate{From: model.LedgerCurrency, To: "CNY", Day: day, Rate: 7.2, Source: "test"}).Error; err != nil {
		t.Fatalf("seed fx: %v", err)
	}
	fxCache.Store(&fxSnapshot{rates: map[string]float64{"CNY": 7.2}, day: day})
	written, err := fxRateRefreshWithRates(ctx, "test-source", map[string]float64{"CNY": 9.1})
	if err == nil || written != 0 {
		t.Fatalf("refresh: written=%d err=%v, want rejection", written, err)
	}
	var stored model.FxRate
	if err := db.GetDB().Where("`from` = ? AND `to` = ? AND `day` = ?", model.LedgerCurrency, "CNY", day).First(&stored).Error; err != nil {
		t.Fatalf("load stored fx: %v", err)
	}
	if stored.Rate != 7.2 {
		t.Fatalf("stored rate changed: %v", stored.Rate)
	}
	if snap := fxCache.Load(); snap == nil || snap.rates["CNY"] != 7.2 {
		t.Fatalf("snapshot changed: %#v", snap)
	}
}

func TestFxRateRefreshAcceptsNormalMove(t *testing.T) {
	fxRateTestDB(t)
	ctx := context.Background()
	day := time.Now().Format("20060102")
	if err := db.GetDB().Create(&model.FxRate{From: model.LedgerCurrency, To: "CNY", Day: "20200101", Rate: 7.2, Source: "test"}).Error; err != nil {
		t.Fatalf("seed fx: %v", err)
	}
	fxCache.Store(&fxSnapshot{rates: map[string]float64{"CNY": 7.2}, day: "20200101"})
	written, err := fxRateRefreshWithRates(ctx, "test-source", map[string]float64{"CNY": 7.25})
	if err != nil || written != 1 {
		t.Fatalf("refresh: written=%d err=%v", written, err)
	}
	var stored model.FxRate
	if err := db.GetDB().Where("`from` = ? AND `to` = ? AND `day` = ?", model.LedgerCurrency, "CNY", day).First(&stored).Error; err != nil {
		t.Fatalf("load stored fx: %v", err)
	}
	if stored.Rate != 7.25 || stored.Source != "test-source" {
		t.Fatalf("stored=%+v", stored)
	}
	if snap := fxCache.Load(); snap == nil || snap.rates["CNY"] != 7.25 {
		t.Fatalf("snapshot not refreshed: %#v", snap)
	}
}

// TestFxRefreshFailureAlertDedup 连续失败达阈值时置位去重时间戳并落站内告警;
// 24h 窗口内的第二次调用不再重复外发(直接看去重时间戳不再前进即可, 外发通道本身
// 因无凭据会失败并只记日志, 不影响断言)。
func TestFxRefreshFailureAlertDedup(t *testing.T) {
	fxRateTestDB(t)
	resetFxRefreshStateForTest()
	t.Cleanup(func() { resetFxRefreshStateForTest() })
	for i := 0; i < fxRefreshAlertThreshold-1; i++ {
		recordFxRefreshFailure("test-src", fmt.Errorf("boom %d", i))
	}
	fxLastAlertMu.Lock()
	coldStart := fxLastAlertAt.IsZero()
	fxLastAlertMu.Unlock()
	if !coldStart {
		t.Fatal("alert must not fire below threshold")
	}
	recordFxRefreshFailure("test-src", fmt.Errorf("boom-final"))
	fxLastAlertMu.Lock()
	fired := !fxLastAlertAt.IsZero()
	fxLastAlertMu.Unlock()
	if !fired {
		t.Fatal("alert must fire at threshold")
	}
	first := fxLastAlertAt
	recordFxRefreshFailure("test-src", fmt.Errorf("again"))
	fxLastAlertMu.Lock()
	deduped := fxLastAlertAt.Equal(first)
	fxLastAlertMu.Unlock()
	if !deduped {
		t.Fatal("alert must be deduplicated within 24h window")
	}
	if st := FxRefreshStatusView(); st.FailureCount != fxRefreshAlertThreshold+1 {
		t.Fatalf("failure count = %d, want %d", st.FailureCount, fxRefreshAlertThreshold+1)
	}
}

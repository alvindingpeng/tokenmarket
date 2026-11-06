package op

import (
	"context"
	"path/filepath"
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

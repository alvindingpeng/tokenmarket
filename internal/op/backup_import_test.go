package op

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

func backupImportTestDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "backup-import.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := InitCache(); err != nil {
		t.Fatalf("init cache: %v", err)
	}
}

func TestDBImportRejectsInvalidMediaPriceWithoutWrites(t *testing.T) {
	backupImportTestDB(t)
	dump := &model.DBDump{
		Version:       dbDumpVersion,
		LLMInfos:      []model.LLMInfo{{Name: "must-not-import", LLMPrice: model.LLMPrice{Input: 1}}},
		ChannelModels: []model.ChannelModel{{ID: 91, Name: "bad", MediaSupply: model.MediaPrice{PerImage: math.NaN()}}},
	}
	if _, err := DBImportIncremental(context.Background(), dump); err == nil || !strings.Contains(err.Error(), "media") {
		t.Fatalf("expected invalid media price rejection, got %v", err)
	}
	var count int64
	if err := db.GetDB().Model(&model.LLMInfo{}).Where("name = ?", "must-not-import").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("validation failure must happen before transaction writes, found %d imported rows", count)
	}
}

func TestDBImportRejectsInvalidSettingWithoutWrites(t *testing.T) {
	backupImportTestDB(t)
	dump := &model.DBDump{
		Version:  dbDumpVersion,
		LLMInfos: []model.LLMInfo{{Name: "must-not-import", LLMPrice: model.LLMPrice{Input: 1}}},
		Settings: []model.Setting{{Key: model.SettingKeyMinBalance, Value: "NaN"}},
	}
	if _, err := DBImportIncremental(context.Background(), dump); err == nil || !strings.Contains(err.Error(), "min balance") {
		t.Fatalf("expected invalid setting rejection, got %v", err)
	}
	var count int64
	if err := db.GetDB().Model(&model.LLMInfo{}).Where("name = ?", "must-not-import").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("validation failure must happen before transaction writes, found %d imported rows", count)
	}
}

func TestDBImportRejectsInvalidChannelConfigWithoutWrites(t *testing.T) {
	backupImportTestDB(t)
	dump := &model.DBDump{
		Version:  dbDumpVersion,
		LLMInfos: []model.LLMInfo{{Name: "must-not-import", LLMPrice: model.LLMPrice{Input: 1}}},
		Channels: []model.Channel{{ID: 92, ChannelConfig: model.ChannelConfig{Name: "bad", BaseURL: "  "}}},
	}
	if _, err := DBImportIncremental(context.Background(), dump); err == nil || !strings.Contains(err.Error(), "base url") {
		t.Fatalf("expected invalid channel rejection, got %v", err)
	}
	var count int64
	if err := db.GetDB().Model(&model.LLMInfo{}).Where("name = ?", "must-not-import").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("validation failure must happen before transaction writes, found %d imported rows", count)
	}
}

func TestDBImportRejectsUnsupportedVersionWithoutWrites(t *testing.T) {
	backupImportTestDB(t)
	_, err := DBImportIncremental(context.Background(), &model.DBDump{
		Version:  dbDumpVersion + 1,
		LLMInfos: []model.LLMInfo{{Name: "must-not-import"}},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported dump version") {
		t.Fatalf("expected unsupported dump version rejection, got %v", err)
	}
	var count int64
	if err := db.GetDB().Model(&model.LLMInfo{}).Where("name = ?", "must-not-import").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("unsupported version must not import rows, found %d", count)
	}
}

package op

import (
	"encoding/json"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

func TestModelCatalogBackfillsChannelsAndPreservesConfiguration(t *testing.T) {
	ctx, reseller, _ := reviewTestEnv(t)
	t.Cleanup(func() { modelReferences.Store(nil) })
	detail := reviewTestDetail("models-dev")
	oldName := detail.Models[0]
	channel, err := ChannelCreate(detail, reseller, ctx)
	if err != nil {
		t.Fatal(err)
	}
	conn := db.GetDB()
	supply := model.LLMPrice{Input: 12, Output: 14}
	if err := conn.Model(&model.ChannelModel{}).Where("channel_id = ?", channel.ID).
		Select("supply_price").Updates(model.ChannelModel{SupplyPrice: supply}).Error; err != nil {
		t.Fatal(err)
	}
	if err := CapabilitySaveManual(model.ModelCapability{Name: oldName, Context: 32000}, ctx); err != nil {
		t.Fatal(err)
	}

	capabilities := []model.ModelCapability{{Name: oldName, Context: 128000}}
	references := []model.ModelReference{
		{Name: oldName, ContextLength: 128000, PriceProvider: "openai", OfficialPrice: json.RawMessage(`{"input":0,"output":5,"tiers":[{"input":2}]}`)},
		{Name: "example/new-model", ContextLength: 64000},
	}
	result, err := ReplaceModelCatalog(ctx, capabilities, references)
	if err != nil || result.ChannelModelsUpdated != 1 {
		t.Fatalf("initial sync = %+v, %v", result, err)
	}
	detailAfter, err := ChannelDetailGet(channel.ID, reseller)
	if err != nil || detailAfter.ModelContexts[oldName] != 128000 {
		t.Fatalf("channel detail context = %v, %v", detailAfter.ModelContexts, err)
	}
	var row model.ChannelModel
	if err := conn.Where("channel_id = ? AND name = ?", channel.ID, oldName).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.ContextLength != 128000 || row.SupplyPrice != supply {
		t.Fatalf("stored channel model = %+v", row)
	}
	manual, ok := CapabilityGet(oldName)
	if !ok || manual.Source != model.CapabilitySourceManual || manual.Context != 32000 {
		t.Fatalf("manual capability was overwritten: %+v", manual)
	}

	// Cache reload uses the persisted metadata, including explicit zero prices and tiers.
	if err := modelReferenceRefreshCache(ctx); err != nil {
		t.Fatal(err)
	}
	ref := ModelReferenceGet(oldName)
	if string(ref.OfficialPrice) != string(references[0].OfficialPrice) {
		t.Fatalf("restored reference = %s", ref.OfficialPrice)
	}
	detailAfter.Models = append(detailAfter.Models, "Example/New-Model")
	if _, err := ChannelUpdate(&detailAfter, reseller, ctx); err != nil {
		t.Fatal(err)
	}
	var added model.ChannelModel
	if err := conn.Where("channel_id = ? AND name = ?", channel.ID, "Example/New-Model").First(&added).Error; err != nil {
		t.Fatal(err)
	}
	if added.ContextLength != 64000 {
		t.Fatalf("new channel model context = %d", added.ContextLength)
	}
	result, err = ReplaceModelCatalog(ctx, capabilities, references)
	if err != nil || result.ChannelModelsUpdated != 0 {
		t.Fatalf("unchanged sync = %+v, %v", result, err)
	}
	// A failed insert must keep the previous catalog and channel backfill intact.
	duplicates := []model.ModelReference{references[1], references[1]}
	if _, err := ReplaceModelCatalog(ctx, capabilities, duplicates); err == nil {
		t.Fatal("expected duplicate references to fail the sync")
	}
	if err := modelReferenceRefreshCache(ctx); err != nil {
		t.Fatal(err)
	}
	if got := ModelReferenceGet(oldName).ContextLength; got != 128000 {
		t.Fatalf("failed sync removed existing reference: %d", got)
	}

	result, err = ReplaceModelCatalog(ctx, capabilities, references[1:])
	if err != nil || result.ChannelModelsUpdated != 1 {
		t.Fatalf("sync without old model = %+v, %v", result, err)
	}
	detailAfter, err = ChannelDetailGet(channel.ID, reseller)
	if err != nil || detailAfter.ModelContexts[oldName] != 0 {
		t.Fatalf("stale context was retained: %v, %v", detailAfter.ModelContexts, err)
	}
}

package op

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
)

var modelReferences atomic.Pointer[map[string]model.ModelReference]

// Model creation and catalog publication share a lock so new rows use the
// same reference snapshot as the backfill, including when the first sync runs.
var modelCatalogWriteMu sync.RWMutex

func channelModelTransaction(ctx context.Context, fn func(*gorm.DB) error) error {
	modelCatalogWriteMu.RLock()
	defer modelCatalogWriteMu.RUnlock()
	return db.GetDB().WithContext(ctx).Transaction(fn)
}

type ModelCatalogSyncResult struct {
	References           int   `json:"references"`
	ChannelModelsUpdated int64 `json:"channel_models_updated"`
}

func ModelReferenceGet(name string) model.ModelReference {
	if references := modelReferences.Load(); references != nil {
		return (*references)[normalizeCapabilityKey(name)]
	}
	return model.ModelReference{}
}

func modelReferenceRefreshCache(ctx context.Context) error {
	var rows []model.ModelReference
	if err := db.GetDB().WithContext(ctx).Find(&rows).Error; err != nil {
		return err
	}
	publishModelReferences(rows)
	return nil
}

func publishModelReferences(rows []model.ModelReference) {
	references := make(map[string]model.ModelReference, len(rows))
	for _, row := range rows {
		references[row.Name] = row
	}
	modelReferences.Store(&references)
}

// ReplaceModelCatalog commits the feed and channel backfill together.
func ReplaceModelCatalog(ctx context.Context, capabilities []model.ModelCapability, references []model.ModelReference) (ModelCatalogSyncResult, error) {
	if len(capabilities) == 0 || len(references) == 0 {
		return ModelCatalogSyncResult{}, fmt.Errorf("models.dev returned an empty model catalog")
	}
	modelCatalogWriteMu.Lock()
	defer modelCatalogWriteMu.Unlock()
	updated, err := replaceModelCatalog(ctx, capabilities, references)
	if err != nil {
		return ModelCatalogSyncResult{}, err
	}
	publishModelReferences(references)
	channelModelStatsNeedUpdateLock.Lock()
	channelModelCacheWriteMu.Lock()
	for id, cm := range channelModelCache.GetAll() {
		if length := ModelReferenceGet(cm.Name).ContextLength; cm.ContextLength != length {
			cm.ContextLength = length
			channelModelCache.Set(id, cm)
		}
	}
	channelModelCacheWriteMu.Unlock()
	channelModelStatsNeedUpdateLock.Unlock()
	if err := capabilityRefreshCache(ctx); err != nil {
		return ModelCatalogSyncResult{}, err
	}
	return ModelCatalogSyncResult{References: len(references), ChannelModelsUpdated: updated}, nil
}

func replaceModelCatalog(ctx context.Context, capabilities []model.ModelCapability, references []model.ModelReference) (int64, error) {
	var updated int64
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := replaceModelsDevCapabilities(tx, capabilities); err != nil {
			return err
		}
		if err := tx.Exec("DELETE FROM model_references").Error; err != nil {
			return fmt.Errorf("clear model references: %w", err)
		}
		if err := tx.CreateInBatches(&references, 200).Error; err != nil {
			return fmt.Errorf("save model references: %w", err)
		}
		const contextLength = "COALESCE((SELECT context_length FROM model_references WHERE name = lower(trim(channel_models.name))), 0)"
		result := tx.Model(&model.ChannelModel{}).
			Where("context_length <> "+contextLength).
			UpdateColumn("context_length", gorm.Expr(contextLength))
		if result.Error != nil {
			return fmt.Errorf("backfill channel model contexts: %w", result.Error)
		}
		updated = result.RowsAffected
		return nil
	})
	return updated, err
}

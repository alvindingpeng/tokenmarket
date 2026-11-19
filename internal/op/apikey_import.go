package op

import (
	"encoding/hex"
	"fmt"
	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
	"strings"
)

// validateImportedAPIKeys rejects ambiguous credential ownership before any import
// writes. Existing rows are immutable (the incremental import skips primary keys).
func validateImportedAPIKeys(tx *gorm.DB, keys []model.APIKey) error {
	var existing []model.APIKey
	if err := tx.Find(&existing).Error; err != nil {
		return fmt.Errorf("validate imported API keys: %w", err)
	}
	byID := map[int]model.APIKey{}
	owners := map[string]int{}
	for _, k := range existing {
		byID[k.ID] = k
		owners[k.KeyHash] = k.ID
	}
	seenIDs := map[int]bool{}
	for _, k := range keys {
		if k.ID <= 0 || seenIDs[k.ID] {
			return fmt.Errorf("import API key: invalid or duplicate row ID")
		}
		seenIDs[k.ID] = true
		hash := k.KeyHash
		if strings.TrimSpace(k.APIKey) != "" {
			if strings.HasPrefix(strings.TrimSpace(k.APIKey), "skr-") {
				return fmt.Errorf("import API key: reserved credential prefix")
			}
			calculated := model.APIKeyFingerprint(k.APIKey)
			if hash != "" && hash != calculated {
				return fmt.Errorf("import API key: plaintext/fingerprint conflict")
			}
			hash = calculated
		}
		decoded, err := hex.DecodeString(hash)
		if err != nil || len(decoded) != 32 || strings.ToLower(hash) != hash {
			return fmt.Errorf("import API key: invalid fingerprint")
		}
		if old, ok := byID[k.ID]; ok && (old.KeyHash != hash || old.UserID != k.UserID) {
			return fmt.Errorf("import API key: existing ID ownership conflict")
		}
		if owner, ok := owners[hash]; ok && owner != k.ID {
			return fmt.Errorf("import API key: duplicate credential ownership")
		}
		owners[hash] = k.ID
	}
	return nil
}

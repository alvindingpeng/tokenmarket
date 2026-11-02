package db

import (
	"fmt"
	"github.com/alvindingpeng/switchyard/internal/secretstore"
	"gorm.io/gorm"
)

// validateUserTOTPSecrets also supports read-only checks of pre-2FA databases.
// Unlike historical upstream credentials, TOTP secrets have never had a plaintext
// storage format. Reject malformed/plaintext values instead of silently accepting them.
func validateUserTOTPSecrets(conn *gorm.DB, store *secretstore.MasterKey) error {
	if !conn.Migrator().HasTable("users") || !conn.Migrator().HasColumn("users", "totp_secret_encrypted") {
		return nil
	}
	var rows []struct {
		ID                  uint
		TOTPEnabled         bool
		TOTPSecretEncrypted string
	}
	if err := conn.Table("users").Select("id, totp_enabled, totp_secret_encrypted").Scan(&rows).Error; err != nil {
		return fmt.Errorf("read user TOTP secrets: %w", err)
	}
	for _, row := range rows {
		if row.TOTPSecretEncrypted == "" {
			if row.TOTPEnabled {
				return fmt.Errorf("user %d: enabled TOTP has no secret", row.ID)
			}
			continue
		}
		if !secretstore.IsEnvelope(row.TOTPSecretEncrypted) {
			return fmt.Errorf("user %d: TOTP secret is not encrypted", row.ID)
		}
		if _, err := store.Open(secretstore.UserTOTPBinding(row.ID), row.TOTPSecretEncrypted); err != nil {
			return fmt.Errorf("user %d TOTP secret: %w", row.ID, err)
		}
	}
	return nil
}

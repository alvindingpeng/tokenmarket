package migrate

import (
	"github.com/alvindingpeng/switchyard/internal/model"
	"testing"
)

func TestAPIKeyHashMigrationRollsBackInvalidLegacyRows(t *testing.T) {
	for _, mode := range []string{"conflict", "duplicate", "empty"} {
		t.Run(mode, func(t *testing.T) {
			c := apiKeyMigrateTestDB(t)
			s, _ := c.DB()
			defer s.Close()
			legacyAPIKeyTable(t, c)
			for _, r := range []legacyAPIKeyRow{{ID: 1, Name: "first", APIKey: "first-secret"}, {ID: 2, Name: "second", APIKey: "second-secret"}} {
				if err := c.Create(&r).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := c.AutoMigrate(&model.APIKey{}); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "conflict":
				c.Exec("UPDATE api_keys SET key_hash=? WHERE id=2", model.APIKeyFingerprint("other-secret"))
			case "duplicate":
				c.Exec("UPDATE api_keys SET api_key=? WHERE id=2", "first-secret")
			case "empty":
				c.Exec("UPDATE api_keys SET api_key=? WHERE id=2", "")
			}
			if err := migrateAPIKeyPlaintextToHash(c); err == nil {
				t.Fatal("unsafe row accepted")
			}
			if !hasPhysicalColumn(c, "api_keys", "api_key") {
				t.Fatal("plaintext column lost on failure")
			}
			var hash string
			if err := c.Raw("SELECT key_hash FROM api_keys WHERE id=1").Scan(&hash).Error; err != nil {
				t.Fatal(err)
			}
			if hash != "" {
				t.Fatal("partial update survived rollback")
			}
		})
	}
}

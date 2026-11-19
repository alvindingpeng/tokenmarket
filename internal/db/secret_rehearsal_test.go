package db

import (
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/secretstore"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"os"
	"testing"
)

// Opt-in read-only integration check against an isolated pre/post migration pair.
// Never logs credentials, fingerprints, or decrypted field values.
func TestSecretMigrationRehearsalPair(t *testing.T) {
	before, after, key := os.Getenv("SECURITY_REHEARSAL_BEFORE"), os.Getenv("SECURITY_REHEARSAL_AFTER"), os.Getenv("SECURITY_REHEARSAL_KEK_FILE")
	if before == "" || after == "" || key == "" {
		t.Skip("isolated rehearsal pair not configured")
	}
	if before == after {
		t.Fatal("distinct snapshots required")
	}
	if err := secretstore.Init(secretstore.KEKConfig{File: key}); err != nil {
		t.Fatal("key initialization failed")
	}
	defer secretstore.Reset()
	open := func(path string) *gorm.DB {
		t.Helper()
		c, e := gorm.Open(sqlite.Open("file:"+path+"?mode=ro"), &gorm.Config{Logger: logger.Discard})
		if e != nil {
			t.Fatal("read-only snapshot open failed")
		}
		s, e := c.DB()
		if e != nil {
			t.Fatal("connection failed")
		}
		t.Cleanup(func() { s.Close() })
		return c
	}
	old, migrated := open(before), open(after)
	var settings []struct {
		Key   string
		Value string
	}
	if old.Raw("SELECT key,value FROM settings").Scan(&settings).Error != nil {
		t.Fatal("old settings read failed")
	}
	for _, r := range settings {
		var got model.Setting
		if migrated.Where("key = ?", r.Key).First(&got).Error != nil || got.Value != r.Value {
			t.Fatal("settings plaintext roundtrip mismatch")
		}
	}
	var keys []struct {
		ID  int
		Key string
	}
	if old.Raw("SELECT id,key FROM channel_keys").Scan(&keys).Error != nil {
		t.Fatal("old channel keys read failed")
	}
	for _, r := range keys {
		var got model.ChannelKey
		if migrated.First(&got, r.ID).Error != nil || got.Key != r.Key {
			t.Fatal("channel credential roundtrip mismatch")
		}
	}
	var apis []struct {
		ID     int
		APIKey string
	}
	if old.Raw("SELECT id,api_key FROM api_keys").Scan(&apis).Error != nil {
		t.Fatal("old API keys read failed")
	}
	for _, r := range apis {
		var got model.APIKey
		if migrated.First(&got, r.ID).Error != nil || got.KeyHash != model.APIKeyFingerprint(r.APIKey) || got.APIKey != "" {
			t.Fatal("legacy API credential lookup mismatch")
		}
	}
	t.Logf("verified settings=%d channel_keys=%d api_keys=%d", len(settings), len(keys), len(apis))
}

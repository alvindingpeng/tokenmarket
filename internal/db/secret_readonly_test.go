package db

import (
	"bytes"
	"github.com/alvindingpeng/switchyard/internal/secretstore"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"os"
	"path/filepath"
	"testing"
)

func TestSecretStatusLegacyDatabaseIsReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	conn, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{"CREATE TABLE settings (key TEXT PRIMARY KEY,value TEXT)", "INSERT INTO settings VALUES ('auth_secret','legacy-secret')", "CREATE TABLE channel_keys (id INTEGER PRIMARY KEY,channel_id INTEGER,name TEXT,key TEXT)", "CREATE TABLE api_keys (id INTEGER,api_key TEXT)", "INSERT INTO api_keys VALUES (1,'legacy-api-key')"} {
		if err := conn.Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	handle, _ := conn.DB()
	handle.Close()
	before, _ := os.ReadFile(path)
	if err := secretstore.Init(secretstore.KEKConfig{Inline: "0101010101010101010101010101010101010101010101010101010101010101"}); err != nil {
		t.Fatal(err)
	}
	defer secretstore.Reset()
	if err := InitReadOnlySecretDB("sqlite", path); err != nil {
		t.Fatal(err)
	}
	stats, err := MigrateSecretRows(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total() != 1 {
		t.Fatal("did not detect legacy secret")
	}
	if err := GetDB().Exec("UPDATE settings SET value='changed'").Error; err == nil {
		t.Fatal("read-only connection allowed writes")
	}
	if GetDB().Migrator().HasColumn("api_keys", "key_hash") {
		t.Fatal("status migrated API schema")
	}
	Close()
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("status changed database bytes")
	}
	missing := filepath.Join(t.TempDir(), "missing.db")
	if err := InitReadOnlySecretDB("sqlite", missing); err == nil {
		Close()
		t.Fatal("missing database must fail")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("status created database")
	}
}

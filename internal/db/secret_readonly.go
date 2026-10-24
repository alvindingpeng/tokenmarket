package db

import (
	"fmt"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"net/url"
	"path/filepath"
	"strings"
)

// InitReadOnlySecretDB deliberately bypasses InitDB: no schema migrations, WAL mode
// changes, cache startup or writes. Only SQLite has a proven read-only connection
// contract here; other dialects fail rather than pretending a dry run is harmless.
func InitReadOnlySecretDB(kind, path string) error {
	if kind != "sqlite" {
		return fmt.Errorf("read-only secret status currently supports SQLite only")
	}
	if strings.Contains(path, "?") || path == ":memory:" {
		return fmt.Errorf("read-only status requires an existing database file")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	uri := url.URL{Scheme: "file", Path: abs}
	query := url.Values{"mode": {"ro"}, "_pragma": {"query_only(ON)", "busy_timeout(5000)"}}
	uri.RawQuery = query.Encode()
	conn, err := gorm.Open(sqlite.Open(uri.String()), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		return err
	}
	db = conn
	return nil
}

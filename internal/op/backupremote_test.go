package op

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/bestruirui/octopus/internal/conf"
)

func TestPushBackupRemoteRetriesThenSucceeds(t *testing.T) {
	var puts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		puts.Add(1)
		if puts.Load() < 2 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	conf.AppConfig.Backup.RemoteURL = srv.URL
	conf.AppConfig.Backup.RemoteUser = "u"
	conf.AppConfig.Backup.RemotePassword = "p"
	defer func() { conf.AppConfig.Backup = conf.Backup{} }()

	dir := t.TempDir()
	path := filepath.Join(dir, "backup.db")
	if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PushBackupRemote(path); err != nil {
		t.Fatalf("push should succeed after retry: %v", err)
	}
	if got := puts.Load(); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
}

func TestPushBackupRemoteFailsAfterThreeAttempts(t *testing.T) {
	var puts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		puts.Add(1)
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()
	conf.AppConfig.Backup.RemoteURL = srv.URL
	defer func() { conf.AppConfig.Backup = conf.Backup{} }()

	path := filepath.Join(t.TempDir(), "backup.db")
	if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PushBackupRemote(path); err == nil {
		t.Fatal("push must fail after 3 attempts")
	}
	if got := puts.Load(); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
}

func TestBackupRemoteConfiguredValidation(t *testing.T) {
	defer func() { conf.AppConfig.Backup = conf.Backup{} }()
	if BackupRemoteConfigured() {
		t.Fatal("empty url must not be configured")
	}
	conf.AppConfig.Backup.RemoteURL = "ftp://x/y"
	if BackupRemoteConfigured() {
		t.Fatal("ftp must be rejected")
	}
	conf.AppConfig.Backup.RemoteURL = "https://dav.example.com/octopus/"
	if !BackupRemoteConfigured() {
		t.Fatal("https url should be configured")
	}
}

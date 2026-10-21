package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newFixtureDB creates a real SQLite file that looks like a pre-phase2A copy: it has the SQLite
// magic header but none of the phase 2A schema landmarks, so the rehearsal has something to add.
func newFixtureDB(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	gdb, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("create fixture db: %v", err)
	}
	if err := gdb.Exec("CREATE TABLE pre_phase2a_marker (id INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatalf("seed fixture db: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("fixture db handle: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close fixture db: %v", err)
	}
	return path
}

// resolvedTempDir returns a canonical temp base so fixtures never sit behind a symlinked ancestor.
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	return base
}

func TestValidateMigrationVerifyTarget(t *testing.T) {
	base := resolvedTempDir(t)
	valid := newFixtureDB(t, base, "copy.db")

	nonSQLite := filepath.Join(base, "notes.txt")
	if err := os.WriteFile(nonSQLite, []byte("not a database"), 0o600); err != nil {
		t.Fatalf("write non-sqlite file: %v", err)
	}

	symlinkFile := filepath.Join(base, "link.db")
	if err := os.Symlink(valid, symlinkFile); err != nil {
		t.Fatalf("create file symlink: %v", err)
	}

	linkDir := filepath.Join(base, "linkdir")
	if err := os.Symlink(base, linkDir); err != nil {
		t.Fatalf("create dir symlink: %v", err)
	}
	symlinkedParent := filepath.Join(linkDir, "copy.db")

	emptyFile := filepath.Join(base, "empty.db")
	if err := os.WriteFile(emptyFile, nil, 0o600); err != nil {
		t.Fatalf("write empty file: %v", err)
	}

	cases := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"empty flag", "", true},
		{"relative path", "relative/copy.db", true},
		{"nonexistent", filepath.Join(base, "missing.db"), true},
		{"directory", base, true},
		{"symlinked file", symlinkFile, true},
		{"symlinked parent dir", symlinkedParent, true},
		{"non sqlite content", nonSQLite, true},
		{"zero length file", emptyFile, true},
		{"protected production db", protectedDataDB, true},
		{"protected data dir sibling", filepath.Join(protectedDataDir, "copy.db"), true},
		{"valid isolated clone", valid, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validateMigrationVerifyTarget(tc.path)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected rejection for %q, got path %q", tc.path, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected rejection for %q: %v", tc.path, err)
			}
			if got != valid {
				t.Fatalf("canonical path = %q, want %q", got, valid)
			}
		})
	}
}

func TestExecuteMigrationVerifySuccess(t *testing.T) {
	base := resolvedTempDir(t)
	fixture := newFixtureDB(t, base, "rehearsal.db")

	var out bytes.Buffer
	code := executeMigrationVerify(fixture, &out)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; output: %s", code, out.String())
	}

	var result migrationVerifyResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("output is not machine readable JSON: %v\n%s", err, out.String())
	}
	if !result.OK {
		t.Fatalf("ok = false, want true: %+v", result)
	}
	if result.Command != "verify-migration" {
		t.Fatalf("command = %q", result.Command)
	}
	if result.Database != fixture {
		t.Fatalf("database = %q, want %q", result.Database, fixture)
	}
	if result.Error != "" {
		t.Fatalf("error = %q, want empty", result.Error)
	}

	lease, ok := result.Checks["lease_expires_at"]
	if !ok || !lease.Present || lease.Table != "balance_reservations" || lease.Column != "lease_expires_at" {
		t.Fatalf("lease_expires_at check missing or failed: %+v", result.Checks)
	}
	recovery, ok := result.Checks["recovery_table"]
	if !ok || !recovery.Present || recovery.Table != "billing_settlement_tasks" {
		t.Fatalf("recovery_table check missing or failed: %+v", result.Checks)
	}
	for _, column := range []string{"reservation_id", "status", "snapshot"} {
		if !recovery.Columns[column] {
			t.Fatalf("recovery table column %q missing: %+v", column, recovery.Columns)
		}
	}

	// The rehearsal must have migrated the clone: the landmark now exists in the file itself.
	gdb, err := gorm.Open(sqlite.Open(fixture), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("reopen clone: %v", err)
	}
	defer func() {
		if sqlDB, err := gdb.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}()
	if !gdb.Migrator().HasColumn("balance_reservations", "lease_expires_at") {
		t.Fatal("lease_expires_at was not migrated into the clone")
	}
	if !gdb.Migrator().HasTable("billing_settlement_tasks") {
		t.Fatal("billing_settlement_tasks was not migrated into the clone")
	}
}

func TestExecuteMigrationVerifyErrorsAreNonzeroJSON(t *testing.T) {
	base := resolvedTempDir(t)

	for _, target := range []string{"", protectedDataDB, filepath.Join(base, "missing.db")} {
		var out bytes.Buffer
		code := executeMigrationVerify(target, &out)
		if code == 0 {
			t.Fatalf("target %q: exit code 0, want nonzero", target)
		}
		var result migrationVerifyResult
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatalf("target %q: error output is not JSON: %v\n%s", target, err, out.String())
		}
		if result.OK || result.Error == "" {
			t.Fatalf("target %q: want ok=false with error, got %+v", target, result)
		}
	}
}

func TestMigrationVerifyChecksReportMissingSchema(t *testing.T) {
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open bare db: %v", err)
	}
	checks := migrationVerifyChecks(gdb)
	if checks["lease_expires_at"].Present {
		t.Fatal("lease_expires_at reported present on a bare database")
	}
	if checks["recovery_table"].Present {
		t.Fatal("recovery_table reported present on a bare database")
	}
	for _, column := range []string{"reservation_id", "status", "snapshot"} {
		if checks["recovery_table"].Columns[column] {
			t.Fatalf("recovery column %q reported present on a bare database", column)
		}
	}
}

func TestVerifyMigrationCommandRegistered(t *testing.T) {
	for _, c := range rootCmd.Commands() {
		if c.Name() != "verify-migration" {
			continue
		}
		if c.Flags().Lookup("database") == nil {
			t.Fatal("verify-migration is missing the --database flag")
		}
		return
	}
	t.Fatal("verify-migration command is not registered on rootCmd")
}

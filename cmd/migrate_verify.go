package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/spf13/cobra"
	"gorm.io/gorm"
)

// verify-migration: Phase 2A release gate. Rehearse the schema migrations offline against an
// ISOLATED SQLite clone and report the phase 2A schema landmarks as machine readable JSON.
//
// Safety contract (this command is intentionally narrow):
//   - only an existing regular file at an absolute path is accepted; it must carry the SQLite
//     magic header, so a new/empty/non-SQLite file is never opened;
//   - the live production database /opt/switchyard/data/data.db (and anything else under the
//     production data directory) is rejected before it is ever opened, as are symlinked targets
//     and hard links to the production file;
//   - only db.InitDB runs (migrations only): no cache, user, server or task initialization and
//     no upstream/network access of any kind.
const (
	protectedDataDir = "/opt/switchyard/data"
	protectedDataDB  = "/opt/switchyard/data/data.db"
	sqliteMagic      = "SQLite format 3\x00"
)

type migrationVerifyCheck struct {
	Table   string          `json:"table"`
	Column  string          `json:"column,omitempty"`
	Present bool            `json:"present"`
	Columns map[string]bool `json:"columns,omitempty"`
}

type migrationVerifyResult struct {
	OK       bool                            `json:"ok"`
	Command  string                          `json:"command"`
	Database string                          `json:"database"`
	Checks   map[string]migrationVerifyCheck `json:"checks,omitempty"`
	Error    string                          `json:"error,omitempty"`
}

var migrateVerifyDatabase string

var migrateVerifyCmd = &cobra.Command{
	Use:   "verify-migration",
	Short: "Offline migration rehearsal on an isolated SQLite database copy",
	Long: `Rehearse schema migrations offline against an isolated SQLite clone and verify the
phase 2A schema landmarks (balance_reservations.lease_expires_at and the billing settlement
recovery table) after db.InitDB completes.

The target must be an existing regular file at an absolute path with the SQLite magic header.
The live production database (/opt/switchyard/data/data.db), anything under the production data
directory, and symlinked targets are rejected before they are opened.

The command performs migrations only: no cache/user/server/task initialization and no upstream
or network access. It prints a single JSON object on stdout; ok=true means success (exit 0),
ok=false carries "error" and exits nonzero.`,
	Run: func(cmd *cobra.Command, args []string) {
		os.Exit(executeMigrationVerify(migrateVerifyDatabase, os.Stdout))
	},
}

func init() {
	migrateVerifyCmd.Flags().StringVar(&migrateVerifyDatabase, "database", "",
		"absolute path to an existing SQLite database file (isolated clone only)")
	rootCmd.AddCommand(migrateVerifyCmd)
}

// executeMigrationVerify validates the target, runs the migrations and writes one JSON result
// object to out. It returns the process exit code (0 on success, 1 on any error).
func executeMigrationVerify(database string, out io.Writer) int {
	result := migrationVerifyResult{Command: "verify-migration", Database: database}

	path, err := validateMigrationVerifyTarget(database)
	if err != nil {
		return failMigrationVerify(out, &result, err)
	}
	result.Database = path

	if err := db.InitDB("sqlite", path, false); err != nil {
		return failMigrationVerify(out, &result,
			fmt.Errorf("migration rehearsal on %s failed: %w", path, err))
	}
	defer func() { _ = db.Close() }()

	result.Checks = migrationVerifyChecks(db.GetDB())
	var failed []string
	for name, check := range result.Checks {
		if !check.Present {
			failed = append(failed, name)
		}
		for column, ok := range check.Columns {
			if !ok {
				failed = append(failed, name+"."+column)
			}
		}
	}
	sort.Strings(failed)
	if len(failed) > 0 {
		return failMigrationVerify(out, &result,
			fmt.Errorf("post-migration schema check failed: %s", strings.Join(failed, ", ")))
	}

	result.OK = true
	writeMigrationVerifyResult(out, &result)
	return 0
}

// migrationVerifyChecks inspects the post-migration schema for the phase 2A landmarks:
// the lease_expires_at column on balance_reservations and the billing settlement recovery table.
func migrationVerifyChecks(gdb *gorm.DB) map[string]migrationVerifyCheck {
	lease := migrationVerifyCheck{
		Table:  "balance_reservations",
		Column: "lease_expires_at",
	}
	lease.Present = gdb.Migrator().HasTable("balance_reservations") &&
		gdb.Migrator().HasColumn("balance_reservations", "lease_expires_at")

	recovery := migrationVerifyCheck{
		Table:   "billing_settlement_tasks",
		Present: gdb.Migrator().HasTable("billing_settlement_tasks"),
		Columns: map[string]bool{},
	}
	for _, column := range []string{"reservation_id", "status", "snapshot"} {
		recovery.Columns[column] = gdb.Migrator().HasColumn("billing_settlement_tasks", column)
	}

	return map[string]migrationVerifyCheck{
		"lease_expires_at": lease,
		"recovery_table":   recovery,
	}
}

// validateMigrationVerifyTarget enforces the safety contract and returns the canonical path.
func validateMigrationVerifyTarget(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("--database is required")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("--database must be an absolute path, got %q", path)
	}
	clean := filepath.Clean(path)
	// Rejected before the file is ever opened.
	if isProtectedMigrationTarget(clean) {
		return "", fmt.Errorf("--database %q is the protected production data location; verify-migration only accepts an isolated clone", clean)
	}

	info, err := os.Lstat(clean)
	if err != nil {
		return "", fmt.Errorf("--database must be an existing regular file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("--database %q is a symlink; symlink targets are not accepted", clean)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("--database %q is not a regular file", clean)
	}

	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", fmt.Errorf("--database %q cannot be resolved: %w", clean, err)
	}
	if resolved != clean {
		return "", fmt.Errorf("--database %q traverses a symlink (resolves to %q); symlink targets are not accepted", clean, resolved)
	}
	if isProtectedMigrationTarget(resolved) {
		return "", fmt.Errorf("--database %q resolves into the protected production data location", resolved)
	}
	// Hard links share the production file's identity without looking like symlinks.
	if prodInfo, err := os.Stat(protectedDataDB); err == nil && os.SameFile(info, prodInfo) {
		return "", fmt.Errorf("--database %q is the production database %s (or a hard link to it)", clean, protectedDataDB)
	}

	ok, err := hasSQLiteMagicHeader(clean)
	if err != nil {
		return "", fmt.Errorf("--database %q cannot be read: %w", clean, err)
	}
	if !ok {
		return "", fmt.Errorf("--database %q is not a SQLite database file", clean)
	}
	return resolved, nil
}

func isProtectedMigrationTarget(path string) bool {
	clean := filepath.Clean(path)
	if clean == protectedDataDB {
		return true
	}
	return strings.HasPrefix(clean, protectedDataDir+string(os.PathSeparator))
}

func hasSQLiteMagicHeader(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	header := make([]byte, len(sqliteMagic))
	if _, err := io.ReadFull(f, header); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return false, nil
		}
		return false, err
	}
	return string(header) == sqliteMagic, nil
}

func failMigrationVerify(out io.Writer, result *migrationVerifyResult, err error) int {
	result.OK = false
	result.Error = err.Error()
	writeMigrationVerifyResult(out, result)
	return 1
}

func writeMigrationVerifyResult(out io.Writer, result *migrationVerifyResult) {
	payload, err := json.Marshal(result)
	if err != nil {
		// Result struct itself is broken; emit the minimal machine readable error object.
		payload, err = json.Marshal(map[string]any{
			"ok":      false,
			"command": "verify-migration",
			"error":   "result serialization failed",
		})
		if err != nil {
			return
		}
	}
	fmt.Fprintln(out, string(payload))
}

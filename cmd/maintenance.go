package cmd

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/conf"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/glebarez/sqlite"
	"github.com/spf13/cobra"
	"gorm.io/gorm"
)

type maintenanceResult struct {
	OK               bool   `json:"ok"`
	Database         string `json:"database"`
	BeforeBytes      int64  `json:"before_bytes"`
	AfterBytes       int64  `json:"after_bytes"`
	ReclaimedBytes   int64  `json:"reclaimed_bytes"`
	DurationMS       int64  `json:"duration_ms"`
	AutoVacuumBefore int    `json:"auto_vacuum_before"`
	AutoVacuumAfter  int    `json:"auto_vacuum_after"`
	Applied          bool   `json:"applied"`
	// Estimate 说明 reclaimed_bytes 是怎么来的: "freelist_lower_bound" 是 dry-run 的估算,
	// "measured" 是压实后的实测。两者差距不小 —— 生产库实测 freelist 只有 5.9 MB,
	// 而真正压实回收了 14.3 MB, 差额是表内部的碎片(038 迁移就地清空 snapshot 列留下的死页)。
	// 不区分开就会让运维拿着偏小的数字去判断要不要停机。
	Estimate string `json:"estimate"`
	Error    string `json:"error,omitempty"`
}

var maintenanceDatabase, maintenanceConfig string
var maintenanceApply bool

var maintenanceCmd = &cobra.Command{
	Use:   "db-maintenance",
	Short: "Compact an existing SQLite database during a maintenance window",
	Long:  "Run a one-time SQLite compaction after the service has been stopped. This cannot be a migration: compaction requires an exclusive lock and a downtime window, while migrations run on every service startup. It cannot be a scheduled task because VACUUM requires the service to be stopped and an exclusive maintenance window.",
	Run: func(cmd *cobra.Command, args []string) {
		os.Exit(executeDatabaseMaintenance(maintenanceDatabase, maintenanceConfig, maintenanceApply, os.Stdout))
	},
}

func init() {
	maintenanceCmd.Flags().StringVar(&maintenanceDatabase, "database", "", "absolute path to an existing database file (required)")
	maintenanceCmd.Flags().StringVar(&maintenanceConfig, "config", "", "config file used to read database.type")
	maintenanceCmd.Flags().BoolVar(&maintenanceApply, "apply", false, "perform compaction (default: dry-run)")
	rootCmd.AddCommand(maintenanceCmd)
}

func executeDatabaseMaintenance(database, configPath string, apply bool, out io.Writer) int {
	result := maintenanceResult{Database: database, Applied: apply}
	fail := func(err error) int {
		result.OK = false
		result.Error = err.Error()
		writeMaintenanceJSON(out, result)
		return 1
	}
	if database == "" || !filepath.IsAbs(database) {
		return fail(errors.New("--database must be an absolute path to an existing regular file"))
	}
	path := filepath.Clean(database)
	info, err := os.Lstat(path)
	if err != nil {
		return fail(fmt.Errorf("--database must be an existing regular file: %w", err))
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fail(errors.New("--database must be an existing regular file, not a symlink"))
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return fail(errors.New("--database path must not traverse symlinks"))
	}
	dbType := "sqlite"
	if configPath != "" {
		if err := conf.Load(configPath); err != nil {
			return fail(fmt.Errorf("load config: %w", err))
		}
		dbType = conf.AppConfig.Database.Type
	}
	if dbType != "sqlite" {
		return fail(fmt.Errorf("db-maintenance supports SQLite only (database.type=%s)", dbType))
	}
	if err := probeExclusiveLock(path); err != nil {
		// 区分「被占用」与「根本打不开」: 对服务在跑的情况, 指引先停服务是对的;
		// 对权限/IO 之类的错误, 那句指引是误导, 会把运维带去停一个正常的服务。
		if errors.Is(err, sql.ErrConnDone) || errors.Is(err, context.DeadlineExceeded) || isSQLiteBusy(err) {
			return fail(fmt.Errorf("database is locked; first run systemctl stop switchyard, then retry: %w", err))
		}
		return fail(fmt.Errorf("database is not writable: %w", err))
	}
	gdb, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		return fail(fmt.Errorf("open SQLite database: %w", err))
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		return fail(err)
	}
	defer sqlDB.Close()
	var pageCount, pageSize, freePages, autoVac int64
	for _, pragma := range []struct {
		name string
		dst  *int64
	}{{"page_count", &pageCount}, {"page_size", &pageSize}, {"freelist_count", &freePages}, {"auto_vacuum", &autoVac}} {
		if err := gdb.Raw("PRAGMA " + pragma.name).Scan(pragma.dst).Error; err != nil {
			return fail(fmt.Errorf("read PRAGMA %s: %w", pragma.name, err))
		}
	}
	result.BeforeBytes = pageCount * pageSize
	result.AutoVacuumBefore, result.AutoVacuumAfter = int(autoVac), int(autoVac)
	if apply {
		compact, err := op.DatabaseCompact(gdb)
		if err != nil {
			return fail(err)
		}
		result.BeforeBytes, result.AfterBytes, result.ReclaimedBytes = compact.BeforeBytes, compact.AfterBytes, compact.ReclaimedBytes
		result.DurationMS = compact.DurationMS
		result.AutoVacuumBefore, result.AutoVacuumAfter = compact.AutoVacuumBefore, compact.AutoVacuumAfter
		result.Estimate = "measured"
	} else {
		// Dry-run 只估算当前 freelist 可回收量, 不写 pragma、不 checkpoint、不执行 VACUUM。
		// 这是**下界**: 表内部碎片与页内空洞不计入 freelist, 却一样会被 VACUUM 回收。
		result.ReclaimedBytes = freePages * pageSize
		result.AfterBytes = result.BeforeBytes - result.ReclaimedBytes
		result.Estimate = "freelist_lower_bound"
	}
	result.OK = true
	writeMaintenanceJSON(out, result)
	return 0
}

// isSQLiteBusy 判断错误是否为 SQLite 的"库被占用"。用错误文本而不是驱动包的哨兵值:
// cmd 包不该只为这一个判断去 import 具体驱动。
func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "SQLITE_BUSY")
}

// probeExclusiveLock 与服务对同一 SQLite 文件争抢写锁。BEGIN IMMEDIATE 一旦成功即可立刻回滚；
// 失败时指引先停服务, 不重试、不等待, 避免掩盖 SQLITE_BUSY。
func probeExclusiveLock(path string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "PRAGMA locking_mode=EXCLUSIVE"); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	_, rollbackErr := conn.ExecContext(ctx, "ROLLBACK")
	return rollbackErr
}

func writeMaintenanceJSON(out io.Writer, result maintenanceResult) {
	payload, err := json.Marshal(result)
	if err != nil {
		fmt.Fprintln(out, "{\"ok\":false,\"error\":\"result serialization failed\"}")
		return
	}
	fmt.Fprintln(out, string(payload))
}

package op

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/charmbracelet/log"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// rateLimitUsageRetention 是限流监控汇总的留存窗口。
// 限流用量只在监控页看最近窗口, 30 天足够定位; rate_limit_usage_hourly 是全库唯一既无上限也无留存期的非财务表。
const rateLimitUsageRetention = 30 * 24 * time.Hour

// incrementalVacuumMaxPages 是每轮留存清理最多归还给操作系统的页数(4096 页 = 16 MiB)。
//
// 两条实测(本机库, sqlite3 与 glebarez 纯 Go 驱动结论一致, integrity_check 均为 ok):
//   - `PRAGMA incremental_vacuum(N)` **单次只归还 1 页**, N 决定不了回收量 —— 12250 个空闲页
//     实测要调 12250 次才消化完(6 次调用只从 12267 页降到 12261 页)。所以"跑一次就行"是错的。
//   - 单次极便宜: 500 次共 5 ms; WAL 库上 4096 次 88 ms(约 0.02 ms/次)。
//     逐条执行而不是包一个大事务: 逐条时写锁每次只持有毫秒级, 且循环里能响应 ctx 取消;
//     一个事务会把写锁连续占住近 90 ms, 对一个在跑的网关来说这才是更贵的选择。
//
// 因此按页数循环, 并给出上界 4096 页(16 MiB)/天。上界是必要的: 空闲页可能远多于此
// (迁移 038 那种就地清空列的做法, 或在文件中间的某张大表上做一次大删除),
// 无上界地循环等于在维护窗口之外做一次 VACUUM。
//
// 上界够不够: 16 MiB/天 ≈ 5.8 GB/年, 远高于这张表任何可能的增长速度, 所以不是瓶颈。
// 单次成本实测(本机 400 MB 库, 98005 个空闲页): 0.11 ms/次, 4096 次约 0.47 s;
// 小库(WAL, 4096 个空闲页)更快, 88 ms。每次调用自成一个自动提交事务,
// 所以写锁是"4096 个毫秒级小事务"而不是"一次长事务" —— 这正是要的形状。
//
// 顺带一个容易想错的地方: 即使空闲页在文件**中间**(被删的表后面还有别的表在尾部),
// incremental_vacuum 照样能回收 —— 它把尾部的页搬到空洞里, 实测 14721 → 10620 页,
// 文件的另一张表数据完好(integrity ok)。所以"只有文件末尾的空闲页才收得回来"是错的。
const incrementalVacuumMaxPages = 4096

// RateLimitUsageRetentionRun 删除限流监控小时汇总中早于 30 天的行, 可幂等重复执行。
// 该表不是财务证据, 过期数据不再支持监控排障。
//
// 删完顺手做有界 incremental_vacuum: 库的 auto_vacuum 已由 db-maintenance 落成 INCREMENTAL,
// 但那个模式**本身不会缩小文件** —— 它只是把释放的页记进 freelist 并维护 ptrmap,
// 真正把页还给操作系统要等有人执行 incremental_vacuum。没有这一步, auto_vacuum 就只是个写在库头的标记。
// 这些整理语句失败只记 Warn 不算任务失败: 空间回收是尽力而为, 留存删除本身已经成功。
func RateLimitUsageRetentionRun(ctx context.Context) (int64, error) {
	cutoff := time.Now().Add(-rateLimitUsageRetention)
	result := db.GetDB().WithContext(ctx).Where("hour < ?", cutoff).Delete(&model.RateLimitUsageHourly{})
	if result.Error != nil {
		return result.RowsAffected, result.Error
	}
	if result.RowsAffected == 0 {
		return 0, nil
	}
	// 先看有多少空闲页, 再按 min(空闲页, 上界) 次调用。用无参形式: 参数既决定不了回收量,
	// 又不能写成占位符(`PRAGMA incremental_vacuum(?)` 会报 `near "?": syntax error`, 实测),
	// 拼字面量只是徒增一处需要审的东西。
	var freeBefore int64
	if err := db.GetDB().WithContext(ctx).Raw("PRAGMA freelist_count").Scan(&freeBefore).Error; err != nil {
		log.Warnf("rate limit usage retention: read freelist_count failed (rows already deleted): %v", err)
		return result.RowsAffected, nil
	}
	pages := freeBefore
	if pages > incrementalVacuumMaxPages {
		pages = incrementalVacuumMaxPages
	}
	done := int64(0)
	for i := int64(0); i < pages; i++ {
		if ctxErr := ctx.Err(); ctxErr != nil {
			log.Warnf("rate limit usage retention: context done after %d incremental_vacuum calls; the rest stays in the freelist until the next run: %v", done, ctxErr)
			break
		}
		if err := db.GetDB().WithContext(ctx).Exec("PRAGMA incremental_vacuum").Error; err != nil {
			log.Warnf("rate limit usage retention: incremental_vacuum failed after %d calls (rows already deleted), the rest stays in the freelist until the next maintenance window: %v", done, err)
			break
		}
		done++
	}
	// 报实测差值而不是调用次数: 只有 auto_vacuum=INCREMENTAL 的库会真的归还页,
	// 在 auto_vacuum=0 的库上调用次数会很好看而文件一字节不变(实测 1339 次调用, freelist 纹丝不动)。
	var freeAfter int64
	_ = db.GetDB().WithContext(ctx).Raw("PRAGMA freelist_count").Scan(&freeAfter).Error
	returned := freeBefore - freeAfter
	if returned == 0 && freeBefore > 0 {
		log.Warnf("rate limit usage retention: freelist unchanged after %d incremental_vacuum calls; this database is probably still auto_vacuum=NONE, so freed pages stay in the file forever — run `switchyard db-maintenance --apply` in a maintenance window to fix that once and for all", done)
	}
	log.Infof("rate limit usage retention: deleted=%d freelist_before=%d freelist_after=%d pages_returned=%d calls=%d", result.RowsAffected, freeBefore, freeAfter, returned, done)
	return result.RowsAffected, nil
}

// CompactResult 是一次 SQLite 文件压实的结果。
//
// BeforeBytes/AfterBytes 是逻辑大小(page_count × page_size), BeforeFileBytes/AfterFileBytes
// 是主库文件的实际大小。两个数字都给出来: 逻辑大小不含 freelist 之外的空洞, 文件大小还受 WAL 影响,
// 只看其中一个都会让「回收了多少」这句话说不清。
type CompactResult struct {
	BeforeBytes int64 `json:"before_bytes"`
	AfterBytes  int64 `json:"after_bytes"`
	// ReclaimedBytes 是压实后的**净变化**, 可能为负。
	//
	// 负值不是错误: 把 auto_vacuum 从 NONE 改成 INCREMENTAL 会让 SQLite 在 VACUUM 时
	// 为每约 1024 页补一张 ptrmap 页, 一个本来就没有空闲页的库会因此略微变大。
	// 在没有可回收空间的库上实测: 14721024 → 14741504 字节(-20480, 即 5 页 ptrmap)。
	// 生产库有 14.3 MB 死页, 那次是净回收。字段名保持不变是为了 CLI 的 JSON 契约稳定。
	ReclaimedBytes   int64 `json:"reclaimed_bytes"`
	DurationMS       int64 `json:"duration_ms"`
	AutoVacuumBefore int   `json:"auto_vacuum_before"`
	AutoVacuumAfter  int   `json:"auto_vacuum_after"`
	BeforeFileBytes  int64 `json:"before_file_bytes"`
	AfterFileBytes   int64 `json:"after_file_bytes"`
}

// DatabaseCompact 就地压实 SQLite 主库并把 auto_vacuum 落成 INCREMENTAL(值为 2)。
//
// 只给停机维护用(switchyard db-maintenance --apply), 两个原因:
//   - VACUUM 需要独占写锁, 服务在跑时会以 SQLITE_BUSY 失败; 这是期望行为, 不做重试或忙等掩盖它。
//   - 返回前会关闭传入句柄的连接池并重新打开文件以自证 auto_vacuum 已写进库头; 调用后原句柄不可再用。
func DatabaseCompact(gdb *gorm.DB) (CompactResult, error) {
	var result CompactResult
	if gdb == nil || gdb.Dialector == nil {
		return result, fmt.Errorf("database handle is not initialized")
	}
	if gdb.Dialector.Name() != "sqlite" {
		return result, fmt.Errorf("database compaction is supported only for SQLite (dialect=%s)", gdb.Dialector.Name())
	}
	filePath := sqliteDatabasePath(gdb)
	if filePath == "" {
		return result, fmt.Errorf("SQLite database file path is unavailable; in-memory databases cannot be compacted")
	}
	started := time.Now()
	// database/sql 是连接池, 普通 db.Exec 可能让 PRAGMA 与 VACUUM 落到不同连接; Connection 将两者固定在同一连接。
	err := gdb.Connection(func(conn *gorm.DB) error {
		if err := conn.Exec("PRAGMA wal_checkpoint(TRUNCATE)").Error; err != nil {
			return fmt.Errorf("WAL checkpoint failed (service may still be running; stop it before maintenance): %w", err)
		}
		if err := conn.Raw("PRAGMA auto_vacuum").Scan(&result.AutoVacuumBefore).Error; err != nil {
			return fmt.Errorf("read auto_vacuum before compaction: %w", err)
		}
		if err := sqliteLogicalSize(conn, &result.BeforeBytes); err != nil {
			return fmt.Errorf("read SQLite size before compaction: %w", err)
		}
		beforeFileBytes, err := sqliteFileSize(filePath)
		if err != nil {
			return fmt.Errorf("stat SQLite file before compaction: %w", err)
		}
		result.BeforeFileBytes = beforeFileBytes
		// 对存量数据库单独设置此 pragma 会被静默忽略; 必须在同一连接紧接着 VACUUM 才能落盘。
		if err := conn.Exec("PRAGMA auto_vacuum=INCREMENTAL").Error; err != nil {
			return fmt.Errorf("set incremental auto_vacuum: %w", err)
		}
		if err := conn.Exec("VACUUM").Error; err != nil {
			return fmt.Errorf("VACUUM failed (service may still be running; stop it before maintenance): %w", err)
		}
		if err := conn.Raw("PRAGMA auto_vacuum").Scan(&result.AutoVacuumAfter).Error; err != nil {
			return fmt.Errorf("read auto_vacuum after compaction: %w", err)
		}
		if err := sqliteLogicalSize(conn, &result.AfterBytes); err != nil {
			return fmt.Errorf("read SQLite size after compaction: %w", err)
		}
		afterFileBytes, err := sqliteFileSize(filePath)
		if err != nil {
			return fmt.Errorf("stat SQLite file after compaction: %w", err)
		}
		result.AfterFileBytes = afterFileBytes
		return nil
	})
	if err != nil {
		return result, err
	}
	// 关闭旧连接池并重新打开文件, 自证 auto_vacuum 已写入数据库头而非仅留在连接状态。
	oldSQLDB, err := gdb.DB()
	if err != nil {
		return result, fmt.Errorf("get SQLite connection pool after compaction: %w", err)
	}
	if err := oldSQLDB.Close(); err != nil {
		return result, fmt.Errorf("close SQLite connection pool after compaction: %w", err)
	}
	verifyDB, err := gorm.Open(sqlite.Open(filePath), &gorm.Config{})
	if err != nil {
		return result, fmt.Errorf("reopen SQLite database to verify auto_vacuum: %w", err)
	}
	verifySQLDB, err := verifyDB.DB()
	if err != nil {
		return result, fmt.Errorf("get reopened SQLite connection pool: %w", err)
	}
	defer verifySQLDB.Close()
	if err := verifyDB.Raw("PRAGMA auto_vacuum").Scan(&result.AutoVacuumAfter).Error; err != nil {
		return result, fmt.Errorf("verify auto_vacuum after reopening database: %w", err)
	}
	if result.AutoVacuumAfter != 2 {
		return result, fmt.Errorf("compaction verification failed: auto_vacuum after reopening is %d, want 2", result.AutoVacuumAfter)
	}
	result.DurationMS = time.Since(started).Milliseconds()
	result.ReclaimedBytes = result.BeforeBytes - result.AfterBytes
	if result.ReclaimedBytes < 0 {
		// 说明这个库本来就没有可回收空间, 变大的部分是 INCREMENTAL 所需的 ptrmap 页。
		log.Infof("database compact: net size increased by %d bytes; this database had little free space, the growth is the ptrmap pages required by incremental auto_vacuum", -result.ReclaimedBytes)
	}
	log.Infof("database compact: logical_before=%d logical_after=%d reclaimed=%d file_before=%d file_after=%d duration_ms=%d auto_vacuum_before=%d auto_vacuum_after=%d", result.BeforeBytes, result.AfterBytes, result.ReclaimedBytes, result.BeforeFileBytes, result.AfterFileBytes, result.DurationMS, result.AutoVacuumBefore, result.AutoVacuumAfter)
	return result, nil
}

func sqliteLogicalSize(gdb *gorm.DB, size *int64) error {
	var pageCount, pageSize int64
	if err := gdb.Raw("PRAGMA page_count").Scan(&pageCount).Error; err != nil {
		return err
	}
	if err := gdb.Raw("PRAGMA page_size").Scan(&pageSize).Error; err != nil {
		return err
	}
	*size = pageCount * pageSize
	return nil
}

func sqliteFileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// sqliteDatabasePath 从 SQLite dialector DSN 中取数据库文件路径, 并去掉 query 参数。
func sqliteDatabasePath(gdb *gorm.DB) string {
	var dsn string
	switch dialector := gdb.Dialector.(type) {
	case sqlite.Dialector:
		dsn = dialector.DSN
	case *sqlite.Dialector:
		dsn = dialector.DSN
	default:
		return ""
	}
	if dsn == "" || dsn == ":memory:" {
		return ""
	}
	if i := strings.IndexByte(dsn, '?'); i >= 0 {
		dsn = dsn[:i]
	}
	// 支持 file:/path 形式的 DSN(URI 模式): filepath.Clean 不会去掉这个前缀。
	dsn = strings.TrimPrefix(dsn, "file:")
	if dsn == "" {
		return ""
	}
	return filepath.Clean(dsn)
}

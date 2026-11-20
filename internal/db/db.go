package db

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db/migrate"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var db *gorm.DB

// fingerprint 标识"当前这个库", 用于让进程级缓存在换库后自动失效。
//
// 为什么需要它: 进程级缓存键通常只用业务主键(如 user:1), 而不同库里的 user 1
// 是两条无关记录。测试逐用例建临时库、运维切换到恢复副本, 都会让同一个键指向
// 不同的数据 —— 缓存于是把 A 库的结果给了 B 库。把库的身份并入缓存键,
// 就不必依赖"换库时记得清缓存"这种口头约定(那种约定在 -shuffle 下必然破功)。
var fingerprint string

// Fingerprint 返回当前库的标识; 未初始化时返回 "nosql", 不会与真实库冲突。
//
// 只用 DSN 的**哈希**而不是原文: DSN 里可能含口令(MySQL/Postgres), 而缓存键
// 会被写进日志或指标。sha256 前 8 字节足够区分同进程内的几个库, 又不可逆。
func Fingerprint() string {
	if fingerprint == "" {
		return "nosql"
	}
	return fingerprint
}

func setFingerprint(dbType, dsn string) {
	sum := sha256.Sum256([]byte(dbType + "|" + dsn))
	fingerprint = hex.EncodeToString(sum[:8])
}

func InitDB(dbType, dsn string, debug bool) error {
	setFingerprint(dbType, dsn)
	var err error
	gormConfig := gorm.Config{Logger: logger.Discard}
	if debug {
		gormConfig.Logger = logger.Default.LogMode(logger.Info)
	}

	switch dbType {
	case "sqlite":
		db, err = initSQLite(dsn, &gormConfig)
	case "mysql":
		db, err = initMySQL(dsn, &gormConfig)
	case "postgres", "postgresql":
		db, err = initPostgres(dsn, &gormConfig)
	default:
		return fmt.Errorf("unsupported database type: %s", dbType)
	}

	if err != nil {
		return err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return err
	}

	// 连接池按方言分档。SQLite 只有一把写锁, 池子开得越大, 越容易让两个连接同时
	// 进入"读后改写"的事务而互相把对方挤成 database is locked —— 池子上限不提高吞吐,
	// 只放大锁冲突: 线上 30 天里 13 次 database is locked 全部出在结算任务上, 每一次都
	// 靠重试救回来, 说明问题不是并发不够而是并发过多。8 个连接足够覆盖"若干转发协程
	// 各写一条计费明细 + 一个定时任务"的实际并发度, 且全部写操作仍是短事务。
	// MySQL/Postgres 是真并发数据库, 维持原有上限。
	maxOpen, maxIdle := 100, 10
	if dbType == "sqlite" {
		maxOpen, maxIdle = 8, 8
	}
	sqlDB.SetMaxIdleConns(maxIdle)
	sqlDB.SetMaxOpenConns(maxOpen)
	sqlDB.SetConnMaxLifetime(time.Hour)
	sqlDB.SetConnMaxIdleTime(10 * time.Minute)

	// 静态凭据落库加密: 字段序列化器随模型 tag 生效, map 写入由回调兜底(见 secretfields.go)。
	registerSecretFieldProtection(db)

	if err := migrate.BeforeAutoMigrate(db); err != nil {
		return err
	}
	if err := db.AutoMigrate(
		&model.User{},
		&model.EmailCode{},
		&model.Channel{},
		&model.ChannelKey{},
		&model.ChannelModel{},
		&model.ChannelGrant{},
		&model.Group{},
		&model.GroupItem{},
		&model.LLMInfo{},
		&model.ModelAutoAddRule{}, // 按(用户, 模型)存放的自动添加条件; 独立于价格表, 免受价格同步覆盖。
		&model.ModelCapability{},
		&model.APIKey{},
		&model.BillingRecord{},
		&model.BalanceReservation{},
		&model.BillingSettlementTask{},
		&model.BillingLedger{},
		&model.RechargeOrder{},
		&model.Withdrawal{},
		&model.RedemptionBatch{},
		&model.RedemptionCode{},
		&model.Setting{},
		&model.AuditLog{},
		&model.Notification{},
		&model.Webhook{},
		&model.WebhookDelivery{},
		&model.BalanceAlert{},
		&model.Ticket{},
		&model.TicketMessage{},
		&model.StatsTotal{},
		&model.StatsDaily{},
		&model.StatsHourly{},
		&model.StatsAPIKey{},
		&model.StatsChannelDaily{},
		&model.StatsChannelModelDaily{},
		&model.RelayRequest{},
		&model.RelayRequestAttempt{},
		&model.RouteStateRecord{},
		&model.RouteCooldownRecord{},
		&model.RouteMetricRecord{},
		&model.RateLimitPolicy{},
		&model.RateLimitUsageHourly{},
		&model.BillingReconcile{},
		&model.ChannelHealthRecord{},
		&model.ChannelQualitySnapshot{},
		&model.ChannelEgress{},
		&model.EgressMetricRecord{}, // 出口成绩与冷却的持久快照; 重启后按它恢复, 不必从零重新学。
		&model.VideoJob{},
		&model.ChannelAlert{},
		&model.AlertSilenceWindow{},
		&model.SpendBudget{},
		&model.MarketFavorite{},
		&model.FxRate{},
		&model.UpstreamBill{},
		&model.Dispute{},
		&model.SettlementStatement{},
		&model.SettlementRun{},
		&model.BillingRollup{}, // 明细被留存清理后, 终身口径读口改从这张日汇总表补齐。
		&model.WriteAnchor{},   // 空锚点表: 短事务读后写的写锁把手, 由 db.AcquireWriteLock 使用。
		&migrate.MigrationRecord{},
	); err != nil {
		return err
	}
	// 工单 resolved_at 回填(一次性幂等): 该列新加, 存量 resolved 行为空则永远进不了
	// 自动关闭计时。仅对空值兜底 —— 正常写入由 op.setTicketStatus 维护
	// (resolved ⇔ resolved_at 非空), 已有值的行不会被改写。
	if err := db.Exec("UPDATE tickets SET resolved_at = updated_at WHERE status = ? AND resolved_at IS NULL",
		model.TicketStatusResolved).Error; err != nil {
		return fmt.Errorf("backfill ticket resolved_at: %w", err)
	}
	if err := migrate.AfterAutoMigrate(db); err != nil {
		return err
	}
	// Postgres: schema changes during migrations can invalidate cached prepared plans
	// (e.g. "cached plan must not change result type"). Clear them.
	if db.Dialector != nil && db.Dialector.Name() == "postgres" {
		db.Exec("DEALLOCATE ALL")
		db.Exec("DISCARD ALL")
	}
	return nil
}

// initSQLite 使用指定文件路径初始化 SQLite，并为每个连接应用运行参数。
func initSQLite(path string, config *gorm.Config) (*gorm.DB, error) {
	params := url.Values{}
	params.Add("_pragma", "journal_mode(WAL)")
	params.Add("_pragma", "synchronous(NORMAL)")
	params.Add("_pragma", "cache_size(10000)")
	params.Add("_pragma", "busy_timeout(5000)")
	params.Add("_pragma", "foreign_keys(ON)")
	// auto_vacuum(INCREMENTAL) 只对空库/新建库有效; 对已有内容的库会静默无效(生产实测 auto_vacuum=0)。
	// 存量库须停机后运行一次 switchyard db-maintenance --apply 才能落地; 落地后此 pragma 才真正生效。
	params.Add("_pragma", "auto_vacuum(INCREMENTAL)")
	params.Add("_pragma", "mmap_size(268435456)")
	params.Add("_pragma", "locking_mode(NORMAL)")
	return gorm.Open(sqlite.Open(path+"?"+params.Encode()), config)
}

func initMySQL(dsn string, config *gorm.Config) (*gorm.DB, error) {
	// DSN 格式: user:password@tcp(host:port)/dbname?charset=utf8mb4&parseTime=True&loc=Local
	if !strings.Contains(dsn, "?") {
		dsn += "?charset=utf8mb4&parseTime=True&loc=Local"
	}
	return gorm.Open(mysql.Open(dsn), config)
}

func initPostgres(dsn string, config *gorm.Config) (*gorm.DB, error) {
	// DSN 格式: host=localhost user=postgres password=xxx dbname=switchyard port=5432 sslmode=disable
	return gorm.Open(postgres.Open(dsn), config)
}

func Close() error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func GetDB() *gorm.DB {
	return db
}

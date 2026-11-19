package migrate

import (
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// TestMigration037BackfillsNullAttemptsRows 复现线上那起"迁移报告成功却一条没生效"的事故。
//
// 事故本质: notify_attempts 这一列由 036 同批新增, AutoMigrate 给存量行补列时填的是 NULL,
// 而 036 的判定条件 "notify_attempts = 0" 在 SQL 三值逻辑下对 NULL 恒不成立 —— 命中 0 行。
// 更关键的是: 036 在生产上已经记录为"执行成功", 它永远不会再跑一遍。
// 因此本用例只调用 037, 且必须面对 NULL 行 —— 这是线上真实处境, 也是唯一的补救路径。
//
// 构造上刻意绕开 AutoMigrate: 现在模型给该列带了 not null 默认值, 用 AutoMigrate 造出来的表
// 根本插不进 NULL, 测试会失去意义。所以这里手写一张"改造前形状"的表(无约束)。
func TestMigration037BackfillsNullAttemptsRows(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Exec(`CREATE TABLE channel_alerts (
		id integer PRIMARY KEY AUTOINCREMENT,
		channel_id integer, channel_code text, subject text, kind text, reason text,
		detail text, notified numeric, created_at datetime,
		notified_at datetime, notify_attempts integer, next_notify_at datetime, last_notify_error text)`).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	// 三条存量行, 形态覆盖线上真实分布。
	insert := func(kind string, notified any, attempts any, next any) int64 {
		if err := conn.Exec("INSERT INTO channel_alerts (kind, subject, reason, notified, notify_attempts, next_notify_at, created_at) VALUES (?,?,?,?,?,?,?)",
			kind, "存量渠道", "probe failed", notified, attempts, next, now.Add(-1*time.Hour)).Error; err != nil {
			t.Fatalf("insert %s: %v", kind, err)
		}
		var id int64
		if err := conn.Raw("SELECT last_insert_rowid()").Scan(&id).Error; err != nil {
			t.Fatal(err)
		}
		return id
	}
	legacy := insert("down", 0, nil, nil)         // 未送达 + 从未尝试 + 窗内: 必须被捞回
	delivered := insert("recovered", 1, nil, nil) // 已送达: 不许动
	// 窗外历史告警: 三周前的存量(线上首次部署时有 245 条这种行),
	// 迁移必须跳过排期 —— 否则补发任务会向 Telegram 灌几百条失去上下文的旧告警。
	expired := insert("degraded", 0, nil, nil)
	if err := conn.Exec("UPDATE channel_alerts SET created_at = ? WHERE id = ?", time.Now().Add(-72*time.Hour), expired).Error; err != nil {
		t.Fatal(err)
	}

	if err := Migration037_AlertRedeliverBackfillNullFix(conn); err != nil {
		t.Fatalf("migration 037: %v", err)
	}

	got := model.ChannelAlert{}
	if err := conn.Where("id = ?", legacy).First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.NextNotifyAt == nil {
		t.Fatal("037 未能把 NULL 尝试次数的存量告警排进补发队列")
	}
	if got.Notified {
		t.Fatal("回填不得把未送达标记成已送达")
	}
	if got.LastNotifyError == "" {
		t.Fatal("回填应说明这些是历史未记录原因的告警")
	}
	if got.NotifyAttempts != 0 {
		t.Fatalf("回填应把 NULL 归一到 0, got %d", got.NotifyAttempts)
	}

	kept := model.ChannelAlert{}
	if err := conn.Where("id = ?", delivered).First(&kept).Error; err != nil {
		t.Fatal(err)
	}
	if kept.NextNotifyAt != nil {
		t.Fatal("已送达的告警不应被排进补发队列")
	}

	// 超窗行: 不排期(窗口防刷屏), 但 NULL 必须已归零。
	expRow := model.ChannelAlert{}
	if err := conn.Where("id = ?", expired).First(&expRow).Error; err != nil {
		t.Fatal(err)
	}
	if expRow.NextNotifyAt != nil {
		t.Fatal("超出 48 小时窗口的历史告警不应被排期(会向 Telegram 灌旧告警刷屏)")
	}
	if expRow.NotifyAttempts != 0 {
		t.Fatalf("超窗行 attempts 应归零, got %d", expRow.NotifyAttempts)
	}
	if expRow.Notified {
		t.Fatal("超窗处置不得改变送达标记")
	}
}

// TestMigration036AlsoNullSafe 036 的判定也必须 NULL 安全: 新装库上 037 同样会跑,
// 但两边口径必须一致, 否则同一份数据在两套安装上得到不同的补发队列。
func TestMigration036AlsoNullSafe(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Exec(`CREATE TABLE channel_alerts (
		id integer PRIMARY KEY AUTOINCREMENT,
		channel_id integer, channel_code text, subject text, kind text, reason text,
		detail text, notified numeric, created_at datetime,
		notified_at datetime, notify_attempts integer, next_notify_at datetime, last_notify_error text)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := conn.Exec("INSERT INTO channel_alerts (kind, subject, reason, notified, notify_attempts, created_at) VALUES ('down','存量','probe failed',0,NULL,?)", time.Now()).Error; err != nil {
		t.Fatal(err)
	}
	if err := Migration036_AlertRedeliverBackfill(conn); err != nil {
		t.Fatalf("migration 036: %v", err)
	}
	got := model.ChannelAlert{}
	if err := conn.Where("kind = ?", "down").First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.NextNotifyAt == nil {
		t.Fatal("036 对 NULL 尝试次数的行未生效")
	}
}

// TestMigration037IsIdempotent 重复执行不得重置排期或改写退避节奏。
func TestMigration037IsIdempotent(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.AutoMigrate(&model.ChannelAlert{}); err != nil {
		t.Fatal(err)
	}
	scheduled := time.Now().Add(37 * time.Minute)
	active := model.ChannelAlert{
		Kind: "down", Subject: "进行中渠道", Reason: "probe failed",
		NotifyAttempts: 2, NextNotifyAt: &scheduled, LastNotifyError: "telegram api 400",
		CreatedAt: time.Now(),
	}
	if err := conn.Create(&active).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := Migration037_AlertRedeliverBackfillNullFix(conn); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	got := model.ChannelAlert{}
	if err := conn.Where("id = ?", active.ID).First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.NextNotifyAt == nil || !got.NextNotifyAt.Equal(scheduled) {
		t.Fatalf("已有排期被改写: %v", got.NextNotifyAt)
	}
	if got.NotifyAttempts != 2 || got.LastNotifyError != "telegram api 400" {
		t.Fatalf("进行中记录被改写: attempts=%d err=%q", got.NotifyAttempts, got.LastNotifyError)
	}
}

// TestAutoMigrateKeepsExistingAlertRows 保命测试: 对"生产当前形状"的表跑 AutoMigrate 必须成功。
//
// v1.5.11 的线上崩溃正是这条路径: 当时模型给 notify_attempts 加了 not null,
// GORM 判定列定义变化, 走 SQLite 表重建(建 __temp 新表 → 拷行), 而存量 572 行该列全为 NULL,
// 拷贝触发 NOT NULL 约束失败, 数据库初始化中止, 服务起不来(启动日志:
// "constraint failed: NOT NULL constraint failed: channel_alerts__temp.notify_attempts")。
//
// 因此本用例锁死两件事:
//  1. 模型字段不得携带 not null 之类的严格约束 —— 一旦有人加回来, 这里立即失败;
//  2. AutoMigrate 对已有数据的表必须是幂等无损的(行数不变、值不变)。
//
// 测试表刻意手写建出(不经 AutoMigrate), 以保证起始形状就是"列可空、全 NULL"的生产状态。
func TestAutoMigrateKeepsExistingAlertRows(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Exec(`CREATE TABLE channel_alerts (
		id integer PRIMARY KEY AUTOINCREMENT,
		channel_id integer, channel_code text, subject text, kind text, reason text,
		detail text, notified numeric, created_at datetime,
		notified_at datetime, notify_attempts integer, next_notify_at datetime, last_notify_error text)`).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(-1 * time.Hour)
	for i := 0; i < 3; i++ {
		if err := conn.Exec("INSERT INTO channel_alerts (kind, subject, reason, notified, notify_attempts, created_at) VALUES ('down','存量','probe failed',0,NULL,?)", now).Error; err != nil {
			t.Fatal(err)
		}
	}

	if err := conn.AutoMigrate(&model.ChannelAlert{}); err != nil {
		t.Fatalf("AutoMigrate 对已有数据的表失败(生产即启动崩溃): %v", err)
	}

	var count int64
	if err := conn.Table("channel_alerts").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("AutoMigrate 后行数 %d, want 3 (数据丢失)", count)
	}

	// 迁移把 NULL 归零并排期回填。
	if err := Migration037_AlertRedeliverBackfillNullFix(conn); err != nil {
		t.Fatalf("migration 037: %v", err)
	}
	var nullCount, scheduled int64
	if err := conn.Table("channel_alerts").Where("notify_attempts IS NULL").Count(&nullCount).Error; err != nil {
		t.Fatal(err)
	}
	if nullCount != 0 {
		t.Fatalf("037 之后仍有 %d 行 notify_attempts 为 NULL", nullCount)
	}
	if err := conn.Table("channel_alerts").Where("next_notify_at IS NOT NULL").Count(&scheduled).Error; err != nil {
		t.Fatal(err)
	}
	if scheduled != 3 {
		t.Fatalf("排期回填 %d 行, want 3", scheduled)
	}
}

// TestAutoMigrateAddsAlertColumnsToLegacyTable 更早的存量形状: 表里还没有那四个新列。
// AutoMigrate 必须能加列成功(加列后存量行该列为 NULL), 037 随后完成补救 —— 新装/旧库两条路都要通。
func TestAutoMigrateAddsAlertColumnsToLegacyTable(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Exec(`CREATE TABLE channel_alerts (
		id integer PRIMARY KEY AUTOINCREMENT,
		channel_id integer, channel_code text, subject text, kind text, reason text,
		detail text, notified numeric, created_at datetime)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := conn.Exec("INSERT INTO channel_alerts (kind, subject, reason, notified, created_at) VALUES ('down','旧库','probe failed',0,?)", time.Now().Add(-1*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if err := conn.AutoMigrate(&model.ChannelAlert{}); err != nil {
		t.Fatalf("AutoMigrate 加列失败: %v", err)
	}
	if err := Migration037_AlertRedeliverBackfillNullFix(conn); err != nil {
		t.Fatalf("migration 037: %v", err)
	}
	got := model.ChannelAlert{}
	if err := conn.Where("kind = ?", "down").First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.NextNotifyAt == nil {
		t.Fatal("旧库形状下 037 未完成排期回填")
	}
	if got.NotifyAttempts != 0 {
		t.Fatalf("notify_attempts = %d, want 0", got.NotifyAttempts)
	}
}

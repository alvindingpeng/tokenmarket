package db_test

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// sqliteTestDB 用 InitDB 建一个临时库: 只有走正式初始化路径才有 db_write_anchor
// (它由 AutoMigrate 建出), 而取锁语义必须建立在真实库而不是裸 gorm.Open 上。
func sqliteTestDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), name), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	conn := db.GetDB()
	if !conn.Migrator().HasTable("db_write_anchor") {
		t.Fatalf("db_write_anchor 未随 AutoMigrate 建出")
	}
	return conn
}

// TestAcquireWriteLockAnchorTableStaysEmpty 钉住"空表锚点"这个设计: 取锁靠的是零命中的
// 空更新, 因此取多少次数都不往表里落一行 —— 否则锚点表会长成一张每事务一行的日志表。
func TestAcquireWriteLockAnchorTableStaysEmpty(t *testing.T) {
	conn := sqliteTestDB(t, "anchor_empty.db")
	for i := 0; i < 50; i++ {
		if err := conn.Transaction(func(tx *gorm.DB) error {
			return db.AcquireWriteLock(tx)
		}); err != nil {
			t.Fatalf("acquire lock #%d: %v", i, err)
		}
	}
	var rows int64
	if err := conn.Table("db_write_anchor").Count(&rows).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 0 {
		t.Fatalf("db_write_anchor 应恒为空, 实际 %d 行", rows)
	}
}

// TestAcquireWriteLockNoOpOnNilTx 保证"没事务就什么也不做" —— 调用方在只读路径上
// 误传 nil 不应 panic, 也不应凭空开一条写语句。
func TestAcquireWriteLockNoOpOnNilTx(t *testing.T) {
	if err := db.AcquireWriteLock(nil); err != nil {
		t.Fatalf("nil tx 应返回 nil, 得到 %v", err)
	}
}

// TestAcquireWriteLockCreatesMissingAnchorOnRawDB 覆盖裸 gorm.Open 建出的库(历史测试、
// 外部工具生成的副本)没有锚点表的情况: 取锁要自建表并成功, 而不是把业务事务带崩。
func TestAcquireWriteLockCreatesMissingAnchorOnRawDB(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "raw.db")), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if conn.Migrator().HasTable("db_write_anchor") {
		t.Fatalf("前提不成立: 裸库不该已有锚点表")
	}
	if err := conn.Transaction(func(tx *gorm.DB) error {
		return db.AcquireWriteLock(tx)
	}); err != nil {
		t.Fatalf("缺表时取锁应自建表并成功, 得到 %v", err)
	}
	if !conn.Migrator().HasTable("db_write_anchor") {
		t.Fatalf("取锁后锚点表应已建出")
	}
}

// TestAcquireWriteLockTurnsUpgradeFailureIntoWait 是这次改动的核心断言, 且是确定性的:
// 另一个连接持着写锁时, "先读后写"事务在 DEFERRED 下会在写语句上立刻撞 database is locked
// (busy_timeout 管不了这条路径), 而先取写锁的事务会一直排队到持锁者提交。
func TestAcquireWriteLockTurnsUpgradeFailureIntoWait(t *testing.T) {
	conn := sqliteTestDB(t, "upgrade.db")
	if err := conn.Exec("CREATE TABLE lock_probe (id INTEGER PRIMARY KEY, v INTEGER)").Error; err != nil {
		t.Fatalf("create probe table: %v", err)
	}
	if err := conn.Exec("INSERT INTO lock_probe (id, v) VALUES (1, 0)").Error; err != nil {
		t.Fatalf("seed probe: %v", err)
	}

	// holdWriteLock 占用写锁 hold 这么久, 并在拿到锁后关闭 ready。
	holdWriteLock := func(ready chan<- struct{}, hold time.Duration) <-chan error {
		done := make(chan error, 1)
		go func() {
			done <- conn.Transaction(func(tx *gorm.DB) error {
				if err := tx.Exec("UPDATE lock_probe SET v = v WHERE id = 1").Error; err != nil {
					return err
				}
				close(ready)
				time.Sleep(hold)
				return nil
			})
		}()
		return done
	}

	t.Run("deferred 读后写会失败", func(t *testing.T) {
		ready := make(chan struct{})
		holderDone := holdWriteLock(ready, 300*time.Millisecond)
		<-ready
		time.Sleep(30 * time.Millisecond) // 让持锁者确实进入 sleep
		err := conn.Transaction(func(tx *gorm.DB) error {
			var v int
			if derr := tx.Raw("SELECT v FROM lock_probe WHERE id = 1").Scan(&v).Error; derr != nil {
				return derr
			}
			return tx.Exec("UPDATE lock_probe SET v = v + 1 WHERE id = 1").Error
		})
		if err == nil {
			t.Fatalf("写锁被占用时, 未取锁的读后写事务应当失败")
		}
		if !containsLockError(err.Error()) {
			t.Fatalf("失败原因应是锁冲突, 实际 %v", err)
		}
		if herr := <-holderDone; herr != nil {
			t.Fatalf("持锁者不应失败: %v", herr)
		}
	})

	t.Run("取锁后改为排队等待", func(t *testing.T) {
		ready := make(chan struct{})
		holderDone := holdWriteLock(ready, 300*time.Millisecond)
		<-ready
		time.Sleep(30 * time.Millisecond)
		start := time.Now()
		err := conn.Transaction(func(tx *gorm.DB) error {
			if aerr := db.AcquireWriteLock(tx); aerr != nil {
				return aerr
			}
			var v int
			if derr := tx.Raw("SELECT v FROM lock_probe WHERE id = 1").Scan(&v).Error; derr != nil {
				return derr
			}
			return tx.Exec("UPDATE lock_probe SET v = v + 1 WHERE id = 1").Error
		})
		if err != nil {
			t.Fatalf("取锁后的读后写事务应当排队成功, 实际失败: %v", err)
		}
		if waited := time.Since(start); waited < 100*time.Millisecond {
			t.Fatalf("应当真的等到持锁者提交(>=100ms), 实际只用了 %v", waited)
		}
		if herr := <-holderDone; herr != nil {
			t.Fatalf("持锁者不应失败: %v", herr)
		}
	})
}

// TestAcquireWriteLockSurvivesConcurrentReadThenWrite 是抖动复现: 8 个写协程 + 4 个
// 读后写协程, 每个读后写事务都先取锁。断言"一次锁冲突都没有", 而不只是"最后数据对"。
func TestAcquireWriteLockSurvivesConcurrentReadThenWrite(t *testing.T) {
	conn := sqliteTestDB(t, "contend.db")
	if err := conn.Exec("CREATE TABLE acct (id INTEGER PRIMARY KEY, balance INTEGER)").Error; err != nil {
		t.Fatalf("create table: %v", err)
	}
	for i := 1; i <= 8; i++ {
		if err := conn.Exec("INSERT INTO acct (id, balance) VALUES (?, 100000)", i).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	stop := make(chan struct{})
	errCh := make(chan error, 64)
	var writers, readers = 8, 4
	done := make(chan struct{}, writers+readers)
	for i := 0; i < writers; i++ {
		go func(id int) {
			defer func() { done <- struct{}{} }()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := conn.Transaction(func(tx *gorm.DB) error {
					return tx.Exec("UPDATE acct SET balance = balance - 1 WHERE id = ?", id%8+1).Error
				}); err != nil {
					select {
					case errCh <- fmt.Errorf("writer: %w", err):
					default:
					}
					return
				}
			}
		}(i)
	}
	for i := 0; i < readers; i++ {
		go func(id int) {
			defer func() { done <- struct{}{} }()
			for {
				select {
				case <-stop:
					return
				default:
				}
				err := conn.Transaction(func(tx *gorm.DB) error {
					if aerr := db.AcquireWriteLock(tx); aerr != nil {
						return aerr
					}
					var bal int
					if derr := tx.Raw("SELECT balance FROM acct WHERE id = ?", id%8+1).Scan(&bal).Error; derr != nil {
						return derr
					}
					if bal <= 0 {
						return nil
					}
					return tx.Exec("UPDATE acct SET balance = balance - 1 WHERE id = ?", id%8+1).Error
				})
				if err != nil {
					select {
					case errCh <- fmt.Errorf("reader: %w", err):
					default:
					}
					return
				}
			}
		}(i)
	}
	time.Sleep(400 * time.Millisecond)
	close(stop)
	for i := 0; i < writers+readers; i++ {
		<-done
	}
	close(errCh)
	for err := range errCh {
		t.Fatalf("取写锁后不该还有锁冲突: %v", err)
	}
}

func containsLockError(msg string) bool {
	for _, sub := range []string{"database is locked", "SQLITE_BUSY", "database table is locked"} {
		if len(msg) >= len(sub) {
			for i := 0; i+len(sub) <= len(msg); i++ {
				if msg[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}

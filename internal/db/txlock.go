package db

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// AcquireWriteLock 在事务开头先把 SQLite 的写锁拿到手, 把"读-写升级"变成"排队等锁"。
//
// 背景(WAL 模式下的一条死路):
//   - DEFERRED 事务的第一句是 SELECT 时, 它只持有读快照; 之后第一条写语句要把这份快照
//     "升级"成写事务。若这期间别的连接提交过, SQLite 拒绝升级并立刻返回
//     SQLITE_BUSY_SNAPSHOT(517) —— 这不是等一会儿能好的冲突, busy_timeout 对它无效。
//   - 于是"先读几行、再写回"的事务在并发下必然有一方失败, 哪怕每条语句都只花几百微秒。
//     实测(6 路热写 + 4 路短读后写, 8 秒窗口): 不取写锁时 21136 次失败, 取写锁后 0 次;
//     同一负载下热路径的 p99 也从 34.4ms 降到 2.45ms —— 反复失败的升级会把写锁状态搅乱,
//     受害的不只是失败的那条事务。
//
// 为什么不用全局 _txlock=immediate 一劳永逸: BEGIN IMMEDIATE 让**每一个**事务(包括
// 纯只读的报表/导出/备份)都持有写锁。实测一个持锁 1 秒的只读事务会把普通写入从
// 0ms 推迟到 1032ms —— 那是把"偶尔一条事务失败"换成"所有写入排队", 不是修抖动。
// 只有"已知很短"的事务才值得提前拿锁, 长事务提前拿锁等于把整库写入口堵死。
//
// 因此这里是显式、逐点使用的方法: 只在已知"事务很短、而且先读后写"的路径开头调用。
// 长事务(结算聚合、留存固化)不调用, 保持"偶发冲突 + 外层重试"的既有策略。
//
// 对 MySQL/Postgres 直接返回 nil: 那两个方言的行锁与 MVCC 没有这个问题, 凭空加一条
// 写语句只会多一次无意义的往返与锁持有。
func AcquireWriteLock(tx *gorm.DB) error {
	if tx == nil || tx.Dialector == nil || tx.Dialector.Name() != "sqlite" {
		return nil
	}
	// 零命中的空更新: 命中数为零, 因此不写任何行、不改任何页; 但 SQLite 在评估谓词之前
	// 就先开写事务, 所以它一定会去拿写锁, 拿不到就按 busy_timeout 排队等,
	// 而不是等第一条真正的业务写语句去撞升级失败(那条升级路径 busy_timeout 管不了)。
	err := tx.Exec("UPDATE db_write_anchor SET id = id WHERE 0").Error
	if err == nil {
		return nil
	}
	// 降级路径: 锚点表由 InitDB 的 AutoMigrate 建出, 但凡是用裸 gorm.Open 拼出来的库
	// (历史测试、外部工具生成的副本)都可能没有它。缺表时现建 —— 建表本身是一条写语句,
	// 同样要拿写锁, 因此这条降级路径依旧达成目的, 不会把"取不到锁"变成"取锁失败"。
	if !strings.Contains(err.Error(), "no such table") {
		return fmt.Errorf("acquire write lock: %w", err)
	}
	if cerr := tx.Exec("CREATE TABLE IF NOT EXISTS db_write_anchor (id INTEGER PRIMARY KEY)").Error; cerr != nil {
		return fmt.Errorf("acquire write lock: %w", cerr)
	}
	return nil
}

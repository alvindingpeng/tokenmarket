package op

import (
	"context"
	"errors"
	"strings"
	"time"

	sqlite "github.com/glebarez/go-sqlite"
)

// settlementMaxAttempts 是结算完整流程(聚合读 + 写事务)的最大尝试次数(含首次)。
//
// 从 5 提到 8: 结算撞的不是"忙一会儿就好"的锁等待, 而是 WAL 下的读快照失效
// (SQLITE_BUSY_SNAPSHOT, 517) 与普通 BUSY —— busy_timeout 对前一条路径完全无效,
// 唯一出路是整轮重来。碰撞的一方往往是另一个日结或一次审核, 它自己就要几百毫秒,
// 而 5 次 × 150ms 的总退避窗口比对手还短, 于是重试全部落在对方的持锁区间里, 五次全废
// (实测并发压测下 24/26 次仍然失败)。8 次 × 1.6s 让最后一次重试落在对手提交之后。
//
// 注意这**只是兜底**: v1.10.16 把聚合读挪出了写事务、写事务开头就取写锁之后, 同样的
// 负载下失败数已经是 0。单靠拉长重试窗口实测仍然救不回来(8 次里失败 6 次) —— 真正
// 起作用的是锁的获取顺序(见 db.AcquireWriteLock 与 settlementGenerateDayOnce)。
//
// 为什么不再往上加: 结算的调用方是定时任务与管理员手动重算, 都有分钟级的时间预算,
// 1.6s 的等待可以接受; 而重试窗口长到分钟级就变成"锁等待放大", 出问题时更难定位。
const settlementMaxAttempts = 8

// settlementRetryBaseDelay / settlementRetryMaxDelay 是退避区间(指数增长后截顶)。
const (
	settlementRetryBaseDelay = 10 * time.Millisecond
	settlementRetryMaxDelay  = 500 * time.Millisecond
)

// run must finish/roll back its entire transaction before returning. The injected
// wait keeps retry policy testable without process-wide hooks or sleeping tests.
func settlementRetry(ctx context.Context, dialect string, run func() error, wait func(context.Context, time.Duration) error) error {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := run()
		if err == nil {
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if dialect != "sqlite" || !settlementSQLiteLock(err) || attempt+1 >= settlementMaxAttempts {
			return err
		}
		delay := settlementRetryDelay(attempt)
		if err := wait(ctx, delay); err != nil {
			return err
		}
	}
}

// settlementRetryDelay 返回第 attempt 次失败后的等待时长: 指数退避的上界, 再加 [0, 上界/2) 的
// 正向抖动。
//
// 抖动是为了拆开"同时开始的重试": 定时任务与管理员手动重算可能在同一毫秒撞上同一把锁,
// 固定退避会让它们每一轮都同步重来(明明只要错开一点就能一前一后成功)。抖动只向上取,
// 保证第 n 次的等待永远不小于 2^n × 10ms —— 否则抖动会把退避退化成上一轮的长度。
//
// 用 attempt 做确定性混合而不是全局随机源: 重试策略要可重现, 否则测试只能断言"大概"。
func settlementRetryDelay(attempt int) time.Duration {
	ceiling := settlementRetryBaseDelay
	for i := 0; i < attempt && ceiling < settlementRetryMaxDelay; i++ {
		ceiling *= 2
	}
	if ceiling > settlementRetryMaxDelay {
		ceiling = settlementRetryMaxDelay
	}
	span := int64(ceiling / 2)
	if span <= 0 {
		return ceiling
	}
	// 乘法必须先转 int64: 2654435761 大于 int32 上限, 直接在 int 上乘会在 386/arm 目标上
	// 以"constant overflows int"编译失败 —— 而发布矩阵里恰好有 linux/386 与 linux/arm 两档,
	// 于是 release 产物整批打不出来(GitHub Actions 的 release-assets 因此连续失败)。
	// 64 位平台上的取值不变(只是把隐式拓宽写成显式), 退避时序与测试断言完全一致。
	offset := (int64(attempt) * 2654435761 >> 3) % span
	return ceiling + time.Duration(offset)
}

func settlementRetryWait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

func settlementSQLiteLock(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		// Extended codes include BUSY_SNAPSHOT (517) and LOCKED_SHAREDCACHE.
		code := sqliteErr.Code() & 0xff
		return code == 5 || code == 6
	}
	// Compatibility for wrappers which preserve driver text but not its type.
	// Inspect unwrapped messages, not arbitrary substrings (e.g. statement names).
	for e := err; e != nil; e = errors.Unwrap(e) {
		msg := strings.ToLower(e.Error())
		for _, prefix := range []string{"database is locked", "database is busy", "database table is locked", "database schema is locked"} {
			if msg == prefix || strings.HasPrefix(msg, prefix+" (") || strings.HasPrefix(msg, prefix+":") {
				return true
			}
		}
		if msg == "sqlite_busy" || strings.HasPrefix(msg, "sqlite_busy (") || msg == "sqlite_locked" || strings.HasPrefix(msg, "sqlite_locked (") {
			return true
		}
	}
	return false
}

package op

import (
	"context"
	"errors"
	"strings"
	"time"

	sqlite "github.com/glebarez/go-sqlite"
)

// Five attempts total (including the first), with at most 150ms of backoff.
const settlementMaxAttempts = 5

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
		if err := wait(ctx, (10*time.Millisecond)<<attempt); err != nil {
			return err
		}
	}
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

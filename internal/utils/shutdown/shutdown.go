package shutdown

import (
	"os"
	"os/signal"
	"reflect"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"
)

type logger interface {
	Infof(string, ...interface{})
	Errorf(string, ...interface{})
	Warnf(string, ...interface{})
}
type hook struct {
	name string
	fn   func() error
}

var (
	ilog    logger
	funcs   []hook
	funcsMu sync.Mutex
)

const defaultHookTimeout = 35 * time.Second

func Init(log logger) { ilog = log; funcsMu.Lock(); funcs = make([]hook, 0); funcsMu.Unlock() }

// Register accepts Register(name, fn); the legacy Register(fn) form remains supported.
func Register(nameOrFn interface{}, fn ...func() error) {
	var h hook
	switch v := nameOrFn.(type) {
	case string:
		if len(fn) == 0 || fn[0] == nil {
			return
		}
		h = hook{name: v, fn: fn[0]}
	case func() error:
		h = hook{name: runtimeName(v), fn: v}
	default:
		return
	}
	funcsMu.Lock()
	funcs = append(funcs, h)
	funcsMu.Unlock()
}
func runtimeName(fn func() error) string {
	if fn == nil {
		return "unnamed"
	}
	if f := runtime.FuncForPC(reflect.ValueOf(fn).Pointer()); f != nil {
		return f.Name()
	}
	return "unnamed"
}

func timeout() time.Duration {
	raw := os.Getenv("SWITCHYARD_SHUTDOWN_TIMEOUT_SECONDS")
	if raw == "" {
		return defaultHookTimeout
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 600 {
		if ilog != nil {
			ilog.Warnf("invalid SWITCHYARD_SHUTDOWN_TIMEOUT_SECONDS=%q; using %s", raw, defaultHookTimeout)
		}
		return defaultHookTimeout
	}
	return time.Duration(n) * time.Second
}
func hooksSnapshot() []hook {
	funcsMu.Lock()
	defer funcsMu.Unlock()
	out := make([]hook, len(funcs))
	copy(out, funcs)
	return out
}

// runHooks runs hooks in reverse registration order. A timeout is reported but cannot kill Go code;
// later hooks still run so independent cleanup can proceed.
func runHooks() bool {
	hooks := hooksSnapshot()
	ok := true
	limit := timeout()
	for i := len(hooks) - 1; i >= 0; i-- {
		h := hooks[i]
		done := make(chan error, 1)
		started := time.Now()
		go func() { done <- h.fn() }()
		timer := time.NewTimer(limit)
		select {
		case err := <-done:
			timer.Stop()
			if err != nil {
				ok = false
				if ilog != nil {
					ilog.Errorf("shutdown hook %q failed: %v", h.name, err)
				}
			}
		case <-timer.C:
			ok = false
			if ilog != nil {
				ilog.Errorf("shutdown hook %q timed out after %.0f seconds; continuing with remaining hooks (hook goroutine may remain running)", h.name, time.Since(started).Seconds())
			}
		}
	}
	if ok {
		if ilog != nil {
			ilog.Infof("Shutdown completed successfully")
		}
	} else if ilog != nil {
		ilog.Errorf("Shutdown completed with errors; exit status 1")
	}
	return ok
}

// Listen waits for an OS signal, then runs hooks and exits nonzero on any failure/timeout.
func Listen() {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	if ilog != nil {
		ilog.Infof("Program started, press Ctrl+C to exit")
	}
	sig := <-quit
	signal.Stop(quit)
	if ilog != nil {
		ilog.Warnf("Received exit signal: %v", sig)
	}
	if runHooks() {
		os.Exit(0)
	}
	os.Exit(1)
}

// Shutdown runs hooks without waiting for a signal and returns success instead of exiting.
// Self-update calls this immediately before syscall.Exec: returning status (rather than os.Exit)
// ensures a failed cleanup is visible in logs but cannot prevent replacing the process image.
func Shutdown() bool { return runHooks() }

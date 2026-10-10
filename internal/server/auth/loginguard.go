package auth

import (
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/charmbracelet/log"
)

// loginAttempt 记录一个登录主体(IP+用户名)的连续失败情况。
// 全部在内存里: 登录防爆破是单进程即可生效的防线, 重启清零可接受,
// 多实例部署时应把这张表换成共享存储(与限流器同一演进路径)。
type loginAttempt struct {
	failures    int
	lastFailure time.Time
	lockedUntil time.Time
}

const (
	loginWindow        = 15 * time.Minute // 失败计数窗口: 窗口外的旧失败不再累计。
	loginMaxLockout    = time.Hour        // 指数退避封顶, 防止长期占位锁死。
	loginSweepInterval = 10 * time.Minute
)

// loginGuard 登录防爆破闸门: 按 主体IP+用户名 计连续失败, 超限后指数退避锁定。
type loginGuard struct {
	mu        sync.Mutex
	attempts  map[string]*loginAttempt
	lastSweep time.Time
}

var LoginGuard = &loginGuard{attempts: map[string]*loginAttempt{}, lastSweep: time.Now()}

func loginMaxAttempts() int {
	if n := op.SettingGetIntDefault(model.SettingKeyLoginMaxAttempts, 5); n > 0 {
		return n
	}
	return 5
}

func loginLockoutBase() time.Duration {
	seconds := op.SettingGetIntDefault(model.SettingKeyLoginLockoutSeconds, 900)
	if seconds <= 0 {
		return 0 // 管理员显式设 0 表示不做锁定退避, 仅保留计数。
	}
	return time.Duration(seconds) * time.Second
}

// sweepLocked 清掉早已过期且不在锁定期的条目, 防止 map 被刷接口撑爆。
// 惰性触发: 每次判定前若距上次清理超过一个间隔就顺手清一遍。
func (g *loginGuard) sweepLocked(now time.Time) {
	if now.Sub(g.lastSweep) < loginSweepInterval {
		return
	}
	g.lastSweep = now
	for key, att := range g.attempts {
		if att.lockedUntil.IsZero() || att.lockedUntil.Before(now) {
			if att.lastFailure.Before(now.Add(-loginWindow)) {
				delete(g.attempts, key)
			}
		}
	}
}

// LoginAllowed 判定该主体当前是否允许尝试登录; 不允许时返回建议等待时长。
func LoginAllowed(identity string) (time.Duration, bool) {
	now := time.Now()
	LoginGuard.mu.Lock()
	defer LoginGuard.mu.Unlock()
	LoginGuard.sweepLocked(now)

	att, ok := LoginGuard.attempts[identity]
	if !ok {
		return 0, true
	}
	if !att.lockedUntil.IsZero() {
		if now.Before(att.lockedUntil) {
			return att.lockedUntil.Sub(now), false
		}
		// 锁定期结束: 保留失败计数, 下一次失败会按更高档位重新锁定。
		att.lockedUntil = time.Time{}
	}
	return 0, true
}

// LoginFailure 记录一次失败; 达到阈值时按连续轮数指数锁定。
func LoginFailure(identity string) {
	now := time.Now()
	base := loginLockoutBase()
	LoginGuard.mu.Lock()
	defer LoginGuard.mu.Unlock()
	LoginGuard.sweepLocked(now)

	att, ok := LoginGuard.attempts[identity]
	if !ok || att.lastFailure.Before(now.Add(-loginWindow)) {
		att = &loginAttempt{}
		LoginGuard.attempts[identity] = att
	}
	att.failures++
	att.lastFailure = now
	if base == 0 {
		return
	}
	max := loginMaxAttempts()
	if att.failures < max {
		return
	}
	// 第 N 轮超限锁定 base * 2^(N-max), 封顶 1 小时。
	rounds := att.failures - max + 1
	lock := base
	for i := 1; i < rounds; i++ {
		lock *= 2
		if lock >= loginMaxLockout {
			lock = loginMaxLockout
			break
		}
	}
	att.lockedUntil = now.Add(lock)
	log.Warnf("login throttled: identity %s locked for %s after %d failures", identity, lock, att.failures)
}

// LoginSuccess 登录成功后清零该主体的失败记录。
func LoginSuccess(identity string) {
	LoginGuard.mu.Lock()
	defer LoginGuard.mu.Unlock()
	delete(LoginGuard.attempts, identity)
}

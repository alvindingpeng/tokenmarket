package op

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/secretstore"
	"github.com/alvindingpeng/switchyard/internal/totp"
	"github.com/charmbracelet/log"
	"gorm.io/gorm"
)

const TOTPLoginWindow = 1

var ErrTOTPThrottled = errors.New("verification throttled, try again later")
var errTOTPCode = errors.New("invalid verification code")

// Serialize local cache publication; database CAS also protects independent processes.
var userTOTPMu sync.Mutex

func userTOTPFresh(id uint) (model.User, error) {
	var u model.User
	err := db.GetDB().First(&u, id).Error
	return u, err
}

// Every attempt (including successful requests) consumes a persistent account budget.
func userTOTPAttempt(id uint) error {
	now := time.Now().Unix()
	if err := db.GetDB().Model(&model.User{}).Where("id = ? AND totp_attempt_window <= ?", id, now-300).Updates(map[string]any{"totp_attempt_window": now, "totp_attempts": 0}).Error; err != nil {
		return err
	}
	r := db.GetDB().Model(&model.User{}).Where("id = ? AND totp_attempts < 10", id).UpdateColumn("totp_attempts", gorm.Expr("totp_attempts + 1"))
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected != 1 {
		return ErrTOTPThrottled
	}
	return nil
}

func userTOTPSave(u model.User, changes map[string]any) error {
	changes["totp_version"] = gorm.Expr("totp_version + 1")
	r := db.GetDB().Model(&model.User{}).Where("id = ? AND totp_version = ? AND token_version = ?", u.ID, u.TOTPVersion, u.TokenVersion).Updates(changes)
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected != 1 {
		return fmt.Errorf("two-factor state changed; retry")
	}
	fresh, err := userTOTPFresh(u.ID)
	if err != nil {
		return err
	}
	userCache.Set(int(u.ID), fresh)
	return nil
}

func UserTOTPSetup(userID uint) (string, string, error) {
	userTOTPMu.Lock()
	defer userTOTPMu.Unlock()
	if err := userTOTPAttempt(userID); err != nil {
		return "", "", err
	}
	u, err := userTOTPFresh(userID)
	if err != nil {
		return "", "", err
	}
	if u.TOTPEnabled {
		return "", "", fmt.Errorf("two-factor authentication is already enabled")
	}
	// TOTP must never use the legacy plaintext-compatible model codec.
	store := secretstore.Active()
	if store == nil {
		return "", "", fmt.Errorf("TOTP master key is not initialized")
	}
	secret, err := totp.GenerateSecret()
	if err != nil {
		return "", "", err
	}
	sealed, err := store.Seal(secretstore.UserTOTPBinding(userID), secret)
	if err != nil {
		return "", "", err
	}
	err = userTOTPSave(u, map[string]any{"totp_secret_encrypted": sealed, "totp_last_step": -1, "totp_recovery_hashes": ""})
	if err != nil {
		return "", "", err
	}
	return secret, totp.OtpauthURL(secret, "Switchyard", u.Username), nil
}

func recoveryHash(code string) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(code)))
	return hex.EncodeToString(h[:])
}

// userTOTPNewRecoveryCodes 生成 10 个一次性恢复码与对应哈希。
//
// 抽成函数是因为它现在有两个调用方(首次启用、自助重新生成)。两份实现迟早会在
// "生成几个""用什么熵""哈希怎么算"上走岔 —— 而恢复码是账号的最后一道门, 走岔一次就是灾难。
func userTOTPNewRecoveryCodes() ([]string, []string, error) {
	codes := make([]string, 10)
	hashes := make([]string, 10)
	for i := range codes {
		raw := make([]byte, 16)
		if _, err := rand.Read(raw); err != nil {
			return nil, nil, err
		}
		codes[i] = hex.EncodeToString(raw)
		hashes[i] = recoveryHash(codes[i])
	}
	return codes, hashes, nil
}
func userTOTPMatch(u model.User, code string, recovery bool) (map[string]any, error) {
	secret, err := userOpenTOTPSecret(u)
	if err != nil {
		return nil, errTOTPCode
	}
	if step, ok := totp.MatchStep(secret, code, TOTPLoginWindow, time.Now()); ok && step > u.TOTPLastStep {
		return map[string]any{"totp_last_step": step}, nil
	}
	if recovery {
		var hashes []string
		if json.Unmarshal([]byte(u.TOTPRecoveryHashes), &hashes) == nil {
			hash := recoveryHash(code)
			for i, h := range hashes {
				if h == hash {
					hashes = append(hashes[:i], hashes[i+1:]...)
					b, _ := json.Marshal(hashes)
					return map[string]any{"totp_recovery_hashes": string(b)}, nil
				}
			}
		}
	}
	return nil, errTOTPCode
}

func UserTOTPEnable(userID uint, code string) ([]string, error) {
	userTOTPMu.Lock()
	defer userTOTPMu.Unlock()
	if err := userTOTPAttempt(userID); err != nil {
		return nil, err
	}
	u, err := userTOTPFresh(userID)
	if err != nil {
		return nil, err
	}
	if u.TOTPEnabled {
		return nil, fmt.Errorf("two-factor authentication is already enabled")
	}
	changes, err := userTOTPMatch(u, code, false)
	if err != nil {
		return nil, err
	}
	codes, hashes, err := userTOTPNewRecoveryCodes()
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(hashes)
	changes["totp_enabled"] = true
	changes["totp_recovery_hashes"] = string(b)
	changes["token_version"] = gorm.Expr("token_version + 1")
	if err := userTOTPSave(u, changes); err != nil {
		return nil, err
	}
	// 重绑 2FA 意味着第二因子换了, 先前所有"免码"承诺一律作废。
	// 失败不回滚启用(启用已生效), 但要留日志 —— 残留的信任设备是真实的安全缺口。
	if _, err := TrustedDeviceRevokeAll(u.ID); err != nil {
		log.Warnf("revoke trusted devices after 2FA enable failed: user=%d err=%v", u.ID, err)
	}
	return codes, nil
}

func UserTOTPDisable(userID uint, code string) error {
	userTOTPMu.Lock()
	defer userTOTPMu.Unlock()
	if err := userTOTPAttempt(userID); err != nil {
		return err
	}
	u, err := userTOTPFresh(userID)
	if err != nil {
		return err
	}
	if !u.TOTPEnabled {
		return fmt.Errorf("two-factor authentication is not enabled")
	}
	if _, err := userTOTPMatch(u, code, true); err != nil {
		return err
	}
	if err := userTOTPSave(u, map[string]any{"totp_enabled": false, "totp_secret_encrypted": "", "totp_recovery_hashes": "", "totp_last_step": -1, "token_version": gorm.Expr("token_version + 1")}); err != nil {
		return err
	}
	// 2FA 都关了, "免第二因子"的凭证没有任何意义 —— 留着只会攒下一堆无人认领的凭据。
	if _, err := TrustedDeviceRevokeAll(u.ID); err != nil {
		log.Warnf("revoke trusted devices after 2FA disable failed: user=%d err=%v", u.ID, err)
	}
	return nil
}

// UserTOTPLogin 只验动态码。保留这个签名给不关心信任设备的调用方(测试、内部校验用)。
func UserTOTPLogin(account model.User, code string) (model.User, error) {
	u, _, err := UserTOTPLoginWithTrust(account, code, "")
	return u, err
}

// UserTOTPLoginWithTrust 校验第二因子, 支持"本机信任 30 天"免码。
//
// 重新读取权威状态后做 CAS: 并发重放(@totp_last_step)、恢复码被同时消费、
// 以及"改密/登出使会话失效"(token_version/password 变化)都会被这里拒掉。
//
// 顺序是刻意的: **先看显式动态码, 没有码才看信任设备**。
//   - 用户掏手机输了码 → 就按码验, 不碰信任凭据(不刷新 LastUsedAt, 也不把"用过的码"换信任);
//   - 一个码都没输 → 才拿信任凭据试一次; 命中即放行, 并返回 usedTrustDevice=true,
//     让调用方按服务端记录的到期时间给 Cookie 续期(时间是服务端权威, 前端不该自己算)。
//
// 空码且没有信任设备时仍然消耗一次失败预算(与原实现一致): 否则攻击者可以用"空码"无限探测,
// 而限流恰好是这条路上唯一能拖慢在线爆破的东西。
func UserTOTPLoginWithTrust(account model.User, code, trustedToken string) (model.User, bool, error) {
	userTOTPMu.Lock()
	defer userTOTPMu.Unlock()
	u, err := userTOTPFresh(account.ID)
	if err != nil {
		return u, false, err
	}
	if u.TokenVersion != account.TokenVersion || u.Password != account.Password || u.Status != model.StatusActive {
		return u, false, errTOTPCode
	}
	if !u.TOTPEnabled {
		return u, false, nil
	}
	trimmed := strings.TrimSpace(code)
	if trimmed == "" {
		if device, ok := TrustedDeviceVerify(u.ID, trustedToken); ok && device.UserID == u.ID {
			return u, true, nil
		}
	}
	if err := userTOTPAttempt(u.ID); err != nil {
		return u, false, err
	}
	if trimmed == "" {
		return u, false, errors.New("two_factor_required")
	}
	changes, err := userTOTPMatch(u, trimmed, true)
	if err != nil {
		return u, false, err
	}
	if err := userTOTPSave(u, changes); err != nil {
		return u, false, errTOTPCode
	}
	return u, false, nil
}

// UserTOTPRegenerateRecovery 自助重新生成恢复码, 返回新的 10 个明文码(只此一次)。
//
// 为什么需要它: 恢复码此前只能在启用 2FA 那一刻拿到一次, 用光后只能"解绑再重绑" ——
// 而解绑要输动态码, 恰恰是丢了设备的人做不到的事。这条路把最后一道门堵成了死锁。
//
// 门槛刻意与解绑同档: 登录态 + step-up(重输密码, 在 handler 层) + **当前动态码或恢复码**。
// 允许用恢复码换新恢复码是必须的 —— 否则"最后一张恢复码"用掉之后又回到死锁。
func UserTOTPRegenerateRecovery(userID uint, code string) ([]string, error) {
	userTOTPMu.Lock()
	defer userTOTPMu.Unlock()
	if err := userTOTPAttempt(userID); err != nil {
		return nil, err
	}
	u, err := userTOTPFresh(userID)
	if err != nil {
		return nil, err
	}
	if !u.TOTPEnabled {
		return nil, fmt.Errorf("two-factor authentication is not enabled")
	}
	trimmed := strings.TrimSpace(code)
	if trimmed == "" {
		return nil, fmt.Errorf("verification code is required")
	}
	changes, err := userTOTPMatch(u, trimmed, true)
	if err != nil {
		return nil, err
	}
	codes, hashes, err := userTOTPNewRecoveryCodes()
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(hashes)
	// 覆盖而不是追加: 旧码必须整体失效, 否则"重新生成"只是攒了更多能进门的钥匙。
	changes["totp_recovery_hashes"] = string(b)
	if err := userTOTPSave(u, changes); err != nil {
		return nil, err
	}
	return codes, nil
}

func UserTOTPVerify(user model.User, code string) bool {
	_, err := UserTOTPLogin(user, code)
	return err == nil
}
func userOpenTOTPSecret(user model.User) (string, error) {
	store := secretstore.Active()
	if store == nil || !secretstore.IsEnvelope(user.TOTPSecretEncrypted) {
		return "", errTOTPCode
	}
	return store.Open(secretstore.UserTOTPBinding(user.ID), user.TOTPSecretEncrypted)
}

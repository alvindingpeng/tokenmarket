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
	codes := make([]string, 10)
	hashes := make([]string, 10)
	for i := range codes {
		raw := make([]byte, 16)
		if _, err := rand.Read(raw); err != nil {
			return nil, err
		}
		codes[i] = hex.EncodeToString(raw)
		hashes[i] = recoveryHash(codes[i])
	}
	b, _ := json.Marshal(hashes)
	changes["totp_enabled"] = true
	changes["totp_recovery_hashes"] = string(b)
	changes["token_version"] = gorm.Expr("token_version + 1")
	if err := userTOTPSave(u, changes); err != nil {
		return nil, err
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
	return userTOTPSave(u, map[string]any{"totp_enabled": false, "totp_secret_encrypted": "", "totp_recovery_hashes": "", "totp_last_step": -1, "token_version": gorm.Expr("token_version + 1")})
}

// UserTOTPLogin re-reads authoritative state after password verification. CAS prevents
// simultaneous replay/recovery consumption and rejects concurrent session revocation.
func UserTOTPLogin(account model.User, code string) (model.User, error) {
	userTOTPMu.Lock()
	defer userTOTPMu.Unlock()
	u, err := userTOTPFresh(account.ID)
	if err != nil {
		return u, err
	}
	if u.TokenVersion != account.TokenVersion || u.Password != account.Password || u.Status != model.StatusActive {
		return u, errTOTPCode
	}
	if !u.TOTPEnabled {
		return u, nil
	}
	if err := userTOTPAttempt(u.ID); err != nil {
		return u, err
	}
	if strings.TrimSpace(code) == "" {
		return u, errors.New("two_factor_required")
	}
	changes, err := userTOTPMatch(u, code, true)
	if err != nil {
		return u, err
	}
	if err := userTOTPSave(u, changes); err != nil {
		return u, errTOTPCode
	}
	return u, nil
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

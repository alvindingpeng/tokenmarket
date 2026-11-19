package op

import (
	"context"
	"errors"
	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/secretstore"
	"github.com/alvindingpeng/switchyard/internal/totp"
	"strings"
	"sync"
	"testing"
	"time"
)

func totpEnv(t *testing.T) model.User {
	t.Helper()
	userDeleteTestSetup(t)
	secretstore.Reset()
	t.Cleanup(secretstore.Reset)
	if err := secretstore.Init(secretstore.KEKConfig{Inline: strings.Repeat("13", 32)}); err != nil {
		t.Fatal(err)
	}
	return userDeleteTestCreateUser(t, "totp-user")
}
func TestUserTOTPLifecycleRecoveryReplay(t *testing.T) {
	u := totpEnv(t)
	if _, err := UserTOTPEnable(u.ID, "123456"); err == nil {
		t.Fatal("enabled without setup")
	}
	secret, uri, err := UserTOTPSetup(u.ID)
	if err != nil || !strings.HasPrefix(uri, "otpauth://") {
		t.Fatalf("setup: %v", err)
	}
	pending, _ := userTOTPFresh(u.ID)
	if pending.TOTPEnabled || !secretstore.IsEnvelope(pending.TOTPSecretEncrypted) || strings.Contains(pending.TOTPSecretEncrypted, secret) {
		t.Fatal("invalid pending state")
	}
	code, _ := totp.Generate(secret, time.Now().Unix())
	codes, err := UserTOTPEnable(u.ID, code)
	if err != nil || len(codes) != 10 {
		t.Fatalf("enable: %v", err)
	}
	enabled, _ := userTOTPFresh(u.ID)
	if !enabled.TOTPEnabled || enabled.TokenVersion != u.TokenVersion+1 || strings.Contains(enabled.TOTPRecoveryHashes, codes[0]) {
		t.Fatal("invalid enabled state")
	}
	if _, err := UserTOTPLogin(u, codes[0]); err == nil {
		t.Fatal("stale password snapshot accepted")
	}
	if _, err := UserTOTPLogin(enabled, ""); err == nil || err.Error() != "two_factor_required" {
		t.Fatalf("missing: %v", err)
	}
	if _, err := UserTOTPLogin(enabled, code); err == nil {
		t.Fatal("enable code replay")
	}
	// Clear cache/reload to prove consumed counters are database state.
	userCache.Clear()
	if err := userRefreshCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	enabled, _ = userTOTPFresh(u.ID)
	if _, err := UserTOTPLogin(enabled, codes[0]); err != nil {
		t.Fatalf("recovery: %v", err)
	}
	if _, err := UserTOTPLogin(enabled, codes[0]); err == nil {
		t.Fatal("recovery replay")
	}
	if err := UserTOTPDisable(u.ID, codes[1]); err != nil {
		t.Fatal(err)
	}
	disabled, _ := userTOTPFresh(u.ID)
	if disabled.TOTPEnabled || disabled.TOTPSecretEncrypted != "" || disabled.TOTPRecoveryHashes != "" || disabled.TokenVersion != enabled.TokenVersion+1 {
		t.Fatal("disable did not clear/revoke")
	}
}
func TestUserTOTPFailClosedAndRateLimit(t *testing.T) {
	u := totpEnv(t)
	secretstore.Reset()
	if _, _, err := UserTOTPSetup(u.ID); err == nil {
		t.Fatal("setup without KEK")
	}
	fresh, _ := userTOTPFresh(u.ID)
	if fresh.TOTPSecretEncrypted != "" {
		t.Fatal("plaintext persisted")
	}
	for i := 0; i < 9; i++ {
		_, _ = UserTOTPEnable(u.ID, "000000")
	}
	if _, err := UserTOTPEnable(u.ID, "000000"); !errors.Is(err, ErrTOTPThrottled) {
		t.Fatalf("budget: %v", err)
	}
	if err := db.GetDB().Model(&model.User{}).Where("id = ?", u.ID).Update("totp_attempt_window", time.Now().Unix()-301).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := UserTOTPEnable(u.ID, "000000"); errors.Is(err, ErrTOTPThrottled) {
		t.Fatal("window did not expire")
	}
}
func TestUserTOTPConcurrentEnableAndConsumption(t *testing.T) {
	u := totpEnv(t)
	secret, _, err := UserTOTPSetup(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := totp.Generate(secret, time.Now().Unix())
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	var codes []string
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := UserTOTPEnable(u.ID, code)
			if err == nil {
				mu.Lock()
				successes++
				codes = got
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("enable successes: %d", successes)
	}
	fresh, _ := userTOTPFresh(u.ID)
	successes = 0
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := UserTOTPLogin(fresh, codes[0]); err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("recovery successes: %d", successes)
	}
	if _, _, err := UserTOTPSetup(u.ID); err == nil {
		t.Fatal("setup overwrote enabled secret")
	}
}

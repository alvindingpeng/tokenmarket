package op

import (
	"github.com/alvindingpeng/switchyard/internal/totp"
	"testing"
	"time"
)

func TestUserTOTPCASRejectsStaleSetupAndReplay(t *testing.T) {
	u := totpEnv(t)
	secret, _, err := UserTOTPSetup(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	stale, _ := userTOTPFresh(u.ID)
	code, _ := totp.Generate(secret, time.Now().Unix()-30)
	if _, err := UserTOTPEnable(u.ID, code); err != nil {
		t.Fatal(err)
	}
	if err := userTOTPSave(stale, map[string]any{"totp_secret_encrypted": "stale"}); err == nil {
		t.Fatal("stale setup overwrote enable")
	}
	fresh, _ := userTOTPFresh(u.ID)
	current, _ := totp.Generate(secret, time.Now().Unix())
	if _, err := UserTOTPLogin(fresh, current); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := userTOTPFresh(u.ID)
	if _, err := UserTOTPLogin(snapshot, current); err == nil {
		t.Fatal("persisted counter replay accepted")
	}
	if snapshot.TOTPLastStep <= stale.TOTPLastStep {
		t.Fatal("counter not advanced")
	}
}

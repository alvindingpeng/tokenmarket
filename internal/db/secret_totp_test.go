package db

import (
	"github.com/alvindingpeng/switchyard/internal/secretstore"
	"testing"
)

func TestValidateUserTOTPSecrets(t *testing.T) {
	setupSecretDB(t, testKEK(1))
	store, err := secretstore.Require()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := store.Seal(secretstore.UserTOTPBinding(1), "JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO users(id,username,password,totp_enabled,totp_secret_encrypted) VALUES(1,'totp-check','unused',1,?)", sealed).Error; err != nil {
		t.Fatal(err)
	}
	if err := validateUserTOTPSecrets(db, store); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "JBSWY3DPEHPK3PXP", "sysec$invalid"} {
		if err := db.Exec("UPDATE users SET totp_secret_encrypted=? WHERE id=1", bad).Error; err != nil {
			t.Fatal(err)
		}
		if err := validateUserTOTPSecrets(db, store); err == nil {
			t.Fatalf("accepted invalid secret %q", bad)
		}
	}
	swapped, err := store.Seal(secretstore.UserTOTPBinding(2), "JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE users SET totp_secret_encrypted=? WHERE id=1", swapped).Error; err != nil {
		t.Fatal(err)
	}
	if err := validateUserTOTPSecrets(db, store); err == nil {
		t.Fatal("accepted another user's ciphertext")
	}
}

package auth

import (
	"github.com/alvindingpeng/switchyard/internal/conf"
	"github.com/golang-jwt/jwt/v5"
	"testing"
	"time"
)

func TestStepUpStrictJWTConstraints(t *testing.T) {
	oldSecret := secret
	secret = []byte("strict-step-up-secret")
	defer func() { secret = oldSecret }()
	user := stepupTestUser()
	for _, name := range []string{"wrong-algorithm", "wrong-issuer", "missing-expiration", "future-issued", "expired"} {
		t.Run(name, func(t *testing.T) {
			claims := StepUpClaims{UserID: user.ID, TokenVersion: user.TokenVersion, Session: SessionFingerprint("session"), Purpose: StepUpPurpose, RegisteredClaims: jwt.RegisteredClaims{Issuer: conf.APP_NAME, IssuedAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)), ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute))}}
			var method jwt.SigningMethod = jwt.SigningMethodHS256
			switch name {
			case "wrong-algorithm":
				method = jwt.SigningMethodHS384
			case "wrong-issuer":
				claims.Issuer = "unexpected-issuer"
			case "missing-expiration":
				claims.ExpiresAt = nil
			case "future-issued":
				claims.IssuedAt = jwt.NewNumericDate(time.Now().Add(time.Hour))
			case "expired":
				claims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Second))
			}
			token, err := jwt.NewWithClaims(method, claims).SignedString(secret)
			if err != nil {
				t.Fatal(err)
			}
			if err := VerifyStepUpToken(token, "session", user); err == nil {
				t.Fatal("invalid credential accepted")
			}
		})
	}
}

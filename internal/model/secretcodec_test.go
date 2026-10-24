package model

import (
	"github.com/alvindingpeng/switchyard/internal/secretstore"
	"testing"
)

func TestOpenSecretEnvelopeWithoutKeyFailsClosed(t *testing.T) {
	secretstore.Reset()
	binding := secretstore.SettingBinding("auth_secret")
	if value, err := OpenSecretValue(binding, "sysec$1$anything"); err == nil || value != "" {
		t.Fatal("envelope must not escape as plaintext without master key")
	}
	if value, err := OpenSecretValue(binding, "legacy"); err != nil || value != "legacy" {
		t.Fatal("legacy plaintext read compatibility changed")
	}
}

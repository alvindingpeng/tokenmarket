package secretstore

import (
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadKEKFromInlineHexAndBase64(t *testing.T) {
	want := testKEK("01")
	hexMaterial := hex.EncodeToString(want)
	b64Material := base64.StdEncoding.EncodeToString(want)

	for name, material := range map[string]string{"hex": hexMaterial, "base64": b64Material} {
		key, err := LoadKEK(KEKConfig{Inline: material})
		if err != nil {
			t.Fatalf("LoadKEK(%s): %v", name, err)
		}
		for i := range want {
			if key.kek[i] != want[i] {
				t.Fatalf("LoadKEK(%s) decoded wrong key material", name)
			}
		}
	}
}

func TestLoadKEKFromFile(t *testing.T) {
	want := testKEK("02")
	path := filepath.Join(t.TempDir(), "kek")
	if err := os.WriteFile(path, []byte("  "+base64.StdEncoding.EncodeToString(want)+"\n"), 0o600); err != nil {
		t.Fatalf("write kek file: %v", err)
	}
	key, err := LoadKEK(KEKConfig{File: path})
	if err != nil {
		t.Fatalf("LoadKEK(file): %v", err)
	}
	for i := range want {
		if key.kek[i] != want[i] {
			t.Fatal("LoadKEK(file) decoded wrong key material")
		}
	}
}

func TestLoadKEKFailsClosed(t *testing.T) {
	emptyFile := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(emptyFile, []byte("   \n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	badLength := filepath.Join(t.TempDir(), "short")
	if err := os.WriteFile(badLength, []byte(base64.StdEncoding.EncodeToString([]byte("short"))), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cases := map[string]KEKConfig{
		"missing everywhere":  {},
		"empty inline":        {Inline: "   "},
		"missing file":        {File: filepath.Join(t.TempDir(), "nope")},
		"empty file":          {File: emptyFile},
		"wrong key length":    {Inline: base64.StdEncoding.EncodeToString([]byte("short-key-material"))},
		"garbage material":    {Inline: "not hex or base64 !!!"},
		"all-zero key":        {Inline: hex.EncodeToString(make([]byte, kekLen))},
		"bad explicit key ID": {Inline: hex.EncodeToString(testKEK("01")), KeyID: "bad$id"},
		"oversized key ID":    {Inline: hex.EncodeToString(testKEK("01")), KeyID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
	}
	for name, cfg := range cases {
		if _, err := LoadKEK(cfg); err == nil {
			t.Fatalf("LoadKEK(%s) succeeded, want fail-closed error", name)
		}
	}
}

func TestKeyIDIsDerivedAndStable(t *testing.T) {
	first, err := New(testKEK("01"), "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	second, err := New(testKEK("01"), "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	other, err := New(testKEK("02"), "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if first.KeyID() != second.KeyID() {
		t.Fatalf("derived key ID not stable: %q vs %q", first.KeyID(), second.KeyID())
	}
	if first.KeyID() == other.KeyID() {
		t.Fatal("different master keys derived the same key ID")
	}

	explicit, err := New(testKEK("01"), "prod-2026q1")
	if err != nil {
		t.Fatalf("New with explicit key ID: %v", err)
	}
	if explicit.KeyID() != "prod-2026q1" {
		t.Fatalf("explicit key ID not honored: %q", explicit.KeyID())
	}
}

func TestGlobalInitFailsClosedAndReset(t *testing.T) {
	Reset()
	t.Cleanup(Reset)

	if Active() != nil {
		t.Fatal("Active() before init should be nil")
	}
	if err := Init(KEKConfig{}); err == nil {
		t.Fatal("Init without key material succeeded, want fail-closed error")
	}
	if Active() != nil {
		t.Fatal("failed Init must leave the global key uninitialized")
	}
	if err := Init(KEKConfig{Inline: hex.EncodeToString(testKEK("01"))}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if Active() == nil {
		t.Fatal("Active() after Init should return the master key")
	}
	if _, err := Require(); err != nil {
		t.Fatalf("Require: %v", err)
	}
	Reset()
	if Active() != nil {
		t.Fatal("Reset must clear the global key")
	}
	if _, err := Require(); err == nil {
		t.Fatal("Require after Reset should fail")
	}
}

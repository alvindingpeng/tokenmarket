package op

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupEncryptRoundtrip(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "backup.db")
	enc := filepath.Join(dir, "backup.db.enc")
	dec := filepath.Join(dir, "restored.db")
	payload := bytes.Repeat([]byte("octopus backup payload\x00\xff"), 5000)
	if err := os.WriteFile(src, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EncryptBackupFile(src, enc, "correct horse battery"); err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	// 明文与密文不同, 密文不含 magic 明文头之外的可识别内容。
	encData, err := os.ReadFile(enc)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encData, payload[:32]) {
		t.Fatal("ciphertext contains plaintext")
	}
	if err := DecryptBackupFile(enc, dec, "correct horse battery"); err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	got, err := os.ReadFile(dec)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("roundtrip mismatch")
	}
}

func TestBackupDecryptWrongPassphrase(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "backup.db")
	enc := filepath.Join(dir, "backup.db.enc")
	dec := filepath.Join(dir, "restored.db")
	if err := os.WriteFile(src, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EncryptBackupFile(src, enc, "right-passphrase"); err != nil {
		t.Fatal(err)
	}
	if err := DecryptBackupFile(enc, dec, "wrong-passphrase"); err == nil {
		t.Fatal("expected error for wrong passphrase")
	}
	if _, err := os.Stat(dec); !os.IsNotExist(err) {
		t.Fatal("plaintext output must not exist after failed decrypt")
	}
}

func TestBackupDecryptTampered(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "backup.db")
	enc := filepath.Join(dir, "backup.db.enc")
	dec := filepath.Join(dir, "restored.db")
	if err := os.WriteFile(src, bytes.Repeat([]byte("x"), 1000), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EncryptBackupFile(src, enc, "pass"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(enc)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)/2] ^= 0xff
	if err := os.WriteFile(enc, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := DecryptBackupFile(enc, dec, "pass"); err == nil {
		t.Fatal("expected mac mismatch error")
	}
}

func TestBackupChecksumRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "backup.db")
	if err := os.WriteFile(path, []byte("checksum me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteBackupChecksum(path); err != nil {
		t.Fatal(err)
	}
	if !VerifyBackupChecksum(path) {
		t.Fatal("checksum should verify")
	}
	if err := os.WriteFile(path, []byte("tampered!"), 0o600); err != nil {
		t.Fatal(err)
	}
	if VerifyBackupChecksum(path) {
		t.Fatal("checksum must fail after tamper")
	}
	if VerifyBackupChecksum(filepath.Join(dir, "missing.db")) {
		t.Fatal("missing checksum must fail")
	}
}

func TestBackupCryptoRejectsEmptyPassphrase(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.db")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EncryptBackupFile(src, filepath.Join(dir, "a.db.enc"), "  "); err == nil {
		t.Fatal("empty passphrase must be rejected")
	}
}

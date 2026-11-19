package secretstore

import (
	"encoding/base64"
	"strings"
	"testing"
)

func testMasterKey(t *testing.T) *MasterKey {
	t.Helper()
	key, err := New(testKEK("01"), "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return key
}

// testKEK 由标签派生确定性测试密钥, 不同标签得到不同密钥。
func testKEK(tag string) []byte {
	kek := make([]byte, kekLen)
	for i := range kek {
		kek[i] = byte(i) ^ tag[len(tag)-1]
	}
	return kek
}

func TestSealOpenRoundtrip(t *testing.T) {
	key := testMasterKey(t)
	binding := SettingBinding("epay_key")

	envelope, err := key.Seal(binding, "sk-live-secret")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if !IsEnvelope(envelope) {
		t.Fatalf("sealed value is not recognized as envelope: %q", envelope)
	}
	// 版本与 keyID 必须出现在信封明文头里。
	parts := strings.Split(envelope, "$")
	if parts[1] != envelopeVersion {
		t.Fatalf("envelope version = %q, want %q", parts[1], envelopeVersion)
	}
	if parts[2] != key.KeyID() {
		t.Fatalf("envelope key ID = %q, want %q", parts[2], key.KeyID())
	}

	plaintext, err := key.Open(binding, envelope)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if plaintext != "sk-live-secret" {
		t.Fatalf("Open = %q, want %q", plaintext, "sk-live-secret")
	}
}

func TestSealIsRandomizedPerRow(t *testing.T) {
	key := testMasterKey(t)
	binding := SettingBinding("epay_key")
	first, err := key.Seal(binding, "same")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	second, err := key.Seal(binding, "same")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	// 每条秘密独立随机 DEK 与随机数: 同一明文两次加密不得产出相同密文。
	if first == second {
		t.Fatalf("two seals of the same plaintext produced identical envelopes")
	}
}

func TestOpenRejectsBindingMismatch(t *testing.T) {
	key := testMasterKey(t)
	envelope, err := key.Seal(SettingBinding("epay_key"), "sk-live-secret")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	cases := []Binding{
		SettingBinding("stripe_secret_key"),                                                 // 换设置键。
		SettingBinding("epay_key").withColumn("other"),                                      // 换列。
		ChannelKeyBinding(7, "default"),                                                     // 换表。
		{Table: "settings", Owner: "key:epay_key" + string(rune(0)) + "x", Column: "value"}, // 归属内容差异。
	}
	for _, wrong := range cases {
		if _, err := key.Open(wrong, envelope); err == nil {
			t.Fatalf("Open with binding %+v succeeded, want ownership mismatch error", wrong)
		}
	}
}

func (b Binding) withColumn(column string) Binding {
	b.Column = column
	return b
}

func TestOpenRejectsTamperedEnvelope(t *testing.T) {
	key := testMasterKey(t)
	binding := ChannelKeyBinding(7, "default")
	envelope, err := key.Seal(binding, "sk-live-secret")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	parts := strings.Split(envelope, "$")

	tamper := func(mutate func(parts []string)) string {
		copied := append([]string(nil), parts...)
		mutate(copied)
		return strings.Join(copied, "$")
	}

	cases := map[string]func(parts []string){
		"keyID":           func(p []string) { p[2] = p[2] + "x" },
		"binding":         func(p []string) { p[3] = base64.RawURLEncoding.EncodeToString([]byte("settings9:key:evilvalue")) },
		"wrappedDEK":      func(p []string) { p[4] = flipBase64(p[4]) },
		"shortWrapNonce":  func(p []string) { p[5] = base64.RawURLEncoding.EncodeToString([]byte{1}) },
		"emptyWrapNonce":  func(p []string) { p[5] = "" },
		"longWrapNonce":   func(p []string) { p[5] = base64.RawURLEncoding.EncodeToString(make([]byte, 13)) },
		"wrapNonce":       func(p []string) { p[5] = flipBase64(p[5]) },
		"shortValueNonce": func(p []string) { p[6] = base64.RawURLEncoding.EncodeToString([]byte{1}) },
		"emptyValueNonce": func(p []string) { p[6] = "" },
		"longValueNonce":  func(p []string) { p[6] = base64.RawURLEncoding.EncodeToString(make([]byte, 13)) },
		"valueNonce":      func(p []string) { p[6] = flipBase64(p[6]) },
		"ciphertext":      func(p []string) { p[7] = flipBase64(p[7]) },
	}
	for name, mutate := range cases {
		if _, err := key.Open(binding, tamper(mutate)); err == nil {
			t.Fatalf("Open with tampered %s succeeded, want failure", name)
		}
	}
}

func flipBase64(value string) string {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return value + "x"
	}
	raw[len(raw)-1] ^= 0xff
	return base64.RawURLEncoding.EncodeToString(raw)
}

func TestOpenRejectsWrongMasterKey(t *testing.T) {
	right := testMasterKey(t)
	wrong, err := New(testKEK("02"), right.KeyID())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	binding := SettingBinding("epay_key")
	envelope, err := right.Seal(binding, "sk-live-secret")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := wrong.Open(binding, envelope); err == nil {
		t.Fatal("Open with wrong master key succeeded, want failure")
	}
}

func TestOpenRejectsUnknownKeyID(t *testing.T) {
	oldKey := testMasterKey(t)
	newKey, err := New(testKEK("03"), "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	binding := SettingBinding("epay_key")
	envelope, err := oldKey.Seal(binding, "sk-live-secret")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	// 换钥后旧信封必须拒绝, 而不是解出错误明文: 轮换要靠显式迁移, 不能靠静默降级。
	if _, err := newKey.Open(binding, envelope); err == nil {
		t.Fatal("Open with rotated key ID succeeded, want failure")
	}
}

func TestOpenRejectsMalformedEnvelope(t *testing.T) {
	key := testMasterKey(t)
	binding := SettingBinding("epay_key")
	cases := []string{
		"",
		"plaintext-secret",
		"sysec$",
		"sysec$2$kid$YQ$YQ$YQ$YQ$YQ",
		"sysec$1$kid$YQ$YQ$YQ$YQ",
		"sysec$1$kid$!!!$YQ$YQ$YQ$YQ",
	}
	for _, envelope := range cases {
		if _, err := key.Open(binding, envelope); err == nil {
			t.Fatalf("Open(%q) succeeded, want failure", envelope)
		}
	}
}

func TestCanonicalBindingIsUnambiguous(t *testing.T) {
	// 长度前缀: 归属里带分隔符的伪造内容不能拼出另一个绑定的 canonical 串。
	forged := Binding{Table: "settings", Owner: "key:" + "a	:valuesettings", Column: "value"}
	other := Binding{Table: "settings", Owner: "key:a", Column: "value"}
	if forged.Canonical() == other.Canonical() {
		t.Fatal("forged binding collides with plain binding")
	}
}

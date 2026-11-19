package totp

import (
	"strings"
	"testing"
	"time"
)

// RFC 6238 Appendix B 的 SHA1 测试向量。密钥为 ASCII "12345678901234567890",
// 其 base32 表示即 GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ。
// 官方给出的是 8 位码; 本实现为 6 位(认证器通行值), 故取其 mod 10^6 作为期望值。
const rfcSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

func TestRFC6238Vectors(t *testing.T) {
	cases := []struct {
		unix int64
		want string // 6 位
	}{
		{59, "287082"},          // 8 位 94287082
		{1111111109, "081804"},  // 8 位 07081804
		{1111111111, "050471"},  // 8 位 14050471
		{1234567890, "005924"},  // 8 位 89005924
		{2000000000, "279037"},  // 8 位 69279037
		{20000000000, "353130"}, // 8 位 65353130
	}
	for _, c := range cases {
		got, err := Generate(rfcSecret, c.unix)
		if err != nil {
			t.Fatalf("Generate(%d): %v", c.unix, err)
		}
		if got != c.want {
			t.Errorf("Generate(%d) = %q, want %q", c.unix, got, c.want)
		}
	}
}

func TestGenerateSecretRoundTrip(t *testing.T) {
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatalf("GenerateSecret: %v", err)
	}
	if len(secret) < 32 {
		t.Fatalf("secret too short: %q", secret)
	}
	if secret != strings.ToUpper(secret) {
		t.Errorf("secret must be uppercase: %q", secret)
	}
	// 生成的密钥必须能算出可校验的码。
	now := time.Unix(1234567890, 0)
	code, err := Generate(secret, now.Unix())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !Validate(secret, code, 1, now) {
		t.Errorf("Validate should accept freshly generated code %q", code)
	}
}

func TestValidateRejectsWrongCode(t *testing.T) {
	now := time.Unix(1234567890, 0)
	if Validate(rfcSecret, "000000", 1, now) {
		t.Error("wrong code must not validate")
	}
	if Validate(rfcSecret, "", 1, now) {
		t.Error("empty code must not validate")
	}
	if Validate(rfcSecret, "abc123", 1, now) {
		t.Error("non-digit code must not validate")
	}
	if Validate(rfcSecret, "12345", 1, now) {
		t.Error("wrong length must not validate")
	}
}

// TestValidateWindowTolerance 允许前后一个步长(时钟漂移), 但不允许跨多步。
func TestValidateWindowTolerance(t *testing.T) {
	base := time.Unix(1234567890, 0)
	code, err := Generate(rfcSecret, base.Unix())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// 客户端时钟快 30 秒(一个步长): window=1 应通过。
	clientFast := base.Add(30 * time.Second)
	if !Validate(rfcSecret, code, 1, clientFast) {
		t.Error("one-step drift should be tolerated with window=1")
	}
	// 快 90 秒(三步): window=1 应拒绝。
	clientMuchFast := base.Add(90 * time.Second)
	if Validate(rfcSecret, code, 1, clientMuchFast) {
		t.Error("three-step drift must not validate with window=1")
	}
}

func TestOtpauthURL(t *testing.T) {
	u := OtpauthURL(rfcSecret, "Switchyard", "alice@example.com")
	if !strings.HasPrefix(u, "otpauth://totp/") {
		t.Fatalf("bad prefix: %q", u)
	}
	if !strings.Contains(u, "secret="+rfcSecret) {
		t.Errorf("missing secret: %q", u)
	}
	if !strings.Contains(u, "issuer=Switchyard") {
		t.Errorf("missing issuer: %q", u)
	}
	if !strings.Contains(u, "digits=6") || !strings.Contains(u, "period=30") {
		t.Errorf("missing params: %q", u)
	}
}

// TestDecodeSecretToleratesFormatting 模拟认证器里带空格/连字符分组的密钥展示。
func TestDecodeSecretToleratesFormatting(t *testing.T) {
	grouped := "gezd gnbv gy3t qojq gezd gnbv gy3t qojq"
	now := time.Unix(1234567890, 0)
	code, err := Generate(grouped, now.Unix())
	if err != nil {
		t.Fatalf("Generate with grouped secret: %v", err)
	}
	code2, _ := Generate(rfcSecret, now.Unix())
	if code != code2 {
		t.Errorf("grouped secret produced %q, want %q", code, code2)
	}
}

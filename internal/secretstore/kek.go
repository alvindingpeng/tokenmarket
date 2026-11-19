package secretstore

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"strings"
)

const (
	// kekLen 是 KEK 长度: AES-256。
	kekLen = 32
	// keyIDMaxLen 限制信封里的 keyID 长度; keyID 不是秘密, 但要防止塞进分隔符破坏信封结构。
	keyIDMaxLen = 32
	// keyIDDigestDomain 参与派生 keyID, 与其它用途的摘要隔离。
	keyIDDigestDomain = "switchyard:kek-id:v1"
)

var keyIDPattern = regexp.MustCompile("^[A-Za-z0-9_.-]+$")

// KEKConfig 是主密钥(KEK)的外部来源。三者都不提供任何默认值:
// 密钥材料缺失或非法时 LoadKEK 返回错误, 由调用方拒绝启动(fail closed)。
type KEKConfig struct {
	// Inline 是环境变量直接给出的密钥材料(hex 或 base64), 优先于 File。
	Inline string
	// File 是密钥文件路径, 文件内容为 hex 或 base64(允许首尾空白与换行)。
	File string
	// KeyID 可选: 显式指定信封里的密钥 ID。留空时按密钥材料派生(跨实例共享同一 KEK 时自动一致)。
	KeyID string
}

// LoadKEK 从外部来源加载主密钥。无来源、材料长度不对、全零密钥、非法 keyID 一律报错,
// 绝不回退到任何内置或零值密钥。
func LoadKEK(cfg KEKConfig) (*MasterKey, error) {
	material := strings.TrimSpace(cfg.Inline)
	source := "inline"
	if material == "" {
		if strings.TrimSpace(cfg.File) == "" {
			return nil, fmt.Errorf("secret master key is not configured: set SWITCHYARD_SECRET_KEK or SWITCHYARD_KEK_FILE (config: secret.kek_file)")
		}
		raw, err := os.ReadFile(cfg.File)
		if err != nil {
			return nil, fmt.Errorf("read secret master key file: %w", err)
		}
		material = strings.TrimSpace(string(raw))
		source = "file " + cfg.File
	}
	if material == "" {
		return nil, fmt.Errorf("secret master key source %s is empty", source)
	}
	kek, err := decodeKEKMaterial(material)
	if err != nil {
		return nil, fmt.Errorf("secret master key from %s: %w", source, err)
	}
	return New(kek, cfg.KeyID)
}

// New 校验密钥材料与 keyID 并构造 MasterKey。kek 必须是 32 字节且不能全零。
func New(kek []byte, keyID string) (*MasterKey, error) {
	if len(kek) != kekLen {
		return nil, fmt.Errorf("secret master key must be %d bytes, got %d", kekLen, len(kek))
	}
	allZero := true
	for _, b := range kek {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		// 全零密钥等于没有密钥: 明确拒绝, 免得「配了但配错」被当成已保护。
		return nil, fmt.Errorf("secret master key must not be all zeros")
	}
	if keyID == "" {
		keyID = deriveKeyID(kek)
	}
	if !keyIDPattern.MatchString(keyID) || len(keyID) > keyIDMaxLen {
		return nil, fmt.Errorf("secret key ID %q is invalid: use up to %d chars of [A-Za-z0-9_.-]", keyID, keyIDMaxLen)
	}
	stored := make([]byte, len(kek))
	copy(stored, kek)
	return &MasterKey{kek: stored, keyID: keyID}, nil
}

// decodeKEKMaterial 解析 hex 或 base64(标准/URL, 有无 padding)编码的 32 字节密钥。
func decodeKEKMaterial(material string) ([]byte, error) {
	if len(material) == hex.EncodedLen(kekLen) {
		if decoded, err := hex.DecodeString(material); err == nil {
			return decoded, nil
		}
	}
	encodings := []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	}
	for _, encoding := range encodings {
		if decoded, err := encoding.DecodeString(material); err == nil && len(decoded) == kekLen {
			return decoded, nil
		}
	}
	return nil, fmt.Errorf("key material must be 32 bytes encoded as hex or base64")
}

// deriveKeyID 由密钥材料单向派生短 ID: 同一 KEK 在任何实例上得到同一 ID, 不同 KEK 必然不同。
// 派生值是高熵密钥的带域分隔摘要, 不泄露可用的密钥信息。
func deriveKeyID(kek []byte) string {
	digest := sha256.Sum256(append([]byte(keyIDDigestDomain), kek...))
	return hex.EncodeToString(digest[:8])
}

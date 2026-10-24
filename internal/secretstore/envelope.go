// Package secretstore 实现静态凭据的信封加密(envelope encryption):
// 每条秘密用随机生成的 DEK(数据加密密钥)做 AES-256-GCM 加密, DEK 再由外部注入的
// KEK(主加密密钥)包裹后随密文一起存库。主密钥只来自外部文件或环境变量, 本包不提供
// 任何默认值或零值回退 —— 主密钥缺失或非法时调用方必须拒绝启动(fail closed)。
//
// 信封文本格式(单列 TEXT 即可存下, 无需迁移列类型):
//
//	sysec$1$<keyID>$<b64url(归属绑定)>$<b64url(被包裹的 DEK)>$<b64url(包裹随机数)>$<b64url(加密随机数)>$<b64url(密文)>
//
// 其中归属绑定(canonical binding)以长度前缀把「表 + 行归属 + 列」拼成无歧义串,
// 同时进入两层 GCM 的 AAD 与信封明文头: 改动信封头任何一个字段都会让 GCM 验签失败,
// 把整条密文搬到别的行(换表/换行/换列)则会在解密前被归属比对拦下。
package secretstore

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
)

const (
	// envelopePrefix 是信封前缀, 用于把密文与历史明文区分开(既有明文迁移的判据)。
	envelopePrefix = "sysec$"
	// envelopeVersion 是信封格式版本; 解析到不认识的版本一律拒绝而不是猜测。
	envelopeVersion = "1"
	// envelopeFields 是信封的字段数: 前缀占位 + 版本 + keyID + 绑定 + 包裹 DEK + 两个随机数 + 密文。
	envelopeFields = 8
	// dekLen / nonceLen: AES-256 与 GCM 标准参数。
	dekLen   = 32
	nonceLen = 12
	// bindingSeparator 用作长度前缀绑定串的分隔符。
	bindingSeparator = ""
	// aadPrefix 按用途域分隔两层 GCM 的 AAD, 防止包裹值与密文互相顶替。
	aadPrefixValue = "sysec/v1|val|"
	aadPrefixDEK   = "sysec/v1|dek|"
)

// Binding 描述一条秘密的字段归属, 进入 AAD 与信封头:
// 同一段密文只有在「同一张表的同一行的同一列」上才能解开, 搬到别处解密即失败。
type Binding struct {
	Table  string // 表名, 如 settings / channel_keys。
	Owner  string // 行归属标识, 如 key:epay_key / channel_id:7;name:default。
	Column string // 列名, 如 value / key。
}

// Canonical 返回无歧义的归属串: 长度前缀保证用户可控的 Owner 内容无法伪造出别的绑定。
func (b Binding) Canonical() string {
	return b.Table + bindingSeparator +
		fmt.Sprintf("%d:", len(b.Owner)) + b.Owner + bindingSeparator +
		b.Column
}

// SettingBinding 系统设置行的归属绑定: 设置键即主键, 天然不可变。
func SettingBinding(key string) Binding {
	return Binding{Table: "settings", Owner: "key:" + key, Column: "value"}
}

// ChannelKeyBinding 渠道凭据行的归属绑定: 行内凭据名与所属渠道构成归属, 两者在业务上不随更新而变。
func ChannelKeyBinding(channelID int, name string) Binding {
	return Binding{Table: "channel_keys", Owner: fmt.Sprintf("channel_id:%d;name:%s", channelID, name), Column: "key"}
}

// IsEnvelope 判断库内取到的值是否为信封格式。历史明文与空值都不是信封。
func IsEnvelope(value string) bool {
	return strings.HasPrefix(value, envelopePrefix)
}

// Seal 用随机 DEK 加密 plaintext 并以 KEK 包裹 DEK, 返回信封文本。
// AAD 同时绑定 keyID 与字段归属: 密文换行/换列/换表, 或信封头被改写, 都无法通过解密。
func (k *MasterKey) Seal(b Binding, plaintext string) (string, error) {
	dek := make([]byte, dekLen)
	if _, err := rand.Read(dek); err != nil {
		return "", fmt.Errorf("generate data key: %w", err)
	}
	wrapNonce := make([]byte, nonceLen)
	valueNonce := make([]byte, nonceLen)
	if _, err := rand.Read(wrapNonce); err != nil {
		return "", fmt.Errorf("generate wrap nonce: %w", err)
	}
	if _, err := rand.Read(valueNonce); err != nil {
		return "", fmt.Errorf("generate value nonce: %w", err)
	}

	canonical := []byte(b.Canonical())
	wrappedDEK, err := sealWithKey(k.kek, wrapNonce, dek, dekAAD(k.keyID, canonical))
	if err != nil {
		return "", fmt.Errorf("wrap data key: %w", err)
	}
	ciphertext, err := sealWithKey(dek, valueNonce, []byte(plaintext), valueAAD(k.keyID, canonical))
	if err != nil {
		return "", fmt.Errorf("encrypt secret: %w", err)
	}

	return strings.Join([]string{
		strings.TrimSuffix(envelopePrefix, "$"),
		envelopeVersion,
		k.keyID,
		base64.RawURLEncoding.EncodeToString(canonical),
		base64.RawURLEncoding.EncodeToString(wrappedDEK),
		base64.RawURLEncoding.EncodeToString(wrapNonce),
		base64.RawURLEncoding.EncodeToString(valueNonce),
		base64.RawURLEncoding.EncodeToString(ciphertext),
	}, "$"), nil
}

// Open 校验归属绑定后解开信封。任何一处不符(版本/密钥 ID/绑定/密文)都返回错误, 不做降级。
func (k *MasterKey) Open(b Binding, envelope string) (string, error) {
	parts := strings.Split(envelope, "$")
	if len(parts) != envelopeFields || parts[0] != strings.TrimSuffix(envelopePrefix, "$") {
		return "", fmt.Errorf("secret envelope is malformed")
	}
	if parts[1] != envelopeVersion {
		return "", fmt.Errorf("unsupported secret envelope version %q", parts[1])
	}
	if parts[2] != k.keyID {
		return "", fmt.Errorf("secret envelope belongs to key ID %q but active key ID is %q", parts[2], k.keyID)
	}
	storedBinding, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil {
		return "", fmt.Errorf("decode secret envelope binding: %w", err)
	}
	canonical := []byte(b.Canonical())
	if len(storedBinding) != len(canonical) || !bytes.Equal(storedBinding, canonical) {
		// 密文被搬到别的行/列: 归属不符直接拒绝, 不给换行解密的机会。
		return "", fmt.Errorf("secret envelope ownership binding mismatch")
	}
	wrappedDEK, err := base64.RawURLEncoding.DecodeString(parts[4])
	if err != nil {
		return "", fmt.Errorf("decode wrapped data key: %w", err)
	}
	wrapNonce, err := base64.RawURLEncoding.DecodeString(parts[5])
	if err != nil {
		return "", fmt.Errorf("decode wrap nonce: %w", err)
	}
	valueNonce, err := base64.RawURLEncoding.DecodeString(parts[6])
	if err != nil {
		return "", fmt.Errorf("decode value nonce: %w", err)
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(parts[7])
	if err != nil {
		return "", fmt.Errorf("decode ciphertext: %w", err)
	}

	dek, err := openWithKey(k.kek, wrapNonce, wrappedDEK, dekAAD(k.keyID, canonical))
	if err != nil {
		return "", fmt.Errorf("unwrap data key (wrong master key?): %w", err)
	}
	plaintext, err := openWithKey(dek, valueNonce, ciphertext, valueAAD(k.keyID, canonical))
	if err != nil {
		return "", fmt.Errorf("decrypt secret (wrong master key or tampered row?): %w", err)
	}
	return string(plaintext), nil
}

func valueAAD(keyID string, canonical []byte) []byte {
	return append([]byte(aadPrefixValue+keyID+bindingSeparator), canonical...)
}

func dekAAD(keyID string, canonical []byte) []byte {
	return append([]byte(aadPrefixDEK+keyID+bindingSeparator), canonical...)
}

func sealWithKey(key, nonce, plaintext, aad []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	return gcm.Seal(nil, nonce, plaintext, aad), nil
}

func openWithKey(key, nonce, ciphertext, aad []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, fmt.Errorf("invalid secret nonce length")
	}
	return gcm.Open(nil, nonce, ciphertext, aad)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

package op

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// 备份加密格式(版本 1):
//
//	header = "OCTOBK1\n"(8) + salt(16) + iv(16); 之后为 AES-256-CTR 密文; 结尾 32 字节 HMAC-SHA256。
//	MAC 覆盖 header+密文(加密后认证), 口令经 PBKDF2-HMAC-SHA256(20 万轮)派生 encKey/macKey 各 32 字节。
//
// 口令只来自 config.json backup.passphrase 或环境变量 OCTOPUS_BACKUP_PASSPHRASE, 绝不入库:
// 备份文件本身包含整个数据库, 口令若存进数据库等于把钥匙锁进要送走的保险箱。
const (
	backupMagic         = "OCTOBK1\n"
	backupSaltLen       = 16
	backupIVLen         = 16
	backupMACLen        = 32
	backupKeyLen        = 32
	backupKDFIterations = 200000
)

// pbkdf2SHA256 按 RFC 8018 派生密钥, 自实现以免引入外部依赖。
func pbkdf2SHA256(password, salt []byte, iterations, keyLen int) []byte {
	prf := hmac.New(sha256.New, password)
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen
	dk := make([]byte, 0, numBlocks*hashLen)
	var counter [4]byte
	for block := 1; block <= numBlocks; block++ {
		prf.Reset()
		prf.Write(salt)
		counter[0] = byte(block >> 24)
		counter[1] = byte(block >> 16)
		counter[2] = byte(block >> 8)
		counter[3] = byte(block)
		prf.Write(counter[:])
		u := prf.Sum(nil)
		t := make([]byte, len(u))
		copy(t, u)
		for i := 1; i < iterations; i++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(nil)
			for x := range t {
				t[x] ^= u[x]
			}
		}
		dk = append(dk, t...)
	}
	return dk[:keyLen]
}

// deriveBackupKeys 由口令与盐派生加密密钥与 MAC 密钥。
func deriveBackupKeys(passphrase string, salt []byte) (encKey, macKey []byte) {
	dk := pbkdf2SHA256([]byte(passphrase), salt, backupKDFIterations, 2*backupKeyLen)
	return dk[:backupKeyLen], dk[backupKeyLen:]
}

// ctrMACWriter 同时完成 CTR 加/解密与 MAC 累计。
// MAC 恒覆盖密文(加密后认证): 加密方向先 XOR 得密文再入 MAC, 解密方向先入 MAC 再 XOR 还原。
type ctrMACWriter struct {
	stream   cipher.Stream
	mac      hash.Hash
	w        io.Writer
	macFirst bool // 解密方向: p 是待验密文, 先入 MAC 再解密。
}

func (c *ctrMACWriter) Write(p []byte) (int, error) {
	if c.macFirst {
		c.mac.Write(p)
		c.stream.XORKeyStream(p, p)
	} else {
		c.stream.XORKeyStream(p, p)
		c.mac.Write(p)
	}
	return c.w.Write(p)
}

// EncryptBackupFile 加密备份文件到 dst(0600)。src 保持不动, 由调用方决定删除明文。
func EncryptBackupFile(src, dst, passphrase string) error {
	if strings.TrimSpace(passphrase) == "" {
		return fmt.Errorf("backup passphrase is empty")
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create encrypted backup: %w", err)
	}
	ok := false
	defer func() {
		out.Close()
		if !ok {
			_ = os.Remove(dst)
		}
	}()

	salt := make([]byte, backupSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("read salt: %w", err)
	}
	iv := make([]byte, backupIVLen)
	if _, err := rand.Read(iv); err != nil {
		return fmt.Errorf("read iv: %w", err)
	}
	header := append([]byte(backupMagic), salt...)
	header = append(header, iv...)
	encKey, macKey := deriveBackupKeys(passphrase, salt)
	block, err := aes.NewCipher(encKey)
	if err != nil {
		return fmt.Errorf("init cipher: %w", err)
	}
	mac := hmac.New(sha256.New, macKey)
	mac.Write(header)
	if _, err := out.Write(header); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	cw := &ctrMACWriter{stream: cipher.NewCTR(block, iv), mac: mac, w: out}
	if _, err := io.CopyBuffer(cw, in, make([]byte, 128*1024)); err != nil {
		return fmt.Errorf("encrypt backup: %w", err)
	}
	if _, err := out.Write(mac.Sum(nil)); err != nil {
		return fmt.Errorf("write mac: %w", err)
	}
	ok = true
	return nil
}

// DecryptBackupFile 校验 MAC 后解密备份; 口令错误或文件被篡改返回错误且不产出明文。
func DecryptBackupFile(src, dst, passphrase string) error {
	if strings.TrimSpace(passphrase) == "" {
		return fmt.Errorf("backup passphrase is empty")
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open encrypted backup: %w", err)
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("stat encrypted backup: %w", err)
	}
	headerLen := len(backupMagic) + backupSaltLen + backupIVLen
	ctLen := info.Size() - int64(headerLen) - backupMACLen
	if ctLen < 0 {
		return fmt.Errorf("encrypted backup is truncated")
	}
	header := make([]byte, headerLen)
	if _, err := io.ReadFull(in, header); err != nil {
		return fmt.Errorf("read header: %w", err)
	}
	if string(header[:len(backupMagic)]) != backupMagic {
		return fmt.Errorf("not an octopus encrypted backup")
	}
	salt := header[len(backupMagic) : len(backupMagic)+backupSaltLen]
	iv := header[len(backupMagic)+backupSaltLen:]
	encKey, macKey := deriveBackupKeys(passphrase, salt)
	block, err := aes.NewCipher(encKey)
	if err != nil {
		return fmt.Errorf("init cipher: %w", err)
	}
	mac := hmac.New(sha256.New, macKey)
	mac.Write(header)

	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	ok := false
	defer func() {
		out.Close()
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	cr := &ctrMACWriter{stream: cipher.NewCTR(block, iv), mac: mac, w: out, macFirst: true}
	if _, err := io.CopyBuffer(cr, io.LimitReader(in, ctLen), make([]byte, 128*1024)); err != nil {
		return fmt.Errorf("decrypt backup: %w", err)
	}
	stored := make([]byte, backupMACLen)
	if _, err := io.ReadFull(in, stored); err != nil {
		return fmt.Errorf("read mac: %w", err)
	}
	if subtle.ConstantTimeCompare(stored, mac.Sum(nil)) != 1 {
		return fmt.Errorf("backup mac mismatch: wrong passphrase or corrupted file")
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close output: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return fmt.Errorf("finalize output: %w", err)
	}
	ok = true
	return nil
}

// BackupChecksumFile 返回备份对应的 .sha256 校验和文件路径。
func BackupChecksumFile(path string) string { return path + ".sha256" }

// WriteBackupChecksum 计算备份文件 SHA-256 并以标准 "<hex>  <文件名>" 格式写入旁路文件。
func WriteBackupChecksum(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open backup for checksum: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.CopyBuffer(h, f, make([]byte, 128*1024)); err != nil {
		return fmt.Errorf("checksum backup: %w", err)
	}
	line := hex.EncodeToString(h.Sum(nil)) + "  " + filepath.Base(path) + "\n"
	if err := os.WriteFile(BackupChecksumFile(path), []byte(line), 0o640); err != nil {
		return fmt.Errorf("write checksum: %w", err)
	}
	return nil
}

// VerifyBackupChecksum 校验备份文件与 .sha256 是否一致; 校验和缺失或不匹配返回 false。
func VerifyBackupChecksum(path string) bool {
	data, err := os.ReadFile(BackupChecksumFile(path))
	if err != nil {
		return false
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.CopyBuffer(h, f, make([]byte, 128*1024)); err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(fields[0]), []byte(hex.EncodeToString(h.Sum(nil)))) == 1
}

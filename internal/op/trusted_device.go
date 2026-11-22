package op

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// 2FA 信任设备的签发、校验与吊销。设计口径见 model/trusted_device.go 头注释。
//
// 这里刻意**不**缓存: 校验要走数据库, 这样吊销立即生效。信任设备是低频动作(登录时一次),
// 为它加一层进程内缓存换来的是"吊销后仍能免码登录"的窗口 —— 那是安全功能最不该有的 bug。

// trustedDeviceTokenLen 明文令牌字节数。32 字节(256 bit)熵, hex 后 64 字符。
const trustedDeviceTokenLen = 32

// trustedDeviceHash 把明文令牌换成落库形态。
//
// 用裸 sha256 而非加盐慢哈希: 令牌本身是 32 字节随机数, 不存在"弱口令被字典爆破"的问题;
// 加盐慢哈希只会让每次登录多花几十毫秒却换不来任何安全性(这一点与用户密码正相反)。
func trustedDeviceHash(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}

// normalizeTrustedDeviceToken 统一令牌形状: 空串与空白一律视为"没有令牌"。
// 不做长度/字符集校验以外的推断 —— 形状不对的令牌查不到哈希, 天然无效。
func normalizeTrustedDeviceToken(token string) string {
	return strings.TrimSpace(token)
}

// normalizeDeviceText 截断 UA / IP, 并去掉换行(它们会进设置页展示, 也进审计 detail)。
func normalizeDeviceText(value string, max int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if len(value) > max {
		value = value[:max]
	}
	return value
}

// TrustedDeviceIssue 为某用户签发一张信任设备凭证, 返回**明文令牌**(只在这一次出现)与设备记录。
//
// 同一次签发顺带清理该用户的过期记录: 过期行除了占空间没有别的用途, 而信任设备没有"历史"语义
// (用户要看的是"现在有哪几台能免码登录")。清理失败不影响签发 —— 它只是卫生, 不是正确性。
func TrustedDeviceIssue(userID uint, userAgent, clientIP string) (string, model.TrustedDevice, error) {
	if userID == 0 {
		return "", model.TrustedDevice{}, fmt.Errorf("invalid user")
	}
	raw := make([]byte, trustedDeviceTokenLen)
	if _, err := rand.Read(raw); err != nil {
		return "", model.TrustedDevice{}, err
	}
	token := hex.EncodeToString(raw)
	now := time.Now()
	device := model.TrustedDevice{
		UserID:     userID,
		TokenHash:  trustedDeviceHash(token),
		UserAgent:  normalizeDeviceText(userAgent, model.TrustedDeviceUserAgentMaxLen),
		ClientIP:   normalizeDeviceText(clientIP, 64),
		CreatedAt:  now,
		LastUsedAt: now,
		ExpiresAt:  now.Add(model.TrustedDeviceTTL),
	}
	if err := db.GetDB().Create(&device).Error; err != nil {
		return "", model.TrustedDevice{}, fmt.Errorf("failed to register trusted device: %w", err)
	}
	_, _ = TrustedDevicePruneExpired(userID)
	return token, device, nil
}

// TrustedDeviceVerify 校验明文令牌对该用户是否仍然有效; 有效时刷新 LastUsedAt。
//
// 判据三条: 哈希命中 + 归属该用户 + 未过期。任何一条不满足都返回 false, **不区分原因** ——
// 调用方(登录)只会得到"要不要动态码"这一个结论, 区分原因等于给探测者一个判别接口。
//
// 命中后刷新 LastUsedAt 用的是 Update 而不是 Save: 并发登录时后写者覆盖前写者也无所谓,
// 这个字段只用于展示"最近使用时间"。
func TrustedDeviceVerify(userID uint, rawToken string) (model.TrustedDevice, bool) {
	token := normalizeTrustedDeviceToken(rawToken)
	if userID == 0 || token == "" {
		return model.TrustedDevice{}, false
	}
	var device model.TrustedDevice
	err := db.GetDB().Where("token_hash = ? AND user_id = ?", trustedDeviceHash(token), userID).
		First(&device).Error
	if err != nil {
		// 查不到 = 无效令牌; 数据库其它错误同样按"不放行"处理(fail closed)。
		return model.TrustedDevice{}, false
	}
	now := time.Now()
	if !now.Before(device.ExpiresAt) {
		return model.TrustedDevice{}, false
	}
	_ = db.GetDB().Model(&model.TrustedDevice{}).Where("id = ?", device.ID).
		Update("last_used_at", now).Error
	device.LastUsedAt = now
	return device, true
}

// TrustedDeviceList 列出该用户尚未过期的信任设备(按创建时间倒序)。
// 过期记录不返回: 展示一堆"早已失效但还在列表里"的条目只会让人以为还有旁路可用。
func TrustedDeviceList(userID uint) ([]model.TrustedDevice, error) {
	if userID == 0 {
		return nil, fmt.Errorf("invalid user")
	}
	var devices []model.TrustedDevice
	err := db.GetDB().Where("user_id = ? AND expires_at > ?", userID, time.Now()).
		Order("created_at DESC").Find(&devices).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list trusted devices: %w", err)
	}
	return devices, nil
}

// TrustedDeviceRevoke 吊销一台设备。带 user_id 条件 —— 不能靠 id 单独定位, 否则一个用户
// 可以吊销别人的设备(越权写)。返回受影响行数, 0 表示不存在或不属于该用户。
func TrustedDeviceRevoke(userID, deviceID uint) (int64, error) {
	if userID == 0 || deviceID == 0 {
		return 0, fmt.Errorf("invalid arguments")
	}
	result := db.GetDB().Where("id = ? AND user_id = ?", deviceID, userID).Delete(&model.TrustedDevice{})
	if result.Error != nil {
		return 0, fmt.Errorf("failed to revoke trusted device: %w", result.Error)
	}
	return result.RowsAffected, nil
}

// TrustedDeviceRevokeAll 吊销该用户全部信任设备(改密、重绑 2FA、用户点"全部吊销"时调用)。
func TrustedDeviceRevokeAll(userID uint) (int64, error) {
	if userID == 0 {
		return 0, fmt.Errorf("invalid user")
	}
	result := db.GetDB().Where("user_id = ?", userID).Delete(&model.TrustedDevice{})
	if result.Error != nil {
		return 0, fmt.Errorf("failed to revoke trusted devices: %w", result.Error)
	}
	return result.RowsAffected, nil
}

// TrustedDevicePruneExpired 删除该用户的过期记录, 返回删除行数。
// 调用方不需要处理它的错误: 清理失败只是留下几行垃圾, 不影响"过期即失效"这个正确性判据 ——
// 那个判据在 TrustedDeviceVerify 里, 不依赖本函数是否跑过。
func TrustedDevicePruneExpired(userID uint) (int64, error) {
	if userID == 0 {
		return 0, fmt.Errorf("invalid user")
	}
	result := db.GetDB().Where("user_id = ? AND expires_at <= ?", userID, time.Now()).
		Delete(&model.TrustedDevice{})
	return result.RowsAffected, result.Error
}

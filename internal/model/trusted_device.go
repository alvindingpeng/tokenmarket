package model

import "time"

// TrustedDevice 2FA 信任设备(登录时免输动态码的旁路)。
//
// 为什么要有它: 每次登录都必须掏手机看 6 位码, 用户会在第三周开始把恢复码拍照存在相册里,
// 或者干脆关掉 2FA —— 那就是"安全措施把人逼成更不安全的行为"。业界做法是允许把**当前这台设备**
// 记 30 天, 期间登录只验密码; 一旦账号出现敏感变更(改密/重绑 2FA)或用户手动吊销, 立刻失效。
//
// 三条安全口径(逐条都有用例钉住, 见 op/trusted_device_test.go):
//  1. **令牌只以哈希形态落库**: TokenHash 是 sha256(明文), 明文只在签发那一刻经 Set-Cookie 下发一次。
//     库被拖走也换不出可用令牌 —— 与恢复码同一套思路(recoveryHash)。
//  2. **只免第二因子, 不免密码**: 它不能当登录凭据用, 也不能替代 step-up(高危操作仍要重新验密码)。
//  3. **可吊销且会被动失效**: 用户可在设置页吊销单台/全部; 改密、重绑/解绑 2FA 时全量清除。
//
// 明文令牌不进本结构体: 它在 op.TrustedDeviceIssue 的返回值里传一次, 不落任何日志/响应视图。
const (
	// TrustedDeviceTTL 信任设备有效期(30 天)。与 GitHub/Google 的常见口径一致。
	TrustedDeviceTTL = 30 * 24 * time.Hour
	// TrustedDeviceCookieName 承载明文令牌的 Cookie 名。
	TrustedDeviceCookieName = "sy_trusted_device"
	// TrustedDeviceUserAgentMaxLen 记录 UA 的最大长度(只用于让用户辨认是哪台设备, 不参与鉴权)。
	TrustedDeviceUserAgentMaxLen = 200
)

// TrustedDevice 一行 = 一台被信任的设备。
//
// TokenHash 与 UserID 都不下发: 前者是凭据等价物, 后者是当前登录用户自己, 没有信息量。
type TrustedDevice struct {
	ID     uint `json:"id" gorm:"primaryKey"`
	UserID uint `json:"-" gorm:"not null;index:idx_trusted_device_user,priority:1"`
	// TokenHash 是明文令牌的 sha256 hex。uniqueIndex 保证同一令牌不可能登记两次。
	TokenHash string `json:"-" gorm:"not null;uniqueIndex:idx_trusted_device_hash"`
	// UserAgent / ClientIP 只为"让用户认出这是哪台设备", 不是鉴权依据(都会变、都可伪造)。
	UserAgent string    `json:"user_agent" gorm:"not null;default:''"`
	ClientIP  string    `json:"client_ip" gorm:"not null;default:''"`
	CreatedAt time.Time `json:"created_at"`
	// LastUsedAt 每次靠它免码登录时刷新; 从未用过时等于 CreatedAt。
	LastUsedAt time.Time `json:"last_used_at"`
	ExpiresAt  time.Time `json:"expires_at" gorm:"not null;index:idx_trusted_device_user,priority:2"`
}

// TableName 固定表名, 避免 GORM 复数化在改写时漂移。
func (TrustedDevice) TableName() string { return "trusted_devices" }

// TrustedDeviceView 下发给设置页的设备清单视图 —— 不含任何凭据。
type TrustedDeviceView struct {
	ID         uint      `json:"id"`
	UserAgent  string    `json:"user_agent"`
	ClientIP   string    `json:"client_ip"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	// Expired 由服务端算好: 前端不该自己拿本地时钟跟 UTC 时间戳比 —— 时区/时钟漂移会算出
	// "已过期"而服务端仍接受, 或反之。判据只存在于一处(op.TrustedDeviceVerify)。
	Expired bool `json:"expired"`
}

// View 转视图; expired 显式传入, 与校验函数共用同一个 now。
func (d TrustedDevice) View(now time.Time) TrustedDeviceView {
	return TrustedDeviceView{
		ID:         d.ID,
		UserAgent:  d.UserAgent,
		ClientIP:   d.ClientIP,
		CreatedAt:  d.CreatedAt,
		LastUsedAt: d.LastUsedAt,
		ExpiresAt:  d.ExpiresAt,
		Expired:    !now.Before(d.ExpiresAt),
	}
}

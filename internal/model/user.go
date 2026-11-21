package model

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Role 用户角色: 管理员保留全部功能; 渠道商拥有渠道管理与共享发布; 用户为终端消费者。
type Role string

const (
	RoleAdmin    Role = "admin"
	RoleReseller Role = "reseller"
	RoleUser     Role = "user"
)

// UserStatus 账号状态: 注册默认关闭, 开启后按设置决定是否需要审批。
type UserStatus string

const (
	StatusActive   UserStatus = "active"
	StatusPending  UserStatus = "pending"
	StatusDisabled UserStatus = "disabled"
)

// PasswordMinLen 密码最短长度: 注册/改密/找回密码统一收紧到 8 位。
const PasswordMinLen = 8

// emailPattern 邮箱基本格式; 不追求 RFC 全集, 只挡明显非法输入。
var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// ValidEmail 判断邮箱基本格式。
func ValidEmail(email string) bool {
	return emailPattern.MatchString(email)
}

// TimezoneMaxLen 时区偏好名的最大长度; 最长的真实 IANA 名
// ("America/Argentina/ComodRivadavia")只有 32 字节, 64 是留给未来新增区名的余量,
// 同时也是一个硬上限 —— 超长的输入不该进库(列宽就按它建的)。
const TimezoneMaxLen = 64

// NormalizeTimezone 归一用户时区偏好, 返回"可入库的名字"与"是否合法"。
//
// 为什么不能只是 TrimSpace 后存原文: 这一列的下游是 time.LoadLocation。存进去一个
// 解析不了的值不会当场报错, 而是等到用户打开统计页时才静默回落到服务器时区 ——
// 用户以为自己设成了 +07, 看到的却是 +08 的数字, 且界面上没有任何提示。
// 因此校验必须发生在**写入时**, 把"存坏了"这种状态挡在库外。
//
// 语义约定:
//   - 空串 = 跟随服务器时区(默认)。这不是"未设置"的错误形态, 老账号全部是它, 合法。
//   - "UTC" 显式接受: 它是被 time.LoadLocation 解析的真实区名, 也是一个合理的选择
//     (跨境团队刻意想看 UTC 日历), 不能因为它"等于不加偏移"就拒掉。
//   - "Local" 拒绝: 它是 Go 的伪时区名, 含义取决于**服务器**的运行时环境。把它存进账号
//     偏好里, 迁移到另一台机器后同一个账号会悄悄换时区 —— 用户无法控制, 也无法理解。
//     想要"跟随服务器"的正确写法是空串。
//   - 控制字符(换行/回车/制表等)与 IANA 名字符集之外的字节一律拒绝: LoadLocation
//     会把名字当路径的一部分去查 tzdata, 允许任意字节等于让请求体能左右文件查找。
//     按 IANA 名的字符集(字母数字与 `/`、`-`、`_`、`+`)做白名单即可挡掉这类输入。
//   - 两端的普通空格是宽容的(粘贴噪声), 由 TrimSpace 收掉; 但控制字符不宽容, 见函数体。
//
// 返回的名字取 loc.String() 而不是原始输入: 两者的差别只在两端空白,
// 但它让"写进库的值"就是"被校验过的值", 不必让人再去比对。
//
// **不要指望这里能统一大小写或别名** —— 实测 time.LoadLocation 不做归一:
// "asia/shanghai" 直接报 unknown time zone, 而 "Asia/Calcutta" 与 "Asia/Kolkata"
// 是两个独立条目、各自原样返回。所以库里可能同时存在偏移相同但名字不同的两个值,
// 下游任何"是否同区"的判断都必须按**偏移**比, 不能按名字字符串比
// (统计页那侧已经是这么做的, 见 statsTimezoneFacts 的注释)。
func NormalizeTimezone(name string) (string, bool) {
	// 控制字符(换行/回车/制表/其它 <0x20 与 DEL)一律拒绝, 且这一判必须**早于** TrimSpace。
	//
	// 为什么不能靠 TrimSpace 兜住: 它会把两端的 "\n"/"\t" 直接吃掉, 于是
	// "Asia/Shanghai\n" 清洗成合法名后被接受 —— 库里存的是干净值, 请求却是脏的。
	// 这种输入只可能来自手搓请求或拼错的客户端, 猜它的意图没有价值; 而且"被清洗后才合法"
	// 会让 400 的边界取决于清洗顺序(以后有人把 TrimSpace 挪走, 行为就变了)。
	// 显式失败比静默修复更适合这条链路: 前端下拉框本来就不会产生这种值。
	//
	// 注意: 普通空格(0x20)不算控制字符 —— 两端的空格是粘贴代码里最常见的噪声,
	// 由下面的 TrimSpace 收掉, 不因此拒掉整个请求。
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return "", false
		}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", true
	}
	if len(name) > TimezoneMaxLen {
		return "", false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '/', r == '-', r == '_', r == '+':
		default:
			return "", false
		}
	}
	// "Local" 是 Go 的运行时伪名而非 IANA 区名: 存它等于把"服务器时区"冻结进账号数据,
	// 而本列的语义是"空串才跟随服务器"。大小写不敏感地拒绝, 因为 LoadLocation 也接受小写。
	if strings.EqualFold(name, "Local") {
		return "", false
	}
	loc, err := time.LoadLocation(name)
	if err != nil || loc == nil {
		return "", false
	}
	// loc.String() 返回的就是解析成功时的那个名字(不归一大小写, 也不合并别名),
	// 用它而非原始输入, 只是为了让"写进库的值"就是"刚被校验过的值"。
	return loc.String(), true
}

// User 平台账号; 余额三字段承担计费: Balance 可用余额, Frozen 预扣冻结, 其余为累计口径。
type User struct {
	ID       uint   `json:"id" gorm:"primaryKey"`
	Username string `json:"username" gorm:"unique;not null"`
	Password string `json:"-" gorm:"not null"`
	// Email 注册邮箱(注册必填, 找回密码凭据); 空串表示老账号未绑定, 唯一性由应用层保证。
	Email  string     `json:"email" gorm:"size:254;not null;default:'';index"`
	Role   Role       `json:"role" gorm:"not null;default:user;index"`
	Status UserStatus `json:"status" gorm:"not null;default:active;index"`
	// Tier 服务等级: 只影响限流兜底额度, 与 Role 权限正交。空串按 free 处理(见 EffectiveTier),
	// 这样加列前的老账号不需要回填, 也不会意外拿到高额度。
	Tier UserTier `json:"tier" gorm:"size:32;not null;default:'';index"`
	// Timezone 时区偏好: IANA 名(如 Asia/Shanghai), 由 model.NormalizeTimezone 校验后写入。
	// **空串 = 跟随服务器时区**, 这是默认值也是老账号的值 —— 正因为空串承载了这个语义,
	// 加列时不需要任何回填(否则就得区分"没设过"和"设成了服务器时区"两种状态, 而后者
	// 一旦服务器搬家就变成错的)。不要在别处把它当"必填"或"未知"处理。
	Timezone string `json:"timezone" gorm:"size:64;not null;default:'';index"`
	// TokenVersion 随改密/禁用递增, 使既有 JWT 全部失效。
	TokenVersion int `json:"-" gorm:"not null;default:0"`
	// 计费口径: Balance 可用余额; Frozen 预扣冻结额(可用余额 = Balance - Frozen 的语义由扣费 SQL 保证);
	// TotalSpent 用户累计支出(上浮后价格); TotalRevenue 渠道商累计收入(供货价口径)。
	Balance      float64 `json:"balance" gorm:"type:decimal(18,6);not null;default:0"`
	Frozen       float64 `json:"frozen" gorm:"type:decimal(18,6);not null;default:0"`
	TotalSpent   float64 `json:"total_spent" gorm:"type:decimal(18,6);not null;default:0"`
	TotalRevenue float64 `json:"total_revenue" gorm:"type:decimal(18,6);not null;default:0"`
	// TotalRecharged 预留: 充值功能上线后累计充值额。
	TotalRecharged float64 `json:"total_recharged" gorm:"type:decimal(18,6);not null;default:0"`
	// 收益池提现口径(P3): 可提 = TotalRevenue - TotalWithdrawn - WithdrawFrozen;
	// 提现只在这三个字段之间迁移, 永不动 Balance(充值款不可提现, 防套现)。
	TotalWithdrawn float64 `json:"total_withdrawn" gorm:"type:decimal(18,6);not null;default:0"`
	WithdrawFrozen float64 `json:"withdraw_frozen" gorm:"type:decimal(18,6);not null;default:0"`
	// TOTP 双因素: TOTPEnabled 表示已启用(登录除密码外还需动态码);
	// TOTPSecretEncrypted 存信封加密后的 base32 密钥(见 secretstore.UserTOTPBinding), 绝不下发。
	TOTPEnabled         bool      `json:"totp_enabled" gorm:"not null;default:false"`
	TOTPSecretEncrypted string    `json:"-" gorm:"not null;default:''"`
	TOTPVersion         int64     `json:"-" gorm:"not null;default:0"`
	TOTPLastStep        int64     `json:"-" gorm:"not null;default:-1"`
	TOTPRecoveryHashes  string    `json:"-" gorm:"not null;default:''"`
	TOTPAttempts        int       `json:"-" gorm:"not null;default:0"`
	TOTPAttemptWindow   int64     `json:"-" gorm:"not null;default:0"`
	CreatedAt           time.Time `json:"created_at"`
}

type UserLogin struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Expire   int    `json:"expire"`
	// TOTPCode 双因素动态码: 仅当账号启用 2FA 时必填; 未启用时忽略该字段。
	TOTPCode string `json:"totp_code"`
}

// UserRegister 注册请求; 不收 role, 角色由注册端点决定, 防止自提权。
// 密码输入两遍(Password/PasswordConfirm), 邮箱验证码(EmailCode)校验通过才允许注册。
type UserRegister struct {
	Username        string `json:"username"`
	Password        string `json:"password"`
	PasswordConfirm string `json:"password_confirm"`
	Email           string `json:"email"`
	EmailCode       string `json:"email_code"`
	Reason          string `json:"reason"` // 渠道商注册的申请说明。
}

// UserEmailCode 发送邮箱验证码请求; purpose: register(注册) | reset(找回密码)。
type UserEmailCode struct {
	Email   string `json:"email"`
	Purpose string `json:"purpose"`
}

// UserForgotPassword 找回密码请求: 邮箱验证码通过后重设密码, 同样输入两遍。
type UserForgotPassword struct {
	Email           string `json:"email"`
	EmailCode       string `json:"email_code"`
	Password        string `json:"password"`
	PasswordConfirm string `json:"password_confirm"`
}

type UserChangePassword struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

type UserChangeUsername struct {
	NewUsername string `json:"new_username"`
}

// UserChangeTimezone 本人改时区偏好请求。
//
// Timezone 用普通 string 而不是 *string: 空串在这里是**合法且有意义的取值**
// (表示恢复"跟随服务器"), 与 UserManageRequest.Balance 那种"零值即不修改"的
// 指针语义正好相反。用指针反而会让 handler 不得不区分"字段缺失"与"显式清空",
// 而本端点没有"只改别的字段"的场景 —— 整个请求就是改这一列。
type UserChangeTimezone struct {
	Timezone string `json:"timezone"`
}

// UserManageRequest 管理员用户管理请求; 字段按操作语义选用。
// Balance 为指针: 未提交表示不调整余额, 提交 0 表示清零。
type UserManageRequest struct {
	ID       uint       `json:"id"`
	Username string     `json:"username"`
	Email    string     `json:"email"`
	Role     Role       `json:"role"`
	Status   UserStatus `json:"status"`
	Tier     UserTier   `json:"tier"`
	Password string     `json:"password"`
	Balance  *float64   `json:"balance"`
}

// UserView 对外展示的账号信息, 不含密码与令牌版本。
type UserView struct {
	ID             uint       `json:"id"`
	Username       string     `json:"username"`
	Email          string     `json:"email"`
	Role           Role       `json:"role"`
	Status         UserStatus `json:"status"`
	Tier           UserTier   `json:"tier"`
	Balance        float64    `json:"balance"`
	Frozen         float64    `json:"frozen"`
	TotalSpent     float64    `json:"total_spent"`
	TotalRevenue   float64    `json:"total_revenue"`
	TotalWithdrawn float64    `json:"total_withdrawn"`
	WithdrawFrozen float64    `json:"withdraw_frozen"`
	// Timezone 时区偏好(空串 = 跟随服务器); 前端据此决定设置项的显示值。
	Timezone string `json:"timezone"`
	// MustChangePassword 仅登录/状态响应携带: 默认管理员未改初始密码时为真, 前端据此强制引导改密。
	MustChangePassword bool `json:"must_change_password,omitempty"`
	// TOTPEnabled 供前端展示双因素状态; 密钥本身绝不下发。
	TOTPEnabled bool      `json:"totp_enabled,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// EffectiveTier 返回生效的等级: 未设置时回落到 free。
// 存量账号的 tier 列是空串, 若直接当成"无限制"会给所有老用户无限额度 —— 那是最危险的方向。
func (u *User) EffectiveTier() UserTier {
	if IsValidUserTier(u.Tier) {
		return u.Tier
	}
	return TierFree
}

// View 返回账号的对外副本。
func (u *User) View() UserView {
	return UserView{
		ID:             u.ID,
		Username:       u.Username,
		Email:          u.Email,
		Role:           u.Role,
		Status:         u.Status,
		Tier:           u.EffectiveTier(),
		Balance:        u.Balance,
		Frozen:         u.Frozen,
		TotalSpent:     u.TotalSpent,
		TotalRevenue:   u.TotalRevenue,
		TotalWithdrawn: u.TotalWithdrawn,
		WithdrawFrozen: u.WithdrawFrozen,
		Timezone:       u.Timezone,
		TOTPEnabled:    u.TOTPEnabled,
		CreatedAt:      u.CreatedAt,
	}
}

func (u *User) HashPassword() error {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(u.Password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}
	u.Password = string(hashedPassword)
	return nil
}

func (u *User) ComparePassword(password string) error {
	return bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(password))
}

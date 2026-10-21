package op

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/utils/cache"
	"github.com/charmbracelet/log"
	"gorm.io/gorm"
)

// 多用户缓存: 按主键与用户名双向索引; 取代单用户的全局单例。
var (
	userCache     = cache.New[int, model.User](16)
	userNameIndex = cache.New[string, int](16)
)

// UserInit 启动时加载全部用户; 空库创建初始管理员 admin/admin。
func UserInit() error {
	if err := userRefreshCache(context.Background()); err != nil {
		return err
	}
	if userCache.Len() > 0 {
		return nil
	}
	admin := model.User{
		Username: "admin",
		Password: "admin",
		Role:     model.RoleAdmin,
		Status:   model.StatusActive,
	}
	if err := admin.HashPassword(); err != nil {
		return err
	}
	if err := db.GetDB().Create(&admin).Error; err != nil {
		return err
	}
	userCache.Set(int(admin.ID), admin)
	userNameIndex.Set(admin.Username, int(admin.ID))
	log.Infof("initial user: admin,password: admin")
	return nil
}

func userRefreshCache(ctx context.Context) error {
	users := []model.User{}
	if err := db.GetDB().WithContext(ctx).Find(&users).Error; err != nil {
		return err
	}
	userCache.Clear()
	userNameIndex.Clear()
	for _, user := range users {
		userCache.Set(int(user.ID), user)
		userNameIndex.Set(user.Username, int(user.ID))
	}
	return nil
}

// UserVerify 校验用户名密码并返回账号; 账号非 active 一律拒绝。
func UserVerify(username, password string) (model.User, error) {
	id, ok := userNameIndex.Get(strings.TrimSpace(username))
	if !ok {
		return model.User{}, fmt.Errorf("incorrect username or password")
	}
	user, ok := userCache.Get(id)
	if !ok {
		return model.User{}, fmt.Errorf("incorrect username or password")
	}
	if err := user.ComparePassword(password); err != nil {
		return model.User{}, fmt.Errorf("incorrect username or password")
	}
	if user.Status != model.StatusActive {
		return model.User{}, fmt.Errorf("account is not active")
	}
	return user, nil
}

// UserGetByID 按主键取账号, 供鉴权中间件比对状态与令牌版本。
func UserGetByID(id uint) (model.User, error) {
	user, ok := userCache.Get(int(id))
	if !ok {
		return model.User{}, fmt.Errorf("user not found")
	}
	return user, nil
}

// UserList 返回全部账号的对外视图, 按主键升序。
func UserList() []model.UserView {
	views := make([]model.UserView, 0, userCache.Len())
	for _, user := range userCache.GetAll() {
		views = append(views, user.View())
	}
	sort.Slice(views, func(i, j int) bool { return views[i].ID < views[j].ID })
	return views
}

// UserCreate 创建账号; 角色与状态由调用方按端点语义决定, 注册端点不允许自提权。
// email 允许为空(管理员代建的老式账号), 非空须格式合法且全库唯一。
func UserCreate(username, email, password string, role model.Role, status model.UserStatus) (model.User, error) {
	username = strings.TrimSpace(username)
	if username == "" || len(username) > 64 {
		return model.User{}, fmt.Errorf("invalid username")
	}
	if len(password) < model.PasswordMinLen {
		return model.User{}, fmt.Errorf("password must be at least %d characters", model.PasswordMinLen)
	}
	if _, ok := userNameIndex.Get(username); ok {
		return model.User{}, fmt.Errorf("username already exists")
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email != "" {
		if !model.ValidEmail(email) {
			return model.User{}, fmt.Errorf("invalid email")
		}
		if _, err := userGetByEmail(email); err == nil {
			return model.User{}, fmt.Errorf("email already registered")
		}
	}
	user := model.User{Username: username, Email: email, Password: password, Role: role, Status: status}
	if err := user.HashPassword(); err != nil {
		return model.User{}, err
	}
	if err := db.GetDB().Create(&user).Error; err != nil {
		return model.User{}, fmt.Errorf("failed to create user: %w", err)
	}
	userCache.Set(int(user.ID), user)
	userNameIndex.Set(user.Username, int(user.ID))
	return user, nil
}

// userGetByEmail 按邮箱查账号; 邮箱唯一性由应用层保证(空串不参与查重)。
func userGetByEmail(email string) (model.User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return model.User{}, fmt.Errorf("user not found")
	}
	for _, user := range userCache.GetAll() {
		if strings.EqualFold(user.Email, email) {
			return user, nil
		}
	}
	return model.User{}, fmt.Errorf("user not found")
}

// UserUpdateEmail 修改账号邮箱; 非空须格式合法且全库唯一。
func UserUpdateEmail(id uint, email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if !model.ValidEmail(email) {
		return fmt.Errorf("invalid email")
	}
	user, err := UserGetByID(id)
	if err != nil {
		return err
	}
	if other, err := userGetByEmail(email); err == nil && other.ID != id {
		return fmt.Errorf("email already registered")
	}
	if err := db.GetDB().Model(&model.User{}).Where("id = ?", id).Update("email", email).Error; err != nil {
		return fmt.Errorf("failed to update email: %w", err)
	}
	user.Email = email
	userCache.Set(int(id), user)
	return nil
}

// UserForgotPassword 邮箱验证码通过后重设密码(不需旧密码), 吊销其全部既有会话。
func UserForgotPassword(email, password string) error {
	user, err := userGetByEmail(email)
	if err != nil {
		return fmt.Errorf("no account bound to this email")
	}
	if len(password) < model.PasswordMinLen {
		return fmt.Errorf("password must be at least %d characters", model.PasswordMinLen)
	}
	user.Password = password
	if err := user.HashPassword(); err != nil {
		return err
	}
	if err := db.GetDB().Model(&model.User{}).Where("id = ?", user.ID).
		Updates(map[string]any{"password": user.Password, "token_version": user.TokenVersion + 1}).Error; err != nil {
		return fmt.Errorf("failed to update password: %w", err)
	}
	user.TokenVersion++
	userCache.Set(int(user.ID), user)
	return nil
}

// UserUpdateRole 修改账号角色。
func UserUpdateRole(id uint, role model.Role) error {
	if role != model.RoleAdmin && role != model.RoleReseller && role != model.RoleUser {
		return fmt.Errorf("invalid role")
	}
	user, err := UserGetByID(id)
	if err != nil {
		return err
	}
	if err := db.GetDB().Model(&model.User{}).Where("id = ?", id).Update("role", role).Error; err != nil {
		return fmt.Errorf("failed to update role: %w", err)
	}
	user.Role = role
	userCache.Set(int(id), user)
	return nil
}

// UserUpdateStatus 修改账号状态; 禁用递增令牌版本以吊销全部既有会话。
func UserUpdateStatus(id uint, status model.UserStatus) error {
	if status != model.StatusActive && status != model.StatusPending && status != model.StatusDisabled {
		return fmt.Errorf("invalid status")
	}
	user, err := UserGetByID(id)
	if err != nil {
		return err
	}
	updates := map[string]any{"status": status}
	if status == model.StatusDisabled {
		updates["token_version"] = user.TokenVersion + 1
	}
	if err := db.GetDB().Model(&model.User{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}
	if v, ok := updates["token_version"].(int); ok {
		user.TokenVersion = v
	}
	user.Status = status
	userCache.Set(int(id), user)
	return nil
}

// UserResetPassword 管理员重置任意账号密码, 吊销其全部既有会话。
func UserResetPassword(id uint, password string) error {
	if len(password) < model.PasswordMinLen {
		return fmt.Errorf("password must be at least %d characters", model.PasswordMinLen)
	}
	user, err := UserGetByID(id)
	if err != nil {
		return err
	}
	user.Password = password
	if err := user.HashPassword(); err != nil {
		return err
	}
	if err := db.GetDB().Model(&model.User{}).Where("id = ?", id).
		Updates(map[string]any{"password": user.Password, "token_version": user.TokenVersion + 1}).Error; err != nil {
		return fmt.Errorf("failed to reset password: %w", err)
	}
	user.TokenVersion++
	userCache.Set(int(id), user)
	return nil
}

// errBalanceConflict 目标余额写入撞上别的余额变动(预扣/结算/入账)的哨兵错误: 整轮重试。
var errBalanceConflict = errors.New("balance changed concurrently")

// userBalanceWriteAttempts 绝对写连续撞行时的重试上限; 连续撞车按拒绝收场, 不无限自旋。
const userBalanceWriteAttempts = 5

// UserSetBalance 管理员直接调整可用余额: 语义是"把余额设成目标值"(绝对写), 不是充值。
// 冻结额与累计口径不动, 预扣判定始终以余额对冻结额为准。
// actor 记入余额流水, 使"谁在什么时候把余额改成了多少"有据可查; 系统内部调用传零值 actor。
//
// 账本口径(不以缓存作账本): 差额 = 目标值 - 事务内数据库当前值, 流水差额恒等于实际资金变动。
// 并发防护 = 行写锁 + CAS: 事务首个语句把该行余额写成自身值(先占写锁), 再读当前值算差额,
// 最后按读到的值条件迁移写目标值并校验 RowsAffected; 与预扣/结算/入账撞车即整轮重试,
// 绝对写下重试收敛到目标值, 连续撞车则拒绝并要求重试。
// 目标值约束: 有限数(拒绝 NaN/Inf, 它们会污染账本与库值)且不低于在途冻结额 ——
// 冻结是已承诺的支出, 扣到冻结线之下会让可用余额变负、在途预扣无处着落。
func UserSetBalance(id uint, balance float64, actorID uint, actorName, note string) error {
	if math.IsNaN(balance) || math.IsInf(balance, 0) {
		return fmt.Errorf("balance must be a finite number")
	}
	if balance < 0 {
		return fmt.Errorf("balance cannot be negative")
	}
	for attempt := 0; attempt < userBalanceWriteAttempts; attempt++ {
		applied, err := userSetBalanceOnce(id, balance, actorID, actorName, note)
		if err != nil {
			return err
		}
		if applied {
			UserTouchBalance(id)
			return nil
		}
	}
	return fmt.Errorf("failed to update balance: %w", errBalanceConflict)
}

// userSetBalanceOnce 一轮"占行锁 → 读当前值 → 算差额 → CAS 写目标值"。
// applied=false 表示 CAS 撞车且本轮没有任何写入(事务回滚), 由调用方重试。
func userSetBalanceOnce(id uint, balance float64, actorID uint, actorName, note string) (bool, error) {
	applied := false
	err := db.GetDB().Transaction(func(tx *gorm.DB) error {
		// 占行写锁: 把余额写成自身值。拿到写锁后再读, 读到的当前值在本事务内不会被别人改掉,
		// 差额与后续资金变动因此严格对账; 也让本事务首个语句即写, 避免读-写升级相撞。
		// 这是空更新, 不看 RowsAffected(MySQL 按"值变化"计行数, 空更新恒报 0 行)。
		if err := tx.Exec("UPDATE users SET balance = balance WHERE id = ?", id).Error; err != nil {
			return fmt.Errorf("failed to lock user balance: %w", err)
		}
		var current model.User
		if err := tx.First(&current, id).Error; err != nil {
			return fmt.Errorf("user not found")
		}
		// 目标余额不得低于在途冻结额(以事务内当前值判定): 等于冻结线允许, 可用余额恰好为 0。
		if balance < current.Frozen {
			return fmt.Errorf("balance %.6f cannot be lower than the frozen amount %.6f", balance, current.Frozen)
		}
		delta := balance - current.Balance
		if delta == 0 {
			// 目标值即当前值: 绝对写在该点已满足, 不动钱不记流水。
			applied = true
			return nil
		}
		// CAS 写目标值: 只允许把"读到的那个当前值"改成目标值, 撞上别的变动就认输重试,
		// 否则流水记的差额会与真实资金变动对不上。
		result := tx.Model(&model.User{}).
			Where("id = ? AND balance = ?", id, current.Balance).
			Update("balance", balance)
		if result.Error != nil {
			return fmt.Errorf("failed to update balance: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return errBalanceConflict
		}
		if err := ledgerRecord(tx, model.BillingLedger{
			UserID:       id,
			Kind:         model.LedgerKindAdjust,
			Amount:       delta,
			BalanceAfter: balance,
			ActorID:      actorID,
			ActorName:    actorName,
			Note:         note,
		}); err != nil {
			return err
		}
		applied = true
		return nil
	})
	if errors.Is(err, errBalanceConflict) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return applied, nil
}

// UserChangePassword 本人改密(需旧密码), 改后吊销全部既有会话含当前。
func UserChangePassword(id uint, oldPassword, newPassword string) error {
	user, err := UserGetByID(id)
	if err != nil {
		return err
	}
	if err := user.ComparePassword(oldPassword); err != nil {
		return fmt.Errorf("incorrect old password: %w", err)
	}
	if len(newPassword) < model.PasswordMinLen {
		return fmt.Errorf("password must be at least %d characters", model.PasswordMinLen)
	}
	user.Password = newPassword
	if err := user.HashPassword(); err != nil {
		return err
	}
	if err := db.GetDB().Model(&model.User{}).Where("id = ?", id).
		Updates(map[string]any{"password": user.Password, "token_version": user.TokenVersion + 1}).Error; err != nil {
		return fmt.Errorf("failed to update password: %w", err)
	}
	user.TokenVersion++
	userCache.Set(int(id), user)
	return nil
}

// UserChangeUsername 本人改用户名。
func UserChangeUsername(id uint, newUsername string) error {
	newUsername = strings.TrimSpace(newUsername)
	if newUsername == "" {
		return fmt.Errorf("username is required")
	}
	user, err := UserGetByID(id)
	if err != nil {
		return err
	}
	if user.Username == newUsername {
		return fmt.Errorf("new username is the same as the old username")
	}
	if other, ok := userNameIndex.Get(newUsername); ok && other != int(id) {
		return fmt.Errorf("username already exists")
	}
	if err := db.GetDB().Model(&model.User{}).Where("id = ?", id).Update("username", newUsername).Error; err != nil {
		return fmt.Errorf("failed to update username: %w", err)
	}
	userNameIndex.Del(user.Username)
	user.Username = newUsername
	userCache.Set(int(id), user)
	userNameIndex.Set(newUsername, int(id))
	return nil
}

// UserDelete 删除账号; 不允许删除自己, 由 handler 层保证。
func UserDelete(id uint) error {
	user, err := UserGetByID(id)
	if err != nil {
		return err
	}
	if err := db.GetDB().Delete(&model.User{}, id).Error; err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}
	userCache.Del(int(id))
	userNameIndex.Del(user.Username)
	return nil
}

// UserTouchBalance 供计费层原子更新后同步缓存副本; 余额以库内为准覆盖。
func UserTouchBalance(id uint) {
	if _, ok := userCache.Get(int(id)); !ok {
		return
	}
	var fresh model.User
	if err := db.GetDB().First(&fresh, id).Error; err != nil {
		return
	}
	userCache.Set(int(id), fresh)
}

// UserBumpTokenVersion 递增令牌版本, 使签发更早的 JWT 马上失效 (Auth 中间件比对版本)。
// 用于服务端登出等场景。
func UserBumpTokenVersion(id uint) error {
	user, err := UserGetByID(id)
	if err != nil {
		return err
	}
	if err := db.GetDB().Model(&model.User{}).Where("id = ?", id).
		Update("token_version", user.TokenVersion+1).Error; err != nil {
		return fmt.Errorf("failed to bump token version: %w", err)
	}
	user.TokenVersion++
	userCache.Set(int(id), user)
	return nil
}

package op

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/utils/cache"
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
func UserCreate(username, password string, role model.Role, status model.UserStatus) (model.User, error) {
	username = strings.TrimSpace(username)
	if username == "" || len(username) > 64 {
		return model.User{}, fmt.Errorf("invalid username")
	}
	if len(password) < 6 {
		return model.User{}, fmt.Errorf("password must be at least 6 characters")
	}
	if _, ok := userNameIndex.Get(username); ok {
		return model.User{}, fmt.Errorf("username already exists")
	}
	user := model.User{Username: username, Password: password, Role: role, Status: status}
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
	if len(password) < 6 {
		return fmt.Errorf("password must be at least 6 characters")
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

// UserSetBalance 管理员直接调整可用余额; 冻结额与累计口径不动, 预扣判定始终以余额对冻结额为准。
// actor 记入余额流水, 使"谁在什么时候把余额改成了多少"有据可查; 系统内部调用传零值 actor。
func UserSetBalance(id uint, balance float64, actorID uint, actorName, note string) error {
	if balance < 0 {
		return fmt.Errorf("balance cannot be negative")
	}
	user, err := UserGetByID(id)
	if err != nil {
		return err
	}
	delta := balance - user.Balance
	err = db.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.User{}).Where("id = ?", id).Update("balance", balance).Error; err != nil {
			return fmt.Errorf("failed to update balance: %w", err)
		}
		if delta == 0 {
			return nil
		}
		return ledgerRecord(tx, model.BillingLedger{
			UserID:       id,
			Kind:         model.LedgerKindAdjust,
			Amount:       delta,
			BalanceAfter: balance,
			ActorID:      actorID,
			ActorName:    actorName,
			Note:         note,
		})
	})
	if err != nil {
		return err
	}
	user.Balance = balance
	userCache.Set(int(id), user)
	return nil
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
	if len(newPassword) < 6 {
		return fmt.Errorf("password must be at least 6 characters")
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

package op

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
)

// 租户成员关系: user_tenant_mappings 是"谁属于哪个租户、在租户里是什么角色"的权威表。
// 改造前这张表从未被写入: GetUserTenants 恒为空, 停用租户也无人可查,
// 租户成员关系实际上不存在。本文件补上写入面。
//
// 两个必须同时维护的状态:
//   - users.tenant_id  鉴权路径唯一认的租户归属(认证中间件按它判租户状态、构建作用域);
//   - user_tenant_mappings 成员关系(角色、租户成员列表)。
// 只写 mapping 不写 users.tenant_id, 成员关系在鉴权路径上不可见; 只写 users.tenant_id
// 不写 mapping, 成员列表与 GetUserTenants 又是空的。两者必须同一事务落库。
//
// 缓存陷阱: op.UserGetByID 只读内存缓存, 不会回库重建。改完 users.tenant_id 必须
// 调 UserTouchBalance(userID) 让缓存从库刷新 —— 否则认证中间件读到的还是旧 tenant_id,
// 迁租"看起来成功"但请求仍落在旧租户(Lead 写中间件测试时实际踩过)。

const (
	// DefaultTenantID 平台默认租户; users.tenant_id 的默认值, 也是无归属账号的回落值。
	DefaultTenantID uint = 1
	// TenantRoleOwner 租户所有者: 平台 admin 在租户内的对应角色。
	TenantRoleOwner = "owner"
	// TenantRoleAdmin 租户管理员: 平台 reseller(渠道商)在租户内的对应角色。
	TenantRoleAdmin = "admin"
	// TenantRoleMember 普通租户成员: 平台 user 在租户内的对应角色。
	TenantRoleMember = "member"
)

// ErrTenantMembershipNotFound 指定 (user, tenant) 没有成员关系。
var ErrTenantMembershipNotFound = errors.New("tenant membership not found")

// NormalizeTenantRole 归一并校验租户内角色; 空串按 member(与模型默认值一致)。
func NormalizeTenantRole(role string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "", TenantRoleMember:
		return TenantRoleMember, nil
	case TenantRoleAdmin:
		return TenantRoleAdmin, nil
	case TenantRoleOwner:
		return TenantRoleOwner, nil
	}
	return "", fmt.Errorf("invalid tenant role: %s", role)
}

// TenantRoleForUser 平台角色 -> 租户内角色: admin→owner, reseller→admin, 其余→member。
// 回填迁移 046 用的是同一套映射(SQL CASE), 两处必须一致, 否则新老数据角色对不上。
func TenantRoleForUser(role model.Role) string {
	switch role {
	case model.RoleAdmin:
		return TenantRoleOwner
	case model.RoleReseller:
		return TenantRoleAdmin
	}
	return TenantRoleMember
}

// TenantMemberView 租户成员对外视图: 成员关系 + 账号信息, 供管理端成员列表展示。
type TenantMemberView struct {
	UserID    uint             `json:"user_id"`
	Username  string           `json:"username"`
	TenantID  uint             `json:"tenant_id"`
	Role      string           `json:"role"`      // 租户内角色: member/admin/owner
	UserRole  model.Role       `json:"user_role"` // 平台角色, 供对照
	Status    model.UserStatus `json:"status"`
	CreatedAt time.Time        `json:"created_at"`
}

// hasTenantMappingsTable 判断成员关系表是否存在。
// 老库与最小化测试 fixture 可能只建了部分表; 缺表时调用方安全跳过, 而不是把建号打成失败。
func hasTenantMappingsTable(conn *gorm.DB) bool {
	return conn.Migrator().HasTable(&model.UserTenantMapping{})
}

// tenantExistsIn 校验租户存在: 指向不存在的租户等于把账号锁死在 403(认证按租户状态拦截),
// 因此写入前必须拒绝, 而不是留下一个永远进不去的归属。
func tenantExistsIn(conn *gorm.DB, tenantID uint) error {
	if tenantID == 0 {
		return fmt.Errorf("tenant id is required")
	}
	var count int64
	if err := conn.Model(&model.Tenant{}).Where("id = ?", tenantID).Count(&count).Error; err != nil {
		return fmt.Errorf("check tenant %d: %w", tenantID, err)
	}
	if count == 0 {
		return fmt.Errorf("tenant %d does not exist", tenantID)
	}
	return nil
}

// upsertTenantMapping 在事务内落成员关系; 已存在则只在角色变化时更新。
// 幂等的关键: 同 (user, tenant) 重复添加不报错, 也不会产生第二行。
func upsertTenantMapping(tx *gorm.DB, userID, tenantID uint, role string) error {
	var existing model.UserTenantMapping
	err := tx.Where("user_id = ? AND tenant_id = ?", userID, tenantID).First(&existing).Error
	switch {
	case err == nil:
		if existing.Role != role {
			if err := tx.Model(&existing).Update("role", role).Error; err != nil {
				return fmt.Errorf("update tenant membership role: %w", err)
			}
		}
		return nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		mapping := model.UserTenantMapping{UserID: userID, TenantID: tenantID, Role: role}
		if err := tx.Create(&mapping).Error; err != nil {
			return fmt.Errorf("create tenant membership: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("query tenant membership: %w", err)
	}
}

// removeTenantMapping 在事务内删成员关系, 返回是否真的删掉了行。
func removeTenantMapping(tx *gorm.DB, userID, tenantID uint) (bool, error) {
	result := tx.Where("user_id = ? AND tenant_id = ?", userID, tenantID).Delete(&model.UserTenantMapping{})
	if result.Error != nil {
		return false, fmt.Errorf("delete tenant membership: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}

// setUserTenantTx 在事务内改 users.tenant_id; 返回改动前值。
func setUserTenantTx(tx *gorm.DB, userID, tenantID uint) (uint, error) {
	var user model.User
	if err := tx.Select("id, tenant_id").First(&user, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, fmt.Errorf("user not found")
		}
		return 0, fmt.Errorf("load user: %w", err)
	}
	previous := user.TenantID
	if previous == tenantID {
		return previous, nil
	}
	if err := tx.Model(&model.User{}).Where("id = ?", userID).Update("tenant_id", tenantID).Error; err != nil {
		return 0, fmt.Errorf("update user tenant: %w", err)
	}
	return previous, nil
}

// TenantAddMember 把账号加入租户; 幂等, 重复添加只按需更新角色。
//
// 首次添加的同步规则(任务约定): 若账号当前 tenant_id 为 0 或默认租户 1, 说明它还没有
// 真实租户归属(或只是建号时的默认值), 此时把 users.tenant_id 指向目标租户,
// 让成员关系在鉴权路径上立即生效。已有其它租户归属的账号不在此处移动 ——
// 跨租户迁移必须走 TenantMoveUser, 那条路径会清理旧成员关系并保留审计。
func TenantAddMember(userID, tenantID uint, role string) error {
	if userID == 0 {
		return fmt.Errorf("user id is required")
	}
	normalized, err := NormalizeTenantRole(role)
	if err != nil {
		return err
	}
	err = db.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := tenantExistsIn(tx, tenantID); err != nil {
			return err
		}
		var user model.User
		if err := tx.Select("id, tenant_id, role").First(&user, userID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("user not found")
			}
			return fmt.Errorf("load user: %w", err)
		}
		if err := upsertTenantMapping(tx, userID, tenantID, normalized); err != nil {
			return err
		}
		if (user.TenantID == 0 || user.TenantID == DefaultTenantID) && user.TenantID != tenantID {
			if err := tx.Model(&model.User{}).Where("id = ?", userID).Update("tenant_id", tenantID).Error; err != nil {
				return fmt.Errorf("update user tenant: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	// 缓存刷新必须在事务提交后: 回滚时库里的 tenant_id 没变, 提前刷新会让缓存短暂指向目标租户。
	UserTouchBalance(userID)
	return nil
}

// TenantRemoveMember 把账号移出租户; 不存在时返回 ErrTenantMembershipNotFound。
//
// 若被移除的正是账号当前 tenant_id 指向的租户, 必须立刻改指: 否则用户会继续留在
// 一个已经没有成员关系的租户里(旧租户仍能读数据), 或指向一个不存在/被停用的租户而被 403 锁死。
// 回落顺序: 其余成员关系里 tenant_id 最小的那个, 没有则回默认租户 1 ——
// users.tenant_id 列本身不允许指向"没有归属"的空值, 认证中间件也按它判状态。
func TenantRemoveMember(userID, tenantID uint) error {
	if userID == 0 || tenantID == 0 {
		return fmt.Errorf("user id and tenant id are required")
	}
	err := db.GetDB().Transaction(func(tx *gorm.DB) error {
		var user model.User
		if err := tx.Select("id, tenant_id").First(&user, userID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("user not found")
			}
			return fmt.Errorf("load user: %w", err)
		}
		removed, err := removeTenantMapping(tx, userID, tenantID)
		if err != nil {
			return err
		}
		if !removed {
			return ErrTenantMembershipNotFound
		}
		if user.TenantID != tenantID {
			return nil
		}
		var remaining model.UserTenantMapping
		err = tx.Where("user_id = ?", userID).Order("tenant_id ASC").First(&remaining).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if err := tx.Model(&model.User{}).Where("id = ?", userID).Update("tenant_id", DefaultTenantID).Error; err != nil {
				return fmt.Errorf("reset user tenant: %w", err)
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("query remaining tenant memberships: %w", err)
		}
		if err := tx.Model(&model.User{}).Where("id = ?", userID).Update("tenant_id", remaining.TenantID).Error; err != nil {
			return fmt.Errorf("update user tenant: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	UserTouchBalance(userID)
	return nil
}

// TenantListMembers 列出租户成员; 账号信息按库内当前值补齐(不依赖用户缓存是否已加载)。
func TenantListMembers(tenantID uint) ([]TenantMemberView, error) {
	if tenantID == 0 {
		return nil, fmt.Errorf("tenant id is required")
	}
	if err := tenantExistsIn(db.GetDB(), tenantID); err != nil {
		return nil, err
	}
	mappings := []model.UserTenantMapping{}
	if err := db.GetDB().Where("tenant_id = ?", tenantID).Order("user_id ASC").Find(&mappings).Error; err != nil {
		return nil, fmt.Errorf("list tenant members: %w", err)
	}
	views := make([]TenantMemberView, 0, len(mappings))
	for _, mapping := range mappings {
		view := TenantMemberView{
			UserID:    mapping.UserID,
			TenantID:  mapping.TenantID,
			Role:      mapping.Role,
			CreatedAt: mapping.CreatedAt,
		}
		var user model.User
		if err := db.GetDB().Select("id, username, role, status").First(&user, mapping.UserID).Error; err == nil {
			view.Username = user.Username
			view.UserRole = user.Role
			view.Status = user.Status
		}
		views = append(views, view)
	}
	return views, nil
}

// TenantMembershipOf 返回账号唯一的租户成员关系(当前数据模型是单租户)。
// 没有成员关系时返回 ErrTenantMembershipNotFound —— 调用方据此区分"没有归属"与"查询失败"。
func TenantMembershipOf(userID uint) (model.UserTenantMapping, error) {
	if userID == 0 {
		return model.UserTenantMapping{}, fmt.Errorf("user id is required")
	}
	var mapping model.UserTenantMapping
	if err := db.GetDB().Where("user_id = ?", userID).Order("tenant_id ASC").First(&mapping).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.UserTenantMapping{}, ErrTenantMembershipNotFound
		}
		return model.UserTenantMapping{}, fmt.Errorf("load tenant membership: %w", err)
	}
	return mapping, nil
}

// TenantMoveUser 把账号迁到目标租户, 同步维护 users.tenant_id 与成员关系。
//
// 旧成员关系**删除**而不是保留: 模型注释写明当前是单租户简化(users.tenant_id 只有一个值),
// 留着旧 mapping 会让 GetUserTenants 继续把旧租户列给用户、管理端成员列表里也留着已迁走的人,
// 而鉴权路径根本不会再用它 —— 三处口径互不一致。删除是同一事务内的, 回滚不会留下半截状态。
// 迁移动作本身由 handler 写审计(actor、原租户、目标租户), 历史不依赖 mapping 行留存。
func TenantMoveUser(userID, tenantID uint) (previousTenantID uint, err error) {
	if userID == 0 {
		return 0, fmt.Errorf("user id is required")
	}
	if tenantID == 0 {
		return 0, fmt.Errorf("tenant id is required")
	}
	err = db.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := tenantExistsIn(tx, tenantID); err != nil {
			return err
		}
		var user model.User
		if err := tx.Select("id, tenant_id, role").First(&user, userID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("user not found")
			}
			return fmt.Errorf("load user: %w", err)
		}
		previousTenantID = user.TenantID
		role := TenantRoleForUser(user.Role)
		if err := upsertTenantMapping(tx, userID, tenantID, role); err != nil {
			return err
		}
		if err := tx.Where("user_id = ? AND tenant_id <> ?", userID, tenantID).
			Delete(&model.UserTenantMapping{}).Error; err != nil {
			return fmt.Errorf("delete previous tenant membership: %w", err)
		}
		if _, err := setUserTenantTx(tx, userID, tenantID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	// 关键: 迁租改的是 users.tenant_id, 而鉴权只读缓存。不刷新缓存 = 迁租静默不生效。
	UserTouchBalance(userID)
	return previousTenantID, nil
}

package model

// Scope 数据访问作用域: 访问者身份与可见范围的统一判定。
// 归属过滤一律经此判断, 非归属访问按 404 语义处理, 避免暴露资源存在性。
type Scope struct {
	ID       uint
	Role     Role
	TenantID uint // 租户 ID; 0 表示平台级/跨租户(仅管理员可用)
}

// IsAdmin 管理员可见全部资源。
func (s Scope) IsAdmin() bool {
	return s.Role == RoleAdmin
}

// IsPlatform 判断是否为平台级作用域(跨租户管理员)。
func (s Scope) IsPlatform() bool {
	return s.IsAdmin() && s.TenantID == 0
}

// Owns 判断资源是否归属该作用域。
// - 平台管理员: 拥有全部资源
// - 租户管理员/成员: 仅拥有同租户内的资源
func (s Scope) Owns(ownerID uint, resourceTenantID uint) bool {
	if s.IsPlatform() {
		return true
	}
	if s.TenantID == 0 || resourceTenantID == 0 {
		// 无租户信息时退回原有行为(兼容旧数据): 仅管理员或 ID 匹配
		return s.IsAdmin() || s.ID == ownerID
	}
	// 必须同租户且(管理员或归属用户)
	return s.TenantID == resourceTenantID && (s.IsAdmin() || s.ID == ownerID)
}

// OwnsUser 判断是否拥有指定用户资源(便捷方法, 从 User 对象推断租户)。
func (s Scope) OwnsUser(user *User) bool {
	if user == nil {
		return false
	}
	return s.Owns(user.ID, user.TenantID)
}

// OwnsChannel 判断是否拥有指定渠道资源。
func (s Scope) OwnsChannel(channel *Channel) bool {
	if channel == nil {
		return false
	}
	return s.Owns(channel.UserID, channel.TenantID)
}

// OwnsChannelValue 判断是否拥有指定渠道资源(值类型版本)。
func (s Scope) OwnsChannelValue(channel Channel) bool {
	return s.Owns(channel.UserID, channel.TenantID)
}

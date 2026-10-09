package model

// Scope 数据访问作用域: 访问者身份与可见范围的统一判定。
// 归属过滤一律经此判断, 非归属访问按 404 语义处理, 避免暴露资源存在性。
type Scope struct {
	ID   uint
	Role Role
}

// IsAdmin 管理员可见全部资源。
func (s Scope) IsAdmin() bool {
	return s.Role == RoleAdmin
}

// Owns 判断资源是否归属该作用域; 管理员视为拥有全部。
func (s Scope) Owns(ownerID uint) bool {
	return s.IsAdmin() || s.ID == ownerID
}

package op

import (
	"sync"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// tenantStatusCache 是"租户是否可用"的进程内缓存。
// 认证中间件每个请求都要判断租户状态, 直接查库会把租户表变成热表;
// 缓存以 ID 为键, 租户状态变更时由 TenantStatusInvalidate 立即失效, 不必等 TTL 过期。
type tenantStatusCacheEntry struct {
	status    model.TenantStatus
	expiresAt time.Time
}

var (
	tenantStatusMu    sync.RWMutex
	tenantStatusCache = make(map[uint]tenantStatusCacheEntry)
	// tenantStatusTTL 是兜底过期时间: 多副本部署时一个副本改了状态, 其它副本靠它最终一致。
	tenantStatusTTL = 30 * time.Second
)

// TenantStatusInvalidate 清除指定租户的状态缓存; 租户写操作(改状态/删除)后必须调用。
func TenantStatusInvalidate(tenantID uint) {
	tenantStatusMu.Lock()
	delete(tenantStatusCache, tenantID)
	tenantStatusMu.Unlock()
}

// TenantStatusInvalidateAll 清空全部租户状态缓存。
func TenantStatusInvalidateAll() {
	tenantStatusMu.Lock()
	tenantStatusCache = make(map[uint]tenantStatusCacheEntry)
	tenantStatusMu.Unlock()
}

// TenantStatusOf 返回租户当前状态; 缓存命中时不查库。
// 租户不存在视为禁用: 宁可拒绝也不能让已被删除的租户继续通行。
func TenantStatusOf(tenantID uint) (model.TenantStatus, error) {
	if tenantID == 0 {
		// 平台级作用域(跨租户管理员)不属于任何租户, 恒可用。
		return model.TenantStatusActive, nil
	}
	now := time.Now()
	tenantStatusMu.RLock()
	entry, ok := tenantStatusCache[tenantID]
	tenantStatusMu.RUnlock()
	if ok && now.Before(entry.expiresAt) {
		return entry.status, nil
	}

	var tenant model.Tenant
	err := db.GetDB().Select("id, status").First(&tenant, tenantID).Error
	if err != nil {
		// 查不到按禁用处理, 同时写入短缓存避免每次请求都打库。
		tenantStatusMu.Lock()
		tenantStatusCache[tenantID] = tenantStatusCacheEntry{status: model.TenantStatusDisabled, expiresAt: now.Add(tenantStatusTTL)}
		tenantStatusMu.Unlock()
		return model.TenantStatusDisabled, nil
	}
	tenantStatusMu.Lock()
	tenantStatusCache[tenantID] = tenantStatusCacheEntry{status: tenant.Status, expiresAt: now.Add(tenantStatusTTL)}
	tenantStatusMu.Unlock()
	return tenant.Status, nil
}

// TenantUsable 判断租户当前是否允许放行流量: 只有 active 可用。
// pending(待审批)与 disabled(禁用)都必须拦截 —— 前者是尚未开通, 后者是被停服。
func TenantUsable(tenantID uint) (bool, error) {
	status, err := TenantStatusOf(tenantID)
	if err != nil {
		return false, err
	}
	return status == model.TenantStatusActive, nil
}

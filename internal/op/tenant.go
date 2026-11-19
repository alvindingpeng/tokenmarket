package op

import (
	"errors"
	"fmt"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
)

// TenantList 返回所有租户
func TenantList() ([]model.Tenant, error) {
	var tenants []model.Tenant
	if err := db.GetDB().Order("id ASC").Find(&tenants).Error; err != nil {
		return nil, err
	}
	return tenants, nil
}

// TenantGet 根据 ID 获取租户
func TenantGet(id int) (model.Tenant, error) {
	var tenant model.Tenant
	if err := db.GetDB().First(&tenant, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.Tenant{}, fmt.Errorf("tenant not found")
		}
		return model.Tenant{}, err
	}
	return tenant, nil
}

// TenantCreate 创建新租户
func TenantCreate(name, code, status string) (model.Tenant, error) {
	if status == "" {
		status = string(model.TenantStatusActive)
	}
	tenant := model.Tenant{
		Name:   name,
		Code:   code,
		Status: model.TenantStatus(status),
	}
	if err := db.GetDB().Create(&tenant).Error; err != nil {
		return model.Tenant{}, fmt.Errorf("failed to create tenant: %w", err)
	}
	TenantStatusInvalidate(tenant.ID)
	// 创建默认配额
	quota := model.TenantQuota{
		TenantID:          tenant.ID,
		RequestsLimit:     0,
		InputTokensLimit:  0,
		OutputTokensLimit: 0,
		Period:            "monthly",
		ResetDay:          1,
	}
	if err := db.GetDB().Create(&quota).Error; err != nil {
		return model.Tenant{}, fmt.Errorf("failed to create tenant quota: %w", err)
	}
	// 创建默认钱包
	wallet := model.TenantWallet{
		TenantID: tenant.ID,
		Balance:  0,
		Frozen:   0,
	}
	if err := db.GetDB().Create(&wallet).Error; err != nil {
		return model.Tenant{}, fmt.Errorf("failed to create tenant wallet: %w", err)
	}
	// 创建默认定价策略
	policy := model.TenantPricingPolicy{
		TenantID:    tenant.ID,
		ChannelID:   nil,
		ModelName:   "",
		MarkupRatio: -1,
		Currency:    "",
	}
	if err := db.GetDB().Create(&policy).Error; err != nil {
		return model.Tenant{}, fmt.Errorf("failed to create tenant pricing policy: %w", err)
	}
	return tenant, nil
}

// TenantUpdate 更新租户
func TenantUpdate(id int, name, code, status string) (model.Tenant, error) {
	tenant, err := TenantGet(id)
	if err != nil {
		return model.Tenant{}, err
	}
	if name != "" {
		tenant.Name = name
	}
	if code != "" {
		tenant.Code = code
	}
	if status != "" {
		tenant.Status = model.TenantStatus(status)
	}
	if err := db.GetDB().Save(&tenant).Error; err != nil {
		return model.Tenant{}, fmt.Errorf("failed to update tenant: %w", err)
	}
	// 状态缓存立即失效: 改状态是要即刻生效的运维动作, 等 TTL 过期意味着
	// 被禁用的租户还能继续跑最长 30 秒的流量。
	TenantStatusInvalidate(uint(id))
	return tenant, nil
}

// TenantDelete 删除租户
func TenantDelete(id int) error {
	if id == 1 {
		return fmt.Errorf("cannot delete default tenant")
	}
	// 检查是否有关联用户
	var count int64
	if err := db.GetDB().Table("users").Where("tenant_id = ?", id).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("tenant has associated users, cannot delete")
	}
	// 级联删除相关数据
	err := db.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id = ?", id).Delete(&model.TenantQuota{}).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id = ?", id).Delete(&model.TenantWallet{}).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id = ?", id).Delete(&model.TenantPricingPolicy{}).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id = ?", id).Delete(&model.UserTenantMapping{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&model.Tenant{}, id).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	TenantStatusInvalidate(uint(id))
	return nil
}

// TenantQuotaGet 获取租户配额
func TenantQuotaGet(tenantID int) (model.TenantQuota, error) {
	var quota model.TenantQuota
	if err := db.GetDB().First(&quota, "tenant_id = ?", tenantID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 如果不存在，创建默认配额
			quota = model.TenantQuota{
				TenantID:          uint(tenantID),
				RequestsLimit:     0,
				InputTokensLimit:  0,
				OutputTokensLimit: 0,
				Period:            "monthly",
				ResetDay:          1,
			}
			if err := db.GetDB().Create(&quota).Error; err != nil {
				return model.TenantQuota{}, err
			}
			return quota, nil
		}
		return model.TenantQuota{}, err
	}
	return quota, nil
}

// TenantQuotaUpdate 更新租户配额
func TenantQuotaUpdate(tenantID int, requestsLimit, inputTokensLimit, outputTokensLimit int64, period string, resetDay int) (model.TenantQuota, error) {
	quota, err := TenantQuotaGet(tenantID)
	if err != nil {
		return model.TenantQuota{}, err
	}
	quota.RequestsLimit = requestsLimit
	quota.InputTokensLimit = inputTokensLimit
	quota.OutputTokensLimit = outputTokensLimit
	quota.Period = period
	quota.ResetDay = resetDay
	if err := db.GetDB().Save(&quota).Error; err != nil {
		return model.TenantQuota{}, fmt.Errorf("failed to update tenant quota: %w", err)
	}
	return quota, nil
}

// TenantWalletGet 获取租户钱包
func TenantWalletGet(tenantID int) (model.TenantWallet, error) {
	var wallet model.TenantWallet
	if err := db.GetDB().First(&wallet, "tenant_id = ?", tenantID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			wallet = model.TenantWallet{
				TenantID: uint(tenantID),
				Balance:  0,
				Frozen:   0,
			}
			if err := db.GetDB().Create(&wallet).Error; err != nil {
				return model.TenantWallet{}, err
			}
			return wallet, nil
		}
		return model.TenantWallet{}, err
	}
	return wallet, nil
}

// GetUserTenants 获取用户关联的租户列表
func GetUserTenants(userID uint) ([]model.Tenant, error) {
	var mappings []model.UserTenantMapping
	if err := db.GetDB().Where("user_id = ?", userID).Find(&mappings).Error; err != nil {
		return nil, err
	}
	if len(mappings) == 0 {
		return []model.Tenant{}, nil
	}
	tenantIDs := make([]uint, len(mappings))
	for i, m := range mappings {
		tenantIDs[i] = m.TenantID
	}
	var tenants []model.Tenant
	if err := db.GetDB().Where("id IN ?", tenantIDs).Find(&tenants).Error; err != nil {
		return nil, err
	}
	return tenants, nil
}

// GetUserDefaultTenant 获取用户的默认租户（第一个关联租户或默认租户1）
func GetUserDefaultTenant(userID uint) (*model.Tenant, error) {
	tenants, err := GetUserTenants(userID)
	if err != nil {
		return nil, err
	}
	if len(tenants) > 0 {
		return &tenants[0], nil
	}
	// 回落到默认租户
	tenant, err := TenantGet(1)
	if err != nil {
		return nil, err
	}
	return &tenant, nil
}

// TenantPricingPolicyList 列出租户定价策略
func TenantPricingPolicyList(tenantID int) ([]model.TenantPricingPolicy, error) {
	var policies []model.TenantPricingPolicy
	if err := db.GetDB().Where("tenant_id = ?", tenantID).Order("id ASC").Find(&policies).Error; err != nil {
		return nil, err
	}
	return policies, nil
}

// TenantPricingPolicyCreate 创建租户定价策略
func TenantPricingPolicyCreate(tenantID int, channelID *int, modelName string, markupRatio float64, currency string) (model.TenantPricingPolicy, error) {
	policy := model.TenantPricingPolicy{
		TenantID:    uint(tenantID),
		ChannelID:   channelID,
		ModelName:   modelName,
		MarkupRatio: markupRatio,
		Currency:    currency,
	}
	if err := db.GetDB().Create(&policy).Error; err != nil {
		return model.TenantPricingPolicy{}, fmt.Errorf("failed to create tenant pricing policy: %w", err)
	}
	return policy, nil
}

// TenantPricingPolicyUpdate 更新租户定价策略
func TenantPricingPolicyUpdate(policyID int, channelID *int, modelName string, markupRatio float64, currency string) (model.TenantPricingPolicy, error) {
	var policy model.TenantPricingPolicy
	if err := db.GetDB().First(&policy, policyID).Error; err != nil {
		return model.TenantPricingPolicy{}, fmt.Errorf("pricing policy not found")
	}
	if channelID != nil {
		policy.ChannelID = channelID
	}
	if modelName != "" {
		policy.ModelName = modelName
	}
	if markupRatio >= 0 {
		policy.MarkupRatio = markupRatio
	}
	if currency != "" {
		policy.Currency = currency
	}
	if err := db.GetDB().Save(&policy).Error; err != nil {
		return model.TenantPricingPolicy{}, fmt.Errorf("failed to update tenant pricing policy: %w", err)
	}
	return policy, nil
}

// TenantPricingPolicyBelongsTo 判断策略是否属于指定租户。
// 路由是 /tenant/:id/pricing/:pid, 校验归属才能让 :id 成为真正的权限边界 ——
// 否则任何管理员都能拿别的租户的 pid 改或删别人的定价策略。
func TenantPricingPolicyBelongsTo(policyID, tenantID int) bool {
	var count int64
	if err := db.GetDB().Model(&model.TenantPricingPolicy{}).
		Where("id = ? AND tenant_id = ?", policyID, tenantID).Count(&count).Error; err != nil {
		return false
	}
	return count > 0
}

// TenantPricingPolicyDelete 删除租户定价策略
func TenantPricingPolicyDelete(policyID int) error {
	if err := db.GetDB().Delete(&model.TenantPricingPolicy{}, policyID).Error; err != nil {
		return fmt.Errorf("failed to delete tenant pricing policy: %w", err)
	}
	return nil
}

// GetTenantPricingPolicy 获取租户特定模型/渠道的定价策略
// 优先级: 租户+渠道+模型 > 租户+渠道 > 租户+模型 > 租户默认
func GetTenantPricingPolicy(tenantID uint, channelID *int, modelName string) (model.TenantPricingPolicy, bool) {
	var policies []model.TenantPricingPolicy
	db.GetDB().Where("tenant_id = ?", tenantID).Find(&policies)

	// 精确匹配: 租户+渠道+模型
	for _, p := range policies {
		if p.ChannelID != nil && channelID != nil && *p.ChannelID == *channelID && p.ModelName == modelName {
			return p, true
		}
	}
	// 租户+渠道
	for _, p := range policies {
		if p.ChannelID != nil && channelID != nil && *p.ChannelID == *channelID && p.ModelName == "" {
			return p, true
		}
	}
	// 租户+模型
	for _, p := range policies {
		if (p.ChannelID == nil || channelID == nil) && p.ModelName == modelName {
			return p, true
		}
	}
	// 租户默认
	for _, p := range policies {
		if (p.ChannelID == nil || channelID == nil) && p.ModelName == "" {
			return p, true
		}
	}
	return model.TenantPricingPolicy{}, false
}

package op

import (
	"fmt"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// UserTierPolicies 返回当前生效的等级配额表。
// 设置缺失或非法时回落到出厂默认: 宁可给出保守额度的默认值, 也不能让"配置写坏了"
// 变成"全平台无限制"。
func UserTierPolicies() map[model.UserTier]model.TierLimits {
	raw, err := SettingGetString(model.SettingKeyUserTierPolicies)
	if err != nil {
		return model.DefaultUserTierPolicies
	}
	policies, err := model.ParseUserTierPolicies(raw)
	if err != nil || len(policies) == 0 {
		return model.DefaultUserTierPolicies
	}
	return policies
}

// UserTierLimits 返回某等级生效的限流额度; 未配置的等级视为不限制。
func UserTierLimits(tier model.UserTier) model.TierLimits {
	if !model.IsValidUserTier(tier) {
		tier = model.TierFree
	}
	return UserTierPolicies()[tier]
}

// UserTierLimitsForUser 按用户当前等级取兜底额度; 用户不存在时返回未限制,
// 由调用方既有的鉴权失败路径处理, 不在限流层制造第二个错误源。
func UserTierLimitsForUser(userID uint) (model.TierLimits, bool) {
	user, err := UserGetByID(userID)
	if err != nil {
		return model.TierLimits{}, false
	}
	return UserTierLimits(user.EffectiveTier()), true
}

// UserUpdateTier 修改账号等级; 空串表示清空等级(回落到 free)。
func UserUpdateTier(id uint, tier model.UserTier) error {
	if tier != "" && !model.IsValidUserTier(tier) {
		return fmt.Errorf("invalid user tier")
	}
	user, err := UserGetByID(id)
	if err != nil {
		return err
	}
	if err := db.GetDB().Model(&model.User{}).Where("id = ?", id).Update("tier", tier).Error; err != nil {
		return fmt.Errorf("failed to update tier: %w", err)
	}
	user.Tier = tier
	userCache.Set(int(id), user)
	return nil
}

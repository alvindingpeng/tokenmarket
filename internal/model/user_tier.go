package model

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// UserTier 用户服务等级: 决定限流兜底额度, 不改变角色权限。
// 与 Role 正交 —— 管理员可以同时是 enterprise 用户; 渠道商也可以是 free 等级。
type UserTier string

const (
	TierFree       UserTier = "free"
	TierBasic      UserTier = "basic"
	TierPro        UserTier = "pro"
	TierEnterprise UserTier = "enterprise"
)

// UserTiers 全部合法等级, 顺序即界面展示顺序(由低到高)。
var UserTiers = []UserTier{TierFree, TierBasic, TierPro, TierEnterprise}

// IsValidUserTier 判断等级标识是否受支持; 空串视为未设置, 不是合法等级。
func IsValidUserTier(tier UserTier) bool {
	for _, candidate := range UserTiers {
		if tier == candidate {
			return true
		}
	}
	return false
}

// TierLimits 单个等级的限流额度; 0 表示该维度不限制。
type TierLimits struct {
	RPM        int64 `json:"rpm"`
	TPM        int64 `json:"tpm"`
	Concurrent int64 `json:"concurrent"`
}

// Zero 判断该等级是否等于"完全不限制", 供调用方跳过无意义的兜底。
func (l TierLimits) Zero() bool {
	return l.RPM == 0 && l.TPM == 0 && l.Concurrent == 0
}

// DefaultUserTierPolicies 出厂默认配额: free 只够试用量, enterprise 不限制。
// 默认值写死而不是留空, 是为了让"新建用户没配等级"也有可解释的额度,
// 而不是要么无限、要么全拒。
var DefaultUserTierPolicies = map[UserTier]TierLimits{
	TierFree:       {RPM: 20, TPM: 40000, Concurrent: 2},
	TierBasic:      {RPM: 120, TPM: 400000, Concurrent: 8},
	TierPro:        {RPM: 600, TPM: 2000000, Concurrent: 32},
	TierEnterprise: {RPM: 0, TPM: 0, Concurrent: 0},
}

// DefaultUserTierPoliciesJSON 默认配额的 JSON 形态, 用于设置键播种。
var DefaultUserTierPoliciesJSON = mustMarshalTierPolicies(DefaultUserTierPolicies)

func mustMarshalTierPolicies(policies map[UserTier]TierLimits) string {
	encoded, err := json.Marshal(tierPoliciesToWire(policies))
	if err != nil {
		// 常量数据序列化失败属于编码期错误, 让启动直接失败比静默给空额度更安全。
		panic(fmt.Sprintf("marshal default user tier policies: %v", err))
	}
	return string(encoded)
}

// tierPoliciesToWire 转成以字符串为键的 map, 便于序列化与跨语言消费。
func tierPoliciesToWire(policies map[UserTier]TierLimits) map[string]TierLimits {
	wire := make(map[string]TierLimits, len(policies))
	for tier, limits := range policies {
		wire[string(tier)] = limits
	}
	return wire
}

// ParseUserTierPolicies 解析设置里的配额 JSON。
// 规则: 允许只写部分等级(缺的等级视为未配置), 但键必须是合法等级,
// 数值必须非负 —— 负数没有语义, 静默截断成 0 会让"限得很死"看起来像"没限"。
func ParseUserTierPolicies(raw string) (map[UserTier]TierLimits, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return map[UserTier]TierLimits{}, nil
	}
	var wire map[string]TierLimits
	if err := json.Unmarshal([]byte(trimmed), &wire); err != nil {
		return nil, fmt.Errorf("user tier policies must be a JSON object: %w", err)
	}
	policies := make(map[UserTier]TierLimits, len(wire))
	for key, limits := range wire {
		tier := UserTier(strings.TrimSpace(strings.ToLower(key)))
		if !IsValidUserTier(tier) {
			return nil, fmt.Errorf("unknown user tier %q; valid tiers: %s", key, strings.Join(tierNames(), ", "))
		}
		if limits.RPM < 0 || limits.TPM < 0 || limits.Concurrent < 0 {
			return nil, fmt.Errorf("tier %s limits must be non-negative", tier)
		}
		policies[tier] = limits
	}
	return policies, nil
}

// tierNames 合法等级名列表, 仅用于错误信息。
func tierNames() []string {
	names := make([]string, 0, len(UserTiers))
	for _, tier := range UserTiers {
		names = append(names, string(tier))
	}
	sort.Strings(names)
	return names
}

package model

// QuotaUsage 用户维度的限流配额与当前窗口用量。
//
// 为什么要把"生效来源"一起返回: 最终额度可能来自管理员为该用户单独配的限流策略,
// 也可能来自等级兜底。两者数值相同但含义完全不同 —— 只看数字, 管理员改了等级却没变化
// (或改了策略却没变化)时无从判断到底哪个在生效, 只能猜。
type QuotaUsage struct {
	UserID   uint     `json:"user_id"`
	Username string   `json:"username"`
	Tier     UserTier `json:"tier"` // 生效等级; 未设置时为 free。

	// Limits 本窗口生效的额度; 0 表示该维度不限制。
	RPM        int64 `json:"rpm"`
	TPM        int64 `json:"tpm"`
	Concurrent int64 `json:"concurrent"`

	// Source 额度来源: "policy"(显式限流策略) / "tier"(等级兜底) / "none"(不限制)。
	Source string `json:"source"`
	// Model 额度所针对的模型名; 空串表示全模型共享额度。
	Model string `json:"model"`

	// 本窗口已用量(RPM/TPM 按分钟窗口滚动, 与限流器同一口径)。
	UsedRPM  int64 `json:"used_rpm"`
	UsedTPM  int64 `json:"used_tpm"`
	InFlight int64 `json:"in_flight"` // 当前并发占用。

	// WindowResetAt 当前窗口刷新时间(RFC3339); 用量在该时刻归零。
	WindowResetAt string `json:"window_reset_at"`
}

// QuotaSource* 额度来源标识。
const (
	QuotaSourcePolicy = "policy"
	QuotaSourceTier   = "tier"
	QuotaSourceNone   = "none"
)

// QuotaRemain 返回三个维度的剩余量; 0 表示不限制的维度也返回 0。
// 单独提供是为了让前端不必重复实现"不限制"的判定。
func (q QuotaUsage) QuotaRemain() (rpm, tpm, concurrent int64) {
	return remainOrZero(q.RPM, q.UsedRPM), remainOrZero(q.TPM, q.UsedTPM), remainOrZero(q.Concurrent, q.InFlight)
}

// remainOrZero 额度为 0(不限制)时返回 0, 否则返回 max(limit-used, 0)。
// 不返回负数: 并发可能因租约释放与预扣顺序短暂超过额度, 负数没有意义且会显示成怪值。
func remainOrZero(limit, used int64) int64 {
	if limit <= 0 {
		return 0
	}
	remain := limit - used
	if remain < 0 {
		return 0
	}
	return remain
}

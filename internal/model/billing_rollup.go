package model

import "time"

// BillingRollup 计费明细的按日汇总快照, 用于把"永久聚合"与"原始明细留存"解耦。
//
// 为什么必须有这张表: billing_records 是逐笔明细, 只增不减; 而若干读口要对**全历史**求和 ——
// 用户累计消费(BillingTotal)、提现成熟收益(maturedRevenueIn)、平台全期毛利(ProfitMarginGet)。
// 直接对明细做 180 天留存会让这些终身口径随删除一起缩水: 提现可提额凭空变少, 看板收入比实际少,
// 那是钱算错, 不是显示问题。
//
// 做法: 删除某一天的明细**之前**, 先把那一天的聚合值固化成本表的一行(整日整桶固化, 不做半日切分,
// 否则重跑会对不上), 之后删除明细。终身口径查询 = 本表 + 明细表并集。
//
// 为什么按天存而不是汇总成一行全局数: 若干读口要按时间窗过滤(如提现成熟收益 created_at <= cutoff),
// 只有带日期才能继续支持这个语义。粒度是"天 + 维度", 规模远小于明细(日均明细笔数比它高 3 个量级)。
//
// 为什么带 refunded_* 一组列: 不同读口对退款行的口径不同 —— BillingTotal 计全部, maturedRevenueIn
// 与渠道利润只计未退款部分。只存一份合计会让其中一个读口算错; 存两组即可用"全部 - 退款"还原两者。
type BillingRollup struct {
	ID uint64 `json:"id" gorm:"primaryKey"`
	// Dimension 维度: user(付费方) / owner(收入方) / channel(渠道) / apikey(访问密钥) / platform(全平台)。
	// 五个维度各自成行, 而不是一行带五个 id: 各维度的聚合列含义不同, 混在一行说不清"这行是谁的和"。
	Dimension   string `json:"dimension" gorm:"size:8;not null;uniqueIndex:idx_billing_rollup_key,priority:1"`
	DimensionID uint   `json:"dimension_id" gorm:"not null;uniqueIndex:idx_billing_rollup_key,priority:2;index:idx_billing_rollup_dim_day,priority:1"`
	// Day 自然日 YYYYMMDD(本地挂钟), 与 BillingDaily 的桶口径完全一致。
	Day string `json:"day" gorm:"size:16;not null;uniqueIndex:idx_billing_rollup_key,priority:3;index:idx_billing_rollup_dim_day,priority:2"`

	InputToken      int64   `json:"input_token" gorm:"bigint"`
	OutputToken     int64   `json:"output_token" gorm:"bigint"`
	CacheReadToken  int64   `json:"cache_read_token" gorm:"bigint"`
	CacheWriteToken int64   `json:"cache_write_token" gorm:"bigint"`
	RequestCount    int64   `json:"request_count" gorm:"bigint"`
	UserCost        float64 `json:"user_cost" gorm:"type:decimal(18,6)"`
	OwnerRevenue    float64 `json:"owner_revenue" gorm:"type:decimal(18,6)"`
	PlatformRevenue float64 `json:"platform_revenue" gorm:"type:decimal(18,6)"`

	// 其中状态为 refunded 的部分; 用"合计 - 本组"即可还原"只计 settled"的口径。
	RefundedRecords         int64   `json:"refunded_records" gorm:"bigint"`
	RefundedUserCost        float64 `json:"refunded_user_cost" gorm:"type:decimal(18,6)"`
	RefundedOwnerRevenue    float64 `json:"refunded_owner_revenue" gorm:"type:decimal(18,6)"`
	RefundedPlatformRevenue float64 `json:"refunded_platform_revenue" gorm:"type:decimal(18,6)"`

	// SourceRecords 固化这一行时明细表里有多少笔, 供运维核对汇总是否漏算。
	SourceRecords int64     `json:"source_records"`
	SealedAt      time.Time `json:"sealed_at"`
}

// TableName 指定表名。
func (BillingRollup) TableName() string { return "billing_rollups" }

// 汇总维度常量。写成常量而不是字面量: 汇总表与查询两侧都要用, 拼错不会报错, 只会静默少数据。
const (
	BillingRollupUser     = "user"     // 付费方维度, 对应 billing_records.user_id。
	BillingRollupOwner    = "owner"    // 收入方维度, 对应 billing_records.owner_id。
	BillingRollupChannel  = "channel"  // 渠道维度, 对应 billing_records.channel_id。
	BillingRollupAPIKey   = "apikey"   // API Key 维度, 对应 billing_records.api_key_id。
	BillingRollupPlatform = "platform" // 全平台维度, DimensionID 恒为 0。
)

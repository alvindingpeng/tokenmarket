package model

// 模型广场(P1)的只读挂牌视图。这些形状只描述供给事实(谁在卖什么、多少钱、能不能用),
// 不描述观测(成功率/延迟): 后者来自 relay 的进程内指标, 而 internal/op 不 import internal/relay,
// 故由处理器在 handlers 侧合并补齐, 模型包不引入对 relay 的依赖。

// MarketChannel 是广场上的一行"渠道 × 模型"挂牌。
//
// 以「渠道授权」而非「渠道模型」为最小单元的理由: group_items 只认 channel_grant_id
// (validateGroupItemAccess 也按授权校验), 广场若按渠道模型展示, 点击"加入分组"时还要再翻译一次;
// 直接按授权列出, 点击即可提交。
//
// 字段集刻意是 ChannelGrantCandidate 的子集: 不含上游渠道真名、凭据名、渠道归属用户,
// 因此广场比分组页的候选列表暴露面更小而不是更大。
type MarketChannel struct {
	ChannelGrantID int        `json:"channel_grant_id"`         // 加入分组时提交的就是它。
	ChannelModelID int        `json:"channel_model_id"`         // 供处理器合并 relay 指标; 不是展示字段。
	ChannelCode    string     `json:"channel_code"`             // 渠道发布编码, 形如 "#1001"。
	Available      bool       `json:"available"`                // 当前能否转发(含牌价可用与三侧均未熔断)。
	StatusReason   string     `json:"status_reason,omitempty"`  // 不可用/需提示的原因: disabled|key_disabled|breaker|fx_missing|unpriced。
	UserPrice      LLMPrice   `json:"user_price"`               // 用户价(已折算为上浮后的账面币种), 与选路/计费同口径。
	UserMedia      MediaPrice `json:"user_media,omitempty"`     // 媒体用户价; 文本模型为零值。
	Kind           MediaKind  `json:"kind,omitempty"`           // 模型媒体形态, 文本留空。
	PriceCurrency  string     `json:"price_currency,omitempty"` // 供货报价币种; 非 USD 表示价格已折算。
	FxMissing      bool       `json:"fx_missing,omitempty"`     // 声明了非 USD 报价但牌价不可用: 价格未折算, 已判不可用。
	Unpriced       bool       `json:"unpriced,omitempty"`       // 文本价与媒体价全为零: 界面显示"未定价"而非 0 元。
}

// MarketModel 是按小写模型名折叠后的一屏一行: 同名跨渠道的多条挂牌归在它下面。
type MarketModel struct {
	Name     string          `json:"name"`      // 展示名: 同名多种写法时取出现次数最多的原写法。
	Brand    string          `json:"brand"`     // 品牌键(见 op.MarketBrandOf); 解析不出为 "other"。
	Channels []MarketChannel `json:"channels"`  // 定序: 可用优先 → 用户价升 → 编码升。
	InGroups []int           `json:"in_groups"` // 我已把它加进哪些自有分组(供"已加入"角标与重复提示)。
}

// MarketChannelStatus 是 StatusReason 的取值集合, 供前后端共用同一份字符串常量。
// 优先级(多因并存时取靠前者, 与设计稿 §3.5 的徽章优先级表一致):
// fx_missing > disabled > key_disabled > breaker > unpriced。空串表示无异常。
const (
	MarketStatusFxMissing   = "fx_missing"   // 牌价缺失, 价格未折算。
	MarketStatusDisabled    = "disabled"     // 渠道被停用(含渠道级额度暂停)。
	MarketStatusKeyDisabled = "key_disabled" // 凭据被停用。
	MarketStatusBreaker     = "breaker"      // 渠道/凭据/渠道模型处于熔断窗口内。
	MarketStatusUnpriced    = "unpriced"     // 报价全零: 不是错误, 但需在价格位显示"未定价"。
)

// 品牌键集合: 广场的筛选维度与图标选择的共用标识。
// 这里列的是"能解析出确切厂商"的键; 解析不出时统一给 MarketBrandOther, 界面显示"其他"。
// 新增品牌时同时补: 本常量、internal/op/market_brand.go 的两张对照表、
// 前端 web/src/lib/model-icons.tsx 的品牌键→图标映射与三语 market.brand.* 文案。
const (
	MarketBrandAnthropic   = "anthropic"
	MarketBrandOpenAI      = "openai"
	MarketBrandGoogle      = "google"
	MarketBrandDeepSeek    = "deepseek"
	MarketBrandXAI         = "xai"
	MarketBrandAlibaba     = "alibaba"
	MarketBrandZhipu       = "zhipu"
	MarketBrandMiniMax     = "minimax"
	MarketBrandMoonshot    = "moonshot"
	MarketBrandMistral     = "mistral"
	MarketBrandMeta        = "meta"
	MarketBrandXiaomi      = "xiaomi"
	MarketBrandByteDance   = "bytedance"
	MarketBrandYi          = "yi"
	MarketBrandTencent     = "tencent"
	MarketBrandIflytek     = "iflytek"
	MarketBrandBaidu       = "baidu"
	MarketBrandInternLM    = "internlm"
	MarketBrandStepFun     = "stepfun"
	MarketBrandNVIDIA      = "nvidia"
	MarketBrandCohere      = "cohere"
	MarketBrandPerplexity  = "perplexity"
	MarketBrandMicrosoft   = "microsoft"
	MarketBrandKuaishou    = "kuaishou"
	MarketBrandAmazon      = "amazon"
	MarketBrandVercel      = "vercel"
	MarketBrandBlackForest = "blackforest"
	MarketBrandRunway      = "runway"
	MarketBrandRecraft     = "recraft"
	MarketBrandIdeogram    = "ideogram"
	MarketBrandElevenLabs  = "elevenlabs"
	MarketBrandOther       = "other"
)

// PriceBlended 返回四类单价的混合价, 用于排序与"最低价"展示: (input+output)/2。
// 口径与选路的 PriceMetricBlended 一致(见 Group 的 price_metric), 否则广场排出来的"最便宜"
// 与价格优先选路挑中的成员会是两个。
func (m MarketChannel) PriceBlended() float64 {
	return (m.UserPrice.Input + m.UserPrice.Output) / 2
}

// MediaBlended 返回媒体单价的排序用代表值: 每张 > 每秒 > 每条, 取第一个非零项。
// 媒体模型没有 token 单价, 若不参与排序就会永远沉在价格排序的末尾。
func (m MarketChannel) MediaBlended() float64 {
	switch {
	case m.UserMedia.PerImage != 0:
		return m.UserMedia.PerImage
	case m.UserMedia.PerSecond != 0:
		return m.UserMedia.PerSecond
	default:
		return m.UserMedia.PerVideo
	}
}

// SortPrice 返回参与价格排序的代表单价: 文本模型取混合 token 价, 媒体模型取媒体价。
func (m MarketChannel) SortPrice() float64 {
	if price := m.PriceBlended(); price != 0 {
		return price
	}
	return m.MediaBlended()
}

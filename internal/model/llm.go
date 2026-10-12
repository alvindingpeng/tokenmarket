package model

type LLMPrice struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
}

// Cost 按四类单价(每百万 token)与用量计算费用。
// 用量口径: read 为未命中缓存的输入, cacheRead/cacheWrite 为缓存读写量。
func (p LLMPrice) Cost(read, output, cacheRead, cacheWrite int64) float64 {
	return (float64(read)*p.Input + float64(output)*p.Output +
		float64(cacheRead)*p.CacheRead + float64(cacheWrite)*p.CacheWrite) / 1_000_000
}

// Scale 按比例缩放四类单价, ratio=0.2 表示上浮 20%; 用户价 = 供货价 × (1 + ratio)。
func (p LLMPrice) Scale(ratio float64) LLMPrice {
	factor := 1 + ratio
	return LLMPrice{
		Input:      p.Input * factor,
		Output:     p.Output * factor,
		CacheRead:  p.CacheRead * factor,
		CacheWrite: p.CacheWrite * factor,
	}
}

// MediaKind 标记模型的媒体形态; 文本模型留空按 text 处理。
// 新加的 image 是 P1 生图协议的载体, video 是 P2 的占位。
type MediaKind string

const (
	MediaKindText  MediaKind = ""
	MediaKindImage MediaKind = "image"
	MediaKindVideo MediaKind = "video"
)

// MediaPrice 媒体模型的定价: 按输出单位计价, 不入 token 体系。
// JSON 与 LLMPrice 同名但字段完全不同, 共享同一个 serde 的原因是历史 BillingRecord.SupplyPrice
// 字段必须保持为 token 形状以便恢复; MediaPrice 改成独立列, 旧行依旧可读。
// 三个计价维度各自独立, 按媒体形态取用, 互不叠加:
//   - PerImage  每张: 生图单价, 一次请求出 n 张即 n 倍。
//   - PerVideo  每条: 生视频的固定起步价, 与时长无关。
//   - PerSecond 每秒: 生视频的时长单价, 与秒数相乘。
//
// Resolution 是可选的按档定价(480p / 720p / 1080p 等), 命中时覆盖 PerImage 或 PerSecond 的单价;
// 每条的固定起步价与分辨率无关, 不参与分档。
type MediaPrice struct {
	PerImage   float64            `json:"per_image,omitempty"`
	PerVideo   float64            `json:"per_video,omitempty"`
	PerSecond  float64            `json:"per_second,omitempty"`
	Resolution map[string]float64 `json:"resolution,omitempty"`
}

// CostImages 按张数与(可选)分档计算输出费用; resolution 为空时使用 PerImage。
func (p MediaPrice) CostImages(images int64, resolution string) float64 {
	if images <= 0 {
		return 0
	}
	unit := p.PerImage
	if resolution != "" {
		if v, ok := p.Resolution[resolution]; ok && v > 0 {
			unit = v
		}
	}
	return float64(images) * unit
}

// CostSeconds 按录像秒数与(可选)分档计算时长费用。
func (p MediaPrice) CostSeconds(seconds float64, resolution string) float64 {
	if seconds <= 0 {
		return 0
	}
	unit := p.PerSecond
	if resolution != "" {
		if v, ok := p.Resolution[resolution]; ok && v > 0 {
			unit = v
		}
	}
	return seconds * unit
}

// CostVideos 按条数与时长计算一次生视频任务的输出费用: 每条固定起步价 + 时长费。
// videos 为本次任务产出的视频条数(OpenAI Videos 协议下恒为 1), 传 0 视为未产出、不计费。
func (p MediaPrice) CostVideos(videos int64, seconds float64, resolution string) float64 {
	if videos <= 0 {
		return 0
	}
	return float64(videos)*p.PerVideo + p.CostSeconds(seconds, resolution)
}

// Scale 按比例缩放所有媒体维度, ratio 同 LLMPrice.Scale。
func (p MediaPrice) Scale(ratio float64) MediaPrice {
	factor := 1 + ratio
	res := make(map[string]float64, len(p.Resolution))
	for k, v := range p.Resolution {
		res[k] = v * factor
	}
	return MediaPrice{
		PerImage:   p.PerImage * factor,
		PerVideo:   p.PerVideo * factor,
		PerSecond:  p.PerSecond * factor,
		Resolution: res,
	}
}

// MediaUnits 媒体调用的一次用量: 生图记张数, 生视频记条数与时长, Resolution 为命中的分档键。
type MediaUnits struct {
	Images     int64   `json:"images,omitempty"`
	Videos     int64   `json:"videos,omitempty"`
	Seconds    float64 `json:"seconds,omitempty"`
	Resolution string  `json:"resolution,omitempty"`
}

// MaxImagesPerRequest 返回定价表中按输出计费的最大单次单位价值; 用于 reserveBalance 的保守估算。
// 计费逻辑与 CostImages / CostSeconds 对齐, 不依赖具体 N 值, 只看价格档位以避免低估。
func (p MediaPrice) MaxImagesPerRequest() float64 {
	max := p.PerImage
	for _, v := range p.Resolution {
		if v > max {
			max = v
		}
	}
	return max
}

// MaxVideosPerRequest 返回一次生视频任务的保守费用上限: 每条固定起步价 + 最大每秒单价 × seconds。
// seconds 由调用方按请求声明的时长给出(生视频请求自带 seconds 字段, 无需猜测);
// 与 CostVideos 同口径, 只用于冻结额度, 实际结算按上游返回的时长。
func (p MediaPrice) MaxVideosPerRequest(seconds float64) float64 {
	max := p.PerSecond
	for _, v := range p.Resolution {
		if v > max {
			max = v
		}
	}
	return p.PerVideo + max*seconds
}

type LLMInfo struct {
	Name string `json:"name" gorm:"primaryKey;not null"`
	LLMPrice
}

type OpenAIModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int    `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type OpenAIModelList struct {
	Object string        `json:"object"`
	Data   []OpenAIModel `json:"data"`
}
type AnthropicModel struct {
	ID          string `json:"id"`
	CreatedAt   string `json:"created_at"`
	DisplayName string `json:"display_name"`
	Type        string `json:"type"`
}

type AnthropicModelList struct {
	Data    []AnthropicModel `json:"data"`
	FirstID string           `json:"first_id"`
	HasMore bool             `json:"has_more"`
	LastID  string           `json:"last_id"`
}

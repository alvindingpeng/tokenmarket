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
// PerImage 按张收费, PerSecond 按秒收费; Resolution 是可选的分档定价, 例如 480p / 720p / 1080p。
type MediaPrice struct {
	PerImage    float64            `json:"per_image,omitempty"`
	PerSecond   float64            `json:"per_second,omitempty"`
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

// CostSeconds 按录像秒数与(可选)分档计算输出费用。
func (p MediaPrice) CostSeconds(seconds float64, resolution string) float64 {
	if seconds <= 0 {
		return 0
	}
	unit := p.PerSecond
	if resolution != "" {
		if v, ok := p.Resolution[resolution]; ok && v > 0 {
			unit = v
		}
		if unit == 0 {
			unit = p.PerSecond
		}
	}
	return seconds * unit
}

// Scale 按比例缩放所有媒体维度, ratio 同 LLMPrice.Scale。
func (p MediaPrice) Scale(ratio float64) MediaPrice {
	factor := 1 + ratio
	res := make(map[string]float64, len(p.Resolution))
	for k, v := range p.Resolution {
		res[k] = v * factor
	}
	return MediaPrice{
		PerImage:  p.PerImage * factor,
		PerSecond: p.PerSecond * factor,
		Resolution: res,
	}
}

// MediaUnits 媒体调用的一次用量: P1 只有图像张数, P2 扩展视频秒数与分辨率档位。
type MediaUnits struct {
	Images     int64   `json:"images,omitempty"`
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

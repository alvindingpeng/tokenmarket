package model

import (
	"strings"
	"time"
)

// ModelCapability 是一个模型按官方登记(目前来自 models.dev)的输入输出能力与规模上限。
// 只服务于两件事: 探测渠道模型时预勾选协议位, 以及在界面上解释"为什么这个模型被判成生图/视频"。
// 不参与计费: 媒体单价仍由管理员按每张/每秒填写, 官方登记里也没有这类单价。
type ModelCapability struct {
	Name      string    `json:"name" gorm:"primaryKey;not null"` // 统一小写的模型名, 可能带厂商前缀(如 openai/gpt-image-1)。
	Input     string    `json:"input" gorm:"default:''"`         // 逗号分隔的输入模态: text,image,audio,video,pdf。
	Output    string    `json:"output" gorm:"default:''"`        // 逗号分隔的输出模态: text,image,video,audio,pdf。
	Context   int       `json:"context" gorm:"default:0"`        // 上下文窗口(token), 0 表示官方未登记。
	MaxOutput int       `json:"max_output" gorm:"default:0"`     // 单次最大输出(token), 0 表示官方未登记。
	Family    string    `json:"family" gorm:"default:''"`        // 官方系列名, 仅供排错与展示。
	Source    string    `json:"source" gorm:"default:''"`        // 来源: models.dev 或 manual(人工补录, 不会被同步删除)。
	UpdatedAt time.Time `json:"updated_at"`                      // 最近一次写入时间。
}

// CapabilitySourceModelsDev / CapabilitySourceManual 能力条目的两种来源。
const (
	CapabilitySourceModelsDev = "models.dev"
	CapabilitySourceManual    = "manual"
)

// hasModality 判断逗号分隔的模态串里是否含某个模态; 大小写与空白都不敏感。
func hasModality(list, want string) bool {
	for _, item := range strings.Split(list, ",") {
		if strings.EqualFold(strings.TrimSpace(item), want) {
			return true
		}
	}
	return false
}

// OutputsText / OutputsImage / OutputsVideo 判定输出模态。
func (c ModelCapability) OutputsText() bool  { return hasModality(c.Output, "text") }
func (c ModelCapability) OutputsImage() bool { return hasModality(c.Output, "image") }
func (c ModelCapability) OutputsVideo() bool { return hasModality(c.Output, "video") }

// MediaKind 由输出模态推出的媒体形态:
// 只输出视频/图像才算专用媒体模型; 同时输出文本的(如 gemini-flash-image、nano-banana)是"图文混合",
// 既能聊天又能出图, 按图计费会把聊天流量算成图片, 故一律按文本处理, 由管理员在界面上自行决定。
func (c ModelCapability) MediaKind() MediaKind {
	if c.OutputsVideo() && !c.OutputsText() {
		return MediaKindVideo
	}
	if c.OutputsImage() && !c.OutputsText() {
		return MediaKindImage
	}
	return MediaKindText
}

// MixedMedia 标记"既能出图/出视频又能出文本"的混合模型: 形态按文本算, 但界面上要提示它具备媒体能力。
func (c ModelCapability) MixedMedia() bool {
	return (c.OutputsImage() || c.OutputsVideo()) && c.OutputsText()
}

// Empty 表示这条能力记录没有可用信息。
func (c ModelCapability) Empty() bool {
	return c.Output == "" && c.Input == "" && c.Context == 0 && c.MaxOutput == 0
}

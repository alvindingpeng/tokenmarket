package model

import (
	"fmt"
	"net/url"
	"strings"
)

// EgressKind 是出口的接入方式。
type EgressKind string

const (
	// EgressKindDirect 直连: 不经任何代理, 用网关本机网络出去。
	EgressKindDirect EgressKind = "direct"
	// EgressKindHTTP 走 HTTP/HTTPS 代理。
	EgressKindHTTP EgressKind = "http"
	// EgressKindSOCKS5 走 SOCKS5 代理。
	EgressKindSOCKS5 EgressKind = "socks5"
)

// ChannelEgress 是渠道的一个出口: 转发到该渠道时可以走不同的网络出口。
//
// 出口与凭据正交: 凭据决定"用什么身份", 出口决定"从哪条线路出去"。同一渠道配多个出口,
// 由转发路径按实测耗时择优, 某个出口不通时切到另一个 —— 这正是"给渠道加加速线路"的落点。
type ChannelEgress struct {
	ID        int        `json:"id" gorm:"primaryKey"`                                       // 出口主键。
	ChannelID int        `json:"channel_id" gorm:"not null;index:idx_channel_egress,unique"` // 所属渠道 ID。
	Name      string     `json:"name" gorm:"not null;index:idx_channel_egress,unique"`       // 出口名称, 渠道内唯一, 界面展示与人工识别用。
	Kind      EgressKind `json:"kind" gorm:"size:16;not null;default:direct"`                // 接入方式: direct|http|socks5。
	URL       string     `json:"url"`                                                        // 代理地址, kind=direct 时留空。
	Region    string     `json:"region" gorm:"size:16;default:''"`                           // 出口所在地区码, 用于区域亲和; 留空表示不参与匹配。
	Weight    int        `json:"weight" gorm:"default:0"`                                    // 同分时的优先权重, 数值越大越先被选中; 也是冷出口的采样顺序。
	Enabled   bool       `json:"enabled" gorm:"default:true"`                                // 是否启用; 停用后不参与选路, 但保留配置。
}

// Normalize 清洗并校验出口配置, 返回可直接落库的副本。
// 校验放在模型层而非仅前端: 出口地址写错会让整条渠道不可用, 而错配的 kind/URL 组合
// (例如 kind=http 却填了 socks5://) 只会在转发时炸, 那时已经花了用户一次请求。
func (e ChannelEgress) Normalize() (ChannelEgress, error) {
	normalized := e
	normalized.Name = strings.TrimSpace(normalized.Name)
	normalized.URL = strings.TrimSpace(normalized.URL)
	normalized.Region = strings.ToUpper(strings.TrimSpace(normalized.Region))

	if normalized.Name == "" {
		return ChannelEgress{}, fmt.Errorf("egress name is required")
	}
	switch normalized.Kind {
	case EgressKindDirect:
		// 直连不需要地址; 填了也清掉, 免得界面显示一个不生效的代理地址。
		normalized.URL = ""
		return normalized, nil
	case EgressKindHTTP, EgressKindSOCKS5:
	default:
		return ChannelEgress{}, fmt.Errorf("unsupported egress kind: %s", normalized.Kind)
	}

	if normalized.URL == "" {
		return ChannelEgress{}, fmt.Errorf("egress %s requires a proxy url", normalized.Name)
	}
	parsed, err := url.Parse(normalized.URL)
	if err != nil {
		return ChannelEgress{}, fmt.Errorf("egress %s has invalid url: %w", normalized.Name, err)
	}
	// 协议必须与接入方式一致: socks5 地址填成 http 会在拨号时才失败。
	expected := []string{"http", "https"}
	if normalized.Kind == EgressKindSOCKS5 {
		expected = []string{"socks5", "socks5h"}
	}
	for _, scheme := range expected {
		if parsed.Scheme == scheme {
			return normalized, nil
		}
	}
	return ChannelEgress{}, fmt.Errorf("egress %s kind %s requires %s url, got %s", normalized.Name, normalized.Kind, expected[0], parsed.Scheme)
}

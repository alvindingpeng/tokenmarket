package model

// AutoGroupType 表示渠道自动加入模型分组的匹配方式。
type AutoGroupType int

const (
	AutoGroupTypeNone  AutoGroupType = 0 //不自动分组
	AutoGroupTypeFuzzy AutoGroupType = 1 //模糊匹配
	AutoGroupTypeExact AutoGroupType = 2 //准确匹配
	AutoGroupTypeRegex AutoGroupType = 3 //正则匹配
)

// ChannelProvider 表示渠道使用的上游服务提供方。
type ChannelProvider string

const (
	ChannelProviderOpenAI          ChannelProvider = "openai"
	ChannelProviderOpenAIResponses ChannelProvider = "openai_responses"
	ChannelProviderAnthropic       ChannelProvider = "anthropic"
	ChannelProviderGemini          ChannelProvider = "gemini"
	ChannelProviderVolcengine      ChannelProvider = "volcengine"
)

// Channel 保存单个上游渠道的连接和转发配置。
type Channel struct {
	ID            int             `json:"id" gorm:"primaryKey"`                    // ID 是渠道主键。
	Name          string          `json:"name" gorm:"unique;not null"`            // Name 是渠道名称。
	Type          ChannelProvider `json:"type"`                                    // Type 是上游服务提供方。
	Enabled       bool            `json:"enabled" gorm:"default:true"`            // Enabled 表示渠道是否可用。
	BaseURL       string          `json:"base_url"`                                // BaseURL 是唯一的上游基础地址。
	Key           string          `json:"key"`                                     // Key 是唯一的上游访问凭据。
	Model         string          `json:"model"`                                   // Model 是自动同步的模型列表。
	CustomModel   string          `json:"custom_model"`                            // CustomModel 是手动配置的模型列表。
	Proxy         bool            `json:"proxy" gorm:"default:false"`             // Proxy 表示是否使用代理。
	AutoSync      bool            `json:"auto_sync" gorm:"default:false"`         // AutoSync 表示是否自动同步模型。
	AutoGroup     AutoGroupType   `json:"auto_group" gorm:"default:0"`            // AutoGroup 是自动分组策略。
	CustomHeader  []CustomHeader  `json:"custom_header" gorm:"serializer:json"`   // CustomHeader 是追加到上游请求的 Header。
	ParamOverride *string         `json:"param_override"`                          // ParamOverride 是请求参数覆盖配置。
	ChannelProxy  *string         `json:"channel_proxy"`                           // ChannelProxy 是渠道专用代理地址。
	Stats         *StatsChannel   `json:"stats,omitempty" gorm:"foreignKey:ChannelID"` // Stats 是渠道统计信息。
	MatchRegex    *string         `json:"match_regex"`                             // MatchRegex 是模型同步过滤表达式。
}

// CustomHeader 表示追加到上游请求的单个 Header。
type CustomHeader struct {
	HeaderKey   string `json:"header_key"`   // HeaderKey 是 Header 名称。
	HeaderValue string `json:"header_value"` // HeaderValue 是 Header 值。
}

// ChannelUpdateRequest 渠道更新请求 - 仅包含变更的数据
type ChannelUpdateRequest struct {
	ID            int              `json:"id" binding:"required"`   // ID 是待更新渠道的主键。
	Name          *string          `json:"name,omitempty"`          // Name 是新的渠道名称。
	Type          *ChannelProvider `json:"type,omitempty"`          // Type 是新的上游服务提供方。
	Enabled       *bool            `json:"enabled,omitempty"`       // Enabled 是新的启用状态。
	BaseURL       *string          `json:"base_url,omitempty"`      // BaseURL 是新的上游基础地址。
	Key           *string          `json:"key,omitempty"`           // Key 是新的上游访问凭据。
	Model         *string          `json:"model,omitempty"`         // Model 是新的自动同步模型列表。
	CustomModel   *string          `json:"custom_model,omitempty"`  // CustomModel 是新的自定义模型列表。
	Proxy         *bool            `json:"proxy,omitempty"`         // Proxy 是新的代理开关。
	AutoSync      *bool            `json:"auto_sync,omitempty"`     // AutoSync 是新的自动同步开关。
	AutoGroup     *AutoGroupType   `json:"auto_group,omitempty"`    // AutoGroup 是新的自动分组策略。
	CustomHeader  *[]CustomHeader  `json:"custom_header,omitempty"` // CustomHeader 是新的自定义 Header。
	ChannelProxy  *string          `json:"channel_proxy,omitempty"` // ChannelProxy 是新的渠道代理地址。
	ParamOverride *string          `json:"param_override,omitempty"` // ParamOverride 是新的参数覆盖配置。
	MatchRegex    *string          `json:"match_regex,omitempty"`   // MatchRegex 是新的模型过滤表达式。
}

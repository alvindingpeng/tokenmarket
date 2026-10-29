package model

import "time"

// UpstreamTiming 是一次上游请求的连接与首字节耗时分解, 单位纳秒。
// 各段是包含关系: TTFBNs 覆盖 DNS+建连+TLS, 故上游净等待 ≈ TTFBNs - DNSNs - ConnectNs - TLSNs。
//
// 某一段为 0 有三种可能, 看统计时不能一律读成"很快":
//  1. 复用了已有连接(DNS/建连/TLS 都不会触发), 由 ConnReused 标出;
//  2. 上游用 http 明文(没有 TLS 段), 见渠道 base_url 的 scheme;
//  3. 上游地址本身就是 IP(没有 DNS 段), 见渠道 base_url 的 host。
//
// 线上实测就有渠道直连 http://<IP>:<port>, 三段全为 0 而首字节 2.3s —— 时间全在上游排队。
// 这也是本模块存在的意义: 只看首字节, 会误判成"我们连得很快"。
type UpstreamTiming struct {
	EgressID   int   `json:"egress_id" gorm:"index;default:0"` // 出口标识, 出口池使用; 0 表示直连或全局代理。
	DNSNs      int64 `json:"dns_ns" gorm:"default:0"`          // 域名解析耗时; 上游地址为 IP 时为 0。
	ConnectNs  int64 `json:"connect_ns" gorm:"default:0"`      // TCP 建连耗时。
	TLSNs      int64 `json:"tls_ns" gorm:"default:0"`          // TLS 握手耗时; 上游用 http 明文时为 0。
	TTFBNs     int64 `json:"ttfb_ns" gorm:"default:0"`         // 请求发出到收到首字节的耗时, 含上面三段。
	ConnReused bool  `json:"conn_reused" gorm:"default:false"` // 本次是否复用了已有连接; 为 true 时上面三段必为 0。
}

// RelayRequest 持久化的调用请求记录: 与进程内 RequestState 同一 ID 空间, 服务重启后仍可分页查询。
type RelayRequest struct {
	ID                   uint64            `json:"id" gorm:"primaryKey;autoIncrement:false"`
	UserID               uint              `json:"user_id" gorm:"index"`
	APIKeyID             int               `json:"api_key_id" gorm:"index"`
	APIKeyName           string            `json:"api_key_name"`
	GroupID              int               `json:"group_id" gorm:"index"`
	Model                string            `json:"model"`
	ReasoningEffort      string            `json:"reasoning_effort"`
	Protocol             int               `json:"protocol"`
	Status               string            `json:"status" gorm:"index"`
	ClientIP             string            `json:"client_ip" gorm:"size:64;index"`                // 调用方地址, 供风控与排查。
	ClientCountry        string            `json:"client_country" gorm:"size:8;index;default:''"` // 调用方国家码(ISO 3166-1 alpha-2), 取自可信反代注入的头; 无边缘接入时为空。
	StartedAt            time.Time         `json:"started_at" gorm:"index"`
	FinishedAt           time.Time         `json:"finished_at"`
	DurationNs           int64             `json:"duration_ns"`             // 总耗时(纳秒), 与状态流一致。
	FirstTokenDurationNs int64             `json:"first_token_duration_ns"` // 首字节耗时(纳秒)。
	StreamDurationNs     int64             `json:"stream_duration_ns"`      // 流式耗时(纳秒)。
	ResponseDurationNs   int64             `json:"response_duration_ns"`    // 非流式耗时(纳秒)。
	UpstreamTiming       `gorm:"embedded"` // 最终轮次上游请求的连接耗时分解。
	Round                int               `json:"round"`
	PromptTokens         int64             `json:"prompt_tokens"`
	CompletionTokens     int64             `json:"completion_tokens"`
	CachedTokens         int64             `json:"cached_tokens"`
	CacheWriteTokens     int64             `json:"cache_write_tokens"`
	Cost                 float64           `json:"cost"`              // 用户侧计费金额(美元), 含加价。
	SupplyCost           float64           `json:"supply_cost"`       // 上游供货成本(美元), 不含加价。
	UserCharge           float64           `json:"user_charge"`       // 用户实际扣款金额(美元), 等于 Cost。
	Profit               float64           `json:"profit"`            // 毛利(美元) = UserCharge - SupplyCost。
	OutputChars          int               `json:"output_chars"`
	TargetChannelKey     string            `json:"target_channel_key"`
	TargetChannelCode    string            `json:"target_channel_code"`
	TargetModel          string            `json:"target_model"`
	TargetProtocol       int               `json:"target_protocol"`
	Error                string            `json:"error" gorm:"type:text"`
	RequestBody          string            `json:"request_body" gorm:"type:text"`
	RouteEvents          string            `json:"route_events" gorm:"type:text"`
	ResponseBody         string            `json:"response_body" gorm:"type:text"`
}

// TableName 指定表名。
func (RelayRequest) TableName() string { return "relay_requests" }

// RelayRequestAttempt 一次请求内单个上游尝试的记录; 一个请求有多轮时逐轮留存。
type RelayRequestAttempt struct {
	ID             uint              `json:"id" gorm:"primaryKey"`
	RequestID      uint64            `json:"request_id" gorm:"uniqueIndex:idx_attempt_request_round"`
	Round          int               `json:"round" gorm:"uniqueIndex:idx_attempt_request_round"`
	GroupItemID    int               `json:"group_item_id"`
	ChannelID      int               `json:"channel_id" gorm:"index:idx_attempt_channel_started,priority:1"`
	ChannelCode    string            `json:"channel_code"`
	TargetModel    string            `json:"target_model"`
	TargetProtocol int               `json:"target_protocol"`
	StartedAt      time.Time         `json:"started_at" gorm:"index;index:idx_attempt_channel_started,priority:2"`
	FirstTokenMs   int64             `json:"first_token_ms"`
	DurationMs     int64             `json:"duration_ms"`
	UpstreamTiming `gorm:"embedded"` // 本轮上游请求的连接耗时分解。
	Status         string            `json:"status"` // success | failed | limited | canceled | timeout
	Error          string            `json:"error" gorm:"type:text"`
	Decision       string            `json:"decision" gorm:"type:text"` // 本轮路由决策说明(JSON)。
}

// RelayRouteDecision 是一轮尝试的结构化路由解释。
type RelayRouteCandidate struct {
	ItemID      int    `json:"item_id"`
	ChannelCode string `json:"channel_code"`
	Model       string `json:"model"`
	Reason      string `json:"reason"`
}

type RelayRouteDecision struct {
	Mode       string                `json:"mode"`
	Candidates []RelayRouteCandidate `json:"candidates,omitempty"`
	Phase      string                `json:"phase"`
	Reason     string                `json:"reason"`
	Details    string                `json:"details,omitempty"`
}

// TableName 指定表名。
func (RelayRequestAttempt) TableName() string { return "relay_request_attempts" }

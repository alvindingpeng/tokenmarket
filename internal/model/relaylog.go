package model

import "time"

// RelayRequest 持久化的调用请求记录: 与进程内 RequestState 同一 ID 空间, 服务重启后仍可分页查询。
type RelayRequest struct {
	ID                   uint64    `json:"id" gorm:"primaryKey;autoIncrement:false"`
	UserID               uint      `json:"user_id" gorm:"index"`
	APIKeyID             int       `json:"api_key_id" gorm:"index"`
	APIKeyName           string    `json:"api_key_name"`
	GroupID              int       `json:"group_id" gorm:"index"`
	Model                string    `json:"model"`
	ReasoningEffort      string    `json:"reasoning_effort"`
	Protocol             int       `json:"protocol"`
	Status               string    `json:"status" gorm:"index"`
	ClientIP             string    `json:"client_ip" gorm:"size:64;index"` // 调用方地址, 供风控与排查。
	StartedAt            time.Time `json:"started_at" gorm:"index"`
	FinishedAt           time.Time `json:"finished_at"`
	DurationNs           int64     `json:"duration_ns"`             // 总耗时(纳秒), 与状态流一致。
	FirstTokenDurationNs int64     `json:"first_token_duration_ns"` // 首字节耗时(纳秒)。
	StreamDurationNs     int64     `json:"stream_duration_ns"`      // 流式耗时(纳秒)。
	ResponseDurationNs   int64     `json:"response_duration_ns"`    // 非流式耗时(纳秒)。
	Round                int       `json:"round"`
	PromptTokens         int64     `json:"prompt_tokens"`
	CompletionTokens     int64     `json:"completion_tokens"`
	CachedTokens         int64     `json:"cached_tokens"`
	CacheWriteTokens     int64     `json:"cache_write_tokens"`
	Cost                 float64   `json:"cost"`
	OutputChars          int       `json:"output_chars"`
	TargetChannelKey     string    `json:"target_channel_key"`
	TargetChannelCode    string    `json:"target_channel_code"`
	TargetModel          string    `json:"target_model"`
	TargetProtocol       int       `json:"target_protocol"`
	Error                string    `json:"error" gorm:"type:text"`
	RequestBody          string    `json:"request_body" gorm:"type:text"`
	RouteEvents          string    `json:"route_events" gorm:"type:text"`
	ResponseBody         string    `json:"response_body" gorm:"type:text"`
}

// TableName 指定表名。
func (RelayRequest) TableName() string { return "relay_requests" }

// RelayRequestAttempt 一次请求内单个上游尝试的记录; 一个请求有多轮时逐轮留存。
type RelayRequestAttempt struct {
	ID             uint      `json:"id" gorm:"primaryKey"`
	RequestID      uint64    `json:"request_id" gorm:"uniqueIndex:idx_attempt_request_round"`
	Round          int       `json:"round" gorm:"uniqueIndex:idx_attempt_request_round"`
	GroupItemID    int       `json:"group_item_id"`
	ChannelID      int       `json:"channel_id"`
	ChannelCode    string    `json:"channel_code"`
	TargetModel    string    `json:"target_model"`
	TargetProtocol int       `json:"target_protocol"`
	StartedAt      time.Time `json:"started_at"`
	FirstTokenMs   int64     `json:"first_token_ms"`
	DurationMs     int64     `json:"duration_ms"`
	Status         string    `json:"status"` // success | failed | limited | canceled | timeout
	Error          string    `json:"error" gorm:"type:text"`
	Decision       string    `json:"decision" gorm:"type:text"` // 本轮路由决策说明(JSON)。
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

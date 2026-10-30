package model

import (
	"encoding/json"
	"fmt"
	"time"
)

// Webhook 出站通知订阅(P0-5)。
//
// 订阅按用户隔离: 一条 webhook 只投递其归属用户自己的通知, 不是全局事件广播。
// 事件名与 NotificationType 同值(system / balance_low / ...), 前端订阅什么就投什么。
//
// 密钥永不回传: SecretEncrypted 列存 secretstore 信封密文(无钥环境透传, 同 settings/channel_keys
// 的既有约定), API 只暴露 HasSecret。信封归属绑定见 secretstore.WebhookSecretBinding。

const (
	// WebhookMaxPerUser 每用户订阅配额。
	WebhookMaxPerUser = 10
	// WebhookSecretMinBytes / WebhookSecretMaxBytes 签名密钥长度(按 UTF-8 字节数)。
	WebhookSecretMinBytes = 16
	WebhookSecretMaxBytes = 128
	// WebhookMaxRetryCount 首投失败后的最大重试次数。
	WebhookMaxRetryCount = 3
	// WebhookMaxEvents 单订阅最多订阅的事件数。
	WebhookMaxEvents = 20
	// WebhookURLMaxLen URL 最大长度。
	WebhookURLMaxLen = 2048
	// WebhookTestMinInterval 测试发送的最小间隔(每订阅)。
	WebhookTestMinInterval = 60 * time.Second
)

// WebhookEventTest 测试发送专用事件名, 不属于通知类型, 也不受 events 订阅过滤。
const WebhookEventTest = "test"

// WebhookEvents 全部可订阅事件(= 通知类型)。校验用闭集, 防止把任意字符串写进订阅表。
var WebhookEvents = []string{
	string(NotificationTypeSystem),
	string(NotificationTypeBalanceLow),
	string(NotificationTypeRateLimitHit),
	string(NotificationTypeChannelDown),
	string(NotificationTypeChannelReview),
	string(NotificationTypeWithdrawal),
	string(NotificationTypeDispute),
	string(NotificationTypeRecharge),
	string(NotificationTypeTicket),
}

// WebhookValidEvent 判断事件名是否在可订阅闭集内。
func WebhookValidEvent(event string) bool {
	for _, e := range WebhookEvents {
		if e == event {
			return true
		}
	}
	return false
}

// Webhook 投递状态机的持久状态(pending/processing/succeeded/failed/cancelled)。
const (
	WebhookDeliveryStatusPending    = "pending"
	WebhookDeliveryStatusProcessing = "processing"
	WebhookDeliveryStatusSucceeded  = "succeeded"
	WebhookDeliveryStatusFailed     = "failed"
	WebhookDeliveryStatusCancelled  = "cancelled"
)

// WebhookLastStatusNone 表示该订阅从未有过投递。
const WebhookLastStatusNone = "none"

// Webhook 一条出站订阅。
type Webhook struct {
	ID     uint64 `json:"id" gorm:"primaryKey"`
	UserID uint   `json:"-" gorm:"index:idx_webhook_user;not null"`
	URL    string `json:"url" gorm:"size:2048;not null"`
	// Events 存 JSON 数组文本(如 ["system","recharge"]): 跨 SQLite/MySQL/Postgres 一致,
	// 过滤在应用层做(每用户订阅数上限 10, 全量取回可忽略)。
	// 注意: Enabled/RetryCount 等列刻意不带 DB default —— gorm 会跳过"带默认值的零值列",
	// retry_count=0 与 enabled=false 会被悄悄落成默认值; 默认值由 WebhookCreate 在代码里给。
	Events     string `json:"-" gorm:"type:text;not null"`
	Enabled    bool   `json:"enabled" gorm:"not null"`
	RetryCount int    `json:"retry_count" gorm:"not null"`
	// SecretEncrypted 只存信封密文; 任何 API 都不回传本列(json:"-")。
	SecretEncrypted string `json:"-" gorm:"column:secret_encrypted;type:text"`
	// Version 是 URL 版本: 每次改 URL 自增。未完成投递记录其入队时的版本,
	// 投递前版本不符即取消 —— 旧投递永远不会发往改绑后的地址。
	Version uint64 `json:"-" gorm:"not null"`
	// LastTestAt 测试发送频率限制(每订阅 60 秒一次)的原子判据。
	LastTestAt *time.Time `json:"-"`
	// LastStatus 最近一次投递状态(none/pending/processing/succeeded/failed/cancelled)。
	LastStatus string    `json:"last_status" gorm:"size:16;not null"`
	CreatedAt  time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt  time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

// WebhookDelivery 一条待投递/已投递记录: 队列即日志。
//
// 交付语义是至少一次: DeliveryID 在入队时生成且重试间不变, 接收端按它去重。
// Payload 存签名用的原始 JSON body(重试间字节不变, 签名时间戳每次重签);
// 响应正文永不落库, LastError 只存脱敏摘要。
type WebhookDelivery struct {
	ID         uint64 `json:"id" gorm:"primaryKey"`
	DeliveryID string `json:"delivery_id" gorm:"size:64;uniqueIndex;not null"`
	WebhookID  uint64 `json:"webhook_id" gorm:"index:idx_webhook_delivery_hook,priority:1;not null"`
	UserID     uint   `json:"user_id" gorm:"index;not null"`
	// HookVersion 入队时的订阅 URL 版本; 投递前与当前版本不符即取消。
	HookVersion    uint64 `json:"hook_version" gorm:"not null"`
	Event          string `json:"event" gorm:"size:32;not null"`
	NotificationID uint64 `json:"notification_id" gorm:"index"`
	Payload        string `json:"-" gorm:"type:text;not null"`
	Status         string `json:"status" gorm:"size:16;index:idx_webhook_delivery_due,priority:2;not null;default:'pending'"`
	Attempts       int    `json:"attempts" gorm:"not null;default:0"`
	// NextAttemptAt 到期才可被 worker 抢占; 也是批量挑选的排序依据。
	NextAttemptAt  time.Time  `json:"next_attempt_at" gorm:"index"`
	LeaseExpiresAt *time.Time `json:"lease_expires_at" gorm:"index"`
	// LastStatusCode 最近一次 HTTP 状态码; 0 = 未获得响应(网络失败/超时)。
	LastStatusCode int `json:"last_status_code" gorm:"not null;default:0"`
	// DurationMs 最近一次投递耗时(毫秒); 未发出请求为 0。
	DurationMs int64     `json:"duration_ms" gorm:"not null;default:0"`
	LastError  string    `json:"last_error" gorm:"type:text"`
	CreatedAt  time.Time `json:"created_at" gorm:"autoCreateTime;index"`
	UpdatedAt  time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

// WebhookView API 返回的订阅视图: 只暴露 has_secret, 永不带密钥(明文或密文)。
type WebhookView struct {
	ID         uint64    `json:"id"`
	URL        string    `json:"url"`
	Events     []string  `json:"events"`
	Enabled    bool      `json:"enabled"`
	RetryCount int       `json:"retry_count"`
	HasSecret  bool      `json:"has_secret"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	LastStatus string    `json:"last_status"`
}

// WebhookDeliveryLogView API 返回的投递日志条目:
// 不含 Payload、不含响应正文、不含 URL(日志不回显可能带 token 的地址与 SSRF 响应)。
type WebhookDeliveryLogView struct {
	DeliveryID   string    `json:"delivery_id"`
	Event        string    `json:"event"`
	Status       string    `json:"status"`
	Attempts     int       `json:"attempts"`
	ResponseCode int       `json:"response_code"`
	DurationMs   int64     `json:"duration_ms"`
	Error        string    `json:"error"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// WebhookCreateRequest 创建订阅请求。
type WebhookCreateRequest struct {
	URL        string   `json:"url" binding:"required,max=2048"`
	Events     []string `json:"events" binding:"required,min=1,max=20"`
	Secret     string   `json:"secret" binding:"required"`
	Enabled    *bool    `json:"enabled"`
	RetryCount *int     `json:"retry_count" binding:"omitempty,min=0,max=3"`
}

// WebhookUpdateRequest 更新订阅请求(部分更新): Secret 留空或省略即保留原密钥。
type WebhookUpdateRequest struct {
	URL        *string   `json:"url"`
	Events     *[]string `json:"events"`
	Secret     string    `json:"secret"`
	Enabled    *bool     `json:"enabled"`
	RetryCount *int      `json:"retry_count" binding:"omitempty,min=0,max=3"`
}

// WebhookListRequest 列表/日志分页请求。
type WebhookListRequest struct {
	Page     int `form:"page" binding:"omitempty,min=1"`
	PageSize int `form:"page_size" binding:"omitempty,min=1,max=100"`
}

// WebhookPage 订阅列表分页返回。
type WebhookPage struct {
	Items    []WebhookView `json:"items"`
	Total    int64         `json:"total"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
}

// WebhookLogPage 投递日志分页返回。
type WebhookLogPage struct {
	Items    []WebhookDeliveryLogView `json:"items"`
	Total    int64                    `json:"total"`
	Page     int                      `json:"page"`
	PageSize int                      `json:"page_size"`
}

// WebhookTestResult 测试发送的入队回执: 只代表已入队, 不代表远端已收到。
type WebhookTestResult struct {
	DeliveryID string `json:"delivery_id"`
	Status     string `json:"status"`
}

// WebhookPayload 投递请求体: 只含用户自己的通知字段, 不带请求原文、API Key 或任何密钥。
type WebhookPayload struct {
	ID        string             `json:"id"`
	Event     string             `json:"event"`
	Timestamp int64              `json:"timestamp"`
	UserID    uint               `json:"user_id"`
	Data      WebhookPayloadData `json:"data"`
}

// WebhookPayloadData 通知内容摘要。
type WebhookPayloadData struct {
	Title     string `json:"title"`
	Content   string `json:"content"`
	Link      string `json:"link"`
	RelatedID uint64 `json:"related_id"`
}

// WebhookDecodeEvents 解出订阅事件列表; 存量行损坏时报错而不是静默投空。
func WebhookDecodeEvents(stored string) ([]string, error) {
	var events []string
	if err := json.Unmarshal([]byte(stored), &events); err != nil {
		return nil, fmt.Errorf("decode webhook events: %w", err)
	}
	return events, nil
}

// WebhookEncodeEvents 编码订阅事件列表。
func WebhookEncodeEvents(events []string) (string, error) {
	body, err := json.Marshal(events)
	if err != nil {
		return "", fmt.Errorf("encode webhook events: %w", err)
	}
	return string(body), nil
}

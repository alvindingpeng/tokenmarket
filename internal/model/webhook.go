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

// Webhook 报文模板: 决定投递时 body 的**形状**, 与该订阅的可信度/签名无关。
//
// 为什么要有它: 钉钉/飞书/Slack 的机器人端点只认自己的格式, 通用 JSON 丢进去直接被拒
// (钉钉回 errcode 400, Slack 回 invalid_payload)。用户要么自己写一个转换服务,
// 要么就只能不用 WebHook —— 前者把每个用户都变成运维, 后者等于功能不可用。
//
// 边界写清楚: 我们只改**报文形状**, **不做平台加签**(钉钉 sign / 飞书 signature)。
// 我们不知道用户的平台密钥, 也没有资格替平台签名; 接收端因此必须接受未加签报文
// (钉钉走「自定义关键词」模式、Slack incoming webhook 本身不校验签名)。
// 我们自己的 HMAC 头(X-Switchyard-Signature 等)照旧发送, 与模板无关。
const (
	// WebhookTemplateGeneric 通用 JSON: 本平台自有格式, 契约见 docs/WEBHOOKS.md。
	WebhookTemplateGeneric = "generic"
	// WebhookTemplateDingtalk 钉钉自定义机器人 markdown 报文。
	WebhookTemplateDingtalk = "dingtalk"
	// WebhookTemplateFeishu 飞书自定义机器人交互式卡片报文。
	WebhookTemplateFeishu = "feishu"
	// WebhookTemplateSlack Slack incoming webhook 消息报文。
	WebhookTemplateSlack = "slack"
)

// WebhookTemplates 报文模板闭集, 顺序即界面下拉顺序(generic 在首, 默认值)。
var WebhookTemplates = []string{
	WebhookTemplateGeneric,
	WebhookTemplateDingtalk,
	WebhookTemplateFeishu,
	WebhookTemplateSlack,
}

// WebhookValidTemplate 判断模板名是否在闭集内。
// 用闭集而不是自由字符串: 模板名会进 SQL 分支, 错一个字就是"投出去的 body 谁都不认",
// 而这种失败在本地测不出来(只有对方端点才拒)。
func WebhookValidTemplate(template string) bool {
	for _, t := range WebhookTemplates {
		if t == template {
			return true
		}
	}
	return false
}

// WebhookNormalizeTemplate 归一模板名: 空串 → generic(存量订阅与省略该字段的请求都走这里)。
// 非闭集值原样返回, 由调用方判定合法性 —— 归一函数不做校验, 否则"归一"和"拒绝"混在一起,
// 调用方拿不到"这是非法值"这个信号。
func WebhookNormalizeTemplate(template string) string {
	if template == "" {
		return WebhookTemplateGeneric
	}
	return template
}

// WebhookEvents 全部可订阅事件(= 通知类型)。校验用闭集, 防止把任意字符串写进订阅表。
var WebhookEvents = []string{
	string(NotificationTypeSystem),
	string(NotificationTypeBalanceLow),
	string(NotificationTypeRateLimitHit),
	string(NotificationTypeChannelDown),
	string(NotificationTypeChannelRecovered),
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

// WebhookDeliveryStatuses 投递状态闭集: 日志过滤的合法取值, 顺序按状态机走向排列。
var WebhookDeliveryStatuses = []string{
	WebhookDeliveryStatusPending,
	WebhookDeliveryStatusProcessing,
	WebhookDeliveryStatusSucceeded,
	WebhookDeliveryStatusFailed,
	WebhookDeliveryStatusCancelled,
}

// WebhookValidDeliveryStatus 判断状态名是否在投递状态闭集内。
// 过滤参数来自 query, 非法值必须被拒(400)而不是被当成"查不到"静默返回空列表。
func WebhookValidDeliveryStatus(status string) bool {
	for _, s := range WebhookDeliveryStatuses {
		if s == status {
			return true
		}
	}
	return false
}

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
	// Template 报文模板(generic/dingtalk/feishu/slack), 决定投递 body 的形状。
	// DB default 'generic' 是**必要的**: 存量订阅行没有这一列, 加列后必须落到 generic
	// 才能保持"字节不变"的既有契约(generic 走的正是原来的组装路径)。
	// 注意本列与 Enabled/RetryCount 不同, 它**带** DB default —— 那几个列刻意不带,
	// 因为它们的零值(false/0)是合法取值会被 GORM 跳过; 而空的 template 不是合法取值。
	Template string `json:"template" gorm:"not null;default:'generic'"`
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
	ID         uint64   `json:"id"`
	URL        string   `json:"url"`
	Events     []string `json:"events"`
	Enabled    bool     `json:"enabled"`
	RetryCount int      `json:"retry_count"`
	// Template 报文模板; 恒为闭集内的非空值(generic/dingtalk/feishu/slack)。
	// 存量行由 WebhookNormalizeTemplate 归一成 generic 后再下发, 前端不必处理空串。
	Template   string    `json:"template"`
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
	// Template 报文模板; 省略或空串 = generic(见 WebhookNormalizeTemplate)。
	// 闭集校验放在 op 层而不是 binding 标签: 那里能给出可区分的错误码与归一后的值。
	Template string `json:"template"`
}

// WebhookUpdateRequest 更新订阅请求(部分更新): Secret 留空或省略即保留原密钥。
type WebhookUpdateRequest struct {
	URL        *string   `json:"url"`
	Events     *[]string `json:"events"`
	Secret     string    `json:"secret"`
	Enabled    *bool     `json:"enabled"`
	RetryCount *int      `json:"retry_count" binding:"omitempty,min=0,max=3"`
	// Template 用指针: 部分更新的语义是"省略即不改动", 与"改成 generic"必须能区分。
	// 提交空串(非 nil)表示显式改回 generic —— 两者在 JSON 里都是合法的不同请求。
	Template *string `json:"template"`
}

// WebhookListRequest 列表/日志分页请求。
type WebhookListRequest struct {
	Page     int `form:"page" binding:"omitempty,min=1"`
	PageSize int `form:"page_size" binding:"omitempty,min=1,max=100"`
	// Status / Event 只对投递日志端点有意义(订阅列表忽略它们): 空串=不过滤。
	// 合法值不写在 binding 标签里 —— 闭集校验放在 op 层, 那里能同时管住 Count 与 Find,
	// 也能给出"非法状态"与"非法事件"两个可区分的错误码。
	Status string `form:"status"`
	Event  string `form:"event"`
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

// ---------- 平台机器人报文(generic 之外的三种模板) ----------
//
// 这些结构**不参与** generic 路径: generic 的 body 由 WebhookPayload 组装, 契约锁在
// docs/WEBHOOKS.md 与既有测试里, 一个字节都不能动。这里只描述"改成平台格式后长什么样"。
// 字段名与大小写必须照平台文档写死(钉钉 msgtype / 飞书 msg_type / Slack text+blocks),
// 拼错不会报错, 只会在对方端点被拒 —— 所以形状由测试逐个字段解回断言。

// WebhookDingtalkPayload 钉钉自定义机器人 markdown 报文。
// 钉钉对字段名大小写敏感: msgtype(小写) / markdown.title / markdown.text。
type WebhookDingtalkPayload struct {
	MsgType  string                  `json:"msgtype"`
	Markdown WebhookDingtalkMarkdown `json:"markdown"`
}

// WebhookDingtalkMarkdown 钉钉 markdown 段: title 是会话列表里的摘要行, text 是正文(markdown 语法)。
type WebhookDingtalkMarkdown struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

// WebhookFeishuPayload 飞书自定义机器人交互式卡片报文: msg_type 是下划线写法(不是 msgtype)。
type WebhookFeishuPayload struct {
	MsgType string            `json:"msg_type"`
	Card    WebhookFeishuCard `json:"card"`
}

// WebhookFeishuCard 飞书卡片: header 标题用 plain_text, 正文 div 用 lark_md。
type WebhookFeishuCard struct {
	Header   WebhookFeishuCardHeader    `json:"header"`
	Elements []WebhookFeishuCardElement `json:"elements"`
}

// WebhookFeishuCardHeader 卡片标题段。
type WebhookFeishuCardHeader struct {
	Title WebhookFeishuCardText `json:"title"`
}

// WebhookFeishuCardElement 卡片元素; 目前只发 div 一种(tag 决定平台如何解释 text)。
type WebhookFeishuCardElement struct {
	Tag  string                `json:"tag"`
	Text WebhookFeishuCardText `json:"text"`
}

// WebhookFeishuCardText 带 tag 的文本节点: plain_text / lark_md。
type WebhookFeishuCardText struct {
	Tag     string `json:"tag"`
	Content string `json:"content"`
}

// WebhookSlackPayload Slack incoming webhook 报文: text 是兜底纯文本(通知栏与不支持 blocks 的客户端用),
// blocks 是结构化正文。两个都给, 是因为 Slack 在不同客户端上对 blocks 的支持度不一致,
// 只发 blocks 会让通知栏空着。
type WebhookSlackPayload struct {
	Text   string              `json:"text"`
	Blocks []WebhookSlackBlock `json:"blocks"`
}

// WebhookSlackBlock Slack 块: type=section, text.type=mrkdwn。
type WebhookSlackBlock struct {
	Type string           `json:"type"`
	Text WebhookSlackText `json:"text"`
}

// WebhookSlackText Slack 块内文本节点(mrkdwn 支持粗体/链接等 markdown 子集)。
type WebhookSlackText struct {
	Type string `json:"type"`
	Text string `json:"text"`
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

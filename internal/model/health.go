package model

import "time"

// ChannelHealthRecord 渠道健康探测结果; 每次探测一行, 保留最近若干条供趋势分析。
type ChannelHealthRecord struct {
	ID          uint64    `json:"id" gorm:"primaryKey"`
	ChannelID   int       `json:"channel_id" gorm:"index"`
	ChannelCode string    `json:"channel_code" gorm:"size:64"`
	LatencyMs   int64     `json:"latency_ms"`
	StatusCode  int       `json:"status_code"`
	Error       string    `json:"error" gorm:"type:text"`
	Healthy     bool      `json:"healthy" gorm:"index"`
	CheckedAt   time.Time `json:"checked_at" gorm:"index"`
}

// TableName 指定表名。
func (ChannelHealthRecord) TableName() string { return "channel_health_records" }

// ChannelAlert 告警记录: 渠道连续异常或恢复时生成。
// Subject/Detail 是本次新增: 前者是告警主体的真实名称(渠道名, 系统级为"系统"),
// 后者是产生告警那一侧填好的结构化明细, 让管理员不必再回后台按编码反查是哪家渠道。
//
// 送达状态三件套(NotifyAttempts/NextNotifyAt/LastNotifyError)是补发机制的基础:
// 改造前只有一个 notified 布尔位, 发失败就永久为 false —— 2026-11-02 实测 260 条告警
// 因 Telegram 实体解析 400 全部静默丢失, 库里留着记录却没有任何重试入口,
// 管理员也不知道"有告警没送到"。现在失败会带原因、按退避排下一次, 由后台任务重试到位。
type ChannelAlert struct {
	ID          uint64 `json:"id" gorm:"primaryKey"`
	ChannelID   int    `json:"channel_id" gorm:"index"`
	ChannelCode string `json:"channel_code" gorm:"size:64"` // 共享编码, 用户侧看到的标识。
	Subject     string `json:"subject" gorm:"size:128"`     // 真实渠道名或"系统"。
	Kind        string `json:"kind" gorm:"size:32"`
	Reason      string `json:"reason" gorm:"type:text"`
	Detail      string `json:"detail" gorm:"type:text"` // 明细行("标签: 值"逐行), 外发内容与它一致。
	Notified    bool   `json:"notified"`
	// NotifiedAt 送达成功的时刻; 只有 notified 为真时才有意义, 用于展示送达延迟。
	NotifiedAt *time.Time `json:"notified_at,omitempty"`
	// NotifyAttempts 已尝试的外发轮数(含首发)。超过上限后不再补发, 但记录保留可查。
	//
	// 这个字段的 gorm tag 必须保持空 —— 2026-11-02 的线上事故就是加了 default:0;not null 造成的:
	//   1. 该列是后加的, 存量行全为 NULL, 而 "notify_attempts = 0" 对 NULL 恒不成立 →
	//      补发队列静默漏掉全部历史告警(v1.5.10, pending=0);
	//   2. 带 not null 后 AutoMigrate 判定列定义变化, 走 SQLite 表重建路径, 把 NULL 行
	//      拷进 NOT NULL 新表直接失败 → 服务起不来(v1.5.11 启动即崩)。
	// 因此这里不加任何约束, 存量 NULL 由迁移 037 归零, 读路径一律用 COALESCE(notify_attempts, 0) 兜底。
	NotifyAttempts int `json:"notify_attempts"`
	// NextNotifyAt 下一次补发时间; 为空表示没有排期(已送达、已放弃或从未尝试)。
	NextNotifyAt *time.Time `json:"next_notify_at,omitempty"`
	// LastNotifyError 最近一次外发失败原因; 送达成功后清空。
	// 这是"为什么没送到"唯一的持久线索 —— 只写日志的话过几天日志轮转就查不到了。
	LastNotifyError string    `json:"last_notify_error" gorm:"type:text"`
	CreatedAt       time.Time `json:"created_at" gorm:"index"`
}

// TableName 指定表名。
func (ChannelAlert) TableName() string { return "channel_alerts" }

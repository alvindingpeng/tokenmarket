package alert

import (
	"strings"
	"time"
)

// 本文件把「站内告警记录」渲染成管理员真正读得懂的外发消息。
//
// 改造前的两个硬伤:
//   1. 标题与正文只有 channel_code(共享渠道的 ShareCode), 管理员在 Telegram 里看到一串随机码,
//      还得回后台按编码反查是哪家渠道; 渠道名、上游模型名、错误类型全部缺失。
//   2. 事件类型只认 down/recovered 两种, 其余(余额不足、配额超限、备份失败、对账差异……)
//      一律被塞进 channel_degraded, 于是外发消息把"备份失败"说成"渠道降级"。
//
// 现在由 op 侧在产生告警时填好结构化事实(Kind/Scope/Fields), 本文件只负责排版:
// 谁产生告警谁最清楚该说什么, alert 包不猜。

// kindMeta 一种告警类型的展示元数据。
type kindMeta struct {
	Level string // critical | warning | info
	Title string
}

// kindMetas 覆盖全部已知告警类型(与 op 侧产生的 kind 一一对应)。
//
// 刻意保留 metaFor 的"查不到"分支: 新增一个 kind 却忘了在这里登记时,
// 标题会显示原始 kind 而不是冒充"渠道降级" —— 后者正是改造前把备份失败报成渠道降级的成因。
var kindMetas = map[string]kindMeta{
	"down":             {Level: "critical", Title: "渠道故障"},
	"recovered":        {Level: "info", Title: "渠道恢复"},
	"degraded":         {Level: "warning", Title: "渠道延迟降级"},
	"key_disabled":     {Level: "warning", Title: "渠道凭据熔断"},
	"model_disabled":   {Level: "warning", Title: "渠道模型熔断"},
	"low_balance":      {Level: "warning", Title: "余额不足"},
	"quota_exceeded":   {Level: "warning", Title: "配额超限"},
	"mismatch":         {Level: "critical", Title: "计费对账异常"},
	"billing_failed":   {Level: "critical", Title: "计费结算失败"},
	"backup_failed":    {Level: "critical", Title: "自动备份失败"},
	"archive_failed":   {Level: "warning", Title: "日志归档失败"},
	"task_failed":      {Level: "warning", Title: "后台任务失败"},
	"task_recovered":   {Level: "info", Title: "后台任务恢复"},
	"update_available": {Level: "info", Title: "发现新版本"},
	"test":             {Level: "info", Title: "告警通道测试"},
}

// metaFor 查表; 未登记的 kind 按 warning 处理并把 kind 本身当标题。
func metaFor(kind string) kindMeta {
	if meta, ok := kindMetas[kind]; ok {
		return meta
	}
	return kindMeta{Level: "warning", Title: strings.TrimSpace(kind)}
}

// levelOf 取告警级别: 调用方显式给的优先, 否则按 kind 推导。
func levelOf(a Alert) string {
	if a.Severity != "" {
		return a.Severity
	}
	return metaFor(a.Kind).Level
}

// levelIcon 告警级别图标; Telegram 与邮件客户端都能正确显示 emoji。
func levelIcon(level string) string {
	switch level {
	case "critical":
		return "🚨"
	case "info":
		return "✅"
	default:
		return "⚠️"
	}
}

// renderTitle 生成外发标题: 图标 + 类型 + 主体(渠道名或"系统")。
//
// Title 由调用方显式给出时直接沿用, 保留管理面板连通性测试这类自定义标题的能力。
func (a Alert) renderTitle() string {
	if title := strings.TrimSpace(a.Title); title != "" {
		return title
	}
	meta := metaFor(a.Kind)
	if a.Scope == "" {
		return levelIcon(meta.Level) + " " + meta.Title
	}
	return levelIcon(meta.Level) + " " + meta.Title + ": " + a.Scope
}

// renderMessage 生成外发正文: 可选首行概要, 随后逐行明细。
//
// 逐行"标签: 值"而不是一整段自然语言: 管理员在手机通知里扫一眼就能定位到哪家渠道、
// 哪个模型、熔断到几点, 不必点开后台。时间由各出口按自己的排版补上。
func (a Alert) renderMessage() string {
	lines := make([]string, 0, len(a.Fields)+2)
	if head := strings.TrimSpace(a.Message); head != "" {
		lines = append(lines, head)
	}
	for _, field := range a.Fields {
		label := strings.TrimSpace(field.Label)
		value := strings.TrimSpace(field.Value)
		if label == "" || value == "" {
			continue
		}
		lines = append(lines, label+": "+value)
	}
	return strings.Join(lines, "\n")
}

// telegramText 组装 Telegram 消息: 粗体标题 + 明细 + 斜体时间。
// 所有可变文本都要转义 —— 上游错误里带下划线是常事, 漏一处就让 Telegram 回 400 整条发不出去。
func telegramText(a Alert) string {
	stamp := a.Timestamp
	if stamp.IsZero() {
		stamp = time.Now()
	}
	body := a.renderMessage()
	if body != "" {
		body = "\n\n" + body + "\n\n"
	} else {
		body = "\n\n"
	}
	return "*" + escapeTelegramMarkdown(a.renderTitle()) + "*" + body +
		"_" + escapeTelegramMarkdown(stamp.Format("2006-01-02 15:04:05")) + "_"
}

// emailBody 邮件正文: 纯文本, 首行是标题, 末尾补时间。
func emailBody(a Alert) string {
	stamp := a.Timestamp
	if stamp.IsZero() {
		stamp = time.Now()
	}
	return a.renderTitle() + "\n\n" + a.renderMessage() + "\n\n时间: " + stamp.Format("2006-01-02 15:04:05")
}

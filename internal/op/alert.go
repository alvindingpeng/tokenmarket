package op

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/alvindingpeng/switchyard/internal/alert"
	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/charmbracelet/log"
)

// 告警的产生与送达。
//
// 本次改造要解决的: 管理员此前在 Telegram 里只收到"渠道告警: 8fKd2xQ"这类共享随机码,
// 既不知道是哪家渠道, 也不知道熔断到几点、错误出在哪; 而"备份失败""对账不平"又被统一
// 贴上 channel_degraded 的事件名, 消费端无法按类型分流。现在:
//   1. 主体用真实渠道名(共享编码只作为附加事实),
//   2. 事实以结构化 Fields 交给 alert 包按各出口排版,
//   3. kind 与事件一一对应, 渠道/凭据/渠道模型熔断都纳入告警。

// alertDedupDefaultMinutes 告警去重窗口默认值(分钟): 设置为 0 或读取失败时回退到此值。
// 窗口可配(alert_dedup_minutes), 让"同一故障多久重复提醒一次"成为运维策略而非硬编码常量。
const alertDedupDefaultMinutes = 10

// subjectSystem 系统级告警的主体: 不属于任何渠道的告警(备份、归档、任务)都归到这里。
const subjectSystem = "系统"

// alertDedupWindow 返回当前生效的告警去重窗口; 负数按默认值处理, 0 表示不去重。
func alertDedupWindow() time.Duration {
	minutes := SettingGetIntDefault(model.SettingKeyAlertDedupMinutes, alertDedupDefaultMinutes)
	if minutes < 0 {
		minutes = alertDedupDefaultMinutes
	}
	return time.Duration(minutes) * time.Minute
}

// AlertFacts 一条告警的结构化事实, 由产生告警的一侧填写。
//
// 原则是"谁产生谁描述": relay 知道错误类型与熔断时长, 备份任务知道文件名与磁盘,
// op 只负责去重、落库与投递, 不认识任何具体故障的细节。
type AlertFacts struct {
	ChannelID int           // 相关渠道; 0 表示系统级。
	Subject   string        // 主体显示名, 留空时按 ChannelID 反查真实渠道名, 再无则记为"系统"。
	Kind      string        // 站内告警类型, 决定标题、图标与外发事件名。
	Reason    string        // 一句话原因; 参与去重, 因此不得包含会每轮变化的数字。
	Fields    []alert.Field // 给管理员看的明细行(渠道名/模型/截止时间等)。
}

// RaiseAlert 是兼容旧签名的告警入口: 只给类型与原因, 主体按渠道 ID 自动补全。
func RaiseAlert(ctx context.Context, channelID int, subject, kind, reason string) bool {
	return RaiseAlertFacts(ctx, AlertFacts{ChannelID: channelID, Subject: subject, Kind: kind, Reason: reason})
}

// RaiseAlertFacts 记录一条站内告警并外发。
// 返回是否新建了记录(命中去重窗口返回 false); 外发是异步的, 成功后回填 notified 标记。
func RaiseAlertFacts(ctx context.Context, facts AlertFacts) bool {
	if facts.Kind == "" || strings.TrimSpace(facts.Reason) == "" {
		return false
	}
	facts.Reason = truncateReason(strings.TrimSpace(facts.Reason))
	subject, shareCode := resolveSubject(facts.ChannelID, facts.Subject)
	facts.Subject = subject

	// 去重: 窗口内已有同类型(同渠道同原因)告警则跳过, 避免每轮探测或每轮任务都重发。
	var count int64
	if window := alertDedupWindow(); window > 0 {
		if err := db.GetDB().WithContext(ctx).Model(&model.ChannelAlert{}).
			Where("channel_id = ? AND kind = ? AND reason = ? AND created_at > ?",
				facts.ChannelID, facts.Kind, facts.Reason, time.Now().Add(-window)).
			Count(&count).Error; err == nil && count > 0 {
			return false
		}
	}

	record := model.ChannelAlert{
		ChannelID:   facts.ChannelID,
		ChannelCode: shareCode,
		Subject:     facts.Subject,
		Kind:        facts.Kind,
		Reason:      facts.Reason,
		Detail:      renderDetail(facts.Fields),
		CreatedAt:   time.Now(),
	}
	if err := db.GetDB().Create(&record).Error; err != nil {
		return false
	}

	// 外发只走这一条路: 分发器按 alert_channels 决定渠道(默认含 webhook)。
	// 这里不再单独发一次 webhook —— 否则 alert_channels 含 webhook 时同一条告警会外发两次。
	outbound := alert.Alert{
		Event:     alert.Event(determineAlertEvent(facts.Kind)),
		Kind:      facts.Kind,
		Scope:     facts.Subject,
		Message:   facts.Reason,
		Fields:    facts.Fields,
		Timestamp: record.CreatedAt,
	}
	// 外发要在调用方返回之后继续, 故脱掉请求的取消信号(保留其中的值):
	// 直接用请求 ctx 的话, HTTP 处理函数一返回 ctx 就被取消, 发送会半路失败。
	sendCtx := context.WithoutCancel(ctx)
	go func() {
		if sendMultiChannelAlert(sendCtx, outbound) {
			// 回填「已通知」, 让界面能区分「已外发」与「仅站内」。
			db.GetDB().Model(&model.ChannelAlert{}).Where("id = ?", record.ID).Update("notified", true)
		}
	}()

	return true
}

// resolveSubject 定出告警主体并返回该渠道的共享编码(供落库 ChannelCode)。
//
// 真实渠道名优先: 管理员要一眼认出是哪家渠道, 而不是一串发布时生成的随机码。
// 旧调用把 ShareCode 传进来, 这里同样换成渠道名 —— 共享编码仍然进 ChannelCode 列,
// 用户侧看到的还是它, 两边信息都不丢。"system" 是历史上系统级告警的占位值, 一律改写为"系统"。
func resolveSubject(channelID int, subject string) (string, string) {
	if channelID > 0 {
		if channel, err := ChannelGet(channelID); err == nil {
			subject = strings.TrimSpace(subject)
			// 空主体或主体就是共享码本身(健康探测等旧调用点就是这么传的)时换成渠道名。
			// 调用方显式给的其它名称要尊重: 可能是凭据名、模型名这类更精确的主体。
			if subject == "" || (channel.ShareCode != "" && subject == channel.ShareCode) {
				subject = channel.Name
			}
			if subject == "" {
				subject = fmt.Sprintf("渠道 #%d", channelID)
			}
			return subject, channel.ShareCode
		}
		if strings.TrimSpace(subject) == "" {
			subject = fmt.Sprintf("渠道 #%d", channelID)
		}
		return subject, ""
	}
	if strings.TrimSpace(subject) == "" || subject == "system" {
		return subjectSystem, ""
	}
	return subject, ""
}

// channelDisplayName 返回渠道对管理员的可读名称: 优先真实名称, 再退化到共享编码与 ID。
// 告警明细里用它而不是裸 ShareCode —— 管理员对自己的渠道是按名称认的。
func channelDisplayName(channel model.Channel) string {
	if channel.Name != "" {
		return channel.Name
	}
	if channel.ShareCode != "" {
		return channel.ShareCode
	}
	return fmt.Sprintf("渠道 #%d", channel.ID)
}

// renderDetail 把明细行折成一段可读文本, 供站内告警列表展示与二次排查。
func renderDetail(fields []alert.Field) string {
	lines := make([]string, 0, len(fields))
	for _, field := range fields {
		if strings.TrimSpace(field.Label) == "" || strings.TrimSpace(field.Value) == "" {
			continue
		}
		lines = append(lines, field.Label+": "+field.Value)
	}
	return truncateDetail(strings.Join(lines, "\n"))
}

// truncateDetail 明细比 reason 长(可能几十行), 落库前收敛到 2000 字符。
func truncateDetail(s string) string {
	runes := []rune(s)
	if len(runes) <= 2000 {
		return s
	}
	return string(runes[:2000]) + "...(truncated)"
}

// truncateReason 截断过长原因, 保持 reason 字段可读且不撑爆表格。
func truncateReason(reason string) string {
	runes := []rune(reason)
	if len(runes) <= 500 {
		return reason
	}
	return string(runes[:500]) + "...(truncated)"
}

// ChannelAlertList 返回最近告警记录。
func ChannelAlertList(ctx context.Context, limit int) ([]model.ChannelAlert, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows := []model.ChannelAlert{}
	if err := db.GetDB().WithContext(ctx).Order("created_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// errorClassLabels 上游错误分类的中文说明; 与 relay.ErrorClass 的取值对齐。
// 分类常量在 relay 包(relay 依赖 op, 不能反向引用), 故这里按字符串映射。
var errorClassLabels = map[string]string{
	"auth":      "鉴权失败(密钥无效或已被撤销)",
	"quota":     "额度耗尽(上游账户余额不足)",
	"rate":      "上游限流",
	"overload":  "上游过载",
	"transient": "上游临时故障",
	"model":     "上游模型不可用",
	"policy":    "内容被安全策略拦截",
	"client":    "请求不被上游接受",
	"unknown":   "未分类错误",
}

// errorClassLabel 返回错误分类的中文说明; 未知分类原样返回, 便于发现新增而未登记的值。
func errorClassLabel(errorType string) string {
	if label, ok := errorClassLabels[errorType]; ok {
		return label
	}
	if errorType == "" {
		return "未分类错误"
	}
	return errorType
}

// determineAlertEvent 把站内告警类型映射为外发事件名。
//
// 改造前只有 down/recovered 两个分支, 其余一律掉进 channel_degraded,
// 于是"自动备份失败"在消费端被当成"渠道延迟降级"处理。未登记的 kind 归到 system_alert:
// 至少是个诚实的事件名, 不会误导分流逻辑。
func determineAlertEvent(kind string) string {
	switch kind {
	case "down":
		return "channel_failed"
	case "recovered":
		return "channel_recovered"
	case "degraded":
		return "channel_degraded"
	case "key_disabled":
		return "channel_key_disabled"
	case "model_disabled":
		return "channel_model_disabled"
	case "low_balance":
		return "balance_low"
	case "quota_exceeded":
		return "quota_exceeded"
	case "mismatch":
		return "billing_mismatch"
	case "billing_failed":
		return "billing_failed"
	case "backup_failed":
		return "backup_failed"
	case "archive_failed":
		return "archive_failed"
	case "task_failed":
		return "task_failed"
	case "task_recovered":
		return "task_recovered"
	case "update_available":
		return "update_available"
	case "test":
		return "test"
	default:
		return "system_alert"
	}
}

// AlertDispatcher 把一条告警外发到设置里启用的渠道(webhook/telegram/email), 由启动时注入(见 cmd/start.go)。
// 仍然保留注入而不是直接调 alert.Send: alert 读设置要经 op, 让 cmd 单向接线可以让 op 的
// 单元测试在没有任何网络的情况下运行, 也让"忘接线路由"这一静默失效点可测。
// 返回是否至少有一个渠道发送成功。
var AlertDispatcher func(ctx context.Context, alert alert.Alert) bool

// alertDispatchMissingOnce 让「未接线」只提示一次, 避免每条告警都刷日志。
var alertDispatchMissingOnce sync.Once

// sendMultiChannelAlert 把告警交给注入的分发器; 分发器内部按 alert_channels 决定实际渠道。
func sendMultiChannelAlert(ctx context.Context, out alert.Alert) bool {
	if AlertDispatcher == nil {
		// 未接线时告警只会留在站内, 配好的 Telegram/Email 收不到。
		// 这正是曾经的静默失效点, 故留下一条可见日志而不是直接 return。
		alertDispatchMissingOnce.Do(func() {
			log.Warnf("alert dispatcher not injected: 站内告警不会外发到配置的告警渠道 (event=%s)", out.Event)
		})
		return false
	}
	return AlertDispatcher(ctx, out)
}

// alertWebhookConfigured 判断告警 Webhook 是否已配置有效地址, 供设置页与健康页展示。
func alertWebhookConfigured(rawURL string) bool {
	if rawURL == "" {
		return false
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return false
	}
	return strings.HasPrefix(rawURL, "http://") || strings.HasPrefix(rawURL, "https://")
}

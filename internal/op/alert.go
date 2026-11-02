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
	outbound := outboundAlert(record, facts.Fields)
	// 外发要在调用方返回之后继续, 故脱掉请求的取消信号(保留其中的值):
	// 直接用请求 ctx 的话, HTTP 处理函数一返回 ctx 就被取消, 发送会半路失败。
	sendCtx := context.WithoutCancel(ctx)
	// 分发器在 spawn 之前捕获: 派发协程不再并发读 AlertDispatcher 全局(注入方/测试可能改写它),
	// 在途派发稳定走 spawn 时接好的那条线路。
	dispatcher := alertDispatchDetailed()
	// 在途计数在 spawn 之前登记: Wait 与协程内的 Add 并发不构成同步, 必须先 Add 再 go。
	alertDispatchPending.Add(1)
	go func() {
		defer alertDispatchPending.Done()
		ok, sendErr := alertSendDetailedWithRetry(dispatcher, sendCtx, outbound, alertDispatchRetryWait)
		markAlertDelivery(record.ID, ok, sendErr)
	}()

	return true
}

// outboundAlert 由落库的告警记录还原外发报文; 首次投递与补发共用同一份构造,
// 保证补发出去的内容与首发完全一致(补发时 facts 已不在手上, 只能靠落库字段还原)。
func outboundAlert(record model.ChannelAlert, fields []alert.Field) alert.Alert {
	return alert.Alert{
		Event:     alert.Event(determineAlertEvent(record.Kind)),
		Kind:      record.Kind,
		Scope:     record.Subject,
		Message:   record.Reason,
		Fields:    fields,
		Timestamp: record.CreatedAt,
	}
}

// outboundAlertFromRecord 补发用: 明细只剩落库的那段文本, 直接作为单行正文交给各出口。
// 补发的目的是"让管理员收到这条告警", 而不是还原一份像素级相同的排版。
func outboundAlertFromRecord(record model.ChannelAlert) alert.Alert {
	fields := []alert.Field{}
	if strings.TrimSpace(record.Detail) != "" {
		fields = append(fields, alert.Field{Label: "明细", Value: record.Detail})
	}
	return outboundAlert(record, fields)
}

// 告警补发策略常量。
//
// 首发失败后进入补发队列: 按指数退避排下一次, 累计尝试到上限即放弃(记录保留可查)。
// 这类失败几乎都是外部原因(网络抖动、上游服务短暂不可用、实体解析被拒后降级),
// 与"额度耗尽""token 没配"这类永久性配置错误性质不同 —— 后者由 LastNotifyError 呈现给
// 管理员, 补发任务照常按上限放弃, 不会无限重试。
const (
	// alertRedeliverMaxAttempts 单条告警累计外发轮数上限(含首发)。
	// 3 轮即时重试 + 8 轮补发, 覆盖半小时到数小时量级的网络故障窗口。
	alertRedeliverMaxAttempts = 8
	// alertRedeliverBase 补发退避基数: 第 n 次补发等 base*2^(n-1), 上限 alertRedeliverMaxDelay。
	alertRedeliverBase = 2 * time.Minute
	// alertRedeliverMaxDelay 单次退避上限, 防止指数增长到几天后才重试。
	alertRedeliverMaxDelay = 2 * time.Hour
	// alertRedeliverBatch 单轮补发的最大条数, 避免积压时长时间占住任务协程。
	alertRedeliverBatch = 50
	// alertRedeliverMinAge 只补发超过该时长的失败记录: 留出即时重试窗口,
	// 避免补发任务与首发协程抢同一条记录(同一条告警被外发两次)。
	alertRedeliverMinAge = time.Minute
)

// nextAlertNotifyDelay 计算第 attempts 次失败后的补发退避时长。
func nextAlertNotifyDelay(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delay := alertRedeliverBase * time.Duration(1<<(attempts-1))
	if delay > alertRedeliverMaxDelay || delay <= 0 {
		return alertRedeliverMaxDelay
	}
	return delay
}

// markAlertDelivery 回填一条告警的外发结果: 送达则记时刻并清空失败信息,
// 失败则累计尝试次数、记下原因并按退避排下一次补发。
func markAlertDelivery(id uint64, ok bool, err error) {
	now := time.Now()
	if ok {
		db.GetDB().Model(&model.ChannelAlert{}).Where("id = ?", id).Updates(map[string]any{
			"notified":          true,
			"notified_at":       now,
			"next_notify_at":    nil,
			"last_notify_error": "",
		})
		return
	}
	// 失败: 先把尝试次数读出来再算退避 —— 退避时长取决于已经失败过几轮。
	// COALESCE 兜底: AutoMigrate 补列时存量行是 NULL, 直接扫进 int 会报
	// "converting NULL to int is unsupported", 让本条告警的退避排期彻底丢失。
	record := model.ChannelAlert{}
	if loadErr := db.GetDB().Select("id, COALESCE(notify_attempts, 0) AS notify_attempts").Where("id = ?", id).First(&record).Error; loadErr != nil {
		log.Warnf("alert redeliver: load alert %d failed: %v", id, loadErr)
		return
	}
	attempts := record.NotifyAttempts + 1
	updates := map[string]any{
		"notify_attempts":   attempts,
		"last_notify_error": truncateReason(errText(err)),
	}
	// 达到上限即停止补发(排期置空), 但保留记录与失败原因供管理员排查。
	if attempts < alertRedeliverMaxAttempts {
		updates["next_notify_at"] = now.Add(nextAlertNotifyDelay(attempts))
	} else {
		updates["next_notify_at"] = nil
		log.Errorf("alert redeliver: giving up on alert %d after %d attempts: %s", id, attempts, errText(err))
	}
	if updateErr := db.GetDB().Model(&model.ChannelAlert{}).Where("id = ?", id).Updates(updates).Error; updateErr != nil {
		log.Warnf("alert redeliver: update alert %d failed: %v", id, updateErr)
	}
}

// errText 取错误文本; nil 安全。
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// AlertRedeliverPending 重试尚未送达的告警, 返回本轮成功补发与放弃的条数。
//
// 这是"告警发不出去没人知道"这一静默失效点的收口: 2026-11-02 实测 260 条告警因 Telegram
// 实体解析 400 全部丢失, 而系统没有任何重试入口。现在失败会排期重发, 直到送达或达到上限。
func AlertRedeliverPending(ctx context.Context) (sent int, gaveUp int, err error) {
	dispatcher := alertDispatchDetailed()
	if dispatcher == nil {
		// 未接线时补发同样无意义, 但不能静默: 这正是需要管理员知道的失效状态。
		alertDispatchMissingOnce.Do(func() {
			log.Warnf("alert redeliver: dispatcher not injected, pending alerts cannot be resent")
		})
		return 0, 0, fmt.Errorf("alert dispatcher not injected")
	}
	rows := []model.ChannelAlert{}
	// 只取"未送达 + 已排期 + 到点 + 未超上限"的记录。冷却 alertRedeliverMinAge 是为了
	// 不与首发协程抢同一条: 首发失败后才写 next_notify_at, 而 next_notify_at 最小也在
	// alertRedeliverBase 之后, 故正常路径不会重复外发。
	if err := db.GetDB().WithContext(ctx).
		Where("notified = ? AND next_notify_at IS NOT NULL AND next_notify_at <= ? AND COALESCE(notify_attempts, 0) < ?", false, time.Now(), alertRedeliverMaxAttempts).
		Order("next_notify_at ASC").Limit(alertRedeliverBatch).
		Find(&rows).Error; err != nil {
		return 0, 0, fmt.Errorf("load pending alerts: %w", err)
	}
	for _, record := range rows {
		if ctx.Err() != nil {
			return sent, gaveUp, ctx.Err()
		}
		ok, sendErr := alertSendDetailedWithRetry(dispatcher, ctx, outboundAlertFromRecord(record), alertDispatchRetryWait)
		markAlertDelivery(record.ID, ok, sendErr)
		if ok {
			sent++
			log.Infof("alert redeliver: alert %d (%s) delivered after %d attempts", record.ID, record.Kind, record.NotifyAttempts+1)
		} else if record.NotifyAttempts+1 >= alertRedeliverMaxAttempts {
			gaveUp++
		}
	}
	return sent, gaveUp, nil
}

// AlertUndeliveredList 返回尚未送达的告警, 按排期先后排序。
// 已放弃的排在最后(排期为空), 因为它们只留作排查证据, 不再有动作。
func AlertUndeliveredList(ctx context.Context, limit int) ([]model.ChannelAlert, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows := []model.ChannelAlert{}
	if err := db.GetDB().WithContext(ctx).
		Where("notified = ?", false).
		Order("next_notify_at IS NULL ASC"). // false(有排期) 排在 true(已放弃) 之前
		Order("next_notify_at ASC").
		Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// AlertDeliveryStats 告警送达概览: 管理员需要一眼看到"有没有告警没送出去", 而不是翻日志。
type AlertDeliveryStats struct {
	// Pending 已排期待补发的条数。
	Pending int64 `json:"pending"`
	// GaveUp 达到重试上限后放弃的条数: 这些告警只存在于站内, 永远送不出去了。
	GaveUp int64 `json:"gave_up"`
	// Failed24h 最近 24 小时外发失败的条数(含已补发成功的)。
	Failed24h int64 `json:"failed_24h"`
	// Delivered24h 最近 24 小时送达成功的条数。
	Delivered24h int64 `json:"delivered_24h"`
	// LastError 最近一条失败原因, 供界面直接显示。
	LastError string `json:"last_error"`
	// LastErrorAt 最近一条失败发生的时间。
	LastErrorAt *time.Time `json:"last_error_at,omitempty"`
}

// AlertDeliveryStatsGet 汇总告警送达情况。无数据时各项为 0 而不是报错。
func AlertDeliveryStatsGet(ctx context.Context) (AlertDeliveryStats, error) {
	stats := AlertDeliveryStats{}
	conn := db.GetDB().WithContext(ctx)
	since := time.Now().Add(-24 * time.Hour)

	if err := conn.Model(&model.ChannelAlert{}).
		Where("notified = ? AND next_notify_at IS NOT NULL", false).
		Count(&stats.Pending).Error; err != nil {
		return stats, fmt.Errorf("count pending alerts: %w", err)
	}
	// 放弃 = 未送达 + 没有排期 + 尝试过(排除"从未尝试过外发"的历史脏数据)。
	if err := conn.Model(&model.ChannelAlert{}).
		Where("notified = ? AND next_notify_at IS NULL AND COALESCE(notify_attempts, 0) >= ?", false, alertRedeliverMaxAttempts).
		Count(&stats.GaveUp).Error; err != nil {
		return stats, fmt.Errorf("count gave-up alerts: %w", err)
	}
	if err := conn.Model(&model.ChannelAlert{}).
		Where("notified = ? AND created_at >= ?", true, since).
		Count(&stats.Delivered24h).Error; err != nil {
		return stats, fmt.Errorf("count delivered alerts: %w", err)
	}
	if err := conn.Model(&model.ChannelAlert{}).
		Where("last_notify_error <> '' AND created_at >= ?", since).
		Count(&stats.Failed24h).Error; err != nil {
		return stats, fmt.Errorf("count failed alerts: %w", err)
	}
	last := model.ChannelAlert{}
	if err := conn.Where("last_notify_error <> ''").Order("id DESC").First(&last).Error; err == nil {
		stats.LastError = last.LastNotifyError
		stats.LastErrorAt = &last.CreatedAt
	}
	return stats, nil
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

// AlertDispatcherDetailed 与 AlertDispatcher 同义, 但把失败原因带出来。
// 补发机制要知道"为什么没送到": token 没配这种永久性错误重试多少次都没用,
// 而实体解析、网络抖动这类是可能自愈的。两个变量都注入, 老路径行为不变。
var AlertDispatcherDetailed func(ctx context.Context, alert alert.Alert) (bool, error)

// alertDispatchDetailed 取当前生效的细致分发器。
// 只注入了老的单值版本时(测试与既有接线), 包一层适配器把 false 记成"无渠道接受",
// 这样补发路径在任何接线形态下都能工作, 不必强求调用方同时注入两个变量。
func alertDispatchDetailed() func(context.Context, alert.Alert) (bool, error) {
	if AlertDispatcherDetailed != nil {
		return AlertDispatcherDetailed
	}
	if AlertDispatcher == nil {
		return nil
	}
	plain := AlertDispatcher
	return func(ctx context.Context, out alert.Alert) (bool, error) {
		if plain(ctx, out) {
			return true, nil
		}
		return false, fmt.Errorf("no alert channel accepted the message")
	}
}

// alertDispatchMissingOnce 让「未接线」只提示一次, 避免每条告警都刷日志。
var alertDispatchMissingOnce sync.Once

// alertDispatchPending 跟踪在途的异步外发协程。
// 记账规则: Add 必须在 spawn 之前、Done 在协程收尾 —— 协程内 Add 与 Wait 并发不构成同步,
// 等待方可能在协程起步前就返回, 收尾时改写 AlertDispatcher 会与在途协程互相踩。
var alertDispatchPending sync.WaitGroup

// 告警外发失败只做少量重试，避免瞬时网络抖动造成漏告警，又不让后台协程长期滞留。
const (
	alertDispatchMaxAttempts = 3
	alertDispatchRetryBase   = 100 * time.Millisecond
)

// alertDispatchWait 等待全部在途外发跑完: 返回后不再有派发协程读写 AlertDispatcher 全局,
// notified 回填也已落库。测试收尾/优雅退出用。
func alertDispatchWait() {
	alertDispatchPending.Wait()
}

// sendMultiChannelAlert 把告警交给注入的分发器; 分发器内部按 alert_channels 决定实际渠道。
func sendMultiChannelAlert(ctx context.Context, out alert.Alert) bool {
	return alertSendWith(AlertDispatcher, ctx, out)
}

// alertDispatchRetryWait 是生产环境的可注入等待器, 测试可替换为零等待以保持快速。
var alertDispatchRetryWait = func(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// alertSendDetailedWithRetry 同 alertSendWithRetry, 但保留最后一次失败原因。
// 返回 (是否送达, 失败原因); 成功时原因为空。
func alertSendDetailedWithRetry(dispatcher func(context.Context, alert.Alert) (bool, error), ctx context.Context, out alert.Alert, wait func(context.Context, time.Duration) error) (bool, error) {
	if dispatcher == nil {
		alertDispatchMissingOnce.Do(func() {
			log.Warnf("alert dispatcher not injected: 站内告警不会外发到配置的告警渠道 (event=%s)", out.Event)
		})
		return false, fmt.Errorf("alert dispatcher not injected")
	}
	var lastErr error
	for attempt := 1; attempt <= alertDispatchMaxAttempts; attempt++ {
		ok, err := dispatcher(ctx, out)
		if ok {
			return true, nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("no alert channel accepted the message")
		}
		if attempt == alertDispatchMaxAttempts {
			break
		}
		delay := alertDispatchRetryBase * time.Duration(1<<(attempt-1))
		if waitErr := wait(ctx, delay); waitErr != nil {
			return false, lastErr
		}
	}
	return false, lastErr
}

// alertSendWithRetry 在有限次数内按指数退避重试失败的外发。
func alertSendWithRetry(dispatcher func(context.Context, alert.Alert) bool, ctx context.Context, out alert.Alert, wait func(context.Context, time.Duration) error) bool {
	for attempt := 1; attempt <= alertDispatchMaxAttempts; attempt++ {
		if alertSendWith(dispatcher, ctx, out) {
			return true
		}
		if attempt == alertDispatchMaxAttempts {
			break
		}
		delay := alertDispatchRetryBase * time.Duration(1<<(attempt-1))
		if err := wait(ctx, delay); err != nil {
			return false
		}
	}
	return false
}

// alertSendWith 用指定分发器发送一条告警; dispatcher 为 nil 表示未接线。
// 派发协程传 spawn 前捕获的分发器, 同步调用点传全局, 两边共用同一条发送语义。
func alertSendWith(dispatcher func(context.Context, alert.Alert) bool, ctx context.Context, out alert.Alert) bool {
	if dispatcher == nil {
		// 未接线时告警只会留在站内, 配好的 Telegram/Email 收不到。
		// 这正是曾经的静默失效点, 故留下一条可见日志而不是直接 return。
		alertDispatchMissingOnce.Do(func() {
			log.Warnf("alert dispatcher not injected: 站内告警不会外发到配置的告警渠道 (event=%s)", out.Event)
		})
		return false
	}
	return dispatcher(ctx, out)
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

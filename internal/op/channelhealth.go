package op

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/alert"
	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/netguard"
)

// channelHealthClient 专用探测 HTTP 客户端: 超时较短, 挂 DialGuard 防私网 SSRF 且禁止跟随重定向。
// 原实现是裸 http.Client(默认跟随最多 10 跳重定向、无拨号复核), 渠道商可用 302 → 169.254.169.254
// 借健康检查打内网。此处不能 import rhttp(会与 op 形成循环依赖), 故直接用 netguard 构建。
var channelHealthClient = func() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = netguard.DialGuard{AllowPrivate: AllowPrivateUpstream, Next: transport.DialContext}.DialContext
	return &http.Client{
		Timeout:   15 * time.Second,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}()

// alertFailStreakDefault 连续失败次数阈值默认值: 达到此数才判定宕机, 避免单次抖动误报。
// 实际取值来自设置 alert_fail_streak, 未配置时回退到此默认值。
const alertFailStreakDefault = 5

// ChannelHealthCheckAll 对所有启用渠道逐一探测并记录结果。
func ChannelHealthCheckAll(ctx context.Context) error {
	channels := []model.Channel{}
	if err := db.GetDB().WithContext(ctx).Where("enabled = ?", true).Find(&channels).Error; err != nil {
		return fmt.Errorf("load channels: %w", err)
	}
	for _, channel := range channels {
		checkOne(ctx, channel)
	}
	return nil
}

// checkOne 探测单个渠道并落库健康记录, 随后评估是否触发告警。
//
// 探测口径是「凭据是否有效」: 带真实凭据请求 models 端点, 只有 2xx 才算健康。
// 改造前用的是不带凭据的 GET chat/completions, 结果是 401/403/404/405 一律判健康 ——
// 生产 21717 次探测里有 12474 次拿到 4xx 却记为健康, 这个健康位对「渠道还能不能用」没有信息量。
//
// 401/403/404/405 不是渠道故障, 而是「这条探测路径未被上游以该形态支持」, 记为未知(Healthy=false, 但带 authentication_failed / models_endpoint_not_found 等分类说明),
// 不参与宕机判定: 否则上游 provider 只要不实现 /models, 就会每 30 分钟误报一次宕机。
// 5xx 与传输层错误才是真故障。
func checkOne(ctx context.Context, channel model.Channel) {
	record := model.ChannelHealthRecord{
		ChannelID:   channel.ID,
		ChannelCode: channel.ShareCode,
		CheckedAt:   time.Now(),
	}
	start := time.Now()
	statusCode, err := probeChannelModels(ctx, channel)
	record.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		record.Error = err.Error()
	} else {
		record.StatusCode = statusCode
		record.Healthy = statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices
		if !record.Healthy {
			record.Error = classifyHealthStatus(statusCode)
		}
	}
	saveHealthRecord(record)
	evaluateAlerts(ctx, channel, record, probeUnknown(record))
}

// probeUnknown 判断本次探测是否「口径不支持」而非「渠道故障」。
// 4xx 说明上游活着并给出了明确答复, 只是这个探测形态没被支持: 凭据可能有效, 只是 /models 未开放。
// 这类结果既不判健康也不判宕机, 只记录事实; 缺凭据同理(渠道本就没配对凭据, 不是上游故障)。
func probeUnknown(record model.ChannelHealthRecord) bool {
	if record.Healthy {
		return false
	}
	if record.StatusCode >= http.StatusBadRequest && record.StatusCode < http.StatusInternalServerError {
		return true
	}
	return strings.HasPrefix(record.Error, "no enabled channel key available")
}

// probeChannelModels 使用与 ProbeChannelModels 相同的 models URL 和鉴权形态。
// 凭据只从进程内缓存读取, 健康记录与错误文本绝不包含密钥或响应体。
// 讯飞 MaaS 的模型列表地址从 Chat 路径推导, 且需 V2 签名(appID/apiSecret), 因此单独走签名分支。
func probeChannelModels(ctx context.Context, channel model.Channel) (int, error) {
	var key model.ChannelKey
	for _, candidate := range ChannelKeysByChannel(channel.ID) {
		if candidate.Enabled && candidate.DisabledUntil == nil && candidate.Key != "" {
			key = candidate
			break
		}
	}
	if key.Key == "" {
		return 0, fmt.Errorf("no enabled channel key available")
	}
	modelsPath := channel.OpenAIResponsePath
	if channel.Dialect == model.DialectXunfei {
		modelsPath = channel.OpenAIChatCompletionPath
	}
	url := modelsURL(channel.BaseURL, modelsPath)
	req, err := newOpenAIModelsRequest(ctx, channel.ChannelConfig, key.Key, key.APISecret, url)
	if err != nil {
		return 0, err
	}
	resp, err := channelHealthClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

func classifyHealthStatus(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return "authentication_failed (HTTP 401)"
	case http.StatusForbidden:
		return "authorization_failed (HTTP 403)"
	case http.StatusNotFound:
		return "models_endpoint_not_found (HTTP 404)"
	case http.StatusMethodNotAllowed:
		return "models_endpoint_method_not_allowed (HTTP 405)"
	default:
		return fmt.Sprintf("upstream_http_error (HTTP %d)", status)
	}
}

// saveHealthRecord 落库探测结果, 只保留最近 1000 条避免无限增长。
func saveHealthRecord(record model.ChannelHealthRecord) {
	if err := db.GetDB().Create(&record).Error; err != nil {
		return
	}
	db.GetDB().Where("channel_id = ?", record.ChannelID).
		Where("id NOT IN (SELECT id FROM channel_health_records WHERE channel_id = ? ORDER BY id DESC LIMIT 1000)", record.ChannelID).
		Delete(&model.ChannelHealthRecord{})
}

// evaluateAlerts 基于本次探测结果与历史记录评估告警(带抖动抑制):
// 连续 alert_fail_streak 次异常才判定 down; 恢复时若最近告警是 down/degraded 则补发 recovered;
// 健康但延迟超过 health_latency_ms 阈值则发 degraded。同类型告警的去重由 RaiseAlert 负责。
//
// unknown(探测形态不支持/无可用凭据)整轮跳过: 既不进宕机连击, 也不补 recovered ——
// 404 证明不了渠道故障, 但也证明不了已恢复, 只留记录等下一次有信息量的探测。
// 历史连击窗口同理: 窗口里出现未知探测就中断连击, 否则「N 次连续失败探测」的告警文案是假的。
//
// 主体一律留空交给 resolveSubject 补真实渠道名 —— 改造前传的是 ShareCode,
// 管理员在 Telegram 里收到的是"8fKd2xQ 渠道告警"这种读不出来的随机码。共享编码仍进 ChannelCode 列。
func evaluateAlerts(ctx context.Context, channel model.Channel, record model.ChannelHealthRecord, unknown bool) {
	if unknown {
		return
	}
	if record.Healthy {
		// 延迟告警: 健康但超过阈值; 阈值 0 表示关闭该规则。
		threshold := SettingGetIntDefault(model.SettingKeyHealthLatencyMS, 1000)
		if threshold > 0 && record.LatencyMs >= int64(threshold) {
			RaiseAlertFacts(ctx, AlertFacts{
				ChannelID: channel.ID,
				Kind:      "degraded",
				Reason:    fmt.Sprintf("probe latency over threshold %dms", threshold),
				Fields: healthFields(channel, record, []alert.Field{
					{Label: "延迟阈值", Value: fmt.Sprintf("%d ms", threshold)},
					{Label: "说明", Value: "渠道仍可响应, 只是明显变慢"},
				}),
			})
			return
		}
		// 恢复: 本渠道最近一条告警是 down 或 degraded 时补发 recovered。
		last := model.ChannelAlert{}
		err := db.GetDB().WithContext(ctx).
			Where("channel_id = ?", channel.ID).
			Order("id DESC").First(&last).Error
		if err == nil && (last.Kind == "down" || last.Kind == "degraded") {
			if RaiseAlertFacts(ctx, AlertFacts{
				ChannelID: channel.ID,
				Kind:      "recovered",
				Reason:    "channel healthy again",
				Fields: healthFields(channel, record, []alert.Field{
					{Label: "此前告警", Value: alertKindLabel(last.Kind)},
				}),
			}) {
				NotifyChannelStatusAsync(channel.UserID, channel.ID, channel.Name, true)
			}
		}
		return
	}

	// 异常: 连续 N 次都失败才视为宕机, 单次抖动不告警; N 由 alert_fail_streak 配置。
	streak := SettingGetIntDefault(model.SettingKeyAlertFailStreak, alertFailStreakDefault)
	if streak < 1 {
		streak = 1
	}
	recent := []model.ChannelHealthRecord{}
	if err := db.GetDB().WithContext(ctx).
		Where("channel_id = ?", channel.ID).
		Order("id DESC").Limit(streak).
		Find(&recent).Error; err != nil {
		return
	}
	if len(recent) < streak {
		return
	}
	for _, r := range recent {
		if r.Healthy || probeUnknown(r) {
			return
		}
	}
	if RaiseAlertFacts(ctx, AlertFacts{
		ChannelID: channel.ID,
		Kind:      "down",
		Reason:    fmt.Sprintf("%d consecutive failed probes", streak),
		Fields: healthFields(channel, record, []alert.Field{
			{Label: "连续失败", Value: fmt.Sprintf("%d 次", streak)},
		}),
	}) {
		NotifyChannelStatusAsync(channel.UserID, channel.ID, channel.Name, false)
	}
}

// healthFields 组装探测类告警的公共明细行, 末尾再接该告警特有的几行。
// 管理员需要一眼看到"哪家渠道、探的哪个地址、这次到底怎么了", 而不是只有一句结论。
func healthFields(channel model.Channel, record model.ChannelHealthRecord, extra []alert.Field) []alert.Field {
	status := "无响应"
	if record.StatusCode > 0 {
		status = fmt.Sprintf("HTTP %d", record.StatusCode)
	}
	fields := []alert.Field{
		{Label: "渠道", Value: channelDisplayName(channel)},
		{Label: "探测地址", Value: channel.BaseURL},
		{Label: "延迟", Value: fmt.Sprintf("%d ms", record.LatencyMs)},
		{Label: "状态", Value: status},
	}
	// 没有错误就不写"探测错误"这行: 降级告警里出现一句占位的错误描述,
	// 管理员会以为是探测本身失败了, 而实际只是慢。
	if record.Error != "" {
		fields = append(fields, alert.Field{Label: "探测错误", Value: record.Error})
	}
	fields = append(fields, alert.Field{Label: "探测时间", Value: record.CheckedAt.Format("2006-01-02 15:04:05")})
	return append(fields, extra...)
}

// alertKindLabel 站内告警类型的中文说明(恢复告警里回指此前那条用)。
func alertKindLabel(kind string) string {
	switch kind {
	case "down":
		return "渠道故障"
	case "degraded":
		return "渠道延迟降级"
	case "key_disabled":
		return "渠道凭据熔断"
	case "model_disabled":
		return "渠道模型熔断"
	}
	return kind
}

// ChannelHealthList 返回某渠道的最近健康记录。
func ChannelHealthList(ctx context.Context, channelID int, limit int) ([]model.ChannelHealthRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows := []model.ChannelHealthRecord{}
	query := db.GetDB().WithContext(ctx).Order("checked_at DESC")
	if channelID > 0 {
		query = query.Where("channel_id = ?", channelID)
	}
	if err := query.Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

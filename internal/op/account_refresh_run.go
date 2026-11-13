
package op

// 账号令牌刷新任务的批量执行体。
//
// 职责边界: 单条刷新走 AccountRefresh(带锁、原子回写); 这里只管"挑出该刷的、逐条刷、
// 把结果分类并让失败可见"。分类是关键 —— 把"账号已失效"混进"网络抖动"里重试,
// 就等于每轮都拿一个作废的 refresh_token 去打上游, 既无意义又会招来风控。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/alert"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/charmbracelet/log"
)

// AccountRefreshRunResult 一轮刷新的结果, 供任务日志与告警使用。
type AccountRefreshRunResult struct {
	Checked   int // 本轮判定为到期待刷新的账号数
	Refreshed int // 成功刷新数
	Failed    int // 可重试的失败(网络抖动、上游 5xx)
	Expired   int // 账号确定失效(invalid_grant 等), 已标记失效
}

// AccountRefreshBatchLimit 单轮最多处理多少条。
// 账号多时一轮刷完可能占用很长时间, 有界批次让本轮按时结束、剩余的留给下一轮,
// 避免任务协程被长时间占住从而拖住其它定时任务。
const AccountRefreshBatchLimit = 50

// AccountRefreshRun 执行一轮账号令牌刷新。
func AccountRefreshRun(ctx context.Context, now time.Time) (AccountRefreshRunResult, error) {
	result := AccountRefreshRunResult{}

	due := AccountRefreshesDue(now)
	if len(due) == 0 {
		return result, nil
	}
	if len(due) > AccountRefreshBatchLimit {
		due = due[:AccountRefreshBatchLimit]
	}
	result.Checked = len(due)

	for _, channelKey := range due {
		if err := ctx.Err(); err != nil {
			// 上下文到期(任务超时): 已处理的结果保留, 其余留给下一轮。
			return result, err
		}
		err := AccountRefresh(ctx, channelKey.ID, now)
		if err == nil {
			result.Refreshed++
			if channelKey.TokenExpiresAt == nil {
				// 首次刷新成功: 说明这个账号此前从未被刷过, 记一条便于确认导入真的生效了。
				log.Infof("account token refresh: channel_key=%d first refresh ok", channelKey.ID)
			}
			continue
		}

		if accountRefreshShouldInvalidate(err) {
			// 账号已失效: 标记凭据失效并告警, 不再重试。
			// 不直接把 Enabled 置 false 的原因: 那会让运营以为"是自己关的";
			// 走既有的熔断通道(auth 类错误按硬熔断时长隔离)语义一致, 且能在界面上看到原因。
			result.Expired++
			invalidateReason := err.Error()
			RecordChannelKeyError(channelKey.ID, "auth")
			raiseAccountInvalidatedAlert(channelKey, invalidateReason)
			continue
		}

		result.Failed++
		// 可重试失败只记日志, 交给任务框架的连续失败计数与告警出口:
		// 单次网络抖动不该惊动运营, 连续失败才是真故障。
		log.Warnf("account token refresh: channel_key=%d retryable failure: %v", channelKey.ID, err)
	}

	// 只有"全部到期的都刷不动"才算这轮任务失败; 部分失败由上面的告警与日志负责。
	// 这样任务失败计数才有意义(它触发的是"任务整体不可用"级别的告警)。
	if result.Checked > 0 && result.Refreshed == 0 && result.Failed > 0 {
		return result, fmt.Errorf("account refresh made no progress: checked=%d failed=%d", result.Checked, result.Failed)
	}
	return result, nil
}

// accountRefreshShouldInvalidate 判断错误是否表示"这个账号已经没救了"。
//
// 只有这一类才标记失效并停手。其余(网络错误、5xx)都按可重试处理 —— 把可重试错误当成失效,
// 会让一次上游抖动就永久摘掉一个还有效的账号。
func accountRefreshShouldInvalidate(err error) bool {
	// 回写失败绝不标记账号失效: 令牌其实已经换到了, 只是没存下来。
	// 这时最该做的是立刻让运营介入从库里救回新令牌 —— 若能重试写库更好, 但至少不能误判成账号坏了。
	var persistErr *AccountRefreshPersistError
	if errors.As(err, &persistErr) {
		return false
	}
	var refreshErr *AccountRefreshError
	if !errors.As(err, &refreshErr) {
		return false
	}
	// 4xx 里只有明确表示"凭据无效"的才作废。401/403 也可能是上游临时抽风,
	// 但它们通常伴随 invalid_grant/invalid_client, 以响应体为准更可靠。
	body := strings.ToLower(refreshErr.Body)
	for _, token := range []string{"invalid_grant", "invalid_client", "invalid_token", "unauthorized_client", "refresh token expired"} {
		if strings.Contains(body, token) {
			return true
		}
	}
	// 400 且响应体没给可判定的关键词: 仍按可重试 —— 宁可多试一轮, 也别误杀一个有效账号。
	return false
}

// raiseAccountInvalidatedAlert 账号确定失效时告警。
//
// 必须告警: 这类失效无法自愈(续期令牌已作废), 只能人工重新导入。若只留一行日志,
// 表现就是"某几个账号悄悄不再参与轮巡", 要等到用户投诉才发现。
func raiseAccountInvalidatedAlert(channelKey model.ChannelKey, reason string) {
	subject := channelKey.Name
	if channelKey.AccountEmail != "" {
		subject = channelKey.AccountEmail
	}
	RaiseAlertFacts(context.Background(), AlertFacts{
		ChannelID: channelKey.ChannelID,
		Kind:      "account_invalidated",
		Subject: subject,
		Reason:  fmt.Sprintf("上游账号续期令牌已失效, 需要重新导入 (平台=%s)", channelKey.AccountPlatform),
		Fields: []alert.Field{
			{Label: "渠道凭据", Value: channelKey.Name},
			{Label: "平台", Value: channelKey.AccountPlatform},
			{Label: "错误", Value: accountBodyExcerpt(reason)},
		},
	})
}

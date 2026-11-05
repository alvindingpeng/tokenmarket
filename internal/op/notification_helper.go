package op

import (
	"context"
	"fmt"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/charmbracelet/log"
)

// notifyAsync 异步投递站内通知。
//
// 通知是"锦上添花"而不是业务事实的一部分: 余额已扣、订单已入账, 这些事实在调用方
// 拿到返回值时就已经成立。如果通知写入失败却让调用方以为整件事失败了, 用户就会看到
// "充值失败"而钱其实到了 —— 比没有通知糟糕得多。所以这里吞掉所有错误, 只记日志。
//
// 独立 goroutine + 独立超时: 通知写库慢或数据库压力大时, 不该把主流程的响应一起拖住。
func notifyAsync(ctx context.Context, userID uint, notifType model.NotificationType, title, content, link string, relatedID uint64) {
	if userID == 0 {
		return
	}
	go func() {
		// 用独立 context: 调用方的 ctx 可能已经随请求结束而取消, 那时通知就再也发不出去了。
		notifyCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := NotificationCreateDedup(notifyCtx, userID, notifType, title, content, link, relatedID); err != nil {
			log.Warnf("notification create failed: user=%d type=%s err=%v", userID, notifType, err)
		}
	}()
}

// NotifyBalanceLow 发送余额不足通知。
func NotifyBalanceLow(ctx context.Context, userID uint, balance float64) error {
	return NotificationCreate(ctx, userID, model.NotificationTypeBalanceLow,
		"余额不足",
		fmt.Sprintf("您的账户余额已不足 %.2f 元，请及时充值以免影响服务使用。", balance),
		"", 0)
}

// NotifyRateLimitHit 发送限流触发通知。
func NotifyRateLimitHit(ctx context.Context, userID uint, scope, name string) error {
	return NotifyRateLimitHitForScope(ctx, userID, scope, name, 0)
}

// NotifyRateLimitHitForScope 按具体限流实体去重; 同一用户多个 API Key 或渠道不会互相吞通知。
func NotifyRateLimitHitForScope(ctx context.Context, userID uint, scope, name string, relatedID uint64) error {
	dedupKey := fmt.Sprintf("%s:%d", scope, relatedID)
	_, err := createNotificationDedupKey(ctx, userID, model.NotificationTypeRateLimitHit,
		"限流触发",
		fmt.Sprintf("您的 %s（%s）已触发限流，请稍后再试或调整限流配置。", scope, name),
		dedupKey, relatedID)
	return err
}

// rateLimitScopeLabel 返回用户可理解的限流归属。
func rateLimitScopeLabel(scope string) string {
	if scope == "api_key" {
		return "API Key 级"
	}
	return "账户级"
}

// NotifyRateLimitHitAsync 为 429 拒绝发送去重通知; 不等待通知落库也不传递错误到主请求。
func NotifyRateLimitHitAsync(userID uint, modelName string, scope string, relatedID uint64) {
	if scope != "user" && scope != "api_key" {
		return
	}
	if userID == 0 {
		return
	}
	name := modelName
	if name == "" {
		name = "请求模型"
	}
	entityID := relatedID
	if scope == "user" {
		entityID = 0 // userID 已是所有者维度, 避免每个模型分别通知。
	}
	dedupKey := fmt.Sprintf("%s:%d", scope, entityID)
	title := "限流触发"
	content := fmt.Sprintf("您对模型 %s 的请求已触发%s限流，请稍后重试。", name, rateLimitScopeLabel(scope))
	go func() {
		notifyCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := createNotificationDedupKey(notifyCtx, userID, model.NotificationTypeRateLimitHit, title, content, dedupKey, entityID); err != nil {
			log.Warnf("notification create failed: user=%d type=%s err=%v", userID, model.NotificationTypeRateLimitHit, err)
		}
	}()
}

// NotifyChannelDown 发送渠道故障通知。
func NotifyChannelDown(ctx context.Context, userID uint, channelID int, channelName string) error {
	return NotifyChannelStatus(ctx, userID, channelID, channelName, false)
}

// NotifyChannelStatus 通知渠道所有者进入故障或恢复状态; 每次状态边沿使用独立去重类型。
func NotifyChannelStatus(ctx context.Context, userID uint, channelID int, channelName string, recovered bool) error {
	title := "渠道故障"
	content := fmt.Sprintf("渠道 %s 出现故障，系统已自动切换到其他可用渠道。", channelName)
	notifType := model.NotificationTypeChannelDown
	if recovered {
		title = "渠道已恢复"
		content = fmt.Sprintf("渠道 %s 已恢复正常。", channelName)
		notifType = model.NotificationTypeChannelRecovered
	}
	_, err := NotificationCreateDedup(ctx, userID, notifType, title, content, "", uint64(channelID))
	return err
}

// NotifyChannelStatusAsync 非阻塞地发送渠道状态通知; 健康巡检不因站内通知失败而失败。
func NotifyChannelStatusAsync(userID uint, channelID int, channelName string, recovered bool) {
	if userID == 0 {
		return
	}
	title := "渠道故障"
	content := fmt.Sprintf("渠道 %s 出现故障。", channelName)
	notifType := model.NotificationTypeChannelDown
	if recovered {
		title = "渠道已恢复"
		content = fmt.Sprintf("渠道 %s 已恢复正常。", channelName)
		notifType = model.NotificationTypeChannelRecovered
	}
	go func() {
		notifyCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := NotificationCreateDedup(notifyCtx, userID, notifType, title, content, "", uint64(channelID)); err != nil {
			log.Warnf("notification create failed: user=%d type=%s err=%v", userID, notifType, err)
		}
	}()
}

// NotifyChannelReviewAsync 非阻塞地发送渠道审核结果通知。
func NotifyChannelReviewAsync(userID uint, channelID int, channelName string, approved bool) {
	if userID == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := NotifyChannelReview(ctx, userID, channelID, channelName, approved); err != nil {
			log.Warnf("notification create failed: user=%d type=%s err=%v", userID, model.NotificationTypeChannelReview, err)
		}
	}()
}

// NotifyChannelReview 发送渠道审核通知。
func NotifyChannelReview(ctx context.Context, userID uint, channelID int, channelName string, approved bool) error {
	status := "已通过"
	if !approved {
		status = "未通过"
	}
	return NotificationCreate(ctx, userID, model.NotificationTypeChannelReview,
		"渠道审核结果",
		fmt.Sprintf("您提交的渠道 %s 审核%s。", channelName, status),
		"", uint64(channelID))
}

// NotifyWithdrawal 发送提现状态变更通知。
func NotifyWithdrawal(ctx context.Context, userID uint, withdrawalID uint, amount float64, status string) error {
	var title, content string
	switch status {
	case "approved":
		title = "提现审核通过"
		content = fmt.Sprintf("您的提现申请（金额 %.2f 元）已审核通过，资金将在 1-3 个工作日内到账。", amount)
	case "rejected":
		title = "提现审核未通过"
		content = fmt.Sprintf("您的提现申请（金额 %.2f 元）未通过审核，如有疑问请联系管理员。", amount)
	case "paid":
		title = "提现已打款"
		content = fmt.Sprintf("您的提现（金额 %.2f 元）已完成打款，请注意查收。", amount)
	default:
		title = "提现申请已提交"
		content = fmt.Sprintf("您的提现申请（金额 %.2f 元）已提交，请等待管理员审核。", amount)
	}
	return NotificationCreate(ctx, userID, model.NotificationTypeWithdrawal,
		title, content, "", uint64(withdrawalID))
}

// NotifyDisputeAsync 非阻塞地发送争议处理进度通知。
func NotifyDisputeAsync(userID uint, disputeID uint64, status string) {
	if userID == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := NotifyDispute(ctx, userID, disputeID, status); err != nil {
			log.Warnf("notification create failed: user=%d type=%s err=%v", userID, model.NotificationTypeDispute, err)
		}
	}()
}

// NotifyDispute 发送争议处理进度通知。
func NotifyDispute(ctx context.Context, userID uint, disputeID uint64, status string) error {
	return NotificationCreate(ctx, userID, model.NotificationTypeDispute,
		"争议处理进度",
		fmt.Sprintf("您的争议工单 #%d 状态已更新为：%s。", disputeID, status),
		"", disputeID)
}

// NotifyTicket 发送工单回复通知。
func NotifyTicket(ctx context.Context, userID uint, ticketID uint64, title, content string) error {
	return NotificationCreate(ctx, userID, model.NotificationTypeTicket,
		title, content, "", ticketID)
}

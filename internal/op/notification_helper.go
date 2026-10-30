package op

import (
	"context"
	"fmt"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// NotifyBalanceLow 发送余额不足通知
func NotifyBalanceLow(ctx context.Context, userID uint, balance float64) error {
	return NotificationCreate(ctx, userID, model.NotificationTypeBalanceLow,
		"余额不足",
		fmt.Sprintf("您的账户余额已不足 %.2f，请及时充值以免影响服务使用。", balance),
		"/wallet",
		0)
}

// NotifyRateLimitHit 发送限流触发通知
func NotifyRateLimitHit(ctx context.Context, userID uint, scope, name string) error {
	return NotificationCreate(ctx, userID, model.NotificationTypeRateLimitHit,
		"限流触发",
		fmt.Sprintf("您的 %s (%s) 已触发限流，请稍后再试或调整限流配置。", scope, name),
		"/ratelimit",
		0)
}

// NotifyChannelDown 发送渠道故障通知
func NotifyChannelDown(ctx context.Context, userID uint, channelID int, channelName string) error {
	return NotificationCreate(ctx, userID, model.NotificationTypeChannelDown,
		"渠道故障",
		fmt.Sprintf("渠道 %s 出现故障，已自动切换到其他可用渠道。", channelName),
		fmt.Sprintf("/channels/%d", channelID),
		uint64(channelID))
}

// NotifyChannelReview 发送渠道审核通知
func NotifyChannelReview(ctx context.Context, userID uint, channelID int, channelName string, approved bool) error {
	status := "已通过"
	if !approved {
		status = "已拒绝"
	}
	return NotificationCreate(ctx, userID, model.NotificationTypeChannelReview,
		"渠道审核结果",
		fmt.Sprintf("您提交的渠道 %s 审核%s。", channelName, status),
		fmt.Sprintf("/channels/%d", channelID),
		uint64(channelID))
}

// NotifyWithdrawal 发送提现审核通知
func NotifyWithdrawal(ctx context.Context, userID uint, withdrawalID uint, amount float64, status string) error {
	var title, content string
	switch status {
	case "approved":
		title = "提现审核通过"
		content = fmt.Sprintf("您的提现申请（金额 %.2f）已审核通过，资金将在 1-3 个工作日内到账。", amount)
	case "rejected":
		title = "提现审核拒绝"
		content = fmt.Sprintf("您的提现申请（金额 %.2f）已被拒绝，如有疑问请联系管理员。", amount)
	default:
		title = "提现申请已提交"
		content = fmt.Sprintf("您的提现申请（金额 %.2f）已提交，请等待管理员审核。", amount)
	}
	return NotificationCreate(ctx, userID, model.NotificationTypeWithdrawal,
		title, content, "/withdrawals", uint64(withdrawalID))
}

// NotifyRecharge 发送充值成功通知
func NotifyRecharge(ctx context.Context, userID uint, amount float64, orderNo string) error {
	return NotificationCreate(ctx, userID, model.NotificationTypeRecharge,
		"充值成功",
		fmt.Sprintf("您已成功充值 %.2f，订单号：%s。", amount, orderNo),
		"/wallet",
		0)
}

// NotifyDispute 发送争议处理通知
func NotifyDispute(ctx context.Context, userID uint, disputeID uint64, status string) error {
	return NotificationCreate(ctx, userID, model.NotificationTypeDispute,
		"争议处理更新",
		fmt.Sprintf("您的争议工单 #%d 状态已更新为：%s。", disputeID, status),
		fmt.Sprintf("/disputes/%d", disputeID),
		disputeID)
}

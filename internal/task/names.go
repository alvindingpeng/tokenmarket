package task

import "github.com/alvindingpeng/switchyard/internal/model"

// 后台任务名: 调度登记、失败告警与运维展示共用同一份常量, 避免各处手写字符串漂移。
// 周期类任务的名字尽量与设置键解耦; 只有 TaskPriceUpdate 与设置键同名(便于按设置热更新)。
const (
	TaskPriceUpdate      = string(model.SettingKeyModelInfoUpdateInterval) // 任务名与设置键同名, 便于按设置热更新。
	TaskStatsSave        = "stats_save"
	TaskCleanLLM         = "clean_llm"
	TaskBillingRollback  = "billing_rollback"         // 回滚超时未结算的余额预扣。
	TaskBillingReconcile = "billing_reconcile"        // 每日核对计费明细与预扣。
	TaskBillingRecovery  = "billing_recovery"         // 重试持久入队但尚未完成的计费结算(启动即跑 + 周期跑)。
	TaskLogArchive       = "log_archive"              // 归档并清理超期日志。
	TaskAutoBackup       = "auto_backup"              // 定时一致性备份。
	TaskChannelHealth    = "channel_health"           // 低频渠道健康探测。
	TaskChannelTrustEval = "channel_trust_eval"       // 渠道观察期评估: 达标转正, 不达标降级。
	TaskChannelQuality   = "channel_quality_snapshot" // 渠道质量日快照: 昨日流量与探测可用率固化成一行。
	TaskChannelAutoAdd   = "channel_model_autoadd"    // 渠道模型后台自动添加: 周期复查上游模型清单并并入。
	TaskSettlementDaily  = "settlement_daily"         // 每日重算昨日渠道商结算单: 只刷新未审核单, 已定稿不动。
	TaskUpdateCheck      = "update_check"             // 检查本分支 Release 是否有新版本。
	TaskVideoJobCleanup  = "video_job_cleanup"        // 清理过期的生视频任务归属。
	TaskBreakerProbe     = "breaker_probe"            // 熔断恢复探测: 验证到期的熔断成员健康后才完全解除。
	TaskTicketAutoClose  = "ticket_auto_close"        // 工单自动关闭: resolved 停留满 3 天(按 resolved_at 计时)自动关闭。
)

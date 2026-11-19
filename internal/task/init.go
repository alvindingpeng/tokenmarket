package task

import (
	"context"
	"fmt"
	"time"

	"github.com/alvindingpeng/switchyard/internal/alert"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/price"
	"github.com/alvindingpeng/switchyard/internal/update"
	"github.com/charmbracelet/log"
)

// 任务名常量见 names.go。

// 后台任务的周期都来自可改的系统设置。改造前只有模型信息更新会跟随设置变化,
// 其余(统计落库、自动备份、健康探测、更新检查)改了要重启进程才生效 —— 管理员在界面上
// 把备份周期从 24 小时改成 6 小时, 保存成功、日志也记了, 实际仍然 24 小时一次。
// 现在每个任务都用 Interval 函数声明周期, 改完设置经 ApplySetting 立刻重算。
// settingTasks 设置键到受其影响的任务名。新增周期类设置时在这里登记一行,
// 设置处理器就会自动负责热更新; 漏登记的后果是界面报 ErrNotRegistered 而不是静默失效。
var settingTasks = map[model.SettingKey][]string{
	model.SettingKeyModelInfoUpdateInterval: {TaskPriceUpdate},
	model.SettingKeyStatsSaveInterval:       {TaskStatsSave},
	model.SettingKeyAutoBackupEnabled:       {TaskAutoBackup},
	model.SettingKeyAutoBackupInterval:      {TaskAutoBackup},
	model.SettingKeyHealthCheckEnabled:      {TaskChannelHealth},
	model.SettingKeyHealthCheckMinutes:      {TaskChannelHealth},
	model.SettingKeyUpdateCheckEnabled:      {TaskUpdateCheck},
	model.SettingKeyUpdateCheckMinutes:      {TaskUpdateCheck},
	model.SettingKeyAutoAddBackground:       {TaskChannelAutoAdd},
	model.SettingKeyAutoAddInterval:         {TaskChannelAutoAdd},
	model.SettingKeyAuditRetentionDays:      {TaskAuditArchive},
	model.SettingKeyAuditArchiveEnabled:     {TaskAuditArchive},
	model.SettingKeyArchiveRetentionDays:    {TaskArchiveRetention},
	model.SettingKeyFxRateUpdateEnabled:     {TaskFxRateUpdate},
	model.SettingKeyFxRateUpdateInterval:    {TaskFxRateUpdate},
	model.SettingKeyAccountRefreshEnabled:  {TaskAccountTokenRefresh},
	model.SettingKeyAccountRefreshMinutes:  {TaskAccountTokenRefresh},
}

// ApplySetting 重算某项设置影响到的全部任务周期。
// 没有任务登记在这个设置上时返回 ErrNotRegistered: 与"改完了但没人应用"是同一件事,
// 交给调用方决定是报错还是忽略(设置处理器选择回给界面, 让漏配可见)。
func ApplySetting(key model.SettingKey) error {
	names, ok := settingTasks[key]
	if !ok {
		return ErrNotRegistered
	}
	for _, name := range names {
		if err := Apply(name); err != nil {
			return err
		}
	}
	return nil
}

// SettingHasTask 判断某项设置是否挂了可热更新的任务, 供设置处理器区分"没配任务"与"配了但没注册"。
func SettingHasTask(key model.SettingKey) bool {
	_, ok := settingTasks[key]
	return ok
}

func Init() {
	// 价格与能力更新: 拉 models.dev 价格表并同步能力登记。
	Define(Spec{
		Name:       TaskPriceUpdate,
		Interval:   hoursInterval(model.SettingKeyModelInfoUpdateInterval, 24),
		RunOnStart: true,
		Fn: func() error {
			ctx := context.Background()
			capabilities, err := price.UpdateLLMPrice(ctx)
			if err != nil {
				log.Warnf("failed to update price info: %v", err)
			}
			// 能力登记与价格同源却各自独立: 价格解析失败时能力未必也失败, 故分开处理, 互不阻塞。
			if len(capabilities) > 0 {
				if capErr := op.CapabilityReplaceFromModelsDev(capabilities, ctx); capErr != nil {
					log.Warnf("failed to update model capabilities: %v", capErr)
				}
			}
			// 价格解析失败要按任务失败计数: 连续失败达到阈值后由告警出口通知管理员。
			return err
		},
	})

	// 统计落库。
	Define(Spec{
		Name:     TaskStatsSave,
		Interval: minutesInterval(model.SettingKeyStatsSaveInterval, 10),
		Fn: func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if err := op.StatsSaveDB(ctx); err != nil {
				log.Errorf("stats save db error: %v", err)
				return err
			}
			return nil
		},
	})

	// 余额预扣回滚: 30 分钟仍未结算的预扣按泄漏处理, 释放其冻结额。
	Define(Spec{
		Name:       TaskBillingRollback,
		Interval:   fixedInterval(10 * time.Minute),
		RunOnStart: true,
		Fn:         func() error { return op.BillingReleaseStale(30 * time.Minute) },
	})

	// 对账: 每天核对昨日账目, 只登记结论与差异, 绝不自动改余额。
	Define(Spec{
		Name:       TaskBillingReconcile,
		Interval:   fixedInterval(24 * time.Hour),
		RunOnStart: true,
		Fn: func() error {
			_, err := op.ReconcileRun(context.Background(), 1)
			return err
		},
	})

	// 计费结算恢复: 结算入口(BillingSettle)是"持久入队 + 尝试", 进程崩溃、落库失败或执行中断
	// 留下的未完成结算由本任务重试到位 —— 钱必须扣成, 不能靠预扣超时释放(那只是退冻结额)。
	// 启动即跑(重启后先补历史积压)再周期跑; 每轮限量 50 笔、限时 1 分钟(有界上下文),
	// 避免积压大时长时间占住任务协程, 剩余的留给下一轮。手动审核冻结(hold)的账不受本任务影响。
	Define(Spec{
		Name:       TaskBillingRecovery,
		Interval:   fixedInterval(5 * time.Minute),
		RunOnStart: true,
		Fn: func() error {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			return op.BillingRecoveryRun(ctx, 50)
		},
	})

	// 日志生命周期: 每小时归档并清理超出留存期的调用日志。
	// 归档本身失败时 op 已按 archive_failed 告过警, 这里只把错误交回调度按任务失败计数。
	Define(Spec{
		Name:       TaskLogArchive,
		Interval:   fixedInterval(time.Hour),
		RunOnStart: true,
		Fn: func() error {
			result, err := op.LogLifecycleRun(context.Background())
			if err != nil {
				return err
			}
			log.Infof("log lifecycle: cutoff=%s archived=%d requests=%d attempts=%d bodies=%d",
				result.Cutoff.Format("2006-01-02"), result.Archived, result.RequestsDel, result.AttemptsDel, result.BodyCleared)
			return nil
		},
	})

	// 审计归档: 与调用日志归档分开跑, 留存期各自独立 —— 审计证据不能被 7 天日志保留顺手删掉。
	// 每天一次即可; 归档失败时本轮不删除任何记录(见 op.AuditLifecycleRun)。
	Define(Spec{
		Name:       TaskAuditArchive,
		Interval:   fixedInterval(24 * time.Hour),
		RunOnStart: true,
		Fn: func() error {
			result, err := op.AuditLifecycleRun(context.Background())
			if err != nil {
				return err
			}
			log.Infof("audit lifecycle: cutoff=%s archived=%d deleted=%d retention=%dd",
				result.Cutoff.Format("2006-01-02"), result.Archived, result.Deleted, result.RetentionDay)
			return nil
		},
	})

	// 告警补发: 外发失败的告警按退避排期重试, 直到送达或达到上限。
	// 改造前 notified=false 就是终点 —— 2026-11-02 实测 260 条告警因 Telegram 实体解析 400
	// 全部静默丢失, 系统没有任何重试入口, 管理员也不知道有告警没送到。
	// 周期 5 分钟: 补发延迟本身由 next_notify_at 控制(最短 2 分钟), 任务只管把到点的捞出来。
	Define(Spec{
		Name:     TaskAlertRedeliver,
		Interval: fixedInterval(5 * time.Minute),
		Fn: func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			sent, gaveUp, err := op.AlertRedeliverPending(ctx)
			if err != nil {
				return err
			}
			if sent > 0 || gaveUp > 0 {
				log.Infof("alert redeliver: sent=%d gave_up=%d", sent, gaveUp)
			}
			return nil
		},
	})

	// 限流用量监控只看最近窗口, 每日清理超过 30 天的小时汇总。
	Define(Spec{
		Name:       TaskRateLimitUsageRetention,
		Interval:   fixedInterval(24 * time.Hour),
		RunOnStart: true,
		Fn: func() error {
			deleted, err := op.RateLimitUsageRetentionRun(context.Background())
			if err != nil {
				return err
			}
			log.Infof("rate limit usage retention: deleted=%d", deleted)
			return nil
		},
	})

	// 归档文件留存: 数据库里的超期记录已经被归档后删除, 归档 NDJSON 自身也必须过期回收,
	// 否则 data/archives 只增不减(与部署备份一起成为唯一随运维动作增长的目录)。
	// 每天一次即可; 无论多老都保留最近几份, 长期停机后不会一次清空历史。
	Define(Spec{
		Name:       TaskArchiveRetention,
		Interval:   fixedInterval(24 * time.Hour),
		RunOnStart: true,
		Fn: func() error {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			result, err := op.ArchiveRetentionRun(ctx)
			if err != nil {
				return err
			}
			if result.Pruned > 0 {
				log.Infof("archive retention: cutoff=%s kept=%d pruned=%d freed=%dB",
					result.Cutoff.Format("2006-01-02"), result.Kept, result.Pruned, result.FreedB)
			}
			return nil
		},
	})

	// 牌价采集: 结算折算的依据必须有人维护, 否则非 USD 报价只能沿用旧价且无人知晓陈旧程度。
	// RunOnStart 让进程重启后立即补当天牌价(上一个进程可能恰好在采集前挂掉);
	// 失败返回 error 让任务框架按失败计数, 连续失败走既有告警出口 —— 汇率陈旧是计费正确性问题,
	// 不能只留一行日志。
	Define(Spec{
		Name:       TaskFxRateUpdate,
		Interval:   hoursInterval(model.SettingKeyFxRateUpdateInterval, 24),
		RunOnStart: true,
		Fn: func() error {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			written, err := op.FxRateRefresh(ctx)
			if err != nil {
				// 采集失败还要看存量牌价的日龄: 有旧价可用时告警但不中断计费;
				// 连无旧价都没有(首次部署就没网)才按任务失败计, 让失败告警出口接管。
				age, ageErr := op.FxRateStalenessCheck(ctx)
				if ageErr != nil || age < 0 {
					return err
				}
				if age > op.FxRateMaxAgeDays {
					op.RaiseAlertFacts(ctx, op.AlertFacts{
						Kind:    "fx_rate_stale",
						Subject: op.FxRateSourceName(),
						Reason:  fmt.Sprintf("牌价采集失败且快照已 %d 天未更新, 折算按旧价进行", age),
						Fields: []alert.Field{
							{Label: "来源", Value: op.FxRateSourceName()},
							{Label: "快照日龄", Value: fmt.Sprintf("%d 天", age)},
						},
					})
				}
				return err
			}
			log.Infof("fx rate update: refreshed %d currencies from %s", written, op.FxRateSourceName())
			return nil
		},
	})

	// 上游账号令牌刷新: 只处理 oauth 类账号(cookie 类没有续期能力, 由导入时的说明告知使用者)。
	//
	// 为什么必须是独立任务而不是在转发路径里顺手刷新: auth.APIKeyProvider.Get(ctx) 是同步且
	// 不返回 error 的接口, 把一次网络调用塞进去等于给每个请求挂上最长 20 秒的等待, 且失败无处上报。
	// 刷新在这里做, 转发只读缓存 —— 这也解释了为什么刷新失败必须能区分"可重试"与"账号已失效"。
	//
	// RunOnStart 取 false: 重启时集中刷新所有账号会在上游侧形成一阵异常流量, 等第一个周期更稳妥。
	// 周期取分钟级而不是小时级: 令牌有效期通常是小时级, 提前量是 10 分钟, 5 分钟一轮足以覆盖。
	Define(Spec{
		Name:     TaskAccountTokenRefresh,
		Interval: enabledMinutesInterval(model.SettingKeyAccountRefreshEnabled, model.SettingKeyAccountRefreshMinutes, 5),
		Fn: func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			result, err := op.AccountRefreshRun(ctx, time.Now())
			if err != nil {
				return err
			}
			if result.Refreshed > 0 || result.Failed > 0 || result.Expired > 0 {
				log.Infof("account token refresh: refreshed=%d failed=%d invalidated=%d",
					result.Refreshed, result.Failed, result.Expired)
			}
			return nil
		},
	})

	// 外部看门狗日志巡检。RunOnStart 是关键: 看门狗重启服务后, 只有服务再起来才有人能读到停机窗口日志。
	// 这是唯一能发现进程反复重启但每次都通过存活探针的入口(2026-11-02 两小时内重启 1192 次)。
	Define(Spec{
		Name:       TaskWatchdogAlert,
		Interval:   fixedInterval(5 * time.Minute),
		RunOnStart: true,
		Fn: func() error {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			return op.WatchdogAlertRun(ctx)
		},
	})

	// 生视频任务归属: 上游任务本身会过期, 归属记录超过保留期就没有查询价值, 定时清理避免表随调用量增长。
	Define(Spec{
		Name:       TaskVideoJobCleanup,
		Interval:   fixedInterval(time.Hour),
		RunOnStart: true,
		Fn: func() error {
			deleted, err := op.VideoJobCleanup()
			if err != nil {
				return err
			}
			if deleted > 0 {
				log.Infof("video jobs cleaned up: %d", deleted)
			}
			return nil
		},
	})

	// 自动备份: 一致性备份数据库并校验, 保留固定份数。关掉开关时周期为 0, 任务就地停摆。
	Define(Spec{
		Name:       TaskAutoBackup,
		Interval:   enabledHoursInterval(model.SettingKeyAutoBackupEnabled, model.SettingKeyAutoBackupInterval, 24),
		RunOnStart: true,
		Fn: func() error {
			result, err := op.AutoBackupRun()
			if err != nil {
				return err
			}
			log.Infof("auto backup: file=%s size=%d verified=%v pruned=%d remote=%s",
				result.File, result.Size, result.Verified, result.Pruned, result.Remote)
			return nil
		},
	})

	// 渠道健康探测: 默认关闭(可能产生上游费用), 开启后按配置间隔探测。
	Define(Spec{
		Name:       TaskChannelHealth,
		Interval:   enabledMinutesInterval(model.SettingKeyHealthCheckEnabled, model.SettingKeyHealthCheckMinutes, 30),
		RunOnStart: true,
		Fn: func() error {
			if err := op.ChannelHealthCheckAll(context.Background()); err != nil {
				log.Warnf("channel health check failed: %v", err)
				return err
			}
			return nil
		},
	})

	// 渠道观察期评估: 每日一次, 把观察期内的累计成功率与毕业线比较, 决定转正/延长/收回。
	// 启动即跑: 重启后可能已经跨过了观察期截止日, 不补跑的话渠道会一直停在 observing。
	// 固定 24 小时周期: 观察期以「天」为粒度, 更频繁的评估只会让判定随日内抖动反复。
	Define(Spec{
		Name:       TaskChannelTrustEval,
		Interval:   fixedInterval(24 * time.Hour),
		RunOnStart: true,
		Fn: func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			return op.ChannelTrustEvaluate(ctx)
		},
	})

	// 渠道质量日快照: 每日固化昨日质量数据, 供 30 天趋势与公开分享页脱敏展示。
	// 排在观察期评估之后(RunOnStart 时两者并行, 但周期任务按定义顺序错开同一时刻的 tick),
	// 且只写昨日: 今日统计仍在内存累加, 固化会得到半天的假数据。
	Define(Spec{
		Name:       TaskChannelQuality,
		Interval:   fixedInterval(24 * time.Hour),
		RunOnStart: true,
		Fn: func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			return op.ChannelQualitySnapshotRun(ctx)
		},
	})

	// 日结补偿: 启动即跑、每小时补最近七个已结束自然日, 成功日由持久 checkpoint 跳过。
	// 首次部署也补近七天未审核单; 超过七天人工处理, 不扩大自动重算范围。
	// 保留稳定任务名与错误返回, 由调度器统计失败并告警; 不审核、不付款。
	Define(Spec{
		Name:       TaskSettlementDaily,
		Interval:   fixedInterval(time.Hour),
		RunOnStart: true,
		Fn: func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			return op.SettlementCatchupRun(ctx, time.Now())
		},
	})

	// 渠道模型后台自动添加: 周期复查「已开启自动添加」的渠道, 把上游新增的模型并入。
	//
	// 为什么要后台跑而不是只在保存时跑: 保存触发的探测只覆盖"那一刻"的上游清单, 上游后来上新的
	// 模型永远发现不了 —— 自动添加因此只是一次性动作。周期复查让它变成持续同步。
	// 默认关(周期为 0, 任务就地停摆): 它会对每个开启的渠道发上游请求, 属于要管理员明确打开的动作。
	// 不设 RunOnStart: 启动即向全部渠道发探测会在重启时集中制造上游流量, 等第一个周期更稳妥。
	Define(Spec{
		Name:     TaskChannelAutoAdd,
		Interval: enabledHoursInterval(model.SettingKeyAutoAddBackground, model.SettingKeyAutoAddInterval, 24),
		Fn: func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			return op.AutoAddChannelsRun(ctx)
		},
	})

	// 更新检查只读取 Release 元数据, 不下载、不替换二进制; 真正更新必须由管理员点击按钮。
	Define(Spec{
		Name:     TaskUpdateCheck,
		Interval: updateCheckInterval,
		Fn: func() error {
			status, err := update.CheckNow()
			if err != nil {
				log.Warnf("update check failed: %v", err)
				return err
			}
			if status != nil {
				log.Infof("system update available: current=%s latest=%s asset=%s", status.CurrentVersion, status.LatestVersion, status.AssetName)
			}
			return nil
		},
	})

	// 熔断恢复探测: 检查已到期的熔断成员(凭据/模型), 通过健康检查验证后才完全解除;
	// 探测失败则延长熔断, 超过尝试上限则不再自动探测(交给人工判断)。每 2 分钟检查一次。
	Define(Spec{
		Name:       TaskBreakerProbe,
		Interval:   fixedInterval(2 * time.Minute),
		RunOnStart: false, // 不启动即跑: 避免重启时集中探测大量到期成员
		Fn: func() error {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			return op.BreakerRecoveryProbe(ctx)
		},
	})

	// 工单自动关闭: resolved 停留满 3 天的工单自动转 closed(状态机里"用户确认 / 3 天自动关闭"),
	// 按 resolved_at 计时(回复不重置窗口), 关闭时通知工单主人, 用户可重新打开。
	// 每小时扫一次; 启动即跑以补上停机期间跨过阈值的工单。
	Define(Spec{
		Name:       TaskTicketAutoClose,
		Interval:   fixedInterval(time.Hour),
		RunOnStart: true,
		Fn: func() error {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			closed, err := op.TicketAutoCloseResolved(ctx, op.TicketAutoCloseAfter)
			if err != nil {
				return err
			}
			if closed > 0 {
				log.Infof("ticket auto-close: closed=%d", closed)
			}
			return nil
		},
	})

	// Webhook 投递 worker: 每 5 秒一轮, 单轮批量有界(WebhookDispatchBatch)、并发有界、
	// 单飞不叠加; 抢占走条件更新(乐观锁), 多 worker/多实例并发也不会对同一条投递重复发网络请求。
	// RunOnStart: 重启后先把停机积压补投掉(至少一次语义, 接收端按 delivery_id 去重)。
	Define(Spec{
		Name:       TaskWebhookDispatch,
		Interval:   fixedInterval(5 * time.Second),
		RunOnStart: true,
		Fn: func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			return op.WebhookDispatchRun(ctx, op.WebhookDispatchBatch)
		},
	})

	// Webhook 队列/日志容量控制: 终态记录保留 7 天, 每订阅终态日志与非终态积压都有上限,
	// 避免失败订阅把队列表无限撑大。启动即跑 + 每小时一轮。
	Define(Spec{
		Name:       TaskWebhookCleanup,
		Interval:   fixedInterval(time.Hour),
		RunOnStart: true,
		Fn: func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			return op.WebhookCleanupRun(ctx)
		},
	})
}

// fixedInterval 固定周期。
func fixedInterval(d time.Duration) func() time.Duration {
	return func() time.Duration { return d }
}

// hoursInterval 以小时为单位的设置项 -> 周期; 读取失败或非法值回退 fallback。
func hoursInterval(key model.SettingKey, fallback int) func() time.Duration {
	return func() time.Duration {
		return time.Duration(settingInt(key, fallback)) * time.Hour
	}
}

// minutesInterval 以分钟为单位的设置项 -> 周期。
func minutesInterval(key model.SettingKey, fallback int) func() time.Duration {
	return func() time.Duration {
		return time.Duration(settingInt(key, fallback)) * time.Minute
	}
}

// enabledHoursInterval 带开关的小时周期: 开关关闭返回 0, 任务被就地停用而不是空转。
func enabledHoursInterval(enabledKey, intervalKey model.SettingKey, fallback int) func() time.Duration {
	return func() time.Duration {
		if !settingBool(enabledKey) {
			return 0
		}
		return time.Duration(settingInt(intervalKey, fallback)) * time.Hour
	}
}

// enabledMinutesInterval 带开关的分钟周期。
func enabledMinutesInterval(enabledKey, intervalKey model.SettingKey, fallback int) func() time.Duration {
	return func() time.Duration {
		if !settingBool(enabledKey) {
			return 0
		}
		return time.Duration(settingInt(intervalKey, fallback)) * time.Minute
	}
}

// updateCheckInterval 更新检查周期; 开关关闭返回 0。
func updateCheckInterval() time.Duration {
	if !update.CheckEnabled() {
		return 0
	}
	return time.Duration(update.CheckIntervalMinutes()) * time.Minute
}

// settingInt 读取正整数设置; 缺键、非法或 <1 时回退, 与改造前的兜底行为一致。
func settingInt(key model.SettingKey, fallback int) int {
	v, err := op.SettingGetInt(key)
	if err != nil || v < 1 {
		return fallback
	}
	return v
}

// settingBool 读取布尔设置; 缺键或非法值按 false 处理(功能默认关比默认开安全)。
func settingBool(key model.SettingKey) bool {
	v, err := op.SettingGetBool(key)
	if err != nil {
		return false
	}
	return v
}

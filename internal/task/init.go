package task

import (
	"context"
	"fmt"
	"time"

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

	// 渠道商结算单每日重算: 结算单此前只有管理员在计费页手工点「生成」才会更新, 于是当天生成过
	// 一次之后, 再晚进来的调用就永远落在单据之外 —— 20261016 那单差了 41 笔 / 0.998436396 就是这么来的
	// (单据 12:09:29 生成, 当天最后一批计费发生在 13:32)。日任务把「昨天的单据」在当天流量彻底
	// 收尾后再算一遍, 让未审核单自己收敛到全天真实数字。
	//
	// 只重算昨日, 不碰今天: 今天仍在写账单, 重算只会反复改动同一个未审核单。
	// 已审核(audited)的定稿单一个字段都不写, 由 SettlementGenerateDay 自己守着;
	// 若重算值与定稿不符, 该函数会整次报错并保持原样, 交给人工按跨期调整处理。
	Define(Spec{
		Name:       TaskSettlementDaily,
		Interval:   fixedInterval(24 * time.Hour),
		RunOnStart: true,
		Fn: func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			day := time.Now().AddDate(0, 0, -1).Format("20060102")
			if err := op.SettlementGenerateDay(ctx, day); err != nil {
				return fmt.Errorf("generate settlement for %s: %w", day, err)
			}
			return nil
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

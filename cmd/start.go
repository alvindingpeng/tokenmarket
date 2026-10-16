package cmd

import (
	"context"

	"github.com/alvindingpeng/switchyard/internal/alert"
	"github.com/alvindingpeng/switchyard/internal/conf"
	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/relay"
	"github.com/alvindingpeng/switchyard/internal/server"
	"github.com/alvindingpeng/switchyard/internal/server/auth"
	"github.com/alvindingpeng/switchyard/internal/task"
	"github.com/alvindingpeng/switchyard/internal/update"
	"github.com/alvindingpeng/switchyard/internal/utils/shutdown"
	"github.com/charmbracelet/log"
	"github.com/spf13/cobra"
)

var cfgFile string

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start " + conf.APP_NAME,
	PreRun: func(cmd *cobra.Command, args []string) {
		conf.PrintBanner()
		conf.Load(cfgFile)
		if level, err := log.ParseLevel(conf.AppConfig.Log.Level); err == nil {
			log.SetLevel(level)
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		shutdown.Init(log.Default())
		if err := db.InitDB(conf.AppConfig.Database.Type, conf.AppConfig.Database.Path, conf.IsDebug()); err != nil {
			log.Errorf("database init error: %v", err)
			return
		}
		shutdown.Register(db.Close)

		if err := op.InitCache(); err != nil {
			log.Errorf("cache init error: %v", err)
			return
		}
		shutdown.Register(op.SaveCache)

		// 告警外发接线: op 负责站内告警落库, alert 负责按 alert_channels 分发到 webhook/telegram/email。
		// alert 读设置要 op、op 发告警要 alert, 直接互相 import 会成环, 故两个方向都在这里注入。
		// 必须在 InitCache 之后: 设置读取依赖设置缓存。
		// 告警外发接线: op 负责站内告警落库, alert 负责按 alert_channels 分发到 webhook/telegram/email。
		// alert 读设置要 op、op 发告警要 alert, 直接互相 import 会成环, 故两个方向都在这里注入。
		// 必须在 InitCache 之后: 设置读取依赖设置缓存。
		alert.SettingsProvider = op.SettingGetString
		op.AlertDispatcher = func(ctx context.Context, a alert.Alert) bool {
			return alert.Send(ctx, a)
		}

		// 后台任务故障接线: 任务连续失败达到阈值后经同一条告警管道通知管理员。
		// 改造前任务失败只有 log.Warnf —— 备份不再产出、归档停在原地都没人知道,
		// 直到日志把磁盘写满才暴露。
		task.OnTaskFailure = func(name string, consecutive int, reason string) {
			op.RaiseTaskFailureAlert(name, consecutive, reason)
		}
		task.OnTaskRecovered = func(name string, consecutive int) {
			op.RaiseTaskRecoveredAlert(name, consecutive)
		}

		if err := op.UserInit(); err != nil {
			log.Errorf("user init error: %v", err)
			return
		}

		// 渠道准入审核存量回填(P1): 上线前已发布的渠道直接置 approved 并写指纹基线。
		// 幂等条件 shared=1 且 review_status=draft 只命中历史行, 重复启动零副作用; 回填写一条汇总审计。
		if backfilled, err := op.ChannelReviewBackfill(context.Background()); err != nil {
			log.Errorf("channel review backfill error: %v", err)
			return
		} else if backfilled > 0 {
			log.Infof("channel review backfill: %d legacy shared channels approved", backfilled)
		}

		// 发布编码规则切换(v0.32.0): 存量 10 位随机码一次性换成 4 位顺序号(1001 起, 展示 #1001)。
		// 幂等: 已是数字编码一律不重排, 重复启动零改动; 切换写一条汇总审计。
		if recoded, err := op.ShareCodeBackfill(context.Background()); err != nil {
			log.Errorf("share code backfill error: %v", err)
			return
		} else if recoded > 0 {
			log.Infof("share code backfill: %d channels recoded to 4-digit codes", recoded)
		}

		// 限流策略缓存与路由状态恢复: 必须在服务就绪前完成, 保证重启后限流口径与选路状态连续。
		if err := op.RatePolicyInit(); err != nil {
			log.Errorf("rate policy init error: %v", err)
			return
		}
		if err := relay.RestorePersist(); err != nil {
			log.Errorf("relay persist restore error: %v", err)
			return
		}
		// 关闭钩子按注册逆序执行, 此处注册保证落库先于 db.Close。
		shutdown.Register(func() error {
			relay.FlushRoutePersist()
			return nil
		})

		// JWT 全局密钥: 首次启动生成并持久化, 必须在 settings 缓存就绪后初始化。
		if err := auth.InitSecret(); err != nil {
			log.Errorf("auth secret init error: %v", err)
			return
		}

		if err := server.Start(); err != nil {
			log.Errorf("server start error: %v", err)
			return
		}
		shutdown.Register(server.Close)

		task.Init()
		go task.RUN()
		go update.HealthCheckAndRollback()
		shutdown.Listen()
	},
}

func init() {
	startCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is ./data/config.json)")
	rootCmd.AddCommand(startCmd)
}

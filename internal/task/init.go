package task

import (
	"context"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/price"
	"github.com/bestruirui/octopus/internal/update"
	"github.com/charmbracelet/log"
)

const (
	TaskPriceUpdate      = "price_update"
	TaskStatsSave        = "stats_save"
	TaskCleanLLM         = "clean_llm"
	TaskBillingRollback  = "billing_rollback"  // 回滚超时未结算的余额预扣。
	TaskBillingReconcile = "billing_reconcile" // 每日核对计费明细与预扣。
	TaskLogArchive       = "log_archive"       // 归档并清理超期日志。
	TaskAutoBackup       = "auto_backup"       // 定时一致性备份。
	TaskChannelHealth    = "channel_health"    // 低频渠道健康探测。
	TaskUpdateCheck      = "update_check"      // 检查本分支 Release 是否有新版本。
)

func Init() {
	priceUpdateIntervalHours, err := op.SettingGetInt(model.SettingKeyModelInfoUpdateInterval)
	if err != nil {
		log.Errorf("failed to get model info update interval: %v", err)
		return
	}
	priceUpdateInterval := time.Duration(priceUpdateIntervalHours) * time.Hour
	// 注册价格更新任务
	Register(string(model.SettingKeyModelInfoUpdateInterval), priceUpdateInterval, true, func() {
		if err := price.UpdateLLMPrice(context.Background()); err != nil {
			log.Warnf("failed to update price info: %v", err)
		}
	})

	// 注册统计保存任务
	statsSaveIntervalMinutes, err := op.SettingGetInt(model.SettingKeyStatsSaveInterval)
	if err != nil {
		log.Warnf("failed to get stats save interval: %v", err)
		return
	}
	statsSaveInterval := time.Duration(statsSaveIntervalMinutes) * time.Minute
	Register(TaskStatsSave, statsSaveInterval, false, op.StatsSaveDBTask)

	// 注册余额预扣回滚任务: 30 分钟仍未结算的预扣按泄漏处理, 释放其冻结额。
	Register(TaskBillingRollback, 10*time.Minute, true, func() {
		if err := op.BillingReleaseStale(30 * time.Minute); err != nil {
			log.Warnf("failed to release stale balance reservations: %v", err)
		}
	})

	// 对账任务: 每天核对昨日账目, 只登记结论与差异, 绝不自动改余额。
	Register(TaskBillingReconcile, 24*time.Hour, true, func() {
		if _, err := op.ReconcileRun(context.Background(), 1); err != nil {
			log.Warnf("failed to reconcile billing: %v", err)
		}
	})

	// 日志生命周期: 每小时归档并清理超出留存期的调用日志。
	Register(TaskLogArchive, time.Hour, true, func() {
		result, err := op.LogLifecycleRun(context.Background())
		if err != nil {
			log.Warnf("failed to run log lifecycle: %v", err)
			op.RaiseAlert(context.Background(), 0, "system", "archive_failed", "auto archive failed: "+err.Error())
			return
		}
		log.Infof("log lifecycle: cutoff=%s archived=%d requests=%d attempts=%d bodies=%d",
			result.Cutoff.Format("2006-01-02"), result.Archived, result.RequestsDel, result.AttemptsDel, result.BodyCleared)
	})

	// 自动备份任务: 每天一致性备份数据库并校验, 保留固定份数。
	backupInterval, err := op.SettingGetInt(model.SettingKeyAutoBackupInterval)
	if err != nil || backupInterval < 1 {
		backupInterval = 24
	}
	Register(TaskAutoBackup, time.Duration(backupInterval)*time.Hour, true, func() {
		if enabled, _ := op.SettingGetBool(model.SettingKeyAutoBackupEnabled); !enabled {
			return
		}
		result, err := op.AutoBackupRun()
		if err != nil {
			log.Warnf("failed to run auto backup: %v", err)
			op.RaiseAlert(context.Background(), 0, "system", "backup_failed", "auto backup failed: "+err.Error())
			return
		}
		log.Infof("auto backup: file=%s size=%d verified=%v pruned=%d", result.File, result.Size, result.Verified, result.Pruned)
	})

	// 渠道健康探测任务: 默认关闭, 开启后按配置间隔探测。
	healthInterval, err := op.SettingGetInt(model.SettingKeyHealthCheckMinutes)
	if err != nil || healthInterval < 1 {
		healthInterval = 30
	}
	Register(TaskChannelHealth, time.Duration(healthInterval)*time.Minute, true, func() {
		if enabled, _ := op.SettingGetBool(model.SettingKeyHealthCheckEnabled); !enabled {
			return
		}
		if err := op.ChannelHealthCheckAll(context.Background()); err != nil {
			log.Warnf("channel health check failed: %v", err)
		}
	})

	// 更新检查只读取 Release 元数据, 不下载、不替换二进制; 真正更新必须由管理员点击按钮。
	updateInterval := update.CheckIntervalMinutes()
	Register(TaskUpdateCheck, time.Duration(updateInterval)*time.Minute, false, func() {
		if !update.CheckEnabled() {
			return
		}
		status, err := update.CheckNow()
		if err != nil {
			log.Warnf("update check failed: %v", err)
			return
		}
		if status != nil {
			log.Infof("system update available: current=%s latest=%s asset=%s", status.CurrentVersion, status.LatestVersion, status.AssetName)
		}
	})
}

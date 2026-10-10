package handlers

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/relay"
	"github.com/bestruirui/octopus/internal/server/auth"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/ops").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/reconcile/run", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(runReconcile),
		).
		AddRoute(
			router.NewRoute("/reconcile/list", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(listReconcile),
		).
		AddRoute(
			router.NewRoute("/overview", http.MethodGet).
				Handle(opsOverview),
		).
		AddRoute(
			router.NewRoute("/balance-flow", http.MethodGet).
				Handle(listBalanceFlow),
		).
		AddRoute(
			router.NewRoute("/log-lifecycle/run", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(runLogLifecycle),
		).
		AddRoute(
			router.NewRoute("/log-lifecycle/list", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(listArchives),
		).
		AddRoute(
			router.NewRoute("/backup/run", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(runAutoBackup),
		).
		AddRoute(
			router.NewRoute("/backup/list", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(listBackups),
		).
		AddRoute(
			router.NewRoute("/health/list", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(listChannelHealth),
		).
		AddRoute(
			router.NewRoute("/health/run", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(runChannelHealth),
		).
		AddRoute(
			router.NewRoute("/alerts/list", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(listChannelAlerts),
		).
		AddRoute(
			router.NewRoute("/alerts/rules", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(listAlertRules),
		).
		AddRoute(
			router.NewRoute("/metrics/token", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(getMetricsToken),
		).
		AddRoute(
			router.NewRoute("/metrics/token/rotate", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(rotateMetricsToken),
		).
		AddRoute(
			router.NewRoute("/alerts/test-webhook", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(testAlertWebhook),
		).
		AddRoute(
			router.NewRoute("/auth-secret/rotate", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(rotateAuthSecret),
		)
}

// runReconcile 立即执行对账(最近若干天)。
func runReconcile(c *gin.Context) {
	days, _ := strconv.Atoi(c.DefaultQuery("days", "1"))
	results, err := op.ReconcileRun(context.Background(), days)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"items": results})
}

// listReconcile 返回历史对账结果。
func listReconcile(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "30"))
	rows, err := op.ReconcileList(limit)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"items": rows})
}

// listBalanceFlow 返回余额流水; 归属者或管理员可查。
func listBalanceFlow(c *gin.Context) {
	userID, role := middleware.CurrentUser(c)
	if role == model.RoleAdmin {
		if target, err := strconv.Atoi(c.Query("user_id")); err == nil && target > 0 {
			userID = uint(target)
		}
	}
	// 非管理员固定查自己。
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	rows, err := op.BalanceFlowList(context.Background(), userID, limit)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"items": rows})
}

// runLogLifecycle 立即执行归档与清理。
func runLogLifecycle(c *gin.Context) {
	result, err := op.LogLifecycleRun(context.Background())
	if err != nil {
		op.RaiseAlert(context.Background(), 0, "system", "archive_failed", "manual archive failed: "+err.Error())
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	audit(c, "log.lifecycle", "", "archived="+strconv.Itoa(result.Archived))
	resp.Success(c, result)
}

// listArchives 返回归档文件列表。
func listArchives(c *gin.Context) {
	files, err := op.ArchiveList()
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"items": files, "retention_days": op.LogRetentionDays()})
}

// runAutoBackup 立即执行一次备份。
func runAutoBackup(c *gin.Context) {
	result, err := op.AutoBackupRun()
	if err != nil {
		op.RaiseAlert(context.Background(), 0, "system", "backup_failed", "manual backup failed: "+err.Error())
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	audit(c, "backup.run", "", "file="+result.File)
	resp.Success(c, result)
}

// listBackups 返回备份文件列表。
func listBackups(c *gin.Context) {
	files, err := op.BackupList()
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"items": files})
}

// listChannelHealth 返回渠道健康记录。
func listChannelHealth(c *gin.Context) {
	channelID, _ := strconv.Atoi(c.Query("channel_id"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	rows, err := op.ChannelHealthList(context.Background(), channelID, limit)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"items": rows})
}

// runChannelHealth 立即对所有启用渠道探测一轮, 不必等待定时任务。
func runChannelHealth(c *gin.Context) {
	if err := op.ChannelHealthCheckAll(c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	audit(c, "health.run", "", "")
	resp.Success(c, gin.H{"ok": true})
}

// listAlertRules 返回生效中的告警规则(当前值 + 默认值), 管理员专用。
// 规则本身通过 /api/v1/setting/set 修改, 这里只做只读回显, 避免出现第三套配置入口。
func listAlertRules(c *gin.Context) {
	resp.Success(c, gin.H{"items": op.AlertRules()})
}

// getMetricsToken 回显当前抓取令牌, 管理员专用。
// 令牌是内部键, 不随 /setting/list 外露, 只在管理员显式索取时返回, 便于配到抓取端。
func getMetricsToken(c *gin.Context) {
	token, err := op.MetricsTokenEnsure()
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"mode": op.MetricsAuthMode(), "token": token})
}

// rotateMetricsToken 轮换抓取令牌: 旧令牌立即失效, 用于凭据泄漏后的处置。
func rotateMetricsToken(c *gin.Context) {
	token, err := op.MetricsTokenRotate()
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	audit(c, "ops.metrics-token-rotate", "", "")
	resp.Success(c, gin.H{"mode": op.MetricsAuthMode(), "token": token})
}

// rotateAuthSecret 轮换 JWT 签名密钥: 生成新密钥并立即生效, 所有已签发的令牌即刻失效。
func rotateAuthSecret(c *gin.Context) {
	value, err := auth.RotateSecret()
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	audit(c, "ops.auth-secret-rotate", "", "")
	resp.Success(c, gin.H{"message": "auth secret rotated, all sessions need to relogin"})
	_ = value // 新密钥不对外暴露, 日志可查修改操作但不可见密钥本身。
}

// listChannelAlerts 返回告警记录。
func listChannelAlerts(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	rows, err := op.ChannelAlertList(context.Background(), limit)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"items": rows})
}

// testAlertWebhook 主动测试告警 Webhook 连通性: 发送一条 mock 告警并返回是否送达。
// 用于运维/设置页一键验证 Webhook 实际可达, 避免真实告警时才发现配置错误。
func testAlertWebhook(c *gin.Context) {
	webhookURL, err := op.SettingGetString(model.SettingKeyAlertWebhookURL)
	if err != nil || webhookURL == "" {
		resp.Error(c, http.StatusBadRequest, "alert webhook url is not configured")
		return
	}
	mock := model.ChannelAlert{
		ChannelID:   0,
		ChannelCode: "system",
		Kind:        "test",
		Reason:      "webhook connectivity test from admin panel",
		CreatedAt:   time.Now(),
	}
	if !op.SendWebhookTest(c.Request.Context(), webhookURL, mock) {
		resp.Error(c, http.StatusBadGateway, "webhook returned non-2xx or unreachable")
		return
	}
	audit(c, "alert.test_webhook", "", webhookURL)
	resp.Success(c, gin.H{"ok": true})
}

// opsOverview 运维概览: 24 小时调用汇总、 进行中请求、 渠道与告警计数, 供运维仪表盘顶部卡片。
func opsOverview(c *gin.Context) {
	stats, err := op.StatsSince(time.Now().Add(-24 * time.Hour))
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	totals := op.CollectMetrics()
	resp.Success(c, gin.H{
		"requests_24h":      stats.Requests,
		"success_24h":       stats.Success,
		"failed_24h":        stats.Failed,
		"input_tokens_24h":  stats.InputTokens,
		"output_tokens_24h": stats.OutputTokens,
		"cost_24h":          stats.Cost,
		"active_requests":   relay.ActiveCount(),
		"channels_total":    totals.Channels,
		"channels_enabled":  totals.ChannelsEnabled,
		"alerts_24h":        totals.Alerts24h,
	})
}

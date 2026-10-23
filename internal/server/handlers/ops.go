package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/alert"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/relay"
	"github.com/alvindingpeng/switchyard/internal/server/auth"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
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
			router.NewRoute("/reconcile/note", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(setReconcileNote),
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
			router.NewRoute("/latency", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(listLatency),
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
			router.NewRoute("/metrics/reset", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(resetRouteMetrics),
		).
		AddRoute(
			router.NewRoute("/alerts/test-webhook", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(testAlertWebhook),
		).
		AddRoute(
			router.NewRoute("/alerts/test/:channel", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(testAlertChannel),
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

type reconcileNoteReq struct {
	Day  string `json:"day"`
	Note string `json:"note"` // 空串表示撤销处置。
}

// setReconcileNote 为某日对账写下/撤销人工处置说明。
// 只写 note/noted_at/noted_by 与状态, 差异数字一个都不动 —— 这是「查清了但改不了」的唯一出口。
func setReconcileNote(c *gin.Context) {
	var req reconcileNoteReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Day == "" {
		resp.Error(c, http.StatusBadRequest, "day is required")
		return
	}
	actorID, _ := middleware.CurrentUser(c)
	record, err := op.ReconcileNoteSet(c.Request.Context(), req.Day, req.Note, actorID)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	action := "reconcile_note_set"
	if strings.TrimSpace(req.Note) == "" {
		action = "reconcile_note_clear"
	}
	// 审计只记长度与动作, 不抄正文: 正文可能很长, 且审计表要长期留存。
	audit(c, action, req.Day, fmt.Sprintf("note length %d", len(strings.TrimSpace(req.Note))))
	resp.Success(c, record)
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
		// 手动触发失败也要进告警: 管理员点了"立即归档"却没成, 只回一个 500 容易被他当成页面卡住。
		op.RaiseAlertFacts(c.Request.Context(), op.AlertFacts{
			Kind:   "archive_failed",
			Reason: "manual log archive failed",
			Fields: []alert.Field{
				{Label: "触发方式", Value: "运维页手动执行"},
				{Label: "操作者", Value: operatorName(c)},
				{Label: "错误", Value: err.Error()},
				{Label: "影响", Value: "日志表继续膨胀, 磁盘写满会拖垮整个网关"},
			},
		})
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
		op.RaiseAlertFacts(c.Request.Context(), op.AlertFacts{
			Kind:   "backup_failed",
			Reason: "manual backup failed",
			Fields: []alert.Field{
				{Label: "触发方式", Value: "运维页手动执行"},
				{Label: "操作者", Value: operatorName(c)},
				{Label: "错误", Value: err.Error()},
				{Label: "影响", Value: "数据库没有可用的新恢复点"},
			},
		})
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

// resetRouteMetrics 运维重置评分指标: 清掉某个渠道模型(或单个成员)的历史观测, 内存与持久化一并清理。
// 用途: 观测被陈旧样本污染导致评分异常(如一条孤例失败把整个模型钉死 0 分)时手工纠偏,
// 清零后随新请求重新积累, 不必等自然滚动衰减。只清指标, 不动配置、余额与日志。
// 二选一的作用域: channel_model_id 清该渠道模型下全部成员观测; group_id+item_id 只清单个成员。
func resetRouteMetrics(c *gin.Context) {
	var req struct {
		ChannelModelID int `json:"channel_model_id"`
		GroupID        int `json:"group_id"`
		ItemID         int `json:"item_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	switch {
	case req.ChannelModelID > 0 && req.GroupID == 0 && req.ItemID == 0:
		cleared, err := relay.ResetModelMetric(req.ChannelModelID)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		audit(c, "ops.metrics-reset", "", "channel_model_id="+strconv.Itoa(req.ChannelModelID)+" cleared="+strconv.Itoa(cleared))
		resp.Success(c, gin.H{"scope": "channel_model", "channel_model_id": req.ChannelModelID, "cleared_members": cleared})
	case req.ChannelModelID == 0 && req.GroupID > 0 && req.ItemID > 0:
		channelModelID, err := relay.ResetMemberMetric(req.GroupID, req.ItemID)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		audit(c, "ops.metrics-reset", "", "group_id="+strconv.Itoa(req.GroupID)+" item_id="+strconv.Itoa(req.ItemID))
		resp.Success(c, gin.H{"scope": "member", "group_id": req.GroupID, "item_id": req.ItemID, "channel_model_id": channelModelID})
	default:
		resp.Error(c, http.StatusBadRequest, "channel_model_id 或 group_id+item_id 必须给出一个")
	}
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
	mock := alert.Alert{
		Event:     alert.EventTest,
		Kind:      "test",
		Scope:     "系统",
		Message:   "webhook connectivity test from admin panel",
		Timestamp: time.Now(),
	}
	if err := alert.SendWebhookTo(c.Request.Context(), webhookURL, mock); err != nil {
		// 原样回报失败原因: 403 与 connection refused 的处置完全不同, 只说"不可达"等于让管理员猜。
		resp.Error(c, http.StatusBadGateway, "webhook unreachable: "+err.Error())
		return
	}
	audit(c, "alert.test_webhook", "", webhookURL)
	resp.Success(c, gin.H{"ok": true})
}

// testAlertChannel 按指定渠道(telegram/email)发送一条测试告警, 验证凭据与连通性。
func testAlertChannel(c *gin.Context) {
	channel := strings.TrimSpace(c.Param("channel"))
	if channel != "telegram" && channel != "email" {
		resp.Error(c, http.StatusBadRequest, "unsupported alert channel")
		return
	}
	err := alert.SendOnce(c.Request.Context(), channel, alert.Alert{
		Event:     alert.Event("test"),
		Title:     "Switchyard test alert",
		Message:   "test alert from admin panel; this confirms the alert channel is reachable",
		Timestamp: time.Now(),
	})
	if err != nil {
		resp.Error(c, http.StatusBadGateway, err.Error())
		return
	}
	audit(c, "alert.test_"+channel, "", "")
	resp.Success(c, gin.H{"ok": true})
}

// listLatency 延迟归因: 按渠道与调用方国家给出首字节、DNS、建连、TLS 与上游等待的分位。
// 单列 UpstreamWait 是刻意的 —— 首字节耗时是"我们连得慢"与"上游排队久"的合计,
// 拆开后才知道该换上游还是该加出口。
func listLatency(c *gin.Context) {
	days := 7
	if raw := strings.TrimSpace(c.Query("days")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 90 {
			days = parsed
		}
	}
	rows, err := op.LatencyBreakdown(time.Now().AddDate(0, 0, -days))
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"days": days, "rows": rows})
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

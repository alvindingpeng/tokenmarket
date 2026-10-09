package handlers

import (
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/relay"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

// processStartedAt 进程启动时间, 供 /healthz 与 /metrics 上报运行时长。
var processStartedAt = time.Now()

func init() {
	router.NewGroupRouter("/healthz").
		AddRoute(router.NewRoute("", http.MethodGet).Handle(healthzProbe))
	router.NewGroupRouter("/metrics").
		AddRoute(router.NewRoute("", http.MethodGet).Handle(prometheusMetrics))
}

// healthzProbe 存活探针: 无需登录, 返回进程与数据库状态; 数据库异常时以 503 表达降级。
func healthzProbe(c *gin.Context) {
	dbStatus := "ok"
	if sqlDB, err := db.GetDB().DB(); err != nil || sqlDB.Ping() != nil {
		dbStatus = "error"
	}
	status, state := http.StatusOK, "ok"
	if dbStatus != "ok" {
		status, state = http.StatusServiceUnavailable, "degraded"
	}
	c.JSON(status, gin.H{
		"status":          state,
		"uptime_seconds":  int(time.Since(processStartedAt).Seconds()),
		"version":         conf.Version,
		"db":              dbStatus,
		"active_requests": relay.ActiveCount(),
	})
}

// prometheusMetrics 输出 Prometheus 文本格式指标: 进程状态、调用累计、资源计数。
// 默认无需登录(只暴露聚合数字); 需要收敛时把 metrics_auth 设为 bearer, 由 metrics_token 校验抓取方。
// metricsAuthorized 校验抓取凭据: 关闭鉴权时恒通过; 开启后接受 Authorization: Bearer 或 ?token=。
// /healthz 不做鉴权 —— 它是存活探针, 只暴露进程与数据库状态, 加了鉴权反而让编排系统无法探活。
func metricsAuthorized(c *gin.Context) bool {
	if op.MetricsAuthMode() != "bearer" {
		return true
	}
	token, err := op.MetricsTokenEnsure()
	if err != nil || token == "" {
		return false
	}
	provided := strings.TrimSpace(strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
	if provided == "" {
		provided = c.Query("token")
	}
	return provided != "" && provided == token
}

func prometheusMetrics(c *gin.Context) {
	if !metricsAuthorized(c) {
		c.Header("WWW-Authenticate", "Bearer realm=\"metrics\"")
		c.String(http.StatusUnauthorized, "metrics requires a valid token\n")
		return
	}
	totals := op.CollectMetrics()
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)

	lines := make([]string, 0, 32)
	metric := func(help, typ, sample string) {
		lines = append(lines, sample)
		_ = help
		_ = typ
	}
	lines = append(lines,
		"# HELP octopus_up 1 when the process is alive.",
		"# TYPE octopus_up gauge",
		"octopus_up 1",
		"# HELP octopus_uptime_seconds Process uptime in seconds.",
		"# TYPE octopus_uptime_seconds gauge",
		"octopus_uptime_seconds "+strconv.Itoa(int(time.Since(processStartedAt).Seconds())),
		"# HELP octopus_build_info Build information.",
		"# TYPE octopus_build_info gauge",
		"octopus_build_info{version=\""+conf.Version+"\",commit=\""+conf.Commit+"\"} 1",
	)
	_ = metric
	lines = append(lines,
		"# HELP octopus_relay_requests Relay requests by terminal status.",
		"# TYPE octopus_relay_requests counter",
	)
	for status, count := range totals.RequestsByStatus {
		lines = append(lines, "octopus_relay_requests{status=\""+status+"\"} "+strconv.FormatInt(count, 10))
	}
	lines = append(lines,
		"# HELP octopus_relay_tokens_total Relay token totals.",
		"# TYPE octopus_relay_tokens_total counter",
		"octopus_relay_tokens_total{direction=\"input\"} "+strconv.FormatInt(totals.InputTokens, 10),
		"octopus_relay_tokens_total{direction=\"output\"} "+strconv.FormatInt(totals.OutputTokens, 10),
		"# HELP octopus_billing_cost_total Accumulated billed cost.",
		"# TYPE octopus_billing_cost_total counter",
		"octopus_billing_cost_total "+strconv.FormatFloat(totals.Cost, 'f', 6, 64),
		"# HELP octopus_users Registered users and total balance.",
		"# TYPE octopus_users gauge",
		"octopus_users "+strconv.FormatInt(totals.Users, 10),
		"octopus_user_balance_total "+strconv.FormatFloat(totals.Balance, 'f', 6, 64),
		"# HELP octopus_channels Channel counts.",
		"# TYPE octopus_channels gauge",
		"octopus_channels{state=\"total\"} "+strconv.FormatInt(totals.Channels, 10),
		"octopus_channels{state=\"enabled\"} "+strconv.FormatInt(totals.ChannelsEnabled, 10),
		"# HELP octopus_alerts_raised_24h Alerts raised in the last 24 hours.",
		"# TYPE octopus_alerts_raised_24h gauge",
		"octopus_alerts_raised_24h "+strconv.FormatInt(totals.Alerts24h, 10),
		"# HELP octopus_active_requests In-flight relay requests.",
		"# TYPE octopus_active_requests gauge",
		"octopus_active_requests "+strconv.Itoa(relay.ActiveCount()),
		"# HELP octopus_goroutines Goroutine count.",
		"# TYPE octopus_goroutines gauge",
		"octopus_goroutines "+strconv.Itoa(runtime.NumGoroutine()),
		"# HELP octopus_memory_bytes Process heap and system memory.",
		"# TYPE octopus_memory_bytes gauge",
		"octopus_memory_bytes{kind=\"heap\"} "+strconv.FormatUint(memory.HeapAlloc, 10),
		"octopus_memory_bytes{kind=\"sys\"} "+strconv.FormatUint(memory.Sys, 10),
	)
	c.Header("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = c.Writer.WriteString(strings.Join(lines, "\n") + "\n")
}

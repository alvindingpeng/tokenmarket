package handlers

import (
	"crypto/subtle"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/conf"
	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/relay"
	"github.com/alvindingpeng/switchyard/internal/server/router"
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
	// 常量时间比较, 避免时序侧信道逐字节泄露令牌。
	return provided != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(token)) == 1
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
		"# HELP switchyard_up 1 when the process is alive.",
		"# TYPE switchyard_up gauge",
		"switchyard_up 1",
		"# HELP switchyard_uptime_seconds Process uptime in seconds.",
		"# TYPE switchyard_uptime_seconds gauge",
		"switchyard_uptime_seconds "+strconv.Itoa(int(time.Since(processStartedAt).Seconds())),
		"# HELP switchyard_build_info Build information.",
		"# TYPE switchyard_build_info gauge",
		"switchyard_build_info{version=\""+conf.Version+"\",commit=\""+conf.Commit+"\"} 1",
	)
	_ = metric
	lines = append(lines,
		"# HELP switchyard_relay_requests Relay requests by terminal status.",
		"# TYPE switchyard_relay_requests counter",
	)
	for status, count := range totals.RequestsByStatus {
		lines = append(lines, "switchyard_relay_requests{status=\""+status+"\"} "+strconv.FormatInt(count, 10))
	}
	lines = append(lines,
		"# HELP switchyard_relay_tokens_total Relay token totals.",
		"# TYPE switchyard_relay_tokens_total counter",
		"switchyard_relay_tokens_total{direction=\"input\"} "+strconv.FormatInt(totals.InputTokens, 10),
		"switchyard_relay_tokens_total{direction=\"output\"} "+strconv.FormatInt(totals.OutputTokens, 10),
		"# HELP switchyard_billing_cost_total Accumulated billed cost.",
		"# TYPE switchyard_billing_cost_total counter",
		"switchyard_billing_cost_total "+strconv.FormatFloat(totals.Cost, 'f', 6, 64),
		"# HELP switchyard_users Registered users and total balance.",
		"# TYPE switchyard_users gauge",
		"switchyard_users "+strconv.FormatInt(totals.Users, 10),
		"switchyard_user_balance_total "+strconv.FormatFloat(totals.Balance, 'f', 6, 64),
		"# HELP switchyard_channels Channel counts.",
		"# TYPE switchyard_channels gauge",
		"switchyard_channels{state=\"total\"} "+strconv.FormatInt(totals.Channels, 10),
		"switchyard_channels{state=\"enabled\"} "+strconv.FormatInt(totals.ChannelsEnabled, 10),
		"# HELP switchyard_alerts_raised_24h Alerts raised in the last 24 hours.",
		"# TYPE switchyard_alerts_raised_24h gauge",
		"switchyard_alerts_raised_24h "+strconv.FormatInt(totals.Alerts24h, 10),
		"# HELP switchyard_active_requests In-flight relay requests.",
		"# TYPE switchyard_active_requests gauge",
		"switchyard_active_requests "+strconv.Itoa(relay.ActiveCount()),
		"# HELP switchyard_goroutines Goroutine count.",
		"# TYPE switchyard_goroutines gauge",
		"switchyard_goroutines "+strconv.Itoa(runtime.NumGoroutine()),
		"# HELP switchyard_memory_bytes Process heap and system memory.",
		"# TYPE switchyard_memory_bytes gauge",
		"switchyard_memory_bytes{kind=\"heap\"} "+strconv.FormatUint(memory.HeapAlloc, 10),
		"switchyard_memory_bytes{kind=\"sys\"} "+strconv.FormatUint(memory.Sys, 10),
	)
	c.Header("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = c.Writer.WriteString(strings.Join(lines, "\n") + "\n")
}

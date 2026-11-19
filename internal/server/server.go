package server

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/alvindingpeng/switchyard/internal/conf"
	_ "github.com/alvindingpeng/switchyard/internal/server/handlers"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/alvindingpeng/switchyard/static"
	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"
)

// httpSrv 是当前 HTTP 服务实例。
//
// 用指针加锁而不是直接持有值: Close() 可能会在启动协程仍在读该结构体时被调用
// (停服与监听天然并发), 直接改写一个 http.Server 值的字段就是数据竞争。
// 换成"整体替换指针"之后, 正在运行的那个实例只被读取、不再被改写;
// 与此同时 Close 变成幂等且可重入 —— 重复调用、或在从未启动时调用都不panic。
var (
	httpMu  sync.Mutex
	httpSrv *http.Server
)

// currentHTTPServer 返回当前服务实例; 未启动时为 nil。
func currentHTTPServer() *http.Server {
	httpMu.Lock()
	defer httpMu.Unlock()
	return httpSrv
}

// setHTTPServer 替换当前服务实例, 返回被替换下来的那个(供收尾关闭)。测试用它注入独立实例。
func setHTTPServer(srv *http.Server) *http.Server {
	httpMu.Lock()
	defer httpMu.Unlock()
	previous := httpSrv
	httpSrv = srv
	return previous
}

func Start() error {
	if conf.IsDebug() {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	if len(conf.AppConfig.Server.TrustedProxies) > 0 {
		if err := r.SetTrustedProxies(conf.AppConfig.Server.TrustedProxies); err != nil {
			log.Warnf("invalid trusted_proxies config, falling back to default: %v", err)
		}
	} else {
		// 默认不信任任何代理, 禁止 X-Forwarded-For 伪造。如果部署在反向代理后面,
		// 请在 config.json 中设置 server.trusted_proxies, 例如 ["10.0.0.0/8", "192.168.0.0/16"]。
		r.SetTrustedProxies(nil)
	}
	r.Use(gin.CustomRecovery(func(c *gin.Context, _ any) {
		resp.Error(c, http.StatusInternalServerError, resp.ErrInternalServer)
		c.Abort()
	}))

	if conf.IsDebug() {
		r.Use(middleware.Logger())
	}
	r.Use(middleware.Cors())
	// 请求体上限: 网关会把请求体读进内存做改写与留痕, 不设上限时一个几百 MB 的 body
	// 就足以把内存吃穿。分三档 —— 管理端缺省 32 MiB; 转发接口(/v1)更宽(生图底图、
	// 生视频素材是正常业务); 整库导入豁免(导出文件的大小由库本身决定, 生产库可达 GB 级,
	// 套上限等于把恢复数据的通道堵死)。豁免项都有管理员鉴权, 风险面不同。
	r.Use(middleware.BodyLimit(conf.MaxBodyBytes(),
		middleware.BodyLimitRule{Prefix: "/v1", MaxBytes: conf.RelayMaxBodyBytes()},
		middleware.BodyLimitRule{Prefix: "/api/v1/setting/import", MaxBytes: 0},
	))
	r.Use(middleware.StaticEmbed("/", static.StaticFS))

	if err := router.RegisterAll(r); err != nil {
		return err
	}

	srv := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", conf.AppConfig.Server.Host, conf.AppConfig.Server.Port),
		Handler: r,
	}
	applyTimeouts(srv)
	setHTTPServer(srv)
	// 协程持有 srv 本身而不是去读包级变量: 包级变量会在停服时就地清空。
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Errorf("http server listen and serve error: %v", err)
		}
	}()
	return nil
}

// 服务端超时。取值依据见 applyTimeouts 的说明。
//
// 用变量而不是常量: 超时的作用只能靠"让一个连接超时"来验证, 而 10 秒的读头超时
// 没法在单测里等 —— 测试要把它们压到毫秒级才跑得动。
var (
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 120 * time.Second
)

// applyTimeouts 给 http.Server 装上防慢速连接的超时。
//
// 只装两个:
//   - ReadHeaderTimeout: 从连接建立到收完请求头的时间上限。挡的是 slowloris ——
//     只发一半请求头就挂住的连接, 每个都占一个 goroutine 与一个文件描述符, 没有任何超时
//     时几千个半开连接就能耗尽进程资源。请求体不受它管, 所以不影响大文件上传。
//   - IdleTimeout: keep-alive 空闲连接的最长保留时间, 避免空闲连接长期堆积。
//
// 刻意**不装** ReadTimeout 与 WriteTimeout, 这与"稳健性"的直觉相反, 但这里是必须的:
//   - 线上 98.3% 是流式请求, 一条 SSE 流可以挂几分钟(实测最长 156 秒), 非流式请求的
//     上游首字节也可能等几十秒。WriteTimeout 会从读请求头开始计时, 到点直接掐断连接 ——
//     那正是 v1.5.16 优雅关闭刚修好的"回答突然中断", 只是换了个触发条件。
//   - ReadTimeout 同理: 它连请求体一起算, 会掐掉慢速上传。
//
// 慢的上游等待由转发路径自己的上下文超时管(按渠道/分组的超时设置), 那里能给出
// 比"连接被断"更准确的错误; 服务端这一层只负责挡住"对端不说话"的连接。
func applyTimeouts(srv *http.Server) {
	srv.ReadHeaderTimeout = readHeaderTimeout
	srv.IdleTimeout = idleTimeout
	srv.ReadTimeout = 0          // 不设: 见上, 它会把请求体读取一起限时。
	srv.WriteTimeout = 0         // 不设: 见上, 它会掐断长流式响应。
	srv.MaxHeaderBytes = 1 << 20 // 1 MiB: 默认 1 MiB 相同, 显式写出避免依赖库默认值。
}

// shutdownGrace 等待在途请求结束的上限。
//
// 取值依据: 线上 98.3% 是流式请求, 一条 SSE 流可以挂几分钟; 而部署换二进制时
// systemctl stop 会发 SIGTERM。用 Close() 会立即硬关所有连接, 每次部署都打断正在
// 生成的回答。30 秒覆盖绝大多数流, 又不至于让部署长时间卡住。
const shutdownGrace = 30 * time.Second

// Close 优雅停服: 先停止接受新连接, 再等待在途请求结束或超时。
//
// 用 Shutdown 而不是 Close —— 后者直接断开所有连接(含正在传输的 SSE),
// 用户看到的是"回答突然中断", 而服务端日志里没有任何错误可查。
func Close() error {
	srv := setHTTPServer(nil)
	if srv == nil {
		return nil // 从未启动成功, 或已经关过: 停服必须可重入。
	}
	ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		// 超时仍有请求未结束: 记下来再硬关, 否则进程会被 systemd 的 TimeoutStopSec 杀掉。
		log.Warnf("graceful shutdown timed out after %s, forcing close: %v", shutdownGrace, err)
		return srv.Close()
	}
	return nil
}

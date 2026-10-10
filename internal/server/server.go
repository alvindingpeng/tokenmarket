package server

import (
	"fmt"
	"net/http"

	"github.com/bestruirui/octopus/internal/conf"
	_ "github.com/bestruirui/octopus/internal/server/handlers"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/bestruirui/octopus/static"
	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"
)

var httpSrv http.Server

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
	r.Use(middleware.StaticEmbed("/", static.StaticFS))

	if err := router.RegisterAll(r); err != nil {
		return err
	}

	httpSrv.Addr = fmt.Sprintf("%s:%d", conf.AppConfig.Server.Host, conf.AppConfig.Server.Port)
	httpSrv.Handler = r
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Errorf("http server listen and serve error: %v", err)
		}
	}()
	return nil
}

func Close() error {
	return httpSrv.Close()
}

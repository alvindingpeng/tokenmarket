package server

import (
	"fmt"
	"net/http"

	"github.com/alvindingpeng/switchyard/internal/conf"
	_ "github.com/alvindingpeng/switchyard/internal/server/handlers"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/alvindingpeng/switchyard/internal/utils/log"
	"github.com/alvindingpeng/switchyard/static"
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

	if conf.IsDebug() {
		r.Use(middleware.Cors())
		r.Use(middleware.Logger())
	}
	r.Use(middleware.StaticEmbed("/", static.StaticFS))

	router.RegisterAll(r)

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

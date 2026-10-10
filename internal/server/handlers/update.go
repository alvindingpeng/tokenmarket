package handlers

import (
	"errors"
	"net/http"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/bestruirui/octopus/internal/update"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/update").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(latest),
		).
		AddRoute(
			router.NewRoute("/check", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(forceCheck),
		).
		AddRoute(
			router.NewRoute("/now-version", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(getNowVersion),
		).
		AddRoute(
			router.NewRoute("", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(updateFunc),
		)
}

func latest(c *gin.Context) {
	force := c.Query("force") == "1" || c.Query("force") == "true"
	status, err := update.Status(force)
	if err != nil {
		if errors.Is(err, update.ErrUpdatePaused) {
			resp.Error(c, http.StatusServiceUnavailable, err.Error())
			return
		}
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, status)
}

func forceCheck(c *gin.Context) {
	status, err := update.Status(true)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, status)
}

func getNowVersion(c *gin.Context) {
	resp.Success(c, conf.Version)
}

func updateFunc(c *gin.Context) {
	force := c.Query("force") == "1" || c.Query("force") == "true"
	result, err := update.Apply(force)
	if err != nil {
		if errors.Is(err, update.ErrUpdatePaused) {
			resp.Error(c, http.StatusServiceUnavailable, err.Error())
			return
		}
		if errors.Is(err, update.ErrUpToDate) {
			resp.Error(c, http.StatusConflict, err.Error())
			return
		}
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	audit(c, "update.core", result.From+" -> "+result.To, result.Asset)
	resp.Success(c, result)
	// c.JSON 已经写入响应, 再显式 Flush 并延迟 Exec, 避免浏览器只看到网络错误。
	c.Writer.WriteHeaderNow()
	c.Writer.Flush()
	update.ScheduleRestart(result.ExecPath)
}

package handlers

import (
	"net/http"

	"github.com/alvindingpeng/switchyard/internal/conf"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/alvindingpeng/switchyard/internal/update"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/update").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("", http.MethodGet).
				Handle(latest),
		).
		AddRoute(
			router.NewRoute("", http.MethodPost).
				Handle(updateFunc),
		)
}

type SysVersionInfo struct {
	NowVersion        string `json:"now_version"`
	LatestVersion     string `json:"latest_version"`
	LatestPublishedAt string `json:"latest_published_at"`
	LatestBody        string `json:"latest_body"`
	LatestMessage     string `json:"latest_message"`
}

func latest(c *gin.Context) {
	latestInfo, err := update.GetLatestInfo()
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	resp.Success(c, SysVersionInfo{
		NowVersion:        conf.Version,
		LatestVersion:     latestInfo.TagName,
		LatestPublishedAt: latestInfo.PublishedAt,
		LatestBody:        latestInfo.Body,
		LatestMessage:     latestInfo.Message,
	})
}

func updateFunc(c *gin.Context) {
	err := update.UpdateCore()
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, "update success")
}

package handlers

import (
	"net/http"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	// 广场收藏: 用户的个人偏好, 登录即可读写, 不涉及供给面暴露。
	router.NewGroupRouter("/api/v1/market/favorites").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("", http.MethodGet).
				Handle(listMarketFavorites),
		).
		AddRoute(
			router.NewRoute("", http.MethodPost).
				Handle(addMarketFavorite),
		).
		AddRoute(
			router.NewRoute("/:model", http.MethodDelete).
				Handle(removeMarketFavorite),
		)
}

// listMarketFavorites 返回该用户收藏的模型名。
//
// 只回模型名, 不回挂牌数据: 收藏是"记住我关心哪些模型", 展示内容一律走广场目录接口。
// 若收藏接口回一份自己的挂牌快照, 就会与广场形成两份口径 —— 价格变了、渠道停了, 两边对不上。
func listMarketFavorites(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	names, err := op.MarketFavoriteList(userID)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"models": names})
}

// addMarketFavorite 收藏一个模型。
func addMarketFavorite(c *gin.Context) {
	var req struct {
		Model string `json:"model"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	userID, _ := middleware.CurrentUser(c)
	if err := op.MarketFavoriteAdd(userID, req.Model); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, nil)
}

// removeMarketFavorite 取消收藏。模型名走路径参数: 名字里可能有斜杠之外的任意字符, 用 URL 编码传。
func removeMarketFavorite(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	if err := op.MarketFavoriteRemove(userID, c.Param("model")); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, nil)
}

// marketFavoriteModel 仅用于让编译器确认 model 包在本文件被引用(收藏项归 model 层定义)。
var _ = model.MarketFavorite{}
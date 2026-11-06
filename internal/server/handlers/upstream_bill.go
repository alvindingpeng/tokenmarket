package handlers

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	// 上游账单对单: 渠道商对自己的渠道导入账单并核对; 管理员可看全部。
	router.NewGroupRouter("/api/v1/upstream-bills").
		Use(middleware.Auth()).
		AddRoute(router.NewRoute("/import", http.MethodPost).
			Allow(model.RoleAdmin, model.RoleUser).
			Handle(importUpstreamBills)).
		AddRoute(router.NewRoute("", http.MethodGet).
			Allow(model.RoleAdmin, model.RoleUser).
			Handle(listUpstreamBills)).
		AddRoute(router.NewRoute("/explain", http.MethodPost).
			Allow(model.RoleAdmin, model.RoleUser).
			Handle(explainUpstreamBill)).
		AddRoute(router.NewRoute("/recompute", http.MethodPost).
			Allow(model.RoleAdmin, model.RoleUser).
			Handle(recomputeUpstreamBills))
}

// ownsChannel 渠道归属校验: 管理员任意渠道, 渠道商只能动自己的。
// 渠道不存在与"不是你的"同样回 404, 不向调用方泄露他人渠道的存在性。
func ownsChannel(c *gin.Context, channelID int) bool {
	userID, role := middleware.CurrentUser(c)
	if role == model.RoleAdmin {
		return true
	}
	channel, err := op.ChannelGet(channelID)
	return err == nil && channel.UserID == userID
}

// importUpstreamBills 导入账单 CSV(multipart: channel_id, period, file)。
func importUpstreamBills(c *gin.Context) {
	channelID, err := strconv.Atoi(c.PostForm("channel_id"))
	if err != nil || channelID <= 0 {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	if !ownsChannel(c, channelID) {
		resp.Error(c, http.StatusNotFound, "channel not found")
		return
	}
	fileHeader, err := c.FormFile("file")
	if err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	file, err := fileHeader.Open()
	if err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	defer func() { _ = file.Close() }()
	// 账单 CSV 很小(千行级), 限读 2MB 防误传大文件占住请求。
	imported, err := op.UpstreamBillImport(c.Request.Context(), channelID, c.PostForm("period"), io.LimitReader(file, 2<<20))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "channel.upstream-bill", "channel#"+strconv.Itoa(channelID), "imported "+strconv.Itoa(imported)+" rows")
	resp.Success(c, gin.H{"imported": imported})
}

// listUpstreamBills 查询参数: channel_id, period(可选)。
func listUpstreamBills(c *gin.Context) {
	channelID, err := strconv.Atoi(c.Query("channel_id"))
	if err != nil || channelID <= 0 {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	if !ownsChannel(c, channelID) {
		resp.Error(c, http.StatusNotFound, "channel not found")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	rows, err := op.UpstreamBillList(ctx, channelID, c.Query("period"))
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"items": rows})
}

// explainUpstreamBill 定性一条差异(id + note)。
func explainUpstreamBill(c *gin.Context) {
	var request struct {
		ID   uint64 `json:"id"`
		Note string `json:"note"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	bill, err := op.UpstreamBillGet(c.Request.Context(), request.ID)
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	if !ownsChannel(c, bill.ChannelID) {
		resp.Error(c, http.StatusNotFound, "channel not found")
		return
	}
	if err := op.UpstreamBillExplain(c.Request.Context(), request.ID, request.Note); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "channel.upstream-bill", "bill#"+strconv.FormatUint(request.ID, 10), "explained")
	resp.Success(c, gin.H{})
}

// recomputeUpstreamBills 重算对单(channel_id, period)。
func recomputeUpstreamBills(c *gin.Context) {
	var request struct {
		ChannelID int    `json:"channel_id"`
		Period    string `json:"period"`
	}
	if err := c.ShouldBindJSON(&request); err != nil || request.ChannelID <= 0 {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if !ownsChannel(c, request.ChannelID) {
		resp.Error(c, http.StatusNotFound, "channel not found")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Minute)
	defer cancel()
	recomputed, err := op.UpstreamBillRecompute(ctx, request.ChannelID, request.Period)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"recomputed": recomputed})
}

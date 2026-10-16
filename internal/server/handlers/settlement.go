package handlers

import (
	"fmt"
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
	// 交易、退款申诉、结算与毛利分析路由组
	router.NewGroupRouter("/api/v1/trade").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/detail", http.MethodGet).
				Handle(getTradeDetail),
		)

	router.NewGroupRouter("/api/v1/dispute").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/create", http.MethodPost).
				Handle(createDispute),
		).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(listDisputes),
		).
		AddRoute(
			router.NewRoute("/review", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(reviewDispute),
		)

	router.NewGroupRouter("/api/v1/settlement").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(listSettlements),
		).
		AddRoute(
			router.NewRoute("/generate", http.MethodPost).
				Allow(model.RoleAdmin).
				Handle(generateSettlement),
		).
		AddRoute(
			router.NewRoute("/margin", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(getProfitMargin),
		)
}

func getTradeDetail(c *gin.Context) {
	rawID := c.Query("record_id")
	recordID, err := strconv.ParseUint(rawID, 10, 64)
	if err != nil || recordID == 0 {
		resp.Error(c, http.StatusBadRequest, "invalid record_id")
		return
	}
	userID, role := middleware.CurrentUser(c)
	detail, err := op.TradeDetailGet(c.Request.Context(), recordID, userID, model.Role(role))
	if err != nil {
		resp.Error(c, http.StatusForbidden, err.Error())
		return
	}
	resp.Success(c, detail)
}

type createDisputeReq struct {
	RecordID uint64 `json:"record_id"`
	Reason   string `json:"reason"`
}

func createDispute(c *gin.Context) {
	var req createDisputeReq
	if err := c.ShouldBindJSON(&req); err != nil || req.RecordID == 0 {
		resp.Error(c, http.StatusBadRequest, "invalid request")
		return
	}
	userID, _ := middleware.CurrentUser(c)
	dispute, err := op.DisputeCreate(c.Request.Context(), userID, req.RecordID, req.Reason)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "dispute_create", fmt.Sprintf("dispute#%d", dispute.ID), fmt.Sprintf("record#%d: %s", dispute.RecordID, dispute.Reason))
	resp.Success(c, dispute)
}

func listDisputes(c *gin.Context) {
	userID, role := middleware.CurrentUser(c)
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	status := c.Query("status")

	filter := op.DisputeQuery{
		Status: status,
		Limit:  limit,
		Offset: offset,
	}
	if role == model.RoleAdmin {
		if raw := c.Query("user_id"); raw != "" {
			if uid, err := strconv.ParseUint(raw, 10, 64); err == nil {
				filter.UserID = uint(uid)
			}
		}
		if raw := c.Query("owner_id"); raw != "" {
			if oid, err := strconv.ParseUint(raw, 10, 64); err == nil {
				filter.OwnerID = uint(oid)
			}
		}
	} else if role == model.RoleReseller {
		// 渠道商看自己供货的申诉与自己发起的申诉
		filter.OwnerID = userID
	} else {
		// 普通用户只能看自己的申诉
		filter.UserID = userID
	}

	list, total, err := op.DisputeList(c.Request.Context(), filter)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"items": list, "total": total})
}

type reviewDisputeReq struct {
	DisputeID  uint64 `json:"dispute_id"`
	Approve    bool   `json:"approve"`
	ReviewNote string `json:"review_note"`
}

func reviewDispute(c *gin.Context) {
	var req reviewDisputeReq
	if err := c.ShouldBindJSON(&req); err != nil || req.DisputeID == 0 {
		resp.Error(c, http.StatusBadRequest, "invalid request")
		return
	}
	reviewerID, _ := middleware.CurrentUser(c)
	reviewerName := "admin"
	if u, err := op.UserGetByID(reviewerID); err == nil && u.Username != "" {
		reviewerName = u.Username
	}
	err := op.DisputeReview(c.Request.Context(), req.DisputeID, req.Approve, req.ReviewNote, reviewerID, reviewerName)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	action := "dispute_reject"
	if req.Approve {
		action = "dispute_approve"
	}
	audit(c, action, fmt.Sprintf("dispute#%d", req.DisputeID), req.ReviewNote)
	resp.Success(c, "success")
}

func listSettlements(c *gin.Context) {
	userID, role := middleware.CurrentUser(c)
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	period := c.Query("period")
	status := c.Query("status")

	filter := op.SettlementQuery{
		Period: period,
		Status: status,
		Limit:  limit,
		Offset: offset,
	}
	if role == model.RoleAdmin {
		if raw := c.Query("owner_id"); raw != "" {
			if oid, err := strconv.ParseUint(raw, 10, 64); err == nil {
				filter.OwnerID = uint(oid)
			}
		}
	} else {
		filter.OwnerID = userID
	}

	list, total, err := op.SettlementList(c.Request.Context(), filter)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"items": list, "total": total})
}

type generateSettlementReq struct {
	Day string `json:"day"`
}

func generateSettlement(c *gin.Context) {
	var req generateSettlementReq
	_ = c.ShouldBindJSON(&req)
	if req.Day == "" {
		req.Day = time.Now().Format("20060102")
	}
	if err := op.SettlementGenerateDay(c.Request.Context(), req.Day); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "settlement_generate", req.Day, "manual generate settlement statements")
	resp.Success(c, "success")
}

func getProfitMargin(c *gin.Context) {
	var filter op.ProfitMarginQuery
	if raw := c.Query("from"); raw != "" {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			filter.From = t
		}
	}
	if raw := c.Query("to"); raw != "" {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			filter.To = t
		}
	}
	total, trends, err := op.ProfitMarginGet(c.Request.Context(), filter)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"overall": total, "trends": trends})
}

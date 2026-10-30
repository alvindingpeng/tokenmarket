package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"
)

// 工单接口。
//
// 用户端:  create / list / detail / reply / close / reopen(只操作自己的工单)。
// 管理端:  list / detail / reply / assign / status(全部工单, 路由级限 admin)。
//
// 身份一律取自 middleware.CurrentUser —— 登录态中间件从库里读出的 uid/role;
// 请求体/查询串里的任何角色或用户字段都不作数, 普通用户对他人工单的访问由 op 层
// 二次校验后按 403 拒绝。
func init() {
	router.NewGroupRouter("/api/v1/ticket").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("", http.MethodPost).
				Use(middleware.RequireJSON()).
				Handle(createTicket),
		).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(listTickets),
		).
		AddRoute(
			router.NewRoute("/:id", http.MethodGet).
				Handle(getTicket),
		).
		AddRoute(
			router.NewRoute("/:id/reply", http.MethodPost).
				Use(middleware.RequireJSON()).
				Handle(replyTicket),
		).
		AddRoute(
			router.NewRoute("/:id/close", http.MethodPost).
				Handle(closeTicket),
		).
		AddRoute(
			router.NewRoute("/:id/reopen", http.MethodPost).
				Handle(reopenTicket),
		)

	router.NewGroupRouter("/api/v1/admin/ticket").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(adminListTickets),
		).
		AddRoute(
			router.NewRoute("/:id", http.MethodGet).
				Allow(model.RoleAdmin).
				Handle(adminGetTicket),
		).
		AddRoute(
			router.NewRoute("/:id/reply", http.MethodPost).
				Use(middleware.RequireJSON()).
				Allow(model.RoleAdmin).
				Handle(adminReplyTicket),
		).
		AddRoute(
			router.NewRoute("/:id/assign", http.MethodPost).
				Use(middleware.RequireJSON()).
				Allow(model.RoleAdmin).
				Handle(adminAssignTicket),
		).
		AddRoute(
			router.NewRoute("/:id/status", http.MethodPost).
				Use(middleware.RequireJSON()).
				Allow(model.RoleAdmin).
				Handle(adminUpdateTicketStatus),
		)
}

// createTicket POST /api/v1/ticket 创建工单。
func createTicket(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	var req model.TicketCreateRequest
	if !bindTicketJSON(c, &req) {
		return
	}
	view, err := op.TicketCreate(c.Request.Context(), userID, req)
	if err != nil {
		writeTicketError(c, err)
		return
	}
	audit(c, "ticket.create", fmt.Sprintf("#%d", view.ID), view.Title)
	resp.Success(c, gin.H{"ticket": view})
}

// listTickets GET /api/v1/ticket/list 我的工单列表。普通用户只见自己的;
// 管理员(角色来自登录态, 不采信请求参数)可见全部, 与管理端列表同口径。
func listTickets(c *gin.Context) {
	_, role := middleware.CurrentUser(c)
	writeTicketList(c, role == model.RoleAdmin)
}

// adminListTickets GET /api/v1/admin/ticket/list 全部工单列表(筛选、分页)。
func adminListTickets(c *gin.Context) {
	writeTicketList(c, true)
}

// writeTicketList 列表响应: items/total/page/page_size。
func writeTicketList(c *gin.Context, isAdmin bool) {
	userID, _ := middleware.CurrentUser(c)
	var req model.TicketListRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrValidation)
		return
	}
	req.Normalize()
	items, total, err := op.TicketList(c.Request.Context(), userID, isAdmin, req)
	if err != nil {
		writeTicketError(c, err)
		return
	}
	resp.Success(c, gin.H{
		"items":     items,
		"total":     total,
		"page":      req.Page,
		"page_size": req.PageSize,
	})
}

// getTicket GET /api/v1/ticket/:id 工单详情(含消息记录)。
func getTicket(c *gin.Context) {
	writeTicketDetail(c, false)
}

// adminGetTicket GET /api/v1/admin/ticket/:id 工单详情(含消息记录)。
func adminGetTicket(c *gin.Context) {
	writeTicketDetail(c, true)
}

// writeTicketDetail 详情响应: ticket(TicketDetailView, messages 按时间升序)。
func writeTicketDetail(c *gin.Context, isAdmin bool) {
	userID, _ := middleware.CurrentUser(c)
	ticketID, ok := ticketIDFromParam(c)
	if !ok {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	detail, err := op.TicketGet(c.Request.Context(), userID, isAdmin, ticketID)
	if err != nil {
		writeTicketError(c, err)
		return
	}
	resp.Success(c, gin.H{"ticket": detail})
}

// replyTicket POST /api/v1/ticket/:id/reply 用户回复工单。
func replyTicket(c *gin.Context) {
	writeTicketReply(c, false)
}

// adminReplyTicket POST /api/v1/admin/ticket/:id/reply 管理员回复工单。
func adminReplyTicket(c *gin.Context) {
	writeTicketReply(c, true)
}

// writeTicketReply 回复响应: message(落库后的消息视图)。
func writeTicketReply(c *gin.Context, isAdmin bool) {
	userID, _ := middleware.CurrentUser(c)
	ticketID, ok := ticketIDFromParam(c)
	if !ok {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	var req model.TicketReplyRequest
	if !bindTicketJSON(c, &req) {
		return
	}
	message, err := op.TicketReply(c.Request.Context(), userID, isAdmin, ticketID, req)
	if err != nil {
		writeTicketError(c, err)
		return
	}
	audit(c, "ticket.reply", fmt.Sprintf("#%d", ticketID), fmt.Sprintf("message #%d", message.ID))
	resp.Success(c, gin.H{"message": message})
}

// closeTicket POST /api/v1/ticket/:id/close 关闭工单(用户关自己的; 管理员可关任意)。
func closeTicket(c *gin.Context) {
	userID, role := middleware.CurrentUser(c)
	ticketID, ok := ticketIDFromParam(c)
	if !ok {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	if err := op.TicketClose(c.Request.Context(), userID, role == model.RoleAdmin, ticketID); err != nil {
		writeTicketError(c, err)
		return
	}
	audit(c, "ticket.close", fmt.Sprintf("#%d", ticketID), "")
	resp.Success(c, gin.H{"id": ticketID, "status": model.TicketStatusClosed})
}

// reopenTicket POST /api/v1/ticket/:id/reopen 重新打开已关闭工单。
func reopenTicket(c *gin.Context) {
	userID, role := middleware.CurrentUser(c)
	ticketID, ok := ticketIDFromParam(c)
	if !ok {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	if err := op.TicketReopen(c.Request.Context(), userID, role == model.RoleAdmin, ticketID); err != nil {
		writeTicketError(c, err)
		return
	}
	audit(c, "ticket.reopen", fmt.Sprintf("#%d", ticketID), "")
	resp.Success(c, gin.H{"id": ticketID, "status": model.TicketStatusOpen})
}

// adminAssignTicket POST /api/v1/admin/ticket/:id/assign 分配工单(目标必须是在职管理员)。
func adminAssignTicket(c *gin.Context) {
	actorID, _ := middleware.CurrentUser(c)
	ticketID, ok := ticketIDFromParam(c)
	if !ok {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	var req model.TicketAssignRequest
	if !bindTicketJSON(c, &req) {
		return
	}
	if err := op.TicketAssign(c.Request.Context(), actorID, ticketID, req); err != nil {
		writeTicketError(c, err)
		return
	}
	audit(c, "ticket.assign", fmt.Sprintf("#%d", ticketID), fmt.Sprintf("assigned_to=%d", req.AssignedTo))
	resp.Success(c, gin.H{"id": ticketID, "assigned_to": req.AssignedTo})
}

// adminUpdateTicketStatus POST /api/v1/admin/ticket/:id/status 变更状态
// (合法流转见 model.TicketStatusTransitions; 离开 closed 会清空 closed_at)。
func adminUpdateTicketStatus(c *gin.Context) {
	actorID, _ := middleware.CurrentUser(c)
	ticketID, ok := ticketIDFromParam(c)
	if !ok {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	var req model.TicketStatusUpdateRequest
	if !bindTicketJSON(c, &req) {
		return
	}
	if err := op.TicketUpdateStatus(c.Request.Context(), actorID, ticketID, req); err != nil {
		writeTicketError(c, err)
		return
	}
	audit(c, "ticket.status", fmt.Sprintf("#%d", ticketID), req.Status)
	resp.Success(c, gin.H{"id": ticketID, "status": req.Status})
}

// ticketIDFromParam 解析路径参数里的工单 ID; 必须是正整数。
func ticketIDFromParam(c *gin.Context) (uint64, bool) {
	parsed, ok := parsePositiveUint(c.Param("id"))
	return uint64(parsed), ok
}

// bindTicketJSON 绑定 JSON 请求体: JSON 语法/类型错误报 ErrInvalidJSON,
// 字段校验失败报 ErrValidation, 都是 400。
func bindTicketJSON(c *gin.Context, req any) bool {
	if err := c.ShouldBindJSON(req); err != nil {
		var syntaxErr *json.SyntaxError
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &syntaxErr) || errors.As(err, &typeErr) || errors.Is(err, io.ErrUnexpectedEOF) {
			resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		} else {
			resp.Error(c, http.StatusBadRequest, resp.ErrValidation)
		}
		return false
	}
	return true
}

// writeTicketError 把 op 哨兵错误映射为规范 HTTP 状态码:
// 404 不存在 / 403 越权 / 400 输入与分配目标非法 / 409 状态冲突(含已关闭不可回复)。
// 未知错误按 500 处理且不外泄内部细节, 明细只进日志。
func writeTicketError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, op.ErrTicketNotFound):
		resp.Error(c, http.StatusNotFound, resp.ErrResourceNotFound)
	case errors.Is(err, op.ErrTicketForbidden):
		resp.Error(c, http.StatusForbidden, "permission denied")
	case errors.Is(err, op.ErrTicketInvalidInput):
		resp.Error(c, http.StatusBadRequest, err.Error())
	case errors.Is(err, op.ErrTicketAssigneeInvalid):
		resp.Error(c, http.StatusBadRequest, err.Error())
	case errors.Is(err, op.ErrTicketInvalidTransition):
		resp.Error(c, http.StatusConflict, err.Error())
	case errors.Is(err, op.ErrTicketClosed):
		resp.Error(c, http.StatusConflict, err.Error())
	case errors.Is(err, op.ErrTicketConflict):
		// 并发写冲突: 读到的旧状态已被别的请求改掉, 让客户端刷新后重试。
		resp.Error(c, http.StatusConflict, err.Error())
	default:
		log.Warnf("ticket op failed: %v", err)
		resp.Error(c, http.StatusInternalServerError, resp.ErrInternalServer)
	}
}

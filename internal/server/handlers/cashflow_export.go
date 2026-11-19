package handlers

// cashflow_export.go 充值订单与提现单的管理端 CSV 导出。
// 与审计导出同一套约定: 管理员权限 + step-up、上限 5000 行、sanitizeCSVCell
// 防公式注入、导出动作本身落审计; 日期过滤 YYYYMMDD(本地时区), to 含当日全天。

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-gonic/gin"
)

// cashflowExportLimit 单次导出行数上限, 与审计导出一致; 超出截断, 文件尾说明。
const cashflowExportLimit = 5000

func init() {
	router.NewGroupRouter("/api/v1/admin/recharge").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/export", http.MethodGet).
				Use(middleware.RequireStepUp()).
				Allow(model.RoleAdmin).
				Handle(exportRechargeOrders),
		)
	router.NewGroupRouter("/api/v1/admin/withdrawal").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/export", http.MethodGet).
				Use(middleware.RequireStepUp()).
				Allow(model.RoleAdmin).
				Handle(exportWithdrawals),
		)
}

// cashflowDayParam 解析日期过滤参数: YYYYMMDD(兼容 YYYY-MM-DD); 空串返回零值(不过滤)。
// 非法格式回 400 —— 静默忽略会让管理员误以为导出了全量。
func cashflowDayParam(c *gin.Context, name string) (time.Time, bool) {
	compact := strings.ReplaceAll(strings.TrimSpace(c.Query(name)), "-", "")
	if compact == "" {
		return time.Time{}, true
	}
	parsed, err := time.ParseInLocation("20060102", compact, time.Local)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid "+name+", want YYYYMMDD")
		return time.Time{}, false
	}
	return parsed, true
}

// cashflowUserIDParam 解析 user_id 过滤; 空串=不过滤, 非法直接 400。
func cashflowUserIDParam(c *gin.Context) (uint, bool) {
	raw := strings.TrimSpace(c.Query("user_id"))
	if raw == "" {
		return 0, true
	}
	parsed, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || parsed == 0 {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return 0, false
	}
	return uint(parsed), true
}

// exportRechargeOrders 充值订单 CSV: 列含下单/支付币种与汇率快照, 供财务按单核对。
func exportRechargeOrders(c *gin.Context) {
	userID, ok := cashflowUserIDParam(c)
	if !ok {
		return
	}
	from, ok := cashflowDayParam(c, "from")
	if !ok {
		return
	}
	to, ok := cashflowDayParam(c, "to")
	if !ok {
		return
	}
	if !to.IsZero() {
		to = to.AddDate(0, 0, 1) // created_at < to+1d, 含 to 当日全天
	}
	query := op.RechargeExportQuery{UserID: userID, Status: strings.TrimSpace(c.Query("status")), From: from, To: to}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	rows, err := op.RechargeExport(ctx, query, cashflowExportLimit)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=switchyard-recharge-"+time.Now().Format("20060102-150405")+".csv")
	writer := csv.NewWriter(c.Writer)
	_ = writer.Write([]string{"id", "order_no", "user_id", "amount_cents", "currency", "pay_cents", "pay_currency",
		"rate", "rate_day", "rate_source", "credits", "bonus", "status", "created_at"})
	for _, item := range rows {
		_ = writer.Write([]string{
			strconv.FormatUint(item.ID, 10),
			sanitizeCSVCell(item.OrderNo),
			strconv.FormatUint(uint64(item.UserID), 10),
			strconv.FormatInt(item.AmountCents, 10),
			sanitizeCSVCell(item.Currency),
			strconv.FormatInt(item.PayCents, 10),
			sanitizeCSVCell(item.PayCurrency),
			strconv.FormatFloat(item.Rate, 'f', -1, 64),
			sanitizeCSVCell(item.RateDay),
			sanitizeCSVCell(item.RateSource),
			strconv.FormatFloat(item.Credits, 'f', 6, 64),
			strconv.FormatFloat(item.Bonus, 'f', 6, 64),
			sanitizeCSVCell(item.Status),
			item.CreatedAt.Format(time.RFC3339),
		})
	}
	if len(rows) >= cashflowExportLimit {
		_ = writer.Write([]string{"", "", "", "", "", "", "", "", "", "", "", "", "", fmt.Sprintf("# truncated: exported %d rows (limit %d)", len(rows), cashflowExportLimit)})
	}
	writer.Flush()
	audit(c, "recharge.export", fmt.Sprintf("n=%d", len(rows)),
		fmt.Sprintf("user_id=%d status=%s from=%s to=%s", userID, strings.TrimSpace(c.Query("status")), c.Query("from"), c.Query("to")))
}

// exportWithdrawals 提现单 CSV: 列含申请/打款币种与汇率快照; applied_at 时间口径。
func exportWithdrawals(c *gin.Context) {
	userID, ok := cashflowUserIDParam(c)
	if !ok {
		return
	}
	from, ok := cashflowDayParam(c, "from")
	if !ok {
		return
	}
	to, ok := cashflowDayParam(c, "to")
	if !ok {
		return
	}
	if !to.IsZero() {
		to = to.AddDate(0, 0, 1)
	}
	query := op.WithdrawExportQuery{UserID: userID, Status: strings.TrimSpace(c.Query("status")), From: from, To: to}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	rows, err := op.WithdrawExport(ctx, query, cashflowExportLimit)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=switchyard-withdrawal-"+time.Now().Format("20060102-150405")+".csv")
	writer := csv.NewWriter(c.Writer)
	_ = writer.Write([]string{"id", "user_id", "amount", "fee", "actual", "currency", "actual_cents", "actual_currency",
		"rate", "rate_day", "rate_source", "method", "status", "applied_at", "paid_at"})
	for _, item := range rows {
		paidAt := ""
		if item.PaidAt != nil {
			paidAt = item.PaidAt.Format(time.RFC3339)
		}
		_ = writer.Write([]string{
			strconv.FormatUint(uint64(item.ID), 10),
			strconv.FormatUint(uint64(item.UserID), 10),
			strconv.FormatFloat(item.Amount, 'f', 6, 64),
			strconv.FormatFloat(item.Fee, 'f', 6, 64),
			strconv.FormatFloat(item.Actual, 'f', 6, 64),
			sanitizeCSVCell(item.Currency),
			strconv.FormatInt(item.ActualCents, 10),
			sanitizeCSVCell(item.ActualCurrency),
			strconv.FormatFloat(item.Rate, 'f', -1, 64),
			sanitizeCSVCell(item.RateDay),
			sanitizeCSVCell(item.RateSource),
			sanitizeCSVCell(item.Method),
			sanitizeCSVCell(string(item.Status)),
			item.AppliedAt.Format(time.RFC3339),
			paidAt,
		})
	}
	if len(rows) >= cashflowExportLimit {
		_ = writer.Write([]string{"", "", "", "", "", "", "", "", "", "", "", "", "", "", fmt.Sprintf("# truncated: exported %d rows (limit %d)", len(rows), cashflowExportLimit)})
	}
	writer.Flush()
	audit(c, "withdrawal.export", fmt.Sprintf("n=%d", len(rows)),
		fmt.Sprintf("user_id=%d status=%s from=%s to=%s", userID, strings.TrimSpace(c.Query("status")), c.Query("from"), c.Query("to")))
}

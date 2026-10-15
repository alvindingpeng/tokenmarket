package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

// 充值接口: 报价/下单/订单对用户, 人工补单对管理员; 支付回调与回跳公开无鉴权。
func init() {
	router.NewGroupRouter("/api/v1/recharge").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/view", http.MethodGet).
				Handle(rechargeView),
		).
		AddRoute(
			router.NewRoute("/quote", http.MethodPost).
				Use(middleware.RequireJSON()).
				Handle(rechargeQuote),
		).
		AddRoute(
			router.NewRoute("/create", http.MethodPost).
				Use(middleware.RequireJSON()).
				Handle(createRecharge),
		).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(listRecharge),
		).
		AddRoute(
			router.NewRoute("/complete", http.MethodPost).
				Use(middleware.RequireJSON()).
				Allow(model.RoleAdmin).
				Handle(completeRecharge),
		)
	router.NewGroupRouter("/api/v1/pay").
		AddRoute(
			router.NewRoute("/notify/:provider", http.MethodPost).
				Handle(payNotify),
		).
		AddRoute(
			router.NewRoute("/notify/:provider", http.MethodGet).
				Handle(payNotify),
		).
		AddRoute(
			router.NewRoute("/return/:provider", http.MethodGet).
				Handle(payReturn),
		)
}

// rechargeCallbackBase 对外回调基址: 优先取设置的 recharge_notify_base,
// 留空按当前请求推断(反代子路径部署请在设置里显式配置)。
func rechargeCallbackBase(c *gin.Context) string {
	if configured, _ := op.SettingGetString(model.SettingKeyRechargeNotifyBase); strings.TrimSpace(configured) != "" {
		return strings.TrimRight(strings.TrimSpace(configured), "/")
	}
	scheme := "http"
	if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + c.Request.Host
}

// rechargeView 充值配置视图: 是否已配置、汇率、快捷金额、满赠开关。
func rechargeView(c *gin.Context) {
	resp.Success(c, op.RechargeViewGet(c.Request.Context()))
}

// rechargeQuote 充值报价: 实付金额(分)换算入账余额与满赠。
func rechargeQuote(c *gin.Context) {
	var req struct {
		AmountCents int64 `json:"amount_cents"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	credits, bonus, rate, err := op.RechargeQuote(c.Request.Context(), req.AmountCents)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{"credits": credits, "bonus": bonus, "rate": rate})
}

// createRecharge 创建充值订单并返回收银台表单(前端自动提交跳转)。
func createRecharge(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	var req struct {
		AmountCents int64  `json:"amount_cents"`
		Method      string `json:"method"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	base := rechargeCallbackBase(c)
	notifyURL := base + "/api/v1/pay/notify/" + model.PayProviderEpay
	returnURL := base + "/api/v1/pay/return/" + model.PayProviderEpay
	order, form, err := op.RechargeCreate(c.Request.Context(), userID, req.AmountCents, req.Method, notifyURL, returnURL)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "recharge.create", order.OrderNo,
		fmt.Sprintf("amount=%d%s credits=%.6f bonus=%.6f", order.AmountCents, order.Currency, order.Credits, order.Bonus))
	resp.Success(c, gin.H{"order": order, "form": form})
}

// listRecharge 充值订单: 用户看自己, 管理员可按 user_id 看指定用户(不传看全部)。
func listRecharge(c *gin.Context) {
	userID, role := middleware.CurrentUser(c)
	if role == model.RoleAdmin {
		userID = 0
		if raw := strings.TrimSpace(c.Query("user_id")); raw != "" {
			for _, ch := range raw {
				if ch < '0' || ch > '9' {
					resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
					return
				}
			}
			userID = uint(mustAtoi(raw))
		}
	}
	orders, err := op.RechargeList(c.Request.Context(), userID, mustAtoi(c.DefaultQuery("limit", "50")))
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"items": orders})
}

// mustAtoi 字符串转整数, 解析失败按 0 处理。
func mustAtoi(value string) int {
	number := 0
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return 0
		}
		number = number*10 + int(ch-'0')
	}
	return number
}

// completeRecharge 人工补单: 第三方已扣款但回调丢失时补入账。
func completeRecharge(c *gin.Context) {
	userID, _ := middleware.CurrentUser(c)
	var req struct {
		OrderNo string `json:"order_no"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if err := op.RechargeManualComplete(c.Request.Context(), strings.TrimSpace(req.OrderNo), userID, operatorName(c)); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	audit(c, "recharge.complete", strings.TrimSpace(req.OrderNo), "manual")
	resp.Success(c, gin.H{"order_no": req.OrderNo})
}

// payNotify 支付回调(公开端点): 验签+核金额+幂等入账, 明文回 success 才停止重投。
func payNotify(c *gin.Context) {
	if provider := c.Param("provider"); provider != model.PayProviderEpay {
		c.String(http.StatusOK, "fail")
		return
	}
	query := c.Request.URL.Query()
	if c.Request.Method == http.MethodPost {
		if err := c.Request.ParseForm(); err == nil && len(c.Request.Form) > 0 {
			query = c.Request.Form
		}
	}
	if err := op.RechargeNotify(c.Request.Context(), query); err != nil {
		c.String(http.StatusOK, "fail")
		return
	}
	c.String(http.StatusOK, "success")
}

// payReturn 浏览器回跳: 只做签名确认与结果页跳转, 入账一律以 notify 为准。
func payReturn(c *gin.Context) {
	if provider := c.Param("provider"); provider != model.PayProviderEpay {
		c.Redirect(http.StatusFound, "/")
		return
	}
	base := rechargeCallbackBase(c)
	query := c.Request.URL.Query()
	// 验签只决定提示语, 不决定入账; 伪造回跳最多误导一次展示。
	if _, _, _, err := op.RechargeVerifyReturn(query); err != nil {
		c.Redirect(http.StatusFound, base+"/?recharge_result=failed")
		return
	}
	status := query.Get("trade_status")
	result := "failed"
	if strings.EqualFold(status, "TRADE_SUCCESS") {
		result = "paid"
	}
	c.Redirect(http.StatusFound, base+"/?recharge_result="+result+"&order_no="+query.Get("out_trade_no"))
}

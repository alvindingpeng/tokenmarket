package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/auth"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-gonic/gin"
)

// ticketAPIEnv 最小环境: 临时 sqlite、签名密钥、两名用户与两名管理员(均返回登录令牌)。
func ticketAPIEnv(t *testing.T) (userToken, otherToken, adminToken, admin2Token string, user1, user2, admin1, admin2 model.User) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "ticket-api.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		// 通知是异步投递: 等一拍让在途 goroutine 落进本库(或明确失败), 再关库,
		// 免得迟到的写入落进下一个用例的库。
		time.Sleep(300 * time.Millisecond)
		_ = db.Close()
	})
	if err := op.SettingUpsert(model.SettingKeyAdminDefaultPwdChanged, "true"); err != nil {
		t.Fatal(err)
	}
	if err := auth.InitSecret(); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	mk := func(name string, role model.Role) (model.User, string) {
		u, err := op.UserCreate(name, "", "password123", role, model.StatusActive)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		t.Cleanup(func() { _ = op.UserDelete(u.ID) })
		token, _, err := auth.GenerateJWTToken(u, 3600)
		if err != nil {
			t.Fatalf("token for %s: %v", name, err)
		}
		return u, token
	}
	user1, userToken = mk("tkh-user1", model.RoleUser)
	user2, otherToken = mk("tkh-user2", model.RoleUser)
	admin1, adminToken = mk("tkh-admin1", model.RoleAdmin)
	admin2, admin2Token = mk("tkh-admin2", model.RoleAdmin)
	return
}

// ticketEngine 走真实路由注册表构建引擎(含 RequireRole/RequireJSON 等中间件)。
func ticketEngine(t *testing.T) *gin.Engine {
	t.Helper()
	engine := gin.New()
	if err := router.RegisterAll(engine); err != nil {
		t.Fatalf("register routes: %v", err)
	}
	return engine
}

// ticketAPI 发一个请求; token 为空表示匿名。
func ticketAPI(t *testing.T, engine *gin.Engine, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.AddCookie(&http.Cookie{Name: "auth", Value: token})
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

// ticketAPIData 解开成功响应的 data 载荷。
func ticketAPIData(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("parse response %s: %v", w.Body.String(), err)
	}
	if payload.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", payload.Code, w.Body.String())
	}
	return payload.Data
}

// ticketCreateViaAPI 走接口建单, 返回工单 ID。
func ticketCreateViaAPI(t *testing.T, engine *gin.Engine, token, title string) uint64 {
	t.Helper()
	w := ticketAPI(t, engine, http.MethodPost, "/api/v1/ticket", token,
		fmt.Sprintf(`{"title":%q,"content":"问题描述"}`, title))
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	data := ticketAPIData(t, w)
	ticket, ok := data["ticket"].(map[string]any)
	if !ok {
		t.Fatalf("response has no ticket: %s", w.Body.String())
	}
	return uint64(ticket["id"].(float64))
}

// ticketNotifCount 统计某人收到的某工单通知条数。
func ticketNotifCount(t *testing.T, userID uint, ticketID uint64) int64 {
	t.Helper()
	var count int64
	if err := db.GetDB().Model(&model.Notification{}).
		Where("user_id = ? AND type = ? AND related_id = ?", userID, model.NotificationTypeTicket, ticketID).
		Count(&count).Error; err != nil {
		t.Fatalf("count notifications: %v", err)
	}
	return count
}

// waitTicketNotifCount 轮询等待通知条数达标(通知为异步投递)。
func waitTicketNotifCount(t *testing.T, userID uint, ticketID uint64, want int64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if got := ticketNotifCount(t, userID, ticketID); got >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("notifications for user %d ticket #%d = %d, want >= %d (timeout)",
				userID, ticketID, ticketNotifCount(t, userID, ticketID), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// settleTicketNotifs 通知异步投递的静默期: 负向断言前先等一拍。
func settleTicketNotifs() { time.Sleep(300 * time.Millisecond) }

// TestTicketUserLifecycleRoutes 用户端全流程: create/list/detail/reply/close/reopen,
// 全部走真实路由; 建单与用户回复通知管理员而非自己。
func TestTicketUserLifecycleRoutes(t *testing.T) {
	userToken, _, adminToken, _, user1, _, admin1, admin2 := ticketAPIEnv(t)
	engine := ticketEngine(t)

	id := ticketCreateViaAPI(t, engine, userToken, "登录失败")

	// 建单通知两名在职管理员, 不通知提交者(通知异步投递, 轮询等待)。
	waitTicketNotifCount(t, admin1.ID, id, 1)
	waitTicketNotifCount(t, admin2.ID, id, 1)
	settleTicketNotifs()
	if got := ticketNotifCount(t, user1.ID, id); got != 0 {
		t.Fatalf("creator self notifications = %d, want 0", got)
	}

	// 列表。
	w := ticketAPI(t, engine, http.MethodGet, "/api/v1/ticket/list", userToken, "")
	data := ticketAPIData(t, w)
	if total, _ := data["total"].(float64); total != 1 {
		t.Fatalf("list total = %v", data["total"])
	}
	if data["page"].(float64) != 1 || data["page_size"].(float64) != 20 {
		t.Fatalf("pagination echo: %v %v", data["page"], data["page_size"])
	}

	// 详情。
	w = ticketAPI(t, engine, http.MethodGet, fmt.Sprintf("/api/v1/ticket/%d", id), userToken, "")
	data = ticketAPIData(t, w)
	ticket := data["ticket"].(map[string]any)
	if ticket["status"].(string) != model.TicketStatusOpen {
		t.Fatalf("status = %v", ticket["status"])
	}
	if _, has := ticket["closed_at"]; has {
		t.Fatalf("open ticket must not carry closed_at: %v", ticket["closed_at"])
	}

	// 用户回复。
	w = ticketAPI(t, engine, http.MethodPost, fmt.Sprintf("/api/v1/ticket/%d/reply", id), userToken, `{"content":"补充截图"}`)
	data = ticketAPIData(t, w)
	message := data["message"].(map[string]any)
	if message["content"].(string) != "补充截图" || message["is_admin"].(bool) {
		t.Fatalf("message = %v", message)
	}
	// 用户回复 -> 管理员, 不是自己。
	waitTicketNotifCount(t, admin1.ID, id, 2) // 建单 + 回复
	settleTicketNotifs()
	if got := ticketNotifCount(t, user1.ID, id); got != 0 {
		t.Fatalf("user reply self notifications = %d, want 0", got)
	}

	// 管理员回复 -> 工单主人。
	w = ticketAPI(t, engine, http.MethodPost, fmt.Sprintf("/api/v1/admin/ticket/%d/reply", id), adminToken, `{"content":"请提供请求 ID"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("admin reply: %d %s", w.Code, w.Body.String())
	}
	waitTicketNotifCount(t, user1.ID, id, 1)

	// 关闭。
	w = ticketAPI(t, engine, http.MethodPost, fmt.Sprintf("/api/v1/ticket/%d/close", id), userToken, "")
	data = ticketAPIData(t, w)
	if data["status"].(string) != model.TicketStatusClosed {
		t.Fatalf("close status = %v", data["status"])
	}

	// 已关闭不可回复(409)。
	if w := ticketAPI(t, engine, http.MethodPost, fmt.Sprintf("/api/v1/ticket/%d/reply", id), userToken, `{"content":"还想说"}`); w.Code != http.StatusConflict {
		t.Fatalf("reply on closed: %d %s", w.Code, w.Body.String())
	}

	// 重新打开: closed_at 清空。
	if w := ticketAPI(t, engine, http.MethodPost, fmt.Sprintf("/api/v1/ticket/%d/reopen", id), userToken, ""); w.Code != http.StatusOK {
		t.Fatalf("reopen: %d %s", w.Code, w.Body.String())
	}
	w = ticketAPI(t, engine, http.MethodGet, fmt.Sprintf("/api/v1/ticket/%d", id), userToken, "")
	ticket = ticketAPIData(t, w)["ticket"].(map[string]any)
	if ticket["status"].(string) != model.TicketStatusOpen {
		t.Fatalf("reopened status = %v", ticket["status"])
	}
	if _, has := ticket["closed_at"]; has {
		t.Fatalf("closed_at not cleared on reopen: %v", ticket["closed_at"])
	}
}

// TestTicketOwnershipAndRoleGates 越权: 用户碰他人工单一律 403; 管理端路由角色闸门;
// 匿名 401。
func TestTicketOwnershipAndRoleGates(t *testing.T) {
	userToken, otherToken, _, _, _, _, _, _ := ticketAPIEnv(t)
	engine := ticketEngine(t)

	id := ticketCreateViaAPI(t, engine, userToken, "他人勿动")

	// 另一用户: 详情/回复/关闭/重开全部 403。
	cases := []struct {
		method, path, body string
	}{
		{http.MethodGet, fmt.Sprintf("/api/v1/ticket/%d", id), ""},
		{http.MethodPost, fmt.Sprintf("/api/v1/ticket/%d/reply", id), `{"content":"偷看"}`},
		{http.MethodPost, fmt.Sprintf("/api/v1/ticket/%d/close", id), ""},
		{http.MethodPost, fmt.Sprintf("/api/v1/ticket/%d/reopen", id), ""},
	}
	for _, tc := range cases {
		if w := ticketAPI(t, engine, tc.method, tc.path, otherToken, tc.body); w.Code != http.StatusForbidden {
			t.Fatalf("%s %s as other user: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}

	// 他人列表只见自己的。
	w := ticketAPI(t, engine, http.MethodGet, "/api/v1/ticket/list", otherToken, "")
	if total := ticketAPIData(t, w)["total"].(float64); total != 0 {
		t.Fatalf("other user list total = %v, want 0", total)
	}

	// 普通用户进管理端: 403(路由级角色闸门)。
	adminCases := []struct {
		method, path, body string
	}{
		{http.MethodGet, "/api/v1/admin/ticket/list", ""},
		{http.MethodGet, fmt.Sprintf("/api/v1/admin/ticket/%d", id), ""},
		{http.MethodPost, fmt.Sprintf("/api/v1/admin/ticket/%d/reply", id), `{"content":"x"}`},
		{http.MethodPost, fmt.Sprintf("/api/v1/admin/ticket/%d/assign", id), `{"assigned_to":1}`},
		{http.MethodPost, fmt.Sprintf("/api/v1/admin/ticket/%d/status", id), `{"status":"closed"}`},
	}
	for _, tc := range adminCases {
		if w := ticketAPI(t, engine, tc.method, tc.path, otherToken, tc.body); w.Code != http.StatusForbidden {
			t.Fatalf("%s %s as user: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}

	// 匿名: 401。
	if w := ticketAPI(t, engine, http.MethodGet, "/api/v1/ticket/list", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list: %d %s", w.Code, w.Body.String())
	}
	if w := ticketAPI(t, engine, http.MethodGet, "/api/v1/admin/ticket/list", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous admin list: %d %s", w.Code, w.Body.String())
	}

	// 请求体里的伪造角色/用户 ID 不起作用: otherToken 自称管理员也只看得到自己的(0 张)。
	w = ticketAPI(t, engine, http.MethodGet, "/api/v1/ticket/list?user_id=1&role=admin", otherToken, "")
	if total := ticketAPIData(t, w)["total"].(float64); total != 0 {
		t.Fatalf("forged identity changed scope: %v", total)
	}
}

// TestTicketResellerRouteGates reseller 走用户端按普通用户收口(只见自己的), 管理端 403。
func TestTicketResellerRouteGates(t *testing.T) {
	_, userToken, _, _, _, _, _, _ := ticketAPIEnv(t)
	engine := ticketEngine(t)

	seller, err := op.UserCreate("tkh-reseller", "", "password123", model.RoleReseller, model.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = op.UserDelete(seller.ID) })
	sellerToken, _, err := auth.GenerateJWTToken(seller, 3600)
	if err != nil {
		t.Fatal(err)
	}

	// reseller 建单、查自己的列表正常。
	id := ticketCreateViaAPI(t, engine, sellerToken, "渠道商的单")
	w := ticketAPI(t, engine, http.MethodGet, "/api/v1/ticket/list", sellerToken, "")
	if total := ticketAPIData(t, w)["total"].(float64); total != 1 {
		t.Fatalf("reseller list total = %v, want 1", total)
	}
	// 另一用户看不到 reseller 的单。
	w = ticketAPI(t, engine, http.MethodGet, "/api/v1/ticket/list", userToken, "")
	if total := ticketAPIData(t, w)["total"].(float64); total != 0 {
		t.Fatalf("other user sees reseller tickets: %v", total)
	}
	if w := ticketAPI(t, engine, http.MethodGet, fmt.Sprintf("/api/v1/ticket/%d", id), userToken, ""); w.Code != http.StatusForbidden {
		t.Fatalf("cross-user detail: %d %s", w.Code, w.Body.String())
	}
	// 管理端一律 403(角色闸门不认 reseller)。
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/admin/ticket/list", ""},
		{http.MethodGet, fmt.Sprintf("/api/v1/admin/ticket/%d", id), ""},
		{http.MethodPost, fmt.Sprintf("/api/v1/admin/ticket/%d/status", id), `{"status":"closed"}`},
	} {
		if w := ticketAPI(t, engine, tc.method, tc.path, sellerToken, tc.body); w.Code != http.StatusForbidden {
			t.Fatalf("%s %s as reseller: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}

// TestTicketAdminRoutesFlow 管理端: list/detail/reply/assign/status, 含分配校验、
// 状态流转(409)、closed_at 维护与通知。
func TestTicketAdminRoutesFlow(t *testing.T) {
	userToken, otherToken, adminToken, admin2Token, user1, user2, _, admin2 := ticketAPIEnv(t)
	engine := ticketEngine(t)

	id := ticketCreateViaAPI(t, engine, userToken, "管理端流程")

	// 管理员可见全部(包括他人建单)。
	w := ticketAPI(t, engine, http.MethodGet, "/api/v1/admin/ticket/list", adminToken, "")
	if total := ticketAPIData(t, w)["total"].(float64); total != 1 {
		t.Fatalf("admin list total = %v", total)
	}

	// 详情。
	if w := ticketAPI(t, engine, http.MethodGet, fmt.Sprintf("/api/v1/admin/ticket/%d", id), adminToken, ""); w.Code != http.StatusOK {
		t.Fatalf("admin detail: %d %s", w.Code, w.Body.String())
	}
	if w := ticketAPI(t, engine, http.MethodGet, fmt.Sprintf("/api/v1/admin/ticket/%d", id), adminToken, ""); w.Code != http.StatusOK {
		t.Fatalf("admin detail: %d %s", w.Code, w.Body.String())
	}

	// 分配: 非管理员目标 400; 不存在目标 400; 合法管理员 200 且收到通知。
	if w := ticketAPI(t, engine, http.MethodPost, fmt.Sprintf("/api/v1/admin/ticket/%d/assign", id), adminToken,
		fmt.Sprintf(`{"assigned_to":%d}`, user2.ID)); w.Code != http.StatusBadRequest {
		t.Fatalf("assign to user: %d %s", w.Code, w.Body.String())
	}
	if w := ticketAPI(t, engine, http.MethodPost, fmt.Sprintf("/api/v1/admin/ticket/%d/assign", id), adminToken,
		`{"assigned_to":999999}`); w.Code != http.StatusBadRequest {
		t.Fatalf("assign to ghost: %d %s", w.Code, w.Body.String())
	}
	if w := ticketAPI(t, engine, http.MethodPost, fmt.Sprintf("/api/v1/admin/ticket/%d/assign", id), adminToken,
		fmt.Sprintf(`{"assigned_to":%d}`, admin2.ID)); w.Code != http.StatusOK {
		t.Fatalf("assign to admin: %d %s", w.Code, w.Body.String())
	}
	waitTicketNotifCount(t, admin2.ID, id, 2) // 建单 + 分配

	// 状态流转: open->in_progress->resolved; resolved->open 非法(409); resolved->closed 记 closed_at。
	if w := ticketAPI(t, engine, http.MethodPost, fmt.Sprintf("/api/v1/admin/ticket/%d/status", id), adminToken,
		`{"status":"in_progress"}`); w.Code != http.StatusOK {
		t.Fatalf("to in_progress: %d %s", w.Code, w.Body.String())
	}
	if w := ticketAPI(t, engine, http.MethodPost, fmt.Sprintf("/api/v1/admin/ticket/%d/status", id), adminToken,
		`{"status":"resolved"}`); w.Code != http.StatusOK {
		t.Fatalf("to resolved: %d %s", w.Code, w.Body.String())
	}
	if w := ticketAPI(t, engine, http.MethodPost, fmt.Sprintf("/api/v1/admin/ticket/%d/status", id), adminToken,
		`{"status":"open"}`); w.Code != http.StatusConflict {
		t.Fatalf("resolved->open must be 409: %d %s", w.Code, w.Body.String())
	}
	if w := ticketAPI(t, engine, http.MethodPost, fmt.Sprintf("/api/v1/admin/ticket/%d/status", id), adminToken,
		`{"status":"closed"}`); w.Code != http.StatusOK {
		t.Fatalf("to closed: %d %s", w.Code, w.Body.String())
	}
	w = ticketAPI(t, engine, http.MethodGet, fmt.Sprintf("/api/v1/admin/ticket/%d", id), adminToken, "")
	ticket := ticketAPIData(t, w)["ticket"].(map[string]any)
	if _, has := ticket["closed_at"]; !has {
		t.Fatalf("closed ticket must carry closed_at: %s", w.Body.String())
	}

	// 状态变更通知工单主人(且不通知操作者本人之外的人)。
	waitTicketNotifCount(t, user1.ID, id, 1)

	// 另一管理员操作不受影响(角色相同, 各自会话)。
	if w := ticketAPI(t, engine, http.MethodPost, fmt.Sprintf("/api/v1/admin/ticket/%d/status", id), admin2Token,
		`{"status":"open"}`); w.Code != http.StatusOK {
		t.Fatalf("reopen via status: %d %s", w.Code, w.Body.String())
	}

	// 越权再验: otherToken 建的工单, admin 能操作(关闭走 status 端点, 管理端没有独立 close 路由)。
	otherID := ticketCreateViaAPI(t, engine, otherToken, "他人的单")
	if w := ticketAPI(t, engine, http.MethodPost, fmt.Sprintf("/api/v1/admin/ticket/%d/status", otherID), adminToken,
		`{"status":"closed"}`); w.Code != http.StatusOK {
		t.Fatalf("admin close others via status: %d %s", w.Code, w.Body.String())
	}
	_ = user1
}

// TestTicketRequestValidation 输入与分页校验、错误码规范。
func TestTicketRequestValidation(t *testing.T) {
	userToken, _, _, _, _, _, _, _ := ticketAPIEnv(t)
	engine := ticketEngine(t)

	// 空白标题/正文: 400。
	for _, body := range []string{
		`{"title":"   ","content":"x"}`,
		`{"title":"x","content":"  "}`,
		`{"title":"","content":"x"}`,
	} {
		if w := ticketAPI(t, engine, http.MethodPost, "/api/v1/ticket", userToken, body); w.Code != http.StatusBadRequest {
			t.Fatalf("invalid create %s: %d %s", body, w.Code, w.Body.String())
		}
	}

	// 超长标题(256 字符): 400。
	if w := ticketAPI(t, engine, http.MethodPost, "/api/v1/ticket", userToken,
		fmt.Sprintf(`{"title":"%s","content":"x"}`, strings.Repeat("a", 256))); w.Code != http.StatusBadRequest {
		t.Fatalf("long title: %d %s", w.Code, w.Body.String())
	}

	// 非法 JSON: 400。
	if w := ticketAPI(t, engine, http.MethodPost, "/api/v1/ticket", userToken, `{"title":`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d %s", w.Code, w.Body.String())
	}

	// 缺 Content-Type: 415(RequireJSON)。
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ticket", strings.NewReader(`{"title":"x","content":"y"}`))
	req.AddCookie(&http.Cookie{Name: "auth", Value: userToken})
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("missing content type: %d %s", w.Code, w.Body.String())
	}

	// 分页越界与非法筛选: 400。
	badQueries := []string{
		"/api/v1/ticket/list?page_size=200",
		"/api/v1/ticket/list?page=-1",
		"/api/v1/ticket/list?status=bogus",
		"/api/v1/ticket/list?priority=huge",
		"/api/v1/ticket/list?category=nope",
	}
	for _, q := range badQueries {
		if w := ticketAPI(t, engine, http.MethodGet, q, userToken, ""); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", q, w.Code, w.Body.String())
		}
	}

	// 非法工单 ID: 400; 不存在: 404。
	if w := ticketAPI(t, engine, http.MethodGet, "/api/v1/ticket/abc", userToken, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d %s", w.Code, w.Body.String())
	}
	if w := ticketAPI(t, engine, http.MethodGet, "/api/v1/ticket/999999", userToken, ""); w.Code != http.StatusNotFound {
		t.Fatalf("missing id: %d %s", w.Code, w.Body.String())
	}

	// 回复正文空白/超长: 400。
	id := ticketCreateViaAPI(t, engine, userToken, "校验单")
	if w := ticketAPI(t, engine, http.MethodPost, fmt.Sprintf("/api/v1/ticket/%d/reply", id), userToken, `{"content":"   "}`); w.Code != http.StatusBadRequest {
		t.Fatalf("blank reply: %d %s", w.Code, w.Body.String())
	}
	huge := fmt.Sprintf(`{"content":"%s"}`, strings.Repeat("a", 10001))
	if w := ticketAPI(t, engine, http.MethodPost, fmt.Sprintf("/api/v1/ticket/%d/reply", id), userToken, huge); w.Code != http.StatusBadRequest {
		t.Fatalf("huge reply: %d %s", w.Code, w.Body.String())
	}
}

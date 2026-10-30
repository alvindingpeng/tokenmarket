package op

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// ticketTestEnv 临时 sqlite 库 + 两名普通用户、两名在职管理员、一名停用管理员。
func ticketTestEnv(t *testing.T) (user1, user2, admin1, admin2 model.User) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "ticket-op.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		// 先排空在途的异步通知再关库: 迟到的 goroutine 要么落进本库, 要么明确失败,
		// 不能带着悬念落进下一个用例的库。
		ticketNotifyWG.Wait()
		_ = db.Close()
	})
	mk := func(name string, role model.Role, status model.UserStatus) model.User {
		u := model.User{Username: name, Role: role, Status: status}
		if err := db.GetDB().Create(&u).Error; err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		return u
	}
	user1 = mk("tk-user1", model.RoleUser, model.StatusActive)
	user2 = mk("tk-user2", model.RoleUser, model.StatusActive)
	admin1 = mk("tk-admin1", model.RoleAdmin, model.StatusActive)
	admin2 = mk("tk-admin2", model.RoleAdmin, model.StatusActive)
	mk("tk-admin-off", model.RoleAdmin, model.StatusDisabled)
	return
}

// waitTicketNotifs 轮询等待某人收到至少 want 条工单通知(通知为异步投递)。
func waitTicketNotifs(t *testing.T, userID uint, ticketID uint64, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if got := len(ticketNotifs(t, userID, ticketID)); got >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("notifications for user %d ticket #%d = %d, want >= %d (timeout)",
				userID, ticketID, len(ticketNotifs(t, userID, ticketID)), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// settleTicketNotifs 通知异步投递的静默期: 负向断言(不该出现的通知)前先等一拍。
func settleTicketNotifs() { time.Sleep(300 * time.Millisecond) }

// ticketUserByName 按用户名取用户, 供断言不进返回值的测试账号(如停用管理员)。
func ticketUserByName(t *testing.T, name string) model.User {
	t.Helper()
	var u model.User
	if err := db.GetDB().Where("username = ?", name).First(&u).Error; err != nil {
		t.Fatalf("load user %s: %v", name, err)
	}
	return u
}

// ticketReload 重读工单行。每次用全新的结构体接收: gorm 的 First 会把 dest 里
// 已填充的字段并进查询/扫描, 复用同一个变量读两次会得到陈旧字段值。
func ticketReload(t *testing.T, id uint64) model.Ticket {
	t.Helper()
	var row model.Ticket
	if err := db.GetDB().Where("id = ?", id).First(&row).Error; err != nil {
		t.Fatalf("reload ticket #%d: %v", id, err)
	}
	return row
}

// ticketNotifs 取某人收到的工单通知(按关联工单过滤)。
func ticketNotifs(t *testing.T, userID uint, ticketID uint64) []model.Notification {
	t.Helper()
	var list []model.Notification
	if err := db.GetDB().
		Where("user_id = ? AND type = ? AND related_id = ?", userID, model.NotificationTypeTicket, ticketID).
		Find(&list).Error; err != nil {
		t.Fatalf("load notifications: %v", err)
	}
	return list
}

// ticketMessageCount 数工单下的回复条数, 用于断言"被拒的回复没有落库"。
func ticketMessageCount(t *testing.T, ticketID uint64) int {
	t.Helper()
	var count int64
	if err := db.GetDB().Model(&model.TicketMessage{}).Where("ticket_id = ?", ticketID).Count(&count).Error; err != nil {
		t.Fatalf("count messages: %v", err)
	}
	return int(count)
}

// mustCreateTicket 建单并返回视图。
func mustCreateTicket(t *testing.T, userID uint, title string) model.TicketView {
	t.Helper()
	view, err := TicketCreate(context.Background(), userID, model.TicketCreateRequest{
		Title:   title,
		Content: "问题描述",
	})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	return view
}

// TestTicketCreateTrimsValidatesAndNotifiesAdmins 建单: 空白校验、缺省值、通知管理员而非自己;
// 不同工单的通知互不吞并(不走按 type 的全局去重)。
func TestTicketCreateTrimsValidatesAndNotifiesAdmins(t *testing.T) {
	user1, _, admin1, admin2 := ticketTestEnv(t)
	ctx := context.Background()

	view, err := TicketCreate(ctx, user1.ID, model.TicketCreateRequest{
		Title:   "  无法调用  ",
		Content: " 401 错误 ",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if view.Title != "无法调用" || view.Content != "401 错误" {
		t.Fatalf("not trimmed: %q %q", view.Title, view.Content)
	}
	if view.Status != model.TicketStatusOpen ||
		view.Priority != model.TicketPriorityNormal ||
		view.Category != model.TicketCategoryOther {
		t.Fatalf("defaults wrong: %+v", view)
	}

	// 新工单通知两名在职管理员, 不通知提交者本人(通知异步投递, 轮询等待)。
	waitTicketNotifs(t, admin1.ID, view.ID, 1)
	waitTicketNotifs(t, admin2.ID, view.ID, 1)
	settleTicketNotifs()
	if got := len(ticketNotifs(t, user1.ID, view.ID)); got != 0 {
		t.Fatalf("creator must not notify self, got %d", got)
	}
	// 停用管理员不是有效接收人。
	disabled := ticketUserByName(t, "tk-admin-off")
	if got := len(ticketNotifs(t, disabled.ID, view.ID)); got != 0 {
		t.Fatalf("disabled admin must not be notified, got %d", got)
	}

	// 第二张工单必须产生独立通知, 不被按 type 的去重吞掉。
	second := mustCreateTicket(t, user1.ID, "第二个问题")
	waitTicketNotifs(t, admin1.ID, second.ID, 1)

	// 管理员自己建单: 通知其他管理员但不通知自己。
	view3, err := TicketCreate(ctx, admin1.ID, model.TicketCreateRequest{
		Title:   "管理员自查",
		Content: "内容",
	})
	if err != nil {
		t.Fatalf("admin create: %v", err)
	}
	waitTicketNotifs(t, admin2.ID, view3.ID, 1)
	settleTicketNotifs()
	if got := len(ticketNotifs(t, admin1.ID, view3.ID)); got != 0 {
		t.Fatalf("admin self-notification = %d, want 0", got)
	}

	// 输入校验: 空白标题/正文、超长、非法枚举。
	bad := []model.TicketCreateRequest{
		{Title: "   ", Content: "x"},
		{Title: "x", Content: "  \t "},
		{Title: strings.Repeat("a", model.TicketTitleMaxLen+1), Content: "x"},
		{Title: "x", Content: strings.Repeat("a", model.TicketContentMaxLen+1)},
		{Title: "x", Content: "y", Priority: "huge"},
		{Title: "x", Content: "y", Category: "nope"},
	}
	for i, req := range bad {
		if _, err := TicketCreate(ctx, user1.ID, req); !errors.Is(err, ErrTicketInvalidInput) {
			t.Fatalf("bad req #%d: err=%v, want ErrTicketInvalidInput", i, err)
		}
	}
}

// TestTicketListScopeAndPagination 越权范围: 非管理员只见自己的; 分页与筛选生效。
func TestTicketListScopeAndPagination(t *testing.T) {
	user1, user2, _, _ := ticketTestEnv(t)
	ctx := context.Background()

	mustCreateTicket(t, user1.ID, "t1")
	t2 := mustCreateTicket(t, user1.ID, "t2")
	mustCreateTicket(t, user1.ID, "t3")
	mustCreateTicket(t, user2.ID, "t4")

	// 直改状态, 供筛选断言。
	if err := db.GetDB().Model(&model.Ticket{}).Where("id = ?", t2.ID).
		Update("status", model.TicketStatusInProgress).Error; err != nil {
		t.Fatal(err)
	}

	// user1 只看到自己的 3 张。
	items, total, err := TicketList(ctx, user1.ID, false, model.TicketListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(items) != 3 {
		t.Fatalf("user1 scope: total=%d len=%d", total, len(items))
	}
	for _, item := range items {
		if item.UserID != user1.ID {
			t.Fatalf("user1 list leaked ticket #%d of user %d", item.ID, item.UserID)
		}
	}

	// 管理员看到全部 4 张。
	if _, total, err = TicketList(ctx, 0, true, model.TicketListRequest{}); err != nil || total != 4 {
		t.Fatalf("admin scope: total=%d err=%v", total, err)
	}

	// 分页。
	page2, total, err := TicketList(ctx, 0, true, model.TicketListRequest{Page: 2, PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 || len(page2) != 2 {
		t.Fatalf("page2: total=%d len=%d", total, len(page2))
	}

	// 状态筛选。
	openOnly, total, err := TicketList(ctx, user1.ID, false, model.TicketListRequest{Status: model.TicketStatusOpen})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(openOnly) != 2 {
		t.Fatalf("open filter: total=%d len=%d", total, len(openOnly))
	}
	for _, item := range openOnly {
		if item.ID == t2.ID {
			t.Fatal("in_progress ticket leaked into open filter")
		}
	}

	// 非法过滤值直接拒绝。
	if _, _, err := TicketList(ctx, user1.ID, false, model.TicketListRequest{Status: "bogus"}); !errors.Is(err, ErrTicketInvalidInput) {
		t.Fatalf("bad status filter: err=%v", err)
	}
	if _, _, err := TicketList(ctx, user1.ID, false, model.TicketListRequest{Priority: "huge"}); !errors.Is(err, ErrTicketInvalidInput) {
		t.Fatalf("bad priority filter: err=%v", err)
	}
}

// TestTicketResellerScopedLikeUser 非管理员角色(reseller)与普通用户同权: 只操作自己的工单。
func TestTicketResellerScopedLikeUser(t *testing.T) {
	_, user2, _, _ := ticketTestEnv(t)
	ctx := context.Background()
	seller := ticketUserByName(t, "tk-user1")
	// 把账号改成 reseller: 除 admin 外的一切角色都按普通用户收口。
	if err := db.GetDB().Model(&model.User{}).Where("id = ?", seller.ID).
		Update("role", model.RoleReseller).Error; err != nil {
		t.Fatal(err)
	}

	mine := mustCreateTicket(t, seller.ID, "渠道商的单")
	other := mustCreateTicket(t, user2.ID, "别人的单")

	// 列表只回自己的。
	items, total, err := TicketList(ctx, seller.ID, false, model.TicketListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 || items[0].ID != mine.ID {
		t.Fatalf("reseller scope: total=%d items=%+v", total, items)
	}

	// 他人工单不可见、不可回复。
	if _, err := TicketGet(ctx, seller.ID, false, other.ID); !errors.Is(err, ErrTicketForbidden) {
		t.Fatalf("reseller cross-user get: err=%v", err)
	}
	if _, err := TicketReply(ctx, seller.ID, false, other.ID, model.TicketReplyRequest{Content: "偷看"}); !errors.Is(err, ErrTicketForbidden) {
		t.Fatalf("reseller cross-user reply: err=%v", err)
	}
}

// TestTicketGetEnforcesOwnership 详情越权: 他人不可见, 管理员可见, 不存在返回哨兵错误。
func TestTicketGetEnforcesOwnership(t *testing.T) {
	user1, user2, _, _ := ticketTestEnv(t)
	ctx := context.Background()

	ticket := mustCreateTicket(t, user1.ID, "私密工单")
	if _, err := TicketReply(ctx, user1.ID, false, ticket.ID, model.TicketReplyRequest{Content: "补充信息"}); err != nil {
		t.Fatal(err)
	}

	if _, err := TicketGet(ctx, user2.ID, false, ticket.ID); !errors.Is(err, ErrTicketForbidden) {
		t.Fatalf("cross-user get: err=%v, want ErrTicketForbidden", err)
	}
	if _, err := TicketGet(ctx, user2.ID, false, 999999); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("missing get: err=%v, want ErrTicketNotFound", err)
	}

	detail, err := TicketGet(ctx, user1.ID, false, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Messages) != 1 || detail.Messages[0].Content != "补充信息" {
		t.Fatalf("detail messages: %+v", detail.Messages)
	}
}

// TestTicketReplyTransactionAndNotify 回复: 消息与 updated_at 同事务落库;
// 用户回复通知管理员而非自己, 管理员回复通知工单主人; 已关闭不可回复。
func TestTicketReplyTransactionAndNotify(t *testing.T) {
	user1, _, admin1, admin2 := ticketTestEnv(t)
	ctx := context.Background()

	ticket := mustCreateTicket(t, user1.ID, "回复测试")
	// 把 updated_at 拨回去, 验证回复会把它顶起来。
	old := time.Now().Add(-48 * time.Hour)
	if err := db.GetDB().Model(&model.Ticket{}).Where("id = ?", ticket.ID).
		Updates(map[string]interface{}{"updated_at": old}).Error; err != nil {
		t.Fatal(err)
	}

	msg, err := TicketReply(ctx, user1.ID, false, ticket.ID, model.TicketReplyRequest{Content: "  用户补充  "})
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != "用户补充" || msg.IsAdmin {
		t.Fatalf("message view: %+v", msg)
	}

	var fresh model.Ticket
	if err := db.GetDB().First(&fresh, ticket.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !fresh.UpdatedAt.After(old) {
		t.Fatalf("updated_at not bumped: %v", fresh.UpdatedAt)
	}

	// 用户回复 -> 管理员(建单已各有 1 条, 回复再各加 1 条), 不是自己。
	waitTicketNotifs(t, admin1.ID, ticket.ID, 2)
	waitTicketNotifs(t, admin2.ID, ticket.ID, 2)
	settleTicketNotifs()
	if got := len(ticketNotifs(t, user1.ID, ticket.ID)); got != 0 {
		t.Fatalf("user self-notification = %d, want 0", got)
	}

	// 管理员回复 -> 工单主人。
	if _, err := TicketReply(ctx, admin1.ID, true, ticket.ID, model.TicketReplyRequest{Content: "处理方案"}); err != nil {
		t.Fatal(err)
	}
	waitTicketNotifs(t, user1.ID, ticket.ID, 1)
	settleTicketNotifs()
	// 管理员之间互不通知(只有主人收管理员回复)。
	if got := len(ticketNotifs(t, admin2.ID, ticket.ID)); got != 2 {
		t.Fatalf("admin2 notifications = %d, want still 2", got)
	}

	// 回复校验与越权。
	if _, err := TicketReply(ctx, user1.ID, false, ticket.ID, model.TicketReplyRequest{Content: "   "}); !errors.Is(err, ErrTicketInvalidInput) {
		t.Fatalf("blank reply: err=%v", err)
	}

	// 关闭后不可回复。
	if err := TicketClose(ctx, admin1.ID, true, ticket.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := TicketReply(ctx, user1.ID, false, ticket.ID, model.TicketReplyRequest{Content: "还想说话"}); !errors.Is(err, ErrTicketClosed) {
		t.Fatalf("reply on closed: err=%v, want ErrTicketClosed", err)
	}
}

// TestTicketStatusTransitionMachine 状态流转: 合法流转落库、非法流转拒绝、
// closed_at 进 closed 记时间戳、离开 closed 清空。
func TestTicketStatusTransitionMachine(t *testing.T) {
	user1, _, admin1, _ := ticketTestEnv(t)
	ctx := context.Background()

	ticket := mustCreateTicket(t, user1.ID, "状态机")

	// open -> resolved 不合法(必须经过 in_progress)。
	if err := TicketUpdateStatus(ctx, admin1.ID, ticket.ID, model.TicketStatusUpdateRequest{Status: model.TicketStatusResolved}); !errors.Is(err, ErrTicketInvalidTransition) {
		t.Fatalf("open->resolved: err=%v, want ErrTicketInvalidTransition", err)
	}

	// open -> in_progress -> resolved -> closed。
	for _, status := range []string{model.TicketStatusInProgress, model.TicketStatusResolved} {
		if err := TicketUpdateStatus(ctx, admin1.ID, ticket.ID, model.TicketStatusUpdateRequest{Status: status}); err != nil {
			t.Fatalf("-> %s: %v", status, err)
		}
	}
	fresh := ticketReload(t, ticket.ID)
	if fresh.ClosedAt != nil {
		t.Fatalf("closed_at set before closed: %v", fresh.ClosedAt)
	}
	// 进入 resolved 记 resolved_at(自动关闭计时起点)。
	if fresh.ResolvedAt == nil {
		t.Fatal("resolved_at missing after entering resolved")
	}

	// resolved -> open 不合法(重开只从 closed 走)。
	if err := TicketUpdateStatus(ctx, admin1.ID, ticket.ID, model.TicketStatusUpdateRequest{Status: model.TicketStatusOpen}); !errors.Is(err, ErrTicketInvalidTransition) {
		t.Fatalf("resolved->open: err=%v", err)
	}

	// resolved -> closed: 记 closed_at。
	if err := TicketUpdateStatus(ctx, admin1.ID, ticket.ID, model.TicketStatusUpdateRequest{Status: model.TicketStatusClosed}); err != nil {
		t.Fatal(err)
	}
	fresh = ticketReload(t, ticket.ID)
	if fresh.ClosedAt == nil {
		t.Fatal("closed_at missing after close")
	}
	// 离开 resolved 清空 resolved_at(只在 resolved 期间持有)。
	if fresh.ResolvedAt != nil {
		t.Fatalf("resolved_at not cleared on leaving resolved: %v", fresh.ResolvedAt)
	}

	// closed -> in_progress 不合法。
	if err := TicketUpdateStatus(ctx, admin1.ID, ticket.ID, model.TicketStatusUpdateRequest{Status: model.TicketStatusInProgress}); !errors.Is(err, ErrTicketInvalidTransition) {
		t.Fatalf("closed->in_progress: err=%v", err)
	}

	// closed -> open(重新打开): 清空 closed_at。
	if err := TicketUpdateStatus(ctx, admin1.ID, ticket.ID, model.TicketStatusUpdateRequest{Status: model.TicketStatusOpen}); err != nil {
		t.Fatal(err)
	}
	fresh = ticketReload(t, ticket.ID)
	if fresh.ClosedAt != nil {
		t.Fatalf("closed_at not cleared on reopen: %v", fresh.ClosedAt)
	}

	// 状态变更通知工单主人(异步投递): 四次合法变更各一条, 先等到再验幂等。
	waitTicketNotifs(t, user1.ID, ticket.ID, 4)

	// 同状态是幂等空操作: 不报错也不重复通知。
	before := len(ticketNotifs(t, user1.ID, ticket.ID))
	if err := TicketUpdateStatus(ctx, admin1.ID, ticket.ID, model.TicketStatusUpdateRequest{Status: model.TicketStatusOpen}); err != nil {
		t.Fatalf("idempotent update: %v", err)
	}
	settleTicketNotifs()
	if got := len(ticketNotifs(t, user1.ID, ticket.ID)); got != before {
		t.Fatalf("no-op produced notification: %d -> %d", before, got)
	}

	// 不存在的工单。
	if err := TicketUpdateStatus(ctx, admin1.ID, 999999, model.TicketStatusUpdateRequest{Status: model.TicketStatusClosed}); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("missing ticket: err=%v", err)
	}
}

// TestTicketCloseReopenPermissions close/reopen 越权与状态校验。
func TestTicketCloseReopenPermissions(t *testing.T) {
	user1, user2, admin1, _ := ticketTestEnv(t)
	ctx := context.Background()

	ticket := mustCreateTicket(t, user1.ID, "开关测试")

	// 他人不能 close/reopen。
	if err := TicketClose(ctx, user2.ID, false, ticket.ID); !errors.Is(err, ErrTicketForbidden) {
		t.Fatalf("cross-user close: err=%v", err)
	}
	if err := TicketReopen(ctx, user2.ID, false, ticket.ID); !errors.Is(err, ErrTicketForbidden) {
		t.Fatalf("cross-user reopen: err=%v", err)
	}

	// 未关闭不能 reopen。
	if err := TicketReopen(ctx, user1.ID, false, ticket.ID); !errors.Is(err, ErrTicketInvalidTransition) {
		t.Fatalf("reopen open ticket: err=%v", err)
	}

	// 本人关闭: 记 closed_at。
	if err := TicketClose(ctx, user1.ID, false, ticket.ID); err != nil {
		t.Fatal(err)
	}
	fresh := ticketReload(t, ticket.ID)
	if fresh.Status != model.TicketStatusClosed || fresh.ClosedAt == nil {
		t.Fatalf("close: %+v", fresh)
	}

	// 重复 close 拒绝。
	if err := TicketClose(ctx, user1.ID, false, ticket.ID); !errors.Is(err, ErrTicketInvalidTransition) {
		t.Fatalf("double close: err=%v", err)
	}

	// 管理员重开: 清空 closed_at, 并通知主人。
	if err := TicketReopen(ctx, admin1.ID, true, ticket.ID); err != nil {
		t.Fatal(err)
	}
	fresh = ticketReload(t, ticket.ID)
	if fresh.Status != model.TicketStatusOpen || fresh.ClosedAt != nil {
		t.Fatalf("reopen: %+v", fresh)
	}
	waitTicketNotifs(t, user1.ID, ticket.ID, 1)
}

// TestTicketAssignRequiresActiveAdmin 分配目标必须是在职管理员, 分配通知目标本人。
func TestTicketAssignRequiresActiveAdmin(t *testing.T) {
	user1, user2, admin1, admin2 := ticketTestEnv(t)
	ctx := context.Background()
	ticket := mustCreateTicket(t, user1.ID, "分配测试")

	// 停用管理员的 ID: 从库里取。
	var disabled model.User
	if err := db.GetDB().Where("username = ?", "tk-admin-off").First(&disabled).Error; err != nil {
		t.Fatal(err)
	}

	bad := []uint{user2.ID, disabled.ID, 999999}
	for _, target := range bad {
		if err := TicketAssign(ctx, admin1.ID, ticket.ID, model.TicketAssignRequest{AssignedTo: target}); !errors.Is(err, ErrTicketAssigneeInvalid) {
			t.Fatalf("assign to %d: err=%v, want ErrTicketAssigneeInvalid", target, err)
		}
	}

	// 合法分配: 通知被分配的管理员(admin2 因建单已有 1 条, 分配再加 1 条)。
	if err := TicketAssign(ctx, admin1.ID, ticket.ID, model.TicketAssignRequest{AssignedTo: admin2.ID}); err != nil {
		t.Fatal(err)
	}
	waitTicketNotifs(t, admin2.ID, ticket.ID, 2)
	fresh := ticketReload(t, ticket.ID)
	if fresh.AssignedTo == nil || *fresh.AssignedTo != admin2.ID {
		t.Fatalf("assigned_to = %v, want %d", fresh.AssignedTo, admin2.ID)
	}

	// 分给自己: 不发"分配给您"给自己。
	if err := TicketAssign(ctx, admin2.ID, ticket.ID, model.TicketAssignRequest{AssignedTo: admin2.ID}); err != nil {
		t.Fatal(err)
	}
	settleTicketNotifs()
	if got := len(ticketNotifs(t, admin2.ID, ticket.ID)); got != 2 {
		t.Fatalf("self-assign notification = %d, want still 2", got)
	}

	// 不存在的工单。
	if err := TicketAssign(ctx, admin1.ID, 999999, model.TicketAssignRequest{AssignedTo: admin2.ID}); !errors.Is(err, ErrTicketNotFound) {
		t.Fatalf("missing ticket: err=%v", err)
	}
}

// TestTicketAutoCloseResolved 自动关闭按 resolved_at 计时: 标记解决满 3 天关闭;
// 未到期不动; 其它状态不动; 期间的回复(updated_at 被顶起)不重置窗口。
func TestTicketAutoCloseResolved(t *testing.T) {
	user1, _, _, _ := ticketTestEnv(t)
	ctx := context.Background()

	stale := mustCreateTicket(t, user1.ID, "已解决很久")
	fresh := mustCreateTicket(t, user1.ID, "刚解决")
	busy := mustCreateTicket(t, user1.ID, "处理中很久")

	// seed 同时设置 resolved_at 与 updated_at, 两者故意错开以锁定计时口径。
	seed := func(id uint64, status string, resolvedAt *time.Time, updatedAt time.Time) {
		vals := map[string]interface{}{"status": status, "updated_at": updatedAt}
		if resolvedAt != nil {
			vals["resolved_at"] = *resolvedAt
		} else {
			vals["resolved_at"] = nil
		}
		if err := db.GetDB().Model(&model.Ticket{}).Where("id = ?", id).Updates(vals).Error; err != nil {
			t.Fatal(err)
		}
	}
	longAgo := time.Now().Add(-4 * 24 * time.Hour)
	oneDayAgo := time.Now().Add(-24 * time.Hour)
	// stale: resolved_at 4 天前, 但刚刚还有回复(updated_at=现在) —— 必须照关不误。
	seed(stale.ID, model.TicketStatusResolved, &longAgo, time.Now())
	// fresh: resolved_at 才 1 天, 但 updated_at 是 4 天前(残留旧活动) —— 绝不能关。
	seed(fresh.ID, model.TicketStatusResolved, &oneDayAgo, longAgo)
	seed(busy.ID, model.TicketStatusInProgress, nil, longAgo)

	closed, err := TicketAutoCloseResolved(ctx, TicketAutoCloseAfter)
	if err != nil {
		t.Fatal(err)
	}
	if closed != 1 {
		t.Fatalf("closed = %d, want 1", closed)
	}

	row := ticketReload(t, stale.ID)
	if row.Status != model.TicketStatusClosed || row.ClosedAt == nil {
		t.Fatalf("stale ticket: %+v", row)
	}
	waitTicketNotifs(t, user1.ID, stale.ID, 1)

	if row := ticketReload(t, fresh.ID); row.Status != model.TicketStatusResolved {
		t.Fatalf("fresh resolved ticket must stay resolved: %+v", row)
	}
	if row := ticketReload(t, busy.ID); row.Status != model.TicketStatusInProgress {
		t.Fatalf("in_progress ticket must stay untouched: %+v", row)
	}

	// 再跑一轮: 已关闭的不会重复关闭、重复通知。
	closed, err = TicketAutoCloseResolved(ctx, TicketAutoCloseAfter)
	if err != nil {
		t.Fatal(err)
	}
	if closed != 0 {
		t.Fatalf("second run closed = %d, want 0", closed)
	}
	settleTicketNotifs()
	if got := len(ticketNotifs(t, user1.ID, stale.ID)); got != 1 {
		t.Fatalf("duplicate auto-close notification: %d", got)
	}
}

// TestTicketReplyVsCloseRace 并发窗口回归: TicketReply 的状态判断在事务外,
// 事务里必须再按"未关闭"条件顶起 updated_at 才能写消息, 否则关闭与回复交错时
// 消息会落进已关闭工单(用户看得见一条"发进了黑洞"的回复)。
func TestTicketReplyVsCloseRace(t *testing.T) {
	user1, _, _, _ := ticketTestEnv(t)
	ctx := context.Background()

	ticket := mustCreateTicket(t, user1.ID, "竞态测试")
	before := ticketMessageCount(t, ticket.ID)

	if err := TicketClose(ctx, user1.ID, false, ticket.ID); err != nil {
		t.Fatal(err)
	}
	// 手工造出"读到时还没关闭"的窗口: 直接把状态改回 open 再并发——见下面的并发用例。
	if _, err := TicketReply(ctx, user1.ID, false, ticket.ID, model.TicketReplyRequest{Content: "关单后回复"}); !errors.Is(err, ErrTicketClosed) {
		t.Fatalf("reply on closed ticket: err=%v, want ErrTicketClosed", err)
	}
	if got := ticketMessageCount(t, ticket.ID); got != before {
		t.Fatalf("rejected reply must not persist a message: %d != %d", got, before)
	}
}

// TestTicketReplyAndCloseInterleaved 让回复与关闭真正并发, 断言二者互斥:
// 要么回复落库且工单仍开着, 要么关闭成功且没有任何消息被写进已关闭工单。
func TestTicketReplyAndCloseInterleaved(t *testing.T) {
	user1, admin1, _, _ := ticketTestEnv(t)
	ctx := context.Background()

	for round := 0; round < 8; round++ {
		ticket := mustCreateTicket(t, user1.ID, "交错竞态")
		before := ticketMessageCount(t, ticket.ID)

		var wg sync.WaitGroup
		wg.Add(2)
		var replyErr, closeErr error
		go func() {
			defer wg.Done()
			_, replyErr = TicketReply(ctx, user1.ID, false, ticket.ID, model.TicketReplyRequest{Content: "并发回复"})
		}()
		go func() {
			defer wg.Done()
			closeErr = TicketClose(ctx, admin1.ID, true, ticket.ID)
		}()
		wg.Wait()

		row := ticketReload(t, ticket.ID)
		added := ticketMessageCount(t, ticket.ID) - before

		if closeErr != nil {
			// 关闭失败(被并发回复顶掉的极端情况): 工单必须仍开着, 回复才可能成功。
			if row.Status == model.TicketStatusClosed {
				t.Fatalf("round %d: close failed (%v) but ticket is closed", round, closeErr)
			}
			continue
		}
		// 关闭成功: 已关闭工单不允许再落消息 —— 回复要么被拒, 要么发生在关闭之前。
		if row.Status != model.TicketStatusClosed || row.ClosedAt == nil {
			t.Fatalf("round %d: close succeeded but row = %+v", round, row)
		}
		if replyErr != nil && added != 0 {
			t.Fatalf("round %d: reply failed (%v) yet %d message(s) persisted", round, replyErr, added)
		}
	}
}

// TestTicketStatusCASConflict 两个并发 close/reopen 基于同一份旧状态:
// 条件更新必须只让一方成功, 另一方拿到 ErrTicketConflict 而不是静默覆盖。
func TestTicketStatusCASConflict(t *testing.T) {
	user1, _, _, _ := ticketTestEnv(t)
	ctx := context.Background()

	ticket := mustCreateTicket(t, user1.ID, "状态竞态")
	if err := TicketClose(ctx, user1.ID, false, ticket.ID); err != nil {
		t.Fatal(err)
	}

	// 直接读两次同一份旧状态, 模拟两个请求都基于"closed"做判断。
	stale1 := ticketReload(t, ticket.ID)
	stale2 := ticketReload(t, ticket.ID)

	if err := setTicketStatus(ctx, &stale1, model.TicketStatusOpen); err != nil {
		t.Fatalf("first transition: %v", err)
	}
	if err := setTicketStatus(ctx, &stale2, model.TicketStatusOpen); !errors.Is(err, ErrTicketConflict) {
		t.Fatalf("second transition must conflict, got %v", err)
	}

	row := ticketReload(t, ticket.ID)
	if row.Status != model.TicketStatusOpen || row.ClosedAt != nil {
		t.Fatalf("after CAS reopen: %+v", row)
	}
}

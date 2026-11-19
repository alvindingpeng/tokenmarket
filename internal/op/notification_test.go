package op

import (
	"context"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// notificationTestSetup 初始化独立的内存 SQLite, 避免测试之间互相污染。
func notificationTestSetup(t *testing.T) {
	t.Helper()
	opTestDBSetup(t, "notification.db")
}

func TestNotificationCreateAndList(t *testing.T) {
	notificationTestSetup(t)
	ctx := context.Background()
	const userID = uint(42)

	if err := NotificationCreate(ctx, userID, model.NotificationTypeSystem, "系统公告", "内容", "", 0); err != nil {
		t.Fatal(err)
	}

	items, total, err := NotificationList(ctx, userID, model.NotificationListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("total=%d len=%d, want 1/1", total, len(items))
	}
	if items[0].Title != "系统公告" || items[0].Status != model.NotificationStatusUnread {
		t.Fatalf("unexpected notification: %+v", items[0])
	}
}

// 用户之间必须互相看不见对方的通知: 越权读取是最容易被忽略的一类缺陷。
func TestRateLimitScopeLabel(t *testing.T) {
	if got := rateLimitScopeLabel("user"); got != "账户级" {
		t.Fatalf("用户限流标签=%q, want 账户级", got)
	}
	if got := rateLimitScopeLabel("api_key"); got != "API Key 级" {
		t.Fatalf("API Key限流标签=%q, want API Key 级", got)
	}
}

func TestNotificationListIsScopedToUser(t *testing.T) {
	notificationTestSetup(t)
	ctx := context.Background()

	if err := NotificationCreate(ctx, 1, model.NotificationTypeSystem, "给用户1", "", "", 0); err != nil {
		t.Fatal(err)
	}
	if err := NotificationCreate(ctx, 2, model.NotificationTypeSystem, "给用户2", "", "", 0); err != nil {
		t.Fatal(err)
	}

	items, total, err := NotificationList(ctx, 1, model.NotificationListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || items[0].Title != "给用户1" {
		t.Fatalf("user 1 sees %d items (first=%q), want only own", total, items[0].Title)
	}
}

func TestNotificationMarkRead(t *testing.T) {
	notificationTestSetup(t)
	ctx := context.Background()
	const userID = uint(7)

	if err := NotificationCreate(ctx, userID, model.NotificationTypeRecharge, "充值", "", "", 0); err != nil {
		t.Fatal(err)
	}

	count, err := NotificationUnreadCount(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("unread=%d, want 1", count)
	}

	items, _, err := NotificationList(ctx, userID, model.NotificationListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if err := NotificationMarkRead(ctx, userID, []uint64{items[0].ID}); err != nil {
		t.Fatal(err)
	}

	count, err = NotificationUnreadCount(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("unread=%d after mark read, want 0", count)
	}

	// 标记他人通知不应生效。
	if err := NotificationCreate(ctx, 99, model.NotificationTypeRecharge, "别人的", "", "", 0); err != nil {
		t.Fatal(err)
	}
	other, _, err := NotificationList(ctx, 99, model.NotificationListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if err := NotificationMarkRead(ctx, userID, []uint64{other[0].ID}); err != nil {
		t.Fatal(err)
	}
	otherCount, err := NotificationUnreadCount(ctx, 99)
	if err != nil {
		t.Fatal(err)
	}
	if otherCount != 1 {
		t.Fatalf("other user's unread=%d, want 1 (must not be affected)", otherCount)
	}
}

func TestNotificationMarkAllRead(t *testing.T) {
	notificationTestSetup(t)
	ctx := context.Background()
	const userID = uint(11)

	for i := 0; i < 3; i++ {
		if err := NotificationCreate(ctx, userID, model.NotificationTypeSystem, "通知", "", "", 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := NotificationMarkAllRead(ctx, userID); err != nil {
		t.Fatal(err)
	}
	count, err := NotificationUnreadCount(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("unread=%d after mark all read, want 0", count)
	}
}

// 去重窗口内同类未读通知只应写入一条, 否则余额不足这类高频事件会刷屏。
func TestNotificationCreateDedup(t *testing.T) {
	notificationTestSetup(t)
	ctx := context.Background()
	const userID = uint(5)

	created, err := NotificationCreateDedup(ctx, userID, model.NotificationTypeBalanceLow, "余额不足", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("first notification should be created")
	}

	created, err = NotificationCreateDedup(ctx, userID, model.NotificationTypeBalanceLow, "余额不足", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("duplicate notification within window should be skipped")
	}

	_, total, err := NotificationList(ctx, userID, model.NotificationListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("total=%d, want 1", total)
	}

	// 不同类型不互相去重。
	created, err = NotificationCreateDedup(ctx, userID, model.NotificationTypeRateLimitHit, "限流", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("different type should not be deduped")
	}

	// 同一通知类型的不同限流实体使用不同去重键。
	created, err = createNotificationDedupKey(ctx, userID, model.NotificationTypeRateLimitHit, "限流", "第二把Key", "api_key:99", 99)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("different scope key should not be deduped")
	}
	created, err = createNotificationDedupKey(ctx, userID, model.NotificationTypeRateLimitHit, "限流", "再次命中", "api_key:99", 99)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("same scope key should be deduped")
	}
}

// 限流触顶通知是异步投递的: 断言它真的落库、按限流实体各自去重,
// 且对不适用的 scope / 匿名调用直接跳过(不为不存在的人造通知)。
func TestNotifyRateLimitHitAsyncDeliversPerScope(t *testing.T) {
	notificationTestSetup(t)
	const userID = uint(77)

	// 异步投递, 用有界轮询等落库, 不引入固定长 sleep。
	waitFor := func(pred func() bool) bool {
		deadline := time.Now().Add(2 * time.Second)
		for {
			if pred() {
				return true
			}
			if time.Now().After(deadline) {
				return false
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	countByRelated := func(relatedID uint64) int64 {
		var count int64
		if err := db.GetDB().Model(&model.Notification{}).
			Where("user_id = ? AND type = ? AND related_id = ?", userID, model.NotificationTypeRateLimitHit, relatedID).
			Count(&count).Error; err != nil {
			t.Fatalf("count notifications: %v", err)
		}
		return count
	}

	NotifyRateLimitHitAsync(userID, "gpt-4o", "api_key", 12)
	if !waitFor(func() bool { return countByRelated(12) == 1 }) {
		t.Fatalf("expected one api_key rate limit notification for related_id 12, got %d", countByRelated(12))
	}

	// 同一实体在去重窗口内重复触顶不叠加。
	NotifyRateLimitHitAsync(userID, "gpt-4o", "api_key", 12)
	if !waitFor(func() bool { return countByRelated(12) == 1 }) {
		t.Fatalf("duplicate rate limit notification should be deduped, got %d", countByRelated(12))
	}

	// 账户级限流不按模型拆通知: 归属 id 固定为 0。
	NotifyRateLimitHitAsync(userID, "gpt-4o", "user", uint64(userID))
	if !waitFor(func() bool { return countByRelated(0) == 1 }) {
		t.Fatalf("expected one account-level notification with related_id 0, got %d", countByRelated(0))
	}

	// 未定义的 scope 与匿名调用都不该产生通知。
	NotifyRateLimitHitAsync(userID, "gpt-4o", "channel", 5)
	NotifyRateLimitHitAsync(0, "gpt-4o", "api_key", 12)
	var total int64
	if err := db.GetDB().Model(&model.Notification{}).Where("user_id = ?", userID).Count(&total).Error; err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("unknown scope must not notify; total=%d want 2", total)
	}
}

// 用户读掉已有通知后, 窗口内应能再次提醒: 去重只挡"未读且同类"。
func TestNotificationDedupResumesAfterRead(t *testing.T) {
	notificationTestSetup(t)
	ctx := context.Background()
	const userID = uint(6)

	if _, err := NotificationCreateDedup(ctx, userID, model.NotificationTypeBalanceLow, "余额不足", "", "", 0); err != nil {
		t.Fatal(err)
	}
	if err := NotificationMarkAllRead(ctx, userID); err != nil {
		t.Fatal(err)
	}

	created, err := NotificationCreateDedup(ctx, userID, model.NotificationTypeBalanceLow, "余额不足", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("notification should be created again after previous one was read")
	}
}

func TestNotificationDelete(t *testing.T) {
	notificationTestSetup(t)
	ctx := context.Background()
	const userID = uint(8)

	if err := NotificationCreate(ctx, userID, model.NotificationTypeSystem, "待删除", "", "", 0); err != nil {
		t.Fatal(err)
	}
	items, _, err := NotificationList(ctx, userID, model.NotificationListRequest{})
	if err != nil {
		t.Fatal(err)
	}

	if err := NotificationDelete(ctx, userID, items[0].ID); err != nil {
		t.Fatal(err)
	}
	_, total, err := NotificationList(ctx, userID, model.NotificationListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Fatalf("total=%d after delete, want 0", total)
	}

	// 删除不存在的通知应报错, 而不是静默成功。
	if err := NotificationDelete(ctx, userID, 99999); err == nil {
		t.Fatal("delete of missing notification should return error")
	}
}

// 列表筛选: 按状态与类型过滤。
func TestNotificationListFilters(t *testing.T) {
	notificationTestSetup(t)
	ctx := context.Background()
	const userID = uint(9)

	if err := NotificationCreate(ctx, userID, model.NotificationTypeSystem, "系统", "", "", 0); err != nil {
		t.Fatal(err)
	}
	if err := NotificationCreate(ctx, userID, model.NotificationTypeRecharge, "充值", "", "", 0); err != nil {
		t.Fatal(err)
	}

	_, total, err := NotificationList(ctx, userID, model.NotificationListRequest{Type: model.NotificationTypeRecharge})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("filtered by type total=%d, want 1", total)
	}

	if err := NotificationMarkAllRead(ctx, userID); err != nil {
		t.Fatal(err)
	}
	_, total, err = NotificationList(ctx, userID, model.NotificationListRequest{Status: model.NotificationStatusUnread})
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Fatalf("filtered by unread status total=%d, want 0", total)
	}

	_, total, err = NotificationList(ctx, userID, model.NotificationListRequest{Status: model.NotificationStatusRead})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("filtered by read status total=%d, want 2", total)
	}
}

// 分页: page_size 生效且不越界。
func TestNotificationListPagination(t *testing.T) {
	notificationTestSetup(t)
	ctx := context.Background()
	const userID = uint(10)

	for i := 0; i < 5; i++ {
		if err := NotificationCreate(ctx, userID, model.NotificationTypeSystem, "通知", "", "", 0); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond) // 保证 created_at 有先后差异
	}

	items, total, err := NotificationList(ctx, userID, model.NotificationListRequest{Page: 1, PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || len(items) != 2 {
		t.Fatalf("page1 total=%d len=%d, want 5/2", total, len(items))
	}

	items, _, err = NotificationList(ctx, userID, model.NotificationListRequest{Page: 3, PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("page3 len=%d, want 1", len(items))
	}
}

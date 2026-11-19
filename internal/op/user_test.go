package op

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// userDeleteTestSetup 隔离测试库 + 清空进程级用户缓存:
// 缓存是单例, 不清会把上个测试库的同 ID 用户副本带进来, UserDelete 的前置校验就查错了对象。
func userDeleteTestSetup(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "user-delete.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	userCache.Clear()
	userNameIndex.Clear()
}

func userDeleteTestCreateUser(t *testing.T, username string) model.User {
	t.Helper()
	user, err := UserCreate(username, "", "password123", model.RoleUser, model.StatusActive)
	if err != nil {
		t.Fatalf("create user %s: %v", username, err)
	}
	return user
}

func userDeleteTestCreateWebhook(t *testing.T, userID uint) model.Webhook {
	t.Helper()
	hook := model.Webhook{
		UserID:     userID,
		URL:        "https://hooks.example.com/x",
		Events:     "[\"system\"]",
		Enabled:    true,
		RetryCount: 2,
		Version:    1,
		LastStatus: model.WebhookLastStatusNone,
	}
	if err := db.GetDB().Create(&hook).Error; err != nil {
		t.Fatalf("create webhook for user %d: %v", userID, err)
	}
	return hook
}

func userDeleteTestCreateDelivery(t *testing.T, hook model.Webhook, deliveryID string) model.WebhookDelivery {
	t.Helper()
	delivery := model.WebhookDelivery{
		DeliveryID:    deliveryID,
		WebhookID:     hook.ID,
		UserID:        hook.UserID,
		HookVersion:   hook.Version,
		Event:         string(model.NotificationTypeSystem),
		Payload:       "{\"id\":\"" + deliveryID + "\"}",
		Status:        model.WebhookDeliveryStatusPending,
		NextAttemptAt: time.Now(),
	}
	if err := db.GetDB().Create(&delivery).Error; err != nil {
		t.Fatalf("create delivery %s: %v", deliveryID, err)
	}
	return delivery
}

func userDeleteTestCount(t *testing.T, table any, query string, args ...any) int64 {
	t.Helper()
	var count int64
	if err := db.GetDB().Model(table).Where(query, args...).Count(&count).Error; err != nil {
		t.Fatalf("count %T: %v", table, err)
	}
	return count
}

// 注销级联: 同一事务里先删投递行、再删订阅行、最后删用户; 其他用户的数据一律不受影响。
func TestUserDeleteCascadesWebhookRows(t *testing.T) {
	userDeleteTestSetup(t)
	victim := userDeleteTestCreateUser(t, "victim")
	other := userDeleteTestCreateUser(t, "other")
	victimHook := userDeleteTestCreateWebhook(t, victim.ID)
	otherHook := userDeleteTestCreateWebhook(t, other.ID)
	userDeleteTestCreateDelivery(t, victimHook, "victim-delivery-1")
	userDeleteTestCreateDelivery(t, victimHook, "victim-delivery-2")
	userDeleteTestCreateDelivery(t, otherHook, "other-delivery-1")

	if err := UserDelete(victim.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	if got := userDeleteTestCount(t, &model.User{}, "id = ?", victim.ID); got != 0 {
		t.Fatalf("user row = %d, want 0", got)
	}
	if got := userDeleteTestCount(t, &model.Webhook{}, "user_id = ?", victim.ID); got != 0 {
		t.Fatalf("webhook rows = %d, want 0", got)
	}
	if got := userDeleteTestCount(t, &model.WebhookDelivery{}, "user_id = ?", victim.ID); got != 0 {
		t.Fatalf("webhook delivery rows = %d, want 0", got)
	}

	// 其他用户的行必须原样保留(级联只按 user_id 命中, 不能越界)。
	if got := userDeleteTestCount(t, &model.User{}, "id = ?", other.ID); got != 1 {
		t.Fatalf("other user row = %d, want 1", got)
	}
	if got := userDeleteTestCount(t, &model.Webhook{}, "user_id = ?", other.ID); got != 1 {
		t.Fatalf("other user webhooks = %d, want 1", got)
	}
	if got := userDeleteTestCount(t, &model.WebhookDelivery{}, "webhook_id = ?", otherHook.ID); got != 1 {
		t.Fatalf("other user deliveries = %d, want 1", got)
	}

	// 缓存与用户名索引同步失效, 否则已注销账号还能被鉴权读到。
	if _, ok := userCache.Get(int(victim.ID)); ok {
		t.Fatal("deleted user still in cache")
	}
	if _, ok := userNameIndex.Get("victim"); ok {
		t.Fatal("deleted username still in index")
	}
	if _, ok := userCache.Get(int(other.ID)); !ok {
		t.Fatal("other user must stay in cache")
	}
}

// 老库/测试 fixture 缺 webhook 表时跳过级联, 不把"表不存在"当删除失败。
func TestUserDeleteSkipsMissingWebhookTables(t *testing.T) {
	userDeleteTestSetup(t)
	user := userDeleteTestCreateUser(t, "no-webhook-tables")
	if err := db.GetDB().Migrator().DropTable(&model.WebhookDelivery{}); err != nil {
		t.Fatalf("drop webhook_deliveries: %v", err)
	}
	if err := db.GetDB().Migrator().DropTable(&model.Webhook{}); err != nil {
		t.Fatalf("drop webhooks: %v", err)
	}
	if err := UserDelete(user.ID); err != nil {
		t.Fatalf("missing webhook tables must not block user delete: %v", err)
	}
	if got := userDeleteTestCount(t, &model.User{}, "id = ?", user.ID); got != 0 {
		t.Fatalf("user row = %d, want 0", got)
	}
}

// 级联与用户删除在同一事务: 用户行删失败时, 已删的订阅/投递必须回滚回来,
// 不能留下"订阅没了、账号还在"的半截状态。
func TestUserDeleteRollsBackCascadeOnFailure(t *testing.T) {
	userDeleteTestSetup(t)
	user := userDeleteTestCreateUser(t, "rollback")
	hook := userDeleteTestCreateWebhook(t, user.ID)
	userDeleteTestCreateDelivery(t, hook, "rollback-delivery-1")

	// 断掉最后一步: users 表消失 -> 事务末尾的删除必然失败。
	if err := db.GetDB().Migrator().DropTable(&model.User{}); err != nil {
		t.Fatalf("drop users: %v", err)
	}
	err := UserDelete(user.ID)
	if err == nil {
		t.Fatal("delete must fail when the users table is missing")
	}
	// 错误可以回显给调用方, 但响应层会统一替换成固定文案; 这里只关心回滚效果。
	if !strings.Contains(err.Error(), "delete user") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := userDeleteTestCount(t, &model.Webhook{}, "user_id = ?", user.ID); got != 1 {
		t.Fatalf("webhook row after rollback = %d, want 1", got)
	}
	if got := userDeleteTestCount(t, &model.WebhookDelivery{}, "user_id = ?", user.ID); got != 1 {
		t.Fatalf("delivery row after rollback = %d, want 1", got)
	}
}

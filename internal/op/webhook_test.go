package op

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/secretstore"
	"gorm.io/gorm"
)

// webhookTestSetup 独立内存 SQLite + 真实主密钥: 覆盖信封加解密与生产同一条路径。
func webhookTestSetup(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "webhook.db"), false); err != nil {
		t.Fatal(err)
	}
	if err := secretstore.Init(secretstore.KEKConfig{Inline: strings.Repeat("a1", 32)}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		secretstore.Reset()
		_ = db.Close()
	})
}

func webhookTestCreate(t *testing.T, userID uint, rawURL string, events []string, secret string) model.WebhookView {
	t.Helper()
	view, err := WebhookCreate(context.Background(), userID, model.WebhookCreateRequest{
		URL:    rawURL,
		Events: events,
		Secret: secret,
	})
	if err != nil {
		t.Fatalf("WebhookCreate(%s): %v", rawURL, err)
	}
	return view
}

// URL 收紧策略: 仅 https、仅 443、禁 userinfo/fragment、禁私网/环回/元数据。
func TestWebhookValidateURL(t *testing.T) {
	webhookTestSetup(t)
	ctx := context.Background()

	rejects := []string{
		"",
		"not a url",
		"http://hooks.example.com/x",            // 非 https
		"https://hooks.example.com:8443/x",      // 非 443
		"https://hooks.example.com:80/x",        // 非 443
		"https://user:pass@hooks.example.com/x", // userinfo
		"https://hooks.example.com/x#frag",      // fragment
		"https://127.0.0.1/hook",                // 环回
		"https://10.1.2.3/hook",                 // 私网
		"https://172.16.0.1/hook",               // 私网
		"https://192.168.1.1/hook",              // 私网
		"https://169.254.169.254/meta",          // 云元数据
		"https://[::1]/hook",                    // 环回 v6
		"https://100.100.100.200/meta",          // CGNAT(阿里云元数据)
		"ftp://hooks.example.com/x",             // 非 https
		"https:///path",                         // 无主机
		"https://hooks.example.com/" + strings.Repeat("a", model.WebhookURLMaxLen),
	}
	for _, raw := range rejects {
		if err := webhookValidateURL(ctx, raw); !errors.Is(err, ErrWebhookURLInvalid) {
			t.Fatalf("webhookValidateURL(%q) = %v, want ErrWebhookURLInvalid", raw, err)
		}
	}

	accepts := []string{
		"https://hooks.example.com/webhook",
		"https://hooks.example.com:443/webhook",
		"https://hooks.example.com/webhook?token=abc123", // 查询串允许(URL 里的 token 属用户配置)
		"https://8.8.8.8/hook",                           // 公网 IP
	}
	for _, raw := range accepts {
		if err := webhookValidateURL(ctx, raw); err != nil {
			t.Fatalf("webhookValidateURL(%q) = %v, want nil", raw, err)
		}
	}
}

// 事件闭集 + 去重 + 数量上限。
func TestWebhookValidateEvents(t *testing.T) {
	if _, err := webhookValidateEvents(nil); !errors.Is(err, ErrWebhookEventsInvalid) {
		t.Fatalf("empty events should be rejected: %v", err)
	}
	if _, err := webhookValidateEvents([]string{"system", "made_up_event"}); !errors.Is(err, ErrWebhookEventsInvalid) {
		t.Fatalf("unknown event should be rejected: %v", err)
	}
	tooMany := make([]string, model.WebhookMaxEvents+1)
	for i := range tooMany {
		tooMany[i] = "system"
	}
	if _, err := webhookValidateEvents(tooMany); !errors.Is(err, ErrWebhookEventsInvalid) {
		t.Fatalf("too many events should be rejected: %v", err)
	}
	got, err := webhookValidateEvents([]string{"system", "recharge", "system"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "system" || got[1] != "recharge" {
		t.Fatalf("dedupe failed: %v", got)
	}
}

// 越权即 404: 别人的订阅读/改/删/测试/日志一律 ErrWebhookNotFound。
func TestWebhookUserIsolation(t *testing.T) {
	webhookTestSetup(t)
	ctx := context.Background()
	mine := webhookTestCreate(t, 1, "https://hooks.example.com/mine", []string{"system"}, strings.Repeat("k", 16))
	theirs := webhookTestCreate(t, 2, "https://hooks.example.com/theirs", []string{"system"}, strings.Repeat("k", 16))

	if _, err := WebhookGet(ctx, 1, theirs.ID); !errors.Is(err, ErrWebhookNotFound) {
		t.Fatalf("cross-user get: %v", err)
	}
	if _, err := WebhookUpdate(ctx, 1, theirs.ID, model.WebhookUpdateRequest{Secret: strings.Repeat("x", 16)}); !errors.Is(err, ErrWebhookNotFound) {
		t.Fatalf("cross-user update: %v", err)
	}
	if err := WebhookDelete(ctx, 1, theirs.ID); !errors.Is(err, ErrWebhookNotFound) {
		t.Fatalf("cross-user delete: %v", err)
	}
	if _, err := WebhookTest(ctx, 1, theirs.ID); !errors.Is(err, ErrWebhookNotFound) {
		t.Fatalf("cross-user test: %v", err)
	}
	if _, err := WebhookLogList(ctx, 1, theirs.ID, model.WebhookListRequest{}); !errors.Is(err, ErrWebhookNotFound) {
		t.Fatalf("cross-user logs: %v", err)
	}
	// 本人读写正常。
	if got, err := WebhookGet(ctx, 1, mine.ID); err != nil || got.ID != mine.ID {
		t.Fatalf("own get: %v %+v", err, got)
	}
	if _, err := WebhookGet(ctx, 1, 999999); !errors.Is(err, ErrWebhookNotFound) {
		t.Fatalf("missing get: %v", err)
	}
}

// 列表按用户隔离并正确分页。
func TestWebhookListScopedAndPaginated(t *testing.T) {
	webhookTestSetup(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		webhookTestCreate(t, 1, "https://hooks.example.com/"+string(rune('a'+i)), []string{"system"}, strings.Repeat("k", 16))
	}
	webhookTestCreate(t, 2, "https://hooks.example.com/other", []string{"system"}, strings.Repeat("k", 16))

	page, err := WebhookList(ctx, 1, model.WebhookListRequest{Page: 1, PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || len(page.Items) != 2 || page.Page != 1 || page.PageSize != 2 {
		t.Fatalf("page = %+v", page)
	}
	page, err = WebhookList(ctx, 1, model.WebhookListRequest{Page: 2, PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("page 2 len = %d", len(page.Items))
	}
	page, err = WebhookList(ctx, 2, model.WebhookListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 {
		t.Fatalf("user 2 total = %d", page.Total)
	}
}

// 改 URL 递增版本并取消未完成投递; 禁用同理; 删除清空队列与日志。
func TestWebhookUpdateDisableDeleteCancelOldDeliveries(t *testing.T) {
	webhookTestSetup(t)
	ctx := context.Background()
	hook := webhookTestCreate(t, 5, "https://hooks.example.com/old", []string{"system"}, strings.Repeat("k", 16))

	enqueue := func() uint64 {
		t.Helper()
		var row model.Webhook
		if err := db.GetDB().First(&row, hook.ID).Error; err != nil {
			t.Fatal(err)
		}
		var deliveryID string
		if err := db.GetDB().Transaction(func(tx *gorm.DB) error {
			var err error
			deliveryID, err = webhookEnqueueTx(tx, row, string(model.NotificationTypeSystem), 0, model.WebhookPayloadData{Title: "t"})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		var d model.WebhookDelivery
		if err := db.GetDB().Where("delivery_id = ?", deliveryID).First(&d).Error; err != nil {
			t.Fatal(err)
		}
		return d.ID
	}

	// GORM 坑: First(&dest, pk) 复用已扫描过的 dest 时会把 dest 的旧主键也拼进条件,
	// 每次查询都用新变量(生产代码同样如此)。
	loadDelivery := func(id uint64) model.WebhookDelivery {
		t.Helper()
		var row model.WebhookDelivery
		if err := db.GetDB().Where("id = ?", id).First(&row).Error; err != nil {
			t.Fatal(err)
		}
		return row
	}

	// 改 URL: 版本 +1, 旧投递取消。
	first := enqueue()
	newURL := "https://hooks.example.com/new"
	updated, err := WebhookUpdate(ctx, 5, hook.ID, model.WebhookUpdateRequest{URL: &newURL})
	if err != nil {
		t.Fatal(err)
	}
	if updated.URL != newURL {
		t.Fatalf("url not updated: %s", updated.URL)
	}
	if d := loadDelivery(first); d.Status != model.WebhookDeliveryStatusCancelled {
		t.Fatalf("old delivery status = %s, want cancelled", d.Status)
	}
	var row model.Webhook
	if err := db.GetDB().First(&row, hook.ID).Error; err != nil {
		t.Fatal(err)
	}
	if row.Version != 2 {
		t.Fatalf("version = %d, want 2", row.Version)
	}

	// 禁用: 未完成投递取消。
	second := enqueue()
	if _, err := WebhookUpdate(ctx, 5, hook.ID, model.WebhookUpdateRequest{Enabled: boolPtr(false)}); err != nil {
		t.Fatal(err)
	}
	if d := loadDelivery(second); d.Status != model.WebhookDeliveryStatusCancelled {
		t.Fatalf("delivery after disable = %s, want cancelled", d.Status)
	}

	// 其它字段更新(重试次数)不应作废已有投递。
	third := enqueue()
	if _, err := WebhookUpdate(ctx, 5, hook.ID, model.WebhookUpdateRequest{Enabled: boolPtr(true), RetryCount: intPtr(1)}); err != nil {
		t.Fatal(err)
	}
	if d := loadDelivery(third); d.Status != model.WebhookDeliveryStatusPending {
		t.Fatalf("delivery after retry_count change = %s, want pending", d.Status)
	}

	// 删除: 订阅与其全部投递/日志一并消失。
	enqueue()
	if err := WebhookDelete(ctx, 5, hook.ID); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.GetDB().Model(&model.WebhookDelivery{}).Where("webhook_id = ?", hook.ID).Count(&count)
	if count != 0 {
		t.Fatalf("deliveries after delete = %d, want 0", count)
	}
	if err := WebhookDelete(ctx, 5, hook.ID); !errors.Is(err, ErrWebhookNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

// 测试发送: 入队受 60 秒/订阅频率限制(行级 CAS, 并发连点只放行一次)。
func TestWebhookTestRateLimit(t *testing.T) {
	webhookTestSetup(t)
	ctx := context.Background()
	hook := webhookTestCreate(t, 6, "https://hooks.example.com/x", []string{"system"}, strings.Repeat("k", 16))

	result, err := WebhookTest(ctx, 6, hook.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.DeliveryID == "" || result.Status != model.WebhookDeliveryStatusPending {
		t.Fatalf("test result = %+v", result)
	}
	if _, err := WebhookTest(ctx, 6, hook.ID); !errors.Is(err, ErrWebhookTestRateLimited) {
		t.Fatalf("second test within 60s should be rate limited, got %v", err)
	}
	// 测试投递 event=test 且带 payload。
	var d model.WebhookDelivery
	if err := db.GetDB().Where("delivery_id = ?", result.DeliveryID).First(&d).Error; err != nil {
		t.Fatal(err)
	}
	if d.Event != model.WebhookEventTest {
		t.Fatalf("event = %s", d.Event)
	}
	var payload model.WebhookPayload
	if err := json.Unmarshal([]byte(d.Payload), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ID != result.DeliveryID || payload.UserID != 6 {
		t.Fatalf("payload = %+v", payload)
	}

	// 窗口过后放行。
	old := time.Now().Add(-2 * model.WebhookTestMinInterval)
	if err := db.GetDB().Model(&model.Webhook{}).Where("id = ?", hook.ID).Update("last_test_at", old).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := WebhookTest(ctx, 6, hook.ID); err != nil {
		t.Fatalf("test after window: %v", err)
	}
}

// 日志视图不含响应正文与 URL 字段, 只有脱敏摘要。
func TestWebhookLogListShape(t *testing.T) {
	webhookTestSetup(t)
	ctx := context.Background()
	hook := webhookTestCreate(t, 8, "https://hooks.example.com/token-secret-path", []string{"system"}, strings.Repeat("k", 16))

	var row model.Webhook
	if err := db.GetDB().First(&row, hook.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().Transaction(func(tx *gorm.DB) error {
		_, err := webhookEnqueueTx(tx, row, string(model.NotificationTypeSystem), 1, model.WebhookPayloadData{Title: "t"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	page, err := WebhookLogList(ctx, 8, hook.ID, model.WebhookListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("logs = %+v", page)
	}
	body, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"token-secret-path", "hooks.example.com", "payload", "secret"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("log view leaks %q: %s", forbidden, body)
		}
	}
}

// 通知集成: 通知写入与 webhook 入队同事务, 只匹配该用户订阅的事件。
func TestNotificationEnqueueSubscribedEventsOnly(t *testing.T) {
	webhookTestSetup(t)
	ctx := context.Background()

	hookA := webhookTestCreate(t, 1, "https://hooks.example.com/a", []string{"recharge"}, strings.Repeat("k", 16))
	hookB := webhookTestCreate(t, 1, "https://hooks.example.com/b", []string{"ticket"}, strings.Repeat("k", 16))
	hookC := webhookTestCreate(t, 2, "https://hooks.example.com/c", []string{"recharge"}, strings.Repeat("k", 16))

	if err := NotificationCreate(ctx, 1, model.NotificationTypeRecharge, "充值成功", "内容", "/wallet", 42); err != nil {
		t.Fatal(err)
	}

	deliveriesFor := func(hookID uint64) []model.WebhookDelivery {
		t.Helper()
		var rows []model.WebhookDelivery
		if err := db.GetDB().Where("webhook_id = ?", hookID).Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		return rows
	}
	rowsA := deliveriesFor(hookA.ID)
	if len(rowsA) != 1 {
		t.Fatalf("hook A deliveries = %d, want 1", len(rowsA))
	}
	if rowsA[0].Event != string(model.NotificationTypeRecharge) || rowsA[0].UserID != 1 {
		t.Fatalf("delivery = %+v", rowsA[0])
	}
	var payload model.WebhookPayload
	if err := json.Unmarshal([]byte(rowsA[0].Payload), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.UserID != 1 || payload.Data.Title != "充值成功" || payload.Data.Link != "/wallet" || payload.Data.RelatedID != 42 {
		t.Fatalf("payload = %+v", payload)
	}
	if payload.ID != rowsA[0].DeliveryID {
		t.Fatalf("payload id must equal delivery id: %+v", payload)
	}
	if rows := deliveriesFor(hookB.ID); len(rows) != 0 {
		t.Fatalf("hook B(not subscribed) got %d deliveries", len(rows))
	}
	if rows := deliveriesFor(hookC.ID); len(rows) != 0 {
		t.Fatalf("other user hook got %d deliveries", len(rows))
	}

	// 未订阅的事件不入队。
	if err := NotificationCreate(ctx, 1, model.NotificationTypeSystem, "系统", "内容", "", 0); err != nil {
		t.Fatal(err)
	}
	if rows := deliveriesFor(hookA.ID); len(rows) != 1 {
		t.Fatalf("hook A deliveries = %d after unrelated event", len(rows))
	}
}

// 生产路径不吞错: webhook 表缺失(测试 fixture 问题)时通知写入必须整体失败并回滚,
// 而不是"通知落库、入队静默跳过"。fixture 的正确做法是把表建全, 生产绝不降级。
func TestNotificationCreateFailsWhenWebhookTableMissing(t *testing.T) {
	webhookTestSetup(t)
	ctx := context.Background()
	// 先造一条订阅, 让入队路径真的要写投递表(fixture 建全表才是正确做法)。
	webhookTestCreate(t, 1, "https://hooks.example.com/a", []string{"system"}, strings.Repeat("k", 16))
	if err := db.GetDB().Migrator().DropTable(&model.WebhookDelivery{}); err != nil {
		t.Fatal(err)
	}
	if err := NotificationCreate(ctx, 1, model.NotificationTypeSystem, "通知", "内容", "", 0); err == nil {
		t.Fatal("notification create must fail when enqueue is impossible (no swallowed errors)")
	}
	var count int64
	db.GetDB().Model(&model.Notification{}).Count(&count)
	if count != 0 {
		t.Fatalf("notification rolled back? count = %d, want 0", count)
	}
}

// 损坏的订阅事件行: 报错可见, 不静默跳过。
func TestNotificationCreateFailsOnCorruptWebhookEvents(t *testing.T) {
	webhookTestSetup(t)
	ctx := context.Background()
	hook := webhookTestCreate(t, 1, "https://hooks.example.com/a", []string{"system"}, strings.Repeat("k", 16))
	if err := db.GetDB().Model(&model.Webhook{}).Where("id = ?", hook.ID).Update("events", "{corrupt").Error; err != nil {
		t.Fatal(err)
	}
	if err := NotificationCreate(ctx, 1, model.NotificationTypeSystem, "通知", "内容", "", 0); err == nil {
		t.Fatal("corrupt subscription events must surface as error")
	}
}

// 备份边界(本期约定): JSON 导出 DBExportAll 只含路由/设置/统计, 与用户通知/工单/账单同口径,
// 不扩大到 webhook —— 明文 dump 绝不能出现订阅密钥(明文或信封)。
// 全库 sqlite 备份天然覆盖 webhook 表; 跨实例恢复需连主密钥文件一起备份(信封 AAD 绑定行归属)。
// 本测试锁死"不扩大 JSON dump 范围"的边界, 防止后续有人顺手把 webhook 塞进明文 dump。
func TestWebhookStaysOutOfJSONDump(t *testing.T) {
	webhookTestSetup(t)
	ctx := context.Background()
	const secret = "dump-boundary-secret-0123456789"
	hook := webhookTestCreate(t, 1, "https://hooks.example.com/x", []string{"system"}, secret)

	var row model.Webhook
	if err := db.GetDB().Where("id = ?", hook.ID).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if !secretstore.IsEnvelope(row.SecretEncrypted) {
		t.Fatalf("secret not sealed: %q", row.SecretEncrypted)
	}
	dump, err := DBExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(dump)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{secret, row.SecretEncrypted, "secret_encrypted", "webhook"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("json dump leaks %q (webhook must stay out of DBExportAll scope)", forbidden)
		}
	}
}
func boolPtr(v bool) *bool { return &v }
func intPtr(v int) *int    { return &v }

// 密钥长度 16..128 字节(按 UTF-8 字节数)。
func TestWebhookValidateSecret(t *testing.T) {
	for _, secret := range []string{"", "short", strings.Repeat("s", model.WebhookSecretMaxBytes+1)} {
		if _, err := webhookValidateSecret(secret); !errors.Is(err, ErrWebhookSecretInvalid) {
			t.Fatalf("secret %q(len=%d) should be rejected: %v", secret, len(secret), err)
		}
	}
	// 多字节: 12 个汉字 = 36 字节, 合法。
	if _, err := webhookValidateSecret(strings.Repeat("密", 12)); err != nil {
		t.Fatalf("36-byte multibyte secret should pass: %v", err)
	}
	if _, err := webhookValidateSecret(strings.Repeat("密", 5)); !errors.Is(err, ErrWebhookSecretInvalid) {
		t.Fatal("15-byte multibyte secret should be rejected")
	}
}

// 密钥只进不出: 落库是信封密文, 返回值只有 has_secret, 永不回传明文/密文。
func TestWebhookSecretSealedAndNeverReturned(t *testing.T) {
	webhookTestSetup(t)
	const secret = "s3cret-signing-key-0123456789"

	view := webhookTestCreate(t, 7, "https://hooks.example.com/a", []string{"system"}, secret)
	if !view.HasSecret {
		t.Fatal("has_secret should be true")
	}
	body, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), secret) || strings.Contains(string(body), "secret_encrypted") {
		t.Fatalf("secret leaked into view JSON: %s", body)
	}

	var hook model.Webhook
	if err := db.GetDB().First(&hook, view.ID).Error; err != nil {
		t.Fatal(err)
	}
	if hook.SecretEncrypted == secret {
		t.Fatal("secret stored in plaintext")
	}
	if !secretstore.IsEnvelope(hook.SecretEncrypted) {
		t.Fatalf("secret not sealed: %q", hook.SecretEncrypted)
	}
	opened, err := webhookOpenSecret(hook)
	if err != nil || opened != secret {
		t.Fatalf("open = %q, %v", opened, err)
	}

	// AAD 归属: 把密文搬到别的行(换归属)解不开。
	var other model.Webhook
	other = model.Webhook{UserID: 7, URL: "https://hooks.example.com/b", Events: "[\"system\"]", Enabled: true, Version: 1}
	if err := db.GetDB().Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().Model(&other).Update("secret_encrypted", hook.SecretEncrypted).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := webhookOpenSecret(other); err == nil {
		t.Fatal("copied envelope should fail ownership binding check")
	}
}

// 创建必填密钥 16..128 字节; retry_count 0..3。
func TestWebhookCreateValidation(t *testing.T) {
	webhookTestSetup(t)
	ctx := context.Background()
	_, err := WebhookCreate(ctx, 1, model.WebhookCreateRequest{
		URL: "https://hooks.example.com/x", Events: []string{"system"}, Secret: "short",
	})
	if !errors.Is(err, ErrWebhookSecretInvalid) {
		t.Fatalf("want ErrWebhookSecretInvalid, got %v", err)
	}
	badRetry := 4
	_, err = WebhookCreate(ctx, 1, model.WebhookCreateRequest{
		URL: "https://hooks.example.com/x", Events: []string{"system"}, Secret: strings.Repeat("k", 16), RetryCount: &badRetry,
	})
	if !errors.Is(err, ErrWebhookRetryInvalid) {
		t.Fatalf("want ErrWebhookRetryInvalid, got %v", err)
	}
	view, err := WebhookCreate(ctx, 1, model.WebhookCreateRequest{
		URL: "https://hooks.example.com/x", Events: []string{"system"}, Secret: strings.Repeat("k", 16),
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.RetryCount != 2 || !view.Enabled || view.LastStatus != model.WebhookLastStatusNone {
		t.Fatalf("defaults wrong: %+v", view)
	}
}

// 配额: 每用户最多 10 条, 互不影响。
func TestWebhookQuota(t *testing.T) {
	webhookTestSetup(t)
	ctx := context.Background()
	for i := 0; i < model.WebhookMaxPerUser; i++ {
		webhookTestCreate(t, 1, "https://hooks.example.com/"+string(rune('a'+i)), []string{"system"}, strings.Repeat("k", 16))
	}
	_, err := WebhookCreate(ctx, 1, model.WebhookCreateRequest{
		URL: "https://hooks.example.com/overflow", Events: []string{"system"}, Secret: strings.Repeat("k", 16),
	})
	if !errors.Is(err, ErrWebhookQuota) {
		t.Fatalf("11th webhook should hit quota, got %v", err)
	}
	// 另一用户不受影响。
	if _, err := WebhookCreate(ctx, 2, model.WebhookCreateRequest{
		URL: "https://hooks.example.com/b", Events: []string{"system"}, Secret: strings.Repeat("k", 16),
	}); err != nil {
		t.Fatalf("other user quota should be independent: %v", err)
	}
}

// 更新留空 secret 保留原密钥; 换新密钥立即生效。
func TestWebhookUpdateSecretSemantics(t *testing.T) {
	webhookTestSetup(t)
	ctx := context.Background()
	const secret = "original-secret-0123456789ab"
	view := webhookTestCreate(t, 3, "https://hooks.example.com/x", []string{"system"}, secret)

	empty := ""
	updated, err := WebhookUpdate(ctx, 3, view.ID, model.WebhookUpdateRequest{Secret: empty})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.HasSecret {
		t.Fatal("empty secret must keep the old one")
	}
	var hook model.Webhook
	if err := db.GetDB().First(&hook, view.ID).Error; err != nil {
		t.Fatal(err)
	}
	if opened, err := webhookOpenSecret(hook); err != nil || opened != secret {
		t.Fatalf("kept secret = %q, %v", opened, err)
	}

	const replacement = "replacement-secret-9876543210"
	if _, err := WebhookUpdate(ctx, 3, view.ID, model.WebhookUpdateRequest{Secret: replacement}); err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().First(&hook, view.ID).Error; err != nil {
		t.Fatal(err)
	}
	if opened, err := webhookOpenSecret(hook); err != nil || opened != replacement {
		t.Fatalf("rotated secret = %q, %v", opened, err)
	}

	// 短密钥在更新时同样拒绝, 且不影响现有密钥。
	if _, err := WebhookUpdate(ctx, 3, view.ID, model.WebhookUpdateRequest{Secret: "short"}); !errors.Is(err, ErrWebhookSecretInvalid) {
		t.Fatalf("want ErrWebhookSecretInvalid, got %v", err)
	}
	if err := db.GetDB().First(&hook, view.ID).Error; err != nil {
		t.Fatal(err)
	}
	if opened, _ := webhookOpenSecret(hook); opened != replacement {
		t.Fatalf("secret changed after rejected update: %q", opened)
	}
}

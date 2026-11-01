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

// webhookTestDeliveryRow 直接在库里造一条指定状态的投递行(绕过入队/投递引擎),
// 专供重放与日志过滤测试构造终态数据。
func webhookTestDeliveryRow(t *testing.T, hookID uint64, userID uint, deliveryID, event, status string) model.WebhookDelivery {
	t.Helper()
	// 用生产同一个构造函数造 body: 重放要按字段重建 payload, 测试必须覆盖真实的字段形状。
	payload, err := webhookBuildPayload(deliveryID, event, time.Now().Add(-time.Hour), userID, model.WebhookPayloadData{
		Title:     "重放测试",
		Content:   "payload 内容",
		Link:      "/wallet",
		RelatedID: 42,
	})
	if err != nil {
		t.Fatalf("build delivery payload: %v", err)
	}
	row := model.WebhookDelivery{
		DeliveryID:    deliveryID,
		WebhookID:     hookID,
		UserID:        userID,
		HookVersion:   1,
		Event:         event,
		Payload:       payload,
		Status:        status,
		Attempts:      0,
		NextAttemptAt: time.Now().Add(-time.Minute),
	}
	if err := db.GetDB().Create(&row).Error; err != nil {
		t.Fatalf("create delivery row %s: %v", deliveryID, err)
	}
	return row
}

func webhookTestLoadDelivery(t *testing.T, deliveryID string) model.WebhookDelivery {
	t.Helper()
	var row model.WebhookDelivery
	if err := db.GetDB().Where("delivery_id = ?", deliveryID).First(&row).Error; err != nil {
		t.Fatalf("load delivery %s: %v", deliveryID, err)
	}
	return row
}

// 重放: 新建一条 pending 行并复制 event/payload/hook_version/归属; 原行完全不动(保留审计)。
func TestWebhookReplayDeliveryCreatesNewPendingRow(t *testing.T) {
	webhookTestSetup(t)
	ctx := context.Background()
	hook := webhookTestCreate(t, 1, "https://hooks.example.com/replay", []string{"system"}, strings.Repeat("k", 16))

	const originalID = "original-delivery-0001"
	webhookTestDeliveryRow(t, hook.ID, 1, originalID, string(model.NotificationTypeSystem), model.WebhookDeliveryStatusFailed)
	// 首投的失败痕迹: 重放后原行必须原样保留。
	if err := db.GetDB().Model(&model.WebhookDelivery{}).Where("delivery_id = ?", originalID).
		Updates(map[string]interface{}{
			"attempts":         3,
			"last_status_code": 500,
			"duration_ms":      120,
			"last_error":       "endpoint responded 500",
		}).Error; err != nil {
		t.Fatal(err)
	}
	src := webhookTestLoadDelivery(t, originalID)

	view, err := WebhookReplayDelivery(ctx, 1, hook.ID, originalID)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if view.DeliveryID == "" || view.DeliveryID == src.DeliveryID {
		t.Fatalf("replay must get a fresh delivery id, got %q", view.DeliveryID)
	}
	if view.Status != model.WebhookDeliveryStatusPending || view.Attempts != 0 {
		t.Fatalf("replay view = %+v, want fresh pending", view)
	}
	if view.ResponseCode != 0 || view.DurationMs != 0 || view.Error != "" {
		t.Fatalf("replay view leaks previous attempt result: %+v", view)
	}

	replayed := webhookTestLoadDelivery(t, view.DeliveryID)
	if replayed.ID == src.ID {
		t.Fatal("replay must insert a new row, not reuse the original")
	}
	if replayed.WebhookID != src.WebhookID || replayed.UserID != src.UserID {
		t.Fatalf("ownership not copied: %+v", replayed)
	}
	if replayed.Event != src.Event || replayed.HookVersion != src.HookVersion {
		t.Fatalf("content not copied: event=%q hook_version=%d", replayed.Event, replayed.HookVersion)
	}
	// body 必须按新投递 ID 重建: 接收端按 payload.id 去重, 照抄旧 id 会整条被丢掉。
	var srcPayload model.WebhookPayload
	if err := json.Unmarshal([]byte(src.Payload), &srcPayload); err != nil {
		t.Fatal(err)
	}
	var replayPayload model.WebhookPayload
	if err := json.Unmarshal([]byte(replayed.Payload), &replayPayload); err != nil {
		t.Fatalf("replay payload not decodable: %v (%s)", err, replayed.Payload)
	}
	if replayPayload.ID != view.DeliveryID || replayPayload.ID == srcPayload.ID {
		t.Fatalf("replay payload id = %q, want %q (original %q)", replayPayload.ID, view.DeliveryID, srcPayload.ID)
	}
	if replayPayload.Timestamp == 0 {
		t.Fatal("replay payload must carry a timestamp")
	}
	if replayPayload.Event != srcPayload.Event || replayPayload.UserID != srcPayload.UserID ||
		replayPayload.Data != srcPayload.Data {
		t.Fatalf("replay payload changed content: %+v, original %+v", replayPayload, srcPayload)
	}
	if replayed.Payload == src.Payload {
		t.Fatal("replay payload must be rebuilt, not copied byte-for-byte")
	}
	if replayed.Status != model.WebhookDeliveryStatusPending || replayed.Attempts != 0 {
		t.Fatalf("replay row = %+v, want pending with 0 attempts", replayed)
	}
	if replayed.NextAttemptAt.IsZero() || replayed.NextAttemptAt.After(time.Now().Add(5*time.Second)) {
		t.Fatalf("replay must be immediately due, next_attempt_at=%v", replayed.NextAttemptAt)
	}
	if replayed.LeaseExpiresAt != nil || replayed.LastStatusCode != 0 || replayed.DurationMs != 0 || replayed.LastError != "" {
		t.Fatalf("replay row must reset attempt columns: %+v", replayed)
	}
	if replayed.NotificationID != 0 {
		t.Fatalf("replay must not inherit notification id: %d", replayed.NotificationID)
	}

	// 原行是审计记录: 重放改不动它。
	after := webhookTestLoadDelivery(t, originalID)
	if after.Status != model.WebhookDeliveryStatusFailed || after.Attempts != 3 ||
		after.LastStatusCode != 500 || after.DurationMs != 120 || after.LastError != "endpoint responded 500" {
		t.Fatalf("original row mutated by replay: %+v", after)
	}
	var count int64
	db.GetDB().Model(&model.WebhookDelivery{}).Where("webhook_id = ?", hook.ID).Count(&count)
	if count != 2 {
		t.Fatalf("delivery rows = %d, want 2 (original + replay)", count)
	}

	// 同一行可以重复重放: 每次都是独立 id + 各自匹配的 body.id。
	second, err := WebhookReplayDelivery(ctx, 1, hook.ID, originalID)
	if err != nil {
		t.Fatalf("second replay: %v", err)
	}
	if second.DeliveryID == view.DeliveryID {
		t.Fatalf("two replays must get different ids, both %q", second.DeliveryID)
	}
	secondRow := webhookTestLoadDelivery(t, second.DeliveryID)
	var secondPayload model.WebhookPayload
	if err := json.Unmarshal([]byte(secondRow.Payload), &secondPayload); err != nil {
		t.Fatal(err)
	}
	if secondPayload.ID != second.DeliveryID {
		t.Fatalf("second replay payload id = %q, want %q", secondPayload.ID, second.DeliveryID)
	}
	if secondPayload.Data != srcPayload.Data || secondPayload.Event != srcPayload.Event {
		t.Fatalf("second replay payload changed content: %+v", secondPayload)
	}
	// 第一次重放的行不受第二次重放影响。
	if again := webhookTestLoadDelivery(t, view.DeliveryID); again.Payload != replayed.Payload {
		t.Fatalf("first replay row mutated by second replay: %q", again.Payload)
	}
	db.GetDB().Model(&model.WebhookDelivery{}).Where("webhook_id = ?", hook.ID).Count(&count)
	if count != 3 {
		t.Fatalf("delivery rows = %d, want 3 (original + 2 replays)", count)
	}
}

// cancelled 同样可重放(用户手动取消后想把这条再发一次)。
func TestWebhookReplayDeliveryAllowsCancelled(t *testing.T) {
	webhookTestSetup(t)
	ctx := context.Background()
	hook := webhookTestCreate(t, 1, "https://hooks.example.com/cancelled", []string{"system"}, strings.Repeat("k", 16))
	webhookTestDeliveryRow(t, hook.ID, 1, "cancelled-delivery-0001", string(model.NotificationTypeSystem), model.WebhookDeliveryStatusCancelled)

	view, err := WebhookReplayDelivery(ctx, 1, hook.ID, "cancelled-delivery-0001")
	if err != nil {
		t.Fatalf("replay cancelled delivery: %v", err)
	}
	if view.Status != model.WebhookDeliveryStatusPending {
		t.Fatalf("status = %s, want pending", view.Status)
	}
}

// 损坏的存量 body: 重放必须报错而不是退回旧 payload(退回等于让接收端按旧 id 去重丢弃)。
func TestWebhookReplayDeliveryRejectsCorruptPayload(t *testing.T) {
	webhookTestSetup(t)
	ctx := context.Background()
	hook := webhookTestCreate(t, 1, "https://hooks.example.com/corrupt", []string{"system"}, strings.Repeat("k", 16))
	webhookTestDeliveryRow(t, hook.ID, 1, "corrupt-delivery-0001", string(model.NotificationTypeSystem), model.WebhookDeliveryStatusFailed)
	if err := db.GetDB().Model(&model.WebhookDelivery{}).Where("delivery_id = ?", "corrupt-delivery-0001").
		Update("payload", "{corrupt").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := WebhookReplayDelivery(ctx, 1, hook.ID, "corrupt-delivery-0001"); err == nil {
		t.Fatal("replay of a corrupt payload must fail, not fall back to the old body")
	}
	var count int64
	db.GetDB().Model(&model.WebhookDelivery{}).Where("webhook_id = ?", hook.ID).Count(&count)
	if count != 1 {
		t.Fatalf("delivery rows = %d, want 1 (no replay row on failure)", count)
	}
}

// 非终态不允许重放: pending/processing 还在队列里(再插一条就是重复外发), succeeded 重放是主动重复推送。
func TestWebhookReplayDeliveryRejectsNonTerminal(t *testing.T) {
	webhookTestSetup(t)
	ctx := context.Background()
	hook := webhookTestCreate(t, 1, "https://hooks.example.com/not-terminal", []string{"system"}, strings.Repeat("k", 16))

	for i, status := range []string{
		model.WebhookDeliveryStatusPending,
		model.WebhookDeliveryStatusProcessing,
		model.WebhookDeliveryStatusSucceeded,
	} {
		deliveryID := "non-terminal-" + status
		webhookTestDeliveryRow(t, hook.ID, 1, deliveryID, string(model.NotificationTypeSystem), status)
		if _, err := WebhookReplayDelivery(ctx, 1, hook.ID, deliveryID); !errors.Is(err, ErrWebhookDeliveryNotReplayable) {
			t.Fatalf("status %s replay: %v, want ErrWebhookDeliveryNotReplayable", status, err)
		}
		var count int64
		db.GetDB().Model(&model.WebhookDelivery{}).Where("webhook_id = ?", hook.ID).Count(&count)
		if count != int64(i+1) {
			t.Fatalf("status %s must not insert a row: count = %d", status, count)
		}
	}
}

// 归属隔离: 别人的订阅是 404 webhook_not_found; 投递不在该订阅下一律 404 webhook_delivery_not_found。
func TestWebhookReplayDeliveryIsolation(t *testing.T) {
	webhookTestSetup(t)
	ctx := context.Background()
	mine := webhookTestCreate(t, 1, "https://hooks.example.com/mine", []string{"system"}, strings.Repeat("k", 16))
	sibling := webhookTestCreate(t, 1, "https://hooks.example.com/sibling", []string{"system"}, strings.Repeat("k", 16))
	theirs := webhookTestCreate(t, 2, "https://hooks.example.com/theirs", []string{"system"}, strings.Repeat("k", 16))
	webhookTestDeliveryRow(t, mine.ID, 1, "isolation-delivery-0001", string(model.NotificationTypeSystem), model.WebhookDeliveryStatusFailed)

	if _, err := WebhookReplayDelivery(ctx, 1, theirs.ID, "isolation-delivery-0001"); !errors.Is(err, ErrWebhookNotFound) {
		t.Fatalf("replay on other user's webhook: %v, want ErrWebhookNotFound", err)
	}
	if _, err := WebhookReplayDelivery(ctx, 2, mine.ID, "isolation-delivery-0001"); !errors.Is(err, ErrWebhookNotFound) {
		t.Fatalf("replay as other user: %v, want ErrWebhookNotFound", err)
	}
	if _, err := WebhookReplayDelivery(ctx, 1, sibling.ID, "isolation-delivery-0001"); !errors.Is(err, ErrWebhookDeliveryNotFound) {
		t.Fatalf("delivery of another webhook: %v, want ErrWebhookDeliveryNotFound", err)
	}
	if _, err := WebhookReplayDelivery(ctx, 1, mine.ID, "no-such-delivery"); !errors.Is(err, ErrWebhookDeliveryNotFound) {
		t.Fatalf("missing delivery: %v, want ErrWebhookDeliveryNotFound", err)
	}
	var count int64
	db.GetDB().Model(&model.WebhookDelivery{}).Count(&count)
	if count != 1 {
		t.Fatalf("replay failures must not insert rows: count = %d", count)
	}
}

// 日志过滤: status/event 同时作用于 Count 与 Find, 分页时 total 仍是过滤后的总数。
func TestWebhookLogListFilters(t *testing.T) {
	webhookTestSetup(t)
	ctx := context.Background()
	hook := webhookTestCreate(t, 1, "https://hooks.example.com/filters", []string{"system"}, strings.Repeat("k", 16))

	rows := []struct{ deliveryID, event, status string }{
		{"filter-delivery-1", string(model.NotificationTypeSystem), model.WebhookDeliveryStatusPending},
		{"filter-delivery-2", string(model.NotificationTypeSystem), model.WebhookDeliveryStatusFailed},
		{"filter-delivery-3", string(model.NotificationTypeRecharge), model.WebhookDeliveryStatusFailed},
		{"filter-delivery-4", string(model.NotificationTypeSystem), model.WebhookDeliveryStatusSucceeded},
		{"filter-delivery-5", model.WebhookEventTest, model.WebhookDeliveryStatusCancelled},
	}
	for _, row := range rows {
		webhookTestDeliveryRow(t, hook.ID, 1, row.deliveryID, row.event, row.status)
	}

	cases := []struct {
		name      string
		req       model.WebhookListRequest
		wantTotal int64
	}{
		{"no filter", model.WebhookListRequest{}, 5},
		{"status failed", model.WebhookListRequest{Status: model.WebhookDeliveryStatusFailed}, 2},
		{"status succeeded", model.WebhookListRequest{Status: model.WebhookDeliveryStatusSucceeded}, 1},
		{"event system", model.WebhookListRequest{Event: string(model.NotificationTypeSystem)}, 3},
		{"event test", model.WebhookListRequest{Event: model.WebhookEventTest}, 1},
		{"status+event", model.WebhookListRequest{Status: model.WebhookDeliveryStatusFailed, Event: string(model.NotificationTypeRecharge)}, 1},
		{"no match", model.WebhookListRequest{Status: model.WebhookDeliveryStatusFailed, Event: string(model.NotificationTypeTicket)}, 0},
	}
	for _, tc := range cases {
		page, err := WebhookLogList(ctx, 1, hook.ID, tc.req)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if page.Total != tc.wantTotal {
			t.Fatalf("%s: total = %d, want %d", tc.name, page.Total, tc.wantTotal)
		}
		if int64(len(page.Items)) != tc.wantTotal {
			t.Fatalf("%s: items = %d, want %d", tc.name, len(page.Items), tc.wantTotal)
		}
		for _, item := range page.Items {
			if tc.req.Status != "" && item.Status != tc.req.Status {
				t.Fatalf("%s: item status = %s", tc.name, item.Status)
			}
			if tc.req.Event != "" && item.Event != tc.req.Event {
				t.Fatalf("%s: item event = %s", tc.name, item.Event)
			}
		}
	}

	// 分页: total 是过滤后的总数, 不是当前页长度。
	page, err := WebhookLogList(ctx, 1, hook.ID, model.WebhookListRequest{
		Status: model.WebhookDeliveryStatusFailed, Page: 2, PageSize: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Items) != 1 {
		t.Fatalf("paginated filter = %+v, want total 2 with 1 item", page)
	}

	// 非法闭集外的取值是错误, 不是"查不到"。
	if _, err := WebhookLogList(ctx, 1, hook.ID, model.WebhookListRequest{Status: "bogus"}); !errors.Is(err, ErrWebhookStatusInvalid) {
		t.Fatalf("invalid status: %v, want ErrWebhookStatusInvalid", err)
	}
	if _, err := WebhookLogList(ctx, 1, hook.ID, model.WebhookListRequest{Event: "bogus"}); !errors.Is(err, ErrWebhookEventInvalid) {
		t.Fatalf("invalid event: %v, want ErrWebhookEventInvalid", err)
	}
	// 别人的订阅: 过滤参数不影响归属判定, 仍然 404。
	if _, err := WebhookLogList(ctx, 2, hook.ID, model.WebhookListRequest{Status: model.WebhookDeliveryStatusFailed}); !errors.Is(err, ErrWebhookNotFound) {
		t.Fatalf("cross-user filtered logs: %v, want ErrWebhookNotFound", err)
	}
}

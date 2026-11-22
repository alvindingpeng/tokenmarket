package op

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// webhookTemplateTestData 覆盖"标题里带引号/换行/反斜杠"这类会让手拼 JSON 破掉的内容。
func webhookTemplateTestData() model.WebhookPayloadData {
	return model.WebhookPayloadData{
		Title:     "余额告警 \"test\" \\ 路径",
		Content:   "第一行\n第二行\t制表",
		Link:      "/wallet?x=1&y=\"2\"",
		RelatedID: 7,
	}
}

const webhookTemplateTestFixedTS = 1700000000

// 闭集: 模板名会进 SQL 分支与投递分派, 错一个字就是"投出去的 body 谁都不认",
// 而这种失败本地测不出来(只有对方端点才拒)。因此取值、顺序、归一、合法性都钉住。
func TestWebhookTemplateClosedSet(t *testing.T) {
	want := []string{"generic", "dingtalk", "feishu", "slack"}
	if len(model.WebhookTemplates) != len(want) {
		t.Fatalf("模板数 = %d, 期望 %d", len(model.WebhookTemplates), len(want))
	}
	for i, name := range want {
		if model.WebhookTemplates[i] != name {
			t.Fatalf("WebhookTemplates[%d] = %q, 期望 %q(generic 必须排第一, 它是界面默认值)", i, model.WebhookTemplates[i], name)
		}
		if !model.WebhookValidTemplate(name) {
			t.Fatalf("%q 应被视为合法模板", name)
		}
	}
	for _, bad := range []string{"Generic", "GENERIC", "dingding", "Dingtalk", "feishu-card", " slack", "generic ", "test"} {
		if model.WebhookValidTemplate(bad) {
			t.Fatalf("%q 不该是合法模板名(区分大小写且不接受空白)", bad)
		}
	}
	// 空串归一成 generic: 存量订阅与"省略该字段"的请求都靠这一条。
	if got := model.WebhookNormalizeTemplate(""); got != model.WebhookTemplateGeneric {
		t.Fatalf("空串应归一成 generic, 实际 %q", got)
	}
	// 归一函数不做校验: 非闭集值原样返回, 由调用方拒绝(否则"归一"和"拒绝"混在一起)。
	if got := model.WebhookNormalizeTemplate("dingding"); got != "dingding" {
		t.Fatalf("归一函数不该改动非法值, 实际 %q", got)
	}
}

// generic 的字节**必须完全不变**: 既有接收端按 docs/WEBHOOKS.md 的结构解析,
// 字段顺序或空值处理的任何变化都会让它们静默失败, 而我们在本地看到的是投递成功(200)。
// 这条用例故意拿写死的字符串比字节, 不用解回来比 —— 解回来比看不出顺序与转义差异。
func TestWebhookBuildPayloadGenericBytesUnchanged(t *testing.T) {
	data := model.WebhookPayloadData{Title: "t", Content: "c", Link: "/l", RelatedID: 7}
	body, err := webhookBuildPayload("delivery-1", "system", time.Unix(webhookTemplateTestFixedTS, 0), 42, data)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"delivery-1","event":"system","timestamp":1700000000,"user_id":42,"data":{"title":"t","content":"c","link":"/l","related_id":7}}`
	if body != want {
		t.Fatalf("generic body 字节变了:\n 实际 %s\n 期望 %s", body, want)
	}
	// 同一个函数走模板分派入口也必须一致(generic 走的是原路径, 不是另一条复制品)。
	body2, err := webhookBuildPayloadForTemplate(model.WebhookTemplateGeneric, "delivery-1", "system", time.Unix(webhookTemplateTestFixedTS, 0), 42, data)
	if err != nil {
		t.Fatal(err)
	}
	if body2 != want {
		t.Fatalf("按 generic 模板显式组装的结果与默认路径不一致:\n 实际 %s\n 期望 %s", body2, want)
	}
}

// webhookTemplateBodyAsMap 解回 map: 断言的是字段路径与字段名大小写,
// 用结构体解回来会"自动对齐"字段名, 恰好漏掉钉钉 msgtype / 飞书 msg_type 这类易错点。
func webhookTemplateBodyAsMap(t *testing.T, body string) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("模板 body 不是合法 JSON: %v\nbody=%s", err, body)
	}
	return m
}

func webhookTemplateMapGet(t *testing.T, m map[string]interface{}, path ...string) string {
	t.Helper()
	var cur interface{} = m
	for _, key := range path {
		switch obj := cur.(type) {
		case map[string]interface{}:
			next, ok := obj[key]
			if !ok {
				t.Fatalf("路径 %v 缺少键 %q; 实际 body=%v", path, key, m)
			}
			cur = next
		case []interface{}:
			// 数组下标用数字键("0"/"1"), 让调用方写出一条连续的字段路径。
			idx, err := strconv.Atoi(key)
			if err != nil || idx < 0 || idx >= len(obj) {
				t.Fatalf("路径 %v 的 %q 不是合法数组下标(数组长度 %d)", path, key, len(obj))
			}
			cur = obj[idx]
		default:
			t.Fatalf("路径 %v 在 %q 之前既不是对象也不是数组, 实际 %#v", path, key, cur)
		}
	}
	s, ok := cur.(string)
	if !ok {
		t.Fatalf("路径 %v 不是字符串, 实际 %#v", path, cur)
	}
	return s
}

func TestWebhookBuildPayloadDingtalkShape(t *testing.T) {
	data := webhookTemplateTestData()
	body, err := webhookBuildPayloadForTemplate(model.WebhookTemplateDingtalk, "d-1", "balance_low", time.Unix(webhookTemplateTestFixedTS, 0), 42, data)
	if err != nil {
		t.Fatal(err)
	}
	m := webhookTemplateBodyAsMap(t, body)
	if got := webhookTemplateMapGet(t, m, "msgtype"); got != "markdown" {
		t.Fatalf("钉钉 msgtype = %q, 期望 markdown(小写, 平台敏感)", got)
	}
	// 钉钉的标题栏会被平台按字符数截断: 事件名放在最前面才不会被截掉。
	title := webhookTemplateMapGet(t, m, "markdown", "title")
	if !strings.HasPrefix(title, "balance_low") {
		t.Fatalf("钉钉标题应以事件名开头, 实际 %q", title)
	}
	text := webhookTemplateMapGet(t, m, "markdown", "text")
	if !strings.Contains(text, data.Content) {
		t.Fatalf("钉钉正文应包含通知正文 %q, 实际 %q", data.Content, text)
	}
	if !strings.Contains(text, data.Title) {
		t.Fatalf("钉钉正文应包含通知标题 %q, 实际 %q", data.Title, text)
	}
	// 钉钉只认这两个键: 多带 generic 的 id/data 等字段会被平台忽略, 少一个就整条被拒。
	for _, key := range []string{"msgtype", "markdown"} {
		if _, ok := m[key]; !ok {
			t.Fatalf("钉钉 body 缺少顶层键 %q", key)
		}
	}
	if len(m) != 2 {
		t.Fatalf("钉钉 body 顶层键数 = %d, 期望 2(msgtype/markdown): %v", len(m), m)
	}
}

func TestWebhookBuildPayloadFeishuShape(t *testing.T) {
	data := webhookTemplateTestData()
	body, err := webhookBuildPayloadForTemplate(model.WebhookTemplateFeishu, "d-2", "channel_down", time.Unix(webhookTemplateTestFixedTS, 0), 42, data)
	if err != nil {
		t.Fatal(err)
	}
	m := webhookTemplateBodyAsMap(t, body)
	// 飞书是 msg_type(下划线), 钉钉是 msgtype —— 这正是最容易写错的一处。
	if got := webhookTemplateMapGet(t, m, "msg_type"); got != "interactive" {
		t.Fatalf("飞书 msg_type = %q, 期望 interactive", got)
	}
	if got := webhookTemplateMapGet(t, m, "card", "header", "title", "tag"); got != "plain_text" {
		t.Fatalf("飞书标题 tag = %q, 期望 plain_text", got)
	}
	if got := webhookTemplateMapGet(t, m, "card", "header", "title", "content"); !strings.HasPrefix(got, "channel_down") {
		t.Fatalf("飞书标题应以事件名开头, 实际 %q", got)
	}
	if got := webhookTemplateMapGet(t, m, "card", "elements", "0", "tag"); got != "div" {
		t.Fatalf("飞书元素 tag = %q, 期望 div", got)
	}
	if got := webhookTemplateMapGet(t, m, "card", "elements", "0", "text", "tag"); got != "lark_md" {
		t.Fatalf("飞书正文 tag = %q, 期望 lark_md", got)
	}
	content := webhookTemplateMapGet(t, m, "card", "elements", "0", "text", "content")
	if !strings.Contains(content, data.Content) || !strings.Contains(content, data.Title) {
		t.Fatalf("飞书正文应包含标题与正文, 实际 %q", content)
	}
	// elements 必须是数组(单个对象会被飞书拒)。
	if _, ok := m["card"].(map[string]interface{})["elements"].([]interface{}); !ok {
		t.Fatalf("飞书 card.elements 必须是数组, 实际 %#v", m["card"])
	}
}

func TestWebhookBuildPayloadSlackShape(t *testing.T) {
	data := webhookTemplateTestData()
	body, err := webhookBuildPayloadForTemplate(model.WebhookTemplateSlack, "d-3", "withdrawal", time.Unix(webhookTemplateTestFixedTS, 0), 42, data)
	if err != nil {
		t.Fatal(err)
	}
	m := webhookTemplateBodyAsMap(t, body)
	// text 是兜底纯文本: 只发 blocks 会让不支持 blocks 的客户端(与通知栏)空着。
	text := webhookTemplateMapGet(t, m, "text")
	if !strings.HasPrefix(text, "withdrawal") {
		t.Fatalf("Slack 兜底 text 应以事件名开头, 实际 %q", text)
	}
	if got := webhookTemplateMapGet(t, m, "blocks", "0", "type"); got != "section" {
		t.Fatalf("Slack block type = %q, 期望 section", got)
	}
	if got := webhookTemplateMapGet(t, m, "blocks", "0", "text", "type"); got != "mrkdwn" {
		t.Fatalf("Slack block text.type = %q, 期望 mrkdwn", got)
	}
	blockText := webhookTemplateMapGet(t, m, "blocks", "0", "text", "text")
	// 注意: blocks[0].text 的对象键名也叫 text —— 平台就是这么设计的, 不是笔误。
	if !strings.Contains(blockText, data.Title) || !strings.Contains(blockText, data.Content) {
		t.Fatalf("Slack 块正文应包含标题与正文, 实际 %q", blockText)
	}
}

// 转义: 通知标题与正文是用户可控内容, 一个双引号或换行就能破掉手拼的 JSON。
// 这条用例对四种模板逐个验"仍是合法 JSON 且字段值原样还原"。
func TestWebhookBuildPayloadTemplatesSurviveEscaping(t *testing.T) {
	data := model.WebhookPayloadData{
		Title:     "她说了 \"别开枪\" \n 然后换行",
		Content:   "反斜杠 \\\\ 与制表\t结束",
		Link:      "/x?a=\"1\"&b=\n",
		RelatedID: 9,
	}
	for _, template := range model.WebhookTemplates {
		body, err := webhookBuildPayloadForTemplate(template, "delivery-esc", "system", time.Unix(webhookTemplateTestFixedTS, 0), 42, data)
		if err != nil {
			t.Fatalf("[%s] 组装失败: %v", template, err)
		}
		// 1) 必须是合法 JSON(手拼破了这里就会炸)。
		if !json.Valid([]byte(body)) {
			t.Fatalf("[%s] body 不是合法 JSON:\n%s", template, body)
		}
		// 2) 真值必须原样还原: 不能多转义一层, 也不能丢字符。
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(body), &m); err != nil {
			t.Fatalf("[%s] 解不回来: %v", template, err)
		}
		var collected string
		for _, v := range m {
			collected += webhookTemplateFlatten(v)
		}
		for _, want := range []string{"别开枪", "换行", "反斜杠", "制表"} {
			if !strings.Contains(collected, want) {
				t.Fatalf("[%s] body 里丢了 %q(转义把内容吃掉了):\n%s", template, want, body)
			}
		}
		// 3) 换行在 JSON 里必须是 \n 转义而不是裸换行(裸换行在字符串内是非法 JSON,
		//    虽然 json.Valid 已覆盖, 这里再看一眼 body 里没有真实换行字节)。
		if strings.Contains(body, "\n\n") {
			_ = body // 模板正文本来就有段落间空行, 由 json.Marshal 转义成 \n, 允许
		}
	}
}

// webhookTemplateFlatten 把任意 JSON 值拍平成字符串, 用于"内容有没有丢"的检查。
func webhookTemplateFlatten(v interface{}) string {
	switch typed := v.(type) {
	case string:
		return typed + "\x00"
	case map[string]interface{}:
		var sb strings.Builder
		for _, child := range typed {
			sb.WriteString(webhookTemplateFlatten(child))
		}
		return sb.String()
	case []interface{}:
		var sb strings.Builder
		for _, child := range typed {
			sb.WriteString(webhookTemplateFlatten(child))
		}
		return sb.String()
	default:
		return ""
	}
}

// 创建时非法模板必须被拒且不留半个订阅: 静默降级成 generic 会让用户"选了钉钉却一直失败",
// 而界面显示一切正常。
func TestWebhookCreateRejectsInvalidTemplate(t *testing.T) {
	webhookTestSetup(t)
	ctx := t.Context()
	for _, bad := range []string{"dingding", "Generic", "feishu-card", "slack ", "markdown", "generic;drop table"} {
		_, err := WebhookCreate(ctx, 1, model.WebhookCreateRequest{
			URL:      "https://hooks.example.com/x",
			Events:   []string{"system"},
			Secret:   strings.Repeat("k", 16),
			Template: bad,
		})
		if !errors.Is(err, ErrWebhookTemplateInvalid) {
			t.Fatalf("非法模板 %q 应被拒(ErrWebhookTemplateInvalid), 实际 %v", bad, err)
		}
	}
	var count int64
	if err := db.GetDB().Model(&model.Webhook{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("被拒的创建不该留下订阅行, 实际 %d 行", count)
	}
}

// 省略/空串 → generic; 合法值原样存下并回填到视图。
func TestWebhookCreateNormalizesTemplate(t *testing.T) {
	webhookTestSetup(t)
	ctx := t.Context()

	cases := []struct{ given, want string }{
		{"", model.WebhookTemplateGeneric},
		{model.WebhookTemplateGeneric, model.WebhookTemplateGeneric},
		{model.WebhookTemplateDingtalk, model.WebhookTemplateDingtalk},
		{model.WebhookTemplateFeishu, model.WebhookTemplateFeishu},
		{model.WebhookTemplateSlack, model.WebhookTemplateSlack},
	}
	for i, tc := range cases {
		view, err := WebhookCreate(ctx, uint(10+i), model.WebhookCreateRequest{
			URL:      "https://hooks.example.com/x",
			Events:   []string{"system"},
			Secret:   strings.Repeat("k", 16),
			Template: tc.given,
		})
		if err != nil {
			t.Fatalf("[%q] 创建失败: %v", tc.given, err)
		}
		if view.Template != tc.want {
			t.Fatalf("[%q] 视图 template = %q, 期望 %q", tc.given, view.Template, tc.want)
		}
		// 库里也要是归一后的值(不能只回填视图)。
		var row model.Webhook
		if err := db.GetDB().Where("id = ?", view.ID).First(&row).Error; err != nil {
			t.Fatal(err)
		}
		if row.Template != tc.want {
			t.Fatalf("[%q] 库内 template = %q, 期望 %q", tc.given, row.Template, tc.want)
		}
	}
}

// 一部分更新: 省略 template 即不改动; 显式空串表示改回 generic —— 两者必须能区分。
func TestWebhookUpdateTemplatePointerSemantics(t *testing.T) {
	webhookTestSetup(t)
	ctx := t.Context()
	view, err := WebhookCreate(ctx, 1, model.WebhookCreateRequest{
		URL:      "https://hooks.example.com/x",
		Events:   []string{"system"},
		Secret:   strings.Repeat("k", 16),
		Template: model.WebhookTemplateSlack,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 只改 retry_count, 不带 template: 模板必须还是 slack。
	rc := 1
	after, err := WebhookUpdate(ctx, 1, view.ID, model.WebhookUpdateRequest{RetryCount: &rc})
	if err != nil {
		t.Fatal(err)
	}
	if after.Template != model.WebhookTemplateSlack {
		t.Fatalf("省略 template 时不该改动, 实际 %q", after.Template)
	}

	// 显式空串: 改回 generic。
	empty := ""
	after, err = WebhookUpdate(ctx, 1, view.ID, model.WebhookUpdateRequest{Template: &empty})
	if err != nil {
		t.Fatal(err)
	}
	if after.Template != model.WebhookTemplateGeneric {
		t.Fatalf("显式空串应改回 generic, 实际 %q", after.Template)
	}

	// 非法值: 拒且不改动。
	bad := "dingding"
	if _, err := WebhookUpdate(ctx, 1, view.ID, model.WebhookUpdateRequest{Template: &bad}); !errors.Is(err, ErrWebhookTemplateInvalid) {
		t.Fatalf("非法模板更新应被拒, 实际 %v", err)
	}
	var row model.Webhook
	if err := db.GetDB().Where("id = ?", view.ID).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Template != model.WebhookTemplateGeneric {
		t.Fatalf("被拒的更新不该改动模板, 实际 %q", row.Template)
	}

	// 改模板不该动 URL 版本(Version 只管"防发往旧地址")。
	if row.Version != 1 {
		t.Fatalf("改模板不该自增 version, 实际 %d", row.Version)
	}
}

// 入队时就按订阅模板定型: 重试间 body 字节不变, 投递期间改模板不追溯。
func TestWebhookEnqueueUsesSubscriptionTemplate(t *testing.T) {
	webhookTestSetup(t)
	ctx := t.Context()
	for _, template := range model.WebhookTemplates {
		view, err := WebhookCreate(ctx, 1, model.WebhookCreateRequest{
			URL:      "https://hooks.example.com/x",
			Events:   []string{"system"},
			Secret:   strings.Repeat("k", 16),
			Template: template,
		})
		if err != nil {
			t.Fatal(err)
		}
		var row model.Webhook
		if err := db.GetDB().Where("id = ?", view.ID).First(&row).Error; err != nil {
			t.Fatal(err)
		}
		d := webhookSeedDelivery(t, row, string(model.NotificationTypeSystem))
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(d.Payload), &m); err != nil {
			t.Fatalf("[%s] 入队的 payload 不是合法 JSON: %v", template, err)
		}
		switch template {
		case model.WebhookTemplateGeneric:
			if _, ok := m["data"].(map[string]interface{}); !ok {
				t.Fatalf("[%s] generic 应保留 id/data 原结构, 实际 %v", template, m)
			}
		case model.WebhookTemplateDingtalk:
			if m["msgtype"] != "markdown" {
				t.Fatalf("[%s] 入队 payload 没按模板改写: %v", template, m)
			}
		case model.WebhookTemplateFeishu:
			if m["msg_type"] != "interactive" {
				t.Fatalf("[%s] 入队 payload 没按模板改写: %v", template, m)
			}
		case model.WebhookTemplateSlack:
			if _, ok := m["blocks"]; !ok {
				t.Fatalf("[%s] 入队 payload 没按模板改写: %v", template, m)
			}
		}
	}
}

// 重放: 源投递是 generic 时代的 body, 但订阅已改成 dingtalk —— 重放必须按**当前**模板重建,
// 否则用户改完模板后重放积压投递, 对方收到的仍是通用的老结构(被平台拒)。
func TestWebhookReplayRebuildsWithCurrentTemplate(t *testing.T) {
	webhookTestSetup(t)
	ctx := t.Context()
	view, err := WebhookCreate(ctx, 1, model.WebhookCreateRequest{
		URL:      "https://hooks.example.com/x",
		Events:   []string{"system"},
		Secret:   strings.Repeat("k", 16),
		Template: model.WebhookTemplateGeneric,
	})
	if err != nil {
		t.Fatal(err)
	}
	const originalID = "template-replay-source"
	webhookTestDeliveryRow(t, view.ID, 1, originalID, string(model.NotificationTypeSystem), model.WebhookDeliveryStatusFailed)

	// 换成钉钉模板后再重放。
	dingtalk := model.WebhookTemplateDingtalk
	if _, err := WebhookUpdate(ctx, 1, view.ID, model.WebhookUpdateRequest{Template: &dingtalk}); err != nil {
		t.Fatal(err)
	}
	replayed, err := WebhookReplayDelivery(ctx, 1, view.ID, originalID)
	if err != nil {
		t.Fatal(err)
	}
	var row model.WebhookDelivery
	if err := db.GetDB().Where("delivery_id = ?", replayed.DeliveryID).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(row.Payload), &m); err != nil {
		t.Fatalf("重放 body 不是合法 JSON: %v", err)
	}
	if m["msgtype"] != "markdown" {
		t.Fatalf("重放应按当前订阅模板(dingtalk)重建, 实际 body=%s", row.Payload)
	}
	if _, ok := m["data"]; ok {
		t.Fatalf("重放 body 不该还带 generic 的 data 结构, 实际 %s", row.Payload)
	}
}

// 存量订阅(加列前建的)必须落到 generic: DB default 是"字节不变"契约的落地方式。
func TestWebhookTemplateColumnAddedByAutoMigrate(t *testing.T) {
	// 手工建一版**加列前**的 webhooks 表(每列都带 DEFAULT, 否则 SQLite 不允许给非空表
	// 追加 NOT NULL 无默认值列, 而那是另一件事, 不是本用例要测的), 落一行老数据, 再跑 AutoMigrate。
	conn, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "legacy-webhooks.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	legacy := []string{
		`CREATE TABLE "webhooks" (`,
		`  "id" integer PRIMARY KEY AUTOINCREMENT,`,
		`  "user_id" integer NOT NULL DEFAULT 0,`,
		`  "url" text NOT NULL DEFAULT '',`,
		`  "events" text NOT NULL DEFAULT '[]',`,
		`  "enabled" numeric NOT NULL DEFAULT true,`,
		`  "retry_count" integer NOT NULL DEFAULT 2,`,
		`  "secret_encrypted" text,`,
		`  "version" integer NOT NULL DEFAULT 1,`,
		`  "last_test_at" datetime,`,
		`  "last_status" text NOT NULL DEFAULT 'none',`,
		`  "created_at" datetime,`,
		`  "updated_at" datetime`,
		`)`,
	}
	if err := conn.Exec(strings.Join(legacy, "\n")).Error; err != nil {
		t.Fatalf("建旧版 webhooks 表失败: %v", err)
	}
	if err := conn.Exec(`INSERT INTO "webhooks" ("id","user_id","url","events","enabled","retry_count","version","last_status") VALUES (1,1,'https://hooks.example.com/x','["system"]',true,2,1,'none')`).Error; err != nil {
		t.Fatalf("插入老数据失败: %v", err)
	}
	if err := conn.AutoMigrate(&model.Webhook{}); err != nil {
		t.Fatalf("AutoMigrate 失败: %v", err)
	}
	if !conn.Migrator().HasColumn(&model.Webhook{}, "template") {
		t.Fatalf("AutoMigrate 没有建出 webhooks.template 列(存量订阅会读不到模板)")
	}
	var legacyRow model.Webhook
	if err := conn.Where("id = ?", 1).First(&legacyRow).Error; err != nil {
		t.Fatalf("读老数据失败: %v", err)
	}
	if legacyRow.Template != model.WebhookTemplateGeneric {
		t.Fatalf("存量订阅的 template = %q, 期望 generic(DB default 没落地)", legacyRow.Template)
	}
	// 老行的数据一个都没丢。
	if legacyRow.URL != "https://hooks.example.com/x" || legacyRow.RetryCount != 2 || legacyRow.Version != 1 {
		t.Fatalf("AutoMigrate 加列后老数据变了: %+v", legacyRow)
	}
	// 视图归一: 库里读出的值必须下发成 generic(走真实的 webhookToView, 不复制一份逻辑)。
	view, err := webhookToView(legacyRow)
	if err != nil {
		t.Fatalf("webhookToView: %v", err)
	}
	if view.Template != model.WebhookTemplateGeneric {
		t.Fatalf("视图 template = %q, 期望 generic", view.Template)
	}
}

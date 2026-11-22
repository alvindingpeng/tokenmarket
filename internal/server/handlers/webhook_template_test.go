package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// 模板走 HTTP 全链路: 创建时带 template → 视图回填 → 列表带上 → 部分更新省略不改 →
// 非法值 400 且 error_code 是**模板专用**的那个(前端要按它把错误标到对应表单字段上,
// 混进通用 invalid_request 用户看不出是哪项错了)。
func TestWebhookHandlersTemplateCreateAndView(t *testing.T) {
	webhookHandlersTestSetup(t)
	r, _ := webhookTestEngine(t)

	body := `{"url":"https://hooks.example.com/x","events":["system"],"secret":"secret-key-1234567890","template":"dingtalk"}`
	code, resp := webhookRequest(t, r, http.MethodPost, "/api/v1/webhook", body)
	if code != http.StatusOK {
		t.Fatalf("create with template = %d %s", code, resp)
	}
	var created struct {
		Data struct {
			ID       uint64 `json:"id"`
			Template string `json:"template"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(resp), &created); err != nil {
		t.Fatal(err)
	}
	if created.Data.Template != model.WebhookTemplateDingtalk {
		t.Fatalf("创建后视图 template = %q, 期望 dingtalk; body=%s", created.Data.Template, resp)
	}

	// 列表也带上 template(前端列表页要显示模板名, 少这个字段就只能逐条再查一遍)。
	code, resp = webhookRequest(t, r, http.MethodGet, "/api/v1/webhook/list", "")
	if code != http.StatusOK {
		t.Fatalf("list = %d %s", code, resp)
	}
	if !strings.Contains(resp, `"template":"dingtalk"`) {
		t.Fatalf("列表项缺 template: %s", resp)
	}
}

// 省略 template → generic(存量调用方与老前端不传这个字段也必须能用)。
func TestWebhookHandlersTemplateDefaultsToGeneric(t *testing.T) {
	webhookHandlersTestSetup(t)
	r, _ := webhookTestEngine(t)

	code, resp := webhookRequest(t, r, http.MethodPost, "/api/v1/webhook", webhookValidCreateBody)
	if code != http.StatusOK {
		t.Fatalf("create without template = %d %s", code, resp)
	}
	if !strings.Contains(resp, `"template":"generic"`) {
		t.Fatalf("省略 template 应回填 generic: %s", resp)
	}

	// 显式空串也一样(前端下拉的"未选择"就是空串)。
	code, resp = webhookRequest(t, r, http.MethodPost, "/api/v1/webhook",
		`{"url":"https://hooks.example.com/y","events":["system"],"secret":"secret-key-1234567890","template":""}`)
	if code != http.StatusOK {
		t.Fatalf("create with empty template = %d %s", code, resp)
	}
	if !strings.Contains(resp, `"template":"generic"`) {
		t.Fatalf("空串 template 应回填 generic: %s", resp)
	}
}

// 非法模板: 400 + 专用 error_code, 且订阅没有被建出来(不能"拒了但库里有一行")。
func TestWebhookHandlersTemplateInvalidCode(t *testing.T) {
	webhookHandlersTestSetup(t)
	r, _ := webhookTestEngine(t)

	for _, bad := range []string{"dingding", "Generic", "feishu-card", "markdown"} {
		body := `{"url":"https://hooks.example.com/x","events":["system"],"secret":"secret-key-1234567890","template":"` + bad + `"}`
		code, resp := webhookRequest(t, r, http.MethodPost, "/api/v1/webhook", body)
		if code != http.StatusBadRequest {
			t.Fatalf("[%s] create = %d %s, 期望 400", bad, code, resp)
		}
		if !strings.Contains(resp, codeInvalidWebhookTemplate) {
			t.Fatalf("[%s] 错误码应为 %q, 实际 %s", bad, codeInvalidWebhookTemplate, resp)
		}
		// 错误文案要能告诉用户合法取值, 否则他只知道"错了"却不知道填什么。
		if !strings.Contains(resp, "generic") || !strings.Contains(resp, "dingtalk") {
			t.Fatalf("[%s] 错误文案应列出合法取值: %s", bad, resp)
		}
	}
	code, resp := webhookRequest(t, r, http.MethodGet, "/api/v1/webhook/list", "")
	if code != http.StatusOK || !strings.Contains(resp, `"items":[]`) {
		t.Fatalf("被拒的创建不该留下订阅: %d %s", code, resp)
	}
}

// 更新: 省略 template 不改动, 非法值 400 且不改动。
func TestWebhookHandlersTemplateUpdateSemantics(t *testing.T) {
	webhookHandlersTestSetup(t)
	r, _ := webhookTestEngine(t)

	body := `{"url":"https://hooks.example.com/x","events":["system"],"secret":"secret-key-1234567890","template":"slack"}`
	code, resp := webhookRequest(t, r, http.MethodPost, "/api/v1/webhook", body)
	if code != http.StatusOK {
		t.Fatalf("create = %d %s", code, resp)
	}
	var created struct {
		Data struct {
			ID uint64 `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(resp), &created); err != nil {
		t.Fatal(err)
	}
	id := uint64String(created.Data.ID)

	// 只改 enabled, 不带 template: slack 不变。
	code, resp = webhookRequest(t, r, http.MethodPut, "/api/v1/webhook/"+id, `{"enabled":false}`)
	if code != http.StatusOK {
		t.Fatalf("update enabled = %d %s", code, resp)
	}
	if !strings.Contains(resp, `"template":"slack"`) {
		t.Fatalf("省略 template 时不该改动: %s", resp)
	}

	// 显式空串: 改回 generic。
	code, resp = webhookRequest(t, r, http.MethodPut, "/api/v1/webhook/"+id, `{"template":""}`)
	if code != http.StatusOK {
		t.Fatalf("update template empty = %d %s", code, resp)
	}
	if !strings.Contains(resp, `"template":"generic"`) {
		t.Fatalf("显式空串应改回 generic: %s", resp)
	}

	// 非法值: 400 + 专用错误码, 且仍是 generic(没被写进去)。
	code, resp = webhookRequest(t, r, http.MethodPut, "/api/v1/webhook/"+id, `{"template":"dingding"}`)
	if code != http.StatusBadRequest || !strings.Contains(resp, codeInvalidWebhookTemplate) {
		t.Fatalf("update invalid template = %d %s, 期望 400 %s", code, resp, codeInvalidWebhookTemplate)
	}
	code, resp = webhookRequest(t, r, http.MethodPut, "/api/v1/webhook/"+id, `{"template":"feishu"}`)
	if code != http.StatusOK {
		t.Fatalf("update feishu = %d %s", code, resp)
	}
}

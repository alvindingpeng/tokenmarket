package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
)

// createChannelForAccountTest 走真实创建接口建渠道, 返回 ID。
//
// 不能直接插行: /channel/account 的处理器读的是 op.ChannelGet, 它只查缓存。
// 绕过创建接口插出来的行不在缓存里, 处理器第一步就是"渠道不存在", 测不到平台校验。
func createChannelForAccountTest(t *testing.T, engine *gin.Engine, token, body string) int {
	t.Helper()
	w := ticketAPI(t, engine, http.MethodPost, "/api/v1/channel/create", token, body)
	if w.Code != http.StatusOK {
		t.Fatalf("create channel: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.Data.ID == 0 {
		t.Fatalf("没有拿到渠道 id: %s", w.Body.String())
	}
	return created.Data.ID
}

func countKeysInChannel(t *testing.T, channelID int) int64 {
	t.Helper()
	var count int64
	if err := db.GetDB().Model(&model.ChannelKey{}).Where("channel_id = ?", channelID).Count(&count).Error; err != nil {
		t.Fatalf("count keys: %v", err)
	}
	return count
}

// 订阅渠道 + 不匹配平台的凭据必须当场 400, 且错误里两个平台名都在。
//
// 走真实 HTTP 路径而不是只测 op: 400 与 500 的分流发生在处理器(它看 errors.Is(ErrInvalidInput)),
// 而"该报 400 却报了 500"正是这类校验最容易出的错 —— 前者让使用者去改平台, 后者让他去查服务。
func TestImportAccountRejectsPlatformMismatchOnSubscriptionChannel(t *testing.T) {
	_, _, adminToken, _, _, _, _, _ := ticketAPIEnv(t)
	engine := ticketEngine(t)

	channelID := createChannelForAccountTest(t, engine, adminToken,
		`{"name":"sub-for-import","dialect":"subscription","sub_platform":"gemini","enabled":true}`)

	payload := fmt.Sprintf(
		`{"channel_id":%d,"platform":"codex","name":"codex-into-sub","raw":"{\"access_token\":\"at\",\"refresh_token\":\"rt\"}"}`,
		channelID)
	w := ticketAPI(t, engine, http.MethodPost, "/api/v1/channel/account/import", adminToken, payload)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("平台与方言不匹配应回 400(输入错误), 实际 %d %s", w.Code, w.Body.String())
	}
	message := w.Body.String()
	if !strings.Contains(message, model.AccountPlatformAntigravity) {
		t.Fatalf("错误没说该用哪个平台: %s", message)
	}
	if !strings.Contains(message, model.AccountPlatformCodex) {
		t.Fatalf("错误没说当前提交的是哪个平台: %s", message)
	}
	if got := countKeysInChannel(t, channelID); got != 0 {
		t.Fatalf("被拒的导入不应落库, 实际写入 %d 条", got)
	}
}

// 批量接口同样逐条报原因: 一批里每条都要带自己的错误, 不能只给一句"部分失败"。
func TestImportAccountBatchRejectsMismatchPerItem(t *testing.T) {
	_, _, adminToken, _, _, _, _, _ := ticketAPIEnv(t)
	engine := ticketEngine(t)

	channelID := createChannelForAccountTest(t, engine, adminToken,
		`{"name":"sub-for-batch","dialect":"subscription","sub_platform":"gemini","enabled":true}`)

	payload := fmt.Sprintf(
		`{"channel_id":%d,"platform":"codex","items":[{"name":"b1","raw":"{\"access_token\":\"at\",\"refresh_token\":\"rt\"}"},{"name":"b2","raw":"{\"access_token\":\"at\",\"refresh_token\":\"rt\"}"}]}`,
		channelID)
	w := ticketAPI(t, engine, http.MethodPost, "/api/v1/channel/account/import-batch", adminToken, payload)
	if w.Code != http.StatusOK {
		t.Fatalf("批量导入应回 200(逐条结果里带失败), 实际 %d %s", w.Code, w.Body.String())
	}

	var body struct {
		Data struct {
			Total     int `json:"total"`
			Succeeded int `json:"succeeded"`
			Failed    int `json:"failed"`
			Results   []struct {
				Name  string `json:"name"`
				OK    bool   `json:"ok"`
				Error string `json:"error"`
			} `json:"results"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode batch response: %v (%s)", err, w.Body.String())
	}
	if body.Data.Total != 2 || body.Data.Succeeded != 0 || body.Data.Failed != 2 {
		t.Fatalf("应得 total=2 succeeded=0 failed=2, 实际 %+v", body.Data)
	}
	for _, result := range body.Data.Results {
		if result.OK {
			t.Fatalf("%s 应被拒: %+v", result.Name, result)
		}
		if !strings.Contains(result.Error, model.AccountPlatformAntigravity) || !strings.Contains(result.Error, model.AccountPlatformCodex) {
			t.Fatalf("%s 的错误应同时点名两个平台: %q", result.Name, result.Error)
		}
	}
	if got := countKeysInChannel(t, channelID); got != 0 {
		t.Fatalf("被拒的批量导入不应落库, 实际写入 %d 条", got)
	}
}

// 抗过度拦截: 普通渠道 + antigravity 凭据必须能导入成功(反方向不能挡)。
//
// 这条走完整 HTTP 链路, 因此会真的触发导入后的首刷 —— 用本地假令牌端点接住,
// 既证明"平台校验没连合法的 antigravity 凭据一起挡掉", 也证明这条凭据确实能进入可用状态。
func TestImportAccountAllowsAntigravityOnGenericChannel(t *testing.T) {
	_, _, adminToken, _, _, _, _, _ := ticketAPIEnv(t)
	engine := ticketEngine(t)

	if err := op.SettingUpsert(model.SettingKeyAllowPrivateUpstream, "true"); err != nil {
		t.Fatalf("allow private upstream: %v", err)
	}
	t.Setenv("SWITCHYARD_ANTIGRAVITY_OAUTH_CLIENT_ID", "test-ag-id")
	t.Setenv("SWITCHYARD_ANTIGRAVITY_OAUTH_CLIENT_SECRET", "test-ag-secret")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "new-at",
			"refresh_token": "new-rt",
			"expires_in":    float64(3600),
		})
	}))
	t.Cleanup(server.Close)
	model.AccountOAuthTokenURLOverrideSet(model.AccountPlatformAntigravity, server.URL)
	t.Cleanup(func() { model.AccountOAuthTokenURLOverrideSet(model.AccountPlatformAntigravity, "") })

	channelID := createChannelForAccountTest(t, engine, adminToken,
		`{"name":"generic-with-antigravity","dialect":"generic","base_url":"https://generic.example.com","enabled":true}`)

	payload := fmt.Sprintf(
		`{"channel_id":%d,"platform":"antigravity","name":"anti-generic","raw":"{\"access_token\":\"at\",\"refresh_token\":\"rt\"}"}`,
		channelID)
	w := ticketAPI(t, engine, http.MethodPost, "/api/v1/channel/account/import", adminToken, payload)
	if w.Code != http.StatusOK {
		t.Fatalf("普通渠道应接受 antigravity 凭据(反方向不该挡), 实际 %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Data struct {
			Platform string `json:"platform"`
			Kind     string `json:"kind"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	if body.Data.Platform != model.AccountPlatformAntigravity {
		t.Fatalf("落库平台 = %q, want antigravity", body.Data.Platform)
	}
}

// 网页登录路径(第三个入口)同样要拦: 平台与渠道方言不符时, 必须在**换令牌之前**就拒绝 ——
// 否则会白白消耗一次授权码, 用户还得重新走一遍登录。
//
// 这条用例只用伪造的 code: 校验发生在 Exchange 之前, 所以不需要真的打上游,
// 也不依赖网络 —— 这正是把校验提前的另一个好处(可测)。
func TestSignInCompleteRejectsMismatchBeforeExchange(t *testing.T) {
	_, _, adminToken, _, _, _, _, _ := ticketAPIEnv(t)
	engine := ticketEngine(t)

	channelID := createChannelForAccountTest(t, engine, adminToken,
		`{"name":"sub-for-signin","dialect":"subscription","sub_platform":"gemini","enabled":true}`)

	begin := ticketAPI(t, engine, http.MethodPost, "/api/v1/channel/account/signin/begin", adminToken,
		`{"platform":"claude"}`)
	if begin.Code != http.StatusOK {
		t.Fatalf("begin signin: %d %s", begin.Code, begin.Body.String())
	}
	var beginBody struct {
		Data struct {
			State string `json:"state"`
		} `json:"data"`
	}
	if err := json.Unmarshal(begin.Body.Bytes(), &beginBody); err != nil {
		t.Fatalf("decode begin: %v", err)
	}
	if beginBody.Data.State == "" {
		t.Fatalf("begin 没有返回 state: %s", begin.Body.String())
	}

	payload := fmt.Sprintf(
		`{"platform":"claude","state":%q,"pasted":"fake-authorization-code","channel_id":%d,"name":"claude-signin-into-sub"}`,
		beginBody.Data.State, channelID)
	w := ticketAPI(t, engine, http.MethodPost, "/api/v1/channel/account/signin/complete", adminToken, payload)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("登录导入到订阅渠道应回 400, 实际 %d %s", w.Code, w.Body.String())
	}
	message := w.Body.String()
	if !strings.Contains(message, model.AccountPlatformAntigravity) || !strings.Contains(message, model.AccountPlatformClaude) {
		t.Fatalf("错误应同时点名 antigravity 与 claude: %s", message)
	}
	if got := countKeysInChannel(t, channelID); got != 0 {
		t.Fatalf("被拒的登录导入不应落库, 实际写入 %d 条", got)
	}
}

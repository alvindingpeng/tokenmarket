package handlers

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/gin-gonic/gin"
)

// homePayload 只声明用例要断言的字段: 多余的字段由"不得出现"的断言负责。
type homePayload struct {
	Total struct {
		InputToken     int64   `json:"input_token"`
		OutputToken    int64   `json:"output_token"`
		RequestSuccess int64   `json:"request_success"`
		InputCost      float64 `json:"input_cost"`
	} `json:"total"`
	Daily struct {
		MaxRequestCount int64 `json:"max_request_count"`
		Items           []struct {
			Date           string `json:"date"`
			RequestSuccess int64  `json:"request_success"`
		} `json:"items"`
	} `json:"daily"`
	Hourly []struct {
		Hour           int   `json:"hour"`
		RequestSuccess int64 `json:"request_success"`
	} `json:"hourly"`
	Channels []struct {
		ID           int     `json:"id"`
		Name         string  `json:"name"`
		ChannelName  string  `json:"channelName"`
		TotalCost    float64 `json:"total_cost"`
		TotalToken   int64   `json:"total_token"`
		RequestCount int64   `json:"request_count"`
		Models       []struct {
			ID            int     `json:"id"`
			Name          string  `json:"name"`
			ChannelName   string  `json:"channelName"`
			TotalCost     float64 `json:"total_cost"`
			TotalToken    int64   `json:"total_token"`
			RequestCount  int64   `json:"request_count"`
			RequestFailed int64   `json:"request_failed"`
		} `json:"models"`
	} `json:"channels"`
}

// homeFetch 走真实路由表请求首页聚合接口并解开 data; 非 200 直接失败。
func homeFetch(t *testing.T, engine http.Handler, token string) (homePayload, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "/api/v1/stats/home", nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.AddCookie(&http.Cookie{Name: "auth", Value: token})
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("home: %d %s", rec.Code, rec.Body.String())
	}
	var raw struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("parse %s: %v", rec.Body.String(), err)
	}
	if raw.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", raw.Code, rec.Body.String())
	}
	body, err := json.Marshal(raw.Data)
	if err != nil {
		t.Fatal(err)
	}
	var payload homePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("parse data %s: %v", rec.Body.String(), err)
	}
	return payload, raw.Data
}

// TestStatsHomeRequiresAuth 匿名请求与同组其他 /stats/* 一样按 401 拒绝。
func TestStatsHomeRequiresAuth(t *testing.T) {
	ticketAPIEnv(t)
	engine := ticketEngine(t)
	if w := ticketAPI(t, engine, http.MethodGet, "/api/v1/stats/home", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d %s", w.Code, w.Body.String())
	}
}

// TestStatsHomeUserScope 普通用户拿到本人的计费口径, 且没有渠道榜单(渠道视图对他们不开)。
func TestStatsHomeUserScope(t *testing.T) {
	userToken, otherToken, _, _, user, _, _, _ := ticketAPIEnv(t)
	engine := ticketEngine(t)

	seedBillableFixtures(t, user.ID, time.Now())

	payload, raw := homeFetch(t, engine, userToken)
	// 口径必须与 /stats/total 的计费聚合一致: 1000 = 净读500 + 缓存读300 + 缓存写200。
	if payload.Total.InputToken != 1000 || payload.Total.OutputToken != 500 || payload.Total.RequestSuccess != 1 {
		t.Fatalf("user total: got %+v", payload.Total)
	}
	if !jsonArrayEmpty(raw["channels"]) {
		t.Fatalf("user channels must be empty array: %v", raw["channels"])
	}
	// 日曲线与小时曲线都要有内容: 前者驱动热力图, 后者驱动当日趋势。
	if len(payload.Daily.Items) == 0 {
		t.Fatalf("user daily items empty: %v", raw["daily"])
	}
	if len(payload.Hourly) == 0 {
		t.Fatalf("user hourly empty: %v", raw["hourly"])
	}
	if payload.Daily.MaxRequestCount < 1 {
		t.Fatalf("max_request_count not derived: %+v", payload.Daily)
	}

	// 他人看不到这位用户的任何数据。
	other, _ := homeFetch(t, engine, otherToken)
	if other.Total.InputToken != 0 || other.Total.RequestSuccess != 0 {
		t.Fatalf("other user sees foreign stats: %+v", other.Total)
	}
}

// TestStatsHomeAdminRankingIsLean 管理员拿到全站统计与裁剪后的榜单:
// 榜单只带名称与五个合计项, 不含渠道配置, 也不含榜单不消费的原始累计字段。
func TestStatsHomeAdminRankingIsLean(t *testing.T) {
	_, _, adminToken, _, _, _, _, _ := ticketAPIEnv(t)
	engine := ticketEngine(t)

	channel := model.Channel{
		Shared:    true,
		ShareCode: "1001",
		ChannelConfig: model.ChannelConfig{
			Name:    "home-rank-channel",
			BaseURL: "https://upstream.example",
			Enabled: true,
		},
		StatsMetrics: model.StatsMetrics{InputToken: 30, OutputToken: 70, InputCost: 0.5, OutputCost: 0.25, RequestSuccess: 3, RequestFailed: 1},
	}
	if err := db.GetDB().Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	channelModel := model.ChannelModel{
		ChannelID:    channel.ID,
		Name:         "home-rank-model",
		Listed:       true,
		StatsMetrics: model.StatsMetrics{InputToken: 10, OutputToken: 20, InputCost: 0.2, OutputCost: 0.1, RequestSuccess: 2, RequestFailed: 1},
	}
	if err := db.GetDB().Create(&channelModel).Error; err != nil {
		t.Fatalf("create channel model: %v", err)
	}
	// 榜单读进程内缓存, 与 /channel/stats 用的是同一份来源, 故先把缓存与库对齐。
	if err := op.InitCache(); err != nil {
		t.Fatalf("init cache: %v", err)
	}

	payload, raw := homeFetch(t, engine, adminToken)
	if len(payload.Channels) != 1 {
		t.Fatalf("admin channels: %v", raw["channels"])
	}
	row := payload.Channels[0]
	if row.ID != channel.ID || row.Name != "#1001" || row.ChannelName != "home-rank-channel" {
		t.Fatalf("channel identity: %+v", row)
	}
	// 合计项在后端算好: 金额 = 输入 + 输出, 词元 = 输入 + 输出, 请求数 = 成功 + 失败。
	if math.Abs(row.TotalCost-0.75) > 1e-9 || row.TotalToken != 100 || row.RequestCount != 4 {
		t.Fatalf("channel sums: %+v", row)
	}
	if len(row.Models) != 1 {
		t.Fatalf("channel models: %+v", row.Models)
	}
	channelModelRow := row.Models[0]
	if channelModelRow.ID != channelModel.ID || channelModelRow.Name != "home-rank-model" || channelModelRow.ChannelName != "#1001" {
		t.Fatalf("model identity: %+v", channelModelRow)
	}
	if math.Abs(channelModelRow.TotalCost-0.3) > 1e-9 {
		t.Fatalf("model cost: %+v", channelModelRow)
	}
	if channelModelRow.TotalToken != 30 || channelModelRow.RequestCount != 3 || channelModelRow.RequestFailed != 1 {
		t.Fatalf("model sums: %+v", channelModelRow)
	}

	// 裁剪验证: 榜单行不得夹带渠道配置或原始累计桶。
	channelJSON := mustMap(t, raw["channels"].([]any)[0])
	for _, forbidden := range []string{"user_id", "share_code", "enabled", "review_status"} {
		if _, exists := channelJSON[forbidden]; exists {
			t.Fatalf("channel row leaked %q: %v", forbidden, channelJSON)
		}
	}
	modelJSON := mustMap(t, channelJSON["models"].([]any)[0])
	for _, forbidden := range []string{"input_token", "output_token", "input_cost", "output_cost", "wait_time", "model_id", "error_count"} {
		if _, exists := modelJSON[forbidden]; exists {
			t.Fatalf("model row leaked %q: %v", forbidden, modelJSON)
		}
	}
	// 精简结构必须保留聚合所需的五项。
	for _, required := range []string{"total_cost", "total_token", "request_count", "request_success", "request_failed"} {
		if _, exists := modelJSON[required]; !exists {
			t.Fatalf("model row missing %q: %v", required, modelJSON)
		}
	}
}

// TestStatsHomeRankingPayloadIsSmallerThanChannelStats 首屏报文必须比它替代的那四条更小。
// 榜单裁剪是这个任务的一半目的, 少了这条断言, 以后往 ChannelStats 上加字段会悄悄把首屏撑回去:
// 那种退化不会让任何功能用例失败, 只体现为首页变慢。
func TestStatsHomeRankingPayloadIsSmallerThanChannelStats(t *testing.T) {
	_, _, adminToken, _, _, _, _, _ := ticketAPIEnv(t)
	engine := ticketEngine(t)

	// 造一批"配置很重"的渠道: 真实部署里路径, 代理和参数覆盖才是报文大头。
	for i := 0; i < 12; i++ {
		channel := model.Channel{
			Shared:    true,
			ShareCode: strconv.Itoa(2000 + i),
			ChannelConfig: model.ChannelConfig{
				Name:                "payload-channel-" + strconv.Itoa(i),
				BaseURL:             "https://upstream-" + strconv.Itoa(i) + ".example.com/v1",
				ChannelProxy:        "http://user:pass@proxy.example.com:8080",
				ParamOverride:       strings.Repeat("override", 40),
				MatchRegex:          "^gpt-.*",
				CustomHeader:        []model.CustomHeader{{HeaderKey: "X-Upstream-Tenant", HeaderValue: strings.Repeat("t", 32)}},
				Enabled:             true,
				MarkupRatio:         -1,
				Dialect:             model.DialectGeneric,
				KeyRotationStrategy: "roundrobin",
			},
			StatsMetrics: model.StatsMetrics{InputToken: 10, OutputToken: 20, InputCost: 1.5, OutputCost: 2.5, RequestSuccess: 4, RequestFailed: 1},
		}
		if err := db.GetDB().Create(&channel).Error; err != nil {
			t.Fatalf("create channel: %v", err)
		}
		for j := 0; j < 6; j++ {
			if err := db.GetDB().Create(&model.ChannelModel{
				ChannelID: channel.ID,
				Name:      "model-" + strconv.Itoa(j),
				StatsMetrics: model.StatsMetrics{
					InputToken: 1, OutputToken: 2, InputCost: 0.1, OutputCost: 0.2, RequestSuccess: 3, RequestFailed: 2,
				},
			}).Error; err != nil {
				t.Fatalf("create channel model: %v", err)
			}
		}
	}
	if err := op.InitCache(); err != nil {
		t.Fatalf("init cache: %v", err)
	}

	home := ticketAPI(t, engine, http.MethodGet, "/api/v1/stats/home", adminToken, "")
	channelStats := ticketAPI(t, engine, http.MethodGet, "/api/v1/channel/stats", adminToken, "")
	daily := ticketAPI(t, engine, http.MethodGet, "/api/v1/stats/daily", adminToken, "")
	hourly := ticketAPI(t, engine, http.MethodGet, "/api/v1/stats/hourly", adminToken, "")
	total := ticketAPI(t, engine, http.MethodGet, "/api/v1/stats/total", adminToken, "")
	for _, w := range []*httptest.ResponseRecorder{home, channelStats, daily, hourly, total} {
		if w.Code != http.StatusOK {
			t.Fatalf("unexpected status %d: %s", w.Code, w.Body.String())
		}
	}

	split := len(channelStats.Body.Bytes()) + len(daily.Body.Bytes()) + len(hourly.Body.Bytes()) + len(total.Body.Bytes())
	// 两边都含同一份 daily/hourly/total, 所以这个比较只反映榜单段的裁剪量, 与渠道数量无关。
	t.Logf("payload: home=%d split=%d (daily=%d hourly=%d total=%d channel-stats=%d)",
		len(home.Body.Bytes()), split, len(daily.Body.Bytes()), len(hourly.Body.Bytes()),
		len(total.Body.Bytes()), len(channelStats.Body.Bytes()))
	if len(home.Body.Bytes()) >= split {
		t.Fatalf("聚合报文没有变小: home=%d split=%d", len(home.Body.Bytes()), split)
	}
	// 榜单部分的裁剪必须真的体现出来: 聚合响应里的 channels 段要明显小于整份渠道配置。
	var decoded struct {
		Data struct {
			Channels json.RawMessage `json:"channels"`
		} `json:"data"`
	}
	if err := json.Unmarshal(home.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("parse home: %v", err)
	}
	if got := len(decoded.Data.Channels); got >= len(channelStats.Body.Bytes()) {
		t.Fatalf("榜单段没有比渠道配置小: lean=%d full=%d", got, len(channelStats.Body.Bytes()))
	}
}

// TestStatsHomeDocumentedInOpenAPI 首页聚合接口必须出现在后台 OpenAPI 文档里,
// 且与同组其他统计接口一样带鉴权声明与失败码; 缺失会让前端无从对接这条新接口。
func TestStatsHomeDocumentedInOpenAPI(t *testing.T) {
	route, ok := openAPIAdminPathsOps()["/api/v1/stats/home"].(gin.H)
	if !ok {
		t.Fatal("missing OpenAPI path /api/v1/stats/home")
	}
	operation, ok := route["get"].(gin.H)
	if !ok || operation["summary"] == nil {
		t.Fatalf("missing operation: %#v", route)
	}
	responses, ok := operation["responses"].(gin.H)
	if !ok || responses["401"] == nil || responses["500"] == nil {
		t.Fatalf("missing failure responses: %#v", operation["responses"])
	}
	if operation["security"] == nil {
		t.Fatal("missing authentication documentation")
	}
	// 响应体必须指向聚合结构, 而不是让消费方自己拼四条接口。
	if !schemaMentions(responses["200"], "StatsHome") {
		t.Fatalf("200 does not reference StatsHome: %#v", responses["200"])
	}
	if _, ok := openAPIAdminSchemas()["StatsHome"]; !ok {
		t.Fatal("missing StatsHome schema")
	}
}

// schemaMentions 递归找一个 schema 里是否引用了指定组件名。
func schemaMentions(value any, name string) bool {
	switch typed := value.(type) {
	case gin.H:
		if ref, ok := typed["$ref"].(string); ok && strings.HasSuffix(ref, "/"+name) {
			return true
		}
		for _, nested := range typed {
			if schemaMentions(nested, name) {
				return true
			}
		}
	case []gin.H:
		for _, nested := range typed {
			if schemaMentions(nested, name) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if schemaMentions(nested, name) {
				return true
			}
		}
	}
	return false
}

func jsonArrayEmpty(value any) bool {
	rows, ok := value.([]any)
	return ok && len(rows) == 0
}

func mustMap(t *testing.T, value any) map[string]any {
	t.Helper()
	row, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("not an object: %#v", value)
	}
	return row
}

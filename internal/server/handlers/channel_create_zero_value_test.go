package handlers

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// TestChannelCreateHonoursExplicitZeroValues 创建接口必须把"明确提交的零值"写进去。
//
// 走真实 HTTP 路径的原因: 判据"提交体里写了哪些键"只存在于 JSON 层, 由处理器读裸字节
// 填进 ExplicitColumns。若只测 op.ChannelCreate, 就测不到这一段 —— 而那正是整个修复的入口:
// 处理器没填, op 侧一律按默认值建, 缺陷原样复现。
//
// 断言三处: 接口返回值、库内行、/channel/stats 读到的缓存值。缓存那一处是本项目的惯犯
// ("库对缓存错"), 转发按缓存工作, 所以必须单独看。
func TestChannelCreateHonoursExplicitZeroValues(t *testing.T) {
	_, _, adminToken, _, _, _, _, _ := ticketAPIEnv(t)
	engine := ticketEngine(t)

	body := `{"name":"zero-value","enabled":false,"markup_ratio":0,"base_url":"https://zero.example.com","dialect":"openai"}`
	w := ticketAPI(t, engine, http.MethodPost, "/api/v1/channel/create", adminToken, body)
	if w.Code != http.StatusOK {
		t.Fatalf("create channel: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		Data struct {
			ID          int     `json:"id"`
			Enabled     bool    `json:"enabled"`
			MarkupRatio float64 `json:"markup_ratio"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.Data.ID == 0 {
		t.Fatalf("没有拿到 id: %s", w.Body.String())
	}
	if created.Data.Enabled {
		t.Fatalf("返回值 enabled = true, want false(明确提交的 false 被 DB 默认值吃掉了)")
	}
	if created.Data.MarkupRatio != 0 {
		t.Fatalf("返回值 markup_ratio = %v, want 0", created.Data.MarkupRatio)
	}

	var row model.Channel
	if err := db.GetDB().First(&row, created.Data.ID).Error; err != nil {
		t.Fatalf("reload row: %v", err)
	}
	if row.Enabled || row.MarkupRatio != 0 {
		t.Fatalf("库内 enabled=%v markup=%v, want false/0", row.Enabled, row.MarkupRatio)
	}

	// 列表读的是缓存: 若缓存没跟上(库对缓存错), 列表页仍会显示"启用中"。
	statsResp := ticketAPI(t, engine, http.MethodGet, "/api/v1/channel/stats", adminToken, "")
	if statsResp.Code != http.StatusOK {
		t.Fatalf("channel stats: %d %s", statsResp.Code, statsResp.Body.String())
	}
	var stats struct {
		Data []struct {
			ChannelID int  `json:"channel_id"`
			Enabled   bool `json:"enabled"`
		} `json:"data"`
	}
	if err := json.Unmarshal(statsResp.Body.Bytes(), &stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	found := false
	for _, item := range stats.Data {
		if item.ChannelID != created.Data.ID {
			continue
		}
		found = true
		if item.Enabled {
			t.Fatalf("缓存 enabled = true, want false(库对缓存错, 列表会显示成启用中)")
		}
	}
	if !found {
		t.Fatalf("新建的渠道 %d 不在 /channel/stats 里: %s", created.Data.ID, statsResp.Body.String())
	}
}

// TestChannelCreateKeepsDefaultsForUnmentionedFields 是对照: 没提交的字段必须仍按默认值建。
//
// 这条守着"别把默认值机制整体弄坏"。判据来自 JSON 层, 所以这里刻意只提交最小字段集:
// 若有人图省事把判据改成"一律回写全部可写列", 本用例会立刻变红 —— 那会把所有老客户端
// 建出来的渠道变成"默认停用", 排查方向完全不同。
func TestChannelCreateKeepsDefaultsForUnmentionedFields(t *testing.T) {
	_, _, adminToken, _, _, _, _, _ := ticketAPIEnv(t)
	engine := ticketEngine(t)

	body := `{"name":"minimal","base_url":"https://minimal.example.com","dialect":"openai"}`
	w := ticketAPI(t, engine, http.MethodPost, "/api/v1/channel/create", adminToken, body)
	if w.Code != http.StatusOK {
		t.Fatalf("create channel: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var row model.Channel
	if err := db.GetDB().First(&row, created.Data.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !row.Enabled {
		t.Fatalf("未提交 enabled 应默认启用, 实际是停用(默认值机制被改坏了)")
	}
	if row.KeyRotationStrategy != "roundrobin" {
		t.Fatalf("未提交 key_rotation_strategy 应默认 roundrobin, 实际 %q", row.KeyRotationStrategy)
	}
	if row.OpenAIChatCompletionPath != "/v1/chat/completions" {
		t.Fatalf("未提交路径应填默认值, 实际 %q", row.OpenAIChatCompletionPath)
	}
}

package op

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// createChannelFixture 在独立测试库里建一个管理员渠道并返回创建结果。
//
// explicit 模拟 HTTP 处理器填的"提交体里出现过哪些键": 传 nil 表示处理器没提供
// (等价于"调用方什么都没说", 一切按 DB 默认值建); 传字段名表示该字段明确提交过。
func createChannelFixture(t *testing.T, cfg model.ChannelConfig, explicit map[string]bool) model.ChannelDetail {
	t.Helper()
	resetProcessCaches()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "create.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	admin, err := UserCreate("create-admin-"+t.Name()[len(t.Name())-4:], "create@test.local", "password123", model.RoleAdmin, model.StatusActive)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://upstream.test"
	}
	scope := model.Scope{ID: admin.ID, Role: model.RoleAdmin}
	created, err := ChannelCreate(&model.ChannelDetail{ChannelConfig: cfg, ExplicitColumns: explicit}, scope, context.Background())
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	return *created
}

// TestChannelCreateKeepsZeroValueEnabled 新建渠道必须能表达"先建好但先别启用"。
//
// GORM 对带 `default:` 标签的列在零值时的行为是: 写库时用 DB 默认值, 并把默认值**回填**
// 到结构体。enabled 的默认值是 true, 于是提交 enabled=false 建出来的渠道实际是启用的,
// 接口返回成功、库里和返回体都是 true, 全程没有任何报错 —— 管理员以为自己建了一个
// 停用渠道, 转发却立刻开始接流量。修法是 Create 之后按派生列集合把提交值原样回写一遍。
// 断言必须同时看三处: 返回值、库内行、channelCache 里的缓存副本。
func TestChannelCreateKeepsZeroValueEnabled(t *testing.T) {
	created := createChannelFixture(t, model.ChannelConfig{
		Name:    "disabled-by-request",
		Enabled: false,
	}, map[string]bool{"name": true, "enabled": true})
	if created.Enabled {
		t.Fatalf("返回值 enabled = true, want false (提交的零值被 GORM 的 default 覆盖了)")
	}
	var row model.Channel
	if err := db.GetDB().First(&row, created.ID).Error; err != nil {
		t.Fatalf("reload row: %v", err)
	}
	if row.Enabled {
		t.Fatalf("库内 enabled = true, want false")
	}
	cached, ok := channelCache.Get(created.ID)
	if !ok {
		t.Fatalf("channel %d 不在缓存里", created.ID)
	}
	if cached.Enabled {
		t.Fatalf("缓存 enabled = true, want false (缓存与库分叉, 转发会按缓存工作)")
	}
}

// TestChannelCreateKeepsZeroMarkupRatio 新建渠道必须能表达"按供货价卖, 不加价"。
//
// markup_ratio 的默认值是 -1(跟随全局加价), 而 0 是"明确不加价"这个**真正的业务意图**。
// 两者不是同一个东西: -1 会跟着全局设置漂移, 0 不会。被默认值吃掉之后, 想卖成本价
// 的渠道只能建完再改一次, 而"改一次"在批量建渠道时没人会记得。
func TestChannelCreateKeepsZeroMarkupRatio(t *testing.T) {
	created := createChannelFixture(t, model.ChannelConfig{
		Name:        "no-markup",
		MarkupRatio: 0,
	}, map[string]bool{"name": true, "markup_ratio": true})
	if created.MarkupRatio != 0 {
		t.Fatalf("返回值 markup_ratio = %v, want 0 (被默认值 -1 覆盖了)", created.MarkupRatio)
	}
	var row model.Channel
	if err := db.GetDB().First(&row, created.ID).Error; err != nil {
		t.Fatalf("reload row: %v", err)
	}
	if row.MarkupRatio != 0 {
		t.Fatalf("库内 markup_ratio = %v, want 0", row.MarkupRatio)
	}
	cached, ok := channelCache.Get(created.ID)
	if !ok {
		t.Fatalf("channel %d 不在缓存里", created.ID)
	}
	if cached.MarkupRatio != 0 {
		t.Fatalf("缓存 markup_ratio = %v, want 0", cached.MarkupRatio)
	}
}

// TestChannelCreateKeepsOmittedDefaults 是上两条的对照: 不提交的字段仍按默认值建。
//
// 这条守着"别把默认值机制整体弄坏"—— 修法是"把提交值回写一遍", 若有人图省事改成
// "去掉所有 default 标签", 那么不填 enabled 的新渠道会变成停用、不填键轮换策略的会变成
// 空串, 存量行为整体位移。这里断言默认值还在。
func TestChannelCreateKeepsOmittedDefaults(t *testing.T) {
	created := createChannelFixture(t, model.ChannelConfig{Name: "defaults-intact"}, map[string]bool{"name": true})
	if !created.Enabled {
		t.Fatalf("未提交 enabled 时 want true(默认启用), got false")
	}
	if created.KeyRotationStrategy != "roundrobin" {
		t.Fatalf("未提交 key_rotation_strategy 时 want roundrobin, got %q", created.KeyRotationStrategy)
	}
	if created.OpenAIChatCompletionPath != "/v1/chat/completions" {
		t.Fatalf("未提交 openai_chat_completion_path 时 want 默认路径, got %q", created.OpenAIChatCompletionPath)
	}
	// 统计列必须仍是零: 回写用的是派生集合, 已挖掉运行期列; 若哪天有人把统计字段挪进
	// ChannelConfig, 这条会立刻变红(回写会把刚建好的行抹上统计零值之外的东西)。
	var row model.Channel
	if err := db.GetDB().First(&row, created.ID).Error; err != nil {
		t.Fatalf("reload row: %v", err)
	}
	if row.RequestSuccess != 0 || row.InputToken != 0 {
		t.Fatalf("运行期统计列被写入: success=%d input=%d, want 0/0", row.RequestSuccess, row.InputToken)
	}
}

package op

import (
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// 这组测试守的是"库对缓存错"这一类缺陷 —— 渠道保存(P0)与自动探测回写 project 都栽在这里。
//
// 形状: ChannelUpdate 的参数通道与缓存重建各自是一份手抄的字段清单, 漏掉的字段既不报错也不失败
// (GORM 对 Select 里未识别的列名静默放过), 于是保存返回成功, 库里(或缓存里)却还是旧值。
// 用户看到的是"改了没生效, 重启又回空" —— 重启后 InitCache 重读整行, 才把库里的真值重新装进缓存,
// 所以"重启后又回空"恰恰说明库里也是空的: 写入那一侧漏了, 不只是缓存没同步。
//
// 因此每个用例都必须**断言缓存里的值**, 只断言库里的值抓不到这个 bug: 库里的值本就可能是对的
// (例如探测回写 project: 库写进去了, 缓存没跟上, 转发照旧 403)。

// TestChannelUpdatePersistsSubscriptionFields 覆盖四个曾被 Select 白名单漏掉的列。
// 四列各自对应一次线上故障: gemini_project 空 → 出站信封缺 project → 403 SUBSCRIPTION_REQUIRED;
// sub_platform 空 → 转发按"未登记平台"处理; openai_video_base_path 与 key_rotation_strategy → 静默不生效。
func TestChannelUpdatePersistsSubscriptionFields(t *testing.T) {
	ctx, _, admin := reviewTestEnv(t)
	detail := reviewTestDetail("subscription-fields")
	detail.Dialect = model.DialectSubscription
	detail.SubPlatform = "gemini"
	created, err := ChannelCreate(detail, admin, ctx)
	if err != nil {
		t.Fatalf("create subscription channel: %v", err)
	}

	submitted := *created
	submitted.GeminiProject = "proj-manual"
	submitted.OpenAIVideoBasePath = "/v2"
	submitted.KeyRotationStrategy = "failover"
	// 同时置一个零值字段: 全量提交下 enabled=false 必须落库, 不能被"零值跳过"吃掉。
	submitted.Enabled = false
	if _, err := ChannelUpdate(&submitted, admin, ctx); err != nil {
		t.Fatalf("update subscription channel: %v", err)
	}

	cached, ok := channelCache.Get(created.ID)
	if !ok {
		t.Fatalf("channel %d 不在缓存里", created.ID)
	}
	for _, tc := range []struct {
		name string
		got  any
		want any
	}{
		{"gemini_project", cached.GeminiProject, "proj-manual"},
		{"sub_platform", cached.SubPlatform, "gemini"},
		{"openai_video_base_path", cached.OpenAIVideoBasePath, "/v2"},
		{"key_rotation_strategy", cached.KeyRotationStrategy, "failover"},
		{"enabled", cached.Enabled, false},
	} {
		if tc.got != tc.want {
			t.Errorf("缓存里 %s = %v, want %v(保存成功但缓存没跟上, 转发会按旧值工作)", tc.name, tc.got, tc.want)
		}
	}

	// 库内同样要落上: 否则重启(InitCache 重读整行)之后又会回空。
	var row model.Channel
	if err := db.GetDB().First(&row, created.ID).Error; err != nil {
		t.Fatalf("reload channel row: %v", err)
	}
	if row.GeminiProject != "proj-manual" || row.SubPlatform != "gemini" ||
		row.OpenAIVideoBasePath != "/v2" || row.KeyRotationStrategy != "failover" || row.Enabled {
		t.Fatalf("库内字段没落上: project=%q platform=%q videobase=%q rotation=%q enabled=%v",
			row.GeminiProject, row.SubPlatform, row.OpenAIVideoBasePath, row.KeyRotationStrategy, row.Enabled)
	}
}

// TestChannelUpdateKeepsRuntimeState 守回读重建时的两个运行期例外:
// 统计累加含本轮尚未落库的部分(比库内的行更新), 额度暂停的截止时间不能被一次配置保存抹掉。
// 派生白名单若把统计列也拉进提交范围, 一次保存就会把转发累加的统计抹回零 —— 这条用例同时守着写入侧。
func TestChannelUpdateKeepsRuntimeState(t *testing.T) {
	ctx, _, admin := reviewTestEnv(t)
	detail := reviewTestDetail("runtime-state")
	// 出口池是子表: 回读 channels 行读不到它, 重建缓存时必须保留旧条目那份,
	// 否则两次写入之间会出现"缓存里没有出口池"的窗口, 转发退回渠道自身的代理配置。
	detail.Egresses = []model.ChannelEgress{{Name: "egress-1", Kind: model.EgressKindDirect, Enabled: true}}
	created, err := ChannelCreate(detail, admin, ctx)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 统计只进缓存并标脏, 不落库 —— 正是"缓存比库新"的状态。
	metrics := model.StatsMetrics{InputToken: 4242, OutputToken: 17, RequestSuccess: 3, RequestFailed: 1}
	if err := ChannelStatsUpdate(created.ID, metrics); err != nil {
		t.Fatalf("accumulate stats: %v", err)
	}
	// 额度暂停: 库与缓存同时写入, 回读时应当保留。
	if err := RecordChannelQuotaError(created.ID); err != nil {
		t.Fatalf("pause channel by quota: %v", err)
	}
	// 再把缓存里的暂停推得更晚, 模拟"暂停写入之后、保存之前"的并发时序:
	// 回读只认库内那一行的话, 这一份更晚的截止时间会被较早的库内值顶替。
	later := time.Now().Add(3 * time.Hour)
	channelStatsNeedUpdateLock.Lock()
	if cached, ok := channelCache.Get(created.ID); ok {
		cached.DisabledUntil = &later
		channelCache.Set(created.ID, cached)
	}
	channelStatsNeedUpdateLock.Unlock()

	submitted := *created
	submitted.MarkupRatio = 0.2
	if _, err := ChannelUpdate(&submitted, admin, ctx); err != nil {
		t.Fatalf("update: %v", err)
	}

	cached, ok := channelCache.Get(created.ID)
	if !ok {
		t.Fatalf("channel %d 不在缓存里", created.ID)
	}
	if cached.StatsMetrics != metrics {
		t.Fatalf("缓存统计被保存抹掉了: got %+v, want %+v", cached.StatsMetrics, metrics)
	}
	if cached.DisabledUntil == nil || !cached.DisabledUntil.Equal(later) {
		t.Fatalf("缓存里的额度暂停没保住: got %v, want %v", cached.DisabledUntil, later)
	}
	if cached.MarkupRatio != 0.2 {
		t.Fatalf("上浮比例没落上缓存: %v", cached.MarkupRatio)
	}
	// 统计列不得被这次保存写进库: 白名单挖掉运行期列, 否则转发累加会被提交时的零值回填。
	var row model.Channel
	if err := db.GetDB().First(&row, created.ID).Error; err != nil {
		t.Fatalf("reload row: %v", err)
	}
	if row.InputToken != 0 || row.RequestSuccess != 0 {
		t.Fatalf("保存把运行期统计写进库了: input_token=%d request_success=%d", row.InputToken, row.RequestSuccess)
	}
	// 出口池不得在回读重建时丢掉: 直接调这个 helper 单独验证(在 ChannelUpdate 里它后面紧跟
	// reloadChannelChildren 会把子表整份刷回来, 只看最终状态抓不到中间那段空窗)。
	if _, err := reloadChannelRowCache(ctx, created.ID); err != nil {
		t.Fatalf("reload channel row cache: %v", err)
	}
	afterReload, ok := channelCache.Get(created.ID)
	if !ok {
		t.Fatalf("channel %d 不在缓存里", created.ID)
	}
	if len(afterReload.Egresses) != 1 || afterReload.Egresses[0].Name != "egress-1" {
		t.Fatalf("回读重建丢了出口池: %+v(转发会退回渠道自身的代理配置)", afterReload.Egresses)
	}
}

// TestChannelConfigStaysAfterInitCache 是"重启后又回空"那条用户口径的直接复现:
// 保存后重跑一次 InitCache(等价于进程重启时的缓存重建), 缓存里必须还是新值。
// 只修缓存重建而不修写入侧的话, 这条用例会退回到提交前的旧值。
func TestChannelConfigStaysAfterInitCache(t *testing.T) {
	ctx, _, admin := reviewTestEnv(t)
	detail := reviewTestDetail("survives-restart")
	detail.Dialect = model.DialectSubscription
	detail.SubPlatform = "gemini"
	created, err := ChannelCreate(detail, admin, ctx)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	submitted := *created
	submitted.GeminiProject = "proj-restart"
	submitted.SubPlatform = "gemini"
	if _, err := ChannelUpdate(&submitted, admin, ctx); err != nil {
		t.Fatalf("update: %v", err)
	}
	// 等价于重启: 清空并按库重建全部缓存。
	if err := InitCache(); err != nil {
		t.Fatalf("init cache: %v", err)
	}
	cached, ok := channelCache.Get(created.ID)
	if !ok {
		t.Fatalf("channel %d 在 InitCache 后不在缓存里", created.ID)
	}
	if cached.GeminiProject != "proj-restart" {
		t.Fatalf("重启后 gemini_project = %q, want proj-restart(库里没落上, 于是重启又回空)", cached.GeminiProject)
	}
	if cached.SubPlatform != "gemini" {
		t.Fatalf("重启后 sub_platform = %q, want gemini", cached.SubPlatform)
	}
}

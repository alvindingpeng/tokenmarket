package op

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// reviewTestEnv 建独立库并预热缓存, 返回渠道商与管理员作用域; 每个测试用独立 TempDir, 缓存由 InitCache 清空重建。
func reviewTestEnv(t *testing.T) (ctx context.Context, reseller model.Scope, admin model.Scope) {
	t.Helper()
	// 换库前清空进程级缓存, 否则 -shuffle 下会读到上一个用例的旧库数据。
	resetProcessCaches()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "review.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	ctx = context.Background()
	resellerUser := model.User{Username: "reseller1", Role: model.RoleReseller, Status: model.StatusActive}
	if err := db.GetDB().Create(&resellerUser).Error; err != nil {
		t.Fatal(err)
	}
	adminUser := model.User{Username: "admin1", Role: model.RoleAdmin, Status: model.StatusActive}
	if err := db.GetDB().Create(&adminUser).Error; err != nil {
		t.Fatal(err)
	}
	if err := userRefreshCache(ctx); err != nil {
		t.Fatalf("refresh user cache: %v", err)
	}
	if err := InitCache(); err != nil {
		t.Fatalf("init cache: %v", err)
	}
	return ctx, model.Scope{ID: resellerUser.ID, Role: model.RoleReseller}, model.Scope{ID: adminUser.ID, Role: model.RoleAdmin}
}

// reviewTestDetail 一份最小可用的渠道配置: 一个凭据, 一个模型, 一条授权。
func reviewTestDetail(name string) *model.ChannelDetail {
	return &model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{Name: name, BaseURL: "http://upstream", Enabled: true},
		Keys:          []model.ChannelKeyConfig{{Name: "k1", Key: "sk-test", Enabled: true}},
		Models:        []string{"m1"},
		Grants:        []model.ChannelGrantConfig{{ModelName: "m1", KeyName: "k1", Protocols: model.ProtocolOpenAIChatCompletion}},
	}
}

func TestReviewStateMachine(t *testing.T) {
	ctx, reseller, admin := reviewTestEnv(t)
	// 管理员免审关掉, 走完整提交-审批流程。
	if err := SettingUpsert(model.SettingKeyChannelReviewAdminBypass, "false"); err != nil {
		t.Fatal(err)
	}
	created, err := ChannelCreate(reviewTestDetail("ch1"), reseller, ctx)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	channel, _ := channelCache.Get(created.ID)
	if channel.ReviewStatus != model.ChannelReviewDraft {
		t.Fatalf("new channel status = %s, want draft", channel.ReviewStatus)
	}

	// 未过审不能发布。
	if _, err := ChannelPublish(created.ID, true, reseller, ctx); err == nil {
		t.Fatal("publish of draft channel should fail")
	}

	// 提交 → pending; 重复提交被拒。
	channel, err = ChannelReviewSubmit(created.ID, "please review", reseller, ctx)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if channel.ReviewStatus != model.ChannelReviewPending {
		t.Fatalf("after submit status = %s, want pending", channel.ReviewStatus)
	}
	if _, err := ChannelReviewSubmit(created.ID, "again", reseller, ctx); err == nil {
		t.Fatal("double submit should fail")
	}

	// 驳回原因必填; 驳回 → rejected 并保留原因。
	if _, err := ChannelReviewReject(created.ID, "", admin.ID, admin, ctx); err == nil {
		t.Fatal("reject without reason should fail")
	}
	channel, err = ChannelReviewReject(created.ID, "base url 不合规", admin.ID, admin, ctx)
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if channel.ReviewStatus != model.ChannelReviewRejected || channel.ReviewNote != "base url 不合规" {
		t.Fatalf("after reject status=%s note=%s", channel.ReviewStatus, channel.ReviewNote)
	}

	// 驳回后可重新提交; 批准 → approved 并写指纹基线。
	if _, err = ChannelReviewSubmit(created.ID, "fixed", reseller, ctx); err != nil {
		t.Fatalf("resubmit: %v", err)
	}
	channel, err = ChannelReviewApprove(created.ID, "ok", admin.ID, admin, ctx)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if channel.ReviewStatus != model.ChannelReviewApproved {
		t.Fatalf("after approve status = %s", channel.ReviewStatus)
	}
	if channel.ConfigFingerprint == "" {
		t.Fatal("approve should write config fingerprint baseline")
	}

	// 已通过后可发布。
	if _, err := ChannelPublish(created.ID, true, reseller, ctx); err != nil {
		t.Fatalf("publish after approve: %v", err)
	}
}

func TestReviewFingerprintStale(t *testing.T) {
	ctx, reseller, admin := reviewTestEnv(t)
	if err := SettingUpsert(model.SettingKeyChannelReviewAdminBypass, "false"); err != nil {
		t.Fatal(err)
	}
	created, err := ChannelCreate(reviewTestDetail("ch1"), reseller, ctx)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := ChannelReviewSubmit(created.ID, "", reseller, ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := ChannelReviewApprove(created.ID, "", admin.ID, admin, ctx); err != nil {
		t.Fatal(err)
	}

	// 非指纹字段(代理)改动不触发 stale。
	detail := reviewTestDetail("ch1")
	detail.ID = created.ID
	detail.ChannelProxy = "http://proxy:8080"
	updated, err := ChannelUpdate(detail, reseller, ctx)
	if err != nil {
		t.Fatalf("update proxy: %v", err)
	}
	_ = updated
	channel, _ := channelCache.Get(created.ID)
	if channel.ReviewStatus != model.ChannelReviewApproved {
		t.Fatalf("proxy-only change should stay approved, got %s", channel.ReviewStatus)
	}

	// base_url 改动 → stale。
	detail = reviewTestDetail("ch1")
	detail.ID = created.ID
	detail.BaseURL = "http://other-upstream"
	if _, err := ChannelUpdate(detail, reseller, ctx); err != nil {
		t.Fatalf("update base url: %v", err)
	}
	channel, _ = channelCache.Get(created.ID)
	if channel.ReviewStatus != model.ChannelReviewStale {
		t.Fatalf("base_url change should mark stale, got %s", channel.ReviewStatus)
	}

	// stale 写了审计。
	var count int64
	db.GetDB().Model(&model.AuditLog{}).Where("action = ?", "channel.review_stale").Count(&count)
	if count != 1 {
		t.Fatalf("review_stale audit count = %d, want 1", count)
	}

	// stale 只挡新暴露: 已发布(shared)保持原状。
	channel, _ = channelCache.Get(created.ID)
	if !channel.Shared {
		// 先发布再转 stale 的场景: 此处渠道未发布过, 单独验证发布闸口对 stale 关闭。
		if _, err := ChannelPublish(created.ID, true, reseller, ctx); err == nil {
			t.Fatal("stale channel should not be publishable")
		}
	}

	// stale → 重新提交 → 批准, 基线换新指纹。
	if _, err := ChannelReviewSubmit(created.ID, "fixed", reseller, ctx); err != nil {
		t.Fatalf("resubmit stale: %v", err)
	}
	channel, err = ChannelReviewApprove(created.ID, "", admin.ID, admin, ctx)
	if err != nil {
		t.Fatalf("reapprove: %v", err)
	}
	if channel.ReviewStatus != model.ChannelReviewApproved {
		t.Fatalf("after reapprove status = %s", channel.ReviewStatus)
	}
}

func TestReviewAdminBypass(t *testing.T) {
	ctx, _, admin := reviewTestEnv(t)
	// 默认开关即开: 管理员自有渠道建即过审, 无需提交。
	created, err := ChannelCreate(reviewTestDetail("admin-ch"), admin, ctx)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	channel, _ := channelCache.Get(created.ID)
	if channel.ReviewStatus != model.ChannelReviewApproved {
		t.Fatalf("admin-owned channel status = %s, want approved", channel.ReviewStatus)
	}
	if channel.ConfigFingerprint == "" {
		t.Fatal("admin bypass should write fingerprint at create")
	}
	if channel.ObservationStart == nil || channel.ObservationUntil == nil || channel.TrustLevel != model.ChannelTrustObserving {
		t.Fatalf("admin create must start observation: trust=%q start=%v until=%v", channel.TrustLevel, channel.ObservationStart, channel.ObservationUntil)
	}
	if _, err := ChannelPublish(created.ID, true, admin, ctx); err != nil {
		t.Fatalf("admin publish: %v", err)
	}
}

func TestReviewBackfillIdempotent(t *testing.T) {
	ctx, reseller, _ := reviewTestEnv(t)
	created, err := ChannelCreate(reviewTestDetail("legacy"), reseller, ctx)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 模拟上线前的存量已发布行: shared=1 且 review_status=draft。
	if err := db.GetDB().Model(&model.Channel{}).Where("id = ?", created.ID).Update("shared", true).Error; err != nil {
		t.Fatal(err)
	}
	backfilled, err := ChannelReviewBackfill(ctx)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if backfilled != 1 {
		t.Fatalf("backfilled = %d, want 1", backfilled)
	}
	channel, _ := channelCache.Get(created.ID)
	if channel.ReviewStatus != model.ChannelReviewApproved || channel.ConfigFingerprint == "" {
		t.Fatalf("backfilled status=%s fp-empty=%v", channel.ReviewStatus, channel.ConfigFingerprint == "")
	}
	if channel.TrustLevel != model.ChannelTrustObserving || channel.ObservationStart == nil || channel.ObservationUntil == nil {
		t.Fatalf("backfilled channel must start observation: trust=%q start=%v until=%v", channel.TrustLevel, channel.ObservationStart, channel.ObservationUntil)
	}
	// 幂等: 再跑零条。
	again, err := ChannelReviewBackfill(ctx)
	if err != nil || again != 0 {
		t.Fatalf("second backfill = %d, err = %v, want 0/nil", again, err)
	}
}

func TestReviewGrantCandidateGate(t *testing.T) {
	ctx, reseller, admin := reviewTestEnv(t)
	if err := SettingUpsert(model.SettingKeyChannelReviewAdminBypass, "false"); err != nil {
		t.Fatal(err)
	}
	created, err := ChannelCreate(reviewTestDetail("ch1"), reseller, ctx)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 模型上架(候选要求 listed)。
	if err := db.GetDB().Model(&model.ChannelModel{}).Where("channel_id = ?", created.ID).Update("listed", true).Error; err != nil {
		t.Fatal(err)
	}
	if err := reloadChannelChildren(ctx, created.ID); err != nil {
		t.Fatal(err)
	}

	// 未过审: 候选为空(即使 owned)。
	if got := ChannelGrantCandidates(reseller); len(got) != 0 {
		t.Fatalf("draft channel should not be a grant candidate, got %d", len(got))
	}

	if _, err := ChannelReviewSubmit(created.ID, "", reseller, ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := ChannelReviewApprove(created.ID, "", admin.ID, admin, ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := ChannelPublish(created.ID, true, reseller, ctx); err != nil {
		t.Fatalf("publish: %v", err)
	}
	// 过审发布: 候选出现。
	if got := ChannelGrantCandidates(reseller); len(got) == 0 {
		t.Fatal("approved shared channel should be a grant candidate")
	}

	// 转 stale 后退出候选, 但 shared 不变(存量分组流量零中断)。
	detail := reviewTestDetail("ch1")
	detail.ID = created.ID
	detail.BaseURL = "http://changed"
	if _, err := ChannelUpdate(detail, reseller, ctx); err != nil {
		t.Fatal(err)
	}
	if got := ChannelGrantCandidates(reseller); len(got) != 0 {
		t.Fatalf("stale channel should leave grant candidates, got %d", len(got))
	}
	channel, _ := channelCache.Get(created.ID)
	if !channel.Shared {
		t.Fatal("stale must not unshare an already published channel")
	}
}

// fleetLegacyFixtureConfig 是钉住"指纹算法只是加了两行"的固定夹具:
// 方言 + 地址 + 五个协议路径都取非默认值, 模型清单故意带重复项(证明排序与去重仍在)。
func fleetLegacyFixtureConfig() model.ChannelConfig {
	return model.ChannelConfig{
		Dialect:                   model.DialectGeminiCLI,
		BaseURL:                   "https://upstream.example.com",
		OpenAIChatCompletionPath:  "/v1/chat/completions",
		OpenAIResponsePath:        "/v1/responses",
		OpenAIImageGenerationPath: "/v1/images/generations",
		OpenAIImageEditPath:       "/v1/images/edits",
		AnthropicMessagePath:      "/v1/messages",
	}
}

// fleetLegacyPreimage 逐行重建**加字段之前**的哈希前像, 行序与旧实现完全一致。
// 它的存在只有一个用途: 让"新实现等于旧实现加两行"这件事可被机器验证, 而不是靠"看着像"。
func fleetLegacyPreimage(config model.ChannelConfig, modelNames []string) string {
	names := append([]string(nil), modelNames...)
	sort.Strings(names)
	names = slices.Compact(names)
	var b strings.Builder
	fmt.Fprintf(&b, "dialect=%s\n", config.Dialect)
	fmt.Fprintf(&b, "base=%s\n", config.BaseURL)
	fmt.Fprintf(&b, "chat=%s\n", config.OpenAIChatCompletionPath)
	fmt.Fprintf(&b, "resp=%s\n", config.OpenAIResponsePath)
	fmt.Fprintf(&b, "imggen=%s\n", config.OpenAIImageGenerationPath)
	fmt.Fprintf(&b, "imgedit=%s\n", config.OpenAIImageEditPath)
	fmt.Fprintf(&b, "anth=%s\n", config.AnthropicMessagePath)
	for _, name := range names {
		fmt.Fprintf(&b, "model=%s\n", name)
	}
	return b.String()
}

// TestChannelConfigFingerprintExtendsLegacyAlgorithm 钉住"加字段, 不改算法":
//   - 固定夹具的旧前像单独哈希, 必须仍等于加字段之前线上真实的基线值(旧算法的指纹);
//   - 新实现在旧前像的 anth= 之后插入 videobase= / subplatform= 两行, 哈希必须等于下面的常量。
//
// 为什么值得钉一个魔数: 指纹是**基线**, 差一个字节就等于把全部已过审渠道判成失配。
// 若有人"顺手"改了行序, 去重方式或路径归一口径, 那条常量会立刻失败 —— 这是唯一能在合入前
// 拦住"算法被重写"的地方(行序变了单测全绿, 线上却会批量转 stale)。
func TestChannelConfigFingerprintExtendsLegacyAlgorithm(t *testing.T) {
	config := fleetLegacyFixtureConfig()
	modelNames := []string{"gpt-5", "claude-x", "gpt-5"}

	legacy := fleetLegacyPreimage(config, modelNames)
	sum := sha256.Sum256([]byte(legacy))
	legacyHex := hex.EncodeToString(sum[:])
	if legacyHex != "867916f2574a298f3c9853443e91313e8ca17769736bcdf4e7ca0b4cc6025c14" {
		t.Fatalf("夹具的旧前像哈希变了: got %s —— 说明旧算法都没被如实重建", legacyHex)
	}

	// 新实现 = 旧前像 + 两行(插在协议路径之后, 模型清单之前), 其余一字不动。
	videoDefault := config
	videoDefault.OpenAIVideoBasePath = "/v1" // 保存侧归一后的默认值, 库内与转发看到的就是它。
	marker := "model="
	extended := legacy[:strings.Index(legacy, marker)] +
		fmt.Sprintf("videobase=%s\nsubplatform=%s\n", videoDefault.OpenAIVideoBasePath, videoDefault.SubPlatform) +
		legacy[strings.Index(legacy, marker):]
	got := channelConfigFingerprint(videoDefault, modelNames)
	sum = sha256.Sum256([]byte(extended))
	if want := hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("新实现不是\"旧前像加两行\": got %s want %s", got, want)
	}
	if got != "2758952f35c3a9a3784edbf2d4fe429a11af05d62485e8a55ffb9247bc56f2dd" {
		t.Fatalf("固定夹具的新指纹变了: got %s want 2758952f35c3a9a3784edbf2d4fe429a11af05d62485e8a55ffb9247bc56f2dd", got)
	}

	// op 层与 model 层必须同一份实现: 迁移用 model 那一份重算基线,
	// 两边算出来不一样就等于"迁移一跑, 全部已审渠道转 stale"。
	if modelFingerprint := model.ChannelConfigFingerprint(videoDefault, modelNames); modelFingerprint != got {
		t.Fatalf("op 与 model 的指纹实现已经漂移: op=%s model=%s", got, modelFingerprint)
	}
}

// TestReviewFingerprintCoversSupplyFields 两个新纳入的字段必须各自改变指纹。
//
// 反向断言同样重要: 只改非供给字段(价格/代理/Header/参数覆盖/gemini_project)时指纹必须一字不变,
// 否则修法是"把整个结构体哈希一遍", 那会让渠道商每次调价都触发复审。
func TestReviewFingerprintCoversSupplyFields(t *testing.T) {
	config := fleetLegacyFixtureConfig()
	config.OpenAIVideoBasePath = "/v1"
	modelNames := []string{"gpt-5", "claude-x"}
	baseline := channelConfigFingerprint(config, modelNames)

	t.Run("openai_video_base_path", func(t *testing.T) {
		changed := config
		changed.OpenAIVideoBasePath = "/v1beta"
		if got := channelConfigFingerprint(changed, modelNames); got == baseline {
			t.Fatalf("改动 openai_video_base_path 未改变指纹 —— 视频这一路可以不复审直接改供给; 指纹 %s", got)
		}
	})
	t.Run("sub_platform", func(t *testing.T) {
		changed := config
		changed.SubPlatform = "gemini"
		if got := channelConfigFingerprint(changed, modelNames); got == baseline {
			t.Fatalf("改动 sub_platform 未改变指纹 —— 换平台等于整条供给链换供应商, 却不用复审; 指纹 %s", got)
		}
	})

	// 非供给字段: 变的是"怎么把请求发出去", 不是"卖给用户的是什么"。
	nonSupply := []struct {
		name   string
		mutate func(*model.ChannelConfig)
	}{
		{"markup_ratio 上浮比例", func(c *model.ChannelConfig) { c.MarkupRatio = 1.5 }},
		{"channel_proxy 渠道代理", func(c *model.ChannelConfig) { c.ChannelProxy = "http://proxy:8080" }},
		{"proxy 是否走代理", func(c *model.ChannelConfig) { c.Proxy = true }},
		{"custom_header", func(c *model.ChannelConfig) {
			c.CustomHeader = []model.CustomHeader{{HeaderKey: "X-Test", HeaderValue: "1"}}
		}},
		{"param_override", func(c *model.ChannelConfig) { c.ParamOverride = `{"temperature":0.5}` }},
		{"gemini_project", func(c *model.ChannelConfig) { c.GeminiProject = "proj-manual" }},
		{"enabled 启停", func(c *model.ChannelConfig) { c.Enabled = false }},
		{"model_auto_add", func(c *model.ChannelConfig) { c.ModelAutoAdd = true }},
	}
	for _, tt := range nonSupply {
		t.Run(tt.name, func(t *testing.T) {
			changed := config
			tt.mutate(&changed)
			if got := channelConfigFingerprint(changed, modelNames); got != baseline {
				t.Fatalf("非供给字段 %s 改变了指纹(应为 %s, 实际 %s) —— 指纹口径过宽, 运维调参会反复触发复审", tt.name, baseline, got)
			}
		})
	}
}

// TestReviewStaleOnVideoBasePathChange 已过审渠道改了视频基路径, 不提交复审也必须转 stale。
//
// 修前: 指纹不含 openai_video_base_path, 于是"改视频落地路径"在审核链路眼里等于没改,
// 已过审渠道继续以 approved 身份对外暴露 —— 换了上游的视频供给却零复审直接生效。
func TestReviewStaleOnVideoBasePathChange(t *testing.T) {
	ctx, reseller, admin := reviewTestEnv(t)
	if err := SettingUpsert(model.SettingKeyChannelReviewAdminBypass, "false"); err != nil {
		t.Fatal(err)
	}
	created, err := ChannelCreate(reviewTestDetail("ch-video"), reseller, ctx)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := ChannelReviewSubmit(created.ID, "", reseller, ctx); err != nil {
		t.Fatal(err)
	}
	approved, err := ChannelReviewApprove(created.ID, "", admin.ID, admin, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if approved.ReviewStatus != model.ChannelReviewApproved {
		t.Fatalf("approve 后应为 approved, 实际 %s", approved.ReviewStatus)
	}

	// 过审后只改视频基路径: 不提交, 不经过任何审核动作。
	detail := reviewTestDetail("ch-video")
	detail.ID = created.ID
	detail.OpenAIVideoBasePath = "/v1beta/videos"
	if _, err := ChannelUpdate(detail, reseller, ctx); err != nil {
		t.Fatalf("update video base path: %v", err)
	}

	channel, ok := channelCache.Get(created.ID)
	if !ok {
		t.Fatal("channel missing from cache")
	}
	if channel.ReviewStatus != model.ChannelReviewStale {
		t.Fatalf("改了 openai_video_base_path 之后 review_status = %s, 期望 stale: 已过审渠道的供给面被改, 复审前不该继续以 approved 暴露", channel.ReviewStatus)
	}
	var staleAudits int64
	if err := db.GetDB().Model(&model.AuditLog{}).Where("action = ?", "channel.review_stale").Count(&staleAudits).Error; err != nil {
		t.Fatal(err)
	}
	if staleAudits != 1 {
		t.Fatalf("channel.review_stale 审计条数 = %d, 期望 1", staleAudits)
	}
}

// TestReviewStaleOnSubPlatformChange 已过审渠道的 sub_platform 与基线不一致时, 必须转 stale。
//
// 为什么不直接"用接口把 sub_platform 改成另一个平台": 当前只登记了一个订阅平台(gemini),
// 订阅渠道的平台名必填且必须已登记, 且方言被平台描述钉死 —— 接口层没有"换平台"这条路。
// 因此这里构造的是**绕过接口的那条路**: 库里 sub_platform 与基线记录时不同(手工改库、
// 老版本 dump 导入、将来平台改名), 而接口侧不改任何字段就原样再保存一次。
// 这正是漏审的真实形态: 供给面已经换了, 基线还停在过去, 谁都没提交过复审。
//
// 修前: 指纹不含 sub_platform, 这份库内漂移在比对里完全看不见, 渠道保持 approved。
func TestReviewStaleOnSubPlatformChange(t *testing.T) {
	ctx, reseller, admin := reviewTestEnv(t)
	if err := SettingUpsert(model.SettingKeyChannelReviewAdminBypass, "false"); err != nil {
		t.Fatal(err)
	}
	created, err := ChannelCreate(reviewTestDetail("ch-sub"), reseller, ctx)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := ChannelReviewSubmit(created.ID, "", reseller, ctx); err != nil {
		t.Fatal(err)
	}
	approved, err := ChannelReviewApprove(created.ID, "", admin.ID, admin, ctx)
	if err != nil {
		t.Fatal(err)
	}

	// 基线改写: 把基线换成"平台是 gemini 时"的指纹 —— 相当于这份已审渠道是在另一个订阅平台下审的。
	config := approved.ChannelConfig
	config.SubPlatform = "gemini"
	baselineWithPlatform := channelConfigFingerprint(config, []string{"m1"})
	if baselineWithPlatform == approved.ConfigFingerprint {
		t.Fatalf("修前前提不成立: 带 sub_platform 与不带 sub_platform 的指纹相同(%s), 说明该字段根本没进指纹", baselineWithPlatform)
	}
	if err := db.GetDB().Model(&model.Channel{}).Where("id = ?", created.ID).
		Update("config_fingerprint", baselineWithPlatform).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := reloadChannelRowCache(ctx, created.ID); err != nil {
		t.Fatal(err)
	}

	// 接口侧原样再存一次(一个字都没改): 供给面与基线已经不一致, 必须被比对发现。
	detail := reviewTestDetail("ch-sub")
	detail.ID = created.ID
	if _, err := ChannelUpdate(detail, reseller, ctx); err != nil {
		t.Fatalf("update: %v", err)
	}
	channel, ok := channelCache.Get(created.ID)
	if !ok {
		t.Fatal("channel missing from cache")
	}
	if channel.ReviewStatus != model.ChannelReviewStale {
		t.Fatalf("sub_platform 与基线不一致时 review_status = %s, 期望 stale: 换平台等于换整条上游供给链, 不该继续以 approved 暴露", channel.ReviewStatus)
	}
}

// TestReviewNonSupplyChangesStayApproved 已过审渠道改运维/价格类配置, 必须保持 approved。
//
// 这条是"指纹口径不许放大"的守卫: 把整个 ChannelConfig 哈希一遍能让上面两条都绿,
// 但代价是渠道商调价, 换代理, 加 Header, 改参数覆盖 —— 全部人工复审, 复审队列会被淹掉。
// 它同时钉住"价格不在指纹里"这个既有决定(改价有 models/reprice 审计链路)。
func TestReviewNonSupplyChangesStayApproved(t *testing.T) {
	ctx, reseller, admin := reviewTestEnv(t)
	if err := SettingUpsert(model.SettingKeyChannelReviewAdminBypass, "false"); err != nil {
		t.Fatal(err)
	}
	created, err := ChannelCreate(reviewTestDetail("ch-ops"), reseller, ctx)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := ChannelReviewSubmit(created.ID, "", reseller, ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := ChannelReviewApprove(created.ID, "", admin.ID, admin, ctx); err != nil {
		t.Fatal(err)
	}

	// 供货价是渠道模型的字段, 不在 ChannelConfig 里: 直接按"调价"改库, 再原样保存一次。
	// 走模型实例而不是 map 更新: supply_price 是 serializer:json 列, map 写入绕过序列化器
	// 会把一个裸数字塞进 JSON 列, 读回来就报 "cannot unmarshal number into LLMPrice"。
	var channelModel model.ChannelModel
	if err := db.GetDB().Where("channel_id = ?", created.ID).First(&channelModel).Error; err != nil {
		t.Fatal(err)
	}
	channelModel.SupplyPrice = model.LLMPrice{Input: 1.2, Output: 2.4, CacheRead: 0.3, CacheWrite: 0.4}
	if err := db.GetDB().Save(&channelModel).Error; err != nil {
		t.Fatal(err)
	}
	if err := reloadChannelChildren(ctx, created.ID); err != nil {
		t.Fatal(err)
	}

	detail := reviewTestDetail("ch-ops")
	detail.ID = created.ID
	detail.ChannelProxy = "http://proxy:8080"
	detail.Proxy = true
	detail.MarkupRatio = 1.2
	detail.GeminiProject = "proj-manual"
	detail.CustomHeader = []model.CustomHeader{{HeaderKey: "X-Ops", HeaderValue: "on"}}
	detail.ParamOverride = `{"temperature":0.3}`
	detail.ModelAutoAdd = true
	if _, err := ChannelUpdate(detail, reseller, ctx); err != nil {
		t.Fatalf("update non-supply config: %v", err)
	}

	channel, ok := channelCache.Get(created.ID)
	if !ok {
		t.Fatal("channel missing from cache")
	}
	if channel.ReviewStatus != model.ChannelReviewApproved {
		t.Fatalf("只改价格/代理/Header/参数覆盖/gemini_project 就转成了 %s —— 指纹口径过宽, 运维调参会反复触发复审", channel.ReviewStatus)
	}
	var staleAudits int64
	if err := db.GetDB().Model(&model.AuditLog{}).Where("action = ?", "channel.review_stale").Count(&staleAudits).Error; err != nil {
		t.Fatal(err)
	}
	if staleAudits != 0 {
		t.Fatalf("非供给字段改动写了 %d 条 review_stale 审计", staleAudits)
	}
}

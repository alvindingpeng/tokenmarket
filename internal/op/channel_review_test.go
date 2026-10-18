package op

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// reviewTestEnv 建独立库并预热缓存, 返回渠道商与管理员作用域; 每个测试用独立 TempDir, 缓存由 InitCache 清空重建。
func reviewTestEnv(t *testing.T) (ctx context.Context, reseller model.Scope, admin model.Scope) {
	t.Helper()
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

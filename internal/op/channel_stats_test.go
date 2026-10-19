package op

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// statsTopLevelExposed 渠道统计列表对外暴露的顶层 JSON 字段白名单。
//
// 这张清单是「列表页能看到什么」的唯一契约: 卡片徽章消费 review_status, 发布闸口判 shared 与
// review_status, 统计块消费嵌入的 StatsMetrics。往 ChannelStats 加字段必须同步加进白名单,
// 否则字段会静默漏拷 —— v0.39.6 正是 review_status 漏拷, 徽章渲染成裸 key、发布闸口对已过审
// 渠道也恒判受阻, 而当时所有测试都是绿的。
var statsTopLevelExposed = []string{
	// 身份与归属。
	"channel_id", "user_id", "channel_name",
	// 发布状态。
	"shared", "share_code", "enabled",
	// 准入审核: 卡片徽章与发布闸口只消费这一条查询。
	"review_status", "review_note", "submitted_at", "reviewed_at",
	// 嵌套模型统计; 恒为数组的形状由 TestChannelStatsModelListNeverNull 单独覆盖。
	"models",
	// 嵌入的累计统计。
	"input_token", "output_token", "input_cost", "output_cost",
	"wait_time", "request_success", "request_failed",
}

// statsJSONKeys 把任意值编码成 JSON 再解成 map, 取顶层 key。
//
// 走 JSON 而不是 reflect 数 struct 字段: 契约的另一端是前端, 前端能看到什么由 json tag 与
// omitempty 决定 —— 被 omitempty 掉或在序列化时消失的字段, 对前端等于不存在, 只有编码后才看得到真实形状。
func statsJSONKeys(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

// TestChannelStatsJSONShape 钉住渠道统计列表的 JSON 顶层形状。
//
// 校验「序列化后的字段集合」== 白名单。加字段、改 json tag、删字段都会让它失败,
// 迫使改动者显式声明新字段是否要给前端看、以及是否已在 ChannelStatsList 里赋了值。
func TestChannelStatsJSONShape(t *testing.T) {
	// 填非零值: 零值配 omitempty 会让字段从 JSON 里消失, 那样测不出漏拷。
	stamp := time.Now()
	stats := model.ChannelStats{
		ChannelID:    7,
		UserID:       3,
		ChannelName:  "ch",
		Shared:       true,
		ShareCode:    "1001",
		Enabled:      true,
		ReviewStatus: model.ChannelReviewApproved,
		ReviewNote:   "ok",
		SubmittedAt:  &stamp,
		ReviewedAt:   &stamp,
		Models:       []model.ChannelModelStats{},
		StatsMetrics: model.StatsMetrics{InputToken: 1, OutputToken: 2, InputCost: 0.1, OutputCost: 0.2,
			WaitTime: 30, RequestSuccess: 3, RequestFailed: 4},
	}

	got := statsJSONKeys(t, stats)
	gotKeys := make([]string, 0, len(got))
	for k := range got {
		gotKeys = append(gotKeys, k)
	}
	sort.Strings(gotKeys)

	want := append([]string(nil), statsTopLevelExposed...)
	sort.Strings(want)

	if !reflect.DeepEqual(gotKeys, want) {
		t.Fatalf("ChannelStats 对外字段变了\n  多出: %v\n  缺少: %v\n新增字段请显式加进 statsTopLevelExposed, 并确认 ChannelStatsList 里已赋值",
			diffStrings(gotKeys, want), diffStrings(want, gotKeys))
	}
}

// TestChannelStatsListCarriesReviewFields 回归 v0.39.6: 列表必须把审核字段带出来。
//
// 之前 ChannelStatsList 组装 struct 时漏拷四个审核字段, review_status 恒为空串:
// 前端徽章渲染成裸 key channel.review.status., 发布闸口的 publishBlocked 恒为真。
// 这里断言字段的值, 形状归 TestChannelStatsJSONShape 管。
func TestChannelStatsListCarriesReviewFields(t *testing.T) {
	ctx, reseller, admin := reviewTestEnv(t)
	if err := SettingUpsert(model.SettingKeyChannelReviewAdminBypass, "false"); err != nil {
		t.Fatal(err)
	}
	created, err := ChannelCreate(reviewTestDetail("with-review"), reseller, ctx)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := ChannelReviewSubmit(created.ID, "please review", reseller, ctx); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := ChannelReviewApprove(created.ID, "looks good", admin.ID, admin, ctx); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// 管理员作用域能看到全部渠道, 从中挑出刚建的那个。
	var target *model.ChannelStats
	for _, item := range ChannelStatsList(admin) {
		if item.ChannelID == created.ID {
			found := item
			target = &found
			break
		}
	}
	if target == nil {
		t.Fatalf("channel %d missing from ChannelStatsList", created.ID)
	}

	if target.ReviewStatus != model.ChannelReviewApproved {
		t.Errorf("review_status = %q, want %q —— 漏拷审核字段会让前端徽章变成裸 key",
			target.ReviewStatus, model.ChannelReviewApproved)
	}
	if target.ReviewNote == "" {
		t.Error("review_note 为空 —— 审核意见 tooltip 会一直是空的")
	}
	if target.SubmittedAt == nil {
		t.Error("submitted_at 未带出 —— 列表看不到提交时间")
	}
	if target.ReviewedAt == nil {
		t.Error("reviewed_at 未带出 —— 列表看不到审核时间")
	}
}

// TestChannelStatsModelListNeverNull 列表承诺 models 恒为数组: 无模型的渠道给空切片而非 null,
// 前端直接 .map 才不会在空渠道上炸掉。
func TestChannelStatsModelListNeverNull(t *testing.T) {
	ctx, _, admin := reviewTestEnv(t)
	created, err := ChannelCreate(reviewTestDetail("no-model-grants"), admin, ctx)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_ = created

	for _, item := range ChannelStatsList(admin) {
		payload := statsJSONKeys(t, item)
		raw, ok := payload["models"]
		if !ok {
			t.Fatalf("channel %d: models 字段缺失", item.ChannelID)
		}
		if raw == nil {
			t.Fatalf("channel %d: models 编码为 null, 应为 []", item.ChannelID)
		}
		if _, ok := raw.([]any); !ok {
			t.Fatalf("channel %d: models 类型 = %T, 应为数组", item.ChannelID, raw)
		}
	}
}

// diffStrings 返回 a 中不在 b 里的元素, 用于报出「多出什么 / 缺少什么」。
func diffStrings(a, b []string) []string {
	set := make(map[string]struct{}, len(b))
	for _, v := range b {
		set[v] = struct{}{}
	}
	out := make([]string, 0)
	for _, v := range a {
		if _, ok := set[v]; !ok {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return []string{"(无)"}
	}
	return out
}

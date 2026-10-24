package relay

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
)

// TestParseVideoRequestUnits 固定从请求体读取计费用量的规则: seconds 是字符串或数字都认,
// 分辨率档位优先取显式 resolution, 否则由 size 的短边推出 720p 这样的键。
func TestParseVideoRequestUnits(t *testing.T) {
	cases := []struct {
		name string
		body string
		want model.MediaUnits
	}{
		{"字符串时长与 size 推导", `{"model":"sora-2","seconds":"4","size":"1280x720"}`, model.MediaUnits{Seconds: 4, Resolution: "720p"}},
		{"数字时长", `{"model":"sora-2","seconds":8,"size":"720x1280"}`, model.MediaUnits{Seconds: 8, Resolution: "720p"}},
		{"显式 resolution 优先于 size", `{"seconds":"6","size":"1280x720","resolution":"1080p"}`, model.MediaUnits{Seconds: 6, Resolution: "1080p"}},
		{"缺时长", `{"model":"sora-2","size":"1280x720"}`, model.MediaUnits{Resolution: "720p"}},
		{"非法时长按零", `{"seconds":"abc","size":"1280x720"}`, model.MediaUnits{Resolution: "720p"}},
		{"无分辨率线索", `{"seconds":"4"}`, model.MediaUnits{Seconds: 4}},
		{"非法正文", `not json`, model.MediaUnits{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseVideoRequestUnits([]byte(tc.body)); got != tc.want {
				t.Fatalf("parseVideoRequestUnits = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestMediaVideoUnits 固定结算用量的来源: 上游回显的时长优先, 缺 id 视为没拿到任务对象而不计费。
func TestMediaVideoUnits(t *testing.T) {
	requested := model.MediaUnits{Seconds: 4, Resolution: "720p"}
	cases := []struct {
		name string
		body string
		want model.MediaUnits
	}{
		{"上游回显时长优先", `{"id":"video_1","status":"queued","seconds":"8","size":"1280x720"}`, model.MediaUnits{Videos: 1, Seconds: 8, Resolution: "720p"}},
		{"上游未回显时长时回落到请求声明值", `{"id":"video_1","status":"queued"}`, model.MediaUnits{Videos: 1, Seconds: 4, Resolution: "720p"}},
		{"缺 id 视为未创建成功", `{"error":{"message":"boom"}}`, model.MediaUnits{}},
		{"空正文", ``, model.MediaUnits{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mediaVideoUnits(tc.body, requested); got != tc.want {
				t.Fatalf("mediaVideoUnits = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestVideoResolutionKey 固定分辨率归一: 定价表按 480p / 720p / 1080p 这类键填写, 竖屏与横屏必须归到同一个键。
func TestVideoResolutionKey(t *testing.T) {
	cases := []struct{ resolution, size, want string }{
		{"", "1280x720", "720p"},
		{"", "720x1280", "720p"},
		{"", "1920x1080", "1080p"},
		{"1080p", "1280x720", "1080p"},
		{"", "", ""},
		{"", "720", ""},
		{"", "axb", ""},
	}
	for _, tc := range cases {
		if got := videoResolutionKey(tc.resolution, tc.size); got != tc.want {
			t.Fatalf("videoResolutionKey(%q, %q) = %q, want %q", tc.resolution, tc.size, got, tc.want)
		}
	}
}

// TestIsMediaProtocol 媒体协议按张或按秒计费, 限流估算必须把它们与 token 请求区分开。
func TestIsMediaProtocol(t *testing.T) {
	if !isMediaProtocol(model.ProtocolOpenAIImage) || !isMediaProtocol(model.ProtocolOpenAIVideo) {
		t.Fatal("image and video protocols must be treated as media")
	}
	if isMediaProtocol(model.ProtocolOpenAIChatCompletion) {
		t.Fatal("chat completion must not be treated as media")
	}
}

// TestVideoReserveSeconds 预扣时长优先用客户端声明值, 未声明才回落到保守默认值;
// 不可计费的声明值(NaN/Inf/越界)绝不参与冻结计算, 同样回落默认值。
func TestVideoReserveSeconds(t *testing.T) {
	if got := videoReserveSeconds(4); got != 4 {
		t.Fatalf("declared seconds must be used, got %v", got)
	}
	if got := videoReserveSeconds(0); got != defaultVideoReserveSeconds {
		t.Fatalf("missing seconds must fall back to %v, got %v", defaultVideoReserveSeconds, got)
	}
	if got := videoReserveSeconds(maxVideoSeconds); got != maxVideoSeconds {
		t.Fatalf("in-range upper bound must be preserved, got %v", got)
	}
	for _, unsafe := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -5, maxVideoSeconds + 0.5, 1e15} {
		if got := videoReserveSeconds(unsafe); got != defaultVideoReserveSeconds {
			t.Fatalf("unsafe seconds %v must fall back to %v, got %v", unsafe, defaultVideoReserveSeconds, got)
		}
	}
}

// TestVideoSecondsValueRejectsUnsafeDuration 固定解析侧的有限上界: NaN/Inf/越界一律按"没有可用时长"处理,
// 区间内的合法时长(含 4 小时上界)原样保留 —— 计费时长必须与转发给上游的时长一致, 不做任何改写或截断。
func TestVideoSecondsValueRejectsUnsafeDuration(t *testing.T) {
	cases := []struct {
		name string
		body string
		want float64
	}{
		{"NaN 字符串", `{"seconds":"NaN"}`, 0},
		{"Inf 字符串", `{"seconds":"Inf"}`, 0},
		{"正 Inf 字符串", `{"seconds":"+Inf"}`, 0},
		{"负 Inf 字符串", `{"seconds":"-Inf"}`, 0},
		{"无穷大单词", `{"seconds":"Infinity"}`, 0},
		{"溢出数字", `{"seconds":1e309}`, 0},
		{"越界有限数", `{"seconds":1e15}`, 0},
		{"负数", `{"seconds":-5}`, 0},
		{"零", `{"seconds":"0"}`, 0},
		{"超上界半秒", `{"seconds":14400.5}`, 0},
		{"上界保留", `{"seconds":14400}`, 14400},
		{"常规时长保留", `{"seconds":"8.5"}`, 8.5},
		{"在售范围内的长时长保留", `{"seconds":600}`, 600},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := videoSecondsValue(json.RawMessage(mustJSONField(t, tc.body, "seconds"))); got != tc.want {
				t.Fatalf("videoSecondsValue = %v, want %v", got, tc.want)
			}
			units := parseVideoRequestUnits([]byte(tc.body))
			if units.Seconds != tc.want {
				t.Fatalf("parseVideoRequestUnits Seconds = %v, want %v", units.Seconds, tc.want)
			}
		})
	}
}

// mustJSONField 取出 JSON 正文里某个字段的原始值; 缺字段返回空串。
func mustJSONField(t *testing.T, body, field string) string {
	t.Helper()
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("unmarshal %s: %v", body, err)
	}
	return string(parsed[field])
}

// TestValidateVideoRequestDuration 固定派发前的时长闸门: 声明了 seconds 就必须是 (0, maxVideoSeconds] 内的数,
// 不合法的声明值在派发前拒绝整个请求, 而不是"按默认/上限计费却把原值转发上游"。
func TestValidateVideoRequestDuration(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"未声明时长", `{"model":"sora-2","size":"1280x720"}`, false},
		{"字符串时长", `{"seconds":"4"}`, false},
		{"数字时长", `{"seconds":4}`, false},
		{"显式 null 视为未声明", `{"seconds":null}`, false},
		{"空串视为未声明", `{"seconds":""}`, false},
		{"上界可派发", `{"seconds":14400}`, false},
		{"非数值文本", `{"seconds":"abc"}`, true},
		{"布尔", `{"seconds":true}`, true},
		{"NaN", `{"seconds":"NaN"}`, true},
		{"Inf", `{"seconds":"Inf"}`, true},
		{"零", `{"seconds":0}`, true},
		{"负数", `{"seconds":-1}`, true},
		{"越界有限数", `{"seconds":1e15}`, true},
		{"超上界", `{"seconds":14400.5}`, true},
		{"非 JSON 交给协议校验层", `not json`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateVideoRequestDuration([]byte(tc.body))
			if tc.wantErr && err == nil {
				t.Fatalf("body %s must be rejected before dispatch", tc.body)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("body %s must pass, got %v", tc.body, err)
			}
		})
	}
}

// TestMediaVideoUnitsRejectsUnsafeEcho 固定结算侧的有限上界: 上游回显非有限或越界时长时按"未回显"回落
// 请求声明值, 声明值本身也过同一道区间; 合法回显原样结算, 不因上界收紧而误伤。
func TestMediaVideoUnitsRejectsUnsafeEcho(t *testing.T) {
	requested := model.MediaUnits{Seconds: 4, Resolution: "720p"}
	cases := []struct {
		name string
		body string
		want float64
	}{
		{"NaN 回显回落声明值", `{"id":"video_1","seconds":"NaN"}`, 4},
		{"Inf 回显回落声明值", `{"id":"video_1","seconds":"Inf"}`, 4},
		{"越界回显回落声明值", `{"id":"video_1","seconds":1e15}`, 4},
		{"合法回显保留", `{"id":"video_1","seconds":3600}`, 3600},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mediaVideoUnits(tc.body, requested)
			if got.Videos != 1 || got.Seconds != tc.want || got.Resolution != "720p" {
				t.Fatalf("mediaVideoUnits = %+v, want seconds %v", got, tc.want)
			}
		})
	}
	// 请求声明值本身不可计费时, 回落也归 0, 非有限值进不了金额计算。
	unsafe := mediaVideoUnits(`{"id":"video_1"}`, model.MediaUnits{Seconds: math.NaN(), Resolution: "720p"})
	if unsafe.Seconds != 0 {
		t.Fatalf("unsafe requested seconds must fall back to 0, got %v", unsafe.Seconds)
	}
}

// videoTaskEnv 一条「用户-渠道-两把凭据-模型-授权」链路: 授权绑定 k1, 创建任务时实际用的是轮换选中的 k2。
type videoTaskEnv struct {
	userID      uint
	scope       model.Scope
	detail      model.ChannelDetail
	channelID   int
	channelName string
	modelName   string
	grantID     int
	key1        model.ChannelKey
	key2        model.ChannelKey
}

func newVideoTaskEnv(t *testing.T) videoTaskEnv {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "video_task.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	// httptest 上游都在 127.0.0.1: 打开私网放行, SSRF 防护由 op 层的拒绝用例单独覆盖。
	if err := op.SettingUpsert(model.SettingKeyAllowPrivateUpstream, "true"); err != nil {
		t.Fatalf("seed allow-private setting: %v", err)
	}
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1e9)
	user, err := op.UserCreate("u"+suffix, "u"+suffix+"@test.local", "password123", model.RoleAdmin, model.StatusActive)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	env := videoTaskEnv{userID: user.ID, scope: model.Scope{ID: user.ID, Role: model.RoleAdmin}}
	env.modelName = "up-model-" + suffix
	env.channelName = "ch-" + suffix
	env.detail = model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{Name: env.channelName, Enabled: true, BaseURL: "http://upstream.invalid"},
		Keys: []model.ChannelKeyConfig{
			{Name: "k1", Key: "sk-old-" + suffix, Enabled: true},
			{Name: "k2", Key: "sk-new-" + suffix, Enabled: true},
		},
		Models: []string{env.modelName},
		Grants: []model.ChannelGrantConfig{{ModelName: env.modelName, KeyName: "k1", Protocols: model.ProtocolOpenAIVideo}},
	}
	created, err := op.ChannelCreate(&env.detail, env.scope, ctx)
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	env.channelID = created.ID
	for _, key := range op.ChannelKeysByChannel(created.ID) {
		switch key.Name {
		case "k1":
			env.key1 = key
		case "k2":
			env.key2 = key
		}
	}
	if env.key1.ID == 0 || env.key2.ID == 0 {
		t.Fatalf("fixture keys missing: %+v", op.ChannelKeysByChannel(created.ID))
	}
	var grants []model.ChannelGrant
	if err := db.GetDB().Order("id").Find(&grants).Error; err != nil || len(grants) != 1 {
		t.Fatalf("read grants: %v, got %d rows", err, len(grants))
	}
	env.grantID = grants[0].ID
	return env
}

// updateChannel 走与管理端同一条写路径全量提交配置, 供用例中途改凭据与授权。
func (env videoTaskEnv) updateChannel(t *testing.T, mutate func(*model.ChannelDetail)) {
	t.Helper()
	detail := env.detail
	detail.ID = env.channelID
	mutate(&detail)
	if _, err := op.ChannelUpdate(&detail, env.scope, context.Background()); err != nil {
		t.Fatalf("update channel: %v", err)
	}
}

// TestVideoJobRecordsCreationKey 固定任务归属落库的创建凭据: 授权绑定的凭据会轮换,
// 归属必须记下创建时实际使用的凭据 ID(只记 ID, 不记明文), 查询与取消才能用同一把凭据定位上游。
func TestVideoJobRecordsCreationKey(t *testing.T) {
	env := newVideoTaskEnv(t)
	ctx := context.Background()
	record := func(jobID string, targetKeyID int) model.VideoJob {
		t.Helper()
		request := newRequestState(ctx, "grp-video", "", 7, model.ProtocolOpenAIVideo,
			`{"model":"grp-video","prompt":"p","seconds":"4"}`, 0, env.userID, "127.0.0.1", "", 600)
		// startRound 登记的「渠道名 · 凭据名」就是创建时实际选中的凭据(轮换选路下与授权绑定的 k1 不同)。
		request.startRound(func() {}, env.channelName+" · k2", env.modelName, model.ProtocolOpenAIVideo,
			env.channelID, env.userID, "share-1", model.LLMPrice{}, 0)
		request.setMediaTarget(model.MediaKindVideo, model.MediaPrice{PerVideo: 1, PerSecond: 0.5}, env.grantID)
		request.setRequestedMedia(model.MediaUnits{Seconds: 4, Resolution: "720p"})
		if targetKeyID > 0 {
			request.setTargetKeyID(targetKeyID)
		}
		request.markSucceeded(fmt.Sprintf(`{"id":"%s","status":"queued","seconds":"4","size":"1280x720"}`, jobID), nil)
		job, err := op.VideoJobGet(jobID, env.userID)
		if err != nil {
			t.Fatalf("video job %s not recorded: %v", jobID, err)
		}
		return job
	}

	// 未登记凭据 ID 时按「渠道名 · 凭据名」反查创建凭据: 授权绑定 k1, 创建用的是 k2。
	job := record("video_task_fallback", 0)
	if job.ChannelKeyID != env.key2.ID {
		t.Fatalf("creation key id = %d, want k2 %d", job.ChannelKeyID, env.key2.ID)
	}
	if job.ChannelGrantID != env.grantID {
		t.Fatalf("grant id = %d, want %d", job.ChannelGrantID, env.grantID)
	}

	// 登记了精确凭据 ID 时以它为准, 不受凭据名拼接格式影响。
	job2 := record("video_task_exact", env.key1.ID)
	if job2.ChannelKeyID != env.key1.ID {
		t.Fatalf("creation key id = %d, want k1 %d", job2.ChannelKeyID, env.key1.ID)
	}

	// 凭据明文绝不进任务记录: 归属只记主键。
	for _, job := range []model.VideoJob{job, job2} {
		encoded, err := json.Marshal(job)
		if err != nil {
			t.Fatalf("marshal job: %v", err)
		}
		if strings.Contains(string(encoded), "sk-") {
			t.Fatalf("video job leaked credential material: %s", encoded)
		}
	}
}

// TestVideoTaskRequestUsesCreationKey 固定任务查询/取消使用创建时的凭据, 而不是当前授权绑定的凭据:
// 凭据轮换后换一把凭据查任务必然 404, 还会把「查无此任务」伪装成上游的答复。
func TestVideoTaskRequestUsesCreationKey(t *testing.T) {
	env := newVideoTaskEnv(t)
	ctx := context.Background()
	job := model.VideoJob{JobID: "video_task_1", UserID: env.userID, GroupID: 7,
		ChannelGrantID: env.grantID, ChannelID: env.channelID, ChannelKeyID: env.key2.ID, ModelName: env.modelName}

	request, channel, err := buildVideoTaskRequest(ctx, job, "video_task_1", false)
	if err != nil {
		t.Fatalf("build task request: %v", err)
	}
	if channel.ID != env.channelID {
		t.Fatalf("channel id = %d, want %d", channel.ID, env.channelID)
	}
	if request.Method != "GET" || !strings.HasSuffix(request.URL, "/videos/video_task_1") {
		t.Fatalf("unexpected task request: %s %s", request.Method, request.URL)
	}
	if request.Auth == nil || request.Auth.APIKey != env.key2.Key {
		t.Fatalf("query must use the creation credential k2, got %v", request.Auth)
	}

	// 取消同样用创建凭据。
	deletion, _, err := buildVideoTaskRequest(ctx, job, "video_task_1", true)
	if err != nil {
		t.Fatalf("build delete request: %v", err)
	}
	if deletion.Method != "DELETE" || deletion.Auth == nil || deletion.Auth.APIKey != env.key2.Key {
		t.Fatalf("delete must use the creation credential k2, got %s %v", deletion.Method, deletion.Auth)
	}

	// 老记录没有凭据 ID: 回落授权绑定的凭据(旧行为)。
	legacy := job
	legacy.ChannelKeyID = 0
	legacyRequest, _, err := buildVideoTaskRequest(ctx, legacy, "video_task_1", false)
	if err != nil {
		t.Fatalf("legacy job must fall back to the grant credential: %v", err)
	}
	if legacyRequest.Auth.APIKey != env.key1.Key {
		t.Fatalf("legacy job must use the grant credential k1, got %v", legacyRequest.Auth)
	}

	// 创建凭据被停用也要用它: 停用只影响新请求选路, 任务仍在这把凭据名下。
	env.updateChannel(t, func(d *model.ChannelDetail) {
		d.Keys = []model.ChannelKeyConfig{
			{Name: "k1", Key: env.key1.Key, Enabled: true},
			{Name: "k2", Key: env.key2.Key, Enabled: false},
		}
	})
	stillWorks, _, err := buildVideoTaskRequest(ctx, job, "video_task_1", false)
	if err != nil {
		t.Fatalf("disabled creation credential must still reach its own task: %v", err)
	}
	if stillWorks.Auth.APIKey != env.key2.Key {
		t.Fatalf("query must keep using the creation credential k2, got %v", stillWorks.Auth)
	}

	// 授权绑定的凭据停用后, 老记录不能再拿它定位(旧行为), 也不退到别的凭据。
	env.updateChannel(t, func(d *model.ChannelDetail) {
		d.Keys = []model.ChannelKeyConfig{
			{Name: "k1", Key: env.key1.Key, Enabled: false},
			{Name: "k2", Key: env.key2.Key, Enabled: false},
		}
	})
	if _, _, err := buildVideoTaskRequest(ctx, legacy, "video_task_1", false); err == nil {
		t.Fatal("legacy job must not fall back to a disabled grant credential")
	}
	if stillWorks, _, err = buildVideoTaskRequest(ctx, job, "video_task_1", false); err != nil {
		t.Fatalf("creation credential path must survive the grant credential outage: %v", err)
	} else if stillWorks.Auth.APIKey != env.key2.Key {
		t.Fatalf("query must keep using the creation credential k2, got %v", stillWorks.Auth)
	}

	// 创建凭据被删除: 没有凭据能代表那家上游, 不退到别的凭据。
	env.updateChannel(t, func(d *model.ChannelDetail) {
		d.Keys = []model.ChannelKeyConfig{{Name: "k1", Key: env.key1.Key, Enabled: true}}
	})
	if _, _, err := buildVideoTaskRequest(ctx, job, "video_task_1", false); err == nil {
		t.Fatal("deleted creation credential must error instead of falling back to another credential")
	}

	// 记录的凭据属于别的渠道: 同样不顶替。
	other, err := op.ChannelCreate(&model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{Name: env.channelName + "-other", Enabled: true, BaseURL: "http://upstream.invalid"},
		Keys:          []model.ChannelKeyConfig{{Name: "ok", Key: "sk-other", Enabled: true}},
		Models:        []string{env.modelName + "-other"},
		Grants:        []model.ChannelGrantConfig{{ModelName: env.modelName + "-other", KeyName: "ok", Protocols: model.ProtocolOpenAIVideo}},
	}, env.scope, ctx)
	if err != nil {
		t.Fatalf("create other channel: %v", err)
	}
	otherKeys := op.ChannelKeysByChannel(other.ID)
	if len(otherKeys) != 1 {
		t.Fatalf("other channel keys = %+v", otherKeys)
	}
	foreign := job
	foreign.ChannelKeyID = otherKeys[0].ID
	if _, _, err := buildVideoTaskRequest(ctx, foreign, "video_task_1", false); err == nil {
		t.Fatal("a credential from another channel must not be used")
	}
	unknown := job
	unknown.ChannelKeyID = 999999
	if _, _, err := buildVideoTaskRequest(ctx, unknown, "video_task_1", false); err == nil {
		t.Fatal("unknown credential id must error")
	}
}

// TestVideoTaskRequestRecoversAfterGrantDeletion ensures routine grant-matrix edits do not strand paid tasks.
func TestVideoTaskRequestRecoversAfterGrantDeletion(t *testing.T) {
	env := newVideoTaskEnv(t)
	job := model.VideoJob{JobID: "video_task_deleted_grant", UserID: env.userID, GroupID: 7, ChannelGrantID: env.grantID, ChannelID: env.channelID, ChannelKeyID: env.key2.ID, ModelName: env.modelName}
	env.updateChannel(t, func(d *model.ChannelDetail) {
		d.Grants = nil
	})
	request, channel, err := buildVideoTaskRequest(context.Background(), job, job.JobID, false)
	if err != nil {
		t.Fatalf("task should recover after grant deletion: %v", err)
	}
	if channel.ID != env.channelID || request.Auth == nil || request.Auth.APIKey != env.key2.Key {
		t.Fatalf("recovered task used wrong channel or credential: channel=%d auth=%v", channel.ID, request.Auth)
	}
	deleteRequest, _, err := buildVideoTaskRequest(context.Background(), job, job.JobID, true)
	if err != nil || deleteRequest.Auth == nil || deleteRequest.Auth.APIKey != env.key2.Key {
		t.Fatalf("delete must recover with creation credential: err=%v auth=%v", err, deleteRequest.Auth)
	}
}

// forwardVideoCreate 按 /v1/videos 路由的注册顺序跑「时长闸门 → 转发」链, 返回响应.
// 与真实路由一致: 闸门拒绝即中止, 放行才进入转发流程。
func forwardVideoCreate(t *testing.T, userID uint, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	c.Request = req
	c.Set("api_key_user_id", int(userID))
	c.Set("api_key_id", 0)
	VideoCreateDurationGuard()(c)
	if !c.IsAborted() {
		Forward(llm.APIFormatOpenAIVideo)(c)
	}
	return w
}

// TestVideoCreateDurationGuardRejectsUnsafeSeconds 钉住派发前的时长闸门:
// 声明了非法 seconds 的生视频请求以 400 拒绝, 上游一个请求都收不到 —— 拒绝发生在派发之前,
// 既不冻结余额也不转发, 计费时长与转发时长始终同一套口径。
// 合法请求原样放行(顺带证明正文交还无损: 转发流程还能读到 model 与 seconds)。
func TestVideoCreateDurationGuardRejectsUnsafeSeconds(t *testing.T) {
	srv, posts := newUpstreamStub(http.StatusOK, `{"id":"video_1","status":"queued","seconds":"4","size":"1280x720"}`)
	defer srv.Close()
	env := newRelayGroup(t, srv.URL, model.ProtocolOpenAIVideo, model.MediaKindVideo, 1,
		model.DefaultGroupRelayConfigForMode(model.GroupModeFailover))

	for _, seconds := range []string{`"NaN"`, `"Inf"`, `"-Inf"`, `1e15`, `14400.5`, `0`, `-5`, `true`, `"abc"`} {
		body := fmt.Sprintf(`{"model":%q,"prompt":"a cat","seconds":%s,"size":"1280x720"}`, env.group, seconds)
		w := forwardVideoCreate(t, env.userID, body, nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("seconds=%s: want 400 before dispatch, got %d body %s", seconds, w.Code, w.Body.String())
		}
		if got := posts.Load(); got != 0 {
			t.Fatalf("seconds=%s: unsafe duration reached upstream (%d POSTs)", seconds, got)
		}
		if !strings.Contains(w.Body.String(), "seconds") {
			t.Fatalf("seconds=%s: rejection must explain the duration problem, got %s", seconds, w.Body.String())
		}
	}

	// 未声明时长照旧放行(不强制声明, 只拒绝声明了却非法)。
	body := fmt.Sprintf(`{"model":%q,"prompt":"a cat","size":"1280x720"}`, env.group)
	if w := forwardVideoCreate(t, env.userID, body, nil); w.Code != http.StatusOK {
		t.Fatalf("undeclared duration must pass, got %d body %s", w.Code, w.Body.String())
	}

	// 合法时长原样放行: 正文交还后转发流程读到 model 并真正派发到上游。
	body = fmt.Sprintf(`{"model":%q,"prompt":"a cat","seconds":"4","size":"1280x720"}`, env.group)
	w := forwardVideoCreate(t, env.userID, body, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("valid request must pass the guard, got %d body %s", w.Code, w.Body.String())
	}
	if got := posts.Load(); got != 2 {
		t.Fatalf("valid requests must reach upstream, got %d POSTs", got)
	}
}

// TestVideoCreateDurationGuardSeesThroughGzip 钉住压缩正文不能绕过闸门:
// 闸门按 Content-Encoding 解开后校验(与上游读取同一套解码), 同时原始压缩字节原样交还转发流程。
func TestVideoCreateDurationGuardSeesThroughGzip(t *testing.T) {
	srv, posts := newUpstreamStub(http.StatusOK, `{"id":"video_1","status":"queued"}`)
	defer srv.Close()
	env := newRelayGroup(t, srv.URL, model.ProtocolOpenAIVideo, model.MediaKindVideo, 1,
		model.DefaultGroupRelayConfigForMode(model.GroupModeFailover))

	gzipBody := func(body string) string {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		if _, err := zw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	headers := map[string]string{"Content-Encoding": "gzip"}

	bad := gzipBody(fmt.Sprintf(`{"model":%q,"prompt":"a cat","seconds":"NaN"}`, env.group))
	w := forwardVideoCreate(t, env.userID, bad, headers)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("gzipped unsafe duration must be rejected, got %d body %s", w.Code, w.Body.String())
	}
	if got := posts.Load(); got != 0 {
		t.Fatalf("gzipped unsafe duration reached upstream (%d POSTs)", got)
	}

	good := gzipBody(fmt.Sprintf(`{"model":%q,"prompt":"a cat","seconds":"4"}`, env.group))
	w = forwardVideoCreate(t, env.userID, good, headers)
	if w.Code != http.StatusOK {
		t.Fatalf("gzipped valid request must pass and be dispatched, got %d body %s", w.Code, w.Body.String())
	}
	if got := posts.Load(); got != 1 {
		t.Fatalf("gzipped valid request must reach upstream once, got %d POSTs", got)
	}
}

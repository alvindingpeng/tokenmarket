package migrate

import (
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// fingerprint046TestDB 建一份"老库到本迁移之前"的库: channels / channel_models / audit_logs 由
// AutoMigrate 建出(生产里迁移 46 跑在 AutoMigrate 之后, 顺序一致), 但 config_fingerprint 里放的是
// **旧口径**算出的值 —— 也就是"没把 openai_video_base_path / sub_platform 算进去"的那一份。
func fingerprint046TestDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := conn.AutoMigrate(&model.Channel{}, &model.ChannelModel{}, &model.AuditLog{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return conn
}

// seed046Channel 建一条渠道与它的模型清单, 指纹先写成"迁移前那一份"(调用方给值)。
func seed046Channel(t *testing.T, conn *gorm.DB, channel model.Channel, modelNames []string) model.Channel {
	t.Helper()
	if err := conn.Create(&channel).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	for _, name := range modelNames {
		if err := conn.Create(&model.ChannelModel{ChannelID: channel.ID, Name: name}).Error; err != nil {
			t.Fatalf("seed channel model %s: %v", name, err)
		}
	}
	return channel
}

// TestMigration046RecomputesApprovedBaselines 只重算"已过审 且 已发布"渠道的指纹, 且新值必须等于
// **用库内真实数据**跑 model.ChannelConfigFingerprint 得到的那一份。
//
// 为什么必须逐字相等: 迁移写进去的是基线, 运行期每次保存都拿它比对。差一个字节,
// 下一次人工保存就会把一个什么都没改的已审渠道判成 stale —— 这就是"修好漏审, 换来大面积误停审"。
func TestMigration046RecomputesApprovedBaselines(t *testing.T) {
	conn := fingerprint046TestDB(t)

	approved := seed046Channel(t, conn, model.Channel{
		ChannelConfig: model.ChannelConfig{
			Name:                      "approved-shared",
			Dialect:                   model.DialectGeminiCLI,
			BaseURL:                   "https://upstream.example.com",
			OpenAIChatCompletionPath:  "/v1/chat/completions",
			OpenAIResponsePath:        "/v1/responses",
			OpenAIImageGenerationPath: "/v1/images/generations",
			OpenAIImageEditPath:       "/v1/images/edits",
			OpenAIVideoBasePath:       "/v1beta",
			AnthropicMessagePath:      "/v1/messages",
			SubPlatform:               "",
		},
		ReviewStatus:      model.ChannelReviewApproved,
		Shared:            true,
		ConfigFingerprint: "legacy-fingerprint-without-supply-fields",
	}, []string{"gpt-5", "claude-x"})

	// 这三条必须一动不动: draft 没有基线语义; stale 是"有待复审的失配态", 重算会把待复审抹成已审;
	// 未发布(shared=0)的已审渠道不在公开面, 也不该被本迁移改写。
	draft := seed046Channel(t, conn, model.Channel{
		ChannelConfig:     model.ChannelConfig{Name: "draft", BaseURL: "https://draft.example.com"},
		ReviewStatus:      model.ChannelReviewDraft,
		ConfigFingerprint: "draft-fingerprint",
	}, []string{"m2"})
	stale := seed046Channel(t, conn, model.Channel{
		ChannelConfig:     model.ChannelConfig{Name: "stale", BaseURL: "https://stale.example.com"},
		ReviewStatus:      model.ChannelReviewStale,
		Shared:            true,
		ConfigFingerprint: "stale-fingerprint",
	}, []string{"m3"})
	unshared := seed046Channel(t, conn, model.Channel{
		ChannelConfig:     model.ChannelConfig{Name: "unshared", BaseURL: "https://unshared.example.com"},
		ReviewStatus:      model.ChannelReviewApproved,
		Shared:            false,
		ConfigFingerprint: "unshared-fingerprint",
	}, []string{"m4"})

	// 修前前提: 库里那份基线**不等于**新口径算出来的值, 否则下面的断言证明不了任何事。
	// 期望值按**排序后**的清单算, 而库里插入顺序是 gpt-5 -> claude-x: 迁移必须自己归一, 不能靠库的返回序。
	want := model.ChannelConfigFingerprint(approved.ChannelConfig, []string{"claude-x", "gpt-5"})
	if approved.ConfigFingerprint == want {
		t.Fatalf("修前前提不成立: 旧基线已等于新口径值(%s), 本用例测不到重算", want)
	}

	if err := Migration046_ChannelFingerprintSupplyFields(conn); err != nil {
		t.Fatalf("migration 46: %v", err)
	}

	var got model.Channel
	if err := conn.First(&got, approved.ID).Error; err != nil {
		t.Fatalf("reload approved channel: %v", err)
	}
	if got.ConfigFingerprint != want {
		t.Fatalf("已过审渠道的指纹未按新口径重算: got %s want %s", got.ConfigFingerprint, want)
	}
	// 只动一列: 审核状态, 发布状态与配置本身都不得被顺手改掉。
	if got.ReviewStatus != model.ChannelReviewApproved || !got.Shared {
		t.Fatalf("迁移改了审核/发布状态: status=%s shared=%v", got.ReviewStatus, got.Shared)
	}
	if got.OpenAIVideoBasePath != "/v1beta" || got.BaseURL != approved.BaseURL {
		t.Fatalf("迁移改了配置列: video=%q base=%q", got.OpenAIVideoBasePath, got.BaseURL)
	}

	for _, untouched := range []model.Channel{draft, stale, unshared} {
		var row model.Channel
		if err := conn.First(&row, untouched.ID).Error; err != nil {
			t.Fatalf("reload %s: %v", untouched.Name, err)
		}
		if row.ConfigFingerprint != untouched.ConfigFingerprint {
			t.Fatalf("迁移改写了不该处理的渠道 %q 的基线: %s -> %s",
				untouched.Name, untouched.ConfigFingerprint, row.ConfigFingerprint)
		}
		if row.ReviewStatus != untouched.ReviewStatus {
			t.Fatalf("迁移把渠道 %q 的审核状态改成了 %s(原本 %s) —— 迁移不替管理员做复审决定",
				untouched.Name, row.ReviewStatus, untouched.ReviewStatus)
		}
	}

	// 留痕: 审计里必须能查到"基线被整体推进过、推进了几条、因为什么"。
	var audits []model.AuditLog
	if err := conn.Where("action = ?", auditActionChannelFingerprintBaseline).Find(&audits).Error; err != nil {
		t.Fatalf("load audit: %v", err)
	}
	if len(audits) != 1 {
		t.Fatalf("基线审计条数 = %d, 期望 1", len(audits))
	}
	if audits[0].Target != "channels" {
		t.Fatalf("基线审计 target = %q, 期望 channels", audits[0].Target)
	}
	if !strings.Contains(audits[0].Detail, "recomputed=1") {
		t.Fatalf("基线审计 detail 未记录重算条数: %q", audits[0].Detail)
	}
}

// TestMigration046IsIdempotent 重跑必须零副作用: 不重写指纹, 不再写第二条审计。
//
// 生产里迁移失败后服务会重启, 同一个 Up 会被再跑一遍; 它必须是个空操作,
// 否则"每次启动都往审计表里塞一行基线推进"会把真正的审计记录埋掉。
func TestMigration046IsIdempotent(t *testing.T) {
	conn := fingerprint046TestDB(t)
	approved := seed046Channel(t, conn, model.Channel{
		ChannelConfig: model.ChannelConfig{
			Name:                "approved-shared",
			Dialect:             model.DialectSubscription,
			BaseURL:             "https://daily-cloudcode-pa.googleapis.com",
			SubPlatform:         "gemini",
			OpenAIVideoBasePath: "/v1",
		},
		ReviewStatus:      model.ChannelReviewApproved,
		Shared:            true,
		ConfigFingerprint: "legacy-fingerprint-without-supply-fields",
	}, []string{"m1"})

	if err := Migration046_ChannelFingerprintSupplyFields(conn); err != nil {
		t.Fatalf("first run: %v", err)
	}
	var first model.Channel
	if err := conn.First(&first, approved.ID).Error; err != nil {
		t.Fatal(err)
	}
	var firstAudits int64
	if err := conn.Model(&model.AuditLog{}).Where("action = ?", auditActionChannelFingerprintBaseline).Count(&firstAudits).Error; err != nil {
		t.Fatal(err)
	}

	if err := Migration046_ChannelFingerprintSupplyFields(conn); err != nil {
		t.Fatalf("second run: %v", err)
	}
	var second model.Channel
	if err := conn.First(&second, approved.ID).Error; err != nil {
		t.Fatal(err)
	}
	var secondAudits int64
	if err := conn.Model(&model.AuditLog{}).Where("action = ?", auditActionChannelFingerprintBaseline).Count(&secondAudits).Error; err != nil {
		t.Fatal(err)
	}
	if first.ConfigFingerprint != second.ConfigFingerprint {
		t.Fatalf("第二次运行改了指纹: %s -> %s", first.ConfigFingerprint, second.ConfigFingerprint)
	}
	if firstAudits != 1 || secondAudits != 1 {
		t.Fatalf("重跑写了多余审计: first=%d second=%d, 期望都停在 1", firstAudits, secondAudits)
	}
	if want := model.ChannelConfigFingerprint(approved.ChannelConfig, []string{"m1"}); second.ConfigFingerprint != want {
		t.Fatalf("订阅渠道基线未按新口径重算: got %s want %s", second.ConfigFingerprint, want)
	}
}

// TestMigration046SkipsMissingTable 表不存在时安全跳过: 不报错, 也不凭空建表。
func TestMigration046SkipsMissingTable(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := Migration046_ChannelFingerprintSupplyFields(conn); err != nil {
		t.Fatalf("migration on empty db: %v", err)
	}
	if conn.Migrator().HasTable("channels") {
		t.Fatalf("迁移不该凭空建表")
	}
}

// TestMigration046SkipsMissingColumn 表在、但缺关键列时安全跳过(不猜值, 不写半个基线)。
//
// 缺列时用空串凑一个"看起来对"的指纹是最坏的选择: 它会与运行期算出的值不等,
// 于是全部已审渠道在下一次保存时转 stale —— 而原因藏在迁移里, 现场完全查不出来。
func TestMigration046SkipsMissingColumn(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// 手工建一张"老形态"的 channels: 没有 sub_platform / openai_video_base_path 两列。
	type channelWithoutSupplyColumns struct {
		ID                int `gorm:"primaryKey"`
		Name              string
		Dialect           string
		BaseURL           string
		ReviewStatus      string
		Shared            bool
		ConfigFingerprint string
	}
	if err := conn.Table("channels").AutoMigrate(&channelWithoutSupplyColumns{}); err != nil {
		t.Fatalf("create legacy channels table: %v", err)
	}
	if err := conn.Table("channels").Create(&channelWithoutSupplyColumns{
		Name: "legacy", Dialect: "generic", BaseURL: "https://legacy.example.com",
		ReviewStatus: model.ChannelReviewApproved, Shared: true, ConfigFingerprint: "legacy-fingerprint",
	}).Error; err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	if err := Migration046_ChannelFingerprintSupplyFields(conn); err != nil {
		t.Fatalf("migration should skip, got error: %v", err)
	}
	var fingerprints []string
	if err := conn.Table("channels").Pluck("config_fingerprint", &fingerprints).Error; err != nil {
		t.Fatalf("pluck fingerprints: %v", err)
	}
	if len(fingerprints) != 1 || fingerprints[0] != "legacy-fingerprint" {
		t.Fatalf("缺列时仍改写了基线: %v —— 缺列必须整条跳过, 不能猜值", fingerprints)
	}
}

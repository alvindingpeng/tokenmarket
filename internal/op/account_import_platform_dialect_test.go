package op

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// seedImportChannelWith 建一个指定方言(与订阅平台)的渠道, 并可选择是否放进缓存。
//
// 缓存开关是刻意做成参数的: 平台校验既要读缓存(转发的事实来源), 也要能在缓存没灌好时
// 回退到库 —— 两条路径都必须有用例, 否则"缓存缺失就静默放行"这种退化不会被任何测试挡住。
func seedImportChannelWith(t *testing.T, dialect model.Dialect, subPlatform string, inCache bool) model.Channel {
	t.Helper()
	channel := model.Channel{
		UserID: 1,
		ChannelConfig: model.ChannelConfig{
			Name:        "dialect-" + time.Now().Format("150405.000000000"),
			Enabled:     true,
			BaseURL:     "https://api.example.com",
			Dialect:     dialect,
			SubPlatform: subPlatform,
		},
	}
	if err := db.GetDB().Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if inCache {
		channelCache.Set(channel.ID, channel)
	}
	return channel
}

func countChannelKeys(t *testing.T, channelID int) int64 {
	t.Helper()
	var count int64
	if err := db.GetDB().Model(&model.ChannelKey{}).Where("channel_id = ?", channelID).Count(&count).Error; err != nil {
		t.Fatalf("count keys: %v", err)
	}
	return count
}

// 订阅渠道只接受 antigravity 平台的凭据。
//
// 若不拦: 凭据能导入成功, 失败要等到转发那一刻才暴露 —— 而 relay 的 ProtocolGeminiCLI 分支
// 会按"凭据平台不对"硬拒并熔断该凭据 30 分钟。使用者看到的是"导入成功, 一用就挂,
// 而且要等半小时才恢复", 排查方向完全错。
func TestAccountImportRejectsPlatformMismatchOnSubscriptionChannel(t *testing.T) {
	accountTestDB(t)
	channel := seedImportChannelWith(t, model.DialectSubscription, "gemini", true)

	_, err := AccountImport(context.Background(), channel.ID, AccountImportSpec{
		Platform: model.AccountPlatformCodex, Name: "codex-into-sub",
		Raw: `{"access_token":"at","refresh_token":"rt"}`, Enabled: true,
	}, time.Now())
	if err == nil {
		t.Fatal("订阅渠道导入了 codex 凭据, 应当在写入前拒绝")
	}
	// 错误里必须同时点名两个平台: 只说"平台不匹配"使用者不知道该改成哪个。
	if !strings.Contains(err.Error(), model.AccountPlatformAntigravity) {
		t.Fatalf("错误没说要哪个平台: %v", err)
	}
	if !strings.Contains(err.Error(), model.AccountPlatformCodex) {
		t.Fatalf("错误没说当前是哪个平台: %v", err)
	}
	// 必须是"提交内容不合法", 让接口层按 400 而不是 500 处理。
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("应为 InvalidInputError(接口层按 400 处理), got %T", err)
	}
	if got := countChannelKeys(t, channel.ID); got != 0 {
		t.Fatalf("被拒的导入不应落库, 实际写入 %d 条", got)
	}
}

// gemini_cli 方言是同一个硬约束的另一入口: 它的出站转换器同样是 Gemini CLI 的 RPC 信封。
func TestAccountImportRejectsPlatformMismatchOnGeminiCLIChannel(t *testing.T) {
	accountTestDB(t)
	channel := seedImportChannelWith(t, model.DialectGeminiCLI, "", true)

	_, err := AccountImport(context.Background(), channel.ID, AccountImportSpec{
		Platform: model.AccountPlatformClaude, Name: "claude-into-geminicli",
		Raw: `{"claudeAiOauth":{"accessToken":"at","refreshToken":"rt"}}`, Enabled: true,
	}, time.Now())
	if err == nil {
		t.Fatal("gemini_cli 渠道导入了 claude 凭据, 应当在写入前拒绝")
	}
	if !strings.Contains(err.Error(), model.AccountPlatformAntigravity) || !strings.Contains(err.Error(), model.AccountPlatformClaude) {
		t.Fatalf("错误应同时点名 antigravity 与 claude: %v", err)
	}
	if got := countChannelKeys(t, channel.ID); got != 0 {
		t.Fatalf("被拒的导入不应落库, 实际写入 %d 条", got)
	}
}

// 缓存没灌好(进程刚起、渠道刚建未回填)时, 校验必须回退到库 —— 否则订阅渠道在缓存为空那一刻
// 会放行任何平台的凭据, 而这正是最容易被批量脚本踩中的窗口。
func TestAccountImportRejectsMismatchWhenChannelOnlyInDB(t *testing.T) {
	accountTestDB(t)
	channel := seedImportChannelWith(t, model.DialectSubscription, "gemini", false)
	if _, ok := channelCache.Get(channel.ID); ok {
		t.Fatal("这个用例要求渠道不在缓存里")
	}

	_, err := AccountImport(context.Background(), channel.ID, AccountImportSpec{
		Platform: model.AccountPlatformGemini, Name: "gemini-into-sub",
		Raw: `{"access_token":"at","refresh_token":"rt"}`, Enabled: true,
	}, time.Now())
	if err == nil {
		t.Fatal("缓存缺失时也要能判定方言(gemini 凭据同样不能进订阅渠道), 实际放行了")
	}
	if !strings.Contains(err.Error(), model.AccountPlatformAntigravity) {
		t.Fatalf("错误没说要哪个平台: %v", err)
	}
}

// 反方向的对照: 订阅渠道 + antigravity 凭据是**唯一**正确组合, 绝不能连它一起挡掉。
// 若这条变红, 说明校验写成了"方言订阅就一律拒绝", 整条订阅链路会当场不可用。
func TestAccountImportAllowsAntigravityOnSubscriptionChannel(t *testing.T) {
	accountTestDB(t)
	channel := seedImportChannelWith(t, model.DialectSubscription, "gemini", true)

	result, err := AccountImport(context.Background(), channel.ID, AccountImportSpec{
		Platform: model.AccountPlatformAntigravity, Name: "anti-ok",
		Raw: `{"access_token":"at","refresh_token":"rt"}`, Enabled: true, SkipRefresh: true,
	}, time.Now())
	if err != nil {
		t.Fatalf("订阅渠道应接受 antigravity 凭据: %v", err)
	}
	if result == nil || result.ChannelKeyID == 0 {
		t.Fatal("应当真的写入了一条凭据")
	}
	if result.Platform != model.AccountPlatformAntigravity {
		t.Fatalf("落库平台 = %q, want antigravity", result.Platform)
	}
}

// 反方向的对照(抗过度拦截): 普通渠道 + antigravity 凭据必须放行。
//
// 前端 accountPlatform.ts 刻意只挡"订阅/gemini_cli 方言选了别的平台"这一个方向:
// 反方向(普通渠道挂 antigravity 凭据)是能转发的 —— 只要某条授权带 GeminiCLI 协议位,
// antigravity 的凭据就能出去。后端若图省事写成"antigravity 只能进订阅渠道", 会把这类
// 合法配置一并挡死。
func TestAccountImportAllowsAntigravityOnGenericChannel(t *testing.T) {
	accountTestDB(t)
	channel := seedImportChannelWith(t, model.DialectGeneric, "", false)

	_, err := AccountImport(context.Background(), channel.ID, AccountImportSpec{
		Platform: model.AccountPlatformAntigravity, Name: "anti-generic",
		Raw: `{"access_token":"at","refresh_token":"rt"}`, Enabled: true, SkipRefresh: true,
	}, time.Now())
	if err != nil {
		t.Fatalf("普通渠道应接受 antigravity 凭据(反方向不挡): %v", err)
	}
}

// 批量导入走同一份校验, 且失败必须是**逐条**的: 一批里每条都要带自己的原因,
// 不能让第一条的失败把整批吞掉。
func TestAccountImportBatchRejectsMismatchPerItem(t *testing.T) {
	accountTestDB(t)
	channel := seedImportChannelWith(t, model.DialectSubscription, "gemini", true)

	results := AccountImportBatch(context.Background(), channel.ID, model.AccountPlatformCodex, []AccountBatchItem{
		{Name: "batch-a", Raw: `{"access_token":"at1","refresh_token":"rt1"}`},
		{Name: "batch-b", Raw: `{"access_token":"at2","refresh_token":"rt2"}`},
	}, true, time.Now())

	if len(results) != 2 {
		t.Fatalf("应得 2 条结果, 得到 %d", len(results))
	}
	for i, result := range results {
		if result.OK {
			t.Fatalf("第 %d 条应当被拒: %+v", i, result)
		}
		if result.ChannelKeyID != 0 {
			t.Fatalf("第 %d 条被拒却带了凭据 ID: %+v", i, result)
		}
		if !strings.Contains(result.Error, model.AccountPlatformAntigravity) || !strings.Contains(result.Error, model.AccountPlatformCodex) {
			t.Fatalf("第 %d 条的错误应同时点名两个平台: %q", i, result.Error)
		}
	}
	if got := countChannelKeys(t, channel.ID); got != 0 {
		t.Fatalf("被拒的批量导入不应落库, 实际写入 %d 条", got)
	}
}

// 新校验不能破坏批量的逐条隔离: 一条坏 JSON 不能连累同批里合法的账号。
func TestAccountImportBatchKeepsIsolationWithPlatformCheck(t *testing.T) {
	accountTestDB(t)
	channel := seedImportChannelWith(t, model.DialectGeneric, "", true)

	results := AccountImportBatch(context.Background(), channel.ID, model.AccountPlatformAntigravity, []AccountBatchItem{
		{Name: "iso-good-1", Raw: `{"access_token":"at1","refresh_token":"rt1"}`},
		{Name: "iso-bad", Raw: "not json"},
		{Name: "iso-good-2", Raw: `{"access_token":"at2","refresh_token":"rt2"}`},
	}, true, time.Now())

	if len(results) != 3 {
		t.Fatalf("应得 3 条结果, 得到 %d", len(results))
	}
	if !results[0].OK || !results[2].OK {
		t.Fatalf("合法条目应成功(不被平台校验误伤): %+v / %+v", results[0], results[2])
	}
	if results[1].OK || results[1].Error == "" {
		t.Fatalf("坏 JSON 条目应失败并带原因: %+v", results[1])
	}
}

// 方言 -> 强制平台的对应关系单独钉一遍: 它是"库里改一个方言值就静默改变导入行为"的那处开关。
func TestAccountPlatformRequiredByChannel(t *testing.T) {
	cases := []struct {
		name   string
		config model.ChannelConfig
		want   string
	}{
		{"订阅渠道经平台描述取", model.ChannelConfig{Dialect: model.DialectSubscription, SubPlatform: "gemini"}, model.AccountPlatformAntigravity},
		{"订阅渠道未登记平台时按方言兜底", model.ChannelConfig{Dialect: model.DialectSubscription, SubPlatform: "未知平台"}, model.AccountPlatformAntigravity},
		{"gemini_cli 方言", model.ChannelConfig{Dialect: model.DialectGeminiCLI}, model.AccountPlatformAntigravity},
		{"普通方言不限平台", model.ChannelConfig{Dialect: model.DialectGeneric}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := accountPlatformRequiredByChannel(c.config); got != c.want {
				t.Fatalf("required = %q, want %q", got, c.want)
			}
		})
	}
}

// 渠道彻底查不到(缓存没有、库里也没有)时不拒绝: 这里拒绝只会把"缓存/库状态一时不可知"
// 伪装成"平台不匹配", 而真正的结局该由落库那一步给出(归属/外键校验)。
func TestValidateAccountPlatformForChannelUnknownChannelNotRejected(t *testing.T) {
	accountTestDB(t)
	if err := ValidateAccountPlatformForChannel(context.Background(), 999999, model.AccountPlatformCodex); err != nil {
		t.Fatalf("渠道不可知时不应以平台不匹配为由拒绝: %v", err)
	}
}

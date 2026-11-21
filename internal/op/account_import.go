package op

// 上游账号 JSON 的导入。
//
// 导入要解决的核心问题是"用户手上是什么形状, 我们能不能吃下": 平铺的、CLI 导出的、
// 各平台字段名不同的都有。解析层已经两种形状都吃, 这一层负责把结果落到凭据列上,
// 并保证"不改动用户给的原始 JSON 里我们不需要动的部分"。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/subplatform"
)

// AccountImportSpec 一次导入请求。
type AccountImportSpec struct {
	Platform string // codex | kiro | gemini | grok | claude
	Name     string // 凭据名(渠道内唯一)
	Raw      string // 账号 JSON 原文
	Enabled  bool
	Priority int
	// SkipRefresh 跳过导入后的首刷。批量导入用它: 逐条首刷会 N 次上游调用,
	// 又慢又可能触发风控, 而批量的语义是快速录入 —— 验证留给账号页统一刷新。
	// 单条导入不设(零值 false), 保持"导入即验证"的原语义。
	SkipRefresh bool
}

// AccountImportResult 导入结果, 回给界面展示。
type AccountImportResult struct {
	ChannelKeyID int
	Name         string
	Platform     string
	Kind         string
	Email        string
	// ExpiresAt 是导入时已知的到期时间; 空表示未知(不代表永久有效)。
	ExpiresAt *time.Time
	// RefreshedNow 表示导入时顺手刷了一次(仅 oauth 且已有续期令牌时)。
	RefreshedNow bool
}

// AccountImport 校验并写入一条账号凭据。
//
// 校验顺序很重要: 先解析(能不能读)再判断续期能力(能不能自动维持), 最后才落库。
// 若先落库, 一条读不出来的 JSON 会变成一把"看起来存在但从不能转发"的凭据,
// 而使用者只看到导入成功。
func AccountImport(ctx context.Context, channelID int, spec AccountImportSpec, now time.Time) (*AccountImportResult, error) {
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return nil, fmt.Errorf("凭据名不能为空")
	}
	if len(name) > 64 {
		return nil, fmt.Errorf("凭据名过长(上限 64 字符)")
	}
	raw := strings.TrimSpace(spec.Raw)
	if raw == "" {
		return nil, fmt.Errorf("账号 JSON 不能为空")
	}
	// 先确认它是合法 JSON 对象, 再交给平台解析器 —— 后者对"不是对象"的错误信息不够具体。
	var probe map[string]any
	if err := json.Unmarshal([]byte(raw), &probe); err != nil || probe == nil {
		return nil, fmt.Errorf("账号 JSON 必须是 JSON 对象")
	}

	credential, err := model.AccountJSONParse(spec.Platform, []byte(raw))
	if err != nil {
		return nil, err
	}

	// 平台与渠道方言不匹配的凭据必须在写入前拦下。三个入口(单条导入、登录完成后的导入、
	// 批量导入)都汇聚到这个函数, 所以闸门放在这里就够了 —— 接口层只负责把 InvalidInputError
	// 翻成 400。这里查的是解析后真正要落库的 credential.Platform(而不是请求里的 spec.Platform),
	// 因为 relay 读的是库里那一列: 两者若被解析层改写, 被拦下的必须是实际生效的值。
	if err := ValidateAccountPlatformForChannel(ctx, channelID, credential.Platform); err != nil {
		return nil, err
	}

	// cookie 类凭据没有续期能力: 允许导入(它们也能转发), 但必须让调用方知道
	// "这个账号失效后无法自动恢复"。把这件事放在返回值里而不是拒绝导入。
	enabled := spec.Enabled
	expiresAt := credential.ExpiresAt
	var tokenExpiresAt *time.Time
	if !expiresAt.IsZero() {
		tokenExpiresAt = &expiresAt
	}

	key := model.ChannelKey{
		ChannelID: channelID,
		ChannelKeyConfig: model.ChannelKeyConfig{
			Name:            name,
			Key:             raw,
			Enabled:         enabled,
			Priority:        spec.Priority,
			AccountKind:     credential.Kind,
			AccountPlatform: credential.Platform,
			AccountEmail:    credential.Email,
			TokenExpiresAt:  tokenExpiresAt,
		},
	}
	if err := db.GetDB().WithContext(ctx).Create(&key).Error; err != nil {
		// 名称在渠道内唯一: 撞名时说清楚是撞名, 而不是把 SQL 约束错误原样抛给用户。
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("凭据名 %q 在该渠道内已存在", name)
		}
		return nil, fmt.Errorf("保存账号凭据失败: %w", err)
	}

	result := &AccountImportResult{
		ChannelKeyID: key.ID,
		Name:         name,
		Platform:     credential.Platform,
		Kind:         credential.Kind,
		Email:        credential.Email,
		ExpiresAt:    tokenExpiresAt,
	}

	// 先落缓存再刷新, 顺序不能反。
	// 首刷失败是常态(上游不通/账号到期/地区限制), 而那条路径必须**提前返回 warning**;
	// 若缓存写入放在刷新之后, 首刷一失败就永远走不到, 结果是: 库里有这一行、详情接口看不见、
	// 转发也选不中 —— 使用者看到的是"导入成功但渠道里就是没有这个账号"。
	// 转发路径读的是 channelKeyCache, 而 ChannelKeyLookup 只查缓存, 不回写就等于没导入。
	var stored model.ChannelKey
	if err := db.GetDB().WithContext(ctx).First(&stored, key.ID).Error; err != nil {
		return result, fmt.Errorf("导入成功但读取凭据失败: %w", err)
	}
	channelKeyCache.Set(stored.ID, stored)
	result.ExpiresAt = stored.TokenExpiresAt

	// oauth 类若已有续期令牌, 导入时顺手刷一次:
	// 这样导入的账号立刻就能用, 而不必等到下一个刷新周期; 也让"这份 JSON 到底能不能用"
	// 当场就有答案, 而不是导入成功之后才发现跑不通。
	if credential.Kind == model.AccountKindOAuth && credential.RefreshToken != "" && !spec.SkipRefresh {
		if err := AccountRefresh(ctx, key.ID, now); err != nil {
			// 刷新失败**不回滚导入**: 账号 JSON 本身可能是好的, 只是此刻上游不通或到期时间已过。
			// 把失败如实带回去, 让使用者决定是重试还是先去查账号状态。
			// 注意缓存已经写过了, 所以这条凭据此刻在详情与转发里都是可见的(只是令牌还是旧的)。
			return result, &AccountImportRefreshWarning{Err: err}
		}
		result.RefreshedNow = true
		// 刷新成功会写入新令牌与新的到期时间, 重新读一次把最新值带回并刷新缓存。
		var refreshed model.ChannelKey
		if err := db.GetDB().WithContext(ctx).First(&refreshed, key.ID).Error; err == nil {
			channelKeyCache.Set(refreshed.ID, refreshed)
			result.ExpiresAt = refreshed.TokenExpiresAt
		}
	}
	return result, nil
}

// AccountImportRefreshWarning 导入已成功但首刷失败。调用方应把"导入成功但尚未可用"如实告知。
type AccountImportRefreshWarning struct{ Err error }

func (w *AccountImportRefreshWarning) Error() string {
	return fmt.Sprintf("账号已导入, 但首次刷新失败: %v", w.Err)
}

func (w *AccountImportRefreshWarning) Unwrap() error { return w.Err }

// isUniqueViolation 判断是否撞了唯一约束。
//
// 两种判据都要看: gorm 对支持的方言会把错误翻译成 ErrDuplicatedKey(sqlite 会, 但不是所有
// 驱动都对每个语句翻译), 未翻译时只剩驱动给的原文 —— sqlite 是 "UNIQUE constraint failed",
// postgres 是 "duplicate key value violates unique constraint"。只认一种会在另一个方言上失效。
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") || strings.Contains(message, "duplicate key")
}

// accountPlatformRequiredByChannel 返回某渠道方言**只**接受哪个平台的凭据; 空串表示不限。
//
// 对应关系来自转发层的硬约束, 不是这里凭空制定:
//   - subscription: 订阅渠道的方言与出站协议由平台描述(subplatform.Platform)决定,
//     目前唯一登记的订阅平台把 AccountPlatform 钉在 antigravity;
//   - gemini_cli: 该方言的出站是 Gemini CLI 的 v1internal RPC 信封, relay 的
//     ProtocolGeminiCLI 分支只认 antigravity 凭据(gemini 的凭据缺 cclog/experimentsandconfigs,
//     数据面会 403), 所以同样是只此一家。
//
// 其余方言一律不限: 普通渠道挂 antigravity 凭据是能转发的合法配置(只要授权带 GeminiCLI 协议位),
// 前端 accountPlatform.ts 也刻意只挡"订阅/gemini_cli 选了别的平台"这一个方向。
func accountPlatformRequiredByChannel(config model.ChannelConfig) string {
	if config.Dialect.Subscribable() {
		if platform, ok := subplatform.Of(strings.TrimSpace(config.SubPlatform)); ok && platform.AccountPlatform != "" {
			return platform.AccountPlatform
		}
		// 平台名未登记或为空(历史数据/库被手改)时按方言兜底: 订阅方言这套出站协议就是为
		// antigravity 凭据存在的, 不能因为平台名脏了就把校验整条跳过。
		return model.AccountPlatformAntigravity
	}
	if config.Dialect == model.DialectGeminiCLI {
		return model.AccountPlatformAntigravity
	}
	return ""
}

// channelConfigForImport 取出渠道配置: 先查缓存(转发读的就是它, 它才是事实来源), 未命中再查库。
//
// 缓存缺失是真实存在的窗口(进程刚起、渠道刚建还没回填、缓存被清), 这时**不能**当作"不限平台"
// 放行 —— 那等于"越接近冷启动越容易放进坏凭据", 而批量脚本恰好总在这个窗口里跑。因此回退到库。
//
// 只有库也查不到时才放弃校验(返回 false): 那种情况下渠道 ID 本身就是错的, 正确的拒绝理由
// 是"渠道不存在"(接口层按 404 处理), 不是伪装成"平台不匹配"—— 后者会把使用者引向改平台。
func channelConfigForImport(ctx context.Context, channelID int) (model.ChannelConfig, bool) {
	if channel, ok := channelCache.Get(channelID); ok {
		return channel.ChannelConfig, true
	}
	var channel model.Channel
	if err := db.GetDB().WithContext(ctx).First(&channel, channelID).Error; err != nil {
		return model.ChannelConfig{}, false
	}
	return channel.ChannelConfig, true
}

// ValidateAccountPlatformForChannel 确认某渠道的方言能不能吃下这个平台的凭据。
//
// 这是 op 层的最后一道闸: 三个入口(单条 /import、登录完成的导入、/import-batch)都从这里过。
// 少了它, 平台与方言不匹配的凭据能"导入成功", 失败要等到转发那一刻才暴露 —— relay 的
// ProtocolGeminiCLI 分支会按"凭据平台不对"硬拒并把该凭据熔断 30 分钟, 使用者看到的是
// "导入成功, 一用就挂, 还要等半小时才恢复", 排查方向完全错。
//
// 归属与"渠道是否存在"不在这里判断(接口层已经按 404 处理), 这里只管方言与平台。
func ValidateAccountPlatformForChannel(ctx context.Context, channelID int, platform string) error {
	platform = strings.TrimSpace(platform)
	if platform == "" {
		// 平台为空说明凭据里没解析出平台, 由解析层去报更具体的错; 这里不越权判断。
		return nil
	}
	config, knowable := channelConfigForImport(ctx, channelID)
	if !knowable {
		return nil
	}
	required := accountPlatformRequiredByChannel(config)
	if required == "" || required == platform {
		return nil
	}
	// 两个平台名都要说: 只说"平台不匹配"使用者不知道该改成哪个方向。
	return &InvalidInputError{Reason: fmt.Errorf(
		"渠道方言 %q 只接受 %s 平台的账号凭据, 当前提交的是 %s 凭据; "+
			"平台与方言不匹配的凭据能导入但转发时会被拒绝",
		config.Dialect, required, platform)}
}

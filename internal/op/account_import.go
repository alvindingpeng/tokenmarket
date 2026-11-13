
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
)

// AccountImportSpec 一次导入请求。
type AccountImportSpec struct {
	Platform string // codex | kiro | gemini | grok | claude
	Name     string // 凭据名(渠道内唯一)
	Raw      string // 账号 JSON 原文
	Enabled  bool
	Priority int
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

	// oauth 类若已有续期令牌, 导入时顺手刷一次:
	// 这样导入的账号立刻就能用, 而不必等到下一个刷新周期; 也让"这份 JSON 到底能不能用"
	// 当场就有答案, 而不是导入成功之后才发现跑不通。
	if credential.Kind == model.AccountKindOAuth && credential.RefreshToken != "" {
		if err := AccountRefresh(ctx, key.ID, now); err != nil {
			// 刷新失败**不回滚导入**: 账号 JSON 本身可能是好的, 只是此刻上游不通或到期时间已过。
			// 把失败如实带回去, 让使用者决定是重试还是先去查账号状态。
			return result, &AccountImportRefreshWarning{Err: err}
		}
		result.RefreshedNow = true
	}
	// 写入/刷新后都必须把这一行放进缓存: 转发路径读的是 channelKeyCache, 而 ChannelKeyLookup
	// 也只查缓存 —— 不回写的话, 刚导入的凭据在进程内根本不可见, 要等下一次全量缓存重载
	// (通常是重启或渠道变更)才生效, 使用者会以为"导入成功但就是不工作"。
	var stored model.ChannelKey
	if err := db.GetDB().WithContext(ctx).First(&stored, key.ID).Error; err != nil {
		return result, fmt.Errorf("导入成功但读取凭据失败: %w", err)
	}
	channelKeyCache.Set(stored.ID, stored)
	result.ExpiresAt = stored.TokenExpiresAt
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

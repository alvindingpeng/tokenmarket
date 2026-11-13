
package model

// 上游 OAuth 令牌端点规格。
//
// 抽成表而不是散在调用点: 每个平台的 authorize/refresh 端点、client_id、请求体形态、
// 响应字段名都不一样 —— Codex 是 form-urlencoded + snake_case 响应, Kiro 是 JSON +
// camelCase 响应。把这些差异集中成数据, 刷新逻辑就能只写一份。

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// AccountOAuthSpec 描述一个平台的令牌刷新方式。
type AccountOAuthSpec struct {
	Platform string
	TokenURL string
	ClientID string
	// BodyFormat: "form" = application/x-www-form-urlencoded (Codex), "json" = JSON (Kiro)。
	BodyFormat string
	// AccessTokenField / RefreshTokenField 是响应体里的字段名。命名差异就在这里,
	// 不在解析层归一化: 各平台自己说自己的, 不用猜。
	AccessTokenField  string
	RefreshTokenField string
	ExpiresInField    string
	// ExtraFields 是请求体里除 grant_type/refresh_token 之外必须带的字段(如 Kiro 的
	// clientId/clientSecret), 按平台填写。
	ExtraFields func(credential AccountCredential) map[string]string
}

const (
	accountBodyFormatForm = "form"
	accountBodyFormatJSON = "json"
)

// accountOAuthSpecs 平台 -> 令牌端点规格。未登记的平台的 OAuth 刷新会明确失败, 而不是
// 拿一个猜出来的端点去打上游 —— 打错端点最坏的情况是让对方以为我们在暴力破解。
var accountOAuthSpecs = map[string]AccountOAuthSpec{
	AccountPlatformCodex: {
		Platform: AccountPlatformCodex,
		// 端点与 client_id 来自 new-api 的实现(service/codex_oauth.go), 两处代码用的是同一个值。
		TokenURL:          "https://auth.openai.com/oauth/token",
		ClientID:          "app_EMoamEEZ73f0CkXaXp7hrann",
		BodyFormat:        accountBodyFormatForm,
		AccessTokenField:  "access_token",
		RefreshTokenField: "refresh_token",
		ExpiresInField:    "expires_in",
	},
	AccountPlatformKiro: {
		Platform:   AccountPlatformKiro,
		TokenURL:   "https://oidc.us-east-1.amazonaws.com/token",
		BodyFormat: accountBodyFormatJSON,
		// Kiro 用 camelCase 响应字段。
		AccessTokenField:  "accessToken",
		RefreshTokenField: "refreshToken",
		ExpiresInField:    "expiresIn",
		ExtraFields: func(credential AccountCredential) map[string]string {
			// Kiro 的刷新请求把 clientId/clientSecret 直接放进请求体, 不是 Basic 认证。
			return map[string]string{
				"clientId":     credential.ClientID,
				"clientSecret": credential.ClientSecret,
			}
		},
	},
}

// AccountOAuthSpecOf 取平台规格; 未登记返回错误。
func AccountOAuthSpecOf(platform string) (AccountOAuthSpec, error) {
	spec, ok := accountOAuthSpecs[platform]
	if !ok {
		return AccountOAuthSpec{}, fmt.Errorf("平台 %s 没有登记 OAuth 刷新端点", platform)
	}
	// 测试专用覆盖: 刷新路径必须能被指向本地假服务端。
	// 少了这个口子, 单测就会真的往 auth.openai.com 发请求 —— 测试依赖外部网络既不可靠,
	// 又等于在别人的风控体系里制造无意义流量。生产路径永不修改这张表。
	if override, ok := accountOAuthTokenURLOverride[platform]; ok && override != "" {
		spec.TokenURL = override
	}
	return spec, nil
}

// accountOAuthTokenURLOverride 仅测试使用; 生产代码不应写入。
var accountOAuthTokenURLOverride = map[string]string{}

// AccountOAuthTokenURLOverrideSet 测试用: 覆盖某平台的令牌端点(传空串撤销)。
func AccountOAuthTokenURLOverrideSet(platform, tokenURL string) {
	if tokenURL == "" {
		delete(accountOAuthTokenURLOverride, platform)
		return
	}
	accountOAuthTokenURLOverride[platform] = tokenURL
}

// AccountOAuthRefreshRequest 构造刷新请求的请求体与 Content-Type。
func AccountOAuthRefreshRequest(spec AccountOAuthSpec, credential AccountCredential) (contentType string, body []byte, err error) {
	if strings.TrimSpace(credential.RefreshToken) == "" {
		return "", nil, fmt.Errorf("%s: refresh_token 为空, 无法刷新", spec.Platform)
	}
	fields := map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": credential.RefreshToken,
	}
	if spec.ClientID != "" {
		fields["client_id"] = spec.ClientID
	}
	if spec.ExtraFields != nil {
		for key, value := range spec.ExtraFields(credential) {
			if value == "" {
				// 缺必填字段就当场失败: 拿空 clientSecret 去换令牌只会得到一个 401,
				// 而错误信息会指向"账号失效", 把人引到错的方向。
				return "", nil, fmt.Errorf("%s: 刷新请求缺少 %s", spec.Platform, key)
			}
			fields[key] = value
		}
	}

	switch spec.BodyFormat {
	case accountBodyFormatForm:
		form := url.Values{}
		for key, value := range fields {
			form.Set(key, value)
		}
		return "application/x-www-form-urlencoded", []byte(form.Encode()), nil
	case accountBodyFormatJSON:
		encoded, err := json.Marshal(fields)
		if err != nil {
			return "", nil, fmt.Errorf("%s: 请求体编码失败: %w", spec.Platform, err)
		}
		return "application/json", encoded, nil
	default:
		return "", nil, fmt.Errorf("%s: 未知的请求体格式 %q", spec.Platform, spec.BodyFormat)
	}
}

// AccountOAuthRefreshResult 刷新结果。
//
// 注意 RefreshToken: 上游**每次刷新都可能轮换 refresh_token**, 旧值当场失效。
// 因此调用方必须把这里返回的新值原样落库; 漏写就等于账号永久失效, 且下一次刷新
// 会因为"refresh_token 无效"而失败 —— 看起来像账号被封, 实际是回写丢了。
type AccountOAuthRefreshResult struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	// RefreshTokenRotated 表示上游返回了与输入不同的 refresh_token(常规情况);
	// 为 false 说明上游没有轮换, 此时沿用旧值即可。
	RefreshTokenRotated bool
}

// AccountOAuthParseRefreshResponse 解析令牌端点的响应体。
func AccountOAuthParseRefreshResponse(spec AccountOAuthSpec, previous AccountCredential, raw []byte, now time.Time) (*AccountOAuthRefreshResult, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("%s: 刷新响应不是合法 JSON: %w", spec.Platform, err)
	}
	result := &AccountOAuthRefreshResult{
		AccessToken: accountJSONString(payload[spec.AccessTokenField]),
	}
	if result.AccessToken == "" {
		// 把上游的错误码原样带出来(如 invalid_grant): 它决定我们该"重试"还是"标记账号失效"。
		return nil, fmt.Errorf("%s: 刷新响应缺少 %s (上游回复: %s)", spec.Platform, spec.AccessTokenField, accountTruncate(string(raw), 200))
	}
	result.RefreshToken = accountJSONString(payload[spec.RefreshTokenField])
	if result.RefreshToken == "" {
		// 上游没返回新的 refresh_token: 沿用旧的。这种情况合法(未轮换), 但不能当成错误。
		result.RefreshToken = previous.RefreshToken
	} else if result.RefreshToken != previous.RefreshToken {
		result.RefreshTokenRotated = true
	}

	// 到期时间: 优先 expires_in(秒), 没有就从新 access_token 的 JWT exp 推。
	if expiresIn, ok := payload[spec.ExpiresInField].(float64); ok && expiresIn > 0 {
		result.ExpiresAt = now.Add(time.Duration(expiresIn) * time.Second)
	} else {
		result.ExpiresAt = accountJWTExpiry(result.AccessToken)
	}
	return result, nil
}

func accountTruncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "..."
}

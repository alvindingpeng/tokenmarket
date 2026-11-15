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
	"os"
	"strings"
	"time"
)

// GeminiOAuthSettingGetter 从设置库读取字符串值(由 op 包在启动时注入)。
// model 包不能直接依赖 op/db(会形成导入循环), 故用函数变量注入。
// 为 nil 时回退到环境变量与内置默认值。
var GeminiOAuthSettingGetter func(SettingKey) (string, error)

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
	// GrantTypeField 是请求体里"授权类型"那个键的名字。多数平台是 snake_case 的 grant_type,
	// 但 Kiro 的 OIDC 端点是 camelCase 的 grantType —— 写错会得到一个 unsupported_grant_type,
	// 而错误信息看起来像"账号坏了"。空值表示用 grant_type。
	GrantTypeField string
	// RefreshTokenFieldInBody 是请求体里"续期令牌"那个键的名字; 空值表示用 refresh_token。
	// 与 RefreshTokenField(响应字段名)是两个不同的问题: Kiro 请求体用 refreshToken,
	// 响应也用 refreshToken; 而 Claude 请求体用 refresh_token 但凭据文件里存的是 refreshToken。
	RefreshTokenFieldInBody string
	// Headers 是必须随请求一起发的固定头(不含 Content-Type)。
	// Kiro 的 OIDC 端点会检查 AWS SDK 的 user-agent 系列头, 不带来路不明的裸请求;
	// 这不是伪装, 而是该端点对客户端的既有要求 —— 少了它拿不到令牌, 也查不出原因。
	Headers map[string]string
	// Scopes 是需要随刷新请求一起提交的 scope 列表; 空表示不提交。
	// Claude 的刷新请求要求带 scope, 且该值决定新令牌的权限范围 —— 传错会导致拿到的令牌
	// 缺少推理权限, 表现为"刷新成功但调用 403"。
	Scopes []string
	// StoreAccessTokenField / StoreRefreshTokenField 是"凭据文件里"这两个令牌的键名,
	// 与响应字段名未必相同:
	//   Codex 响应/存储都是 access_token;
	//   Kiro  响应/存储都是 accessToken;
	//   Claude 响应是 access_token, 但凭据文件里是 accessToken。
	// 空值回退到响应字段名。回写时必须用这一对 —— 用响应名会写到解析层读不出的位置,
	// 表现就是"刷新成功了, 但下一次刷新说没令牌", 即账号静默失效。
	StoreAccessTokenField  string
	StoreRefreshTokenField string
	// StoreContainer 是凭据文件里包裹这两个令牌的嵌套层键名(空表示顶层)。
	// Claude 是 "claudeAiOauth"; 回写到顶层会让整个文件形状变掉, 解析层随即失效。
	StoreContainer string
}

const (
	accountBodyFormatForm = "form"
	accountBodyFormatJSON = "json"
)

// geminiBuiltinClientID 是 Gemini CLI 的内置 client_id(来自 npm 包 @google/gemini-cli@0.68.0)。
// geminiBuiltinClientSecret 是对应的 client_secret(包内唯一的 GOCSPX- 值)。
// 这两个常量同时用于: 1) DefaultSettings() 的默认值(在 setting.go 里写死同值); 2) geminiOAuthClientID/Secret 的"是否为默认值"判定。
// 修改时必须同步更新两处, 并在测试中钉住这对值。
const (
	geminiBuiltinClientID     = "681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com"
	geminiBuiltinClientSecret = "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"
)

// Antigravity 客户端的凭据**不由代码提供**, 必须由部署配置注入。
//
// 为什么不内置默认值(与 gemini 那对不同): 把某厂商应用的 client 身份写进公开仓库,
// 等于让每个部署者都在冒用同一个应用 —— 配额、责任与风控都落在不属于他们的地方,
// 而且会让该应用的身份在公开代码里被固化, 后续任何人都能直接拿去用。
// gemini 那对内置值属于既有决定(它让开箱可用), 但不应把这个模式再复制一次。
//
// 与 gemini 的另一处区别: 两套 client 的授权范围不同, **凭据不可互换**。
// 用 gemini 的 token 请求 Antigravity 数据面会 403, 缺的是 cclog 与 experimentsandconfigs
// (见 antigravityScopes 的注释)。所以配错 client 的症状是"授权成功但一个模型都拉不到"。
const (
	antigravityOAuthClientIDEnv     = "SWITCHYARD_ANTIGRAVITY_OAUTH_CLIENT_ID"
	antigravityOAuthClientSecretEnv = "SWITCHYARD_ANTIGRAVITY_OAUTH_CLIENT_SECRET"
)

// antigravityScopes 是 Antigravity 要求的权限。
//
// 前三个与 Gemini CLI 相同(Code Assist 的通用权限), 后两个是 Antigravity 特有的。
// 实测(2026-11): 用只含前三个 scope 的 token 请求 retrieveUserQuota 与 generateContent
// 一律 403 "The caller does not have permission"; 缺的正是这两个。
// 所以它们不是可选项 —— 少了整个数据面不可用, 且报错看不出与 scope 有关。
var antigravityScopes = []string{
	"https://www.googleapis.com/auth/cloud-platform",
	"https://www.googleapis.com/auth/userinfo.email",
	"https://www.googleapis.com/auth/userinfo.profile",
	"https://www.googleapis.com/auth/cclog",
	"https://www.googleapis.com/auth/experimentsandconfigs",
}

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
		// 请求体同样是 camelCase —— 这是与 Codex/Claude 最容易搞混的一处:
		// 它连 grant_type 都写成 grantType, 写错会得到一个 unsupported_grant_type。
		GrantTypeField:          "grantType",
		RefreshTokenFieldInBody: "refreshToken",
		// 存储键名也是 camelCase(与响应同名); 显式写出来, 避免将来有人"统一成 snake_case"时误改。
		StoreAccessTokenField:  "accessToken",
		StoreRefreshTokenField: "refreshToken",
		// AWS OIDC 端点会校验 SDK 的 user-agent 系列头。这不是伪装: 该端点本就只面向
		// AWS 客户端, 少了这些头请求会被拒, 且响应里不会说明原因。
		Headers: map[string]string{
			"User-Agent":       "aws-sdk-rust/1.3.9 os/windows lang/rust/1.87.0",
			"x-amz-user-agent": "aws-sdk-rust/1.3.9 ua/2.1 api/ssooidc/1.88.0 os/windows lang/rust/1.87.0 m/E app/AmazonQ-For-CLI",
			"amz-sdk-request":  "attempt=1; max=3",
		},
		ExtraFields: func(credential AccountCredential) map[string]string {
			// Kiro 的刷新请求把 clientId/clientSecret 直接放进请求体, 不是 Basic 认证。
			return map[string]string{
				"clientId":     credential.ClientID,
				"clientSecret": credential.ClientSecret,
			}
		},
	},
	AccountPlatformGemini: {
		Platform: AccountPlatformGemini,
		// 端点是固定的, 取自 Gemini CLI 官方发行包(0.68.0)的二进制字符串:
		//   token 端点 = https://oauth2.googleapis.com/token
		TokenURL: "https://oauth2.googleapis.com/token",
		// ClientID / client_secret 走**运行时配置**而不是写死在这里, 原因有二:
		//
		// 1) 它们是 Google 分配给 Gemini CLI 这个应用的身份。我们从发行包里提取得出来,
		//    但那不等于我们有权以该应用的身份分发与使用 —— 写死在公开仓库里等于把这套
		//    身份固化进我们的代码, 让所有部署者都在冒用它。
		// 2) 部署者应当用自己注册的 Google OAuth 客户端。自己的配额、自己的责任,
		//    也避免所有人挤在同一个 client 上被上游限流。
		//
		// 未配置时 gemini 的 OAuth 刷新会明确失败并说明要配哪两个环境变量 ——
		// 见 AccountOAuthRefreshRequest 的检查; 绝不用一个猜来的 client 静默去打上游。
		//
		// 用 form-urlencoded: 这是 Google 官方 Node 客户端(gemini-cli 内置的 google-auth-library)
		// 的实际做法。实测该端点也接受 JSON 体(同样返回 invalid_grant),
		// 选 form 是为跟随官方客户端的既有实现, 不是"否则会失败"。
		BodyFormat:        accountBodyFormatForm,
		AccessTokenField:  "access_token",
		RefreshTokenField: "refresh_token",
		ExpiresInField:    "expires_in",
		// 凭据文件(oauth_creds.json)里的键名**就是** snake_case 的同名字段,
		// 与 Claude 的 camelCase 相反 —— 这处差异必须显式写出来, 否则回写会写到解析层读不到的位置。
		StoreAccessTokenField:  "access_token",
		StoreRefreshTokenField: "refresh_token",
		// client_secret 由部署配置提供; 未配置时 refresh 直接报错而不是拿空值去换令牌。
		ExtraFields: func(credential AccountCredential) map[string]string {
			return map[string]string{
				"client_secret": geminiOAuthClientSecret(),
			}
		},
	},
	AccountPlatformAntigravity: {
		Platform: AccountPlatformAntigravity,
		// 与 gemini 共用 Google 的令牌端点, 但 client 不同(见 antigravityOAuthClientIDEnv 注释)。
		TokenURL: "https://oauth2.googleapis.com/token",
		// client_id 由部署配置提供; 未配置时 AccountOAuthRefreshRequest 会明确报错指路。
		// 这里不写死: 本平台不内置默认 client(理由见常量注释), 与 gemini 的"内置可覆盖"不同。
		BodyFormat:        accountBodyFormatForm,
		AccessTokenField:  "access_token",
		RefreshTokenField: "refresh_token",
		ExpiresInField:    "expires_in",
		// 响应与存储键名和 gemini 一样是 snake_case。
		StoreAccessTokenField:  "access_token",
		StoreRefreshTokenField: "refresh_token",
		// **关键**: 刷新时必须把 scope 一起发, 否则新令牌会退回 Google 的默认 scope,
		// 丢掉 cclog 与 experimentsandconfigs —— 表现为"刷新成功但数据面 403"。
		// 这是本平台最容易踩的坑, 实测已确认缺这两个 scope 时 retrieveUserQuota 直接 403。
		Scopes: antigravityScopes,
		ExtraFields: func(credential AccountCredential) map[string]string {
			_, clientSecret := AntigravityOAuthClient()
			return map[string]string{
				"client_secret": clientSecret,
			}
		},
	},
	AccountPlatformClaude: {
		Platform: AccountPlatformClaude,
		// 端点与 client_id 来自 Claude Code 官方发行包(2.1.325)里提取的常量,
		// 不是猜测值。生产环境那一份:
		//   TOKEN_URL: "https://platform.claude.com/v1/oauth/token"
		//   CLIENT_ID: "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
		TokenURL:   "https://platform.claude.com/v1/oauth/token",
		ClientID:   "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
		BodyFormat: accountBodyFormatJSON,
		// 响应字段是 snake_case(与凭据文件里的 camelCase 不同), 见 AccountJSONParse 的 Claude 分支。
		AccessTokenField:  "access_token",
		RefreshTokenField: "refresh_token",
		ExpiresInField:    "expires_in",
		// 刷新请求必须带 scope: 它决定新令牌的权限范围。漏发会拿到一个没有推理权限的令牌,
		// 表现为"刷新成功但调用 403", 比刷新失败更难查。
		Scopes: []string{"user:inference", "user:profile"},
		// Claude Code 的凭据文件把令牌放在 claudeAiOauth 下, 且键名是 camelCase,
		// 而令牌端点的响应是 snake_case —— 这层差异必须显式声明, 否则回写会写到解析层读不到的位置。
		StoreAccessTokenField:  "accessToken",
		StoreRefreshTokenField: "refreshToken",
		StoreContainer:         "claudeAiOauth",
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
	// Gemini 的 client 身份来自部署配置, 不在表里写死。在这里注入而不是在请求构造处判断:
	// 规格表是"这个平台怎么刷新"的唯一来源, 让读 spec 的地方都能看到同一份值。
	if platform == AccountPlatformGemini {
		spec.ClientID = geminiOAuthClientID()
	}
	// Antigravity 的 client 同样来自部署配置, 但不内置默认值:
	// 未配置时这里保持空, 由 AccountOAuthRefreshRequest 当场报错并指出该配哪两个变量。
	if platform == AccountPlatformAntigravity {
		spec.ClientID, _ = AntigravityOAuthClient()
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
// AccountOAuthRefreshRequest 构造刷新请求的请求体与 Content-Type。
//
// 键名按平台规格走而不是写死: 各平台的请求体字段命名不统一(Kiro 全 camelCase,
// Codex/Claude 是 snake_case), 写死一套必然有一个平台拿不到令牌。
func AccountOAuthRefreshRequest(spec AccountOAuthSpec, credential AccountCredential) (contentType string, body []byte, err error) {
	if strings.TrimSpace(credential.RefreshToken) == "" {
		return "", nil, fmt.Errorf("%s: refresh_token 为空, 无法刷新", spec.Platform)
	}
	// 客户端身份没配好时当场拦住, 且把该配哪两个变量直接写在错误里。
	// 让它去发请求的话, 上游会回 invalid_client —— 那条信息会被读成"账号坏了",
	// 而真实原因是部署少配了两个环境变量, 两者排查方向完全不同。
	if spec.Platform == AccountPlatformGemini {
		if spec.ClientID == "" || geminiOAuthClientSecret() == "" {
			clientIDEnv, clientSecretEnv := GeminiOAuthClientEnvNames()
			return "", nil, fmt.Errorf(
				"%s: 未配置 OAuth 客户端, 请设置环境变量 %s 与 %s (需为本部署自行注册的 Google OAuth 客户端)",
				spec.Platform, clientIDEnv, clientSecretEnv)
		}
	}
	if spec.Platform == AccountPlatformAntigravity {
		// 本平台不内置默认 client, 未配置就是不能用。同样在此拦住并指明变量名,
		// 否则会拿空 client 去打上游, 换回一个读起来像"账号失效"的 invalid_client。
		if !AntigravityOAuthClientConfigured() {
			clientIDEnv, clientSecretEnv := AntigravityOAuthClientEnvNames()
			return "", nil, fmt.Errorf(
				"%s: 未配置 OAuth 客户端, 请设置环境变量 %s 与 %s (Antigravity 的 client 不内置默认值)",
				spec.Platform, clientIDEnv, clientSecretEnv)
		}
	}
	grantTypeField := spec.GrantTypeField
	if grantTypeField == "" {
		grantTypeField = "grant_type"
	}
	refreshField := spec.RefreshTokenFieldInBody
	if refreshField == "" {
		refreshField = "refresh_token"
	}
	fields := map[string]string{
		grantTypeField: "refresh_token",
		refreshField:   credential.RefreshToken,
	}
	if spec.ClientID != "" {
		fields["client_id"] = spec.ClientID
	}
	if len(spec.Scopes) > 0 {
		// scope 用空格分隔是 OAuth 的既有约定; 有的端点接受数组, 但空格分隔是两者都认的形式。
		fields["scope"] = strings.Join(spec.Scopes, " ")
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

// AccountOAuthStoreFields 返回"凭据文件里"令牌的真实键名与嵌套层。空键名回退到响应字段名。
func AccountOAuthStoreFields(spec AccountOAuthSpec) (accessField, refreshField, container string) {
	accessField = spec.StoreAccessTokenField
	if accessField == "" {
		accessField = spec.AccessTokenField
	}
	refreshField = spec.StoreRefreshTokenField
	if refreshField == "" {
		refreshField = spec.RefreshTokenField
	}
	return accessField, refreshField, spec.StoreContainer
}

// AccountOAuthHeaders 返回该平台必须携带的固定请求头。
func AccountOAuthHeaders(spec AccountOAuthSpec) map[string]string {
	if spec.Headers == nil {
		return nil
	}
	// 返回副本: 调用方设置 header 时不该改动这张全局规格表。
	copied := make(map[string]string, len(spec.Headers))
	for key, value := range spec.Headers {
		copied[key] = value
	}
	return copied
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

// Gemini 的 OAuth 客户端凭据取值优先级: 设置库(DB) > 环境变量 > 内置默认(Gemini CLI)。
//
// 内置默认值(来自 Gemini CLI npm 包)让部署开箱可用; 用户可在设置页覆盖为自己的 client,
// 也可用环境变量 SWITCHYARD_GEMINI_OAUTH_CLIENT_ID/SECRET 注入。
// client_secret 是秘密: 进 IsSecretSetting() 与 SettingNeedsAtRestEncryption(), 落库加密、界面掩码。
// 旧注释的"不写死在仓库"已不再适用: 现在内置默认值就是写在代码里的, 但可被配置层层覆盖。
const (
	geminiOAuthClientIDEnv     = "SWITCHYARD_GEMINI_OAUTH_CLIENT_ID"
	geminiOAuthClientSecretEnv = "SWITCHYARD_GEMINI_OAUTH_CLIENT_SECRET"
)

// geminiOAuthClientPair 返回这一对 client_id/client_secret, **成对**从同一来源取。
//
// 为什么必须成对而不是各自判定: client_id 与 client_secret 是配对的, 混搭(例如 id 用 DB、
// secret 用 env 或内置默认)会得到一对不匹配的凭据, 上游回 invalid_client —— 而这个错误
// 看起来像"账号坏了", 排查方向完全错。宁可整对回落到下一层, 也不要拼出一对假凭据。
//
// 三层优先级: 设置库(DB, 值 != 内置默认才算用户真改过) > 环境变量(两个都非空才算配了)
//
//	> 内置默认(Gemini CLI)。
//
// "DB 值 != 内置默认"这个条件不是多余的: 启动时会用 DefaultSettings() 把内置默认值补齐进 DB,
// 若把"DB 非空"当作用户覆盖, 环境变量层就永远不可达 —— 升级前按文档配了 env 的部署者
// 会发现配置被静默忽略, 而界面上看不出区别。
func geminiOAuthClientPair() (clientID, clientSecret string) {
	// 1) 设置库(DB): 两个值都存在**且都不是内置默认值**时, 视为用户配置了自己的一对
	if GeminiOAuthSettingGetter != nil {
		id, errID := GeminiOAuthSettingGetter(SettingKeyGeminiOAuthClientID)
		sec, errSec := GeminiOAuthSettingGetter(SettingKeyGeminiOAuthClientSecret)
		id, sec = strings.TrimSpace(id), strings.TrimSpace(sec)
		if errID == nil && errSec == nil && id != "" && sec != "" &&
			id != geminiBuiltinClientID && sec != geminiBuiltinClientSecret {
			return id, sec
		}
	}
	// 2) 环境变量: 两个都非空才算配了 —— 只配一半同样是混搭, 不能采纳
	envID := strings.TrimSpace(os.Getenv(geminiOAuthClientIDEnv))
	envSec := strings.TrimSpace(os.Getenv(geminiOAuthClientSecretEnv))
	if envID != "" && envSec != "" {
		return envID, envSec
	}
	// 3) 内置默认值(Gemini CLI 的 client)
	return geminiBuiltinClientID, geminiBuiltinClientSecret
}

// geminiOAuthClientID 返回成对解析后的 client_id。
func geminiOAuthClientID() string {
	id, _ := geminiOAuthClientPair()
	return id
}

// geminiOAuthClientSecret 返回成对解析后的 client_secret。
func geminiOAuthClientSecret() string {
	_, sec := geminiOAuthClientPair()
	return sec
}

// GeminiOAuthClientConfigured 报告 Gemini 的 OAuth 客户端是否已配置。
// 现在内置了 Gemini CLI 的默认值, 所以永远为 true(开箱可用)。
// 界面若需区分"用的是内置默认还是用户自己的", 请用 GeminiOAuthClientSource().
func GeminiOAuthClientConfigured() bool {
	return true
}

// GeminiOAuthClientSource 说明 client_id/secret 的来源, 用于界面提示。
// 返回值: "builtin"(内置默认)、"env"(环境变量)、"db"(设置库覆盖, 且值 != 内置默认)。
// 注意: 仅当 DB 值 != 内置默认时才返回 "db", 否则回退 env/builtin。
func GeminiOAuthClientSource() string {
	// 判定必须与 geminiOAuthClientPair 完全一致, 否则界面会显示"用的是你配的客户端"
	// 而实际用着内置默认 —— 这种不一致比不显示来源更糟。
	if GeminiOAuthSettingGetter != nil {
		id, errID := GeminiOAuthSettingGetter(SettingKeyGeminiOAuthClientID)
		sec, errSec := GeminiOAuthSettingGetter(SettingKeyGeminiOAuthClientSecret)
		id, sec = strings.TrimSpace(id), strings.TrimSpace(sec)
		if errID == nil && errSec == nil && id != "" && sec != "" &&
			id != geminiBuiltinClientID && sec != geminiBuiltinClientSecret {
			return "db"
		}
	}
	if strings.TrimSpace(os.Getenv(geminiOAuthClientIDEnv)) != "" &&
		strings.TrimSpace(os.Getenv(geminiOAuthClientSecretEnv)) != "" {
		return "env"
	}
	return "builtin"
}

// GeminiOAuthClientEnvNames 返回需要配置的两个环境变量名, 供错误信息与文档引用。
func GeminiOAuthClientEnvNames() (clientID, clientSecret string) {
	return geminiOAuthClientIDEnv, geminiOAuthClientSecretEnv
}

// GeminiOAuthClient 返回配置好的 client_id 与 client_secret(导出版)。
//
// 登录流程(internal/signin)在 model 之外, 拿不到未导出的那两个小写函数;
// 与其把取值逻辑复制一份(迟早漂移), 不如在这里只开一个导出入口。
func GeminiOAuthClient() (clientID, clientSecret string) {
	return geminiOAuthClientID(), geminiOAuthClientSecret()
}

// AntigravityOAuthClient 返回 Antigravity 平台的 OAuth client 凭据。
//
// 只从环境变量读, 代码里不内置默认值(理由见 antigravityOAuthClientIDEnv 的注释)。
// 未配置时返回空对, 由调用方(授权入口与刷新路径)明确报错并指出该配哪两个变量 ——
// 绝不用一个猜来的 client 去打上游: 那会把"漏配环境变量"变成"账号坏了"。
//
// 两个变量必须同时配: 只配一个时整对视为未配置。理由与 gemini 那对相同 ——
// id 与 secret 是配对的, 混搭会得到一对不匹配的凭据, 上游回 invalid_client。
func AntigravityOAuthClient() (clientID, clientSecret string) {
	return strings.TrimSpace(os.Getenv(antigravityOAuthClientIDEnv)),
		strings.TrimSpace(os.Getenv(antigravityOAuthClientSecretEnv))
}

// AntigravityOAuthClientEnvNames 返回需配置的变量名, 供错误信息直接指路。
func AntigravityOAuthClientEnvNames() (clientIDEnv, clientSecretEnv string) {
	return antigravityOAuthClientIDEnv, antigravityOAuthClientSecretEnv
}

// AntigravityOAuthClientConfigured 报告 Antigravity 的 client 是否已配置。
func AntigravityOAuthClientConfigured() bool {
	clientID, clientSecret := AntigravityOAuthClient()
	return clientID != "" && clientSecret != ""
}

// AntigravityOAuthScopes 返回 Antigravity 要求的 scope 列表。
// 含 cclog 与 experimentsandconfigs, 缺任一个数据面就会 403(实测确认)。
func AntigravityOAuthScopes() []string {
	out := make([]string, len(antigravityScopes))
	copy(out, antigravityScopes)
	return out
}

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
		BodyFormat: accountBodyFormatForm,
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
// Gemini 的 OAuth 客户端凭据来自运行时配置, 不写死在仓库里。
//
// 为什么不用 Model 的 Setting 表: 这两项是部署级身份(谁在调用 Google 的接口),
// 不是运行期可调的业务参数 —— 放在设置表里会被导出/备份/在管理端明文显示,
// 而它理应像 KEK 一样只存在于部署环境。
//
// 为什么不是"随便填也能跑": 未配置时刷新必须明确失败。拿一个猜来的或空的 client
// 去打真实端点, 得到的 invalid_client 会被解读成"账号坏了", 把排查引向错的方向。
//
// 命名与 KEK 那一族保持一致(SWITCHYARD_ 前缀), 便于用同一套 systemd drop-in 注入。
const (
	geminiOAuthClientIDEnv     = "SWITCHYARD_GEMINI_OAUTH_CLIENT_ID"
	geminiOAuthClientSecretEnv = "SWITCHYARD_GEMINI_OAUTH_CLIENT_SECRET"
)

// geminiOAuthClientID 返回配置的 Gemini OAuth client_id。
func geminiOAuthClientID() string {
	return strings.TrimSpace(os.Getenv(geminiOAuthClientIDEnv))
}

// geminiOAuthClientSecret 返回配置的 client_secret。
func geminiOAuthClientSecret() string {
	return strings.TrimSpace(os.Getenv(geminiOAuthClientSecretEnv))
}

// GeminiOAuthClientConfigured 报告 Gemini 的 OAuth 客户端是否已配置。
// 界面据此决定是否提示"可在设置里启用 Gemini CLI 导入": 没配就没有刷新能力,
// 让使用者以为能用是更糟的体验。
func GeminiOAuthClientConfigured() bool {
	return geminiOAuthClientID() != "" && geminiOAuthClientSecret() != ""
}

// GeminiOAuthClientEnvNames 返回需要配置的两个环境变量名, 供错误信息与文档引用。
func GeminiOAuthClientEnvNames() (clientID, clientSecret string) {
	return geminiOAuthClientIDEnv, geminiOAuthClientSecretEnv
}

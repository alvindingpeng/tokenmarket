package signin

// 各平台的登录参数。
//
// 端点与 client 身份全部有出处, 不靠猜 —— 登录打错端点或认错 client 的失败信息都极其
// 含糊(上游只会说 invalid_client), 排查成本远高于写下来的成本:
//
//   Claude: 从 @anthropic-ai/claude-code 2.1.325 的官方 linux-x64 二进制里 strings 提取
//           authorize = https://platform.claude.com/oauth/authorize
//           token     = https://platform.claude.com/v1/oauth/token
//           client    = 9d1c250a-...(与本项目刷新规格里的同一个值)
//   Gemini: 从 @google/gemini-cli 0.68.0 提取的 Google OAuth 端点(client 走部署配置,
//           理由见 model/account_oauth.go —— 那是 Google 给该应用的identity, 从包里能
//           提取不代表有权以它身份发起登录)。
//   Codex:  authorize = https://auth.openai.com/oauth/authorize(与刷新规格同源)。
//
// 这些 client 都是各官方 CLI 自己的 OAuth 客户端。用它们发起登录, 用户看到的授权页会
// 显示成该 CLI 在申请权限 —— 这是本功能的既定取舍(用户已确认), 因此界面上必须写明,
// 不能让用户在不知情的情况下授权。

import (
	"fmt"
	"net/url"

	"github.com/alvindingpeng/switchyard/internal/model"
)

const (
	claudeAuthorizeURL = "https://platform.claude.com/oauth/authorize"
	claudeTokenURL     = "https://platform.claude.com/v1/oauth/token"
	// Claude 的 code 回调页: 授权完成后浏览器停在这里并把 code 显示出来。
	// 用它作 redirect_uri, 是因为它不需要我们自己的服务器 —— 页面把 code 给用户看,
	// 用户复制回来, 这正是服务端部署可用的路径。
	claudeRedirectURI = "https://platform.claude.com/oauth/code/callback"
	claudeClientID    = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"

	codexAuthorizeURL = "https://auth.openai.com/oauth/authorize"
	codexRedirectURI  = "http://localhost:1455/auth/callback"
	codexClientID     = "app_EMoamEEZ73f0CkXaXp7hrann"

	googleAuthorizeURL = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL     = "https://oauth2.googleapis.com/token"
	googleRedirectURI  = "http://127.0.0.1"
)

// claudeScopes 是 Claude Code 登录时申请的权限。少一项会导致登录成功但调用被拒。
var claudeScopes = []string{
	"user:inference", "user:profile", "user:sessions:claude_code", "user:mcp_servers",
}

// codexScopes 是 Codex CLI 登录时申请的权限, 与官方 CLI 一致。
// 少了 api.connectors.* 会让登录成功但连接器类请求被拒 —— 那是最难查的一类失败。
var codexScopes = "openid profile email offline_access api.connectors.read api.connectors.invoke"

// googleScopes 是 Gemini CLI 的 Code Assist 权限。
var googleScopes = []string{
	"https://www.googleapis.com/auth/cloud-platform",
	"https://www.googleapis.com/auth/userinfo.email",
	"https://www.googleapis.com/auth/userinfo.profile",
}

// antigravityScopes 是固定的(与 client 无关, 是平台要求的权限), 在此转发一份别名,
// 让下面的 case 分支读起来与本文件其它平台一致。
//
// client 凭据不在此缓存: 它来自环境变量, 而环境变量在测试里会被 t.Setenv 逐用例改变。
// 缓存一次会让"配了 env 的用例"与"没配的用例"互相污染 —— 表现为测试顺序不同结果不同。
var antigravityScopes = model.AntigravityOAuthScopes()

// ProviderOf 取一个平台的登录参数。支持的平台与账号导入的平台列表一致 ——
// 登录只是为了少让用户手贴 JSON, 产出的仍是一份该平台能用的凭据。
func ProviderOf(platform string) (Provider, error) {
	switch platform {
	case "claude":
		return Provider{
			Platform:     "claude",
			AuthorizeURL: claudeAuthorizeURL,
			TokenURL:     claudeTokenURL,
			ClientID:     claudeClientID,
			Scopes:       claudeScopes,
			RedirectURI:  claudeRedirectURI,
			BodyFormat:   "json",
			// Claude 授权完停在回调页, 页面上把 code 显示出来让用户复制。
			AuthStyle:         AuthStyleCode,
			UserCodeHint:      "复制授权页显示的 code 粘贴到下方",
			AccessTokenField:  "access_token",
			RefreshTokenField: "refresh_token",
			ExtraAuth: url.Values{
				// Claude Code 的登录要带上这两个, 少了授权页不认为自己在给 CLI 授权。
				"code": {""},
			},
		}, nil
	case "codex":
		return Provider{
			Platform:     "codex",
			AuthorizeURL: codexAuthorizeURL,
			TokenURL:     "https://auth.openai.com/oauth/token",
			ClientID:     codexClientID,
			RedirectURI:  codexRedirectURI,
			BodyFormat:   "form",
			AuthStyle:    AuthStyleRedirect,
			UserCodeHint: "授权后浏览器会跳到一个打不开的地址(localhost:1455), 把地址栏里的完整地址复制粘贴到下方",
			ExtraAuth: url.Values{
				"id_token_add_organizations": {"true"},
				"codex_cli_simplified_flow":  {"true"},
				"originator":                 {"codex_cli_rs"},
			},
			AccessTokenField:  "access_token",
			RefreshTokenField: "refresh_token",
		}, nil
	case "gemini":
		clientID, clientSecret := model.GeminiOAuthClient()
		// 现在内置了 Gemini CLI 的默认值, clientID/Secret 永远非空。
		// 只有当用户在设置页显式清空覆盖值时才会出现空值(极不推荐), 此时按旧逻辑报错。
		if clientID == "" || clientSecret == "" {
			idEnv, secretEnv := model.GeminiOAuthClientEnvNames()
			return Provider{}, fmt.Errorf(
				"gemini: OAuth 客户端为空(可能因设置页显式清空), 请设置环境变量 %s 与 %s 或在设置页配置",
				idEnv, secretEnv)
		}
		// 来源信息可供界面提示: 内置默认/环境变量/设置库覆盖
		_ = model.GeminiOAuthClientSource()
		return Provider{
			Platform:     "gemini",
			AuthorizeURL: googleAuthorizeURL,
			TokenURL:     googleTokenURL,
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Scopes:       googleScopes,
			RedirectURI:  googleRedirectURI,
			BodyFormat:   "form",
			AuthStyle:    AuthStyleRedirect,
			UserCodeHint: "授权后浏览器会跳到一个打不开的地址(127.0.0.1), 把地址栏里的完整地址复制粘贴到下方",
			// access_type=offline 是拿到 refresh_token 的前提; 少了它只能拿到一次性的
			// access_token, 账号导入后无法自动续期, 表现为"能用一会儿然后静默失效"。
			ExtraAuth: url.Values{
				"access_type": {"offline"},
				"prompt":      {"consent"},
			},
			AccessTokenField:  "access_token",
			RefreshTokenField: "refresh_token",
		}, nil
	case model.AccountPlatformAntigravity:
		// 与 gemini 同后端(Google Code Assist), 但**授权身份不同**。
		//
		// 为什么单列而不是复用 gemini: 实测(2026-11)用 gemini 的凭据请求 Antigravity 的
		// 数据面(fetchAvailableModels / generateContent)一律 403 "The caller does not have permission"。
		// tokeninfo 显示 gemini 的 token 缺 cclog 与 experimentsandconfigs, 而这两个 scope
		// 只属于 Antigravity 的 client —— 凭据不能跨客户端复用。
		//
		// 另一条实测事实: Gemini CLI 的免费层(Code Assist for individuals)已被 Google 停用,
		// onboardUser 返回 403 UNSUPPORTED_CLIENT(error_number 1013)并引导迁移到 Antigravity。
		// 个人订阅账号因此只能走本平台。
		// client 凭据来自部署配置, 不内置默认值(理由见 model.antigravityOAuthClientIDEnv 注释)。
		// 未配置时明确报错并指出变量名 —— 返回一个空 client 的 Provider 会让界面出现
		// "授权按钮亮着但一点就报 invalid_client"的死路。
		clientID, clientSecret := model.AntigravityOAuthClient()
		if clientID == "" || clientSecret == "" {
			clientIDEnv, clientSecretEnv := model.AntigravityOAuthClientEnvNames()
			return Provider{}, fmt.Errorf(
				"antigravity: 未配置 OAuth 客户端, 请设置环境变量 %s 与 %s (Antigravity 的 client 不内置默认值)",
				clientIDEnv, clientSecretEnv)
		}
		return Provider{
			Platform:     model.AccountPlatformAntigravity,
			AuthorizeURL: googleAuthorizeURL,
			TokenURL:     googleTokenURL,
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Scopes:       antigravityScopes,
			RedirectURI:  googleRedirectURI,
			BodyFormat:   "form",
			AuthStyle:    AuthStyleRedirect,
			UserCodeHint: "授权后浏览器会跳到一个打不开的地址(127.0.0.1), 把地址栏里的完整地址复制粘贴到下方",
			ExtraAuth: url.Values{
				"access_type": {"offline"},
				"prompt":      {"consent"},
			},
			AccessTokenField:  "access_token",
			RefreshTokenField: "refresh_token",
		}, nil
	default:
		return Provider{}, fmt.Errorf("平台 %s 不支持网页登录(可改用粘贴凭据 JSON 导入)", platform)
	}
}

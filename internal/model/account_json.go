package model

// 上游账号 JSON 的解析与规格(Codex 起步, 其余平台后续按同一套扩展)。
//
// 为什么单独抽一个解析层: 账号 JSON 的字段命名按平台分三派 —— Codex 用 snake_case
// (access_token), Kiro 用 camelCase (refreshToken), Gemini/Grok 根本没有 token 而是会话 Cookie。
// 若让每个调用方各自 json.Unmarshal, 这三派迟早被写进同一个 struct, 然后静默读错字段。
// 解析集中在一处, 每种平台各自的字段名与必填项就在这里说明一次。

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// 账号凭据形态: 决定"定时验证有效性"到底在验证什么。
const (
	// AccountKindOAuth 可自动续期: 有 refresh_token, 定时任务做刷新。
	AccountKindOAuth = "oauth"
	// AccountKindCookie 浏览器会话: 没有 refresh 概念, 定时任务只能重放一次轻请求看是否仍有效;
	// 失效后无法自动恢复, 必须人工重导 —— 这一点必须让使用者知道, 而不是让任务看起来"一直在跑"。
	AccountKindCookie = "cookie"
)

// 上游平台标识。
const (
	AccountPlatformCodex  = "codex"
	AccountPlatformKiro   = "kiro"
	AccountPlatformGemini = "gemini"
	AccountPlatformGrok   = "grok"
	AccountPlatformClaude = "claude"
)

// AccountCredential 统一后的账号凭据形态。
// 不直接复用 ChannelKeyConfig: 后者是落库与编辑表单的扁平结构, 与"一份导入的 JSON 说了什么"
// 不是同一件事。解析先产出这个中间形态, 再由 op 侧写入凭据列, 两个边界各管一段。
type AccountCredential struct {
	Platform     string            // codex | kiro | gemini | grok | claude
	Kind         string            // oauth | cookie
	AccessToken  string            // oauth: 当前访问令牌
	RefreshToken string            // oauth: 续期令牌; 每次刷新上游都会轮换它
	ExpiresAt    time.Time         // oauth: 到期时间; 零值表示未知(靠刷新失败或 401 才发现)
	AccountID    string            // oauth: 上游账号标识(Codex 的 chatgpt_account_id)
	ClientID     string            // kiro: OIDC client id; codex 用内置常量
	ClientSecret string            // kiro: OIDC client secret
	Email        string            // 展示用; 来自 JSON 或 JWT claim
	Cookies      map[string]string // cookie 类平台的会话值
}

// AccountJSONParse 按平台解析账号 JSON。
//
// 兼容两种真实形状: 平铺(常见于各类中转站导出的凭据)与 CLI 导出(~/.codex/auth.json, 多一层
// tokens 封装)。两种都吃, 是因为用户手上两种都有 —— 只支持一种会让另一半人贴上来就报错。
func AccountJSONParse(platform string, raw []byte) (*AccountCredential, error) {
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("账号 JSON 不是合法对象: %w", err)
	}
	// CLI 导出的外层封装: 存在 tokens 对象时下沉一层。先判存在再取值, 不看平台 ——
	// 平台判断放在字段层, 解包是形状问题。
	if inner, ok := fields["tokens"].(map[string]any); ok {
		fields = inner
	}

	credential := &AccountCredential{Platform: platform, Cookies: map[string]string{}}
	switch platform {
	case AccountPlatformCodex:
		credential.Kind = AccountKindOAuth
		credential.AccessToken = accountJSONString(fields["access_token"])
		credential.RefreshToken = accountJSONString(fields["refresh_token"])
		credential.AccountID = accountJSONString(fields["account_id"])
		credential.Email = accountJSONString(fields["email"])
		credential.ExpiresAt = accountJSONExpiry(fields["expired"], credential.AccessToken)
		// 报错要说清是哪一类缺失: "缺 access_token" 和 "缺 refresh_token" 对使用者是完全不同的
		// 两件事 —— 前者说明贴错了文件, 后者说明这个账号无法自动续期, 只能手动换。
		if credential.AccessToken == "" {
			return nil, fmt.Errorf("%s: 缺少 access_token (若从 CLI 导出, 应包含 tokens 字段)", platform)
		}
		if credential.RefreshToken == "" {
			return nil, fmt.Errorf("%s: 缺少 refresh_token, 该账号无法自动续期", platform)
		}
	case AccountPlatformClaude:
		credential.Kind = AccountKindOAuth
		// Claude Code 把 OAuth 凭据放在 claudeAiOauth 下, 且字段是 camelCase:
		//   {"claudeAiOauth":{"accessToken":..,"refreshToken":..,"expiresAt":..,"scopes":[..]}}
		// 这与 Codex 的顶层 snake_case 完全不同 —— 早先让 Claude 复用 Codex 的分支,
		// 结果是从 Claude 的凭据文件里读不到任何令牌(它压根没有顶层 access_token)。
		// 同时容忍顶层 snake_case, 因为也有人导出的是令牌端点响应原文那种形状。
		nested := fields
		if inner, ok := fields["claudeAiOauth"].(map[string]any); ok {
			nested = inner
		}
		credential.AccessToken = accountJSONFirstString(nested["accessToken"], nested["access_token"])
		credential.RefreshToken = accountJSONFirstString(nested["refreshToken"], nested["refresh_token"])
		credential.Email = accountJSONString(fields["email"])
		// expiresAt 是毫秒时间戳(不是 RFC3339 字符串), 这是 Claude 凭据文件特有的形态:
		// 按字符串解析会永远失败, 于是到期时间恒为零值, 定时任务永不刷新 —— 账号到期即静默失效。
		if millis, ok := accountJSONNumberOf(nested["expiresAt"]); ok && millis > 0 {
			credential.ExpiresAt = timeUnixMillis(millis)
		} else if millis, ok := accountJSONNumberOf(nested["expires_at"]); ok && millis > 0 {
			credential.ExpiresAt = timeUnixMillis(millis)
		} else {
			credential.ExpiresAt = accountJSONExpiry(nested["expired"], credential.AccessToken)
		}
		if credential.AccessToken == "" {
			return nil, fmt.Errorf("claude: 缺少 accessToken (Claude Code 凭据文件应为 claudeAiOauth.accessToken)")
		}
		if credential.RefreshToken == "" {
			return nil, fmt.Errorf("claude: 缺少 refreshToken, 该账号无法自动续期")
		}
	case AccountPlatformKiro:
		credential.Kind = AccountKindOAuth
		// Kiro 用 camelCase: 与 Codex 的 snake_case 不同, 这里刻意不复用同一套键名。
		// accessToken 也要读: 导入时若带了当前令牌就不必等到第一次刷新, 直接可用;
		// 漏读它的后果是转发拿到空 Bearer, 全部 401, 而没有任何地方会报错 —— 解析层静默通过。
		credential.AccessToken = accountJSONString(fields["accessToken"])
		credential.RefreshToken = accountJSONString(fields["refreshToken"])
		credential.ClientID = accountJSONString(fields["clientId"])
		credential.ClientSecret = accountJSONString(fields["clientSecret"])
		credential.Email = accountJSONString(fields["email"])
		if credential.RefreshToken == "" {
			return nil, fmt.Errorf("kiro: 缺少 refreshToken")
		}
		if credential.ClientID == "" || credential.ClientSecret == "" {
			return nil, fmt.Errorf("kiro: 缺少 clientId/clientSecret")
		}
	case AccountPlatformGemini:
		// Gemini 有两种完全不同的凭据, 形状与用法都不同, 必须按内容分流:
		//
		//   OAuth 令牌  —— Gemini CLI 的 ~/.gemini/oauth_creds.json,
		//                  {access_token, refresh_token, expiry_date}; 可自动续期。
		//   Cookie 会话 —— Web 端抠出来的 c_ses 等会话串; 无 refresh 概念, 失效只能人工重导。
		//
		// 为什么不能只看平台名就定 kind: 同一个 "gemini" 平台下这两类都会出现。
		// 一律当 cookie 的话, CLI 用户贴进来的合法 OAuth 文件会得到"缺少 c_ses" ——
		// 报错还把人带偏, 他明明拿对了文件(这正是扩 G1 的直接原因)。
		// 反之一律当 oauth, Web 用户会被要求一个不存在的 refresh_token。
		// 按"有没有 refresh_token"分流是这个区分唯一可靠的判据。
		if rt := accountJSONString(fields["refresh_token"]); rt != "" {
			credential.Kind = AccountKindOAuth
			credential.AccessToken = accountJSONString(fields["access_token"])
			credential.RefreshToken = rt
			credential.Email = accountJSONString(fields["email"])
			if credential.AccessToken == "" {
				return nil, fmt.Errorf("gemini: 提供了 refresh_token 但缺少 access_token (Gemini CLI 的 oauth_creds.json 应同时包含两者)")
			}
			// expiry_date 是**毫秒时间戳**(与 Claude Code 的 expiresAt 同型), 不是 RFC3339。
			// 按字符串解析会永远失败, 到期时间恒为零 -> 定时任务永不刷新 -> 账号到期即静默失效。
			if millis, ok := accountJSONNumberOf(fields["expiry_date"]); ok && millis > 0 {
				credential.ExpiresAt = timeUnixMillis(millis)
			} else {
				credential.ExpiresAt = accountJWTExpiry(credential.AccessToken)
			}
			break
		}
		credential.Kind = AccountKindCookie
		for _, key := range []string{"c_ses", "c_oses", "csesidx", "config_id"} {
			if value := accountJSONString(fields[key]); value != "" {
				credential.Cookies[key] = value
			}
		}
		if credential.Cookies["c_ses"] == "" {
			return nil, fmt.Errorf("gemini: 缺少 c_ses (Cookie 会话); 若使用 Gemini CLI, 请提供 ~/.gemini/oauth_creds.json 的内容")
		}
	case AccountPlatformGrok:
		credential.Kind = AccountKindCookie
		// Grok 的会话 Cookie 是 sso; sso-rw 是它的孪生键, 不参与鉴权。
		value := accountJSONString(fields["sso"])
		if value == "" {
			return nil, fmt.Errorf("grok: 缺少 sso")
		}
		credential.Cookies["sso"] = value
		credential.Email = accountJSONString(fields["email"])
	default:
		return nil, fmt.Errorf("不支持的上游平台: %s", platform)
	}
	return credential, nil
}

// AccountCredentialExpired 判断是否已到期; 到期时间未知时返回 false(交给上游 401 暴露)。
func AccountCredentialExpired(credential AccountCredential, now time.Time) bool {
	if credential.ExpiresAt.IsZero() {
		return false
	}
	return credential.ExpiresAt.Before(now)
}

// AccountCredentialNeedsRefresh 判断是否该提前刷新。
// lead 而非直接比到期时间: 刷新是一次网络调用, 等到"已过期"才刷就意味着这一轮转发必然先用过期
// 令牌打一次上游、拿一个 401, 再重试 —— 提前一小段换来的是转发路径上不出现可避免的失败。
func AccountCredentialNeedsRefresh(credential AccountCredential, now time.Time, lead time.Duration) bool {
	if credential.ExpiresAt.IsZero() {
		return false
	}
	// 用 !Before 而不是 After: 恰好落在提前量边界上时也要刷新。
	// 差这一秒的代价不是「晚一点刷新」, 而是这一轮转发必然拿过期令牌打上游、吃一个 401,
	// 再走重试 —— 一次可避免的失败与一次多余的上游调用。
	return !now.Add(lead).Before(credential.ExpiresAt)
}

// accountJSONFirstString 依次尝试多个键名, 返回第一个非空值。
// 用于同一语义在不同导出形状里用了不同命名的场合(如 accessToken / access_token)。
func accountJSONFirstString(values ...any) string {
	for _, value := range values {
		if text := accountJSONString(value); text != "" {
			return text
		}
	}
	return ""
}

// accountJSONNumberOf 把 JSON 数字收敛成 int64。encoding/json 默认解成 float64。
func accountJSONNumberOf(value any) (int64, bool) {
	if number, ok := value.(float64); ok {
		return int64(number), true
	}
	return 0, false
}

// timeUnixMillis 把毫秒时间戳转成 time.Time。Claude 的 expiresAt 就是这一种。
func timeUnixMillis(millis int64) time.Time {
	return time.Unix(0, millis*int64(time.Millisecond)).UTC()
}

func accountJSONString(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

// accountJSONExpiry 依次尝试显式的 expired 字段与 JWT 的 exp claim。
// 两处都要试: 有的导出只给到期字段, 有的只给 JWT。
func accountJSONExpiry(expired any, accessToken string) time.Time {
	if text := accountJSONString(expired); text != "" {
		// 真实文件里的到期格式不统一: RFC3339(Z 后缀)、带 +00:00 的、以及空格分隔的本地时间。
		for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05+00:00", "2006-01-02 15:04:05"} {
			if parsed, err := time.Parse(layout, text); err == nil {
				return parsed
			}
		}
	}
	return accountJWTExpiry(accessToken)
}

// accountJWTExpiry 从 access_token(JWT)里读 exp; 不是 JWT 或没有 exp 时返回零值。
func accountJWTExpiry(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}
	}
	// encoding/json 把数字解成 float64; 这里只需要整数秒, 精度损失可忽略(exp 是秒级)。
	if exp, ok := claims["exp"].(float64); ok {
		return time.Unix(int64(exp), 0).UTC()
	}
	return time.Time{}
}

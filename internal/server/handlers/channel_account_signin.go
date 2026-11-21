package handlers

// 网页 OAuth 登录的三个接口。
//
// 三步: begin(生成授权 URL) -> 用户在浏览器授权 -> complete(把 code 贴回来换令牌)。
// 换到的令牌**立刻**交给现有的 AccountImport 落库, 而不是新开一条写入路径 ——
// 账号就是渠道下的 ChannelKey, 归属校验、权限、首刷、刷新规格全都已经在那里了;
// 另开一条路只会多一份迟早与它漂移的逻辑。

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/charmbracelet/log"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/alvindingpeng/switchyard/internal/signin"
	"github.com/gin-gonic/gin"
)

// browserSignIn 是进程内的登录会话表。放进程内内存而非数据库:
// 里面存着能换令牌的 code_verifier, 落盘的中间态是可被事后利用的残留。
var browserSignIn = signin.NewStore()

func init() {
	router.NewGroupRouter("/api/v1/channel/account/signin").
		Use(middleware.Auth()).
		AddRoute(
			// 生成授权 URL。不改任何状态, 但会新建一个会话, 所以用 POST。
			router.NewRoute("/begin", http.MethodPost).
				Use(middleware.RequireJSON()).
				Allow(model.RoleAdmin, model.RoleReseller).
				Handle(beginAccountSignIn),
		).
		AddRoute(
			router.NewRoute("/complete", http.MethodPost).
				Use(middleware.RequireJSON()).
				Allow(model.RoleAdmin, model.RoleReseller).
				Handle(completeAccountSignIn),
		).
		AddRoute(
			// 平台能力查询: 界面据此决定哪些平台显示"网页登录"入口。
			router.NewRoute("/platforms", http.MethodGet).
				Allow(model.RoleAdmin, model.RoleReseller).
				Handle(listSignInPlatforms),
		)
}

type beginSignInRequest struct {
	Platform string `json:"platform"`
}

// beginAccountSignIn 生成一次登录并返回授权 URL。
//
// 只回 URL, 不自动跳转: 跳转由前端打开新窗口完成, 服务端不该在此处做重定向 ——
// 后端做 302 会把 token 端点的域名和授权页混进同一个浏览器动作里, 也让错误无处显示。
func beginAccountSignIn(c *gin.Context) {
	var request beginSignInRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		resp.Error(c, http.StatusBadRequest, "请求格式错误")
		return
	}
	provider, err := signin.ProviderOf(strings.TrimSpace(request.Platform))
	if err != nil {
		// 平台不支持或部署没配好 client: 原样把原因给前端, 它知道该提示用户配什么。
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	flow, err := browserSignIn.Begin(provider)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{
		"platform":       flow.Platform,
		"authorize_url":  flow.AuthorizeURL,
		"state":          flow.State,
		"user_code_hint": flow.UserCodeHint,
		"expires_in":     int(signin.FlowTTL.Seconds()),
	})
}

type completeSignInRequest struct {
	Platform  string `json:"platform"`
	State     string `json:"state"`
	Pasted    string `json:"pasted"`
	ChannelID int    `json:"channel_id"`
	Name      string `json:"name"`
	Enabled   *bool  `json:"enabled"`
	Priority  int    `json:"priority"`
}

// completeAccountSignIn 用贴回来的 code 换令牌, 然后走与粘贴 JSON 完全相同的导入路径。
func completeAccountSignIn(c *gin.Context) {
	var request completeSignInRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		resp.Error(c, http.StatusBadRequest, "请求格式错误")
		return
	}
	flow, err := browserSignIn.Take(strings.TrimSpace(request.State))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	platform := strings.TrimSpace(request.Platform)
	if platform != "" && platform != flow.Platform {
		// 会话与请求声明的平台不一致: 用会话里的那个, 但明确拒绝这次请求 ——
		// 静默按会话走会让前端显示的平台与实际登录的平台不同, 用户无从察觉。
		resp.Error(c, http.StatusBadRequest, "登录会话与平台不匹配, 请重新发起登录")
		return
	}
	provider, err := signin.ProviderOf(flow.Platform)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	code, pastedState, err := signin.ParseCode(provider.AuthStyle, request.Pasted)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	// 地址里带的 state 必须与会话一致(只贴 code 时没有 state, 视为通过 ——
	// 那次一次性的会话取出本身已经是绑定)。
	if pastedState != "" && pastedState != flow.State {
		resp.Error(c, http.StatusBadRequest, "state 不匹配, 登录会话可能已失效, 请重新发起登录")
		return
	}

	// 渠道归属与"平台能不能进这个渠道"都在**换令牌之前**判定, 顺序不能反:
	// Exchange 会消耗掉用户刚拿到的授权码, 并且要打一次上游。而"渠道不归你"或
	// "平台与方言不符"这两件事与那次授权本身毫无关系 —— 先拒掉, 用户只需改平台重来,
	// 不会先白授权一遍再被拒。
	channel, err := op.ChannelGet(request.ChannelID)
	if err != nil {
		resp.Error(c, http.StatusNotFound, "渠道不存在")
		return
	}
	userID, role := middleware.CurrentUser(c)
	if !(model.Scope{ID: userID, Role: role}).Owns(channel.UserID) {
		// 不归属时按 404 处理: 不暴露渠道存在性。
		resp.Error(c, http.StatusNotFound, "渠道不存在")
		return
	}
	// 平台与渠道方言不匹配时立刻回 400(与 /import 同一个 op 层校验, 同一个错误文案)。
	// 少了这一步, 登录能成功换到令牌、凭据也能落库, 而失败要等到转发那一刻才暴露 ——
	// 那时它表现为凭据被熔断 30 分钟, 使用者完全想不到问题出在"渠道方言"上。
	if err := op.ValidateAccountPlatformForChannel(c.Request.Context(), channel.ID, flow.Platform); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	tokens, err := browserSignIn.Exchange(flow, provider, code)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	// 把令牌转成该平台能吃的凭据 JSON, 再交给既有导入逻辑。
	// 这一步不能省: 导入层的解析器负责填 kind/ExpiresAt/账号标识等, 绕过它会得到一个
	// 形状不对的 ChannelKey —— 能存进去, 但刷新与转发都读不出来。
	raw, err := credentialJSONOf(flow.Platform, tokens)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	result, err := op.AccountImport(c.Request.Context(), channel.ID, op.AccountImportSpec{
		Platform: flow.Platform,
		Name:     request.Name,
		Raw:      raw,
		Enabled:  enabled,
		Priority: request.Priority,
	}, time.Now())

	var refreshWarning *op.AccountImportRefreshWarning
	if err != nil && !errors.As(err, &refreshWarning) {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	payload := gin.H{
		"account":       result,
		"refreshed_now": refreshWarning == nil,
	}
	if refreshWarning != nil {
		payload["warning"] = refreshWarning.Error()
	}
	// 网页登录是订阅渠道建号的主路径, 与 import 一样要到"账号已落库"这一刻才能拉模型:
	// 建渠道时没有账号, 保存触发的 autoAddAfterSave 拿不到凭据而跳过, 模型就永远空着。
	// best-effort, 失败不影响登录结果(账号已经落库)。
	if channel, channelErr := op.ChannelGet(request.ChannelID); channelErr == nil && channel.ModelAutoAdd {
		// 走渠道出口池: 与转发同一条线路, 否则配了出口代理的渠道拉模型会被上游按地区拒绝。
		if client, clientErr := probeHTTPClientForChannel(channel); clientErr != nil {
			log.Warnf("auto-add after signin: build probe client for channel %d failed: %v", channel.ID, clientErr)
		} else if addErr := op.AutoAddChannelModels(c.Request.Context(), channel.ID, channel.ChannelConfig, client, userID); addErr != nil {
			log.Warnf("auto-add after signin: channel %d failed: %v", channel.ID, addErr)
		}
	}
	resp.Success(c, payload)
}

// credentialJSONOf 把登录换到的令牌转成该平台的凭据 JSON。
//
// 形状必须与"用户手贴的凭据"完全一致 —— 否则同一种账号会有两种形状, 解析层就得同时
// 兼容两种, 而其中一种只在登录路径里出现过, 缺少真实样本可对照。
func credentialJSONOf(platform string, tokens *signin.TokenResult) (string, error) {
	m := map[string]any{}
	switch platform {
	case model.AccountPlatformClaude:
		// Claude 的凭据文件把令牌放在 claudeAiOauth 下, 且是 camelCase ——
		// 这正是解析层与回写层共同约定的形状, 不能改成平铺。
		inner := map[string]any{
			"accessToken":  tokens.AccessToken,
			"refreshToken": tokens.RefreshToken,
			"scopes":       []string{"user:inference", "user:profile"},
		}
		if !tokens.ExpiresAt.IsZero() {
			inner["expiresAt"] = tokens.ExpiresAt.UnixMilli()
		}
		m["claudeAiOauth"] = inner
	case model.AccountPlatformCodex:
		m["access_token"] = tokens.AccessToken
		m["refresh_token"] = tokens.RefreshToken
		if !tokens.ExpiresAt.IsZero() {
			// 键名必须是 expired: 解析层按这个名读 Codex 的到期时间。用 expires_at
			// 不会报错, 只会让到期时间恒为零 -> 定时任务永不刷新 -> 账号静默失效。
			m["expired"] = tokens.ExpiresAt.UTC().Format(time.RFC3339)
		}
		// Codex 的账号标识来自 id_token 的 claim; 拿不到就留空, 由解析层从 access_token 的 JWT 兜底。
		if idt := rawStringOf(tokens.Raw, "id_token"); idt != "" {
			m["id_token"] = idt
		}
	case model.AccountPlatformGemini:
		m["access_token"] = tokens.AccessToken
		m["refresh_token"] = tokens.RefreshToken
		if !tokens.ExpiresAt.IsZero() {
			// Gemini CLI 的 oauth_creds.json 用毫秒时间戳, 解析层按毫秒读 —— 这里必须一致。
			m["expiry_date"] = tokens.ExpiresAt.UnixMilli()
		}
	case model.AccountPlatformAntigravity:
		// 与 gemini 同形状(解析层共用同一组字段, 见 model/account_json.go 的 antigravity 分支),
		// 因此这里写同一份 JSON。两者真正的差异在授权身份(client/scopes), 不在凭据形状 ——
		// 把它当两种形状处理只会让刷新链路多出一条无意义的分支。
		m["access_token"] = tokens.AccessToken
		m["refresh_token"] = tokens.RefreshToken
		if !tokens.ExpiresAt.IsZero() {
			m["expiry_date"] = tokens.ExpiresAt.UnixMilli()
		}
	default:
		return "", errors.New("平台 " + platform + " 的登录凭据转换尚未实现")
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func rawStringOf(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// listSignInPlatforms 返回支持网页登录的平台。
//
// 由后端计算而非前端写死: 哪些平台能登录取决于部署是否配好了 client(gemini 就要配),
// 前端写死会出现"按钮亮着但一点就报未配置"的死路。
func listSignInPlatforms(c *gin.Context) {
	platforms := make([]gin.H, 0, 4)
	for _, platform := range []string{
		model.AccountPlatformClaude, model.AccountPlatformCodex,
		model.AccountPlatformGemini, model.AccountPlatformAntigravity,
	} {
		provider, err := signin.ProviderOf(platform)
		if err != nil {
			// 不支持或未配置: 不列出来, 并把原因带上, 界面可以据此显示"为什么没有它"。
			platforms = append(platforms, gin.H{
				"platform":  platform,
				"available": false,
				"reason":    err.Error(),
			})
			continue
		}
		platforms = append(platforms, gin.H{
			"platform":       platform,
			"available":      true,
			"auth_style":     provider.AuthStyle,
			"user_code_hint": provider.UserCodeHint,
			// 登录用的是各官方 CLI 的 OAuth 客户端。这一点必须让用户知道: 授权页上会
			// 显示成该 CLI 在申请权限, 用户有权在授权前明白自己在给谁授权。
			"client_owner": clientOwnerOf(platform),
		})
	}
	resp.Success(c, platforms)
}

// clientOwnerOf 说明该平台登录用的是谁的 OAuth 客户端。
func clientOwnerOf(platform string) string {
	switch platform {
	case model.AccountPlatformClaude:
		return "claude-code"
	case model.AccountPlatformCodex:
		return "codex-cli"
	case model.AccountPlatformGemini:
		return "self-registered"
	case model.AccountPlatformAntigravity:
		// Antigravity 的 client 不是我们自己注册的, 也不是用户注册的, 而是 Google 随其
		// 桌面客户端分发的那一个。授权页会显示成 Antigravity 在申请权限 —— 用户有权先知道。
		return "antigravity-app"
	}
	return ""
}

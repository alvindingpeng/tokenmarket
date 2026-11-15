package signin

// 网页 OAuth 登录: 让用户在浏览器里授权, 把结果贴回来, 由服务端完成换令牌。
//
// 为什么不靠本地回调端口(magpie 的做法是 net.Listen("tcp", "127.0.0.1:0")):
// 那要求「跑服务的机器」和「开浏览器的机器」是同一台。本项目的部署形态恰恰不是 ——
// 服务在服务器上, 用户从自己的电脑浏览器访问。回调打到服务器的 127.0.0.1 永远到不了,
// 用户只会看到一个打不开的页面。
//
// 所以这里走的是 OAuth 的 out-of-band 变体: 授权完成后浏览器停在一个页面上, 该页面把
// code 显示出来, 用户复制粘贴回来, 服务端用自己的 client 去 token 端点换令牌。
// 全程不需要任何入站回调, 服务器部署与容器部署都能用。
//
// 安全性不靠回调可达性, 靠 PKCE + state:
//   - code_verifier 只存在服务端内存里, 攻击者拿到 code 也换不出令牌 (PKCE S256);
//   - state 把这次粘贴绑到发起它的那次登录, 会话过期或串号都会被拒。

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Flow 是一次进行中的网页登录。
type Flow struct {
	Platform string    `json:"platform"`
	AuthorizeURL string `json:"authorize_url"`
	// UserCodeHint 告诉用户贴什么: 有的平台显示一串 code, 有的把 code 放在最终页面的地址里。
	UserCodeHint string `json:"user_code_hint"`
	// RedirectURI 是授权请求里声明的那个; 换令牌时必须原样回传, 否则上游会拒。
	RedirectURI string `json:"-"`
	State    string `json:"state"`
	verifier string
	Created  time.Time `json:"created_at"`
}

// FlowTTL 是登录会话的有效期。
//
// 定 15 分钟: 短于"泡了杯咖啡回来还想接着贴"的常见区间不至于失效, 又远短于一个可以
// 被反复利用的凭据该活得的时间。过期即作废, 用户重来一次即可。
const FlowTTL = 15 * time.Minute

// Provider 是一个平台的登录参数。
type Provider struct {
	Platform     string
	AuthorizeURL string
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scopes       []string
	RedirectURI  string
	// ExtraAuth 是追加到授权 URL 上的平台特有参数。
	ExtraAuth url.Values
	// BodyFormat: "form" 或 "json"(各平台令牌端点要求不同)。
	BodyFormat string
	// UserCodeHint 是给用户的说明, 例如"贴授权页上显示的 code"。
	UserCodeHint string
	// AccessTokenField / RefreshTokenField 是 token 响应的字段名。
	AccessTokenField  string
	RefreshTokenField string
	// AuthStyle: "code" 表示用户贴 code; "redirect" 表示用户贴浏览器最终地址(地址里带 code)。
	AuthStyle string
}

const (
	AuthStyleCode     = "code"
	AuthStyleRedirect = "redirect"
)

// Store 保存进行中的登录。进程内内存即可: 登录是几秒到几分钟的事, 重启后重来一次
// 比把可换令牌的中间状态写进数据库安全得多 —— 后者会留下可供事后利用的残留。
type Store struct {
	mu    sync.Mutex
	flows map[string]*Flow
	now   func() time.Time
}

func NewStore() *Store {
	return &Store{flows: map[string]*Flow{}, now: time.Now}
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Begin 生成一次登录: 返回给前端的授权 URL 与 state。
func (s *Store) Begin(p Provider) (*Flow, error) {
	verifier, err := randomToken(32)
	if err != nil {
		return nil, fmt.Errorf("生成 code_verifier: %w", err)
	}
	state, err := randomToken(16)
	if err != nil {
		return nil, fmt.Errorf("生成 state: %w", err)
	}
	// S256: challenge = BASE64URL(SHA256(verifier))。plain 会把 verifier 本身发出去,
	// 等于没有 PKCE —— 拦截 code 的人也能换令牌。
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", p.ClientID)
	q.Set("redirect_uri", p.RedirectURI)
	if len(p.Scopes) > 0 {
		q.Set("scope", strings.Join(p.Scopes, " "))
	}
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	for k, vs := range p.ExtraAuth {
		for _, v := range vs {
			q.Set(k, v)
		}
	}

	f := &Flow{
		Platform:     p.Platform,
		AuthorizeURL: p.AuthorizeURL + "?" + q.Encode(),
		UserCodeHint: p.UserCodeHint,
		RedirectURI:  p.RedirectURI,
		State:        state,
		verifier:     verifier,
		Created:      s.now(),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep()
	s.flows[state] = f
	return f, nil
}

// sweep 清掉过期的登录。在写入时顺手清理, 不必另起一个定时器。
func (s *Store) sweep() {
	cutoff := s.now().Add(-FlowTTL)
	for k, f := range s.flows {
		if f.Created.Before(cutoff) {
			delete(s.flows, k)
		}
	}
}

// Take 取出并**删除**一次登录。一次性使用: 同一个 state 贴第二次必然失败,
// 否则一个泄漏的 code 可以被反复重放。
func (s *Store) Take(state string) (*Flow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep()
	f, ok := s.flows[state]
	if !ok {
		return nil, errors.New("登录会话不存在或已过期, 请重新发起登录")
	}
	delete(s.flows, state)
	return f, nil
}

// ParseCode 从用户粘贴的内容里取出授权码。
//
// 两种形状都要吃: 用户可能只贴 code, 也可能把浏览器最终停住的整个地址贴进来 ——
// 后者是更自然的手势(地址栏就在手边, 复制它比从页面里抠一段字符串更不容易出错)。
func ParseCode(authStyle, pasted string) (code, state string, err error) {
	pasted = strings.TrimSpace(pasted)
	if pasted == "" {
		return "", "", errors.New("请粘贴授权码")
	}
	if authStyle == AuthStyleRedirect || strings.HasPrefix(pasted, "http://") || strings.HasPrefix(pasted, "https://") {
		u, perr := url.Parse(pasted)
		if perr != nil {
			return "", "", errors.New("粘贴的地址无法解析, 请贴浏览器最终停住的那个完整地址")
		}
		code = u.Query().Get("code")
		state = u.Query().Get("state")
		if code == "" {
			// 上游把错误放在地址里(用户点了拒绝, 或参数不对), 原样带出来。
			if e := u.Query().Get("error"); e != "" {
				return "", "", fmt.Errorf("授权未完成: %s %s", e, u.Query().Get("error_description"))
			}
			return "", "", errors.New("粘贴的地址里没有 code, 请确认贴的是授权完成后那一页")
		}
		return code, state, nil
	}
	// 只有 code: 有的平台显示成 "code#state" 的形式, 一并拆开。
	if i := strings.Index(pasted, "#"); i > 0 {
		return pasted[:i], pasted[i+1:], nil
	}
	return pasted, "", nil
}

// TokenResult 是换到的令牌。RefreshToken 可能为空: 有的平台只在首次授权时给。
type TokenResult struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	// Raw 是原始响应, 调用方需要更多字段(账号标识等)时自己取。
	Raw map[string]any
}

// Exchange 用 code + verifier 去 token 端点换令牌。
func (s *Store) Exchange(f *Flow, p Provider, code string) (*TokenResult, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", f.RedirectURI)
	form.Set("client_id", p.ClientID)
	form.Set("code_verifier", f.verifier)
	if p.ClientSecret != "" {
		form.Set("client_secret", p.ClientSecret)
	}

	var contentType string
	var body []byte
	if p.BodyFormat == "json" {
		contentType = "application/json"
		m := map[string]any{"grant_type": "authorization_code", "code": code,
			"redirect_uri": f.RedirectURI, "client_id": p.ClientID, "code_verifier": f.verifier}
		if p.ClientSecret != "" {
			m["client_secret"] = p.ClientSecret
		}
		b, err := json.Marshal(m)
		if err != nil {
			return nil, fmt.Errorf("构造令牌请求: %w", err)
		}
		body = b
	} else {
		contentType = "application/x-www-form-urlencoded"
		body = []byte(form.Encode())
	}

	req, err := http.NewRequest(http.MethodPost, p.TokenURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("构造令牌请求: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求令牌端点: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("令牌端点返回非 JSON(HTTP %d): %.200s", resp.StatusCode, string(raw))
	}
	if resp.StatusCode != http.StatusOK {
		// 把上游的错误原样递出去: invalid_grant / invalid_client 的含义只有上游知道,
		// 换成我们自己的话反而会丢掉排查线索。
		return nil, fmt.Errorf("令牌端点 HTTP %d: %.300s", resp.StatusCode, string(raw))
	}

	at := getString(parsed, p.AccessTokenField)
	if at == "" {
		return nil, fmt.Errorf("令牌响应缺少 %s: %.200s", p.AccessTokenField, string(raw))
	}
	return &TokenResult{
		AccessToken:  at,
		RefreshToken: getString(parsed, p.RefreshTokenField),
		ExpiresAt:    expiresAt(parsed),
		Raw:          parsed,
	}, nil
}

func getString(m map[string]any, key string) string {
	if key == "" {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// expiresAt 从 expires_in(秒) 算出到期时间; 没有就返回零值(由刷新失败兜底)。
func expiresAt(m map[string]any) time.Time {
	for _, k := range []string{"expires_in", "expiresIn"} {
		switch v := m[k].(type) {
		case float64:
			if v > 0 {
				return time.Now().Add(time.Duration(v) * time.Second)
			}
		case json.Number:
			if i, err := v.Int64(); err == nil && i > 0 {
				return time.Now().Add(time.Duration(i) * time.Second)
			}
		}
	}
	return time.Time{}
}

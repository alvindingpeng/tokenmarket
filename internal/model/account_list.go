package model

import "time"

// AccountListEntry 是账号管理面的一个条目。
//
// 为什么单独定义而不直接返回 ChannelKey: 凭据的 Key 列是整包账号 JSON(含 access_token
// 与 refresh_token 明文), 而账号列表只需要"是哪个平台、哪个账号、还有多久到期"。
// 直接返回 ChannelKey 会把 token 明文带进 API 响应 —— 一旦有了这个先例,
// 后面任何人加一个字段都能顺手把它泄露出去。这里显式只取展示所需, 从结构上堵死。
type AccountListEntry struct {
	ChannelKeyID int    `json:"channel_key_id"` // 凭据主键, 界面之后"立即刷新"要用它。
	ChannelID    int    `json:"channel_id"`     // 所属渠道。
	ChannelName  string `json:"channel_name"`   // 渠道名, 账号列表里要显示归属。
	Name         string `json:"name"`           // 凭据名, 渠道内唯一。
	Enabled      bool   `json:"enabled"`        // 人工开关; 与熔断无关。
	// Kind 是 oauth/cookie: 前者能自动续期, 后者到期只能人工重导。
	// 界面靠它决定"显示成可续期的账号, 还是显示成会自己失效的会话"。
	Kind     string `json:"kind"`
	Platform string `json:"platform"`
	Email    string `json:"email"`
	// TokenExpiresAt 是当前访问令牌的到期时间; cookie 类没有这个值。
	TokenExpiresAt *time.Time `json:"token_expires_at,omitempty"`
	// TokenRefreshAt 是最近一次刷新成功的时刻。空值表示从未成功刷新过 ——
	// 这是"账号虽然导入了但一次都没活过"的唯一证据, 必须外露。
	TokenRefreshAt *time.Time `json:"token_refresh_at,omitempty"`
	// DisabledUntil 是凭据级熔断截止时间。它与到期时间无关: 到期时间在未来但熔断生效中,
	// 账号此刻同样转发不出去, 界面要能把这个情况与"正常"区分开。
	DisabledUntil *time.Time `json:"disabled_until,omitempty"`
	BreakerTrips  int        `json:"breaker_trips"`
	LastErrorAt   *time.Time `json:"last_error_at,omitempty"`
}

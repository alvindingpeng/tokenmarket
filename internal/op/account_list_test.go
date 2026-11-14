package op

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// seedListAccountKey 建一条账号凭据并同步进缓存 —— AccountList 只读缓存, 不查库。
//
// 这一点本身是关键设计: 列表必须与转发路径看到同一份数据。若这里改成查库,
// 测试反而会掩盖"缓存没同步"这类真实缺陷。
func seedListAccountKey(t *testing.T, channelID int, key model.ChannelKeyConfig) model.ChannelKey {
	t.Helper()
	channelKey := model.ChannelKey{ChannelID: channelID, ChannelKeyConfig: key}
	if err := db.GetDB().Create(&channelKey).Error; err != nil {
		t.Fatalf("create channel key: %v", err)
	}
	channelKeyCache.Set(channelKey.ID, channelKey)
	return channelKey
}

func seedListChannel(t *testing.T, name string, userID uint) model.Channel {
	t.Helper()
	channel := model.Channel{
		UserID: userID,
		ChannelConfig: model.ChannelConfig{
			Name: name + "-" + time.Now().Format("150405.000000000"), Enabled: true,
			BaseURL: "https://api.example.com",
		},
	}
	if err := db.GetDB().Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	channelCache.Set(channel.ID, channel)
	return channel
}

// TestAccountListExcludesStaticKeys 静态 Key 不该出现在账号列表里。
func TestAccountListExcludesStaticKeys(t *testing.T) {
	accountTestDB(t)
	channel := seedListChannel(t, "static", 1)
	seedListAccountKey(t, channel.ID, model.ChannelKeyConfig{Name: "plain", Key: "sk-static", Enabled: true})

	accounts := AccountList(model.Scope{ID: 1, Role: model.RoleAdmin})
	if len(accounts) != 0 {
		t.Fatalf("静态 Key 不应进账号列表, 得到 %d 条", len(accounts))
	}
}

// TestAccountListNeverExposesSecret 列表返回体绝不能带账号 JSON(含 access_token)。
//
// 这条用序列化后的字符串直接断言: 只要有任何一个字段把 Key 带出去,
// 无论字段叫什么名字, 都会在这里被捕获。比逐字段断言更严。
func TestAccountListNeverExposesSecret(t *testing.T) {
	accountTestDB(t)
	channel := seedListChannel(t, "secret", 1)
	secretJSON := `{"claudeAiOauth":{"accessToken":"sk-ant-oat01-SECRET-VALUE","refreshToken":"sk-ant-ort01-SECRET-VALUE"}}`
	seedListAccountKey(t, channel.ID, model.ChannelKeyConfig{
		Name: "acct", Key: secretJSON, Enabled: true,
		AccountKind: model.AccountKindOAuth, AccountPlatform: model.AccountPlatformClaude,
	})

	accounts := AccountList(model.Scope{ID: 1, Role: model.RoleAdmin})
	if len(accounts) != 1 {
		t.Fatalf("应得 1 条账号, 得到 %d", len(accounts))
	}
	raw, err := json.Marshal(accounts)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "SECRET-VALUE") {
		t.Fatalf("列表响应泄露了账号密钥: %s", raw)
	}
	// 也确认它不是靠"整条都不返回"蒙混过关的: 展示字段必须真的在。
	if !strings.Contains(string(raw), "claude") {
		t.Fatalf("平台字段应出现: %s", raw)
	}
}

// TestAccountListFiltersByScope 归属过滤: 非管理员只看自有的。
func TestAccountListFiltersByScope(t *testing.T) {
	accountTestDB(t)
	own := seedListChannel(t, "own", 1)
	other := seedListChannel(t, "other", 2)
	seedListAccountKey(t, own.ID, model.ChannelKeyConfig{Name: "a", Key: "{}", Enabled: true,
		AccountKind: model.AccountKindOAuth, AccountPlatform: model.AccountPlatformCodex})
	seedListAccountKey(t, other.ID, model.ChannelKeyConfig{Name: "b", Key: "{}", Enabled: true,
		AccountKind: model.AccountKindOAuth, AccountPlatform: model.AccountPlatformCodex})

	// 用户 1 不是管理员: 只看得到自己渠道下的那个账号。
	accounts := AccountList(model.Scope{ID: 1, Role: model.RoleUser})
	if len(accounts) != 1 {
		t.Fatalf("应得 1 条, 得到 %d", len(accounts))
	}
	if accounts[0].ChannelID != own.ID {
		t.Fatalf("拿到了别人渠道的账号: %+v", accounts[0])
	}
	// 管理员看全部。
	if all := AccountList(model.Scope{ID: 1, Role: model.RoleAdmin}); len(all) != 2 {
		t.Fatalf("管理员应得 2 条, 得到 %d", len(all))
	}
}

// TestAccountListOrderPutsProblemAccountsFirst 排序: 有问题的账号排前面。
//
// 账号管理面的用途就是找问题账号。若按名称或插入顺序排, 使用者要在一长串
// "正常"里翻找那一个失效的 —— 等于把功能做成了摆设。
func TestAccountListOrderPutsProblemAccountsFirst(t *testing.T) {
	accountTestDB(t)
	channel := seedListChannel(t, "order", 1)
	now := time.Now()
	future := now.Add(72 * time.Hour)
	past := now.Add(-time.Hour)
	expiring := now.Add(2 * time.Hour)
	breakerUntil := now.Add(30 * time.Minute)

	// 按名称故意让"正常"那个排在最前, 以此证明排序不是按名称来的。
	seedListAccountKey(t, channel.ID, model.ChannelKeyConfig{Name: "a-healthy", Key: "{}", Enabled: true,
		AccountKind: model.AccountKindOAuth, AccountPlatform: model.AccountPlatformCodex, TokenExpiresAt: &future})
	seedListAccountKey(t, channel.ID, model.ChannelKeyConfig{Name: "b-breaker", Key: "{}", Enabled: true,
		AccountKind: model.AccountKindOAuth, AccountPlatform: model.AccountPlatformCodex,
		TokenExpiresAt: &future, DisabledUntil: &breakerUntil})
	seedListAccountKey(t, channel.ID, model.ChannelKeyConfig{Name: "c-expired", Key: "{}", Enabled: true,
		AccountKind: model.AccountKindOAuth, AccountPlatform: model.AccountPlatformCodex, TokenExpiresAt: &past})
	seedListAccountKey(t, channel.ID, model.ChannelKeyConfig{Name: "d-cookie", Key: "{}", Enabled: true,
		AccountKind: model.AccountKindCookie, AccountPlatform: model.AccountPlatformGemini})
	seedListAccountKey(t, channel.ID, model.ChannelKeyConfig{Name: "e-expiring", Key: "{}", Enabled: true,
		AccountKind: model.AccountKindOAuth, AccountPlatform: model.AccountPlatformCodex, TokenExpiresAt: &expiring})

	accounts := AccountList(model.Scope{ID: 1, Role: model.RoleAdmin})
	want := []string{"b-breaker", "c-expired", "d-cookie", "e-expiring", "a-healthy"}
	if len(accounts) != len(want) {
		t.Fatalf("应得 %d 条, 得到 %d", len(want), len(accounts))
	}
	for i, name := range want {
		if accounts[i].Name != name {
			got := make([]string, len(accounts))
			for j, item := range accounts {
				got[j] = item.Name
			}
			t.Fatalf("排序不对:\n想要 %v\n得到 %v", want, got)
		}
	}
}

// TestAccountListCarriesChannelName 账号要显示归属渠道, 否则列表里的"这是谁的账号"无从判断。
func TestAccountListCarriesChannelName(t *testing.T) {
	accountTestDB(t)
	channel := seedListChannel(t, "named", 1)
	seedListAccountKey(t, channel.ID, model.ChannelKeyConfig{Name: "a", Key: "{}", Enabled: true,
		AccountKind: model.AccountKindOAuth, AccountPlatform: model.AccountPlatformCodex})

	accounts := AccountList(model.Scope{ID: 1, Role: model.RoleAdmin})
	if len(accounts) != 1 || accounts[0].ChannelName != channel.Name {
		t.Fatalf("渠道名未正确带回: %+v", accounts)
	}
}

// TestAccountListSkipsOrphanKeys 渠道已删除但凭据残留时, 不能出现在任何人的列表里。
func TestAccountListSkipsOrphanKeys(t *testing.T) {
	accountTestDB(t)
	// 不建渠道, 直接把凭据塞进缓存, 模拟数据不一致。
	channelKey := model.ChannelKey{ChannelID: 9999, ChannelKeyConfig: model.ChannelKeyConfig{
		Name: "orphan", Key: "{}", Enabled: true,
		AccountKind: model.AccountKindOAuth, AccountPlatform: model.AccountPlatformCodex,
	}}
	channelKeyCache.Set(channelKey.ID, channelKey)

	if accounts := AccountList(model.Scope{ID: 1, Role: model.RoleAdmin}); len(accounts) != 0 {
		t.Fatalf("孤儿凭据不应出现: %+v", accounts)
	}
}

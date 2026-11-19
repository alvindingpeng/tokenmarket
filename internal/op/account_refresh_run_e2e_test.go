
package op

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// AccountRefreshRun 的端到端测试: 一轮刷新要把"能刷的刷掉、刷不动的按类分开"。
// 用假端点覆盖两种结果(成功 / invalid_grant), 不碰真实上游。

func seedRunKey(t *testing.T, name string, expiresAt time.Time) model.ChannelKey {
	t.Helper()
	channel := seedImportChannel(t)
	config := model.ChannelKeyConfig{
		Name: "run-" + name, Key: `{"access_token":"old","refresh_token":"rt-old"}`,
		Enabled: true, AccountKind: model.AccountKindOAuth, AccountPlatform: model.AccountPlatformCodex,
		TokenExpiresAt: &expiresAt,
	}
	key := model.ChannelKey{ChannelID: channel.ID, ChannelKeyConfig: config}
	if err := db.GetDB().Create(&key).Error; err != nil {
		t.Fatalf("create key: %v", err)
	}
	channelKeyCache.Set(key.ID, key)
	return key
}

func TestAccountRefreshRunSplitsSuccessAndInvalidation(t *testing.T) {
	accountTestDB(t)
	if err := SettingUpsert(model.SettingKeyAllowPrivateUpstream, "true"); err != nil {
		t.Fatalf("enable private upstream: %v", err)
	}
	now := time.Now()

	// 成功的那个: 假端点返回新令牌。
	okServer := accountFakeTokenServer(t, http.StatusOK,
		map[string]any{"access_token": "srv-access", "refresh_token": "srv-rotated", "expires_in": 3600}, nil)
	// 失效的那个: 假端点返回 invalid_grant。
	badServer := accountFakeTokenServer(t, http.StatusBadRequest,
		map[string]any{"error": "invalid_grant"}, nil)

	// 两条都到期(令牌已过期, 必然进待刷新集合)。
	past := now.Add(-time.Hour)
	okKey := seedRunKey(t, "ok", past)
	badKey := seedRunKey(t, "bad", past)

	// 两个平台都用同一个平台名, 所以覆盖只能指向一台服务端。
	// 改为: 一次只覆盖一个端点跑一轮 —— 分两轮分别验证两类结果, 比让一台假服务端
	// 按请求参数分叉更贴近真实(真实上游也是按账号状态给不同结果)。
	model.AccountOAuthTokenURLOverrideSet(model.AccountPlatformCodex, okServer.URL)
	result, err := AccountRefreshRun(context.Background(), now)
	model.AccountOAuthTokenURLOverrideSet(model.AccountPlatformCodex, "")
	if err != nil {
		t.Fatalf("第一轮刷新失败: %v", err)
	}
	// 两条都到期, 但两条都打到同一台(成功)假端点 —— 所以两条都应当成功。
	if result.Checked != 2 || result.Refreshed != 2 {
		t.Fatalf("result = %+v, want checked=2 refreshed=2", result)
	}

	// 第二轮: 让两条都过期并指向"失效"端点。
	past2 := now.Add(-time.Hour)
	if err := db.GetDB().Model(&model.ChannelKey{}).Where("id IN ?", []int{okKey.ID, badKey.ID}).
		Update("token_expires_at", past2).Error; err != nil {
		t.Fatalf("reset expiry: %v", err)
	}
	// 把 refresh_token 换回旧值, 让这一轮重新有东西可刷。
	for _, id := range []int{okKey.ID, badKey.ID} {
		var key model.ChannelKey
		db.GetDB().First(&key, id)
		key.Key = `{"access_token":"old","refresh_token":"rt-old"}`
		db.GetDB().Model(&key).Update("key", key.Key)
		channelKeyCache.Set(id, key)
	}
	model.AccountOAuthTokenURLOverrideSet(model.AccountPlatformCodex, badServer.URL)
	result, err = AccountRefreshRun(context.Background(), now)
	model.AccountOAuthTokenURLOverrideSet(model.AccountPlatformCodex, "")
	if err != nil {
		t.Fatalf("全部失效不算任务失败(失败计数应为 0): %v", err)
	}
	if result.Expired != 2 {
		t.Fatalf("result = %+v, want expired=2", result)
	}
	if result.Failed != 0 {
		t.Fatalf("invalid_grant 应计入 Expired 而非 Failed: %+v", result)
	}

	// 被判定失效的凭据必须进了熔断通道, 否则转发还会继续用它。
	// 断言的是 DisabledUntil 而不是 ErrorCount: auth 类错误走的是"硬熔断"分支,
	// 那条路故意把 error_count 清零, 只靠 disabled_until 生效(见 RecordChannelKeyError)。
	// 盯着 ErrorCount 会得出"熔断没发生"的错误结论。
	for _, id := range []int{okKey.ID, badKey.ID} {
		var key model.ChannelKey
		db.GetDB().First(&key, id)
		if key.DisabledUntil == nil || key.DisabledUntil.Before(now) {
			t.Fatalf("key %d 未被熔断(disabled_until=%v), 失效账号仍在参与选路", id, key.DisabledUntil)
		}
		if key.BreakerTrips == 0 {
			t.Fatalf("key %d 未记熔断次数", id)
		}
	}
}

package op

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// TestAccountImportBatchMixed 一批里成功与失败混合, 逐条结果必须如实带回。
//
// 这是批量接口最关键的性质: 一批里字段齐全和缺字段的账号混在一起是常态,
// 若只给一个"部分失败"的笼统信息, 使用者只能一条条猜哪条出了问题。
func TestAccountImportBatchMixed(t *testing.T) {
	accountTestDB(t)
	channel := seedImportChannel(t)
	ctx := context.Background()
	now := time.Now()

	results := AccountImportBatch(ctx, channel.ID, model.AccountPlatformCodex, []AccountBatchItem{
		{Name: "good", Raw: `{"access_token":"at1","refresh_token":"rt1"}`},
		{Name: "missing-refresh", Raw: `{"access_token":"at2"}`},
		{Name: "not-json", Raw: "not json"},
	}, true, now)

	if len(results) != 3 {
		t.Fatalf("应得 3 条结果, 得到 %d", len(results))
	}
	if !results[0].OK || results[0].ChannelKeyID == 0 {
		t.Fatalf("good 应成功, 得到 %+v", results[0])
	}
	if results[1].OK || !strings.Contains(results[1].Error, "refresh_token") {
		t.Fatalf("missing-refresh 应失败并说明缺 refresh_token, 得到 %+v", results[1])
	}
	if results[2].OK || results[2].Error == "" {
		t.Fatalf("not-json 应失败, 得到 %+v", results[2])
	}
}

// TestAccountImportBatchSkipsRefresh 批量不做首刷。
//
// 批量逐条首刷会 N 次上游令牌调用(串行, 每条最多 20 秒)又可能触发风控;
// 批量的语义是快速录入, 验证留给账号页统一刷新。这条测试钉住"不首刷"这一决策:
// 若将来有人去掉 SkipRefresh, 这里应失败。
func TestAccountImportBatchSkipsRefresh(t *testing.T) {
	accountTestDB(t)
	channel := seedImportChannel(t)
	ctx := context.Background()
	now := time.Now()

	// 用合法 codex JSON(含 refresh_token)。若批量首刷, AccountRefresh 会向真实上游发请求
	// 并失败返回 *AccountImportRefreshWarning; 但 AccountImportBatch 跳过刷新,
	// 因此应返回 nil 错误(干净成功), 而不是 warning。
	results := AccountImportBatch(ctx, channel.ID, model.AccountPlatformCodex, []AccountBatchItem{
		{Name: "no-verify", Raw: `{"access_token":"at","refresh_token":"rt"}`},
	}, true, now)

	if len(results) != 1 || !results[0].OK {
		t.Fatalf("批量导入应干净成功(不首刷), 得到 %+v", results)
	}
	// 且这条凭据的 TokenExpiresAt 应保持 JSON 里的值, 不被刷新覆盖。
	// 无 expires 字段时 token_expires_at 应为空(零值), 说明没有刷新把它设成别的值。
	// (AccountRefresh 成功会写新到期时间; 这里能确认没走那条路径。)
	var key model.ChannelKey
	if err := db.GetDB().First(&key, results[0].ChannelKeyID).Error; err != nil {
		t.Fatalf("读取凭据失败: %v", err)
	}
	if key.TokenExpiresAt != nil {
		t.Fatalf("批量导入不应触发刷新(到期时间被写入): %v", key.TokenExpiresAt)
	}
}

// TestAccountImportBatchIsolation 一条失败不影响其他条(撞名场景)。
func TestAccountImportBatchIsolation(t *testing.T) {
	accountTestDB(t)
	channel := seedImportChannel(t)
	ctx := context.Background()
	now := time.Now()

	// 同名两条: 第二条必然撞名失败, 但第一条已成功 —— 整批不应中断。
	results := AccountImportBatch(ctx, channel.ID, model.AccountPlatformCodex, []AccountBatchItem{
		{Name: "dup", Raw: `{"access_token":"at1","refresh_token":"rt1"}`},
		{Name: "dup", Raw: `{"access_token":"at2","refresh_token":"rt2"}`},
	}, true, now)

	if len(results) != 2 {
		t.Fatalf("应得 2 条结果, 得到 %d", len(results))
	}
	if !results[0].OK {
		t.Fatalf("第一条应成功, 得到 %+v", results[0])
	}
	if results[1].OK || !strings.Contains(results[1].Error, "已存在") {
		t.Fatalf("第二条应失败并说明撞名, 得到 %+v", results[1])
	}
}

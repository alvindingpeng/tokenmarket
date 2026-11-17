package op

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// TestSelectChannelKeyRotatesAcrossAccounts 是 S4 的核心测试。
//
// 背景: handler 原先无条件使用 grant.ChannelKey, 使 SelectChannelKey 在转发主路径成为死代码,
// UI 上的 key_rotation_strategy 与 channel_keys.priority 都不生效。S4 让订阅渠道改走
// 「按渠道选 key」, 本测试锁定「选出来的确实是不同的账号」。
func TestSelectChannelKeyRotatesAcrossAccounts(t *testing.T) {
	ids := makeKeyTestChannel(t, "rotate")
	seen := map[int]int{}
	for i := 0; i < len(ids); i++ {
		key, err := SelectChannelKey(ids[0], "roundrobin")
		if err != nil {
			t.Fatalf("第 %d 次选择失败: %v", i, err)
		}
		seen[key.ID]++
	}
	if len(seen) != len(ids) {
		t.Fatalf("轮询应覆盖 %d 个账号, 实际覆盖 %d 个: %v", len(ids), len(seen), seen)
	}
}

// TestSelectChannelKeyFailoverUsesPriority failover 必须按 priority 取最优先的账号。
//
// 订阅渠道多账号的价值一半在轮换, 另一半在优先级: 主账号用完才切备用账号。
// 若 failover 不按 priority 排序, 备用账号可能先被用掉。
// 这里把 priority 设成与 id 相反的次序, 以便区分「按 priority」与「按 id」。
func TestSelectChannelKeyFailoverUsesPriority(t *testing.T) {
	ids := makeKeyTestChannel(t, "failover")
	// 建 key 时 priority 递增, 即 ids[0] 的 priority 最小, 是首选。
	key, err := SelectChannelKey(ids[0], "failover")
	if err != nil {
		t.Fatalf("failover 选择失败: %v", err)
	}
	if key.ID != ids[0] {
		t.Fatalf("failover 应取 priority 最小者 id=%d, 实际 id=%d", ids[0], key.ID)
	}
}

// TestSelectChannelKeyExcludesBrokenAndDisabled 熔断中与已停用的账号必须出局。
//
// 订阅账号额度用尽会被硬熔断(disabled_until 在未来)。若它仍被选中,
// 请求会持续打到已失效的账号上 —— 这正是多账号轮换最容易出的故障。
func TestSelectChannelKeyExcludesBrokenAndDisabled(t *testing.T) {
	ids := makeKeyTestChannel(t, "exclude")
	future := time.Now().Add(time.Hour)
	// 第一把 key 熔断中, 第二把被停用, 只留第三把可用。
	if err := db.GetDB().Model(&model.ChannelKey{}).Where("id = ?", ids[0]).
		Update("disabled_until", &future).Error; err != nil {
		t.Fatalf("标记熔断: %v", err)
	}
	if err := db.GetDB().Model(&model.ChannelKey{}).Where("id = ?", ids[1]).
		Update("enabled", false).Error; err != nil {
		t.Fatalf("停用账号: %v", err)
	}

	key, err := SelectChannelKey(ids[0], "roundrobin")
	if err != nil {
		t.Fatalf("仍有可用账号时不应失败: %v", err)
	}
	if key.ID != ids[2] {
		t.Fatalf("只剩 id=%d 可用, 实际选中 id=%d", ids[2], key.ID)
	}
}

// TestSelectChannelKeyFailsWhenAllUnavailable 全部账号不可用时必须报错, 不能返回已失效的账号。
//
// 这是上面那条的反面: 若实现「挑不到就返回第一个」, 会把停用账号当可用账号发出去。
func TestSelectChannelKeyFailsWhenAllUnavailable(t *testing.T) {
	ids := makeKeyTestChannel(t, "allbad")
	future := time.Now().Add(time.Hour)
	if err := db.GetDB().Model(&model.ChannelKey{}).Where("channel_id = ?", ids[0]).
		Update("disabled_until", &future).Error; err != nil {
		t.Fatalf("全部熔断: %v", err)
	}
	if _, err := SelectChannelKey(ids[0], "roundrobin"); err == nil {
		t.Fatal("全部账号熔断时必须报错, 不能返回失效账号")
	}
}

// makeKeyTestChannel 建一个带 n 个启用账号的渠道, 返回各 key 的 id(升序)。
//
// 渠道名逐个不同: channels 上有 (user_id, name) 唯一索引, 重用同名会让第二个用例建渠道失败。
func makeKeyTestChannel(t *testing.T, name string) []int {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), fmt.Sprintf("%s.db", name)), false); err != nil {
		t.Fatalf("初始化测试库: %v", err)
	}
	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: fmt.Sprintf("订阅渠道-%s", name)}}
	if err := db.GetDB().Create(&channel).Error; err != nil {
		t.Fatalf("建渠道: %v", err)
	}
	const n = 3
	ids := make([]int, 0, n)
	for i := 1; i <= n; i++ {
		key := model.ChannelKey{
			ChannelID: channel.ID,
			ChannelKeyConfig: model.ChannelKeyConfig{
				Name:        fmt.Sprintf("账号%d", i),
				Key:         "token",
				Priority:    i,
				Enabled:     true,
				AccountKind: model.AccountKindOAuth,
			},
		}
		if err := db.GetDB().Create(&key).Error; err != nil {
			t.Fatalf("建账号 key: %v", err)
		}
		ids = append(ids, key.ID)
	}
	return ids
}

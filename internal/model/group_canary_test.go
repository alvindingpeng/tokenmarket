package model

import "testing"

// TestCanaryHitStickyPerKey 校验按比例的灰度对同一 API Key 恒定命中同一结果。
// 粘性是灰度可用性的前提: 若同一客户端的连续请求在两条路径间来回抖动,
// 长对话会被拆到两个上游, 上下文缓存全丢, 灰度样本也不再代表"某个用户在使用新渠道"。
func TestCanaryHitStickyPerKey(t *testing.T) {
	config := GroupRelayConfig{CanaryItemID: 7, CanaryPercent: 30}
	hits, misses := 0, 0
	for keyID := 1; keyID <= 200; keyID++ {
		first := config.CanaryHit(1, keyID, 1)
		for seed := uint64(2); seed < 6; seed++ {
			if config.CanaryHit(1, keyID, seed) != first {
				t.Fatalf("key %d hit changed between requests: sticky bucketing broken", keyID)
			}
		}
		if first {
			hits++
		} else {
			misses++
		}
	}
	// 30% 的比例落成 100 个桶, 200 把 Key 的命中数应有几十: 只要求两侧都非空且大致均衡,
	// 精确到个位会让测试随散列实现微调而脆弱。
	if hits < 30 || misses < 30 {
		t.Fatalf("canary percent 30 split hits=%d misses=%d, want both substantial", hits, misses)
	}
}

// TestCanaryHitExplicitUsersOverridePercent 校验显式用户列表优先于比例:
// 指定用户是"让这个人先用起来"的确定性安排, 被比例稀释成概率就失去意义。
func TestCanaryHitExplicitUsersOverridePercent(t *testing.T) {
	config := GroupRelayConfig{CanaryItemID: 7, CanaryPercent: 1, CanaryUserIDs: []int{42, 43}}
	for i := 0; i < 20; i++ {
		if !config.CanaryHit(42, 900+i, uint64(i)) {
			t.Fatalf("listed user 42 missed canary on iteration %d", i)
		}
		if !config.CanaryHit(43, 900+i, uint64(i)) {
			t.Fatalf("listed user 43 missed canary on iteration %d", i)
		}
	}
}

// TestCanaryHitBounds 校验关闭态与边界比例: 未指定成员或比例为 0 时永不命中, 100% 时恒定命中。
func TestCanaryHitBounds(t *testing.T) {
	if (GroupRelayConfig{CanaryItemID: 0, CanaryPercent: 100}).CanaryHit(1, 1, 1) {
		t.Fatal("canary without member should never hit")
	}
	if (GroupRelayConfig{CanaryItemID: 7, CanaryPercent: 0}).CanaryHit(1, 1, 1) {
		t.Fatal("canary with zero percent should never hit")
	}
	full := GroupRelayConfig{CanaryItemID: 7, CanaryPercent: 100}
	for keyID := 1; keyID <= 50; keyID++ {
		if !full.CanaryHit(1, keyID, 1) {
			t.Fatalf("100%% canary missed key %d", keyID)
		}
	}
}

// TestCanaryHitPerRequestSamples 校验 canary_per_request 打开后按请求抽样而不是按 Key 定死,
// 用于"只要先来一点样本"的场合; 同一请求种子仍应给出同一结论。
func TestCanaryHitPerRequestSamples(t *testing.T) {
	config := GroupRelayConfig{CanaryItemID: 7, CanaryPercent: 50, CanaryPerRequest: true}
	hits := 0
	for seed := uint64(0); seed < 100; seed++ {
		hit := config.CanaryHit(1, 1, seed)
		if config.CanaryHit(1, 1, seed) != hit {
			t.Fatalf("seed %d produced unstable result", seed)
		}
		if hit {
			hits++
		}
	}
	if hits < 20 || hits > 80 {
		t.Fatalf("per-request sampling hits=%d of 100 at 50%%, want roughly half", hits)
	}
}

// TestNormalizeGroupRelayConfigCanary 校验灰度配置的范围收敛与用户列表清洗。
// 越界比例会让桶号比较失效, 非法用户 ID 会让列表长期膨胀且永不命中。
func TestNormalizeGroupRelayConfigCanary(t *testing.T) {
	config := GroupRelayConfig{CanaryItemID: -3, CanaryPercent: 250, CanaryUserIDs: []int{3, 0, -1, 3, 5}}
	NormalizeGroupRelayConfigForMode(&config, GroupModeScore)
	if config.CanaryItemID != 0 || config.CanaryPercent != 100 {
		t.Fatalf("canary bounds = %d/%d, want 0/100", config.CanaryItemID, config.CanaryPercent)
	}
	if len(config.CanaryUserIDs) != 2 || config.CanaryUserIDs[0] != 3 || config.CanaryUserIDs[1] != 5 {
		t.Fatalf("canary user ids not cleaned: %v", config.CanaryUserIDs)
	}
}

// TestNormalizeGroupRelayConfigEmptyStillDefaults 校验加了切片字段后"全零配置"判定仍然成立:
// 结构体因含切片不可再用 == 比较, 若判定失效, 新建分组会得不到任何默认参数。
func TestNormalizeGroupRelayConfigEmptyStillDefaults(t *testing.T) {
	config := GroupRelayConfig{}
	NormalizeGroupRelayConfigForMode(&config, GroupModeScore)
	if config.MemberMaxAttempts != 1 || config.MaxTotalAttempts != 10 || config.MemberNonStreamResponseTimeoutSeconds != 120 {
		t.Fatalf("empty config did not receive defaults: %+v", config)
	}
}

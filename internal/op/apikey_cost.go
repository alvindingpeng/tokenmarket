package op

import (
	"context"
	"sync"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
)

// APIKeyCostAggregate 是 max_cost 判定的数据库兜底: 按 billing_records 聚合某 Key 的累计用户支出。
//
// 为什么需要兜底: 内存统计(stats_api_key 缓存)只在落库回写后可靠 —— 进程崩溃/被 kill 时
// 脏缓存丢失, 重启后该 Key 的花费从零起算, max_cost 上限失效, 超支 Key 悄悄恢复可用。
// 聚合 billing_records 取 max(内存, 数据库): 数据库永远不低估已结算的花费, 两者取大
// 既覆盖崩溃丢失, 也不让尚未落库的实时用量被漏算。
//
// 为什么不每次都聚合: billing_records 可能很大, auth 在每个请求的关键路径上;
// 按 Key 做 5 分钟 TTL 缓存, 把兜底查询的代价摊平, 而误差上限就是这 5 分钟内的
// 新增结算额 —— 远小于崩溃丢失的整段内存统计。

const (
	apiKeyCostAggTTL   = 5 * time.Minute
	apiKeyCostAggLimit = 4096 // 缓存条数上限, 防异常 Key 数量撑爆内存
)

type apiKeyCostAggEntry struct {
	value     float64
	fetchedAt time.Time
}

var (
	apiKeyCostAggMu    sync.Mutex
	apiKeyCostAggCache = make(map[int]apiKeyCostAggEntry)
)

// APIKeyCostSumDB 按 billing_records(user_cost, status=settled) 聚合某 Key 的累计支出。
// 查询失败返回 (0, err): 调用方决定失败时的口径(判定处按内存值放行, 不因兜底故障误杀请求)。
func APIKeyCostSumDB(ctx context.Context, apiKeyID int) (float64, error) {
	var total float64
	err := db.GetDB().WithContext(ctx).
		Model(nil).
		Table("billing_records").
		Select("COALESCE(SUM(user_cost), 0)").
		Where("api_key_id = ? AND status = ?", apiKeyID, "settled").
		Scan(&total).Error
	return total, err
}

// APIKeyCostEffective max_cost 判定的有效花费口径: max(内存实时统计, 数据库聚合兜底)。
// 聚合结果带 TTL 缓存; DB 错误时退回内存值, 保证判定永远有结果且不因兜底故障拒绝请求。
func APIKeyCostEffective(ctx context.Context, apiKeyID int, memoryCost float64) float64 {
	now := time.Now()
	apiKeyCostAggMu.Lock()
	if entry, ok := apiKeyCostAggCache[apiKeyID]; ok && now.Sub(entry.fetchedAt) < apiKeyCostAggTTL {
		apiKeyCostAggMu.Unlock()
		return maxFloat(memoryCost, entry.value)
	}
	apiKeyCostAggMu.Unlock()

	total, err := APIKeyCostSumDB(ctx, apiKeyID)
	if err != nil || total < 0 {
		// 兜底故障不阻塞认证: 按内存口径判定。失败不缓存, 下次请求重试。
		return memoryCost
	}
	apiKeyCostAggMu.Lock()
	if len(apiKeyCostAggCache) >= apiKeyCostAggLimit {
		// 简单防膨胀: 清掉过期项; 仍超限时整体重置 —— 缓存只影响性能, 不影响正确性。
		for id, entry := range apiKeyCostAggCache {
			if now.Sub(entry.fetchedAt) >= apiKeyCostAggTTL {
				delete(apiKeyCostAggCache, id)
			}
		}
		if len(apiKeyCostAggCache) >= apiKeyCostAggLimit {
			apiKeyCostAggCache = make(map[int]apiKeyCostAggEntry)
		}
	}
	apiKeyCostAggCache[apiKeyID] = apiKeyCostAggEntry{value: total, fetchedAt: now}
	apiKeyCostAggMu.Unlock()
	return maxFloat(memoryCost, total)
}

// apiKeyCostAggInvalidate 清掉某 Key 的兜底缓存(结算落库后调用, 让兜底立即反映新账目)。
func apiKeyCostAggInvalidate(apiKeyID int) {
	apiKeyCostAggMu.Lock()
	delete(apiKeyCostAggCache, apiKeyID)
	apiKeyCostAggMu.Unlock()
}

func maxFloat(a, b float64) float64 {
	if a >= b {
		return a
	}
	return b
}

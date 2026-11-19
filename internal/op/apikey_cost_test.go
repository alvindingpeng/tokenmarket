package op

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// costAggTestDB 临时库: 兜底聚合直接读 db.GetDB()。
func costAggTestDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "costagg.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
}

// 核心回归: 崩溃丢内存缓存后(内存=0), 兜底聚合必须仍能挡住超支 Key。
// 数据库里已结算 user_cost=100, max_cost=80: 无论内存口径是多少, 有效花费 >= 100, 必须超限。
func TestAPIKeyCostEffectiveUsesDBFloor(t *testing.T) {
	costAggTestDB(t)
	apiKeyCostAggMu.Lock()
	apiKeyCostAggCache = make(map[int]apiKeyCostAggEntry)
	apiKeyCostAggMu.Unlock()
	ctx := context.Background()

	if err := db.GetDB().Create(&model.BillingRecord{APIKeyID: 77, UserCost: 100, Status: model.BillingStatusSettled}).Error; err != nil {
		t.Fatalf("seed billing record: %v", err)
	}

	// 内存统计丢失(=0)的崩溃场景: 兜底必须顶上。
	got := APIKeyCostEffective(ctx, 77, 0)
	if got < 100 {
		t.Fatalf("effective cost = %v, want >= 100 from DB fallback", got)
	}
	// 内存口径更大时取大者(实时未落库的用量不能被 DB 旧值盖掉)。
	if got := APIKeyCostEffective(ctx, 77, 150); got != 150 {
		t.Fatalf("effective cost with larger memory = %v, want 150", got)
	}
}

// 退款行(refunded)不计入兜底口径: 退款已把花费退回, 仍按原额聚合会冤枉用户。
func TestAPIKeyCostEffectiveIgnoresRefunded(t *testing.T) {
	costAggTestDB(t)
	ctx := context.Background()
	if err := db.GetDB().Create(&model.BillingRecord{APIKeyID: 78, UserCost: 50, Status: model.BillingStatusRefunded}).Error; err != nil {
		t.Fatalf("seed refunded record: %v", err)
	}
	total, err := APIKeyCostSumDB(ctx, 78)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if total != 0 {
		t.Fatalf("refunded rows must not count: got %v", total)
	}
}

// 兜底查询失败时不阻塞判定: 退回内存口径。
func TestAPIKeyCostEffectiveFallsBackOnMemoryWhenDBBroken(t *testing.T) {
	// 不初始化 DB: db.GetDB() 为 nil 时查询必然失败。
	apiKeyCostAggMu.Lock()
	apiKeyCostAggCache = make(map[int]apiKeyCostAggEntry)
	apiKeyCostAggMu.Unlock()
	if got := APIKeyCostEffective(context.Background(), 99, 42); got != 42 {
		t.Fatalf("DB failure must fall back to memory: got %v, want 42", got)
	}
}

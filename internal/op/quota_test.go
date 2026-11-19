package op

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

func quotaTestSetup(t *testing.T) model.APIKey {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "quota.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// 明文由 APIKeyCreate 统一生成: 自定义密钥已停用(低熵自选值会让指纹被字典爆破)。
	key := model.APIKey{Name: "quota", QuotaRequests: 10, QuotaInputTokens: 100, QuotaOutputTokens: 50}
	if err := APIKeyCreate(&key, context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { apiKeyCache.Del(key.ID); apiKeyFingerprintMap.Del(model.APIKeyFingerprint(key.APIKey)) })
	return key
}

func TestAPIKeyQuotaUsesPersistedHourlySchema(t *testing.T) {
	key := quotaTestSetup(t)
	ctx := context.Background()
	start := apiKeyQuotaResetTime(time.Now(), 0)
	rows := []model.RateLimitUsageHourly{
		{ScopeType: model.RateScopeAPIKey, ScopeID: key.ID, Hour: start.Add(-time.Hour), Requests: 1000},
		{ScopeType: model.RateScopeUser, ScopeID: key.ID, Hour: start, Requests: 1000},
		{ScopeType: model.RateScopeAPIKey, ScopeID: key.ID + 1, Hour: start, Requests: 1000},
		{ScopeType: model.RateScopeAPIKey, ScopeID: key.ID, Hour: start, Requests: 4, InputTokens: 30, OutputTokens: 10},
		{ScopeType: model.RateScopeAPIKey, ScopeID: key.ID, Hour: start.Add(time.Hour), Requests: 5, InputTokens: 40, OutputTokens: 15},
	}
	if err := db.GetDB().Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	r, i, o, err := GetAPIKeyQuotaUsage(ctx, key.ID)
	if err != nil || r != 9 || i != 70 || o != 25 {
		t.Fatalf("usage=%d/%d/%d err=%v", r, i, o, err)
	}
	if err := CheckAPIKeyQuota(ctx, key.ID); err != nil {
		t.Fatal(err)
	}
	key.QuotaRequests = 9
	apiKeyCache.Set(key.ID, key)
	var exceeded QuotaExceeded
	if err := CheckAPIKeyQuota(ctx, key.ID); !errors.As(err, &exceeded) || exceeded.Kind != "requests" {
		t.Fatalf("expected request limit, got %v", err)
	}
	key.QuotaRequests = 0
	key.QuotaInputTokens = 70
	apiKeyCache.Set(key.ID, key)
	if err := CheckAPIKeyQuota(ctx, key.ID); !errors.As(err, &exceeded) || exceeded.Kind != "input_tokens" {
		t.Fatalf("expected input limit, got %v", err)
	}
	key.QuotaInputTokens = 0
	key.QuotaOutputTokens = 25
	apiKeyCache.Set(key.ID, key)
	if err := CheckAPIKeyQuota(ctx, key.ID); !errors.As(err, &exceeded) || exceeded.Kind != "output_tokens" {
		t.Fatalf("expected output limit, got %v", err)
	}
}

func TestAPIKeyQuotaEmptyUnlimitedAndDatabaseErrors(t *testing.T) {
	key := quotaTestSetup(t)
	ctx := context.Background()
	r, i, o, err := GetAPIKeyQuotaUsage(ctx, key.ID)
	if err != nil || r != 0 || i != 0 || o != 0 {
		t.Fatalf("empty usage: %d/%d/%d %v", r, i, o, err)
	}
	if err := CheckAPIKeyQuota(ctx, key.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().Migrator().DropTable(&model.RateLimitUsageHourly{}); err != nil {
		t.Fatal(err)
	}
	if err := CheckAPIKeyQuota(ctx, key.ID); err == nil {
		t.Fatal("database error must not be treated as zero usage")
	}
	if _, _, _, err := GetAPIKeyQuotaUsage(ctx, key.ID); err == nil {
		t.Fatal("usage database error not returned")
	}
	key.QuotaRequests = 0
	key.QuotaInputTokens = 0
	key.QuotaOutputTokens = 0
	apiKeyCache.Set(key.ID, key)
	if err := CheckAPIKeyQuota(ctx, key.ID); err != nil {
		t.Fatalf("unlimited key should bypass usage query: %v", err)
	}
	if err := CheckAPIKeyQuota(ctx, key.ID+100); err == nil {
		t.Fatal("missing key accepted")
	}
}

func TestAPIKeyQuotaCustomResetFiltersTimeValues(t *testing.T) {
	key := quotaTestSetup(t)
	zone := time.FixedZone("UTC+8", 8*60*60)
	reset := apiKeyQuotaResetTime(time.Date(2026, 1, 2, 7, 0, 0, 0, zone), 8)
	rows := []model.RateLimitUsageHourly{
		{ScopeType: model.RateScopeAPIKey, ScopeID: key.ID, Hour: reset.Add(-time.Hour), Requests: 99},
		{ScopeType: model.RateScopeAPIKey, ScopeID: key.ID, Hour: reset, Requests: 3, InputTokens: 7},
		{ScopeType: model.RateScopeAPIKey, ScopeID: key.ID, Hour: reset.Add(time.Hour), Requests: 4, OutputTokens: 8},
	}
	if err := db.GetDB().Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	got, err := loadAPIKeyQuotaUsage(context.Background(), key.ID, reset)
	if err != nil || got.Requests != 7 || got.InputTokens != 7 || got.OutputTokens != 8 {
		t.Fatalf("custom reset usage=%+v err=%v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := loadAPIKeyQuotaUsage(ctx, key.ID, reset); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled query error=%v", err)
	}
}

func TestAPIKeyQuotaResetCalendarBoundary(t *testing.T) {
	zone := time.FixedZone("UTC+8", 8*60*60)
	for _, tc := range []struct {
		name string
		now  time.Time
		hour int
		want time.Time
	}{
		{"before reset", time.Date(2026, 1, 2, 3, 0, 0, 0, zone), 8, time.Date(2026, 1, 1, 8, 0, 0, 0, zone)},
		{"at reset", time.Date(2026, 1, 2, 8, 0, 0, 0, zone), 8, time.Date(2026, 1, 2, 8, 0, 0, 0, zone)},
		{"invalid reset", time.Date(2026, 1, 2, 8, 0, 0, 0, zone), 24, time.Date(2026, 1, 2, 0, 0, 0, 0, zone)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := apiKeyQuotaResetTime(tc.now, tc.hour); !got.Equal(tc.want) {
				t.Fatalf("reset=%v want=%v", got, tc.want)
			}
		})
	}
	zoneDST, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 9, 0, 30, 0, 0, zoneDST)
	want := time.Date(2026, 3, 8, 8, 0, 0, 0, zoneDST)
	if got := apiKeyQuotaResetTime(now, 8); !got.Equal(want) {
		t.Fatalf("DST reset=%v want=%v", got, want)
	}
}

// tenantQuotaFixture 两个租户 + 四个用户, 主键顺序刻意让「租户 ID」与「别的租户的用户 ID」撞号。
//
// 关键: tenants 表带默认租户(1), 所以新建租户拿到 2、3; users 表从 1 起。
// 先建租户 B 的两个用户(1、2), 再建租户 A 的用户(3、4), 于是 userB2.ID == tenantA.ID == 2。
// 旧口径(拿租户 ID 去查 scope_type='user' 的行)因此会把 B 租户用户的用量算进 A —— 这就是
// 生产上"偶发把别人的用量算进来"的形态, 用例必须钉住它。
type tenantQuotaFixture struct {
	tenantA, tenantB model.Tenant
	userA1, userA2   model.User // 租户 A 的两个成员: 用量要合并计入租户 A。
	userB1, userB2   model.User // 租户 B 的两个成员: 一个撞号, 一个不撞。
}

func tenantQuotaTestSetup(t *testing.T) tenantQuotaFixture {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "tenant-quota.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tenants := []model.Tenant{
		{Name: "tenant-a", Code: "tenant-a", Status: model.TenantStatusActive},
		{Name: "tenant-b", Code: "tenant-b", Status: model.TenantStatusActive},
	}
	if err := db.GetDB().Create(&tenants).Error; err != nil {
		t.Fatal(err)
	}
	// 建号顺序决定 ID: 租户 B 的用户先落, 保证有一个用户的 ID 与 tenantA.ID 相同。
	ordering := []model.User{
		{Username: "b1", Role: model.RoleUser, TenantID: tenants[1].ID, Status: model.StatusActive},
		{Username: "b2", Role: model.RoleUser, TenantID: tenants[1].ID, Status: model.StatusActive},
		{Username: "a1", Role: model.RoleUser, TenantID: tenants[0].ID, Status: model.StatusActive},
		{Username: "a2", Role: model.RoleUser, TenantID: tenants[0].ID, Status: model.StatusActive},
	}
	if err := db.GetDB().Create(&ordering).Error; err != nil {
		t.Fatal(err)
	}
	return tenantQuotaFixture{
		tenantA: tenants[0], tenantB: tenants[1],
		userA1: ordering[2], userA2: ordering[3],
		userB1: ordering[0], userB2: ordering[1],
	}
}

// TestTenantQuotaUsageCountsAllTenantMembers 租户内多个用户的用量都要计入: 租户配额是池,
// 只按单个用户统计会漏账, 只看 tenant 维度又会丢掉升级前的存量行。
func TestTenantQuotaUsageCountsAllTenantMembers(t *testing.T) {
	fx := tenantQuotaTestSetup(t)
	ctx := context.Background()
	reset := time.Now().Add(-24 * time.Hour).Truncate(time.Hour)
	// 存量小时只有 user 行, 新小时以 tenant 行为准 —— 两者相加才是租户的真实总用量。
	legacyHour := reset
	newHour := reset.Add(time.Hour)
	rows := []model.RateLimitUsageHourly{
		{ScopeType: model.RateScopeUser, ScopeID: int(fx.userA1.ID), Hour: legacyHour, Requests: 3, InputTokens: 30, OutputTokens: 10},
		{ScopeType: model.RateScopeUser, ScopeID: int(fx.userA2.ID), Hour: legacyHour, Requests: 4, InputTokens: 40, OutputTokens: 20},
		{ScopeType: model.RateScopeTenant, ScopeID: int(fx.tenantA.ID), Hour: newHour, Requests: 5, InputTokens: 50, OutputTokens: 25},
	}
	if err := db.GetDB().Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	usage, err := loadTenantQuotaUsage(ctx, fx.tenantA.ID, reset)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Requests != 12 || usage.InputTokens != 120 || usage.OutputTokens != 55 {
		t.Fatalf("tenant usage=%+v, want 12/120/55", usage)
	}
}

// TestTenantQuotaUsageExcludesOtherTenants 其它租户的用户用量与 tenant 行都不能计入本租户。
func TestTenantQuotaUsageExcludesOtherTenants(t *testing.T) {
	fx := tenantQuotaTestSetup(t)
	if fx.userB2.ID != fx.tenantA.ID {
		t.Fatalf("fixture drift: userB2.ID=%d tenantA.ID=%d, 撞号场景已丢失", fx.userB2.ID, fx.tenantA.ID)
	}
	ctx := context.Background()
	reset := time.Now().Add(-24 * time.Hour).Truncate(time.Hour)
	// A 的存量小时与 B 的全部用量同小时; B 的 tenant 行另置一小时, 与自己的 user 行分开。
	rows := []model.RateLimitUsageHourly{
		{ScopeType: model.RateScopeUser, ScopeID: int(fx.userA1.ID), Hour: reset, Requests: 2},
		{ScopeType: model.RateScopeUser, ScopeID: int(fx.userA2.ID), Hour: reset, Requests: 30, InputTokens: 300},
		{ScopeType: model.RateScopeUser, ScopeID: int(fx.userB1.ID), Hour: reset, Requests: 100, InputTokens: 1000},
		{ScopeType: model.RateScopeUser, ScopeID: int(fx.userB2.ID), Hour: reset, Requests: 7},
		{ScopeType: model.RateScopeTenant, ScopeID: int(fx.tenantB.ID), Hour: reset.Add(time.Hour), Requests: 50, OutputTokens: 500},
	}
	if err := db.GetDB().Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	usageA, err := loadTenantQuotaUsage(ctx, fx.tenantA.ID, reset)
	if err != nil {
		t.Fatal(err)
	}
	// A 只算自己两个成员: 2 + 30; 不含 B 的任何行。旧口径会把撞号的 userB2(7) 也算进来。
	if usageA.Requests != 32 || usageA.InputTokens != 300 || usageA.OutputTokens != 0 {
		t.Fatalf("tenant A usage=%+v, want 32/300/0", usageA)
	}
	usageB, err := loadTenantQuotaUsage(ctx, fx.tenantB.ID, reset)
	if err != nil {
		t.Fatal(err)
	}
	// B 只算自己两个成员(100 + 7)与自己的 tenant 行(50), 不能吃掉 A 的成员。
	if usageB.Requests != 157 || usageB.InputTokens != 1000 || usageB.OutputTokens != 500 {
		t.Fatalf("tenant B usage=%+v, want 157/1000/500 (no cross-tenant leak)", usageB)
	}
}

// TestTenantQuotaUsageEmptyIsZero 没有任何用量行时三项均为 0, 且聚合为空不等于报错。
func TestTenantQuotaUsageEmptyIsZero(t *testing.T) {
	fx := tenantQuotaTestSetup(t)
	usage, err := loadTenantQuotaUsage(context.Background(), fx.tenantA.ID, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if usage.Requests != 0 || usage.InputTokens != 0 || usage.OutputTokens != 0 {
		t.Fatalf("empty tenant usage=%+v, want all zero", usage)
	}
}

// TestTenantQuotaUsageRespectsResetBoundary 周期外的行不计入, 否则历史用量会永久压在配额上。
func TestTenantQuotaUsageRespectsResetBoundary(t *testing.T) {
	fx := tenantQuotaTestSetup(t)
	ctx := context.Background()
	reset := time.Now().Add(-24 * time.Hour).Truncate(time.Hour)
	rows := []model.RateLimitUsageHourly{
		{ScopeType: model.RateScopeUser, ScopeID: int(fx.userA1.ID), Hour: reset.Add(-time.Hour), Requests: 99},
		{ScopeType: model.RateScopeUser, ScopeID: int(fx.userA1.ID), Hour: reset, Requests: 3},
	}
	if err := db.GetDB().Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	usage, err := loadTenantQuotaUsage(ctx, fx.tenantA.ID, reset)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Requests != 3 {
		t.Fatalf("usage before reset must be excluded, got %d", usage.Requests)
	}
}

// TestTenantQuotaUsageDrivesEnforcement 端到端: 用量超限必须真的让 CheckTenantQuota 拒绝。
// 这是"配额形同虚设"的回归断言 —— 旧口径下用量恒为 0, 这里会一路放行。
func TestTenantQuotaUsageDrivesEnforcement(t *testing.T) {
	fx := tenantQuotaTestSetup(t)
	ctx := context.Background()
	if _, err := TenantQuotaUpdate(int(fx.tenantA.ID), 5, 0, 0, "monthly", 1); err != nil {
		t.Fatal(err)
	}
	reset := tenantQuotaResetTime(time.Now(), 1, "monthly")
	rows := []model.RateLimitUsageHourly{
		// 存量小时只有 user 行。
		{ScopeType: model.RateScopeUser, ScopeID: int(fx.userA1.ID), Hour: reset, Requests: 2},
		{ScopeType: model.RateScopeUser, ScopeID: int(fx.userA2.ID), Hour: reset, Requests: 1},
		// 新小时以 tenant 行为准。
		{ScopeType: model.RateScopeTenant, ScopeID: int(fx.tenantA.ID), Hour: reset.Add(time.Hour), Requests: 4},
		// 别的租户的用量不能帮忙凑数。
		{ScopeType: model.RateScopeUser, ScopeID: int(fx.userB1.ID), Hour: reset, Requests: 1000},
	}
	if err := db.GetDB().Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	var exceeded QuotaExceeded
	if err := CheckTenantQuota(ctx, fx.tenantA.ID); !errors.As(err, &exceeded) || exceeded.Kind != "requests" {
		t.Fatalf("expected tenant request quota enforcement, got %v", err)
	}
	if exceeded.Current != 7 {
		t.Fatalf("current usage=%d, want 7 (2 + 4 + 1, foreign tenant excluded)", exceeded.Current)
	}
}

// TestTenantQuotaUsageDoesNotDoubleCount 升级后同一请求同时写 user 行与 tenant 行,
// 若两类都汇总, 一次调用会被算两遍 —— 配额提前一半触发, 实际放量只有配额的一半。
// 断言: 同一小时内两行都在时, 只按 tenant 行计一次。
func TestTenantQuotaUsageDoesNotDoubleCount(t *testing.T) {
	fx := tenantQuotaTestSetup(t)
	ctx := context.Background()
	// 必须落在当前周期内: 用月度重置时间保证 reset 早于 hour。
	quota, err := TenantQuotaGet(int(fx.tenantA.ID))
	if err != nil {
		t.Fatal(err)
	}
	reset := tenantQuotaResetTime(time.Now(), quota.ResetDay, quota.Period)
	hour := time.Now().Truncate(time.Hour)
	rows := []model.RateLimitUsageHourly{
		// 同一请求的两个维度: user 行与 tenant 行, 数值相同。
		{ScopeType: model.RateScopeUser, ScopeID: int(fx.userA1.ID), Hour: hour, Requests: 1, InputTokens: 10, OutputTokens: 5},
		{ScopeType: model.RateScopeTenant, ScopeID: int(fx.tenantA.ID), Hour: hour, Requests: 1, InputTokens: 10, OutputTokens: 5},
	}
	if err := db.GetDB().Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	usage, err := loadTenantQuotaUsage(ctx, fx.tenantA.ID, reset)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Requests != 1 || usage.InputTokens != 10 || usage.OutputTokens != 5 {
		t.Fatalf("tenant usage=%+v, want 1/10/5 (not double counted)", usage)
	}
}

// TestTenantQuotaUsageKeepsLegacyHoursWithoutTenantRows 升级前的小时没有 tenant 行,
// 那些用户行必须照旧计入 —— 否则升级会让存量用量凭空消失, 配额反而被释放。
func TestTenantQuotaUsageKeepsLegacyHoursWithoutTenantRows(t *testing.T) {
	fx := tenantQuotaTestSetup(t)
	ctx := context.Background()
	quota, err := TenantQuotaGet(int(fx.tenantA.ID))
	if err != nil {
		t.Fatal(err)
	}
	reset := tenantQuotaResetTime(time.Now(), quota.ResetDay, quota.Period)
	now := time.Now().Truncate(time.Hour)
	legacyHour := now.Add(-2 * time.Hour)
	rows := []model.RateLimitUsageHourly{
		// 旧小时: 只有 user 行, 应计入。
		{ScopeType: model.RateScopeUser, ScopeID: int(fx.userA1.ID), Hour: legacyHour, Requests: 6},
		{ScopeType: model.RateScopeUser, ScopeID: int(fx.userA2.ID), Hour: legacyHour, Requests: 4},
		// 新小时: 有 tenant 行, user 行被去重。
		{ScopeType: model.RateScopeUser, ScopeID: int(fx.userA1.ID), Hour: now, Requests: 2},
		{ScopeType: model.RateScopeTenant, ScopeID: int(fx.tenantA.ID), Hour: now, Requests: 2},
	}
	if err := db.GetDB().Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	usage, err := loadTenantQuotaUsage(ctx, fx.tenantA.ID, reset)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Requests != 12 {
		t.Fatalf("usage=%d, want 12 (6+4 legacy + 2 tenant, no double count)", usage.Requests)
	}
}

// TestRateScopeTenantIsValidScope 新范围类型必须被校验入口接受, 否则配置写入会被拒。
func TestRateScopeTenantIsValidScope(t *testing.T) {
	if !model.IsValidRateScope(model.RateScopeTenant) {
		t.Fatal("tenant scope must be a valid rate scope")
	}
	if model.RateScopeTenant == model.RateScopeUser {
		t.Fatal("tenant scope must not collide with user scope")
	}
}

// TestResolveEntityNameTenant 限流 TOP 榜要按 tenants.name 展示租户, 而不是裸 ID。
func TestResolveEntityNameTenant(t *testing.T) {
	fx := tenantQuotaTestSetup(t)
	if got := resolveEntityName(model.RateScopeTenant, int(fx.tenantA.ID)); got != fx.tenantA.Name {
		t.Fatalf("entity name=%q, want %q", got, fx.tenantA.Name)
	}
	// 不存在的租户回退成 ID 字符串, 不能慌成空串。
	if got := resolveEntityName(model.RateScopeTenant, 999999); got != "999999" {
		t.Fatalf("missing tenant name=%q, want 999999", got)
	}
}

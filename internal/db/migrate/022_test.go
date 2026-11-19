package migrate

import (
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// legacyAPIKeyRow 复刻改哈希之前的 api_keys 结构: 明文直接躺在 api_key 列里, 没有指纹列。
// 用 GORM 建表而不是手写 DDL, 就是老安装真实长出来的那张表; 测试据此走真实升级路径:
// 老表 → AutoMigrate 补出指纹列 → 迁移换算指纹 → 删明文列。
type legacyAPIKeyRow struct {
	ID                int    `gorm:"primaryKey"`
	UserID            uint   `gorm:"index;not null;default:0"`
	Name              string `gorm:"not null"`
	APIKey            string `gorm:"column:api_key;not null"`
	Enabled           bool   `gorm:"default:true"`
	ExpireAt          int64
	MaxCost           float64
	SupportedModels   []string `gorm:"serializer:json"`
	RoutingPreference string   `gorm:"not null;default:inherit"`
	QuotaRequests     int64    `gorm:"default:0"`
	QuotaInputTokens  int64    `gorm:"default:0"`
	QuotaOutputTokens int64    `gorm:"default:0"`
	QuotaResetHour    int      `gorm:"default:0"`
}

func (legacyAPIKeyRow) TableName() string { return "api_keys" }

func apiKeyMigrateTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := conn.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	return conn
}

func legacyAPIKeyTable(t *testing.T, conn *gorm.DB) {
	t.Helper()
	if err := conn.AutoMigrate(&legacyAPIKeyRow{}); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
}

// TestMigrateAPIKeyPlaintextToHash 旧 Key 无感迁移: 明文换指纹, 原 Key 原样可用, 明文列删掉。
func TestMigrateAPIKeyPlaintextToHash(t *testing.T) {
	conn := apiKeyMigrateTestDB(t)
	legacyAPIKeyTable(t, conn)
	plaintext := "sk-switchyard-" + strings.Repeat("x", 48)
	if err := conn.Create(&legacyAPIKeyRow{
		ID: 1, UserID: 5, Name: "legacy", APIKey: plaintext,
		QuotaRequests: 7, MaxCost: 3.5,
	}).Error; err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	// 真实升级路径: AutoMigrate 先补出指纹列, 迁移再回填并删明文列。
	if err := conn.AutoMigrate(&model.APIKey{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	if err := migrateAPIKeyPlaintextToHash(conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if hasPhysicalColumn(conn, "api_keys", "api_key") {
		t.Fatal("plaintext column must be deleted after migration")
	}

	type row struct {
		ID        int
		KeyHash   string `gorm:"column:key_hash"`
		KeyPrefix string `gorm:"column:key_prefix"`
		KeyLast4  string `gorm:"column:key_last4"`
		QuotaReq  int64  `gorm:"column:quota_requests"`
		MaxCost   float64
	}
	var migrated row
	if err := conn.Table("api_keys").Select("id, key_hash, key_prefix, key_last4, quota_requests, max_cost").
		Where("id = ?", 1).Find(&migrated).Error; err != nil {
		t.Fatalf("load migrated row: %v", err)
	}
	hash, prefix, last4 := model.APIKeySecretFields(plaintext)
	if migrated.KeyHash != hash {
		t.Fatalf("key_hash = %q, want %q", migrated.KeyHash, hash)
	}
	if migrated.KeyPrefix != prefix || migrated.KeyLast4 != last4 {
		t.Fatalf("mask = %q/%q, want %q/%q", migrated.KeyPrefix, migrated.KeyLast4, prefix, last4)
	}
	// 换指纹不能动权限与额度: 配额、额度上限都得原样留下。
	if migrated.QuotaReq != 7 || migrated.MaxCost != 3.5 {
		t.Fatalf("quota/max_cost lost: %+v", migrated)
	}

}

// TestMigrateAPIKeyPlaintextToHashIdempotent 迁移可能因失败重试或崩溃重启反复执行: 重跑必须无副作用。
func TestMigrateAPIKeyPlaintextToHashIdempotent(t *testing.T) {
	conn := apiKeyMigrateTestDB(t)
	legacyAPIKeyTable(t, conn)
	plaintext := "sk-switchyard-idempotent"
	if err := conn.Create(&legacyAPIKeyRow{ID: 1, Name: "legacy", APIKey: plaintext}).Error; err != nil {
		t.Fatal(err)
	}
	if err := conn.AutoMigrate(&model.APIKey{}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := migrateAPIKeyPlaintextToHash(conn); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	var hash string
	if err := conn.Table("api_keys").Select("key_hash").Where("id = ?", 1).Scan(&hash).Error; err != nil {
		t.Fatal(err)
	}
	if hash != model.APIKeyFingerprint(plaintext) {
		t.Fatalf("key_hash = %q after reruns, want %q", hash, model.APIKeyFingerprint(plaintext))
	}
}

// TestMigrateAPIKeyKeepsAlreadyHashedRows 半途失败留下的「已换算」行重跑时不得被二次换算或改写。
func TestMigrateAPIKeyKeepsAlreadyHashedRows(t *testing.T) {
	conn := apiKeyMigrateTestDB(t)
	legacyAPIKeyTable(t, conn)
	if err := conn.Create(&legacyAPIKeyRow{ID: 1, Name: "half", APIKey: "sk-half-migrated"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := conn.AutoMigrate(&model.APIKey{}); err != nil {
		t.Fatal(err)
	}
	// 模拟上一轮已写入指纹但还没来得及删列。
	if err := conn.Exec("UPDATE api_keys SET key_hash = ? WHERE id = ?", model.APIKeyFingerprint("sk-half-migrated"), 1).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateAPIKeyPlaintextToHash(conn); err != nil {
		t.Fatal(err)
	}
	var hash string
	if err := conn.Table("api_keys").Select("key_hash").Where("id = ?", 1).Scan(&hash).Error; err != nil {
		t.Fatal(err)
	}
	if hash != model.APIKeyFingerprint("sk-half-migrated") {
		t.Fatalf("existing fingerprint overwritten: %q", hash)
	}
}

// TestMigrateAPIKeySkipsFreshInstall 新库没有 api_keys 或没有明文列: 直接跳过, 不报错。
func TestMigrateAPIKeySkipsFreshInstall(t *testing.T) {
	conn := apiKeyMigrateTestDB(t)
	if err := migrateAPIKeyPlaintextToHash(conn); err != nil {
		t.Fatalf("missing table must be a no-op: %v", err)
	}
	if err := conn.AutoMigrate(&model.APIKey{}); err != nil {
		t.Fatal(err)
	}
	if err := migrateAPIKeyPlaintextToHash(conn); err != nil {
		t.Fatalf("fresh schema must be a no-op: %v", err)
	}
	if hasPhysicalColumn(conn, "api_keys", "api_key") {
		t.Fatal("fresh schema must not grow a plaintext column")
	}
}

// TestMigrateAPIKeyKeepsIDsQuotasAndStats 生产库只有两把 Key: 迁移必须原样保留主键、额度配额与统计关联。
// ID 不动是硬要求 —— stats_api_key.api_key_id、审计与限额全按 ID 挂在 Key 上。
func TestMigrateAPIKeyKeepsIDsQuotasAndStats(t *testing.T) {
	conn := apiKeyMigrateTestDB(t)
	legacyAPIKeyTable(t, conn)
	if err := conn.Create(&legacyAPIKeyRow{
		ID: 3, UserID: 5, Name: "prod-a", APIKey: "sk-prod-a",
		Enabled: true, ExpireAt: 1900000000, MaxCost: 12.5,
		SupportedModels: []string{"gpt", "claude"}, RoutingPreference: "price",
		QuotaRequests: 100, QuotaInputTokens: 1000, QuotaOutputTokens: 200, QuotaResetHour: 6,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := conn.Create(&legacyAPIKeyRow{
		ID: 7, UserID: 5, Name: "prod-b", APIKey: "sk-prod-b",
		Enabled: true, RoutingPreference: "inherit", QuotaRequests: 5,
	}).Error; err != nil {
		t.Fatal(err)
	}
	// 生产统计按 api_key_id 挂在两把 Key 上。
	if err := conn.AutoMigrate(&model.StatsAPIKey{}); err != nil {
		t.Fatal(err)
	}
	if err := conn.Create(&model.StatsAPIKey{APIKeyID: 3, StatsMetrics: model.StatsMetrics{InputToken: 10, OutputToken: 20, RequestSuccess: 30}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := conn.Create(&model.StatsAPIKey{APIKeyID: 7, StatsMetrics: model.StatsMetrics{RequestFailed: 2}}).Error; err != nil {
		t.Fatal(err)
	}

	if err := conn.AutoMigrate(&model.APIKey{}); err != nil {
		t.Fatal(err)
	}
	if err := migrateAPIKeyPlaintextToHash(conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// 主键原样: 3 与 7 都还在, 不重排也不换号。
	var ids []int
	if err := conn.Table("api_keys").Order("id").Pluck("id", &ids).Error; err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != 3 || ids[1] != 7 {
		t.Fatalf("ids = %v, want [3 7]", ids)
	}

	type row struct {
		ID                int
		Name              string
		KeyHash           string `gorm:"column:key_hash"`
		Enabled           bool
		ExpireAt          int64
		MaxCost           float64
		SupportedModels   []string `gorm:"serializer:json"`
		RoutingPreference string
		QuotaRequests     int64
		QuotaInputTokens  int64
		QuotaOutputTokens int64
		QuotaResetHour    int
	}
	var rows []row
	if err := conn.Table("api_keys").Select(
		"id, name, key_hash, enabled, expire_at, max_cost, supported_models, routing_preference, quota_requests, quota_input_tokens, quota_output_tokens, quota_reset_hour",
	).Order("id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	a, b := rows[0], rows[1]
	if a.KeyHash != model.APIKeyFingerprint("sk-prod-a") || b.KeyHash != model.APIKeyFingerprint("sk-prod-b") {
		t.Fatalf("hash mismatch: %q %q", a.KeyHash, b.KeyHash)
	}
	// 额度、配额、权限与选路偏好逐项照旧。
	if a.MaxCost != 12.5 || a.ExpireAt != 1900000000 || !a.Enabled ||
		a.QuotaRequests != 100 || a.QuotaInputTokens != 1000 || a.QuotaOutputTokens != 200 || a.QuotaResetHour != 6 ||
		a.RoutingPreference != "price" || len(a.SupportedModels) != 2 {
		t.Fatalf("prod-a lost fields: %+v", a)
	}
	if b.MaxCost != 0 || b.QuotaRequests != 5 || b.RoutingPreference != "inherit" || len(b.SupportedModels) != 0 {
		t.Fatalf("prod-b lost fields: %+v", b)
	}

	// 统计关联不变: stats_api_key.api_key_id 仍指向同名 Key, 数值不动。
	var stats []model.StatsAPIKey
	if err := conn.Order("api_key_id").Find(&stats).Error; err != nil {
		t.Fatal(err)
	}
	if len(stats) != 2 || stats[0].APIKeyID != 3 || stats[1].APIKeyID != 7 {
		t.Fatalf("stats linkage changed: %+v", stats)
	}
	if stats[0].InputToken != 10 || stats[0].RequestSuccess != 30 || stats[1].RequestFailed != 2 {
		t.Fatalf("stats values changed: %+v", stats)
	}
	var names []string
	if err := conn.Table("api_keys").Select("name").
		Where("id IN ?", []int{stats[0].APIKeyID, stats[1].APIKeyID}).
		Order("id").Pluck("name", &names).Error; err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "prod-a" || names[1] != "prod-b" {
		t.Fatalf("stats no longer resolve to the same keys: %v", names)
	}
}

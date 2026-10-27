package db

import (
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/secretstore"
)

// 讯飞凭据第二秘密列(api_secret)的落库加密回归:
// 覆盖字段序列化器与 map 写入兜底两条写路径、两列密文互换(换列)拒绝、
// 改归属时两列都必须重给、空秘密更新语义, 以及老库缺列时的迁移兼容。

// rawChannelSecretColumns 按库内原值读回 channel_keys 的两个秘密列(信封/明文照实返回)。
func rawChannelSecretColumns(t *testing.T, id int) (key, apiSecret string) {
	t.Helper()
	var row struct {
		Key       string
		APISecret string
	}
	if err := db.Raw("SELECT key, api_secret FROM channel_keys WHERE id = ?", id).Scan(&row).Error; err != nil {
		t.Fatalf("raw read channel key %d: %v", id, err)
	}
	return row.Key, row.APISecret
}

func TestChannelKeyAPISecretSealedAtRest(t *testing.T) {
	setupSecretDB(t, testKEK(1))
	createTestChannel(t, 7)

	// 字段序列化器写路径: key 与 api_secret 两列都必须是信封, 明文不落库。
	row := model.ChannelKey{ChannelID: 7, ChannelKeyConfig: model.ChannelKeyConfig{
		Name: "xunfei", Key: "xf-api-key", AppID: "xf-app-id", APISecret: "xf-api-secret", Domain: "generalv3", Enabled: true,
	}}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create channel key: %v", err)
	}

	storedKey, storedSecret := rawChannelSecretColumns(t, row.ID)
	for column, stored := range map[string]string{"key": storedKey, "api_secret": storedSecret} {
		if !secretstore.IsEnvelope(stored) {
			t.Fatalf("channel key %s stored in plaintext: %q", column, stored)
		}
	}
	if strings.Contains(storedKey, "xf-api-key") || strings.Contains(storedSecret, "xf-api-secret") {
		t.Fatal("secret leaked into envelope text")
	}

	// 运行时读明文: 两列都解开, 非秘密的标识列(app_id / domain)原样读回。
	var got model.ChannelKey
	if err := db.Where("id = ?", row.ID).First(&got).Error; err != nil {
		t.Fatalf("read channel key: %v", err)
	}
	if got.Key != "xf-api-key" || got.APISecret != "xf-api-secret" || got.AppID != "xf-app-id" || got.Domain != "generalv3" {
		t.Fatalf("read back = %+v", got)
	}

	// map 写入兜底路径: 更新同样两列都封存。
	identity := model.ChannelKey{ID: row.ID, ChannelID: 7, ChannelKeyConfig: model.ChannelKeyConfig{Name: "xunfei"}}
	if err := db.Model(&identity).Where("id = ?", row.ID).
		Updates(map[string]any{"key": "xf-api-key-2", "api_secret": "xf-api-secret-2", "enabled": true}).Error; err != nil {
		t.Fatalf("map update channel key: %v", err)
	}
	storedKey, storedSecret = rawChannelSecretColumns(t, row.ID)
	if !secretstore.IsEnvelope(storedKey) || !secretstore.IsEnvelope(storedSecret) {
		t.Fatalf("map update wrote plaintext: key=%q api_secret=%q", storedKey, storedSecret)
	}
	if err := db.Where("id = ?", row.ID).First(&got).Error; err != nil {
		t.Fatalf("read after map update: %v", err)
	}
	if got.Key != "xf-api-key-2" || got.APISecret != "xf-api-secret-2" {
		t.Fatalf("read back after map update = %+v", got)
	}
}

func TestChannelKeySecretColumnsRejectCiphertextSwap(t *testing.T) {
	setupSecretDB(t, testKEK(1))
	createTestChannel(t, 7)

	row := model.ChannelKey{ChannelID: 7, ChannelKeyConfig: model.ChannelKeyConfig{
		Name: "xunfei", Key: "xf-api-key", APISecret: "xf-api-secret", Enabled: true,
	}}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create channel key: %v", err)
	}

	// 换列: 同一行的 key 密文搬进 api_secret 列, 归属绑定带列名, 必须解不开。
	if err := db.Exec("UPDATE channel_keys SET api_secret = key WHERE id = ?", row.ID).Error; err != nil {
		t.Fatalf("swap ciphertext across columns: %v", err)
	}
	var got model.ChannelKey
	if err := db.Where("id = ?", row.ID).First(&got).Error; err == nil {
		t.Fatalf("cross-column ciphertext decrypted (key=%q api_secret=%q), want binding mismatch", got.Key, got.APISecret)
	}

	// 换行: 另一行的 api_secret 密文搬进本行, 行归属不符, 同样必须解不开。
	if err := db.Exec("UPDATE channel_keys SET key = 'xf-api-key-2', api_secret = 'xf-api-secret-2' WHERE id = ?", row.ID).Error; err != nil {
		t.Fatalf("reset row: %v", err)
	}
	row2 := model.ChannelKey{ChannelID: 7, ChannelKeyConfig: model.ChannelKeyConfig{
		Name: "other", Key: "other-key", APISecret: "other-secret", Enabled: true,
	}}
	if err := db.Create(&row2).Error; err != nil {
		t.Fatalf("create second channel key: %v", err)
	}
	if err := db.Exec("UPDATE channel_keys SET api_secret = (SELECT api_secret FROM channel_keys WHERE id = ?) WHERE id = ?", row.ID, row2.ID).Error; err != nil {
		t.Fatalf("relocate api_secret ciphertext: %v", err)
	}
	if err := db.Where("id = ?", row2.ID).First(&got).Error; err == nil {
		t.Fatalf("relocated api_secret ciphertext decrypted to %q, want ownership mismatch", got.APISecret)
	}
}

func TestMapUpdateMoveRequiresEverySecret(t *testing.T) {
	setupSecretDB(t, testKEK(1))
	createTestChannel(t, 7)

	row := model.ChannelKey{ChannelID: 7, ChannelKeyConfig: model.ChannelKeyConfig{
		Name: "xunfei", Key: "xf-api-key", APISecret: "xf-api-secret", Enabled: true,
	}}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create channel key: %v", err)
	}

	identity := model.ChannelKey{ID: row.ID, ChannelID: 7, ChannelKeyConfig: model.ChannelKeyConfig{Name: "xunfei"}}
	// 改名只重给 key 不重给 api_secret: api_secret 的存量密文会留在旧名字的绑定上, 写入必须被拒绝。
	err := db.Model(&identity).Where("id = ?", row.ID).
		Updates(map[string]any{"name": "renamed", "key": "xf-api-key-2"}).Error
	if err == nil {
		t.Fatal("rename with only one secret re-supplied succeeded, want fail-closed error")
	}

	// 两个秘密列都重给才允许改名: 新名字下两列都按新归属重封、读得开。
	if err := db.Model(&identity).Where("id = ?", row.ID).
		Updates(map[string]any{"name": "renamed", "key": "xf-api-key-2", "api_secret": "xf-api-secret-2"}).Error; err != nil {
		t.Fatalf("rename with every secret re-supplied: %v", err)
	}
	var got model.ChannelKey
	if err := db.Where("id = ?", row.ID).First(&got).Error; err != nil {
		t.Fatalf("read renamed channel key: %v", err)
	}
	if got.Name != "renamed" || got.Key != "xf-api-key-2" || got.APISecret != "xf-api-secret-2" {
		t.Fatalf("read back = %+v", got)
	}
}

func TestMapCreateWithPartialSecretsAllowed(t *testing.T) {
	setupSecretDB(t, testKEK(1))
	createTestChannel(t, 7)

	// 裸表 map 插入只带 key 不带 api_secret: 插入没有存量密文, 不受"改归属必须重给每一列秘密"约束。
	if err := db.Table(secretChannelKeyTable).Create(map[string]any{
		"channel_id": 7, "name": "map-made", "key": "map-key", "enabled": true,
	}).Error; err != nil {
		t.Fatalf("raw map create: %v", err)
	}
	var id int
	if err := db.Raw("SELECT id FROM channel_keys WHERE channel_id = ? AND name = 'map-made'", 7).Scan(&id).Error; err != nil || id == 0 {
		t.Fatalf("look up created row: id=%d err=%v", id, err)
	}
	storedKey, storedSecret := rawChannelSecretColumns(t, id)
	if !secretstore.IsEnvelope(storedKey) || storedSecret != "" {
		t.Fatalf("raw map create stored: key=%q api_secret=%q", storedKey, storedSecret)
	}
	var got model.ChannelKey
	if err := db.Where("id = ?", id).First(&got).Error; err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Key != "map-key" {
		t.Fatalf("read back = %+v", got)
	}
}

func TestMapUpdateEmptySecretClearsByContract(t *testing.T) {
	setupSecretDB(t, testKEK(1))
	createTestChannel(t, 7)

	row := model.ChannelKey{ChannelID: 7, ChannelKeyConfig: model.ChannelKeyConfig{
		Name: "xunfei", Key: "xf-api-key", APISecret: "xf-api-secret", Enabled: true,
	}}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create channel key: %v", err)
	}

	// 空秘密 = 清空(接口整体替换语义; 表单层的"留空不改"在前端合并旧值):
	// 库内保持空串而不是无意义的空信封, 读回空串不报错。
	identity := model.ChannelKey{ID: row.ID, ChannelID: 7, ChannelKeyConfig: model.ChannelKeyConfig{Name: "xunfei"}}
	if err := db.Model(&identity).Where("id = ?", row.ID).
		Updates(map[string]any{"api_secret": ""}).Error; err != nil {
		t.Fatalf("clear api_secret: %v", err)
	}
	_, storedSecret := rawChannelSecretColumns(t, row.ID)
	if storedSecret != "" {
		t.Fatalf("empty secret stored as %q, want empty string", storedSecret)
	}
	var got model.ChannelKey
	if err := db.Where("id = ?", row.ID).First(&got).Error; err != nil {
		t.Fatalf("read after clear: %v", err)
	}
	if got.APISecret != "" || got.Key != "xf-api-key" {
		t.Fatalf("read back = %+v, want api_secret cleared and key intact", got)
	}
}

func TestMigrateSealsAPISecretAndToleratesMissingColumn(t *testing.T) {
	setupSecretDB(t, testKEK(1))
	createTestChannel(t, 7)
	if err := db.Exec("INSERT INTO channel_keys (channel_id, name, key, api_secret, enabled) VALUES (7, 'legacy', 'legacy-key', 'legacy-secret', 1)").Error; err != nil {
		t.Fatalf("seed plaintext: %v", err)
	}
	var legacyID int
	if err := db.Raw("SELECT id FROM channel_keys WHERE channel_id = ? AND name = 'legacy'", 7).Scan(&legacyID).Error; err != nil || legacyID == 0 {
		t.Fatalf("look up seeded row: id=%d err=%v", legacyID, err)
	}

	// 两列遗留明文都包进信封; 空的 api_secret 不计入统计(讯飞专用列, 大多数行没有)。
	stats, err := MigrateSecretRows(t.Context(), false)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if stats.ChannelKeysEncrypted != 2 || stats.EmptySkipped != 0 {
		t.Fatalf("migrate stats = %+v, want 2 secret columns encrypted", stats)
	}
	storedKey, storedSecret := rawChannelSecretColumns(t, legacyID)
	if !secretstore.IsEnvelope(storedKey) || !secretstore.IsEnvelope(storedSecret) {
		t.Fatalf("migration left plaintext: key=%q api_secret=%q", storedKey, storedSecret)
	}

	// 幂等重跑: 零改写, 两列信封都验开。
	again, err := MigrateSecretRows(t.Context(), false)
	if err != nil {
		t.Fatalf("re-run migrate: %v", err)
	}
	if again.Total() != 0 || again.AlreadyEncrypted != 2 {
		t.Fatalf("re-run stats = %+v, want zero writes and 2 verified envelopes", again)
	}

	// 老库还没有 api_secret 列(只读巡检/尚未跑过补列迁移): 迁移不得因缺列报错, key 照常迁移。
	if err := db.Exec("ALTER TABLE channel_keys DROP COLUMN api_secret").Error; err != nil {
		t.Fatalf("drop api_secret column: %v", err)
	}
	if err := db.Exec("INSERT INTO channel_keys (channel_id, name, key, enabled) VALUES (7, 'legacy2', 'legacy-key-2', 1)").Error; err != nil {
		t.Fatalf("seed second plaintext row: %v", err)
	}
	third, err := MigrateSecretRows(t.Context(), false)
	if err != nil {
		t.Fatalf("migrate without api_secret column: %v", err)
	}
	if third.ChannelKeysEncrypted != 1 {
		t.Fatalf("stats = %+v, want the remaining key column encrypted", third)
	}
}

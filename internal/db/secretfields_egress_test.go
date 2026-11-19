package db

import (
	"context"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/secretstore"
)

// 出口代理地址(channel_egresses.url)的落库加密。
//
// 这一列此前是全库唯一明文落库的凭据: http://user:pass@host:port 原文存在库里,
// 而自动备份是 VACUUM INTO 物理快照 —— 每份备份都带着代理账号密码。
// 回显与错误信息早在 v1.5.14 就掩码了, 但"库里有明文"这一面一直没堵上。

// createTestEgress 建一条出口行。channel_id + name 是它唯一的归属标识, 也是密文绑定的依据。
func createTestEgress(t *testing.T, channelID int, name, url string) model.ChannelEgress {
	t.Helper()
	egress := model.ChannelEgress{
		ChannelID: channelID,
		Name:      name,
		Kind:      model.EgressKindHTTP,
		URL:       url,
		Enabled:   true,
	}
	if err := db.Create(&egress).Error; err != nil {
		t.Fatalf("create egress %s: %v", name, err)
	}
	return egress
}

// rawEgressURL 绕过 gorm 序列化器直读存储, 看到的就是库文件里真实躺着的东西。
func rawEgressURL(t *testing.T, id int) string {
	t.Helper()
	var value string
	if err := db.Raw("SELECT url FROM channel_egresses WHERE id = ?", id).Scan(&value).Error; err != nil {
		t.Fatalf("raw read egress %d: %v", id, err)
	}
	return value
}

// TestEgressURLIsSealedAtRest 落库即密文, 读出来仍是明文。
func TestEgressURLIsSealedAtRest(t *testing.T) {
	setupSecretDB(t, testKEK(7))
	createTestChannel(t, 1)

	const proxy = "http://user:s3cret@38.47.53.97:3128"
	egress := createTestEgress(t, 1, "hk-01", proxy)

	stored := rawEgressURL(t, egress.ID)
	if stored == proxy {
		t.Fatal("出口代理地址仍是明文落库: 库文件与每份 VACUUM INTO 备份都会带着 user:pass")
	}
	if !secretstore.IsEnvelope(stored) {
		t.Fatalf("落库值不是信封: %q", stored)
	}
	if strings.Contains(stored, "s3cret") {
		t.Fatalf("密文里仍能看到凭据: %q", stored)
	}

	// gorm 读路径必须自动解开, 转发与掩码回显都依赖它。
	var loaded model.ChannelEgress
	if err := db.First(&loaded, egress.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if loaded.URL != proxy {
		t.Fatalf("读回的明文不符: %q", loaded.URL)
	}
}

// TestEgressDirectURLStaysEmpty 直连出口 URL 恒为空, 不该产生无意义的信封。
func TestEgressDirectURLStaysEmpty(t *testing.T) {
	setupSecretDB(t, testKEK(8))
	createTestChannel(t, 1)

	egress := model.ChannelEgress{ChannelID: 1, Name: "direct", Kind: model.EgressKindDirect, Enabled: true}
	if err := db.Create(&egress).Error; err != nil {
		t.Fatalf("create direct egress: %v", err)
	}
	if stored := rawEgressURL(t, egress.ID); stored != "" {
		t.Fatalf("空地址被写成了 %q", stored)
	}
}

// TestEgressURLMapWriteSealed 与 TestEgressURLMapWriteRequiresOwnership 覆盖 map 写入这条旁路。
//
// gorm 的 Updates(map) 不走字段序列化器: 没有 db 侧的兜底回调, op.syncChannelEgresses
// 保存出口时就会把明文直接写回库里 —— 模型加了 tag 也拦不住。
func TestEgressURLMapWriteSealed(t *testing.T) {
	setupSecretDB(t, testKEK(9))
	createTestChannel(t, 1)
	egress := createTestEgress(t, 1, "hk-01", "http://a:1@old.example:3128")

	const updated = "http://a:2@new.example:3128"
	identity := model.ChannelEgress{ID: egress.ID, ChannelID: 1, Name: "hk-01"}
	if err := db.Model(&identity).Where("id = ?", egress.ID).Updates(map[string]any{"url": updated}).Error; err != nil {
		t.Fatalf("map update: %v", err)
	}

	stored := rawEgressURL(t, egress.ID)
	if !secretstore.IsEnvelope(stored) || strings.Contains(stored, "new.example") {
		t.Fatalf("map 写入绕过了加密: %q", stored)
	}
	var loaded model.ChannelEgress
	if err := db.First(&loaded, egress.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if loaded.URL != updated {
		t.Fatalf("读回明文不符: %q", loaded.URL)
	}
}

func TestEgressURLMapWriteRequiresOwnership(t *testing.T) {
	setupSecretDB(t, testKEK(10))
	createTestChannel(t, 1)
	egress := createTestEgress(t, 1, "hk-01", "http://a:1@old.example:3128")

	// 零值 Model + 不含 channel_id/name 的 map: 推不出归属, 必须拒绝而不是写弱绑定密文。
	err := db.Model(&model.ChannelEgress{}).Where("id = ?", egress.ID).Updates(map[string]any{"url": "http://a:2@x.example:3128"}).Error
	if err == nil {
		t.Fatal("缺少归属的 map 写入被放行: 会产生无法校验归属的密文")
	}
	if !strings.Contains(err.Error(), "ownership") {
		t.Fatalf("拒绝原因不明确: %v", err)
	}
	// 原来的密文不能被破坏。
	var loaded model.ChannelEgress
	if err := db.First(&loaded, egress.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if loaded.URL != "http://a:1@old.example:3128" {
		t.Fatalf("失败的写入改动了数据: %q", loaded.URL)
	}
}

// TestEgressURLMigrationSealsPlaintext 存量明文由启动迁移就地封存, 且幂等。
func TestEgressURLMigrationSealsPlaintext(t *testing.T) {
	setupSecretDB(t, testKEK(11))
	createTestChannel(t, 1)
	egress := createTestEgress(t, 1, "hk-01", "")
	_ = egress

	// 直接写裸 SQL, 模拟"加密上线前就已存在的明文行"。
	const proxy = "http://user:pw@legacy.example:3128"
	if err := db.Exec("UPDATE channel_egresses SET url = ? WHERE channel_id = ? AND name = ?", proxy, 1, "hk-01").Error; err != nil {
		t.Fatalf("seed plaintext: %v", err)
	}
	var id int
	if err := db.Raw("SELECT id FROM channel_egresses WHERE channel_id = ? AND name = ?", 1, "hk-01").Scan(&id).Error; err != nil {
		t.Fatalf("locate row: %v", err)
	}

	stats, err := MigrateSecretRows(context.Background(), false)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if stats.EgressesEncrypted != 1 {
		t.Fatalf("迁移没有统计到出口行: %+v", stats)
	}
	stored := rawEgressURL(t, id)
	if !secretstore.IsEnvelope(stored) || strings.Contains(stored, "legacy.example") {
		t.Fatalf("迁移后仍是明文: %q", stored)
	}
	var loaded model.ChannelEgress
	if err := db.First(&loaded, id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if loaded.URL != proxy {
		t.Fatalf("迁移后读不回原文: %q", loaded.URL)
	}

	// 幂等: 第二次零写入, 且密文逐字节不变。
	stats2, err := MigrateSecretRows(context.Background(), false)
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if stats2.EgressesEncrypted != 0 || stats2.Total() != 0 {
		t.Fatalf("重复迁移改写了已加密行: %+v", stats2)
	}
	if again := rawEgressURL(t, id); again != stored {
		t.Fatalf("重复迁移改变了密文: %q -> %q", stored, again)
	}
}

// TestEgressURLCiphertextCannotMoveRows 把密文搬到另一个出口行必须解不开 —— 搬移等于换线路。
func TestEgressURLCiphertextCannotMoveRows(t *testing.T) {
	setupSecretDB(t, testKEK(12))
	createTestChannel(t, 1)
	a := createTestEgress(t, 1, "hk-01", "http://a:1@a.example:3128")
	b := createTestEgress(t, 1, "jp-01", "http://b:2@b.example:3128")

	cipherOfA := rawEgressURL(t, a.ID)
	if err := db.Exec("UPDATE channel_egresses SET url = ? WHERE id = ?", cipherOfA, b.ID).Error; err != nil {
		t.Fatalf("move ciphertext: %v", err)
	}

	var loaded model.ChannelEgress
	err := db.First(&loaded, b.ID).Error
	if err == nil {
		t.Fatalf("搬移的密文被解开了(拿到 %q): 出口可以借此换线路", loaded.URL)
	}
}

// TestEgressURLWrongMasterKeyFails 换错主密钥必须 fail closed, 不能把密文当明文转发出去了。
func TestEgressURLWrongMasterKeyFails(t *testing.T) {
	setupSecretDB(t, testKEK(13))
	createTestChannel(t, 1)
	egress := createTestEgress(t, 1, "hk-01", "http://a:1@a.example:3128")

	// 换一把密钥重新打开同一个库文件(testKEK(13) 只用于 seal, 这里用截然不同的一份)。
	secretstore.Reset()
	if err := secretstore.Init(secretstore.KEKConfig{Inline: hex.EncodeToString(testKEK(99))}); err != nil {
		t.Fatalf("reinit: %v", err)
	}

	var loaded model.ChannelEgress
	if err := db.First(&loaded, egress.ID).Error; err == nil {
		t.Fatalf("错误主密钥下读到了 %q", loaded.URL)
	}
}

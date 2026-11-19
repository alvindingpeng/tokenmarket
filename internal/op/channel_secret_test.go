package op

import (
	"strings"

	"gorm.io/gorm"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/secretstore"
)

// TestSyncChannelKeysSealsUnderEnvelope 走真实的渠道凭据批量替换/更新路径:
// 新增走 Create 序列化器, 更新走 map 回调(带归属身份), 两条路径都要密文落库、明文读回。
func TestSyncChannelKeysSealsUnderEnvelope(t *testing.T) {
	backupSecretTestDB(t, backupTestKEK(1))

	channel := model.Channel{ID: 5, ChannelConfig: model.ChannelConfig{Name: "keys-channel"}}
	if err := db.GetDB().Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}

	sync := func(requested []model.ChannelKeyConfig) {
		t.Helper()
		err := db.GetDB().Transaction(func(tx *gorm.DB) error {
			return syncChannelKeys(tx, 5, requested)
		})
		if err != nil {
			t.Fatalf("sync channel keys: %v", err)
		}
	}

	sync([]model.ChannelKeyConfig{{Name: "default", Key: "sk-1", Enabled: true}})

	var row model.ChannelKey
	if err := db.GetDB().Where("channel_id = ?", 5).First(&row).Error; err != nil {
		t.Fatalf("read channel key: %v", err)
	}
	stored := rawChannelKey(t, row.ID)
	if !secretstore.IsEnvelope(stored) || strings.Contains(stored, "sk-1") {
		t.Fatalf("created channel key not sealed: %q", stored)
	}
	if row.Key != "sk-1" {
		t.Fatalf("read back = %q, want %q", row.Key, "sk-1")
	}

	// 批量更新: 同名换 key 走 map 更新路径。
	sync([]model.ChannelKeyConfig{{Name: "default", Key: "sk-2", Enabled: true}})
	stored = rawChannelKey(t, row.ID)
	if !secretstore.IsEnvelope(stored) || strings.Contains(stored, "sk-2") {
		t.Fatalf("updated channel key not sealed: %q", stored)
	}
	if err := db.GetDB().Where("id = ?", row.ID).First(&row).Error; err != nil {
		t.Fatalf("re-read channel key: %v", err)
	}
	if row.Key != "sk-2" {
		t.Fatalf("read back = %q, want %q", row.Key, "sk-2")
	}

	// 不变的提交不重写: 对比发生在明文层, 库里的信封保持原样。
	before := rawChannelKey(t, row.ID)
	sync([]model.ChannelKeyConfig{{Name: "default", Key: "sk-2", Enabled: true}})
	if rawChannelKey(t, row.ID) != before {
		t.Fatal("no-op sync rewrote the stored envelope")
	}
}

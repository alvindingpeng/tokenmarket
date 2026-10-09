package op

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// autoaddTestDB 建一个内存库并迁移自动添加涉及的几张表。
func autoaddTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := conn.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1) // 内存库单连接, 避免各协程拿到独立实例。
	if err := conn.AutoMigrate(&model.Channel{}, &model.ChannelKey{}, &model.ChannelModel{}, &model.ChannelGrant{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return conn
}

// autoaddSeed 建一个渠道 + 一个凭据 + 已有模型 m1/m0(带授权), 返回各主键。
func autoaddSeed(t *testing.T, conn *gorm.DB) (channelID, keyID, m0ID, m1ID int) {
	t.Helper()
	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "auto", BaseURL: "http://up"}}
	if err := conn.Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	channelID = channel.ID
	key := model.ChannelKey{ChannelID: channelID, ChannelKeyConfig: model.ChannelKeyConfig{Name: "k1", Key: "sk", Enabled: true}}
	if err := conn.Create(&key).Error; err != nil {
		t.Fatalf("create key: %v", err)
	}
	keyID = key.ID
	// m0: 上游未返回的既有模型, 必须原样保留。
	m0 := model.ChannelModel{ChannelID: channelID, Name: "m0"}
	if err := conn.Create(&m0).Error; err != nil {
		t.Fatalf("create m0: %v", err)
	}
	m0ID = m0.ID
	// m1: 已有授权带 Anthropic 位(1<<3), 探测并入时只 OR 不清除。
	m1 := model.ChannelModel{ChannelID: channelID, Name: "m1"}
	if err := conn.Create(&m1).Error; err != nil {
		t.Fatalf("create m1: %v", err)
	}
	m1ID = m1.ID
	grant := model.ChannelGrant{ChannelModelID: m1ID, ChannelKeyID: keyID, Protocols: model.ProtocolAnthropicMessage}
	if err := conn.Create(&grant).Error; err != nil {
		t.Fatalf("create grant: %v", err)
	}
	return channelID, keyID, m0ID, m1ID
}

func TestMergeFetchedForKey(t *testing.T) {
	t.Run("or-merge and add and keep", func(t *testing.T) {
		conn := autoaddTestDB(t)
		channelID, keyID, m0ID, m1ID := autoaddSeed(t, conn)
		fetched := []model.ChannelFetchModel{
			{Name: "m1", Protocols: model.ProtocolOpenAIResponse},            // 已有: OR 并入 Response 位, Anthropic 位保留。
			{Name: "  m2  ", Protocols: model.ProtocolOpenAIResponse},        // 新增: 名称两侧空白被 trim。
			{Name: "dall-e-3", Protocols: model.ProtocolOpenAIResponse | model.ProtocolOpenAIImage}, // 新增 + 生图位。
		}
		newCount := 0
		added, truncated, err := mergeFetchedForKey(conn, channelID, keyID, fetched, autoAddModelLimit, &newCount)
		if err != nil {
			t.Fatalf("merge: %v", err)
		}
		if truncated {
			t.Fatalf("unexpected truncation")
		}
		// m1 授权 = Anthropic | Response = 0b1100。
		var g model.ChannelGrant
		if err := conn.Where("channel_model_id = ? AND channel_key_id = ?", m1ID, keyID).First(&g).Error; err != nil {
			t.Fatalf("load m1 grant: %v", err)
		}
		if g.Protocols != model.ProtocolAnthropicMessage|model.ProtocolOpenAIResponse {
			t.Fatalf("m1 grant = %b, want %b", g.Protocols, model.ProtocolAnthropicMessage|model.ProtocolOpenAIResponse)
		}
		// m0 仍在, m2/dall-e-3 已建。
		var count int64
		conn.Model(&model.ChannelModel{}).Where("channel_id = ?", channelID).Count(&count)
		if count != 4 {
			t.Fatalf("models = %d, want 4 (m0+m1+m2+dall-e-3)", count)
		}
		var m0 model.ChannelModel
		if err := conn.First(&m0, m0ID).Error; err != nil {
			t.Fatalf("m0 removed unexpectedly: %v", err)
		}
		// 新增名单与 trim 语义。
		wantAdded := []string{"m2", "dall-e-3"}
		if len(added) != len(wantAdded) || added[0] != wantAdded[0] || added[1] != wantAdded[1] {
			t.Fatalf("added = %v, want %v", added, wantAdded)
		}
		if newCount != 2 {
			t.Fatalf("newCount = %d, want 2", newCount)
		}
		// kind 重算: dall-e-3 生图位 → image; m1 无生图位 → 文本。
		if err := syncChannelModelKinds(conn, channelID); err != nil {
			t.Fatalf("kind sync: %v", err)
		}
		var dall model.ChannelModel
		if err := conn.Where("channel_id = ? AND name = ?", channelID, "dall-e-3").First(&dall).Error; err != nil {
			t.Fatalf("load dall-e-3: %v", err)
		}
		if dall.Kind != model.MediaKindImage {
			t.Fatalf("dall-e-3 kind = %q, want image", dall.Kind)
		}
		var m1model model.ChannelModel
		if err := conn.First(&m1model, m1ID).Error; err != nil {
			t.Fatalf("load m1: %v", err)
		}
		if m1model.Kind != model.MediaKindText {
			t.Fatalf("m1 kind = %q, want text", m1model.Kind)
		}
	})

	t.Run("cap at limit", func(t *testing.T) {
		conn := autoaddTestDB(t)
		channelID, keyID, _, _ := autoaddSeed(t, conn)
		fetched := make([]model.ChannelFetchModel, 0, 205)
		for i := 0; i < 205; i++ {
			fetched = append(fetched, model.ChannelFetchModel{Name: "m" + string(rune(48+i%10)) + string(rune(97+i/10)) + string(rune(97+i%26)), Protocols: model.ProtocolOpenAIResponse})
		}
		// 名称必须唯一且含 205 个: 用 m-<i> 生成。
		fetched = fetched[:0]
		for i := 0; i < 205; i++ {
			fetched = append(fetched, model.ChannelFetchModel{Name: "cap-" + fmtInt(i), Protocols: model.ProtocolOpenAIResponse})
		}
		newCount := 0
		added, truncated, err := mergeFetchedForKey(conn, channelID, keyID, fetched, autoAddModelLimit, &newCount)
		if err != nil {
			t.Fatalf("merge: %v", err)
		}
		if !truncated {
			t.Fatalf("expected truncation")
		}
		if newCount != autoAddModelLimit {
			t.Fatalf("newCount = %d, want %d", newCount, autoAddModelLimit)
		}
		if len(added) != autoAddModelLimit {
			t.Fatalf("added = %d, want %d", len(added), autoAddModelLimit)
		}
		var count int64
		conn.Model(&model.ChannelModel{}).Where("channel_id = ?", channelID).Count(&count)
		if count != int64(autoAddModelLimit+2) { // 既有 m0, m1 + 新增 200。
			t.Fatalf("models = %d, want %d", count, autoAddModelLimit+2)
		}
	})
}

func fmtInt(i int) string {
	if i == 0 {
		return "0"
	}
	digits := make([]byte, 0, 4)
	for i > 0 {
		digits = append([]byte{byte(48 + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}

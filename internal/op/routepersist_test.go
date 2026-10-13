package op

import (
	"path/filepath"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// routePersistTestDB 建 SQLite 测试库并预置两个渠道模型各自的成员与指标行。
func routePersistTestDB(t *testing.T) (groupID, goodModelID, badModelID, goodItemID, badItemID int) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "persist.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	database := db.GetDB()
	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "c", BaseURL: "http://up"}}
	if err := database.Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	key := model.ChannelKey{ChannelID: channel.ID, ChannelKeyConfig: model.ChannelKeyConfig{Name: "k", Key: "sk", Enabled: true}}
	if err := database.Create(&key).Error; err != nil {
		t.Fatalf("create key: %v", err)
	}
	good := model.ChannelModel{ChannelID: channel.ID, Name: "good"}
	bad := model.ChannelModel{ChannelID: channel.ID, Name: "bad"}
	if err := database.Create(&good).Error; err != nil {
		t.Fatalf("create good model: %v", err)
	}
	if err := database.Create(&bad).Error; err != nil {
		t.Fatalf("create bad model: %v", err)
	}
	goodGrant := model.ChannelGrant{ChannelModelID: good.ID, ChannelKeyID: key.ID}
	badGrant := model.ChannelGrant{ChannelModelID: bad.ID, ChannelKeyID: key.ID}
	if err := database.Create(&goodGrant).Error; err != nil {
		t.Fatalf("create good grant: %v", err)
	}
	if err := database.Create(&badGrant).Error; err != nil {
		t.Fatalf("create bad grant: %v", err)
	}
	grp := model.Group{Name: "g"}
	if err := database.Create(&grp).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	goodItem := model.GroupItem{GroupID: grp.ID, ChannelGrantID: goodGrant.ID}
	badItem := model.GroupItem{GroupID: grp.ID, ChannelGrantID: badGrant.ID}
	if err := database.Create(&goodItem).Error; err != nil {
		t.Fatalf("create good item: %v", err)
	}
	if err := database.Create(&badItem).Error; err != nil {
		t.Fatalf("create bad item: %v", err)
	}
	for _, record := range []model.RouteMetricRecord{
		{GroupID: grp.ID, ItemID: goodItem.ID, EmaWaitMs: 2708, EmaSuccess: 1, Samples: 163},
		{GroupID: grp.ID, ItemID: badItem.ID, EmaWaitMs: 52, EmaSuccess: 0, Samples: 1},
	} {
		if err := database.Create(&record).Error; err != nil {
			t.Fatalf("create metric record: %v", err)
		}
	}
	return grp.ID, good.ID, bad.ID, goodItem.ID, badItem.ID
}

// TestRouteMetricItemChannelModels 校验成员到渠道模型的映射解析正确。
func TestRouteMetricItemChannelModels(t *testing.T) {
	_, goodModelID, badModelID, goodItemID, badItemID := routePersistTestDB(t)
	mapping, err := RouteMetricItemChannelModels()
	if err != nil {
		t.Fatalf("mapping: %v", err)
	}
	if mapping[goodItemID] != goodModelID || mapping[badItemID] != badModelID {
		t.Fatalf("mapping = %v, want item %d -> %d and %d -> %d", mapping, goodItemID, goodModelID, badItemID, badModelID)
	}
}

// TestRouteMetricDeleteByChannelModel 校验按渠道模型清理只删其名下观测, 不伤及他人。
func TestRouteMetricDeleteByChannelModel(t *testing.T) {
	_, _, badModelID, goodItemID, badItemID := routePersistTestDB(t)
	deleted, err := RouteMetricDeleteByChannelModel(badModelID)
	if err != nil {
		t.Fatalf("delete by channel model: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	var count int64
	db.GetDB().Model(&model.RouteMetricRecord{}).Where("item_id = ?", badItemID).Count(&count)
	if count != 0 {
		t.Fatalf("bad item metric should be gone: %d", count)
	}
	db.GetDB().Model(&model.RouteMetricRecord{}).Where("item_id = ?", goodItemID).Count(&count)
	if count != 1 {
		t.Fatalf("good item metric must survive: %d", count)
	}
}

// TestRouteMetricDelete 校验单成员清理只删目标行。
func TestRouteMetricDelete(t *testing.T) {
	groupID, _, _, goodItemID, badItemID := routePersistTestDB(t)
	if err := RouteMetricDelete(groupID, badItemID); err != nil {
		t.Fatalf("delete member metric: %v", err)
	}
	var count int64
	db.GetDB().Model(&model.RouteMetricRecord{}).Where("item_id = ?", badItemID).Count(&count)
	if count != 0 {
		t.Fatalf("target row should be gone: %d", count)
	}
	db.GetDB().Model(&model.RouteMetricRecord{}).Where("item_id = ?", goodItemID).Count(&count)
	if count != 1 {
		t.Fatalf("other rows must survive: %d", count)
	}
}

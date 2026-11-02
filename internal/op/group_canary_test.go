package op

import (
	"context"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// TestGroupUpdateClearsDanglingCanaryItem 校验灰度成员被移出分组后 canary_item_id 一并清空。
// 留着悬空的成员 ID 会让管理台显示"灰度开着"却永远不生效, 也会让人误以为新渠道还在接流量。
func TestGroupUpdateClearsDanglingCanaryItem(t *testing.T) {
	cacheIsolationTestDB(t)
	ctx := context.Background()

	config := model.DefaultGroupRelayConfigForMode(model.GroupModeScore)
	config.CanaryItemID = 4242 // 指向一个不存在的分组成员。
	config.CanaryPercent = 20
	group := model.Group{UserID: 61, Name: "canary-dangling", Mode: model.GroupModeScore, RelayConfig: config}
	if err := db.GetDB().Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	groupCache.Set(group.ID, group)

	updated, err := GroupUpdate(group.ID, &model.GroupUpdateRequest{RelayConfig: &config}, model.Scope{ID: 61}, ctx)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.RelayConfig.CanaryItemID != 0 {
		t.Fatalf("dangling canary item survived update: %d", updated.RelayConfig.CanaryItemID)
	}
	// 比例等其余字段必须原样保留: 清掉的只是指向不存在成员的 ID。
	if updated.RelayConfig.CanaryPercent != 20 {
		t.Fatalf("canary percent lost: %d", updated.RelayConfig.CanaryPercent)
	}
	var stored model.Group
	if err := db.GetDB().Where("id = ?", group.ID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.RelayConfig.CanaryItemID != 0 {
		t.Fatalf("dangling canary item still persisted: %d", stored.RelayConfig.CanaryItemID)
	}
}

// TestGroupUpdateKeepsExistingCanaryItem 校验灰度成员仍在分组内时配置不被误清:
// 只在成员确实消失时才清理, 否则每次保存分组都会把灰度悄悄关掉。
func TestGroupUpdateKeepsExistingCanaryItem(t *testing.T) {
	cacheIsolationTestDB(t)
	ctx := context.Background()

	_, grants := quotaTestChannel(t, "canary-kept")

	group := model.Group{UserID: 62, Name: "canary-kept", Mode: model.GroupModeScore,
		RelayConfig: model.DefaultGroupRelayConfigForMode(model.GroupModeScore)}
	if err := db.GetDB().Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	item := model.GroupItem{GroupID: group.ID, ChannelGrantID: grants[0].ID, Priority: 1}
	if err := db.GetDB().Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	config := group.RelayConfig
	config.CanaryItemID = item.ID
	config.CanaryPercent = 30
	group.RelayConfig = config
	if err := db.GetDB().Model(&model.Group{}).Where("id = ?", group.ID).
		Select("relay_config").Updates(&group).Error; err != nil {
		t.Fatal(err)
	}
	groupCache.Set(group.ID, group)

	updated, err := GroupUpdate(group.ID, &model.GroupUpdateRequest{RelayConfig: &config}, model.Scope{ID: 62}, ctx)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.RelayConfig.CanaryItemID != item.ID {
		t.Fatalf("existing canary item cleared: %d, want %d", updated.RelayConfig.CanaryItemID, item.ID)
	}
}

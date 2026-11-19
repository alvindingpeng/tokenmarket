package op

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

func cacheIsolationTestDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "cache-isolation.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	userCache.Clear()
	userNameIndex.Clear()
	groupCache.Clear()
	groupNameIndex.Clear()
}

func TestGroupNameIndexIsScopedByOwner(t *testing.T) {
	cacheIsolationTestDB(t)
	first := model.Group{ID: 1001, UserID: 21, Name: "shared-name"}
	second := model.Group{ID: 1002, UserID: 22, Name: "shared-name"}
	groupCache.Set(first.ID, first)
	groupCache.Set(second.ID, second)
	groupNameIndex.Set(groupIndexKey(first.UserID, first.Name), first.ID)
	groupNameIndex.Set(groupIndexKey(second.UserID, second.Name), second.ID)

	for _, tc := range []struct {
		owner uint
		want  int
	}{{21, first.ID}, {22, second.ID}} {
		got, err := GroupGetByName("shared-name", tc.owner)
		if err != nil || got.ID != tc.want {
			t.Fatalf("owner %d resolved group %d, err=%v; want %d", tc.owner, got.ID, err, tc.want)
		}
	}
	if _, err := GroupGetByName("shared-name", 23); err == nil {
		t.Fatal("unrelated owner must not resolve another user's group")
	}
	if got := GroupList(model.Scope{ID: 21}); len(got) != 1 || got[0].ID != first.ID {
		t.Fatalf("owner-scoped list leaked groups: %+v", got)
	}
}

func TestGroupRenameAndRefreshRemoveStaleNameEntries(t *testing.T) {
	cacheIsolationTestDB(t)
	ctx := context.Background()
	group := model.Group{UserID: 31, Name: "before", Mode: model.GroupModeManual}
	if err := db.GetDB().Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	if err := groupRefreshCache(ctx); err != nil {
		t.Fatal(err)
	}
	newName := "after"
	if _, err := GroupUpdate(group.ID, &model.GroupUpdateRequest{Name: &newName}, model.Scope{ID: 31}, ctx); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, err := GroupGetByName("before", 31); err == nil {
		t.Fatal("old name index must be removed after rename")
	}
	if got, err := GroupGetByName("after", 31); err != nil || got.ID != group.ID {
		t.Fatalf("new name lookup: got=%d err=%v", got.ID, err)
	}
	if err := db.GetDB().Where("id = ?", group.ID).Delete(&model.Group{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := groupRefreshCache(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := GroupGetByName("after", 31); err == nil {
		t.Fatal("refresh from empty DB must clear stale name index")
	}
}

func TestUserRefreshCacheClearsPreviousDatabaseIndexes(t *testing.T) {
	cacheIsolationTestDB(t)
	first := model.User{Username: "same-login", Password: "hash-one", Role: model.RoleUser, Status: model.StatusActive}
	if err := db.GetDB().Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	if err := userRefreshCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := userNameIndex.Get("same-login"); !ok {
		t.Fatal("first database username should be indexed")
	}

	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "second.db"), false); err != nil {
		t.Fatal(err)
	}
	second := model.User{Username: "different-login", Password: "hash-two", Role: model.RoleUser, Status: model.StatusActive}
	if err := db.GetDB().Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	if err := userRefreshCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := userNameIndex.Get("same-login"); ok {
		t.Fatal("refresh must remove names from the previous database")
	}
	if got, ok := userNameIndex.Get("different-login"); !ok || got != int(second.ID) {
		t.Fatalf("second database username missing: id=%d ok=%v", got, ok)
	}
}

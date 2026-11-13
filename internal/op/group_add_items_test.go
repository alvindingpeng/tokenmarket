package op

import (
	"context"
	"fmt"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// addItemsGrants 建一个已发布过审的渠道连同 n 条已上架的授权, 供本文件各用例复用。
// 自建而不复用 quotaTestChannel: 那个只给 2 条, 而"追加到末尾"与并发用例都需要 3 条以上。
func addItemsGrants(t *testing.T, name string, n int) []model.ChannelGrant {
	t.Helper()
	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: name, Enabled: true},
		Shared: true, ReviewStatus: model.ChannelReviewApproved}
	if err := db.GetDB().Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	grants := make([]model.ChannelGrant, 0, n)
	for i := 0; i < n; i++ {
		key := model.ChannelKey{ChannelID: channel.ID, ChannelKeyConfig: model.ChannelKeyConfig{
			Name: fmt.Sprintf("k%d", i), Key: "secret", Enabled: true}}
		if err := db.GetDB().Create(&key).Error; err != nil {
			t.Fatal(err)
		}
		channelModel := model.ChannelModel{ChannelID: channel.ID, Name: fmt.Sprintf("%s-%d", name, i), Listed: true}
		if err := db.GetDB().Create(&channelModel).Error; err != nil {
			t.Fatal(err)
		}
		grant := model.ChannelGrant{ChannelKeyID: key.ID, ChannelModelID: channelModel.ID,
			Protocols: model.ProtocolOpenAIChatCompletion}
		if err := db.GetDB().Create(&grant).Error; err != nil {
			t.Fatal(err)
		}
		grants = append(grants, grant)
	}
	return grants
}

// groupAddItemsFixture 建一个归属于指定用户的分组, 并刷新渠道缓存供准入校验使用。
// 准入校验读的是 channelGrantCache, 不走库, 故必须刷一次缓存 —— 否则授权"存在但校验不到"。
func groupAddItemsFixture(t *testing.T, userID uint, name string, grantCount int) (model.Group, []model.ChannelGrant) {
	t.Helper()
	grants := addItemsGrants(t, name, grantCount)
	if err := channelRefreshCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	group := model.Group{UserID: userID, Name: name, Mode: model.GroupModeFailover,
		RelayConfig: model.DefaultGroupRelayConfigForMode(model.GroupModeFailover)}
	if err := db.GetDB().Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	groupCache.Set(group.ID, group)
	return group, grants
}

// storedGrantIDs 读库里该分组的成员授权主键, 用于断言真实落库结果(而不只是看返回值)。
func storedGrantIDs(t *testing.T, groupID int) []int {
	t.Helper()
	var items []model.GroupItem
	if err := db.GetDB().Where("group_id = ?", groupID).Order("priority").Find(&items).Error; err != nil {
		t.Fatal(err)
	}
	out := make([]int, 0, len(items))
	for _, item := range items {
		out = append(out, item.ChannelGrantID)
	}
	return out
}

// TestGroupAddItemsAppendsToTail 校验只追加语义: 新成员接在末尾, 既有成员的顺序与优先级不动。
// 追加不能重排 —— 重排是分组页的编辑动作, 不该被"加一个模型"顺手改掉。
func TestGroupAddItemsAppendsToTail(t *testing.T) {
	cacheIsolationTestDB(t)
	ctx := context.Background()

	group, grants := groupAddItemsFixture(t, 71, "add-tail", 3)
	// 先占两个成员, 其中一个优先级故意写大, 验证"接在最大值之后"而不是"接在个数之后"。
	first := model.GroupItem{GroupID: group.ID, ChannelGrantID: grants[0].ID, Priority: 1}
	second := model.GroupItem{GroupID: group.ID, ChannelGrantID: grants[2].ID, Priority: 7}
	for _, item := range []model.GroupItem{first, second} {
		if err := db.GetDB().Create(&item).Error; err != nil {
			t.Fatal(err)
		}
	}

	results, err := GroupAddItems(&model.GroupAddItemsRequest{
		GroupIDs: []int{group.ID}, ChannelGrantIDs: []int{grants[1].ID},
	}, model.Scope{ID: 71}, ctx)
	if err != nil {
		t.Fatalf("add items: %v", err)
	}
	if len(results) != 1 || len(results[0].Added) != 1 || results[0].Added[0] != grants[1].ID {
		t.Fatalf("unexpected result: %+v", results)
	}

	var items []model.GroupItem
	if err := db.GetDB().Where("group_id = ?", group.ID).Order("priority").Find(&items).Error; err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("want 3 members, got %d", len(items))
	}
	// 既有两个成员的优先级必须原样保留。
	if items[0].ChannelGrantID != grants[0].ID || items[0].Priority != 1 {
		t.Fatalf("existing member reordered: %+v", items[0])
	}
	if items[1].ChannelGrantID != grants[2].ID || items[1].Priority != 7 {
		t.Fatalf("existing member reordered: %+v", items[1])
	}
	// 新成员排在最大值之后。
	if items[2].ChannelGrantID != grants[1].ID || items[2].Priority != 8 {
		t.Fatalf("new member not appended after max priority: %+v", items[2])
	}
}

// TestGroupAddItemsIsIdempotent 校验重复提交不报错也不重复添加。
// 两个标签页各点一次、或点重了按钮, 都是正常操作, 报 409 只会让用户困惑。
func TestGroupAddItemsIsIdempotent(t *testing.T) {
	cacheIsolationTestDB(t)
	ctx := context.Background()

	group, grants := groupAddItemsFixture(t, 72, "add-idempotent", 2)
	req := &model.GroupAddItemsRequest{GroupIDs: []int{group.ID}, ChannelGrantIDs: []int{grants[0].ID}}

	first, err := GroupAddItems(req, model.Scope{ID: 72}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(first[0].Added) != 1 || len(first[0].Skipped) != 0 {
		t.Fatalf("first add: %+v", first[0])
	}
	second, err := GroupAddItems(req, model.Scope{ID: 72}, ctx)
	if err != nil {
		t.Fatalf("second add must not fail: %v", err)
	}
	if len(second[0].Added) != 0 || len(second[0].Skipped) != 1 {
		t.Fatalf("second add should skip: %+v", second[0])
	}
	if got := storedGrantIDs(t, group.ID); len(got) != 1 {
		t.Fatalf("duplicate member stored: %v", got)
	}
}

// TestGroupAddItemsKeepsStaleSnapshotAdditions 是本函数存在的理由: 基于旧快照的并发追加不能丢成员。
//
// 为什么这样测而不是"两个 goroutine 同时提交":
// 真正的缺陷形态不是"同时", 而是**两个会话都读了同一份旧快照, 然后先后写**。
// 分组在广场/分组页读到的是一份快照, 用户在弹层里停留几秒后提交, 期间另一个会话可能已经加过成员 ——
// 此时若实现按"快照 + 本次新增"算出完整集合并整体替换, 后写的那份把先写的成员整段盖掉。
// 纯并发 goroutine 反而测不出来: SQLite 常把两个事务串行化, 双方各自读到的都是最新集合, 于是双双正确。
//
// 因此这里显式构造"过期快照": 先让缓存留住不含任何成员的旧状态, 再往库里写入 A,
// 然后从这份旧缓存出发追加 B —— 正确实现会读到库里的 A 并保留它; 按缓存算差集的实现会漏掉 A。
func TestGroupAddItemsKeepsStaleSnapshotAdditions(t *testing.T) {
	cacheIsolationTestDB(t)
	ctx := context.Background()

	group, grants := groupAddItemsFixture(t, 73, "add-stale", 2)

	// 模拟"另一个会话先加了 A": 直接写库, 但**不更新缓存**, 于是缓存里仍是空分组(过期快照)。
	otherItem := model.GroupItem{GroupID: group.ID, ChannelGrantID: grants[0].ID, Priority: 1}
	if err := db.GetDB().Create(&otherItem).Error; err != nil {
		t.Fatal(err)
	}
	if _, ok := groupCache.Get(group.ID); !ok {
		t.Fatal("fixture must seed cache")
	}
	// 显式确认缓存确实过期: 这正是现实里浏览器手里的那份数据。
	if cached, _ := groupCache.Get(group.ID); len(cached.Items) != 0 {
		t.Fatalf("stale snapshot unexpected: cache has %d items", len(cached.Items))
	}

	results, err := GroupAddItems(&model.GroupAddItemsRequest{
		GroupIDs: []int{group.ID}, ChannelGrantIDs: []int{grants[1].ID},
	}, model.Scope{ID: 73}, ctx)
	if err != nil {
		t.Fatalf("add from stale snapshot: %v", err)
	}
	if len(results[0].Added) != 1 || results[0].Added[0] != grants[1].ID {
		t.Fatalf("unexpected added: %+v", results[0].Added)
	}

	// 关键断言: 另一个会话加的 A 必须还在。按缓存算差集的实现会把它丢在这里。
	got := storedGrantIDs(t, group.ID)
	if len(got) != 2 {
		t.Fatalf("stale-snapshot add lost the other session's member: %v", got)
	}
	seen := map[int]bool{}
	for _, grantID := range got {
		seen[grantID] = true
	}
	for _, grant := range grants {
		if !seen[grant.ID] {
			t.Fatalf("grant %d missing after add from stale snapshot: %v", grant.ID, got)
		}
	}
}

// TestGroupAddItemsRejectsForeignGroup 校验归属: 分组严格按用户私有, 管理员也改不了别人的。
func TestGroupAddItemsRejectsForeignGroup(t *testing.T) {
	cacheIsolationTestDB(t)
	ctx := context.Background()

	group, grants := groupAddItemsFixture(t, 74, "add-foreign", 2)
	if _, err := GroupAddItems(&model.GroupAddItemsRequest{
		GroupIDs: []int{group.ID}, ChannelGrantIDs: []int{grants[0].ID},
	}, model.Scope{ID: 75}, ctx); err == nil {
		t.Fatal("foreign group must be rejected")
	}
	if got := storedGrantIDs(t, group.ID); len(got) != 0 {
		t.Fatalf("foreign add wrote rows: %v", got)
	}
}

// TestGroupAddItemsRejectsUnlistedGrant 校验准入: 未上架的授权不能加进分组, 与 GroupUpdate 同一口径。
func TestGroupAddItemsRejectsUnlistedGrant(t *testing.T) {
	cacheIsolationTestDB(t)
	ctx := context.Background()

	group, grants := groupAddItemsFixture(t, 76, "add-unlisted", 2)
	// 把授权指向的渠道模型改为未上架, 再刷缓存让准入校验读到新状态。
	if err := db.GetDB().Model(&model.ChannelModel{}).
		Where("id = ?", grants[0].ChannelModelID).Update("listed", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := channelRefreshCache(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := GroupAddItems(&model.GroupAddItemsRequest{
		GroupIDs: []int{group.ID}, ChannelGrantIDs: []int{grants[0].ID},
	}, model.Scope{ID: 76}, ctx); err == nil {
		t.Fatal("unlisted grant must be rejected")
	}
	if got := storedGrantIDs(t, group.ID); len(got) != 0 {
		t.Fatalf("unlisted add wrote rows: %v", got)
	}
}

// TestGroupAddItemsDeduplicatesRequest 校验请求内重复的授权只加一次。
// 前端多选很容易提交重复项, 撞唯一索引会整笔失败, 那是实现缺陷而不是用户错误。
func TestGroupAddItemsDeduplicatesRequest(t *testing.T) {
	cacheIsolationTestDB(t)
	ctx := context.Background()

	group, grants := groupAddItemsFixture(t, 77, "add-duplicate", 2)
	results, err := GroupAddItems(&model.GroupAddItemsRequest{
		GroupIDs: []int{group.ID, group.ID}, ChannelGrantIDs: []int{grants[0].ID, grants[0].ID},
	}, model.Scope{ID: 77}, ctx)
	if err != nil {
		t.Fatalf("dedup add: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("duplicate group ids should collapse: %+v", results)
	}
	if got := storedGrantIDs(t, group.ID); len(got) != 1 {
		t.Fatalf("duplicate grant ids should collapse: %v", got)
	}
}

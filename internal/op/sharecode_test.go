package op

import (
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

func TestShareCodeSequentialAllocation(t *testing.T) {
	ctx, _, admin := reviewTestEnv(t)
	first, err := ChannelCreate(reviewTestDetail("seq-a"), admin, ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ChannelCreate(reviewTestDetail("seq-b"), admin, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ChannelPublish(first.ID, true, admin, ctx); err != nil {
		t.Fatalf("publish first: %v", err)
	}
	if _, err := ChannelPublish(second.ID, true, admin, ctx); err != nil {
		t.Fatalf("publish second: %v", err)
	}
	c1, _ := channelCache.Get(first.ID)
	c2, _ := channelCache.Get(second.ID)
	if c1.ShareCode != "1001" || c2.ShareCode != "1002" {
		t.Fatalf("codes = %q, %q, want 1001, 1002", c1.ShareCode, c2.ShareCode)
	}
	// 取消发布清编码; 再次发布拿新号, 已发号码不回收(号只发不收)。
	if _, err := ChannelPublish(first.ID, false, admin, ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := ChannelPublish(first.ID, true, admin, ctx); err != nil {
		t.Fatal(err)
	}
	c1, _ = channelCache.Get(first.ID)
	if c1.ShareCode != "1003" {
		t.Fatalf("republish code = %q, want 1003", c1.ShareCode)
	}
}

func TestShareCodeBackfillMigration(t *testing.T) {
	ctx, _, admin := reviewTestEnv(t)
	a, err := ChannelCreate(reviewTestDetail("mig-a"), admin, ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ChannelCreate(reviewTestDetail("mig-b"), admin, ctx)
	if err != nil {
		t.Fatal(err)
	}
	c, err := ChannelCreate(reviewTestDetail("mig-c"), admin, ctx)
	if err != nil {
		t.Fatal(err)
	}
	// 模拟存量: 两个 10 位随机码 + 一个已是新规则编码(应原样保留)。
	for id, code := range map[int]string{a.ID: "aB12cd34Ef", b.ID: "1005", c.ID: "xY98zw76Ql"} {
		if err := db.GetDB().Model(&model.Channel{}).Where("id = ?", id).Update("share_code", code).Error; err != nil {
			t.Fatal(err)
		}
	}
	recoded, err := ShareCodeBackfill(ctx)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if recoded != 2 {
		t.Fatalf("recoded = %d, want 2", recoded)
	}
	codeOf := func(id int) string {
		var channel model.Channel
		if err := db.GetDB().First(&channel, id).Error; err != nil {
			t.Fatal(err)
		}
		return channel.ShareCode
	}
	if got := codeOf(a.ID); got != "1001" {
		t.Fatalf("channel a code = %q, want 1001", got)
	}
	if got := codeOf(b.ID); got != "1005" {
		t.Fatalf("channel b code = %q, want unchanged 1005", got)
	}
	if got := codeOf(c.ID); got != "1002" {
		t.Fatalf("channel c code = %q, want 1002 (skips taken 1005)", got)
	}
	// 幂等: 第二次运行零改动。
	again, err := ShareCodeBackfill(ctx)
	if err != nil || again != 0 {
		t.Fatalf("second backfill = %d, err = %v, want 0/nil", again, err)
	}
	// 计数器推到最大已用号(1005), 下一个发布号是 1006。
	code, err := nextShareCode(ctx)
	if err != nil || code != "1006" {
		t.Fatalf("next code = %q, err = %v, want 1006", code, err)
	}
}
func TestShareCodeBackfillAdvancesCounter(t *testing.T) {
	ctx, _, admin := reviewTestEnv(t)
	created, err := ChannelCreate(reviewTestDetail("seq-only"), admin, ctx)
	if err != nil {
		t.Fatal(err)
	}
	// 全部存量都是旧随机码(无任何已合规编码), 回填后计数器也要推到分配结果, 不留起点悬空。
	if err := db.GetDB().Model(&model.Channel{}).Where("id = ?", created.ID).Update("share_code", "aB12cd34Ef").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := ShareCodeBackfill(ctx); err != nil {
		t.Fatal(err)
	}
	code, err := nextShareCode(ctx)
	if err != nil || code != "1002" {
		t.Fatalf("next code = %q, err = %v, want 1002", code, err)
	}
}

package op

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// 渠道级上浮覆盖: 未覆盖跟随全局, 覆盖值优先, 渠道缺失回落全局(不归零)。
func TestChannelMarkupFor(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "markup.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := SettingUpsert(model.SettingKeyMarkupRatio, "0.2"); err != nil {
		t.Fatalf("set global markup: %v", err)
	}
	follow := model.Channel{UserID: 7, Shared: true, ShareCode: "2001", ChannelConfig: model.ChannelConfig{Name: "follow", BaseURL: "https://up.example", MarkupRatio: ChannelMarkupFollowGlobal}}
	over := model.Channel{UserID: 7, Shared: true, ShareCode: "2002", ChannelConfig: model.ChannelConfig{Name: "override", BaseURL: "https://up.example", MarkupRatio: 0.35}}
	if err := db.GetDB().Create(&follow).Error; err != nil {
		t.Fatalf("seed follow: %v", err)
	}
	if err := db.GetDB().Create(&over).Error; err != nil {
		t.Fatalf("seed override: %v", err)
	}
	if err := channelRefreshCache(context.Background()); err != nil {
		t.Fatalf("refresh cache: %v", err)
	}
	if got := ChannelMarkupFor(follow.ID); got != 0.2 {
		t.Fatalf("follow-global markup = %v, want 0.2", got)
	}
	if got := ChannelMarkupFor(over.ID); got != 0.35 {
		t.Fatalf("override markup = %v, want 0.35", got)
	}
	if got := ChannelMarkupFor(999999); got != 0.2 {
		t.Fatalf("missing channel markup = %v, want global 0.2 (never 0)", got)
	}
}

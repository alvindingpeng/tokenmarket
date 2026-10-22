package op

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// TestChannelUpdatePersistsModelAutoAdd 守一个静默失效: 保存渠道时 model_auto_add 必须真的落库。
//
// ChannelUpdate 是逐列点名的(不是整行覆盖), 漏列不会报错, 只会让开关「看上去打开了、实际没打开」——
// 后台复查按这一列挑渠道, 于是自动添加和刚上线的模型级条件都无从触发, 而界面上开关明明是开的。
func TestChannelUpdatePersistsModelAutoAdd(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "persist.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	ctx := context.Background()
	scope := model.Scope{ID: 1, Role: model.RoleAdmin}

	created, err := ChannelCreate(&model.ChannelDetail{ChannelConfig: model.ChannelConfig{
		Name: "persist", BaseURL: "http://up", ModelAutoAdd: false,
	}}, scope, ctx)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ModelAutoAdd {
		t.Fatalf("channel created with auto-add already on")
	}

	// 打开开关后保存: 这才是用户在界面上的实际操作。
	created.ModelAutoAdd = true
	if _, err := ChannelUpdate(created, scope, ctx); err != nil {
		t.Fatalf("update: %v", err)
	}

	var row model.Channel
	if err := db.GetDB().First(&row, created.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !row.ModelAutoAdd {
		t.Fatalf("model_auto_add did not persist on update")
	}
	// 再关掉也必须落库: 只增不减的 Select 漏列会让开关永远卡在开。
	reloaded, ok := channelCache.Get(created.ID)
	if !ok {
		t.Fatalf("channel missing from cache")
	}
	detail := channelDetail(reloaded)
	detail.ModelAutoAdd = false
	if _, err := ChannelUpdate(&detail, scope, ctx); err != nil {
		t.Fatalf("update off: %v", err)
	}
	if err := db.GetDB().First(&row, created.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if row.ModelAutoAdd {
		t.Fatalf("model_auto_add did not clear on update")
	}
}

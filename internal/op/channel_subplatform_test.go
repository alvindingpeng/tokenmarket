package op

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/subplatform"
)

// TestSubscriptionChannelAutoFillsFromPlatform 是 S3 的核心验收。
//
// 订阅渠道的卖点是"建渠道不填调用信息": 用户只给平台名, 系统据此填方言与地址。
// 本测试断言用户什么都不填, 建出来的渠道仍然带有正确的方言与上游地址。
func TestSubscriptionChannelAutoFillsFromPlatform(t *testing.T) {
	initSubPlatformDB(t)
	platform, ok := subplatform.Of("gemini")
	if !ok {
		t.Fatal("gemini 平台未登记")
	}
	scope := model.Scope{ID: 1, Role: model.RoleAdmin}
	// 关键: 方言与地址都留空, 只给平台名。
	created, err := ChannelCreate(&model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{
			Name:        "订阅渠道",
			Dialect:     model.DialectSubscription,
			SubPlatform: "gemini",
		},
	}, scope, context.Background())
	if err != nil {
		t.Fatalf("建订阅渠道失败: %v", err)
	}
	if created.Dialect != platform.Dialect {
		t.Errorf("方言: got %q want %q", created.Dialect, platform.Dialect)
	}
	if created.BaseURL != platform.BaseURL {
		t.Errorf("地址未按平台补齐: got %q want %q", created.BaseURL, platform.BaseURL)
	}
	if created.SubPlatform != platform.ID {
		t.Errorf("平台标识: got %q want %q", created.SubPlatform, platform.ID)
	}
}

// TestSubscriptionChannelKeepsUserBaseURL 用户自填的地址不被平台默认值覆盖。
//
// 平台描述是默认值而不是强制值: 自建中转、企业专线都要求能改地址。
// 若这里被覆盖, 那些用户的上游永远打不通, 且界面上看不出为什么。
func TestSubscriptionChannelKeepsUserBaseURL(t *testing.T) {
	initSubPlatformDB(t)
	scope := model.Scope{ID: 1, Role: model.RoleAdmin}
	const custom = "https://my-proxy.example.com"
	created, err := ChannelCreate(&model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{
			Name:        "自建中转",
			Dialect:     model.DialectSubscription,
			SubPlatform: "gemini",
			BaseURL:     custom,
		},
	}, scope, context.Background())
	if err != nil {
		t.Fatalf("建渠道失败: %v", err)
	}
	if created.BaseURL != custom {
		t.Errorf("用户地址被覆盖: got %q want %q", created.BaseURL, custom)
	}
}

// TestSubscriptionChannelRejectsUnknownPlatform 未登记的平台必须明确报错。
//
// 放行会让渠道带着空地址与未知平台建起来, 错误要到转发时才暴露。
func TestSubscriptionChannelRejectsUnknownPlatform(t *testing.T) {
	initSubPlatformDB(t)
	scope := model.Scope{ID: 1, Role: model.RoleAdmin}
	_, err := ChannelCreate(&model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{
			Name:        "未知平台",
			Dialect:     model.DialectSubscription,
			SubPlatform: "not-a-platform",
		},
	}, scope, context.Background())
	if err == nil {
		t.Fatal("未登记平台必须报错")
	}
	if !strings.Contains(err.Error(), "未登记") {
		t.Errorf("错误信息应点明未登记, 实际: %v", err)
	}
}

// TestSubscriptionChannelRequiresPlatform 订阅渠道必须给出平台名。
//
// 没有平台名, 系统不知道填什么地址; 放行会建出一个无法转发的空壳渠道。
func TestSubscriptionChannelRequiresPlatform(t *testing.T) {
	initSubPlatformDB(t)
	scope := model.Scope{ID: 1, Role: model.RoleAdmin}
	_, err := ChannelCreate(&model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{
			Name:    "无平台",
			Dialect: model.DialectSubscription,
		},
	}, scope, context.Background())
	if err == nil {
		t.Fatal("订阅渠道缺少平台名必须报错")
	}
}

// TestNonSubscriptionChannelRejectsPlatform 非订阅方言不允许带平台名。
//
// 两者同时存在是自相矛盾状态(普通渠道手填地址, 却又说自己是某平台)。
// 放行会让"这个渠道到底该走平台描述还是走手填配置"永远说不清。
func TestNonSubscriptionChannelRejectsPlatform(t *testing.T) {
	initSubPlatformDB(t)
	scope := model.Scope{ID: 1, Role: model.RoleAdmin}
	_, err := ChannelCreate(&model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{
			Name:        "普通渠道",
			Dialect:     model.DialectGeneric,
			SubPlatform: "gemini",
			BaseURL:     "https://up.example.com",
		},
	}, scope, context.Background())
	if err == nil {
		t.Fatal("非订阅方言带平台名必须报错")
	}
}

// initSubPlatformDB 建一个独立测试库并给一个管理员用户。
func initSubPlatformDB(t *testing.T) {
	t.Helper()
	// 用户名按用例名派生: 同一个测试库被多个子用例共用会撞唯一约束。
	username := "subadmin-" + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-"))
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "sub.db"), false); err != nil {
		t.Fatalf("初始化测试库: %v", err)
	}
	if _, err := UserCreate(username, username+"@test.local", "password123", model.RoleAdmin, model.StatusActive, 1); err != nil {
		t.Fatalf("建管理员: %v", err)
	}
}

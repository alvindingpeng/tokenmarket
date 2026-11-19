package op

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
)

func breakerCacheFixture(t *testing.T) (model.ChannelKey, model.ChannelModel, model.ChannelGrant, []model.Group) {
	t.Helper()
	breakerTestSetup(t)
	channel := model.Channel{Shared: true, ChannelConfig: model.ChannelConfig{Name: "cache-breaker", Enabled: true}}
	if err := db.GetDB().Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	key := model.ChannelKey{ChannelID: channel.ID, ChannelKeyConfig: model.ChannelKeyConfig{Name: "key", Key: "secret", Enabled: true}}
	cm := model.ChannelModel{ChannelID: channel.ID, Name: "model", Listed: true, SupplyPrice: model.LLMPrice{Input: 7}}
	if err := db.GetDB().Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().Create(&cm).Error; err != nil {
		t.Fatal(err)
	}
	grant := model.ChannelGrant{ChannelKeyID: key.ID, ChannelModelID: cm.ID, Protocols: model.ProtocolOpenAIChatCompletion}
	if err := db.GetDB().Create(&grant).Error; err != nil {
		t.Fatal(err)
	}
	if err := channelRefreshCache(context.Background()); err != nil {
		t.Fatal(err)
	}
	groups := []model.Group{
		{ID: 90001, UserID: 101, Items: []model.GroupItem{{ChannelGrantID: grant.ID}}},
		{ID: 90002, UserID: 102, Items: []model.GroupItem{{ChannelGrantID: grant.ID}}},
	}
	for _, g := range groups {
		groupCache.Set(g.ID, g)
	}
	t.Cleanup(func() {
		for _, g := range groups {
			groupCache.Del(g.ID)
		}
	})
	return key, cm, grant, groups
}

func TestBreakerCacheGroupAndFixedGrant(t *testing.T) {
	for _, kind := range []string{"key", "model", "auth", "quota"} {
		t.Run(kind, func(t *testing.T) {
			key, cm, grant, groups := breakerCacheFixture(t)
			check := func(available bool) {
				t.Helper()
				for _, group := range groups {
					scope := model.Scope{ID: group.UserID}
					got, err := GroupGet(group.ID, scope)
					if err != nil || len(got.Items) != 1 || got.Items[0].Available != available {
						t.Fatalf("GroupGet availability=%v, got=%+v err=%v", available, got, err)
					}
					snapshot := groupSnapshot(group, scope)
					if len(snapshot.Items) != 1 || snapshot.Items[0].Available != available {
						t.Fatalf("snapshot: %+v", snapshot)
					}
				}
			}
			check(true)
			// Capture caller-owned values before a transition: cache updates must not mutate old pointers.
			before, err := ChannelGrantGet(grant.ID)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "model" {
				RecordChannelModelError(cm.ID, "overload")
			} else {
				errorType := "overload"
				if kind == "auth" || kind == "quota" {
					errorType = kind
				}
				RecordChannelKeyError(key.ID, errorType)
			}
			check(false)
			got, err := ChannelGrantGet(grant.ID)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "model" {
				if !isCircuitBroken(got.ChannelModel.DisabledUntil) || got.ChannelModel.LastErrorAt == nil {
					t.Fatalf("fixed model grant stale: %+v", got.ChannelModel)
				}
				ResetChannelModelErrors(cm.ID)
			} else {
				if !isCircuitBroken(got.ChannelKey.DisabledUntil) || got.ChannelKey.LastErrorAt == nil {
					t.Fatalf("fixed key grant stale: %+v", got.ChannelKey)
				}
				ResetChannelKeyErrors(key.ID)
			}
			check(true)
			got, err = ChannelGrantGet(grant.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.ChannelKey.DisabledUntil != nil || got.ChannelModel.DisabledUntil != nil {
				t.Fatal("reset did not reach fixed grant")
			}
			if before.ChannelKey.DisabledUntil != nil || before.ChannelModel.DisabledUntil != nil {
				t.Fatal("mutated old read snapshot")
			}
		})
	}
}

func TestBreakerCacheFailedWritesDoNotChangeCache(t *testing.T) {
	for _, reset := range []bool{false, true} {
		t.Run(fmt.Sprint(reset), func(t *testing.T) {
			key, cm, grant, _ := breakerCacheFixture(t)
			if reset {
				RecordChannelKeyError(key.ID, "auth")
				RecordChannelModelError(cm.ID, "overload")
			}
			before, _ := ChannelGrantGet(grant.ID)
			conn := db.GetDB()
			const callback = "test:fail_breaker_update"
			if err := conn.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) { tx.AddError(fmt.Errorf("injected update failure")) }); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { conn.Callback().Update().Remove(callback) })
			if reset {
				ResetChannelKeyErrors(key.ID)
				ResetChannelModelErrors(cm.ID)
			} else {
				RecordChannelKeyError(key.ID, "auth")
				RecordChannelKeyError(key.ID, "overload")
				RecordChannelModelError(cm.ID, "overload")
			}
			after, _ := ChannelGrantGet(grant.ID)
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("failed write changed cache: before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestBreakerCacheConcurrentStatsPreserved(t *testing.T) {
	key, cm, grant, _ := breakerCacheFixture(t)
	for _, setting := range []model.SettingKey{model.SettingKeyBreakerKeyThreshold, model.SettingKeyBreakerModelThreshold} {
		if err := SettingUpsert(setting, "0"); err != nil {
			t.Fatal(err)
		}
	}
	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			RecordChannelKeyError(key.ID, "overload")
			RecordChannelModelError(cm.ID, "overload")
		}()
		go func() {
			defer wg.Done()
			_ = ChannelKeyStatsUpdate(key.ID, model.StatsMetrics{RequestSuccess: 1})
			_ = ChannelModelStatsUpdate(cm.ID, model.StatsMetrics{RequestSuccess: 1})
		}()
	}
	wg.Wait()
	got, err := ChannelGrantGet(grant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ChannelKey.ErrorCount != n || got.ChannelModel.ErrorCount != n || got.ChannelKey.RequestSuccess != n || got.ChannelModel.RequestSuccess != n || got.ChannelModel.SupplyPrice.Input != 7 {
		t.Fatalf("lost error/stat/config fields: key=%+v model=%+v", got.ChannelKey, got.ChannelModel)
	}
}

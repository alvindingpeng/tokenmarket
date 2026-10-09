package relay

import (
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/ratelimit"
	"testing"
	"time"
)

func TestEveryRoutingModeHonorsLimitsIncludingAffinity(t *testing.T) {
	original := limiter
	defer func() { limiter = original }()
	for i, mode := range []model.GroupMode{model.GroupModeManual, model.GroupModeFailover, model.GroupModePrice, model.GroupModeLatency, model.GroupModeSuccess, model.GroupModeScore, model.GroupModeRandom} {
		t.Run(string(mode), func(t *testing.T) {
			limiter = ratelimit.New(func(s ratelimit.Scope) ratelimit.Limits { return ratelimit.Limits{Concurrent: 1} })
			a := model.GroupItem{ID: 1, ChannelID: 1, ChannelKeyID: 1, ChannelModelID: 1, ModelName: "a", Available: true, Priority: 1}
			b := model.GroupItem{ID: 2, ChannelID: 2, ChannelKeyID: 2, ChannelModelID: 2, ModelName: "b", Available: true, Priority: 2}
			g := model.Group{ID: 900 + i, Mode: mode, ActiveItemID: 1, Items: []model.GroupItem{a, b}, RelayConfig: model.DefaultGroupRelayConfigForMode(mode)}
			defer ResetRouteState(g.ID)
			d := limiter.Reserve(upstreamItemScopes(a), 1, 1)
			routeMu.Lock()
			routes[g.ID] = &RouteState{GroupID: g.ID, CurrentItemID: 1, AffinityUntil: time.Now().Add(time.Minute).UnixMilli(), Cooldowns: map[int]int64{}}
			routeMu.Unlock()
			chosen := pickGroupItem(g)
			if mode == model.GroupModeManual {
				if chosen.ID != 0 {
					t.Fatal("manual target bypassed concurrency")
				}
			} else if chosen.ID != 2 {
				t.Fatalf("affinity bypassed concurrency: %d", chosen.ID)
			}
			e := limiter.Reserve(upstreamItemScopes(b), 1, 1)
			if chosen := pickGroupItem(g); chosen.ID != 0 {
				t.Fatal("fallback bypassed all-candidate limits")
			}
			d.Reservation.Settle(0, 0, true)
			e.Reservation.Settle(0, 0, true)
		})
	}
}

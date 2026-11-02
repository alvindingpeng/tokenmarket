package op

import (
	"context"
	"errors"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
)

// ProbeBreakerTarget is wired by cmd to avoid an op -> relay import cycle.
// Exactly one of keyID/modelID is nonzero. No safe text grant must return an error.
var ProbeBreakerTarget func(ctx context.Context, channelID, keyID, modelID int) error

const breakerProbeTimeout = 20 * time.Second
const breakerProbeDelay = 5 * time.Minute

type breakerProbeSnapshot struct {
	ID, ChannelID, ErrorCount, BreakerTrips, ProbeCount int
	DisabledUntil, LastErrorAt, ProbeAt                 *time.Time
}

func breakerProbeCAS(q *gorm.DB, s breakerProbeSnapshot) *gorm.DB {
	q = q.Where("id = ? AND channel_id = ? AND error_count = ? AND breaker_trips = ? AND probe_count = ?", s.ID, s.ChannelID, s.ErrorCount, s.BreakerTrips, s.ProbeCount)
	for col, value := range map[string]*time.Time{"disabled_until": s.DisabledUntil, "last_error_at": s.LastErrorAt, "probe_at": s.ProbeAt} {
		if value == nil {
			q = q.Where(col + " IS NULL")
		} else {
			q = q.Where(col+" = ?", *value)
		}
	}
	return q
}

// BreakerRecoveryProbe only releases a breaker after a real upstream validation.
// This is a background recovery lease, NOT a global half-open admission policy:
// isCircuitBroken retains its existing expiry semantics outside a claimed probe.
func BreakerRecoveryProbe(ctx context.Context) error {
	if ProbeBreakerTarget == nil {
		return errors.New("breaker probe executor is not configured")
	}
	for _, key := range []bool{true, false} {
		var entity interface{} = &model.ChannelModel{}
		if key {
			entity = &model.ChannelKey{}
		}
		now := time.Now()
		if key {
			var items []model.ChannelKey
			if err := db.GetDB().WithContext(ctx).Where("disabled_until IS NOT NULL AND disabled_until < ? AND probe_count < ? AND (probe_at IS NULL OR probe_at < ?)", now, 3, now.Add(-2*time.Minute)).Find(&items).Error; err != nil {
				return err
			}
			for _, item := range items {
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := recoverBreakerTarget(ctx, entity, true, keySnapshot(item)); err != nil {
					return err
				}
			}
		} else {
			var items []model.ChannelModel
			if err := db.GetDB().WithContext(ctx).Where("disabled_until IS NOT NULL AND disabled_until < ? AND probe_count < ? AND (probe_at IS NULL OR probe_at < ?)", now, 3, now.Add(-2*time.Minute)).Find(&items).Error; err != nil {
				return err
			}
			for _, item := range items {
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := recoverBreakerTarget(ctx, entity, false, modelSnapshot(item)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func recoverBreakerTarget(ctx context.Context, entity interface{}, key bool, row breakerProbeSnapshot) error {
	now := time.Now()
	until := now.Add(breakerProbeDelay) // longer than the bounded request; crash leaves breaker closed.
	channelBreakerWriteMu.Lock()
	// A per-key/model probe cannot validate an account-wide quota pause.
	// Consult persistence as recovery can run before the routing cache is loaded.
	var paused int64
	if err := db.GetDB().WithContext(ctx).Model(&model.Channel{}).Where("id = ? AND disabled_until > ?", row.ChannelID, now).Count(&paused).Error; err != nil {
		channelBreakerWriteMu.Unlock()
		return err
	}
	if paused != 0 {
		channelBreakerWriteMu.Unlock()
		return nil
	}
	result := breakerProbeCAS(db.GetDB().WithContext(ctx).Model(entity), row).
		Where("disabled_until < ? AND probe_count < 3 AND (probe_at IS NULL OR probe_at < ?)", now, now.Add(-2*time.Minute)).
		Updates(map[string]interface{}{"disabled_until": until, "probe_at": now, "probe_count": row.ProbeCount + 1})
	if result.Error != nil || result.RowsAffected == 0 {
		channelBreakerWriteMu.Unlock()
		return result.Error
	}
	row.DisabledUntil, row.ProbeAt, row.ProbeCount = &until, &now, row.ProbeCount+1
	syncBreakerProbeCache(key, row)
	channelBreakerWriteMu.Unlock()

	probeCtx, cancel := context.WithTimeout(ctx, breakerProbeTimeout)
	keyID, modelID := 0, 0
	if key {
		keyID = row.ID
	} else {
		modelID = row.ID
	}
	probeErr := ProbeBreakerTarget(probeCtx, row.ChannelID, keyID, modelID)
	if probeCtx.Err() != nil {
		probeErr = probeCtx.Err()
	}
	cancel()
	// A failed or cancelled request keeps the claim's five-minute isolation intact.
	if probeErr != nil {
		return nil
	}
	channelBreakerWriteMu.Lock()
	defer channelBreakerWriteMu.Unlock()
	result = breakerProbeCAS(db.GetDB().WithContext(ctx).Model(entity), row).Updates(map[string]interface{}{
		"disabled_until": nil, "error_count": 0, "breaker_trips": 0, "probe_count": 0, "probe_at": nil,
	})
	if result.Error != nil || result.RowsAffected == 0 {
		return result.Error
	}
	row.DisabledUntil, row.ProbeAt = nil, nil
	row.ErrorCount, row.BreakerTrips, row.ProbeCount = 0, 0, 0
	syncBreakerProbeCache(key, row)
	return nil
}

func syncBreakerProbeCache(key bool, row breakerProbeSnapshot) {
	if key {
		updateChannelKeyBreakerCache(row.ID, func(c *model.ChannelKey) {
			c.DisabledUntil = row.DisabledUntil
			c.ErrorCount = row.ErrorCount
			c.BreakerTrips = row.BreakerTrips
			c.ProbeCount = row.ProbeCount
			c.ProbeAt = row.ProbeAt
		})
	} else {
		updateChannelModelBreakerCache(row.ID, func(c *model.ChannelModel) {
			c.DisabledUntil = row.DisabledUntil
			c.ErrorCount = row.ErrorCount
			c.BreakerTrips = row.BreakerTrips
			c.ProbeCount = row.ProbeCount
			c.ProbeAt = row.ProbeAt
		})
	}
}
func keySnapshot(v model.ChannelKey) breakerProbeSnapshot {
	return breakerProbeSnapshot{ID: v.ID, ChannelID: v.ChannelID, ErrorCount: v.ErrorCount, BreakerTrips: v.BreakerTrips, ProbeCount: v.ProbeCount, DisabledUntil: v.DisabledUntil, LastErrorAt: v.LastErrorAt, ProbeAt: v.ProbeAt}
}
func modelSnapshot(v model.ChannelModel) breakerProbeSnapshot {
	return breakerProbeSnapshot{ID: v.ID, ChannelID: v.ChannelID, ErrorCount: v.ErrorCount, BreakerTrips: v.BreakerTrips, ProbeCount: v.ProbeCount, DisabledUntil: v.DisabledUntil, LastErrorAt: v.LastErrorAt, ProbeAt: v.ProbeAt}
}

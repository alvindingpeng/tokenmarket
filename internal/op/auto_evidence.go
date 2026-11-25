package op

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync/atomic"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const autoToolProbeValidity = 30 * 24 * time.Hour

// AutoUpstreamRevision includes the routing destination, credential and request-shaping settings.
func AutoUpstreamRevision(channel model.Channel, grant model.ChannelGrant) string {
	data, _ := json.Marshal(struct {
		Endpoint    string
		Headers     []model.CustomHeader
		Params      string
		Protocols   model.Protocol
		Key, Secret string
	}{
		model.ChannelConfigFingerprint(channel.ChannelConfig, []string{grant.ChannelModel.Name}),
		channel.CustomHeader, channel.ParamOverride, grant.Protocols, grant.ChannelKey.Key, grant.ChannelKey.APISecret,
	})
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func AutoGrantRevision(grantID int) string {
	grant, err := ChannelGrantGet(grantID)
	if err != nil || grant.ChannelModel == nil || grant.ChannelKey == nil {
		return ""
	}
	channel, err := ChannelGet(grant.ChannelModel.ChannelID)
	if err != nil {
		return ""
	}
	return AutoUpstreamRevision(channel, grant)
}

func AutoToolProbeSave(ctx context.Context, run model.AutoToolProbeRun) error {
	return db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&run).Error; err != nil {
			return err
		}
		var cutoff model.AutoToolProbeRun
		err := tx.Select("id", "started_at").Where("grant_id = ?", run.GrantID).
			Order("started_at DESC, id DESC").Offset(19).Take(&cutoff).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		return tx.Where("grant_id = ? AND (started_at < ? OR (started_at = ? AND id < ?))",
			run.GrantID, cutoff.StartedAt, cutoff.StartedAt, cutoff.ID).Delete(&model.AutoToolProbeRun{}).Error
	})
}

func AutoToolProbeStatus(grantID int, revision string, now time.Time) string {
	var run model.AutoToolProbeRun
	err := db.GetDB().Where("grant_id = ?", grantID).Order("started_at DESC, id DESC").Take(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "unverified"
	}
	if err != nil {
		return "unavailable"
	}
	return autoToolProbeStatus(run, revision, now)
}

func autoToolProbeStatus(run model.AutoToolProbeRun, revision string, now time.Time) string {
	switch {
	case run.ID == 0:
		return "unverified"
	case revision == "" || run.Revision != revision:
		return "changed"
	case now.Sub(run.CreatedAt) >= autoToolProbeValidity:
		return "expired"
	case !run.Success:
		return "failed"
	default:
		return "verified"
	}
}

func AutoSessionGet(ctx context.Context, key string) (model.AutoSessionBinding, string) {
	var row model.AutoSessionBinding
	err := db.GetDB().WithContext(ctx).Where("key = ?", key).Take(&row).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return row, "auto_signed_session_required"
	case err != nil:
		return row, "auto_session_store_unavailable"
	case !row.ExpiresAt.After(time.Now()):
		return row, "auto_session_expired"
	default:
		return row, ""
	}
}

var autoSessionCleanup atomic.Int64

func AutoSessionSave(ctx context.Context, row model.AutoSessionBinding) error {
	conn := db.GetDB().WithContext(ctx)
	updates := clause.Set{}
	for _, field := range []struct {
		name  string
		value any
	}{
		{"group_id", row.GroupID}, {"item_id", row.ItemID}, {"revision", row.Revision},
		{"expires_at", row.ExpiresAt}, {"started_at", row.StartedAt},
	} {
		updates = append(updates, clause.Assignment{Column: clause.Column{Name: field.name},
			Value: gorm.Expr("CASE WHEN ? <= ? THEN ? ELSE ? END",
				clause.Column{Table: "auto_session_bindings", Name: "started_at"}, row.StartedAt, field.value,
				clause.Column{Table: "auto_session_bindings", Name: field.name})})
	}
	err := conn.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "key"}}, DoUpdates: updates,
	}).Create(&row).Error
	now := time.Now()
	previous := autoSessionCleanup.Load()
	if err == nil && now.Unix()-previous >= 3600 && autoSessionCleanup.CompareAndSwap(previous, now.Unix()) {
		// Keep recently expired rows long enough to diagnose a continuation.
		_ = conn.Where("expires_at < ?", now.Add(-24*time.Hour)).Delete(&model.AutoSessionBinding{}).Error
	}
	return err
}

type AutoProbeTaskSummary struct {
	Task             string  `json:"task"`
	Passed           int     `json:"passed"`
	Total            int     `json:"total"`
	StreamsCompleted int     `json:"streams_completed"`
	StreamsExpected  int     `json:"streams_expected"`
	LatencyP90MS     float64 `json:"latency_p90_ms"`
}

type AutoProbeCandidate struct {
	GrantID int                      `json:"grant_id"`
	Channel string                   `json:"channel"`
	Model   string                   `json:"model"`
	Status  string                   `json:"status"`
	Runs    []model.AutoToolProbeRun `json:"runs"`
	Tasks   []AutoProbeTaskSummary   `json:"tasks"`
}

// AutoProbeCandidates compares the five latest runs on each configured tool grant.
func AutoProbeCandidates(scope model.Scope, groupID int, now time.Time) ([]AutoProbeCandidate, error) {
	if !scope.IsAdmin() || groupID <= 0 {
		return nil, nil
	}
	group, err := GroupGet(groupID, scope)
	if err != nil {
		return nil, nil
	}
	candidates := []AutoProbeCandidate{}
	ids := []int{}
	configured := autoGrantSet(group.RelayConfig.AutoTool)
	for _, item := range group.Items {
		if configured[item.ChannelGrantID] && item.Protocols&model.ProtocolAnthropicMessage != 0 {
			ids = append(ids, item.ChannelGrantID)
			candidates = append(candidates, AutoProbeCandidate{
				GrantID: item.ChannelGrantID, Channel: item.ChannelName, Model: item.ModelName,
			})
		}
	}
	if len(ids) == 0 {
		return candidates, nil
	}
	var rows []model.AutoToolProbeRun
	if err := db.GetDB().Where("grant_id IN ?", ids).Order("started_at DESC, id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	byGrant := make(map[int][]model.AutoToolProbeRun, len(ids))
	for _, row := range rows {
		if len(byGrant[row.GrantID]) < 5 {
			byGrant[row.GrantID] = append(byGrant[row.GrantID], row)
		}
	}
	for i := range candidates {
		candidate := &candidates[i]
		revision := AutoGrantRevision(candidate.GrantID)
		candidate.Runs = byGrant[candidate.GrantID]
		candidate.Status = "unverified"
		if len(candidate.Runs) > 0 {
			candidate.Status = autoToolProbeStatus(candidate.Runs[0], revision, now)
		}
		for _, task := range []string{model.AutoRoutingTaskTool, model.AutoRoutingTaskCoding} {
			summary := AutoProbeTaskSummary{Task: task}
			var latencies []float64
			for _, run := range candidate.Runs {
				if run.Revision != revision {
					continue
				}
				for _, evidence := range run.Evaluation {
					if evidence.Task != task {
						continue
					}
					summary.Passed += evidence.Passed
					summary.Total += evidence.Total
					summary.StreamsCompleted += evidence.StreamsCompleted
					summary.StreamsExpected += evidence.StreamsExpected
					latencies = append(latencies, float64(evidence.DurationMS))
				}
			}
			if len(latencies) > 0 {
				summary.LatencyP90MS = autoPercentile(latencies, .9)
				candidate.Tasks = append(candidate.Tasks, summary)
			}
		}
	}
	return candidates, nil
}

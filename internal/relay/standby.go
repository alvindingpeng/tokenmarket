package relay

import (
	"slices"
	"strconv"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/tidwall/gjson"
)

type standbyRetry struct {
	lastError error
	failures  int
	exhausted bool
	failed    scoreRetryState
}

// reserveStandby keeps the grant out of normal routing, including canary and
// recovery probes, while retaining its ID for cooldown tracking.
func reserveStandby(group model.Group) (model.Group, model.GroupItem) {
	if group.Mode == model.GroupModeManual || group.RelayConfig.StandbyGrantID == 0 || len(group.Items) < 2 {
		return group, model.GroupItem{}
	}
	for i, item := range group.Items {
		if item.ChannelGrantID == group.RelayConfig.StandbyGrantID {
			group.Items = slices.Clone(group.Items)
			group.Items[i].Available = false
			group.StandbyItemID = item.ID
			return group, item
		}
	}
	return group, model.GroupItem{}
}

func (s *standbyRetry) record(item model.GroupItem, class ErrorClass, err error) {
	s.failures++
	s.lastError = err
	s.failed.exclude(item, class == ErrorClassQuota)
}

func (s *standbyRetry) eligible(group model.Group, item model.GroupItem, protocol model.Protocol, body []byte, input, output int64, active bool) bool {
	if s.lastError == nil || item.ID == 0 || !item.Available || s.failed.excludes(item) ||
		upstreamItemLimited(item) || upstreamItemBlockedByPolicy(item) || item.Protocols&protocol == 0 {
		return false
	}
	state := RouteStateOf(group)
	if state.Cooldowns[item.ID] > time.Now().UnixMilli() || state.ProbeItemID == item.ID {
		return false
	}
	task := classifyAuto(body, protocol, input, output)
	if task.name == AutoTaskUnsupported || task.signedHistory {
		return false
	}
	// Encrypted Responses reasoning belongs to the upstream that issued it.
	if protocol == model.ProtocolOpenAIResponse {
		for _, part := range gjson.GetBytes(body, "input").Array() {
			if part.Get("encrypted_content").String() != "" {
				return false
			}
		}
	}
	grant, err := op.ChannelGrantGet(item.ChannelGrantID)
	if err != nil || grant.Protocols&protocol == 0 || grant.ChannelModel.Kind != model.MediaKindText {
		return false
	}
	if active && autoCircuitOpen(group.ID, task.name) {
		return false
	}
	if active || task.name == AutoTaskTool {
		group.Items = []model.GroupItem{item}
		eligible, _ := autoCandidates(group, task)
		return len(eligible.Items) == 1
	}
	reason := autoTokenExclusion(grant.ChannelModel, task, group.RelayConfig.AutoContextOverrides[strconv.Itoa(item.ChannelGrantID)])
	if reason == "auto_context_unknown" {
		exceeded, _, _ := checkContextLimit(grant.ChannelModel.Name, body)
		return !exceeded
	}
	return reason == ""
}

package relay

import "github.com/alvindingpeng/switchyard/internal/model"

// scoreRetryState is request-local: shared cooldown expiry and concurrent probes
// must never reintroduce a failed channel/model through another grant or key.
type scoreRetryState struct {
	failed        map[[2]int]bool
	quotaChannels map[int]bool
}

func (s *scoreRetryState) exclude(item model.GroupItem, quota bool) {
	if s.failed == nil {
		s.failed = make(map[[2]int]bool)
	}
	s.failed[[2]int{item.ChannelID, item.ChannelModelID}] = true
	if quota {
		if s.quotaChannels == nil {
			s.quotaChannels = make(map[int]bool)
		}
		s.quotaChannels[item.ChannelID] = true
	}
}

// filtered keeps every member ID so pickGroupItem cannot mistake locally
// excluded members for deleted configuration and erase shared route state.
// Copy on write also leaves the cached group and every other request untouched.
func (s *scoreRetryState) filtered(group model.Group) model.Group {
	if group.Mode != model.GroupModeScore || (len(s.failed) == 0 && len(s.quotaChannels) == 0) {
		return group
	}
	items := append([]model.GroupItem(nil), group.Items...)
	for i := range items {
		item := &items[i]
		if s.failed[[2]int{item.ChannelID, item.ChannelModelID}] || s.quotaChannels[item.ChannelID] {
			item.Available = false
		}
	}
	group.Items = items
	return group
}

// Preserve the configured group budget; five is an example, not a hard cap.
func scoreAttemptLimit(configured int) int {
	return configured
}

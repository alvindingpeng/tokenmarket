package relay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
)

func autoSessionKey(groupID, keyID int, userID uint, task autoTask, session string) string {
	key := autoStickyKeyFor(groupID, keyID, userID, task.name, task.protocol, session)
	encoded, _ := json.Marshal(key)
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:])
}

func autoSessionLookup(ctx context.Context, group model.Group, keyID int, userID uint, task autoTask, session string) (model.AutoSessionBinding, string) {
	if group.RelayConfig.AutoStickySeconds <= 0 {
		return model.AutoSessionBinding{}, "auto_session_disabled"
	}
	row, reason := op.AutoSessionGet(ctx, autoSessionKey(group.ID, keyID, userID, task, session))
	if reason != "" {
		return row, reason
	}
	member, found := autoItemByID(group.Items, row.ItemID)
	if !found || !member.Available {
		return row, "auto_session_member_unavailable"
	}
	if op.AutoGrantRevision(member.ChannelGrantID) != row.Revision {
		return row, "auto_session_upstream_changed"
	}
	if RouteStateOf(group).Cooldowns[member.ID] > time.Now().UnixMilli() {
		return row, "auto_session_member_cooling"
	}
	return row, ""
}

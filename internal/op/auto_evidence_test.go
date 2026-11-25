package op

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

func TestAutoEvidenceSurvivesDatabaseRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auto-evidence.db")
	if err := db.InitDB("sqlite", path, false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	channel := model.Channel{ChannelConfig: model.ChannelConfig{BaseURL: "https://example.com"}}
	grant := model.ChannelGrant{ID: 7, Protocols: model.ProtocolAnthropicMessage,
		ChannelModel: &model.ChannelModel{Name: "fixture"},
		ChannelKey:   &model.ChannelKey{ChannelKeyConfig: model.ChannelKeyConfig{Key: "fixture-key"}}}
	revision := AutoUpstreamRevision(channel, grant)
	run := model.AutoToolProbeRun{GrantID: grant.ID, StartedAt: now, CreatedAt: now, Revision: revision, Success: true}
	if err := AutoToolProbeSave(ctx, run); err != nil {
		t.Fatal(err)
	}
	row := model.AutoSessionBinding{Key: "session-digest", GroupID: 1, ItemID: 2, Revision: revision, StartedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := AutoSessionSave(ctx, row); err != nil {
		t.Fatal(err)
	}
	older := row
	older.ItemID = 3
	older.StartedAt = now.Add(-time.Second)
	if err := AutoSessionSave(ctx, older); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.InitDB("sqlite", path, false); err != nil {
		t.Fatal(err)
	}
	binding, reason := AutoSessionGet(ctx, row.Key)
	if reason != "" || binding.ItemID != row.ItemID || binding.Revision != revision {
		t.Fatalf("restored binding=%+v reason=%q", binding, reason)
	}
	for _, tc := range []struct {
		name, revision, want string
		at                   time.Time
	}{
		{"current", revision, "verified", now},
		{"expired", revision, "expired", now.Add(31 * 24 * time.Hour)},
	} {
		if got := AutoToolProbeStatus(grant.ID, tc.revision, tc.at); got != tc.want {
			t.Errorf("%s: status=%s, want %s", tc.name, got, tc.want)
		}
	}
	grant.ChannelKey.Key = "rotated-fixture-key"
	if got := AutoToolProbeStatus(grant.ID, AutoUpstreamRevision(channel, grant), now); got != "changed" {
		t.Fatalf("old probe still valid after credential change: %s", got)
	}
	run.StartedAt = now.Add(time.Second)
	run.Success = false
	if err := AutoToolProbeSave(ctx, run); err != nil {
		t.Fatal(err)
	}
	if got := AutoToolProbeStatus(grant.ID, revision, now); got != "failed" {
		t.Fatalf("latest failed result must replace previous verification: %s", got)
	}
}

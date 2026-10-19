package op

import (
	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"testing"
	"time"
)

func TestChannelStatusAuditedCompletionAndExclusions(t *testing.T) {
	ctx := channelStatusTestEnv(t)
	now := time.Now()
	ch := channelStatusSeedChannel(t, "4555", "private", nil)
	channelStatusSeedModel(t, ch.ID, "m", "", model.LLMPrice{Input: 1}, nil)
	channelStatusReload(t, ctx)
	for i, status := range []string{"running", "canceled", "client_error", "policy_blocked", "limited", "model_missing"} {
		channelStatusSeedAttempt(t, uint64(9000+i), 1, ch.ID, "m", now.Add(-time.Minute), status, 100)
	}
	channelStatusSeedAttempt(t, 9100, 1, ch.ID, "m", now.Add(-time.Minute), "success", 100)
	db.GetDB().Model(&model.RelayRequest{}).Where("id = ?", 9100).Update("status", "committed")
	snapshot, err := channelStatusBuild(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	row := channelStatusMustFind(t, snapshot, "4555", "m")
	if row.Attempts != 0 || row.LatencyMs != nil {
		t.Fatalf("inflight or non-provider samples leaked: %+v", row)
	}
	db.GetDB().Model(&model.RelayRequest{}).Where("id = ?", 9100).Update("status", "success")
	snapshot, err = channelStatusBuild(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	row = channelStatusMustFind(t, snapshot, "4555", "m")
	if row.Attempts != 1 || row.Status != "low_sample" || row.LatencyMs == nil || *row.LatencyMs != 100 {
		t.Fatalf("completed timing: %+v", row)
	}
	ch.TrustLevel = model.ChannelTrustRevoked
	channelCache.Set(ch.ID, ch)
	if len(channelStatusVisible(snapshot).Models) != 0 {
		t.Fatal("revoked channel survived snapshot filter")
	}
}

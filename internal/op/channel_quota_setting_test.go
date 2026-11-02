package op

import (
	"github.com/alvindingpeng/switchyard/internal/model"
	"testing"
	"time"
)

func TestChannelQuotaPauseZeroHardDurationFallsBack(t *testing.T) {
	breakerTestSetup(t)
	if err := SettingUpsert(model.SettingKeyBreakerKeyHardMinutes, "0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = SettingUpsert(model.SettingKeyBreakerKeyHardMinutes, "30") })
	channel, _ := quotaTestChannel(t, "zero-duration")
	if err := RecordChannelQuotaError(channel.ID); err != nil {
		t.Fatal(err)
	}
	cached, err := ChannelGet(channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cached.DisabledUntil == nil || time.Until(*cached.DisabledUntil).Round(time.Minute) != 30*time.Minute {
		t.Fatal("zero hard quota setting bypassed channel pause")
	}
}

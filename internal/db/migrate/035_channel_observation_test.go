package migrate

import (
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestMigration035ChannelObservation(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.AutoMigrate(&model.Channel{}); err != nil {
		t.Fatal(err)
	}
	legacy := model.Channel{ChannelConfig: model.ChannelConfig{Name: "legacy"}, ReviewStatus: model.ChannelReviewApproved, TrustLevel: model.ChannelTrustObserving}
	active := model.Channel{ChannelConfig: model.ChannelConfig{Name: "active"}, ReviewStatus: model.ChannelReviewApproved, TrustLevel: model.ChannelTrustObserving, ObservationStart: ptrTime()}
	unapproved := model.Channel{ChannelConfig: model.ChannelConfig{Name: "pending"}, ReviewStatus: model.ChannelReviewPending, TrustLevel: model.ChannelTrustObserving}
	for _, channel := range []*model.Channel{&legacy, &active, &unapproved} {
		if err := conn.Create(channel).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := conn.Table("channels").Where("id = ?", legacy.ID).Updates(map[string]any{"review_status": "approved", "trust_level": "observing"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := conn.Model(&model.Channel{}).Where("id = ?", active.ID).Updates(map[string]any{"review_status": "approved", "trust_level": "observing"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := conn.Model(&model.Channel{}).Where("id = ?", unapproved.ID).Updates(map[string]any{"review_status": "pending", "trust_level": "observing"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := Migration035_ChannelObservation(conn); err != nil {
		t.Fatal(err)
	}
	var got model.Channel
	if err := conn.Model(&model.Channel{}).Where("channels.id = ?", legacy.ID).Take(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.TrustLevel != model.ChannelTrustObserving || got.ObservationStart == nil || got.ObservationUntil == nil {
		t.Fatalf("legacy observation missing: trust=%q start=%v until=%v", got.TrustLevel, got.ObservationStart, got.ObservationUntil)
	}
	got = model.Channel{}
	if err := conn.Model(&model.Channel{}).Where("channels.id = ?", active.ID).Take(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.TrustLevel != model.ChannelTrustObserving {
		t.Fatalf("active trust_level=%q", got.TrustLevel)
	}
	got = model.Channel{}
	if err := conn.Model(&model.Channel{}).Where("channels.id = ?", unapproved.ID).Take(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.TrustLevel == model.ChannelTrustTrusted {
		t.Fatal("unapproved channel was promoted")
	}
}

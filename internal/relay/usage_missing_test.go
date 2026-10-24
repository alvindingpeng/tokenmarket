package relay

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
)

// usageMissingTestDB 独立 sqlite + 干净缓存: 与 relayRetryTestDB 同风格, 但不挂限流器。
func usageMissingTestDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "usage_missing.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := op.SettingUpsert(model.SettingKeyMarkupRatio, "0"); err != nil {
		t.Fatalf("seed markup: %v", err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatalf("init cache: %v", err)
	}
}

func TestSucceededRequestWithoutUsageRaisesAlertAndReleases(t *testing.T) {
	usageMissingTestDB(t)
	user := model.User{Username: "usage-missing", Password: "secret", Role: model.RoleUser, Status: model.StatusActive, Balance: 1}
	if err := db.GetDB().Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	request := newRequestState(context.Background(), "gpt-test", "", 1, model.ProtocolOpenAIChatCompletion, "", 0, user.ID, "127.0.0.1", "", 60)
	reservationID, err := op.BillingReserve(user.ID, request.ID, 0.01)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	request.SetReservation(reservationID)
	request.targetKind = model.MediaKindText
	request.targetSupplyPrice = model.LLMPrice{Input: 1, Output: 1}

	request.markSucceeded("", nil)

	// 预扣必须被释放(成功但无用量不发明计费), 且留了告警痕迹。
	var released model.BalanceReservation
	if err := db.GetDB().First(&released, reservationID).Error; err != nil {
		t.Fatalf("load reservation: %v", err)
	}
	if !released.Settled {
		t.Fatal("reservation should be finalized after release")
	}
	var alerts int64
	if err := db.GetDB().Model(&model.ChannelAlert{}).Where("kind = ?", "usage_missing").Count(&alerts).Error; err != nil {
		t.Fatalf("count alerts: %v", err)
	}
	if alerts == 0 {
		t.Fatal("succeeded request without usage must raise usage_missing alert")
	}
}

func TestFailedRequestWithoutUsageStaysSilent(t *testing.T) {
	usageMissingTestDB(t)
	user := model.User{Username: "usage-failed", Password: "secret", Role: model.RoleUser, Status: model.StatusActive, Balance: 1}
	if err := db.GetDB().Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	request := newRequestState(context.Background(), "gpt-test", "", 1, model.ProtocolOpenAIChatCompletion, "", 0, user.ID, "127.0.0.1", "", 60)
	reservationID, err := op.BillingReserve(user.ID, request.ID, 0.01)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	request.SetReservation(reservationID)
	request.targetKind = model.MediaKindText

	request.markFailed(context.DeadlineExceeded, "", nil)

	// 失败且无用量是常态路径(上游没处理就断开), 不应制造告警噪音。
	var alerts int64
	if err := db.GetDB().Model(&model.ChannelAlert{}).Where("kind = ?", "usage_missing").Count(&alerts).Error; err != nil {
		t.Fatalf("count alerts: %v", err)
	}
	if alerts != 0 {
		t.Fatal("failed request without usage must not raise usage_missing alert")
	}
}

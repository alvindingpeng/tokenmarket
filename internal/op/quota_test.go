package op

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

func quotaTestSetup(t *testing.T) model.APIKey {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "quota.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	key := model.APIKey{Name: "quota", APIKey: "quota-test", QuotaRequests: 10, QuotaInputTokens: 100, QuotaOutputTokens: 50}
	if err := APIKeyCreate(&key, context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { apiKeyCache.Del(key.ID); apiKeyIDMap.Del(key.APIKey) })
	return key
}

func TestAPIKeyQuotaUsesPersistedHourlySchema(t *testing.T) {
	key := quotaTestSetup(t)
	ctx := context.Background()
	start := apiKeyQuotaResetTime(time.Now(), 0)
	rows := []model.RateLimitUsageHourly{
		{ScopeType: model.RateScopeAPIKey, ScopeID: key.ID, Hour: start.Add(-time.Hour), Requests: 1000},
		{ScopeType: model.RateScopeUser, ScopeID: key.ID, Hour: start, Requests: 1000},
		{ScopeType: model.RateScopeAPIKey, ScopeID: key.ID + 1, Hour: start, Requests: 1000},
		{ScopeType: model.RateScopeAPIKey, ScopeID: key.ID, Hour: start, Requests: 4, InputTokens: 30, OutputTokens: 10},
		{ScopeType: model.RateScopeAPIKey, ScopeID: key.ID, Hour: start.Add(time.Hour), Requests: 5, InputTokens: 40, OutputTokens: 15},
	}
	if err := db.GetDB().Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	r, i, o, err := GetAPIKeyQuotaUsage(ctx, key.ID)
	if err != nil || r != 9 || i != 70 || o != 25 {
		t.Fatalf("usage=%d/%d/%d err=%v", r, i, o, err)
	}
	if err := CheckAPIKeyQuota(ctx, key.ID); err != nil {
		t.Fatal(err)
	}
	key.QuotaRequests = 9
	apiKeyCache.Set(key.ID, key)
	var exceeded QuotaExceeded
	if err := CheckAPIKeyQuota(ctx, key.ID); !errors.As(err, &exceeded) || exceeded.Kind != "requests" {
		t.Fatalf("expected request limit, got %v", err)
	}
	key.QuotaRequests = 0
	key.QuotaInputTokens = 70
	apiKeyCache.Set(key.ID, key)
	if err := CheckAPIKeyQuota(ctx, key.ID); !errors.As(err, &exceeded) || exceeded.Kind != "input_tokens" {
		t.Fatalf("expected input limit, got %v", err)
	}
	key.QuotaInputTokens = 0
	key.QuotaOutputTokens = 25
	apiKeyCache.Set(key.ID, key)
	if err := CheckAPIKeyQuota(ctx, key.ID); !errors.As(err, &exceeded) || exceeded.Kind != "output_tokens" {
		t.Fatalf("expected output limit, got %v", err)
	}
}

func TestAPIKeyQuotaEmptyUnlimitedAndDatabaseErrors(t *testing.T) {
	key := quotaTestSetup(t)
	ctx := context.Background()
	r, i, o, err := GetAPIKeyQuotaUsage(ctx, key.ID)
	if err != nil || r != 0 || i != 0 || o != 0 {
		t.Fatalf("empty usage: %d/%d/%d %v", r, i, o, err)
	}
	if err := CheckAPIKeyQuota(ctx, key.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().Migrator().DropTable(&model.RateLimitUsageHourly{}); err != nil {
		t.Fatal(err)
	}
	if err := CheckAPIKeyQuota(ctx, key.ID); err == nil {
		t.Fatal("database error must not be treated as zero usage")
	}
	if _, _, _, err := GetAPIKeyQuotaUsage(ctx, key.ID); err == nil {
		t.Fatal("usage database error not returned")
	}
	key.QuotaRequests = 0
	key.QuotaInputTokens = 0
	key.QuotaOutputTokens = 0
	apiKeyCache.Set(key.ID, key)
	if err := CheckAPIKeyQuota(ctx, key.ID); err != nil {
		t.Fatalf("unlimited key should bypass usage query: %v", err)
	}
	if err := CheckAPIKeyQuota(ctx, key.ID+100); err == nil {
		t.Fatal("missing key accepted")
	}
}

func TestAPIKeyQuotaCustomResetFiltersTimeValues(t *testing.T) {
	key := quotaTestSetup(t)
	zone := time.FixedZone("UTC+8", 8*60*60)
	reset := apiKeyQuotaResetTime(time.Date(2026, 1, 2, 7, 0, 0, 0, zone), 8)
	rows := []model.RateLimitUsageHourly{
		{ScopeType: model.RateScopeAPIKey, ScopeID: key.ID, Hour: reset.Add(-time.Hour), Requests: 99},
		{ScopeType: model.RateScopeAPIKey, ScopeID: key.ID, Hour: reset, Requests: 3, InputTokens: 7},
		{ScopeType: model.RateScopeAPIKey, ScopeID: key.ID, Hour: reset.Add(time.Hour), Requests: 4, OutputTokens: 8},
	}
	if err := db.GetDB().Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	got, err := loadAPIKeyQuotaUsage(context.Background(), key.ID, reset)
	if err != nil || got.Requests != 7 || got.InputTokens != 7 || got.OutputTokens != 8 {
		t.Fatalf("custom reset usage=%+v err=%v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := loadAPIKeyQuotaUsage(ctx, key.ID, reset); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled query error=%v", err)
	}
}

func TestAPIKeyQuotaResetCalendarBoundary(t *testing.T) {
	zone := time.FixedZone("UTC+8", 8*60*60)
	for _, tc := range []struct {
		name string
		now  time.Time
		hour int
		want time.Time
	}{
		{"before reset", time.Date(2026, 1, 2, 3, 0, 0, 0, zone), 8, time.Date(2026, 1, 1, 8, 0, 0, 0, zone)},
		{"at reset", time.Date(2026, 1, 2, 8, 0, 0, 0, zone), 8, time.Date(2026, 1, 2, 8, 0, 0, 0, zone)},
		{"invalid reset", time.Date(2026, 1, 2, 8, 0, 0, 0, zone), 24, time.Date(2026, 1, 2, 0, 0, 0, 0, zone)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := apiKeyQuotaResetTime(tc.now, tc.hour); !got.Equal(tc.want) {
				t.Fatalf("reset=%v want=%v", got, tc.want)
			}
		})
	}
	zoneDST, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 9, 0, 30, 0, 0, zoneDST)
	want := time.Date(2026, 3, 8, 8, 0, 0, 0, zoneDST)
	if got := apiKeyQuotaResetTime(now, 8); !got.Equal(want) {
		t.Fatalf("DST reset=%v want=%v", got, want)
	}
}

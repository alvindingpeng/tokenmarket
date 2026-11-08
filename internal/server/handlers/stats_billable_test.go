package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/gin-gonic/gin"
)

// TestUserBillableStatsMergesCacheTokensIntoInput 验证非管理员统计把计费明细的
// 缓存读/写词元并入输入桶: 总词元 = 毛输入 + 输出, 与管理员侧 usageMetrics 同口径,
// 避免首页"总词元"漏记缓存命中请求。计费行只存毛输入, 净读500 + 缓存读300 + 缓存写200 = 1000。
func TestUserBillableStatsMergesCacheTokensIntoInput(t *testing.T) {
	_, _, _, _, user, _, _, _ := ticketAPIEnv(t)
	seedBillableFixtures(t, user.ID, time.Now())

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/stats/total", nil)
	c.Set("user_id", int(user.ID))
	c.Set("role", string(model.RoleUser))

	daily, hourly, total, err := userBillableStats(c)
	if err != nil {
		t.Fatalf("userBillableStats: %v", err)
	}
	// 逐维求和: 全部日行、全部小时行与总量都必须等于同一份账目。
	sum := func(rows []model.StatsMetrics) (input, output, success int64) {
		for _, r := range rows {
			input += r.InputToken
			output += r.OutputToken
			success += r.RequestSuccess
		}
		return
	}
	dInput, dOutput, dSuccess := sum(metricsOfDaily(daily))
	hInput, hOutput, hSuccess := sum(metricsOfHourly(hourly))
	checks := []struct {
		name                   string
		input, output, success int64
	}{
		{"daily", dInput, dOutput, dSuccess},
		{"hourly", hInput, hOutput, hSuccess},
		{"total", total.InputToken, total.OutputToken, total.RequestSuccess},
	}
	for _, ck := range checks {
		if ck.input != 1000 || ck.output != 500 || ck.success != 1 {
			t.Fatalf("%s: want input=1000(500+300+200) output=500 success=1, got %d/%d/%d", ck.name, ck.input, ck.output, ck.success)
		}
	}
}

func metricsOfDaily(rows []model.StatsDaily) []model.StatsMetrics {
	out := make([]model.StatsMetrics, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.StatsMetrics)
	}
	return out
}

func metricsOfHourly(rows []model.StatsHourly) []model.StatsMetrics {
	out := make([]model.StatsMetrics, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.StatsMetrics)
	}
	return out
}

// seedBillableFixtures 写一条含缓存读写的计费明细(毛输入1000 = 净读500 + 缓存读300 + 缓存写200)。
func seedBillableFixtures(t *testing.T, userID uint, at time.Time) {
	t.Helper()
	record := model.BillingRecord{
		UserID:          userID,
		ModelName:       "billable-stats-model",
		InputToken:      500,
		OutputToken:     500,
		CacheReadToken:  300,
		CacheWriteToken: 200,
		UserCost:        0.01,
		OwnerRevenue:    0.004,
		PlatformRevenue: 0.006,
		Status:          model.BillingStatusSettled,
	}
	record.CreatedAt = at
	if err := db.GetDB().Create(&record).Error; err != nil {
		t.Fatalf("seed billing record: %v", err)
	}
}

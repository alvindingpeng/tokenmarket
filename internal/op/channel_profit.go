package op

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"gorm.io/gorm"
)

var (
	// ErrChannelProfitChannelNotFound 渠道不存在: 与其它"按主键查不到"的路径一样收敛成同一个哨兵,
	// 让 HTTP 层能稳定映射 404, 而不必解析底层错误文案。
	ErrChannelProfitChannelNotFound = errors.New("channel not found")
	// ErrChannelProfitDateRange 日期范围非法: 格式错误或开始晚于结束。
	ErrChannelProfitDateRange = errors.New("invalid channel profit date range")
)

// channelProfitDateLayout 接口约定的日期格式, 与前端筛选控件一致。
const channelProfitDateLayout = "2006-01-02"

// channelProfitDefaultDays 未给日期时的默认窗口: 最近 30 天(含今天)。
const channelProfitDefaultDays = 30

// channelProfitMaxDays 单次查询的区间上限, 含首尾共 366 天。
// 不加上限时管理员一次误选十年就是一次超大范围的索引扫描, 聚合会长时间占用数据库;
// 366 天足够覆盖年度对账, 更长的历史请分段查询。
const channelProfitMaxDays = 366

// ChannelProfit 汇总指定渠道在区间内的结算账目, 供管理员做渠道利润分析。
// 只读: 直接按 billing_records 的结算快照求和, 不重新定价, 也不回写任何账务,
// 因此不会与 settlement/reconcile 的口径打架。
// 口径: 收入=user_cost, 成本=owner_revenue, 毛利=platform_revenue(结算时已对平)。
// 只统计 status=settled 的行: refunded 行表示资金已退回, 计入会虚增收入与毛利。
func ChannelProfit(ctx context.Context, channelID int, startDate, endDate string) (model.ChannelProfit, error) {
	// 主键非正数不可能是合法渠道, 直接按不存在处理, 不必查库。
	if channelID <= 0 {
		return model.ChannelProfit{}, ErrChannelProfitChannelNotFound
	}
	// 先确认渠道存在: 否则一个不存在的渠道会安静地返回全 0, 前端无法区分"没数据"和"没这个渠道"。
	// 这里直读库而不走 channelCache: 统计接口不参与转发路径, 不该因为缓存未预热而误报 404。
	var channel model.Channel
	err := db.GetDB().WithContext(ctx).Select("id", "name").First(&channel, channelID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.ChannelProfit{}, ErrChannelProfitChannelNotFound
		}
		return model.ChannelProfit{}, fmt.Errorf("failed to load channel %d: %w", channelID, err)
	}

	start, end, startLabel, endLabel, err := channelProfitDateRange(startDate, endDate)
	if err != nil {
		return model.ChannelProfit{}, err
	}

	// 按天聚合并按渠道过滤: channel_id 是账单落库时的渠道快照, 天然把其它渠道的行隔离开。
	// 不按 owner_id 过滤: 渠道易主后历史账单仍记结算当时的归属者, 用当前归属过滤会漏掉历史收入。
	// 日期归桶复用 statsBucketExpr, 与用户/渠道统计保持同一套跨方言口径(见该函数的时区说明)。
	dateExpr := statsBucketExpr(statsDialect(), "day")
	var rows []struct {
		Date            string
		Requests        int64
		UserCost        float64
		OwnerRevenue    float64
		PlatformRevenue float64
	}
	if err := db.GetDB().WithContext(ctx).
		Model(&model.BillingRecord{}).
		Select(fmt.Sprintf(`
			%s as date,
			COUNT(*) as requests,
			COALESCE(SUM(user_cost), 0) as user_cost,
			COALESCE(SUM(owner_revenue), 0) as owner_revenue,
			COALESCE(SUM(platform_revenue), 0) as platform_revenue
		`, dateExpr)).
		Where("channel_id = ? AND status = ? AND created_at >= ? AND created_at < ?",
			channelID, model.BillingStatusSettled, start, end).
		Group(dateExpr).
		Order("date ASC").
		Scan(&rows).Error; err != nil {
		return model.ChannelProfit{}, fmt.Errorf("failed to query channel profit: %w", err)
	}

	// daily 恒为数组: 无数据时是 [] 而不是 null, 前端不必为空结果单独兜底。
	result := model.ChannelProfit{
		ChannelID:   channelID,
		ChannelName: channel.Name,
		StartDate:   startLabel,
		EndDate:     endLabel,
		Daily:       make([]model.ChannelProfitDaily, 0, len(rows)),
	}
	for _, row := range rows {
		result.Daily = append(result.Daily, model.ChannelProfitDaily{
			Date:                row.Date,
			ChannelProfitTotals: channelProfitTotals(row.Requests, row.UserCost, row.OwnerRevenue, row.PlatformRevenue),
		})
	}
	// 汇总由逐日结果累加而来, 保证 totals 与 daily 严丝合缝, 不会出现两套查询对不上的情况。
	for _, day := range result.Daily {
		result.Totals.Requests += day.Requests
		result.Totals.UserCost += day.UserCost
		result.Totals.OwnerRevenue += day.OwnerRevenue
		result.Totals.PlatformRevenue += day.PlatformRevenue
	}
	result.Totals.MarginRate = channelProfitMarginRate(result.Totals.UserCost, result.Totals.PlatformRevenue)
	return result, nil
}

// channelProfitTotals 组装单行口径; 金额保持原始精度, 不在这里做分位舍入。
func channelProfitTotals(requests int64, userCost, ownerRevenue, platformRevenue float64) model.ChannelProfitTotals {
	return model.ChannelProfitTotals{
		Requests:        requests,
		UserCost:        userCost,
		OwnerRevenue:    ownerRevenue,
		PlatformRevenue: platformRevenue,
		MarginRate:      channelProfitMarginRate(userCost, platformRevenue),
	}
}

// channelProfitMarginRate 毛利率百分比; 收入为 0 时记 0, 避免除零把空区间变成 NaN/Inf。
func channelProfitMarginRate(revenue, profit float64) float64 {
	if revenue == 0 {
		return 0
	}
	return profit / revenue * 100
}

// channelProfitDateRange 把调用方给的日期解析成 [start, end+1day) 的时间边界。
// 结束日按整天算, 用次日 00:00 做开区间上界, 免得依赖 23:59:59 这种精度假设。
// 缺省窗口为最近 30 天(含今天); 只给一端时另一端跟着推, 保证单边参数也能得到有意义的区间。
// 区间长度受 channelProfitMaxDays 约束, 超限按参数错误处理。
func channelProfitDateRange(startDate, endDate string) (time.Time, time.Time, string, string, error) {
	now := time.Now()
	endDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	startDay := endDay.AddDate(0, 0, -(channelProfitDefaultDays - 1))

	if startDate != "" {
		parsed, err := time.ParseInLocation(channelProfitDateLayout, startDate, time.Local)
		if err != nil {
			return time.Time{}, time.Time{}, "", "", ErrChannelProfitDateRange
		}
		startDay = parsed
	}
	if endDate != "" {
		parsed, err := time.ParseInLocation(channelProfitDateLayout, endDate, time.Local)
		if err != nil {
			return time.Time{}, time.Time{}, "", "", ErrChannelProfitDateRange
		}
		endDay = parsed
	}
	if startDay.After(endDay) {
		return time.Time{}, time.Time{}, "", "", ErrChannelProfitDateRange
	}
	// 用日历加法而不是小时数相减, 避免夏令时把某一天算成 23/25 小时导致边界误判。
	if endDay.After(startDay.AddDate(0, 0, channelProfitMaxDays-1)) {
		return time.Time{}, time.Time{}, "", "", ErrChannelProfitDateRange
	}
	return startDay, endDay.AddDate(0, 0, 1),
		startDay.Format(channelProfitDateLayout), endDay.Format(channelProfitDateLayout), nil
}

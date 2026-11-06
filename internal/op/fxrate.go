package op

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync/atomic"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// 汇率服务: 账面币种(USD)与渠道报价币种之间的折算依据。
//
// 数据流: 每日任务 TaskFxRateUpdate 调 FxRateRefresh 从牌价源拉全量牌价 -> 落 fx_rates
// (采集日 + 币种对, 唯一索引幂等) -> 结算经 FxRateConvert 读快照折算。
//
// 为什么必须落库而不只放内存: billing_records.fx_rate 要能回答"这笔账当时按多少折的",
// 快照表就是答案, 也是事后复核的唯一依据。
// 为什么读取走内存缓存: 折算在每次结算的路径上, 每请求查库会让计费链多一次 I/O;
// 牌价一天只变一次, 缓存到下次刷新即可, 缓存未命中时回落到查库。
// 为什么断源不停计费: 牌价源挂掉时继续用最近一天的旧价并让调用方看到 day(据此告警),
// 折算停摆等于全站不能用, 比"用昨天的价"严重得多。

// fxRateFetchTimeout 单次牌价拉取的 HTTP 超时。
const fxRateFetchTimeout = 15 * time.Second

// fxRateSourceURL 免费无鉴权的牌价源, 以 USD 为基准返回全量牌价。
// 失效时改这个常量并核对返回形状(见 FxRateRefresh), 其余链路不动。
const fxRateSourceURL = "https://open.er-api.com/v6/latest/USD"

const fxRateSourceName = "open.er-api.com"

// FxRateSourceName 牌价来源标识, 任务日志与运维展示用。
func FxRateSourceName() string { return fxRateSourceName }

// fxSnapshot 一份内存牌价快照: quote 币种 -> 1 USD 折多少该币种。
type fxSnapshot struct {
	rates map[string]float64
	day   string // 采集日 YYYYMMDD; 空表示尚无任何牌价。
}

var fxCache atomic.Pointer[fxSnapshot]

// SupportedQuoteCurrencies 参与采集的报价币种 = 供货价白名单去掉基准币种 USD。
func SupportedQuoteCurrencies() []string {
	return []string{"CNY", "EUR", "GBP", "JPY", "HKD", "SGD"}
}

// FxRateRefresh 拉取牌价并落库, 同时刷新内存快照。
// 返回本次写入的币种数; 网络失败返回错误且**不动**已有快照 —— 宁可继续用旧价,
// 也不能把空表当成"汇率为 0"写进去。
func FxRateRefresh(ctx context.Context) (int, error) {
	rates, err := fxRateFetch(ctx)
	if err != nil {
		return 0, err
	}
	day := time.Now().Format("20060102")
	now := time.Now()
	written := 0
	for _, quote := range SupportedQuoteCurrencies() {
		rate, ok := rates[quote]
		if !ok {
			continue
		}
		if err := model.ValidateFxRate(rate); err != nil {
			return written, fmt.Errorf("fx rate %s: %w", quote, err)
		}
		// 同一天重复采集只更新数值(牌价源日内可能微调), 靠唯一索引 (from,to,day) 幂等。
		row := model.FxRate{From: model.LedgerCurrency, To: quote, Day: day, Rate: rate, Source: fxRateSourceName}
		if err := db.GetDB().WithContext(ctx).
			Where("`from` = ? AND `to` = ? AND `day` = ?", row.From, row.To, row.Day).
			Assign(map[string]any{"rate": row.Rate, "source": row.Source, "created_at": now}).
			FirstOrCreate(&model.FxRate{}, row).Error; err != nil {
			return written, fmt.Errorf("persist fx rate %s: %w", quote, err)
		}
		written++
	}
	if written == 0 {
		return 0, fmt.Errorf("fx source returned no supported currencies")
	}
	fxCache.Store(&fxSnapshot{rates: rates, day: day})
	return written, nil
}

// fxRateFetch 从牌价源取全量牌价, 只保留我们支持的币种。
// 返回形状: {"result":"success","rates":{"CNY":7.1,...}} —— 只认 result=success, 否则视为失败,
// 避免把错误页解析成"没有币种"从而静默跳过刷新。
func fxRateFetch(ctx context.Context) (map[string]float64, error) {
	reqCtx, cancel := context.WithTimeout(ctx, fxRateFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, fxRateSourceURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch fx rates: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch fx rates: unexpected status %d", resp.StatusCode)
	}
	// 牌价响应很小(全量约 30KB), 限读防异常大响应。
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read fx rates: %w", err)
	}
	var payload struct {
		Result string             `json:"result"`
		Rates  map[string]float64 `json:"rates"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode fx rates: %w", err)
	}
	if payload.Result != "success" {
		return nil, fmt.Errorf("fx source returned result=%q", payload.Result)
	}
	out := make(map[string]float64, len(SupportedQuoteCurrencies()))
	for _, quote := range SupportedQuoteCurrencies() {
		if rate, ok := payload.Rates[quote]; ok {
			out[quote] = rate
		}
	}
	return out, nil
}

// FxRateLookup 返回 1 单位 from 折多少 to, 以及所用牌价的采集日。
// 支持两种方向: from/to 有一端是账面币种 USD; 两端都不是时按 USD 交叉(decimal 精度足够:
// 交叉只是两次除法, 相较结算金额的量级误差远小于 1e-6 的存储精度)。
func FxRateLookup(ctx context.Context, from, to string) (rate float64, day string, err error) {
	from, to = model.NormalizeCurrency(from), model.NormalizeCurrency(to)
	if from == to {
		return 1, time.Now().Format("20060102"), nil
	}
	snap, err := fxSnapshotLoad(ctx)
	if err != nil {
		return 0, "", err
	}
	if snap == nil || len(snap.rates) == 0 {
		return 0, "", fmt.Errorf("no fx rate available for %s->%s", from, to)
	}
	// rates 的口径是"1 USD 折多少 quote"; 由此推出任意方向的折算系数。
	usdToFrom, usdToTo := 1.0, 1.0
	if from != model.LedgerCurrency {
		v, ok := snap.rates[from]
		if !ok {
			return 0, "", fmt.Errorf("no fx rate for %s", from)
		}
		usdToFrom = v
	}
	if to != model.LedgerCurrency {
		v, ok := snap.rates[to]
		if !ok {
			return 0, "", fmt.Errorf("no fx rate for %s", to)
		}
		usdToTo = v
	}
	// 1 from = (1/usdToFrom) USD = usdToTo/usdToFrom 单位 to。
	return usdToTo / usdToFrom, snap.day, nil
}

// FxRateConvert 把 amount(原币)折算成账面币种(USD), 同时返回所用汇率与牌价日。
// from 已是账面币种时零折算(rate=1), 这是绝大多数模型的价格路径, 不查表也不引入误差。
func FxRateConvert(ctx context.Context, from string, amount float64) (converted float64, rate float64, day string, err error) {
	from = model.NormalizeCurrency(from)
	if from == model.LedgerCurrency {
		return amount, 1, time.Now().Format("20060102"), nil
	}
	rate, day, err = FxRateLookup(ctx, from, model.LedgerCurrency)
	if err != nil {
		return 0, 0, "", err
	}
	return amount * rate, rate, day, nil
}

// fxSnapshotLoad 取内存快照; 未命中时从库里装载最近一天的牌价。
// 装载失败返回错误而不是空快照: 让调用方走"折算不可用"的分支, 而不是按 1 折算。
func fxSnapshotLoad(ctx context.Context) (*fxSnapshot, error) {
	if snap := fxCache.Load(); snap != nil && len(snap.rates) > 0 {
		return snap, nil
	}
	gormDB := db.GetDB()
	if gormDB == nil {
		return nil, nil
	}
	var rows []model.FxRate
	if err := gormDB.WithContext(ctx).
		Where("`from` = ?", model.LedgerCurrency).
		Order("day DESC").Limit(64).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load fx rates: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	// 取最新采集日, 只收当天的行: 混用不同日期的币种会让 snapshot 的 day 失去意义。
	latest := rows[0].Day
	rates := make(map[string]float64, len(rows))
	for _, row := range rows {
		if row.Day != latest {
			continue
		}
		rates[row.To] = row.Rate
	}
	snap := &fxSnapshot{rates: rates, day: latest}
	fxCache.Store(snap)
	return snap, nil
}

// FxRateSnapshotView 返回当前生效的牌价快照(按币种排序), 供运维页展示与人工核对。
func FxRateSnapshotView(ctx context.Context) (day string, items []model.FxRate, err error) {
	snap, err := fxSnapshotLoad(ctx)
	if err != nil {
		return "", nil, err
	}
	if snap == nil {
		return "", []model.FxRate{}, nil
	}
	quotes := make([]string, 0, len(snap.rates))
	for quote := range snap.rates {
		quotes = append(quotes, quote)
	}
	sort.Strings(quotes)
	items = make([]model.FxRate, 0, len(quotes))
	for _, quote := range quotes {
		items = append(items, model.FxRate{From: model.LedgerCurrency, To: quote, Day: snap.day, Rate: snap.rates[quote], Source: fxRateSourceName})
	}
	return snap.day, items, nil
}

// FxRateCacheReset 清空内存快照(测试与人工强制重载用)。
func FxRateCacheReset() { fxCache.Store(nil) }

// FxRateMaxAgeDays 牌价可接受的最大日龄: 超过视为陈旧, 折算误差不可忽略, 要告警。
// 牌价源每日更新, 2 天 = 昨天采集失败且今天也失败, 已属异常。
const FxRateMaxAgeDays = 2

// FxRateStalenessCheck 返回当前快照的日龄(天); 无任何牌价返回 -1。
// 供任务在刷新后自检: 刷新失败 + 日龄超限 = 连续采集中断, 是计费正确性风险。
func FxRateStalenessCheck(ctx context.Context) (int, error) {
	snap, err := fxSnapshotLoad(ctx)
	if err != nil {
		return -1, err
	}
	if snap == nil || snap.day == "" {
		return -1, nil
	}
	dayTime, err := time.ParseInLocation("20060102", snap.day, time.Local)
	if err != nil {
		return -1, fmt.Errorf("parse fx day %q: %w", snap.day, err)
	}
	return int(time.Since(dayTime).Hours() / 24), nil
}

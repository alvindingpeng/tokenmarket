package op

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alvindingpeng/switchyard/internal/alert"
	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/charmbracelet/log"
	"gorm.io/gorm"
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
//
// 多源故障切换: 按序尝试 fxRateFetchers, 直到某源返回覆盖全部报价币种且通过校验的批;
// 全部失败则返回错误且不动快照。实际成功源记录在 fxLastSource, 落库 Source 与
// FxRateSourceName 均反映最近一次成功源。

// fxRateFetchTimeout 单次牌价拉取的 HTTP 超时。
const fxRateFetchTimeout = 15 * time.Second

// fxRateSourceURL 免费无鉴权的主牌价源, 以 USD 为基准返回全量牌价。
const fxRateSourceURL = "https://open.er-api.com/v6/latest/USD"

// fxRateSourceName 主源标识, 保留常量供从未成功刷新时的默认展示。
const fxRateSourceName = "open.er-api.com"

const fxRateBackupSourceName = "frankfurter.app"

const fxRateBackupSourceURL = "https://api.frankfurter.app/latest?from=USD&to="

// fxRateTertiarySourceName 第三备源: frankfurter 不支持 CNY/THB 会被 fxRatesCoverAll
// 必拒(等于主源挂掉时没有真备援), 这个源覆盖全部 11 个报价币种才是有效的最后防线。
const fxRateTertiarySourceName = "frankfurter.dev"

// Frankfurter 的新 v1 API 覆盖 CNY、THB 与全量报价币种, 老 api.frankfurter.app
// 仍缺币种。新域经实测返回 200 和完整 rates。
const fxRateTertiarySourceURL = "https://api.frankfurter.dev/v1/latest?base=USD&symbols="

// FxRateMaxSingleRefreshChange is the largest relative change accepted from
// the current snapshot in one refresh. A larger move is treated as an
// upstream/unit error and the entire refresh is rejected.
const FxRateMaxSingleRefreshChange = 0.25

// fxSnapshot 一份内存牌价快照: quote 币种 -> 1 USD 折多少该币种。
type fxSnapshot struct {
	rates map[string]float64
	day   string // 采集日 YYYYMMDD; 空表示尚无任何牌价。
}

var fxCache atomic.Pointer[fxSnapshot]

// fxRateFetcher 一个牌价源: 名称 + 取数函数。测试通过替换 fxRateFetchers 注入假源。
type fxRateFetcher struct {
	name  string
	fetch func(ctx context.Context) (map[string]float64, error)
}

// fxRateFetchers 按优先级排列的牌价源列表。var 而非 const 是为了测试可注入。
var fxRateFetchers = []fxRateFetcher{
	{name: fxRateSourceName, fetch: fxRateFetchOpenERAPI},
	{name: fxRateTertiarySourceName, fetch: fxRateFetchExchangeRateHost},
	{name: fxRateBackupSourceName, fetch: fxRateFetchFrankfurter},
}

// fxLastSource 最近一次成功刷新使用的源名; 从未成功时回落主源名。
var fxLastSource atomic.Value // string

func init() { fxLastSource.Store(fxRateSourceName) }

// FxRateSourceName 牌价来源标识(最近一次成功刷新的源), 任务日志与运维展示用。
func FxRateSourceName() string {
	if v, ok := fxLastSource.Load().(string); ok && v != "" {
		return v
	}
	return fxRateSourceName
}

// FxRefreshStatus is process-local observability; counters reset on restart.
type FxRefreshStatus struct {
	LastAttemptAt time.Time
	LastSuccessAt time.Time
	LastFailureAt time.Time
	LastError     string
	FailureCount  int
	Source        string // 最近一次成功刷新的源名。
}

var fxRefreshState struct {
	sync.RWMutex
	status FxRefreshStatus
}

// fxRefreshMu 串行化刷新: 周期任务与管理员手动刷新可能并发到达,
// FirstOrCreate 对同一 (from,to,day) 唯一行在并发下会撞 UNIQUE 约束。
var fxRefreshMu sync.Mutex

func FxRefreshStatusView() FxRefreshStatus {
	fxRefreshState.RLock()
	defer fxRefreshState.RUnlock()
	return fxRefreshState.status
}
func recordFxRefreshAttempt() {
	fxRefreshState.Lock()
	fxRefreshState.status.LastAttemptAt = time.Now()
	fxRefreshState.Unlock()
}
func recordFxRefreshFailure(source string, err error) {
	fxRefreshState.Lock()
	fxRefreshState.status.LastFailureAt = time.Now()
	fxRefreshState.status.LastError = fmt.Sprintf("%s: %s", source, err.Error())
	fxRefreshState.status.FailureCount++
	consecutive := fxRefreshState.status.FailureCount
	fxRefreshState.Unlock()
	// 连续 3 次失败发系统告警(采集全挂意味着结算折算将走向 unavailable 口径, 管理员要早知道);
	// RaiseAlertFacts 自带时间窗去重, 这里只负责在阈值处触发一次。
	if consecutive != fxRefreshAlertThreshold {
		return
	}
	RaiseAlertFacts(context.Background(), AlertFacts{
		Subject: "系统",
		Kind:    "fx_rate_refresh_failed",
		Reason:  fmt.Sprintf("fx rate refresh failed %d consecutive times", consecutive),
		Fields: []alert.Field{
			{Label: "最后尝试源", Value: source},
			{Label: "错误", Value: err.Error()},
		},
	})
	// 外发直达(alert.SendOnce, 与告警连通性测试同一组渠道): 站内去重按设置窗口, 这里
	// 另加 24h 进程内去重 —— 周期任务(默认 24h)与管理员手动刷新都会计入失败计数,
	// 没有这层的话同一次断源会被反复推送到群里。
	fxLastAlertMu.Lock()
	if !fxLastAlertAt.IsZero() && time.Since(fxLastAlertAt) < fxAlertOutboundDedupWindow {
		fxLastAlertMu.Unlock()
		return
	}
	fxLastAlertAt = time.Now()
	fxLastAlertMu.Unlock()
	channelsRaw, chErr := SettingGetString(model.SettingKeyAlertChannels)
	if chErr != nil || strings.TrimSpace(channelsRaw) == "" {
		channelsRaw = "webhook" // 与 alert.SendDetailed 的默认口径一致
	}
	for _, channel := range strings.Split(channelsRaw, ",") {
		channel = strings.TrimSpace(channel)
		if channel == "" {
			continue
		}
		sendCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		sendErr := alert.SendOnce(sendCtx, channel, alert.Alert{
			Event:     alert.EventFxRateRefreshFailed,
			Kind:      "fx_rate_refresh_failed",
			Scope:     "系统",
			Severity:  "warning",
			Title:     "汇率牌价采集连续失败",
			Message:   fmt.Sprintf("%s 连续 %d 次失败, 当前继续使用最近一天的牌价快照: %v", source, consecutive, err),
			Timestamp: time.Now(),
			Fields: []alert.Field{
				{Label: "失败源", Value: source},
				{Label: "连续失败", Value: strconv.Itoa(consecutive)},
				{Label: "最近错误", Value: fmt.Sprintf("%s: %v", source, err)},
			},
		})
		cancel()
		if sendErr != nil {
			log.Warnf("fx refresh alert via %s failed: %v", channel, sendErr)
		}
	}
}

// fxAlertOutboundDedupWindow 外发去重窗口: 24h 内同一断源至多推送一次。
const fxAlertOutboundDedupWindow = 24 * time.Hour

// fxLastAlertAt/fxLastAlertMu 上次外发时间(进程内, 重启复位; 复位无害, 失败计数同样从零计)。
var (
	fxLastAlertAt time.Time
	fxLastAlertMu sync.Mutex
)

// fxRefreshAlertThreshold 连续失败多少次触发一次系统告警; 成功一次即清零重新计数。
const fxRefreshAlertThreshold = 3

func recordFxRefreshSuccess(source string) {
	fxRefreshState.Lock()
	fxRefreshState.status.LastSuccessAt = time.Now()
	fxRefreshState.status.LastError = ""
	fxRefreshState.status.Source = source
	fxRefreshState.Unlock()
}

// SupportedQuoteCurrencies 参与采集的报价币种 = 供货价白名单去掉基准币种 USD。
func SupportedQuoteCurrencies() []string {
	return []string{"CNY", "EUR", "GBP", "JPY", "HKD", "SGD", "AUD", "CAD", "KRW", "CHF", "THB"}
}

// fxRatesCoverAll 判断 rates 是否覆盖全部报价币种且逐项通过校验。
func fxRatesCoverAll(rates map[string]float64) error {
	for _, quote := range SupportedQuoteCurrencies() {
		rate, ok := rates[quote]
		if !ok {
			return fmt.Errorf("fx source missing quote currency %s", quote)
		}
		if err := model.ValidateFxRate(rate); err != nil {
			return fmt.Errorf("fx rate %s: %w", quote, err)
		}
	}
	return nil
}

// FxRateRefresh 按序尝试各牌价源并落库, 同时刷新内存快照。
// 返回本次写入的币种数; 全部源失败返回错误且**不动**已有快照 —— 宁可继续用旧价,
// 也不能把空表当成"汇率为 0"写进去。
func FxRateRefresh(ctx context.Context) (int, error) {
	// 单飞: 任务调度与管理员手动刷新可能同时到达, 两个 goroutine 争同一个
	// (from,to,day) 唯一行时 FirstOrCreate 会撞 UNIQUE 约束(观察到的生产告警),
	// 串行化让后来者直接复用先到者的结果语义, 而不是把并发当成采集失败计数。
	fxRefreshMu.Lock()
	defer fxRefreshMu.Unlock()
	recordFxRefreshAttempt()
	var lastErr error
	for _, src := range fxRateFetchers {
		rates, err := src.fetch(ctx)
		if err == nil {
			err = fxRatesCoverAll(rates)
		}
		if err != nil {
			lastErr = err
			recordFxRefreshFailure(src.name, err)
			continue
		}
		written, err := fxRateRefreshWithRates(ctx, src.name, rates)
		if err != nil {
			lastErr = err
			recordFxRefreshFailure(src.name, err)
			continue
		}
		fxLastSource.Store(src.name)
		recordFxRefreshSuccess(src.name)
		return written, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no fx rate fetchers configured")
	}
	return 0, lastErr
}

func fxRateRefreshWithRates(ctx context.Context, source string, rates map[string]float64) (int, error) {
	// Validate the full batch before writing anything, so a bad quote cannot
	// leave the database partially updated or replace the in-memory snapshot.
	for _, quote := range SupportedQuoteCurrencies() {
		if rate, ok := rates[quote]; ok {
			if err := model.ValidateFxRate(rate); err != nil {
				return 0, fmt.Errorf("fx rate %s: %w", quote, err)
			}
		}
	}
	if len(rates) == 0 {
		return 0, fmt.Errorf("fx source returned no supported currencies")
	}
	current, err := fxSnapshotLoad(ctx)
	if err != nil {
		return 0, err
	}
	if current != nil {
		for quote, rate := range rates {
			previous, ok := current.rates[quote]
			if !ok || previous <= 0 {
				continue
			}
			change := rate/previous - 1
			if change > FxRateMaxSingleRefreshChange || change < -FxRateMaxSingleRefreshChange {
				return 0, fmt.Errorf("fx rate %s changed %.1f%% from current snapshot; limit is %.0f%%", quote, change*100, FxRateMaxSingleRefreshChange*100)
			}
		}
	}
	day := time.Now().Format("20060102")
	now := time.Now()
	written := 0
	for _, quote := range SupportedQuoteCurrencies() {
		rate, ok := rates[quote]
		if !ok {
			continue
		}
		// 同一天重复采集只更新数值(牌价源日内可能微调), 靠唯一索引 (from,to,day) 幂等。
		// 注意: FirstOrCreate 不能带整行 struct 作条件 —— rate/source 在日内会变,
		// 用它们查询永远不命中, 随后 INSERT 就撞唯一索引(生产观察到的告警根因)。
		// 所以条件只给唯一索引三键, 数值经 Assign 更新。
		row := model.FxRate{From: model.LedgerCurrency, To: quote, Day: day, Rate: rate, Source: source}
		var existing model.FxRate
		err := db.GetDB().WithContext(ctx).
			Where("`from` = ? AND `to` = ? AND `day` = ?", row.From, row.To, row.Day).
			First(&existing).Error
		switch {
		case err == nil:
			if existing.Rate != row.Rate || existing.Source != row.Source {
				if err := db.GetDB().WithContext(ctx).Model(&existing).
					Updates(map[string]any{"rate": row.Rate, "source": row.Source, "created_at": now}).Error; err != nil {
					return written, fmt.Errorf("update fx rate %s: %w", quote, err)
				}
			}
		case errors.Is(err, gorm.ErrRecordNotFound):
			if err := db.GetDB().WithContext(ctx).Create(&row).Error; err != nil {
				return written, fmt.Errorf("create fx rate %s: %w", quote, err)
			}
		default:
			return written, fmt.Errorf("query fx rate %s: %w", quote, err)
		}
		written++
	}
	if written == 0 {
		return 0, fmt.Errorf("fx source returned no supported currencies")
	}
	fxCache.Store(&fxSnapshot{rates: rates, day: day})
	return written, nil
}

// fxRateFetchOpenERAPI 主源 open.er-api.com: 以 USD 为基准返回全量牌价。
// 返回形状: {"result":"success","rates":{"CNY":7.1,...}} —— 只认 result=success, 否则视为失败,
// 避免把错误页解析成"没有币种"从而静默跳过刷新。
func fxRateFetchOpenERAPI(ctx context.Context) (map[string]float64, error) {
	body, err := fxRateHTTPGet(ctx, fxRateSourceURL)
	if err != nil {
		return nil, err
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

// fxRateFetchFrankfurter 老域兼容备用源; 当前服务端对 CNY/THB 缺项, cover 校验会拒绝。
func fxRateFetchFrankfurter(ctx context.Context) (map[string]float64, error) {
	return fxRateFetchFromURL(ctx, fxRateBackupSourceURL+joinQuotes(SupportedQuoteCurrencies()))
}

// fxRateFetchExchangeRateHost 这里使用 Frankfurter 新 v1 API: 实测覆盖所有 11 个报价币种。
func fxRateFetchExchangeRateHost(ctx context.Context) (map[string]float64, error) {
	return fxRateFetchFromURL(ctx, fxRateTertiarySourceURL+joinQuotes(SupportedQuoteCurrencies()))
}

func fxRateFetchFromURL(ctx context.Context, sourceURL string) (map[string]float64, error) {
	body, err := fxRateHTTPGet(ctx, sourceURL)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Rates map[string]float64 `json:"rates"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode fx rates: %w", err)
	}
	if len(payload.Rates) == 0 {
		return nil, fmt.Errorf("fx source returned no rates")
	}
	out := make(map[string]float64, len(SupportedQuoteCurrencies()))
	for _, quote := range SupportedQuoteCurrencies() {
		if rate, ok := payload.Rates[quote]; ok {
			out[quote] = rate
		}
	}
	return out, nil
}

func joinQuotes(quotes []string) string {
	out := ""
	for i, q := range quotes {
		if i > 0 {
			out += ","
		}
		out += q
	}
	return out
}

// fxRateHTTPGet 带超时与限读的 GET, 返回响应体。
func fxRateHTTPGet(ctx context.Context, url string) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, fxRateFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
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
	return body, nil
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
// Source 取最近一次成功刷新的源, 与落库口径一致。
func FxRateSnapshotView(ctx context.Context) (day string, items []model.FxRate, err error) {
	snap, err := fxSnapshotLoad(ctx)
	if err != nil {
		return "", nil, err
	}
	if snap == nil {
		return "", []model.FxRate{}, nil
	}
	source := FxRateSourceName()
	quotes := make([]string, 0, len(snap.rates))
	for quote := range snap.rates {
		quotes = append(quotes, quote)
	}
	sort.Strings(quotes)
	items = make([]model.FxRate, 0, len(quotes))
	for _, quote := range quotes {
		items = append(items, model.FxRate{From: model.LedgerCurrency, To: quote, Day: snap.day, Rate: snap.rates[quote], Source: source})
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

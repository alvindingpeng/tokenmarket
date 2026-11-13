package handlers

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/relay"
	"github.com/alvindingpeng/switchyard/internal/server/middleware"
	"github.com/alvindingpeng/switchyard/internal/server/resp"
	"github.com/alvindingpeng/switchyard/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	// 模型广场(P1): 只读挂牌表。登录即可, 普通用户是主要受众 —— 他们要用它挑模型加进自己的分组。
	// 不挂 RequireJSON: 全部是查询参数, 没有请求体。
	router.NewGroupRouter("/api/v1/market").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/models", http.MethodGet).
				Handle(listMarketModels),
		)
}

// 广场排序维度。每个维度自带唯一的最优方向, 因此请求只给维度不给方向:
// 越便宜/越快越靠前、成功率越高越靠前、名称升序。
// 让用户反转"成功率升序"只会把最差模型排到第一屏, 那是纯噪声而不是自由度。
const (
	marketSortPrice   = "price"
	marketSortSuccess = "success"
	marketSortLatency = "latency"
	marketSortName    = "name"
)

// 指标来源标注: 界面必须据此决定"显示数字还是显示 —", 不许把没样本渲染成 100% 成功,
// 也不许把小样本当成可信成绩。
const (
	marketMetricsLive         = "live"         // 样本达到门槛, 数字可信。
	marketMetricsDaily        = "daily"        // 本次进程无样本, 数字来自近 7 天的持久化统计。
	marketMetricsInsufficient = "insufficient" // 有样本但不足门槛: 不给数字, 但仍报出样本数。
	marketMetricsNone         = "none"         // 无样本(冷启动), 或进程重启后尚未恢复。
)

// marketMinSamples 是给出成绩数字的最低样本数, 与 model.QualityMinSamples 同值同源。
//
// 不设门槛的后果是真实的: 生产上一个只有 1 次调用的模型以 43ms 排到了"延迟最优"第一位,
// 而它恰好只跑成功过一次。把"没跑几次"当成"跑得最快/最稳", 与把"没数据"当成 100% 成功
// 是同一个错误的两种尺度 —— 都是把"未知"读成了结论。
const marketMinSamples = int(model.QualityMinSamples)

// marketMetricsSourceOf 把样本数翻译成口径标注。单点在此, 免得调用方各自判一个阈值。
func marketMetricsSourceOf(samples int) string {
	switch {
	case samples <= 0:
		return marketMetricsNone
	case samples < marketMinSamples:
		return marketMetricsInsufficient
	default:
		return marketMetricsLive
	}
}

// marketDailyFallbackDays 是日级回落取数的窗口。取 7 天与设计稿 §5.2 一致:
// 更长会把两周前的状况算进"近期", 更短则周末低峰期容易被判成冷启动。
const marketDailyFallbackDays = 7

// marketMetricsSourceOfDaily 是日级回落专用的口径判定: 与 live 同门槛,
// 但把"够不够样本"的结论标成 daily, 让界面能如实写出"来自近 7 天统计"。
// 样本不足时仍退回 insufficient: 一周只跑了 3 次同样不该给出"近期成功率 66%"。
func marketMetricsSourceOfDaily(samples int) string {
	switch {
	case samples <= 0:
		return marketMetricsNone
	case samples < marketMinSamples:
		return marketMetricsInsufficient
	default:
		return marketMetricsDaily
	}
}

// marketModelMetricsSource 汇总一个模型行的口径标注。
//
// 规则: 合计样本数不足 -> insufficient/none(与渠道级同源);
// 达标时再看数字出处 —— 只要有一条渠道的数字来自本次进程(live), 整行就标 live;
// 全部来自近 7 天持久化统计时才标 daily。
//
// 为什么必须区分: daily 与 live 的 WaitTime 语义不同源(前者是区间累计, 后者是逐次 EMA),
// 界面承诺"实时"而数字其实来自历史统计, 是比显示 "—" 更容易误导的一种错误。
func marketModelMetricsSource(channels []marketChannelView, samples int) string {
	source := marketMetricsSourceOf(samples)
	if source != marketMetricsLive {
		return source
	}
	for _, channel := range channels {
		if channel.MetricsSource == marketMetricsLive {
			return marketMetricsLive
		}
	}
	// 样本达标但没有一条是 live: 说明数字全部来自日级回落。
	return marketMetricsDaily
}

// marketChannelModelIDs 收集本屏全部渠道模型主键, 供批量查询回落数据与质量快照。
func marketChannelModelIDs(models []model.MarketModel) []int {
	ids := make([]int, 0, len(models)*2)
	for _, entry := range models {
		for _, channel := range entry.Channels {
			ids = append(ids, channel.ChannelModelID)
		}
	}
	return op.RecentMetricsOrder(ids)
}

// marketQualityBandsOf 取本屏每个渠道近 7 天的质量区间。
//
// 数据源是 channel_quality_snapshots(日级固化), 时效门槛与 op.ShareQualityOf 一致(7 天内)。
// 只回区间文案不出精确值: 理由见 marketQualityBand 的注释(面向所有登录用户, 与分享页同防线)。
// 三个维度各自独立判定样本门槛 —— 探测次数不足时只省略可用率, 不把整段抹掉。
func marketQualityBandsOf(models []model.MarketModel) map[int]op.MarketQualityBand {
	ids := marketChannelModelIDs(models)
	return op.MarketQualityBandsForChannelModels(ids, marketDailyFallbackDays)
}

// marketChannelView 是一条挂牌行加上运行时指标。
// 指标按渠道模型归并(relay 的指标就是按渠道模型统计的), 不是按授权 ——
// 同一渠道模型的多个授权共用同一份成绩, 分开统计会把样本无谓地摊薄。
type marketChannelView struct {
	model.MarketChannel
	WaitMs  float64 `json:"wait_ms,omitempty"` // 样本加权后的首响应耗时(毫秒); 样本不足时省略。
	Success float64 `json:"success,omitempty"` // 样本加权后的成功率(0..1); 样本不足时省略。
	Samples int     `json:"samples"`           // 观测样本数; 0 表示冷启动。
	// MetricsSource 逐行也给一份: 同一模型的不同渠道样本量可以差两个数量级,
	// 由前端各自赛一个"样本够不够"的阈值必然与服务端分叉。
	MetricsSource string `json:"metrics_source"` // live|daily|insufficient|none。
	// QualityBand 是本渠道近 7 天的质量区间, 复用 model.QualityBand* 的三段口径。
	// 只给区间不给精确值: 广场面向全部登录用户, 精确成功率/延迟/请求量属于运营情报,
	// 与分享页(ShareQualityOf)同一道脱敏防线。样本不达标时整段省略(omitempty), 不输出空壳。
	QualityBand *op.MarketQualityBand `json:"quality_band,omitempty"`
}

// marketModelView 是一个模型行加上归并后的模型级指标。
type marketModelView struct {
	model.MarketModel
	WaitMs        float64 `json:"wait_ms,omitempty"`
	Success       float64 `json:"success,omitempty"`
	Samples       int     `json:"samples"`
	MetricsSource string  `json:"metrics_source"`
	// Channels 遮蔽嵌入的 model.MarketModel.Channels, 只为把每条挂牌行换成带指标的那份。
	// 遮蔽而不另起字段名: 对外形状仍是一层对象, 前端拿到的 channels 就是带指标的列表;
	// 同时给出两份(一份无指标、一份有指标)会让消费方无从选择。
	Channels []marketChannelView `json:"channels"`
}

// marketCatalogResponse 是广场的完整响应。
type marketCatalogResponse struct {
	Models []marketModelView `json:"models"`
	// Brands 是全集的品牌键, 供前端品牌下拉使用 —— 不写死在前端, 否则后端新增一个品牌时
	// 前端要多发一次版才能选到。注意这里是**筛选之前**汇总的: 若只回传"本次结果里的品牌",
	// 用户选中 openai 之后下拉就只剩 openai, 再也没法加选第二个厂商。
	Brands    []string  `json:"brands"`
	UpdatedAt time.Time `json:"updated_at"`
}

// listMarketModels 返回模型广场挂牌表: 已发布在用模型的价格、渠道编码、运行状态与近期成绩。
//
// 口径分工: 供给侧事实(哪些授权在卖、多少钱、能不能用)来自 op.MarketCatalog,
// 运行指标来自 relay.ModelScoreViews()。两者在此处汇合 —— 这是既有 GET /api/v1/model/scores
// 已经采用的同一模式(op 不 import relay, 只有处理器能把两边拼起来)。
//
// 筛选与排序一律在服务端: 前端不重排。否则"按成功率排序"会与分页/局部筛选叠加出两套顺序。
func listMarketModels(c *gin.Context) {
	userID, role := middleware.CurrentUser(c)
	scope := model.Scope{ID: userID, Role: role}

	models := op.MarketCatalog(scope)
	views := marketScoreIndex(relay.ModelScoreViews())
	now := time.Now()

	// 日级回落与质量带都是"整表批量读", 不能放进逐行循环里 —— 那样一屏 48 行会打出上百次查询。
	// 先把本屏涉及的渠道模型主键收齐, 两次查询带出全部素材。
	channelModelIDs := marketChannelModelIDs(models)
	// 近 7 天持久化成绩: 只在进程内没有样本的行上使用, 作为重启后的回落口径(见 op.ChannelModelRecentMetrics)。
	recent := op.ChannelModelRecentMetrics(channelModelIDs, marketDailyFallbackDays)
	// 质量区间: 每个渠道近 7 天的最新快照, 复用 ShareQualityOf 同一套时效门槛。
	bands := marketQualityBandsOf(models)

	out := make([]marketModelView, 0, len(models))
	brands := make([]string, 0, 8)
	seenBrand := make(map[string]bool, 8)
	for _, entry := range models {
		view := marketModelView{
			MarketModel:   entry,
			MetricsSource: marketMetricsNone,
			Channels:      make([]marketChannelView, 0, len(entry.Channels)),
		}
		// 模型级指标按样本数加权归并: 与 relay.ModelScoreBriefs 的口径相同(样本加权),
		// 不手写第二套算法, 否则同一个模型在模型页与广场会显示两个成功率。
		// 先按原始指标做一次样本加权归并(与 relay.ModelScoreBriefs 同一口径), 再决定给不给数字:
		// 归并口径与展示门槛是两件事, 混在一起会让模型级样本数与渠道级样本数之和不再对得上。
		weight := 0.0
		waitSum := 0.0
		successSum := 0.0
		for _, channel := range entry.Channels {
			channelView := marketChannelView{MarketChannel: channel}
			if metric, ok := views[channel.ChannelModelID]; ok && metric.Samples > 0 {
				channelView.Samples = metric.Samples
				channelView.MetricsSource = marketMetricsSourceOf(metric.Samples)
				// 样本不足的渠道只报样本数, 不报数字: 让"有 1 次调用"看起来像一条可用成绩,
				// 正是这一档存在的原因。
				if channelView.MetricsSource == marketMetricsLive {
					channelView.WaitMs = metric.WaitMs
					channelView.Success = metric.Success
				}
				weight += float64(metric.Samples)
				waitSum += metric.WaitMs * float64(metric.Samples)
				successSum += metric.Success * float64(metric.Samples)
			} else {
				channelView.MetricsSource = marketMetricsNone
			}
			// 进程内没有样本时才回落: live 数字永远优先, 日级只是"重启后别显示 —"的兜底。
			// 两者不可混算(WaitTime 语义不同源), 所以是整段换源而不是逐字段拼接。
			if channelView.MetricsSource == marketMetricsNone {
				if metrics, ok := recent[channel.ChannelModelID]; ok {
					if samples, success, waitMs := op.RecentMetricsOf(metrics); samples > 0 {
						channelView.Samples = samples
						source := marketMetricsSourceOfDaily(samples)
						channelView.MetricsSource = source
						// 与 live 分支同一条规则: 只有样本达标才给数字。
						// 回落这条路径最初漏了这一步: 一周只跑 7 次的模型带着 insufficient
						// 标注却显示 42.8% —— 标注与数字自相矛盾。
						if source == marketMetricsDaily {
							channelView.WaitMs = waitMs
							channelView.Success = success
						}
						weight += float64(samples)
						waitSum += waitMs * float64(samples)
						successSum += success * float64(samples)
					}
				}
			}
			// 质量带: 区间文案, 样本不达标时保持 nil(omitempty 整段省略, 不输出空壳)。
			if band, ok := bands[channel.ChannelModelID]; ok {
				channelView.QualityBand = &band
			}
			view.Channels = append(view.Channels, channelView)
			view.Samples += channelView.Samples
		}
		// 模型级门槛按**合计**样本数, 与渠道级门限不同: 一个模型跨 8 个渠道各跑 5 次,
		// 合计 40 次已经足够说明它整体稳不稳, 不该因为单个渠道样本少而把整行抹成"未知"。
		//
		// 但"够不够样本"与"数字从哪来"是两件事: 一个模型的全部渠道都靠日级回落时, 合计样本数
		// 照样能过门槛, 若只按样本数判口径就会把 daily 标成 live —— 界面于是声称这是一段
		// "本次进程的实时成绩", 而进程其实一次都没跑过它。故口径要按成员出处汇总:
		// 有任一渠道是 live 就仍算 live(那确实是本次进程测到的), 全部来自日级才标 daily。
		view.MetricsSource = marketModelMetricsSource(view.Channels, view.Samples)
		if weight > 0 && view.hasMetrics() {
			view.WaitMs = waitSum / weight
			view.Success = successSum / weight
		}
		if !seenBrand[view.Brand] {
			seenBrand[view.Brand] = true
			brands = append(brands, view.Brand)
		}
		out = append(out, view)
	}

	out = filterMarketModels(out, c)
	sortMarketModels(out, marketSortOf(c))
	sort.Strings(brands)

	resp.Success(c, marketCatalogResponse{Models: out, Brands: brands, UpdatedAt: now})
}

// marketScoreIndex 把 relay 的指标视图按渠道模型主键索引化, 供每条挂牌行查自己的成绩。
func marketScoreIndex(views []relay.ModelScoreView) map[int]relay.ModelScoreView {
	out := make(map[int]relay.ModelScoreView, len(views))
	for _, view := range views {
		out[view.ChannelModelID] = view
	}
	return out
}

// marketSortOf 解析 sort 参数; 未给或无法识别时按名称, 与无排序参数的默认序一致。
func marketSortOf(c *gin.Context) string {
	switch strings.ToLower(strings.TrimSpace(c.Query("sort"))) {
	case marketSortPrice:
		return marketSortPrice
	case marketSortSuccess:
		return marketSortSuccess
	case marketSortLatency:
		return marketSortLatency
	default:
		return marketSortName
	}
}

// filterMarketModels 按查询参数筛选: 品牌(可多值)、名称关键词、形态、只看可用。
//
// 品牌按"多选即并集"处理(brand=openai&brand=anthropic), 空集合表示不限 ——
// 空集合与"全都不选"在界面上是同一件事, 不给用户一个"选完一个都筛不出"的空屏。
func filterMarketModels(models []marketModelView, c *gin.Context) []marketModelView {
	brands := queryValues(c, "brand")
	keyword := strings.ToLower(strings.TrimSpace(c.Query("q")))
	kind := strings.ToLower(strings.TrimSpace(c.Query("kind")))
	onlyAvailable := queryBool(c, "only_available")

	brandSet := make(map[string]bool, len(brands))
	for _, brand := range brands {
		if brand = strings.ToLower(strings.TrimSpace(brand)); brand != "" {
			brandSet[brand] = true
		}
	}

	out := make([]marketModelView, 0, len(models))
	for _, view := range models {
		if len(brandSet) > 0 && !brandSet[view.Brand] {
			continue
		}
		if keyword != "" && !strings.Contains(strings.ToLower(view.Name), keyword) {
			continue
		}
		if kind != "" && !marketKindMatches(view, kind) {
			continue
		}
		if onlyAvailable {
			// 只看可用: 裁掉不可用的渠道行; 一行都不剩的模型整条不列 ——
			// 留着一条空壳模型会让"只看可用"的语义变成"只看名字"。
			channels := make([]marketChannelView, 0, len(view.Channels))
			for _, channel := range view.Channels {
				if channel.Available {
					channels = append(channels, channel)
				}
			}
			if len(channels) == 0 {
				continue
			}
			view.Channels = channels
		}
		out = append(out, view)
	}
	return out
}

// marketKindMatches 判断模型是否符合形态筛选: text 匹配文本模型(含未标形态的),
// image/video 匹配对应媒体形态。模型级只要有一条渠道是该形态即算命中 ——
// 同一模型在不同渠道被配成不同形态时, 用户要能在这两种筛选下都找到它。
func marketKindMatches(view marketModelView, kind string) bool {
	for _, channel := range view.Channels {
		channelKind := string(channel.Kind)
		if channelKind == "" {
			channelKind = "text"
		}
		if channelKind == kind {
			return true
		}
	}
	return false
}

// sortMarketModels 按维度排序。无样本的模型一律置底: 冷启动不该靠"没数据"排到第一屏,
// 那是把"未知"当成"最优"。
func sortMarketModels(models []marketModelView, sortBy string) {
	switch sortBy {
	case marketSortPrice:
		sort.SliceStable(models, func(i, j int) bool {
			pi, pj := marketMinPrice(models[i]), marketMinPrice(models[j])
			// 无价(全部未定价)置底: 0 元不是"最便宜", 而是"没有报价"。
			if models[i].unpricedAll() != models[j].unpricedAll() {
				return !models[i].unpricedAll()
			}
			if pi != pj {
				return pi < pj
			}
			return models[i].Name < models[j].Name
		})
	case marketSortSuccess:
		sort.SliceStable(models, func(i, j int) bool {
			// 成绩不可信(无样本或样本不足)的模型一律置底, 且不与可信的比较 ——
			// 否则一个只跑过一次的模型会凭"这一次成功了"排到第一屏。
			li, lj := models[i].hasMetrics(), models[j].hasMetrics()
			if li != lj {
				return li
			}
			if !li {
				// 都不可信时按样本数多者靠前: 样本多说明它至少"在被用", 比完全冷启动有信息量。
				if models[i].Samples != models[j].Samples {
					return models[i].Samples > models[j].Samples
				}
				return models[i].Name < models[j].Name
			}
			if models[i].Success != models[j].Success {
				return models[i].Success > models[j].Success
			}
			return models[i].Name < models[j].Name
		})
	case marketSortLatency:
		sort.SliceStable(models, func(i, j int) bool {
			li, lj := models[i].hasMetrics(), models[j].hasMetrics()
			if li != lj {
				return li
			}
			if !li {
				if models[i].Samples != models[j].Samples {
					return models[i].Samples > models[j].Samples
				}
				return models[i].Name < models[j].Name
			}
			if models[i].WaitMs != models[j].WaitMs {
				return models[i].WaitMs < models[j].WaitMs
			}
			return models[i].Name < models[j].Name
		})
	default:
		sort.SliceStable(models, func(i, j int) bool { return models[i].Name < models[j].Name })
	}
}

// hasMetrics 判断这一行有没有可比较的成绩数字。
//
// 排序用"有没有数字"而不是"是不是 live": 日级回落(daily)的样本同样达标、同样可比,
// 只是因为本进程没跑过而换了来源。若把 daily 一起置底, 重启后整屏模型会重新掉到最后,
// 排序结果在运维动作前后凭空跳变, 而那与模型质量毫无关系。
func (view marketModelView) hasMetrics() bool {
	return view.MetricsSource == marketMetricsLive || view.MetricsSource == marketMetricsDaily
}

// marketMinPrice 返回模型下最便宜的那条可用渠道的用户价(混合口径)。
// 只比可用渠道: 一条不可用渠道的价格不构成"能买到的价格", 拿它做"最低价"角标会误导。
func marketMinPrice(view marketModelView) float64 {
	best := -1.0
	for _, channel := range view.Channels {
		if !channel.Available {
			continue
		}
		price := channel.SortPrice()
		if price <= 0 {
			continue
		}
		if best < 0 || price < best {
			best = price
		}
	}
	if best < 0 {
		return 0
	}
	return best
}

// unpricedAll 判断模型下是否没有任何可用且有报价的渠道。
func (v marketModelView) unpricedAll() bool {
	for _, channel := range v.Channels {
		if channel.Available && channel.SortPrice() > 0 {
			return false
		}
	}
	return true
}

// queryValues 读取可重复出现的查询参数, 兼容两种写法: ?brand=a&brand=b 与 ?brand=a,b。
// 前端表单两种都可能生成, 后端两种都收, 免得调用方为一个分隔符猜错。
func queryValues(c *gin.Context, key string) []string {
	raw, ok := c.GetQueryArray(key)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, value := range raw {
		for _, piece := range strings.Split(value, ",") {
			if piece = strings.TrimSpace(piece); piece != "" {
				out = append(out, piece)
			}
		}
	}
	return out
}

// queryBool 读取布尔查询参数, 接受 1/true/yes/on 的常见写法; 空值与无法识别一律当 false。
func queryBool(c *gin.Context, key string) bool {
	value := strings.ToLower(strings.TrimSpace(c.Query(key)))
	if value == "" {
		return false
	}
	if parsed, err := strconv.ParseBool(value); err == nil {
		return parsed
	}
	switch value {
	case "on", "yes":
		return true
	default:
		return false
	}
}

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

// 指标来源标注: 界面必须据此决定"显示数字还是显示 —", 不许把没样本渲染成 100% 成功。
const (
	marketMetricsLive = "live" // 来自 relay 进程内 EMA。
	marketMetricsNone = "none" // 无样本(冷启动), 或进程重启后尚未恢复。
)

// marketChannelView 是一条挂牌行加上运行时指标。
// 指标按渠道模型归并(relay 的指标就是按渠道模型统计的), 不是按授权 ——
// 同一渠道模型的多个授权共用同一份成绩, 分开统计会把样本无谓地摊薄。
type marketChannelView struct {
	model.MarketChannel
	WaitMs  float64 `json:"wait_ms,omitempty"` // 样本加权后的首响应耗时(毫秒)。
	Success float64 `json:"success,omitempty"` // 样本加权后的成功率(0..1)。
	Samples int     `json:"samples"`           // 观测样本数; 0 表示冷启动。
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
		weight := 0.0
		waitSum := 0.0
		successSum := 0.0
		for _, channel := range entry.Channels {
			channelView := marketChannelView{MarketChannel: channel}
			if metric, ok := views[channel.ChannelModelID]; ok && metric.Samples > 0 {
				channelView.WaitMs = metric.WaitMs
				channelView.Success = metric.Success
				channelView.Samples = metric.Samples
			}
			view.Channels = append(view.Channels, channelView)
			if channelView.Samples > 0 {
				weight += float64(channelView.Samples)
				waitSum += channelView.WaitMs * float64(channelView.Samples)
				successSum += channelView.Success * float64(channelView.Samples)
			}
			view.Samples += channelView.Samples
		}
		if weight > 0 {
			view.WaitMs = waitSum / weight
			view.Success = successSum / weight
			view.MetricsSource = marketMetricsLive
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
			// 样本数为 0 的模型置底, 且不与有样本的比较成功率。
			if models[i].Samples == 0 || models[j].Samples == 0 {
				return models[i].Samples > models[j].Samples
			}
			if models[i].Success != models[j].Success {
				return models[i].Success > models[j].Success
			}
			return models[i].Name < models[j].Name
		})
	case marketSortLatency:
		sort.SliceStable(models, func(i, j int) bool {
			if models[i].Samples == 0 || models[j].Samples == 0 {
				return models[i].Samples > models[j].Samples
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

package op

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
)

// latencySampleLimit 单次聚合读取的样本上限。
// 用分位数而非均值: 上游延迟长尾极重(p95 常是 p50 的数倍), 均值会把偶发卡顿抹平成"还行"。
const latencySampleLimit = 100000

// LatencyPercentiles 一对分位值, 单位毫秒。
type LatencyPercentiles struct {
	P50 int64 `json:"p50_ms"`
	P95 int64 `json:"p95_ms"`
}

// LatencyBreakdownRow 按「渠道 × 调用方国家」聚合的延迟分解。
//
// 关键是 UpstreamWait: TTFB 减去 DNS/建连/TLS 之后的剩余, 即上游自己排队的时间。
// 没有这个数就只能看着一个合计的首字节耗时猜"是我们连得慢还是上游慢"——
// 而这正是换上游与加出口两个决策的分水岭。
type LatencyBreakdownRow struct {
	ChannelCode  string             `json:"channel_code"`
	ChannelKey   string             `json:"channel_key"`
	EgressID     int                `json:"egress_id"`   // 出口池里的出口 ID; 0 表示未走出口池(直连或渠道代理)。
	EgressName   string             `json:"egress_name"` // 出口名称, 便于直接读; 出口已删除时为空。
	Country      string             `json:"country"`
	Requests     int                `json:"requests"`
	ReusedRate   float64            `json:"reused_rate"` // 连接复用占比; 复用率高说明建连开销已被摊薄。
	TTFB         LatencyPercentiles `json:"ttfb"`
	DNS          LatencyPercentiles `json:"dns"`
	Connect      LatencyPercentiles `json:"connect"`
	TLS          LatencyPercentiles `json:"tls"`
	UpstreamWait LatencyPercentiles `json:"upstream_wait"`
}

// latencyScanRow 是聚合查询的落点结构; 字段用 column 标签对应 SQL 别名。
type latencyScanRow struct {
	ChannelKey  string `gorm:"column:channel_key"`
	ChannelCode string `gorm:"column:channel_code"`
	EgressID    int    `gorm:"column:egress_id"`
	Country     string `gorm:"column:country"`
	TTFBNs      int64  `gorm:"column:ttfb_ns"`
	DNSNs       int64  `gorm:"column:dns_ns"`
	ConnectNs   int64  `gorm:"column:connect_ns"`
	TLSNs       int64  `gorm:"column:tls_ns"`
	ConnReused  bool   `gorm:"column:conn_reused"`
}

// LatencyBreakdown 统计时间窗内按渠道与国家的延迟分位; since 为零值时统计全量。
// 只统计有首字节耗时的请求: 连都没连上的失败请求没有 TTFB, 计入会把分位拉到 0。
func LatencyBreakdown(since time.Time) ([]LatencyBreakdownRow, error) {
	query := db.GetDB().Model(&model.RelayRequest{}).
		Select("target_channel_key as channel_key, target_channel_code as channel_code, " +
			"egress_id, client_country as country, ttfb_ns, dns_ns, connect_ns, tls_ns, conn_reused").
		Where("ttfb_ns > 0")
	if !since.IsZero() {
		query = query.Where("started_at >= ?", since)
	}
	var samples []latencyScanRow
	if err := query.Limit(latencySampleLimit).Scan(&samples).Error; err != nil {
		return nil, fmt.Errorf("query latency samples: %w", err)
	}
	if len(samples) == 0 {
		return []LatencyBreakdownRow{}, nil
	}

	type bucket struct {
		ttfb, dns, connect, tls, wait []int64
		reused                        int
	}
	buckets := make(map[string]*bucket)
	meta := make(map[string]latencyScanRow)
	for _, sample := range samples {
		// 出口也是分组维度: 同一渠道同一国家走不同出口的耗时差异, 正是出口池要回答的问题。
		key := sample.ChannelCode + "|" + sample.Country + "|" + strconv.Itoa(sample.EgressID)
		if key == "||0" {
			// 早期请求可能没有渠道编码(未发布渠道或失败轮次), 归入同一桶而不是丢弃。
			key = "||0"
		}
		current, ok := buckets[key]
		if !ok {
			current = &bucket{}
			buckets[key] = current
			meta[key] = sample
		}
		current.ttfb = append(current.ttfb, sample.TTFBNs)
		current.dns = append(current.dns, sample.DNSNs)
		current.connect = append(current.connect, sample.ConnectNs)
		current.tls = append(current.tls, sample.TLSNs)
		current.wait = append(current.wait, upstreamWait(sample))
		if sample.ConnReused {
			current.reused++
		}
	}

	egressNames := egressNameByID()
	rows := make([]LatencyBreakdownRow, 0, len(buckets))
	for key, current := range buckets {
		info := meta[key]
		total := len(current.ttfb)
		rows = append(rows, LatencyBreakdownRow{
			ChannelCode:  info.ChannelCode,
			ChannelKey:   info.ChannelKey,
			EgressID:     info.EgressID,
			EgressName:   egressNames[info.EgressID],
			Country:      info.Country,
			Requests:     total,
			ReusedRate:   float64(current.reused) / float64(total),
			TTFB:         percentiles(current.ttfb),
			DNS:          percentiles(current.dns),
			Connect:      percentiles(current.connect),
			TLS:          percentiles(current.tls),
			UpstreamWait: percentiles(current.wait),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Requests != rows[j].Requests {
			return rows[i].Requests > rows[j].Requests
		}
		return rows[i].ChannelCode < rows[j].ChannelCode
	})
	return rows, nil
}

// egressNameByID 返回出口 ID 到名称的映射; 查库失败时返回空表, 让聚合结果仍然可用。
func egressNameByID() map[int]string {
	names := make(map[int]string)
	var egresses []model.ChannelEgress
	if err := db.GetDB().Select("id", "name").Find(&egresses).Error; err != nil {
		return names
	}
	for _, egress := range egresses {
		names[egress.ID] = egress.Name
	}
	return names
}

// upstreamWait 估算上游自身排队耗时: 首字节耗时减去建连各段, 下限为 0。
// 复用连接时三段本就为 0, 相减即等于首字节耗时。
func upstreamWait(sample latencyScanRow) int64 {
	wait := sample.TTFBNs - sample.DNSNs - sample.ConnectNs - sample.TLSNs
	if wait < 0 {
		return 0
	}
	return wait
}

// percentiles 计算 p50 与 p95, 单位毫秒; 空序列返回零值。
func percentiles(values []int64) LatencyPercentiles {
	if len(values) == 0 {
		return LatencyPercentiles{}
	}
	sorted := make([]int64, len(values))
	copy(sorted, values)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return LatencyPercentiles{
		P50: nanosecondToMillis(percentileAt(sorted, 0.50)),
		P95: nanosecondToMillis(percentileAt(sorted, 0.95)),
	}
}

// percentileAt 取有序序列的 p 分位; 用四舍五入定位, 保证 n=1 时返回唯一元素。
func percentileAt(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	index := int(float64(len(sorted)-1)*p + 0.5)
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

// nanosecondToMillis 把纳秒换算成毫秒。
func nanosecondToMillis(value int64) int64 { return value / int64(time.Millisecond) }

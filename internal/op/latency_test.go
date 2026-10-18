package op

import "testing"

// TestPercentiles 固化分位取值规则: n=1 返回唯一元素, 否则按四舍五入定位。
// 用分位数而非均值是本模块的前提, 取值规则一错整张归因表都会失真。
// 刻意不做线性插值: 分位在这里只用来看量级与长尾, 插值会造出一个数据里根本不存在的样本值,
// 反而让"这条 p95 对应哪次真实请求"变得不可追。偶数样本时因此取上侧中位数。
func TestPercentiles(t *testing.T) {
	cases := []struct {
		name   string
		values []int64 // 纳秒
		want   LatencyPercentiles
	}{
		{name: "空序列", values: nil, want: LatencyPercentiles{}},
		{name: "单样本", values: []int64{3_000_000}, want: LatencyPercentiles{P50: 3, P95: 3}},
		{
			name:   "十样本",
			values: []int64{1e6, 2e6, 3e6, 4e6, 5e6, 6e6, 7e6, 8e6, 9e6, 10e6},
			want:   LatencyPercentiles{P50: 6, P95: 10},
		},
		{
			name:   "乱序输入先排序",
			values: []int64{10e6, 1e6, 5e6},
			want:   LatencyPercentiles{P50: 5, P95: 10},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := percentiles(tc.values); got != tc.want {
				t.Fatalf("percentiles() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestPercentilesDoesNotMutateInput 验证计算分位不会打乱调用方的切片顺序。
func TestPercentilesDoesNotMutateInput(t *testing.T) {
	values := []int64{9e6, 1e6, 5e6}
	percentiles(values)
	if values[0] != 9e6 || values[1] != 1e6 || values[2] != 5e6 {
		t.Fatalf("输入被就地排序: %v", values)
	}
}

// TestUpstreamWait 验证"上游等待 = 首字节 - DNS - 建连 - TLS"且不会为负。
// 这是判断"换上游还是加出口"的核心口径: 减错一项就会把自家建连开销算成上游的锅。
func TestUpstreamWait(t *testing.T) {
	cases := []struct {
		name   string
		sample latencyScanRow
		want   int64
	}{
		{
			name:   "新建连接逐段扣除",
			sample: latencyScanRow{TTFBNs: 1000, DNSNs: 100, ConnectNs: 200, TLSNs: 300},
			want:   400,
		},
		{
			name:   "复用连接三段为 0 时等于首字节",
			sample: latencyScanRow{TTFBNs: 800, ConnReused: true},
			want:   800,
		},
		{
			name:   "建连之和超过首字节时钳制为 0",
			sample: latencyScanRow{TTFBNs: 100, DNSNs: 100, ConnectNs: 200, TLSNs: 300},
			want:   0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := upstreamWait(tc.sample); got != tc.want {
				t.Fatalf("upstreamWait() = %d, want %d", got, tc.want)
			}
		})
	}
}

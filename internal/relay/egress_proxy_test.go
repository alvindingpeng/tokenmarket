package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/model"
)

// newRecordingProxy 起一个最小可用的正向代理, 记录被使用的次数。
// 用它模拟"两个出口": 请求经过哪个代理, 就说明本轮走的是哪条线路。
func newRecordingProxy(t *testing.T, counter *atomic.Int32) *httptest.Server {
	t.Helper()
	// 代理自己转发时不能再走代理, 否则会绕回自己。
	forwarder := &http.Client{Transport: &http.Transport{Proxy: nil}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counter.Add(1)
		outReq, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		outReq.Header = r.Header.Clone()
		response, err := forwarder.Do(outReq)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
	}))
	t.Cleanup(server.Close)
	return server
}

// TestEgressSwitchoverKeepsRequestsSucceeding 对应出口池的验收标准:
// 摘掉当前最优的出口后, 请求自动走另一条线路且不失败。
func TestEgressSwitchoverKeepsRequestsSucceeding(t *testing.T) {
	resetEgressStates()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(upstream.Close)

	var fastHits, slowHits atomic.Int32
	fastProxy := newRecordingProxy(t, &fastHits)
	slowProxy := newRecordingProxy(t, &slowHits)

	fast := model.ChannelEgress{ID: 101, Name: "fast", Kind: model.EgressKindHTTP, URL: fastProxy.URL, Weight: 9, Enabled: true}
	slow := model.ChannelEgress{ID: 102, Name: "slow", Kind: model.EgressKindHTTP, URL: slowProxy.URL, Weight: 0, Enabled: true}
	channel := egressFixture(fast, slow)

	// 两条出口都已有实测成绩: fast 明显更快, 因此正常情况总是它胜出。
	recordEgressSample(fast.ID, 20, 50, true)
	recordEgressSample(slow.ID, 20, 800, true)

	get := func(t *testing.T, egress model.ChannelEgress) {
		t.Helper()
		client, err := resolveUpstreamClient(channel, egress)
		if err != nil {
			t.Fatalf("取客户端失败: %v", err)
		}
		response, err := client.Get(upstream.URL)
		if err != nil {
			t.Fatalf("经出口 %s 请求失败: %v", egress.Name, err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "ok") {
			t.Fatalf("经出口 %s 请求未成功: status=%d body=%q", egress.Name, response.StatusCode, body)
		}
	}

	picked, _, err := pickEgress(channel, "")
	if err != nil {
		t.Fatalf("选择出口失败: %v", err)
	}
	if picked.ID != fast.ID {
		t.Fatalf("应选中实测更快的出口 %s, 实得 %s", fast.Name, picked.Name)
	}
	get(t, picked)
	if fastHits.Load() != 1 || slowHits.Load() != 0 {
		t.Fatalf("第一次请求应只经过 fast, 实得 fast=%d slow=%d", fastHits.Load(), slowHits.Load())
	}

	// 管理员摘掉最优出口。
	channel.Egresses[0].Enabled = false

	picked, _, err = pickEgress(channel, "")
	if err != nil {
		t.Fatalf("摘掉最优出口后选择失败: %v", err)
	}
	if picked.ID != slow.ID {
		t.Fatalf("应切到另一条出口 %s, 实得 %s", slow.Name, picked.Name)
	}
	get(t, picked)
	if slowHits.Load() != 1 {
		t.Fatalf("切换后的请求应经过 slow, 实得 slow=%d", slowHits.Load())
	}
	if fastHits.Load() != 1 {
		t.Fatalf("已停用的出口不应再被使用, 实得 fast=%d", fastHits.Load())
	}
}

// TestEgressFailureCoolsAndSwitches 一个出口失败后进入冷却, 下一轮自动换另一条。
func TestEgressFailureCoolsAndSwitches(t *testing.T) {
	resetEgressStates()

	dead := model.ChannelEgress{ID: 201, Name: "dead", Kind: model.EgressKindHTTP, URL: "http://127.0.0.1:1", Weight: 9, Enabled: true}
	alive := model.ChannelEgress{ID: 202, Name: "alive", Kind: model.EgressKindDirect, Enabled: true}
	channel := egressFixture(dead, alive)

	// dead 之前表现更快, 所以本轮会先选它。
	recordEgressSample(dead.ID, 20, 30, true)
	recordEgressSample(alive.ID, 20, 900, true)

	picked, _, err := pickEgress(channel, "")
	if err != nil {
		t.Fatalf("选择出口失败: %v", err)
	}
	if picked.ID != dead.ID {
		t.Fatalf("应选中更快的出口 %s, 实得 %s", dead.Name, picked.Name)
	}

	// 模拟这一轮失败: 记失败样本并冷却。
	recordEgressSample(dead.ID, 20, 5000, false)
	coolEgress(dead.ID, 30)

	picked, _, err = pickEgress(channel, "")
	if err != nil {
		t.Fatalf("冷却后选择失败: %v", err)
	}
	if picked.ID != alive.ID {
		t.Fatalf("失败出口冷却后应换 %s, 实得 %s", alive.Name, picked.Name)
	}
}

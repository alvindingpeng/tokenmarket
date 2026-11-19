package relay

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/rhttp"
)

// egressIDProbeDB 独立 sqlite: attemptRow 会写库, 没有库会空指针。
func egressIDProbeDB(t *testing.T) {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "egress_id.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := op.InitCache(); err != nil {
		t.Fatalf("init cache: %v", err)
	}
}

// TestEgressIDSurvivesTimingSnapshot 验证出口 ID 不会被耗时快照覆盖。
//
// 线上事实(2026-11-03): relay_request_attempts.egress_id 全表 5949 行全为 0,
// 而同一时刻已有 7 个渠道配了出口, 且这些渠道正在承载流量。
// 慢路径归因(按出口分组)因此永远无数据。
//
// 成因: setRoundEgress 在轮次开始前写入 roundTiming.EgressID,
// 而 setRoundTiming 在轮次结束后用 ConnTiming.Snapshot() 整体覆盖 roundTiming ——
// Snapshot 不含 EgressID, 于是被抹回 0。本测试复现该顺序。
func TestEgressIDSurvivesTimingSnapshot(t *testing.T) {
	egressIDProbeDB(t)
	request := newRequestState(context.Background(), "gpt-test", "", 1,
		model.ProtocolOpenAIChatCompletion, "", 0, 1, "127.0.0.1", "", 60)

	egress := model.ChannelEgress{ID: 42, Name: "egress-1", Kind: model.EgressKindHTTP, URL: "http://203.0.113.9:8080", Enabled: true}

	// 顺序与 handler.go 一致: 先选出口(441), 上游返回后登记耗时快照(523)。
	request.setRoundEgress(egress)
	request.setRoundTiming((&rhttp.ConnTiming{}).Snapshot())

	// 尝试行与请求行都从 roundTiming 取出口。
	attempt := attemptRow(request, 1, 10, "ch-code", "gpt-test", model.ProtocolOpenAIChatCompletion,
		request.StartedAt, "success", "")
	if attempt.UpstreamTiming.EgressID != 42 {
		t.Fatalf("尝试行的出口 ID 被耗时快照抹掉: 期望 42, 实得 %d", attempt.UpstreamTiming.EgressID)
	}
	if got := request.rowLocked().UpstreamTiming.EgressID; got != 42 {
		t.Fatalf("请求行的出口 ID 同样被抹掉: %d", got)
	}
}

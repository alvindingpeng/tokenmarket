package relay

import (
	"context"
	"testing"

	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/rhttp"
)

// 测试用的数据库初始化, 仅包含本测试需要的表; 与主流程迁移同步更新。
func egressIDProbeDB(t *testing.T) {
	t.Helper()
	db.GetDB().Exec("DELETE FROM channel_egress")
	db.GetDB().Exec("DELETE FROM relay_attempts")
	db.GetDB().Exec("DELETE FROM relay_requests")
}

// Snapshot 不含 EgressID, 于是被抹回 0。本测试复现该顺序。
func TestEgressIDSurvivesTimingSnapshot(t *testing.T) {
	egressIDProbeDB(t)
	request := newRequestState(context.Background(), "gpt-test", "", 1,
		model.ProtocolOpenAIChatCompletion, "", 0, 1, 0, "127.0.0.1", "", 60)

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

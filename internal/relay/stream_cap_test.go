package relay

import (
	"strings"
	"testing"
)

func TestTrackAggregatedChunkCaps(t *testing.T) {
	total := 0
	var ok bool
	// 未超限: 正常累计。
	total, ok = trackAggregatedChunk(total, []byte(strings.Repeat("x", 1000)))
	if !ok || total != 1000 {
		t.Fatalf("first chunk: total=%d ok=%v", total, ok)
	}
	// 超限: 拒绝且不改变累计值。
	_, ok = trackAggregatedChunk(total, make([]byte, maxAggregatedStreamBytes))
	if ok {
		t.Fatal("chunk beyond the cap must be rejected")
	}
	if total != 1000 {
		t.Fatalf("rejected chunk must not change total: %d", total)
	}
}

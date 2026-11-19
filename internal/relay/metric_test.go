package relay

import "testing"

func TestMetricAlphaUsesWindow(t *testing.T) {
	if got := metricAlpha(10); got < 0.18 || got > 0.19 {
		t.Fatalf("metricAlpha(10) = %v, want about 0.1818", got)
	}
	if metricAlpha(30) >= metricAlpha(10) {
		t.Fatalf("larger windows should be smoother: 10=%v 30=%v", metricAlpha(10), metricAlpha(30))
	}
}

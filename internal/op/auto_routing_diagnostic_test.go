package op

import "testing"

func TestAutoRoutingDiagnosticLabels(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{"limited", "rate"},
		{"timeout", "timeout"},
		{"auth_error", "auth"},
		{"quota", "quota"},
		{"overloaded", "overload"},
		{"compatibility_error", "compatibility"},
		{"failed", "unknown"},
	}
	for _, test := range tests {
		if got := autoAttemptErrorClass(test.status); got != test.want {
			t.Fatalf("status %q classified as %q, want %q", test.status, got, test.want)
		}
	}
}

func TestAutoInputCostBandBoundaries(t *testing.T) {
	tests := []struct {
		tokens int64
		want   string
	}{
		{0, ""},
		{1, "short"},
		{1999, "short"},
		{2000, "medium"},
		{15999, "medium"},
		{16000, "long"},
		{63999, "long"},
		{64000, "xlong"},
	}
	for _, test := range tests {
		if got := autoInputCostBand(test.tokens); got != test.want {
			t.Fatalf("input %d classified as %q, want %q", test.tokens, got, test.want)
		}
	}
}

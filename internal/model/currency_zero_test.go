package model

import "testing"

// TestCurrencyZeroDecimal 零小数币种判定与展示位宽。
func TestCurrencyZeroDecimal(t *testing.T) {
	cases := []struct {
		code   string
		zero   bool
		places int
	}{
		{"JPY", true, 0}, {"jpy ", true, 0}, {"KRW", true, 0},
		{"USD", false, 2}, {"CNY", false, 2}, {"", false, 2}, {"BRL", false, 2},
	}
	for _, tc := range cases {
		if got := CurrencyZeroDecimal(tc.code); got != tc.zero {
			t.Fatalf("CurrencyZeroDecimal(%q)=%v, want %v", tc.code, got, tc.zero)
		}
		if got := CurrencyMinorUnits(tc.code); got != tc.places {
			t.Fatalf("CurrencyMinorUnits(%q)=%d, want %d", tc.code, got, tc.places)
		}
	}
}

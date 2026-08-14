package tenant

import "testing"

func TestParseMinorUsesExactCurrencyUnits(t *testing.T) {
	tests := map[string]int64{"12.34": 1234, "12.3": 1230, "12": 1200, "0.01": 1, "1,234.56": 123456, "": 0}
	for input, want := range tests {
		got, err := parseMinor(input)
		if err != nil || got != want {
			t.Fatalf("%q: got %d, %v; want %d", input, got, err, want)
		}
	}
	if _, err := parseMinor("1.234"); err == nil {
		t.Fatal("expected precision error")
	}
}

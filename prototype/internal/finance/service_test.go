package finance

import (
	"errors"
	"testing"
)

func TestValidateLinesRequiresBalancedDoubleEntry(t *testing.T) {
	tests := []struct {
		name  string
		lines []EntryLineInput
		want  error
	}{
		{"balanced", []EntryLineInput{{AccountID: "cash", DebitMinor: 1250}, {AccountID: "revenue", CreditMinor: 1250}}, nil},
		{"unbalanced", []EntryLineInput{{AccountID: "cash", DebitMinor: 1250}, {AccountID: "revenue", CreditMinor: 1200}}, ErrUnbalanced},
		{"one line", []EntryLineInput{{AccountID: "cash", DebitMinor: 1250}}, errors.New("invalid")},
		{"both sides", []EntryLineInput{{AccountID: "cash", DebitMinor: 1250, CreditMinor: 1}, {AccountID: "revenue", CreditMinor: 1250}}, errors.New("invalid")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateLines(tt.lines)
			if tt.want == nil && err != nil {
				t.Fatal(err)
			}
			if tt.want != nil && err == nil {
				t.Fatal("expected validation error")
			}
			if errors.Is(tt.want, ErrUnbalanced) && !errors.Is(err, ErrUnbalanced) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestNormalBalanceMatchesAccountingEquation(t *testing.T) {
	for _, tt := range []struct{ kind, want string }{{"asset", "debit"}, {"expense", "debit"}, {"liability", "credit"}, {"equity", "credit"}, {"income", "credit"}} {
		got, err := normalBalance(tt.kind)
		if err != nil || got != tt.want {
			t.Fatalf("%s: got %q, %v", tt.kind, got, err)
		}
	}
}

func TestValidateCurrencyNormalizesWithoutFloats(t *testing.T) {
	got, err := validateCurrency(" usd ")
	if err != nil || got != "USD" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := validateCurrency("US"); err == nil {
		t.Fatal("expected invalid currency")
	}
}

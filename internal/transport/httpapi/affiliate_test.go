package httpapi

import (
	"errors"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestAffiliateSettlementInputFailsClosed(t *testing.T) {
	accountID := ids.AccountID("10000000-0000-4000-8000-000000000001")
	tests := []struct {
		name    string
		mode    string
		account ids.AccountID
		want    error
	}{
		{name: "unconfigured", mode: "unconfigured", want: errAffiliateSettlementUnconfigured},
		{name: "unknown mode", mode: "future", want: errAffiliateSettlementUnconfigured},
		{name: "credit needs account", mode: "account_credit", want: errAffiliateSettlementRequired},
		{name: "cash rejects account", mode: "cash", account: accountID, want: errAffiliateSettlementNotAllowed},
		{name: "credit accepts account", mode: "account_credit", account: accountID},
		{name: "cash accepts no account", mode: "cash"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateAffiliateSettlement(test.mode, test.account)
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v want=%v", err, test.want)
			}
		})
	}
}

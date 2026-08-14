package components

import "testing"

func TestEvidenceDispositionLabelRecapsInterviewOutcome(t *testing.T) {
	tests := []struct {
		disposition string
		status      string
		want        string
	}{
		{"", "confirmed", "Verified evidence is attached"},
		{"not_applicable", "not_applicable", "Not applicable to this business"},
		{"have_it", "partial", "Locate and verify the existing record"},
		{"search_sources", "partial", "Mia will search the available sources"},
		{"create_it", "missing", "Mainspring will draft the missing record"},
		{"obtain_it", "missing", "The business will obtain it from an outside source"},
	}
	for _, test := range tests {
		if got := EvidenceDispositionLabel(test.disposition, test.status); got != test.want {
			t.Errorf("EvidenceDispositionLabel(%q, %q) = %q, want %q", test.disposition, test.status, got, test.want)
		}
	}
}

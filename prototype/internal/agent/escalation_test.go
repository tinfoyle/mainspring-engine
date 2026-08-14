package agent

import (
	"strings"
	"testing"
)

func TestLegalComplianceWorkItemActionIsActionable(t *testing.T) {
	action := LegalComplianceWorkItemAction("state licenses")
	if action.ActionType != "tickets.create" || !strings.Contains(string(action.Payload), "Work plan") || !strings.Contains(string(action.Payload), "Definition of done") || !strings.Contains(string(action.Payload), "state licenses") {
		t.Fatalf("work item action = %#v", action)
	}
	if !MatchesLegalComplianceQuestion("Is this company legally compliant?") || MatchesLegalComplianceQuestion("How many jobs are open?") {
		t.Fatal("legal-compliance matcher returned an unexpected result")
	}
}

func TestMissingCredentialWorkItemActionTracksAcquisition(t *testing.T) {
	action := MissingCredentialWorkItemAction("What license do we need?", "business license")
	payload := string(action.Payload)
	for _, expected := range []string{"Confirm and acquire required business credentials", "Ask the owner", "fees", "application", "Upload the issued credential", "Definition of done", "business license"} {
		if !strings.Contains(payload, expected) {
			t.Fatalf("credential work item is missing %q: %s", expected, payload)
		}
	}
	if !MatchesExternalCredentialQuestion("What license do we need?") || !MatchesExternalCredentialQuestion("Do we have the required permit?") {
		t.Fatal("credential matcher missed an acquisition question")
	}
	if MatchesExternalCredentialQuestion("Summarize our sales report") || MatchesExternalCredentialQuestion("Explain this software license") {
		t.Fatal("credential matcher accepted an unrelated question")
	}
}

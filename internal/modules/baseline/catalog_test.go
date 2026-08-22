package baseline

import "testing"

func TestScopePolicySelectsExplainableProfilesAndSoloOmissions(t *testing.T) {
	tests := []struct {
		name    string
		facts   ScopeFacts
		profile string
		code    string
	}{
		{name: "software", facts: ScopeFacts{Industry: "SaaS", Services: "Cloud platform", TeamSize: "8"}, profile: "software", code: "security_access_controls"},
		{name: "field", facts: ScopeFacts{Industry: "HVAC contractor", TeamSize: "12"}, profile: "field_service", code: "licenses_permits"},
		{name: "professional", facts: ScopeFacts{Industry: "Accounting advisory", TeamSize: "4"}, profile: "professional_services", code: "quality_closeout"},
		{name: "retail", facts: ScopeFacts{Services: "Online store and retail", TeamSize: "5"}, profile: "retail", code: "fulfillment_inventory"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selection := SelectScope(test.facts)
			if selection.Profile != test.profile || selection.CatalogVersion != EvidenceCatalogVersion || selection.ScopePolicyVersion != ScopePolicyVersion || selection.Explanation == "" || !hasEvidenceCode(selection.Requirements, test.code) {
				t.Fatalf("selection=%+v", selection)
			}
		})
	}
	solo := SelectScope(ScopeFacts{Industry: "consulting", TeamSize: "solo"})
	if hasEvidenceCode(solo.Requirements, "workforce_records") || hasEvidenceCode(solo.Requirements, "role_responsibility") || len(solo.Requirements) != 11 {
		t.Fatalf("solo selection=%+v", solo)
	}
}

func TestCatalogAccessorsReturnStableCopies(t *testing.T) {
	questions := BaselineQuestions()
	catalog := EvidenceCatalog()
	if len(questions) != 7 || len(catalog) != 20 || catalog[0].Code == "" {
		t.Fatalf("questions=%d catalog=%d", len(questions), len(catalog))
	}
	questions[0].Prompt = "changed"
	catalog[0].ArtifactHints[0] = "changed"
	if BaselineQuestions()[0].Prompt == "changed" || EvidenceCatalog()[0].ArtifactHints[0] == "changed" {
		t.Fatal("catalog accessor leaked mutable backing data")
	}
}

func TestGovernedQuestionsExposeLookupAndRequiredCompleteness(t *testing.T) {
	question, ok := BaselineQuestion(" organization.legal_name ")
	if !ok || question.Optional || question.Prompt == "" || question.Explanation == "" {
		t.Fatalf("question=%+v ok=%v", question, ok)
	}
	if _, ok := BaselineQuestion("caller.selected.question"); ok {
		t.Fatal("unknown question was accepted")
	}
	answered := map[string]struct{}{}
	for _, value := range BaselineQuestions() {
		if !value.Optional {
			answered[value.Key] = struct{}{}
		}
	}
	if missing := MissingRequiredQuestions(answered); len(missing) != 0 {
		t.Fatalf("missing=%v", missing)
	}
	delete(answered, "organization.industry")
	if missing := MissingRequiredQuestions(answered); len(missing) != 1 || missing[0] != "organization.industry" {
		t.Fatalf("missing=%v", missing)
	}
}

func hasEvidenceCode(values []EvidenceDefinition, code string) bool {
	for _, value := range values {
		if value.Code == code {
			return true
		}
	}
	return false
}

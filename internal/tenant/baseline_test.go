package tenant

import (
	"strings"
	"testing"

	"github.com/tinfoyle/mainspring-engine/web/components"
)

func TestBaselineInterviewContract(t *testing.T) {
	questions := BaselineQuestions()
	if len(questions) != 7 {
		t.Fatalf("question count = %d, want 7", len(questions))
	}
	wanted := []string{"business_name", "website_url", "industry", "primary_location", "services", "team_size", "immediate_concern"}
	for index, key := range wanted {
		if questions[index].Key != key || strings.TrimSpace(questions[index].Prompt) == "" || strings.TrimSpace(questions[index].Explanation) == "" {
			t.Fatalf("question %d = %#v", index, questions[index])
		}
	}
}

func TestBaselineEvidenceSeedsCoverOperatingDomains(t *testing.T) {
	domains := map[string]bool{}
	for _, requirement := range baselineEvidenceSeeds {
		domains[requirement.Domain] = true
		if requirement.Key == "" || requirement.Label == "" || len(requirement.Artifacts) == 0 {
			t.Fatalf("incomplete evidence seed: %#v", requirement)
		}
	}
	for _, domain := range []string{
		"Business identity and ownership", "Legal, licensing, insurance, and compliance", "Financial controls and reporting",
		"Sales and pipeline", "Operations and service delivery", "Workforce and responsibilities", "Customer service and retention", "Strategy and ownership",
	} {
		if !domains[domain] {
			t.Fatalf("missing baseline domain %q", domain)
		}
	}
}

func TestBaselineEvidenceScopeAdaptsToSoftwareCompany(t *testing.T) {
	scope := BaselineEvidenceScopeForFacts([]BusinessFact{
		{Key: "industry", Value: "B2B software company"},
		{Key: "services", Value: "Subscription SaaS for independent retailers"},
		{Key: "team_size", Value: "8"},
	})
	if scope.Profile != "software" {
		t.Fatalf("profile = %q, want software", scope.Profile)
	}
	keys := evidenceScopeKeys(scope)
	for _, key := range []string{"product_definition", "software_delivery", "security_access_controls", "privacy_data_handling", "incident_continuity"} {
		if !keys[key] {
			t.Errorf("software scope is missing %q", key)
		}
	}
	for _, key := range []string{"licenses_permits", "service_workflow", "quality_closeout"} {
		if keys[key] {
			t.Errorf("software scope unexpectedly includes %q", key)
		}
	}
}

func TestBaselineEvidenceScopeKeepsFieldServiceRequirements(t *testing.T) {
	scope := BaselineEvidenceScopeForFacts([]BusinessFact{
		{Key: "industry", Value: "Residential plumbing and field service"},
		{Key: "services", Value: "Emergency repairs and water heater installation"},
		{Key: "team_size", Value: "7"},
	})
	if scope.Profile != "field_service" {
		t.Fatalf("profile = %q, want field_service", scope.Profile)
	}
	if len(scope.Requirements) != 14 {
		t.Fatalf("field-service requirement count = %d, want 14", len(scope.Requirements))
	}
	keys := evidenceScopeKeys(scope)
	for _, key := range []string{"licenses_permits", "insurance", "service_workflow", "quality_closeout", "workforce_records"} {
		if !keys[key] {
			t.Errorf("field-service scope is missing %q", key)
		}
	}
}

func TestBaselineEvidenceScopeOmitsTeamRecordsForSoloBusiness(t *testing.T) {
	scope := BaselineEvidenceScopeForFacts([]BusinessFact{
		{Key: "industry", Value: "Software as a service"},
		{Key: "team_size", Value: "1"},
	})
	keys := evidenceScopeKeys(scope)
	if keys["workforce_records"] || keys["role_responsibility"] {
		t.Fatalf("solo scope unexpectedly includes team records: %#v", keys)
	}
}

func evidenceScopeKeys(scope BaselineEvidenceScope) map[string]bool {
	keys := map[string]bool{}
	for _, requirement := range scope.Requirements {
		keys[requirement.Key] = true
	}
	return keys
}

func TestMatchBaselineEvidence(t *testing.T) {
	requirements := []EvidenceRequirement{{ID: "license-id", Key: "licenses_permits"}, {ID: "finance-id", Key: "financial_reporting"}}
	matches := MatchBaselineEvidence(requirements, "Attached is our contractor license and annual permit renewal.")
	if len(matches) != 1 || matches[0] != "license-id" {
		t.Fatalf("matches = %#v", matches)
	}
	if matches := MatchBaselineEvidence(requirements, "General company update with no supporting record"); len(matches) != 0 {
		t.Fatalf("unexpected matches = %#v", matches)
	}
}

func TestBaselineTaskTitlesReflectDisposition(t *testing.T) {
	tests := map[string]string{
		"have_it": "Locate and verify: License", "search_sources": "Search connected sources for: License",
		"create_it": "Create: License", "obtain_it": "Obtain: License", "": "Resolve: License",
	}
	for disposition, expected := range tests {
		if actual := baselineTaskTitle(disposition, "License"); actual != expected {
			t.Fatalf("baselineTaskTitle(%q) = %q, want %q", disposition, actual, expected)
		}
	}
}

func TestBaselineSourcesDeferThirdPartyAuthorizationUntilAfterOnboarding(t *testing.T) {
	sources := []components.BaselineSourceView{
		{Type: "uploads"}, {Type: "email"}, {Type: "google_drive"}, {Type: "public_web"},
	}
	deferred := components.BaselineDeferredSources(sources)
	if len(deferred) != 2 || deferred[0].Type != "email" || deferred[1].Type != "google_drive" {
		t.Fatalf("deferred sources = %#v", deferred)
	}
}

func TestInterpretEvidenceInterviewAnswer(t *testing.T) {
	tests := []struct {
		answer, choice, wantDisposition, wantResponsibility string
	}{
		{"We already have this in a binder", "", "have_it", "owner"},
		{"Please look for it", "", "search_sources", "agent"},
		{"We need to write this", "", "create_it", "agent"},
		{"This sounds like something we can create", "", "create_it", "agent"},
		{"We will create this", "", "create_it", "agent"},
		{"We need to obtain this from our insurer", "", "obtain_it", "shared"},
		{"This doesn't apply to us", "", "not_applicable", "owner"},
		{"I have no idea", "not_sure", "search_sources", "agent"},
	}
	for _, test := range tests {
		disposition, responsibility := interpretEvidenceInterviewAnswer(test.answer, test.choice, "shared")
		if disposition != test.wantDisposition || responsibility != test.wantResponsibility {
			t.Fatalf("interpretEvidenceInterviewAnswer(%q, %q) = %q/%q, want %q/%q", test.answer, test.choice, disposition, responsibility, test.wantDisposition, test.wantResponsibility)
		}
	}
}

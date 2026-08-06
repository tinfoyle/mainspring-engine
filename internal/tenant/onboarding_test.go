package tenant

import (
	"strings"
	"testing"

	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

func TestGenerateBlueprint(t *testing.T) {
	state := Onboarding{Business: BusinessProfile{
		BusinessName: "Acme Plumbing", Trade: "plumbing", ServiceArea: "Mecklenburg County",
	}}
	blueprint := GenerateBlueprint(state)
	if blueprint.Name != "Back Office" {
		t.Fatalf("blueprint name = %q", blueprint.Name)
	}
	if !strings.Contains(blueprint.Description, "Acme Plumbing") || !strings.Contains(blueprint.Description, "plumbing") {
		t.Fatalf("blueprint description does not contain business context: %q", blueprint.Description)
	}
	if len(blueprint.Personas) != 10 {
		t.Fatalf("persona count = %d, want 10", len(blueprint.Personas))
	}
	wantedRoles := map[string]bool{
		"Legal & Compliance Advisor": true, "Market Analyst": true, "Business Developer": true,
		"Customer Experience Manager": true, "HR & Safety Coordinator": true,
		"Estimator & Job Cost Analyst": true, "Procurement Specialist": true,
	}
	enabled := 0
	for _, persona := range blueprint.Personas {
		if persona.Key == "" || persona.Group == "" || persona.Mission == "" {
			t.Fatalf("incomplete persona blueprint: %#v", persona)
		}
		delete(wantedRoles, persona.Role)
		if persona.Enabled {
			enabled++
		}
	}
	if len(wantedRoles) != 0 {
		t.Fatalf("expanded persona catalog is missing roles: %#v", wantedRoles)
	}
	if enabled != 3 {
		t.Fatalf("default enabled persona count = %d, want lean core team of 3", enabled)
	}
}

func TestPersonaCapabilitiesRespectLaunchPermissions(t *testing.T) {
	permissions := PermissionPlan{ReadBusinessRecords: true, DraftCustomerEmail: true}
	capabilities := personaCapabilities("office_manager", permissions)
	wants := map[domain.Capability]bool{
		domain.CapabilityDocumentsRead: true,
		domain.CapabilityTicketRead:    true,
		domain.CapabilityTicketCreate:  true,
		domain.CapabilityScheduleRead:  true,
		domain.CapabilityEmailDraft:    true,
	}
	for _, capability := range capabilities {
		delete(wants, capability)
		if capability == domain.CapabilityEmailSend || capability == domain.CapabilityScheduleModify {
			t.Fatalf("unsafe capability granted during onboarding: %s", capability)
		}
	}
	if len(wants) != 0 {
		t.Fatalf("missing expected capabilities: %#v", wants)
	}
}

func TestPersonaCapabilitiesCanRemoveBusinessReadAccess(t *testing.T) {
	capabilities := personaCapabilities("dispatcher", PermissionPlan{ProposeScheduleEdits: true})
	for _, capability := range capabilities {
		if capability == domain.CapabilityDocumentsRead || capability == domain.CapabilityScheduleRead || capability == domain.CapabilityTicketRead {
			t.Fatalf("read capability granted when business record access is disabled: %s", capability)
		}
	}
	if len(capabilities) != 1 || capabilities[0] != domain.CapabilitySchedulePropose {
		t.Fatalf("dispatcher capabilities = %#v, want schedule proposal only", capabilities)
	}
}

func TestSpecialistCapabilitiesStayBounded(t *testing.T) {
	permissions := PermissionPlan{
		ReadBusinessRecords: true, ResearchPublicWeb: true, CommentOnDocuments: true,
		PrepareInvoiceDrafts: true, DraftCustomerEmail: true, ProposeScheduleEdits: true, ProposePayments: true,
	}
	blueprint := GenerateBlueprint(Onboarding{})
	unsafe := map[domain.Capability]bool{
		domain.CapabilityEmailSend: true, domain.CapabilityInvoiceIssue: true,
		domain.CapabilityPaymentExecute: true, domain.CapabilityScheduleModify: true,
	}
	for _, persona := range blueprint.Personas {
		for _, capability := range personaCapabilities(persona.Key, permissions) {
			if unsafe[capability] {
				t.Fatalf("%s received unsafe capability %s", persona.Role, capability)
			}
		}
	}

	legal := personaCapabilities("legal_advisor", permissions)
	for _, expected := range []domain.Capability{domain.CapabilityDocumentsRead, domain.CapabilityWebSearch, domain.CapabilityDocumentsComment} {
		found := false
		for _, capability := range legal {
			found = found || capability == expected
		}
		if !found {
			t.Fatalf("legal advisor missing capability %s: %#v", expected, legal)
		}
	}
	legalWithoutRecords := personaCapabilities("legal_advisor", PermissionPlan{ResearchPublicWeb: true, CommentOnDocuments: true})
	for _, capability := range legalWithoutRecords {
		if capability == domain.CapabilityDocumentsRead || capability == domain.CapabilityDocumentsComment {
			t.Fatalf("legal advisor received document capability without record access: %s", capability)
		}
	}
}

func TestPriorityCanRecommendSpecialist(t *testing.T) {
	blueprint := GenerateBlueprint(Onboarding{Priorities: []string{"Keep customers updated"}})
	found := false
	for _, persona := range blueprint.Personas {
		if persona.Key == "customer_experience" {
			found = true
			if !persona.Enabled {
				t.Fatal("customer experience specialist should be recommended for customer updates")
			}
		}
	}
	if !found {
		t.Fatal("customer experience specialist is missing")
	}
}

func TestPersonalizedInstructionsIncludeOperatingContext(t *testing.T) {
	state := Onboarding{
		Business:   BusinessProfile{BusinessName: "Acme Plumbing", Trade: "plumbing", ServiceArea: "Charlotte", Services: "service calls", TeamSize: 7},
		Operations: OperatingPlaybook{JobToInvoice: "Technicians leave paper tickets", BiggestBottleneck: "Missing job notes"},
		Priorities: []string{"Get completed work invoiced faster"},
	}
	instructions := personalizedInstructions(PersonaBlueprint{Mission: "Watch the office."}, state)
	for _, expected := range []string{"Acme Plumbing", "Technicians leave paper tickets", "Missing job notes", "Get completed work invoiced faster"} {
		if !strings.Contains(instructions, expected) {
			t.Fatalf("instructions missing %q: %s", expected, instructions)
		}
	}
}

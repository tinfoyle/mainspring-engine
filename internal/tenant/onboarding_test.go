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
	if len(blueprint.Personas) != 3 {
		t.Fatalf("persona count = %d, want 3", len(blueprint.Personas))
	}
	for _, persona := range blueprint.Personas {
		if !persona.Enabled || persona.Key == "" || persona.Mission == "" {
			t.Fatalf("incomplete persona blueprint: %#v", persona)
		}
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

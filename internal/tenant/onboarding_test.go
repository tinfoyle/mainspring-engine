package tenant

import (
	"strings"
	"testing"

	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

func TestSoftwareTemplateAcceptsLegacySaaSAlias(t *testing.T) {
	for _, value := range []string{"software", "saas", "SaaS"} {
		template := ParseBusinessTemplate(value)
		if template != TemplateSoftware || template.String() != "software" || !template.IsSoftware() {
			t.Fatalf("ParseBusinessTemplate(%q) = %q", value, template)
		}
	}
	if ParseBusinessTemplate("trades").IsSoftware() {
		t.Fatal("trades template must not be treated as software")
	}
}

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
	if len(blueprint.Personas) != 11 {
		t.Fatalf("persona count = %d, want 11", len(blueprint.Personas))
	}
	wantedRoles := map[string]bool{
		"Legal & Compliance Advisor": true, "Market Analyst": true, "Business Developer": true,
		"Customer Experience Manager": true, "HR & Safety Coordinator": true,
		"Estimator & Job Cost Analyst": true, "Procurement Specialist": true,
		"Website Advisor": true,
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

func TestGenerateSaaSBlueprint(t *testing.T) {
	state := Onboarding{Business: BusinessProfile{
		BusinessName: "Relay Cloud", Trade: "B2B SaaS", ServiceArea: "operations teams",
	}}
	blueprint := GenerateBlueprintForTemplate(state, TemplateSaaS)
	if blueprint.Name != "Company Operating Room" {
		t.Fatalf("blueprint name = %q", blueprint.Name)
	}
	if !strings.Contains(blueprint.Description, "Relay Cloud") || !strings.Contains(blueprint.Description, "B2B SaaS") {
		t.Fatalf("blueprint description does not contain company context: %q", blueprint.Description)
	}
	if len(blueprint.Personas) != 14 {
		t.Fatalf("persona count = %d, want 14", len(blueprint.Personas))
	}
	wantedRoles := map[string]bool{
		"Software Operations Manager": true, "Revenue & Finance Analyst": true, "Customer Success Lead": true,
		"Product Manager": true, "Engineering Manager": true, "Reliability Advisor": true,
		"Service Delivery Manager": true, "Technical Account Manager": true, "Cloud & Systems Advisor": true,
		"Growth Marketing Advisor": true, "Sales Development Advisor": true, "UX Researcher": true,
		"Website Conversion Advisor": true, "Security & Compliance Advisor": true,
	}
	enabled := 0
	for _, persona := range blueprint.Personas {
		delete(wantedRoles, persona.Role)
		if persona.Enabled {
			enabled++
		}
	}
	if len(wantedRoles) != 0 {
		t.Fatalf("SaaS catalog is missing roles: %#v", wantedRoles)
	}
	if enabled != 3 {
		t.Fatalf("default enabled persona count = %d, want 3", enabled)
	}
}

func TestSaaSPriorityCanRecommendSpecialist(t *testing.T) {
	blueprint := GenerateBlueprintForTemplate(Onboarding{Priorities: []string{
		"Improve customer onboarding and activation",
		"Monitor reliability, security, and incident follow-up",
	}}, TemplateSaaS)
	wanted := map[string]bool{"ux_researcher": false, "reliability_advisor": false}
	for _, persona := range blueprint.Personas {
		if _, ok := wanted[persona.Key]; ok {
			wanted[persona.Key] = persona.Enabled
		}
	}
	for key, enabled := range wanted {
		if !enabled {
			t.Fatalf("%s should be recommended for the selected SaaS priority", key)
		}
	}
}

func TestMSPBlueprintRecommendsServiceDelivery(t *testing.T) {
	state := Onboarding{Business: BusinessProfile{
		BusinessName: "Northstar Technology", Trade: "Managed service provider (MSP)", ServiceArea: "regional professional firms",
	}}
	blueprint := GenerateBlueprintForTemplate(state, TemplateSoftware)
	wanted := map[string]bool{
		"service_delivery_manager":  false,
		"technical_account_manager": false,
		"cloud_operations_advisor":  false,
	}
	for _, persona := range blueprint.Personas {
		if _, ok := wanted[persona.Key]; ok {
			wanted[persona.Key] = persona.Enabled
		}
	}
	if !wanted["service_delivery_manager"] {
		t.Fatal("an MSP should start with the service delivery manager recommended")
	}
	if wanted["technical_account_manager"] || wanted["cloud_operations_advisor"] {
		t.Fatalf("optional MSP specialists should remain owner-selected without a matching priority: %#v", wanted)
	}
}

func TestMSPCapabilitiesStayBounded(t *testing.T) {
	permissions := PermissionPlan{
		ReadBusinessRecords: true, ResearchPublicWeb: true, DraftCustomerEmail: true,
		ReadEmailInbox: true, SendEmail: true, ProposeScheduleEdits: true,
	}
	serviceDelivery := personaCapabilities("service_delivery_manager", permissions)
	wantedDelivery := map[domain.Capability]bool{
		domain.CapabilityTicketRead: true, domain.CapabilityTicketCreate: true,
		domain.CapabilityScheduleRead: true, domain.CapabilitySchedulePropose: true,
	}
	for _, capability := range serviceDelivery {
		delete(wantedDelivery, capability)
		if capability == domain.CapabilityEmailSend || capability == domain.CapabilityScheduleModify {
			t.Fatalf("service delivery manager received unsafe capability %s", capability)
		}
	}
	if len(wantedDelivery) != 0 {
		t.Fatalf("service delivery manager missing capabilities: %#v", wantedDelivery)
	}

	accountManager := personaCapabilities("technical_account_manager", permissions)
	wantedAccount := map[domain.Capability]bool{
		domain.CapabilityEmailRead: true, domain.CapabilityEmailDraft: true, domain.CapabilityEmailSend: true,
		domain.CapabilityWebSearch: true, domain.CapabilityTicketCreate: true,
	}
	for _, capability := range accountManager {
		delete(wantedAccount, capability)
	}
	if len(wantedAccount) != 0 {
		t.Fatalf("technical account manager missing explicitly granted capabilities: %#v", wantedAccount)
	}
}

func TestSaaSSpecialistCapabilitiesStayBounded(t *testing.T) {
	permissions := PermissionPlan{
		ReadBusinessRecords: true, ResearchPublicWeb: true, CommentOnDocuments: true,
		DraftCustomerEmail: true, ReadEmailInbox: true, SendEmail: true, ProposeScheduleEdits: true,
	}
	security := personaCapabilities("security_advisor", permissions)
	wantedSecurity := map[domain.Capability]bool{
		domain.CapabilityDocumentsRead: true, domain.CapabilityDocumentsComment: true,
		domain.CapabilityWebSearch: true, domain.CapabilityTicketCreate: true,
	}
	for _, capability := range security {
		delete(wantedSecurity, capability)
		if capability == domain.CapabilityEmailSend || capability == domain.CapabilityScheduleModify {
			t.Fatalf("security advisor received unsafe capability %s", capability)
		}
	}
	if len(wantedSecurity) != 0 {
		t.Fatalf("security advisor missing capabilities: %#v", wantedSecurity)
	}

	customerSuccess := personaCapabilities("customer_success", permissions)
	wantedCustomer := map[domain.Capability]bool{
		domain.CapabilityEmailRead: true, domain.CapabilityEmailDraft: true, domain.CapabilityEmailSend: true,
	}
	for _, capability := range customerSuccess {
		delete(wantedCustomer, capability)
	}
	if len(wantedCustomer) != 0 {
		t.Fatalf("customer success missing explicitly granted email capabilities: %#v", wantedCustomer)
	}
}

func TestWebsiteAdvisorHasReadOnlyWebCapabilities(t *testing.T) {
	capabilities := personaCapabilities("website_advisor", PermissionPlan{ResearchPublicWeb: true})
	wanted := map[domain.Capability]bool{domain.CapabilityWebRead: true, domain.CapabilityWebSearch: true}
	for _, capability := range capabilities {
		delete(wanted, capability)
		if capability == domain.CapabilityDocumentsComment || capability == domain.CapabilityEmailSend {
			t.Fatalf("website advisor received mutating capability %s", capability)
		}
	}
	if len(wanted) != 0 {
		t.Fatalf("website advisor missing capabilities: %#v", wanted)
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

func TestPersonaEmailSendRequiresExplicitPermission(t *testing.T) {
	capabilities := personaCapabilities("office_manager", PermissionPlan{ReadEmailInbox: true, SendEmail: true})
	wanted := map[domain.Capability]bool{domain.CapabilityEmailRead: true, domain.CapabilityEmailSend: true}
	for _, capability := range capabilities {
		delete(wanted, capability)
	}
	if len(wanted) != 0 {
		t.Fatalf("missing explicitly granted email capabilities: %#v", wanted)
	}
	for _, capability := range personaCapabilities("bookkeeper", PermissionPlan{ReadEmailInbox: true, SendEmail: true}) {
		if capability == domain.CapabilityEmailRead || capability == domain.CapabilityEmailSend {
			t.Fatalf("bookkeeper received communication capability %s", capability)
		}
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
		Business:   BusinessProfile{BusinessName: "Acme Plumbing", WebsiteURL: "https://acme.example", Trade: "plumbing", ServiceArea: "Charlotte", Services: "service calls", TeamSize: 7},
		Operations: OperatingPlaybook{JobToInvoice: "Technicians leave paper tickets", BiggestBottleneck: "Missing job notes"},
		Priorities: []string{"Get completed work invoiced faster"},
	}
	instructions := personalizedInstructions(PersonaBlueprint{Mission: "Watch the office."}, state)
	for _, expected := range []string{"Acme Plumbing", "https://acme.example", "Technicians leave paper tickets", "Missing job notes", "Get completed work invoiced faster"} {
		if !strings.Contains(instructions, expected) {
			t.Fatalf("instructions missing %q: %s", expected, instructions)
		}
	}
}

func TestSaaSPersonalizedInstructionsUseCompanyContext(t *testing.T) {
	state := Onboarding{
		Business: BusinessProfile{
			BusinessName: "Relay Cloud", WebsiteURL: "https://relay.example", Trade: "B2B SaaS",
			ServiceArea: "field service teams", Services: "workflow automation", TeamSize: 12,
		},
		Operations: OperatingPlaybook{
			LeadIntake:        "Trials come from content and referrals.",
			Scheduling:        "Roadmap planning runs monthly.",
			JobToInvoice:      "Releases are weekly and billing is subscription based.",
			BiggestBottleneck: "Activation is unclear.",
		},
		Priorities: []string{"Improve customer onboarding and activation"},
	}
	instructions := personalizedInstructionsForTemplate(PersonaBlueprint{Mission: "Watch the operating system."}, state, TemplateSaaS)
	for _, expected := range []string{
		"Relay Cloud", "https://relay.example", "Roadmap planning runs monthly", "subscription based",
		"Activation is unclear", "Improve customer onboarding and activation",
	} {
		if !strings.Contains(instructions, expected) {
			t.Fatalf("instructions missing %q: %s", expected, instructions)
		}
	}
	if strings.Contains(strings.ToLower(instructions), "technician") {
		t.Fatalf("SaaS instructions leaked trade-specific language: %s", instructions)
	}
}

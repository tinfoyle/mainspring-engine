package tenant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

const (
	OnboardingNotStarted = "not_started"
	OnboardingInProgress = "in_progress"
	OnboardingCompleted  = "completed"

	BusinessStageOperating = "operating"
	BusinessStageStarting  = "starting"
)

var ErrOnboardingNotFound = errors.New("tenant onboarding was not found")

type BusinessProfile struct {
	Template         string   `json:"template"`
	Variant          string   `json:"variant"`
	Stage            string   `json:"stage"`
	BusinessName     string   `json:"business_name"`
	WebsiteURL       string   `json:"website_url"`
	Trade            string   `json:"trade"`
	Services         string   `json:"services"`
	ServiceArea      string   `json:"service_area"`
	TimeZone         string   `json:"time_zone"`
	TeamSize         int      `json:"team_size"`
	CustomerMix      string   `json:"customer_mix"`
	WorkingHours     string   `json:"working_hours"`
	EmergencyService bool     `json:"emergency_service"`
	CurrentSystems   []string `json:"current_systems"`
}

func NormalizeBusinessStage(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), BusinessStageStarting) {
		return BusinessStageStarting
	}
	return BusinessStageOperating
}

func (profile BusinessProfile) IsStarting() bool {
	return NormalizeBusinessStage(profile.Stage) == BusinessStageStarting
}

type OperatingPlaybook struct {
	LeadIntake          string `json:"lead_intake"`
	Scheduling          string `json:"scheduling"`
	EstimateToJob       string `json:"estimate_to_job"`
	JobToInvoice        string `json:"job_to_invoice"`
	Payments            string `json:"payments"`
	VendorBills         string `json:"vendor_bills"`
	BiggestBottleneck   string `json:"biggest_bottleneck"`
	ImportantExceptions string `json:"important_exceptions"`
}

type PersonaBlueprint struct {
	Key          string   `json:"key"`
	Group        string   `json:"group"`
	Name         string   `json:"name"`
	Role         string   `json:"role"`
	Mission      string   `json:"mission"`
	Enabled      bool     `json:"enabled"`
	Capabilities []string `json:"capabilities"`
}

type BoardroomBlueprint struct {
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Personas    []PersonaBlueprint `json:"personas"`
}

type PermissionPlan struct {
	ReadBusinessRecords  bool `json:"read_business_records"`
	ResearchPublicWeb    bool `json:"research_public_web"`
	CommentOnDocuments   bool `json:"comment_on_documents"`
	PrepareInvoiceDrafts bool `json:"prepare_invoice_drafts"`
	DraftCustomerEmail   bool `json:"draft_customer_email"`
	ReadEmailInbox       bool `json:"read_email_inbox"`
	SendEmail            bool `json:"send_email"`
	ProposeScheduleEdits bool `json:"propose_schedule_edits"`
	ProposePayments      bool `json:"propose_payments"`
}

type Onboarding struct {
	Status      string
	CurrentStep int
	Business    BusinessProfile
	Operations  OperatingPlaybook
	Priorities  []string
	Blueprint   BoardroomBlueprint
	Permissions PermissionPlan
}

func DefaultPermissionPlan() PermissionPlan {
	return PermissionPlan{
		ReadBusinessRecords:  true,
		ResearchPublicWeb:    true,
		CommentOnDocuments:   true,
		PrepareInvoiceDrafts: true,
		DraftCustomerEmail:   true,
		ReadEmailInbox:       true,
		SendEmail:            false,
		ProposeScheduleEdits: true,
		ProposePayments:      true,
	}
}

func GenerateBlueprint(state Onboarding) BoardroomBlueprint {
	return GenerateBlueprintForTemplate(state, TemplateTrades)
}

func GenerateBlueprintForTemplate(state Onboarding, template BusinessTemplate) BoardroomBlueprint {
	businessName := strings.TrimSpace(state.Business.BusinessName)
	if businessName == "" {
		businessName = "your business"
	}
	trade := strings.TrimSpace(state.Business.Trade)
	if trade == "" {
		trade = "trade service"
	}
	if template.IsSoftware() {
		if strings.TrimSpace(state.Business.Trade) == "" {
			trade = "software or managed services"
		}
		return specializeBlueprintForBusinessStage(BoardroomBlueprint{
			Name:        "Company Operating Room",
			Description: fmt.Sprintf("The cross-functional operating team for %s, a %s business serving %s.", businessName, trade, fallback(state.Business.ServiceArea, "its target market")),
			Personas:    softwarePersonaBlueprints(state),
		}, state, template)
	}
	return specializeBlueprintForBusinessStage(BoardroomBlueprint{
		Name:        "Back Office",
		Description: fmt.Sprintf("The operational back office for %s, a %s business serving %s.", businessName, trade, fallback(state.Business.ServiceArea, "its local service area")),
		Personas: []PersonaBlueprint{
			{
				Key: "office_manager", Group: "Core operations", Name: "Morgan", Role: "Office Manager", Enabled: true,
				Mission:      "Coordinate the team, catch work falling through the cracks, and give the owner a short action list.",
				Capabilities: []string{"Read business documents", "Review and create internal tasks", "Read schedules", "Draft customer email"},
			},
			{
				Key: "bookkeeper", Group: "Core operations", Name: "Casey", Role: "Bookkeeper", Enabled: true,
				Mission:      "Watch invoicing, accounts receivable, vendor bills, and payment exceptions without moving money.",
				Capabilities: []string{"Read business documents", "Prepare invoice drafts", "Propose payments for approval"},
			},
			{
				Key: "dispatcher", Group: "Core operations", Name: "Riley", Role: "Dispatcher", Enabled: true,
				Mission:      "Review jobs, crews, appointments, and conflicts, then propose practical scheduling changes.",
				Capabilities: []string{"Read jobs and schedules", "Propose scheduling changes", "Create internal tasks"},
			},
			{
				Key: "business_developer", Group: "Growth and customers", Name: "Avery", Role: "Business Developer",
				Mission:      "Find practical growth opportunities, improve lead follow-up, and prepare outreach without contacting anyone directly.",
				Capabilities: []string{"Research markets and prospects", "Review lead records", "Draft outreach", "Create internal tasks"},
			},
			{
				Key: "market_analyst", Group: "Growth and customers", Name: "Taylor", Role: "Market Analyst",
				Mission:      "Track local demand, competitors, pricing signals, and service opportunities using evidence instead of guesswork.",
				Capabilities: []string{"Search the public web", "Read business documents", "Compare market evidence"},
			},
			{
				Key: "website_advisor", Group: "Growth and customers", Name: "Robin", Role: "Website Advisor",
				Mission:      "Review the business website as a prospective customer, compare it with credible local competitors, and recommend clear, practical improvements without editing or publishing anything.",
				Capabilities: []string{"View public website pages", "Research competitor websites", "Recommend content, trust, accessibility, and conversion improvements"},
			},
			{
				Key: "customer_experience", Group: "Growth and customers", Name: "Jamie", Role: "Customer Experience Manager",
				Mission:      "Watch customer communication, follow-ups, complaints, and promises so the company stays responsive.",
				Enabled:      hasPriority(state.Priorities, "Keep customers updated"),
				Capabilities: []string{"Review customer and job records", "Draft customer messages", "Create follow-up tasks"},
			},
			{
				Key: "legal_advisor", Group: "Risk and people", Name: "Jordan", Role: "Legal & Compliance Advisor",
				Mission:      "Spot legal and compliance questions, research authoritative sources, and flag when qualified counsel should review a decision.",
				Capabilities: []string{"Search the public web", "Read and comment on documents", "Identify legal questions"},
			},
			{
				Key: "hr_safety", Group: "Risk and people", Name: "Drew", Role: "HR & Safety Coordinator",
				Mission:      "Organize people policies, training, certifications, and safety follow-up while escalating employment and safety risks.",
				Capabilities: []string{"Research regulations", "Read and comment on documents", "Create compliance tasks"},
			},
			{
				Key: "estimator", Group: "Financial and supply", Name: "Parker", Role: "Estimator & Job Cost Analyst",
				Mission:      "Compare job scope, labor, materials, and past records to surface missing costs and weak assumptions before work is priced.",
				Capabilities: []string{"Research material and market context", "Review job records", "Prepare internal cost notes"},
			},
			{
				Key: "procurement", Group: "Financial and supply", Name: "Quinn", Role: "Procurement Specialist",
				Mission:      "Review purchasing needs, vendor information, lead times, and bill exceptions without placing orders or moving money.",
				Enabled:      hasPriority(state.Priorities, "Track bills and upcoming payments"),
				Capabilities: []string{"Research vendors", "Read bills and documents", "Propose payments", "Create internal tasks"},
			},
		},
	}, state, template)
}

func specializeBlueprintForBusinessStage(blueprint BoardroomBlueprint, state Onboarding, template BusinessTemplate) BoardroomBlueprint {
	if !state.Business.IsStarting() {
		return blueprint
	}
	blueprint.Name = "Launch Room"
	blueprint.Description = fmt.Sprintf(
		"The planning and launch team for %s, a new %s business being built for %s.",
		fallback(state.Business.BusinessName, "your business"), fallback(state.Business.Trade, "small business"),
		fallback(state.Business.ServiceArea, "its first customers"),
	)
	for index := range blueprint.Personas {
		persona := &blueprint.Personas[index]
		persona.Enabled = false
		if template.IsSoftware() {
			switch persona.Key {
			case "software_ops_manager":
				persona.Role = "Startup Operations Lead"
				persona.Mission = "Turn the founder's assumptions into a sequenced launch plan, surface dependencies, and keep the smallest useful operating system moving."
				persona.Enabled = true
			case "revenue_analyst":
				persona.Role = "Startup Finance & Revenue Planner"
				persona.Mission = "Model runway, pricing, recurring revenue, startup costs, and cash risks without moving money or presenting assumptions as facts."
				persona.Enabled = true
			case "customer_success":
				persona.Mission = "Design a practical first-customer onboarding and support motion before volume makes gaps expensive."
				persona.Enabled = hasPriority(state.Priorities, "Design sales and first-customer onboarding")
			case "product_manager":
				persona.Role = "Product & Market Strategist"
				persona.Mission = "Test the problem, ideal customer, product scope, and launch evidence before the founder commits to a large build."
				persona.Enabled = true
			case "growth_marketer", "website_advisor":
				persona.Enabled = hasPriority(state.Priorities, "Build the website and go-to-market plan")
			case "security_advisor":
				persona.Enabled = hasPriority(state.Priorities, "Set up legal, security, privacy, and compliance")
			}
			continue
		}
		switch persona.Key {
		case "office_manager":
			persona.Role = "Business Launch Coordinator"
			persona.Mission = "Turn the owner's idea into a sequenced launch checklist covering registrations, insurance, systems, vendors, and the first operating routines."
			persona.Enabled = true
		case "bookkeeper":
			persona.Role = "Startup Finance Planner"
			persona.Mission = "Model startup costs, pricing, margins, cash needs, and bookkeeping setup without moving money or treating estimates as settled facts."
			persona.Enabled = true
		case "dispatcher":
			persona.Mission = "Design a simple first scheduling and dispatch process before the business has enough work for exceptions to become chaos."
		case "business_developer":
			persona.Role = "Market & Customer Developer"
			persona.Mission = "Validate the target customer, demand, offer, and first sales motion through evidence and bounded outreach preparation."
			persona.Enabled = true
		case "legal_advisor":
			persona.Enabled = hasPriority(state.Priorities, "Set up licensing, insurance, legal, and compliance")
		case "estimator":
			persona.Enabled = hasPriority(state.Priorities, "Define offers, pricing, and target margins")
		case "website_advisor":
			persona.Enabled = hasPriority(state.Priorities, "Plan the website and launch marketing")
		}
	}
	return blueprint
}

func hasPriority(priorities []string, target string) bool {
	for _, priority := range priorities {
		if priority == target {
			return true
		}
	}
	return false
}

func (s *Store) GetOnboarding(ctx context.Context, tenantID domain.TenantID) (Onboarding, error) {
	var state Onboarding
	var businessJSON, operationsJSON, prioritiesJSON, blueprintJSON, permissionsJSON []byte
	err := s.pool.QueryRow(ctx, `
		SELECT status, current_step, business_profile, operating_playbook, priorities,
		       boardroom_blueprint, permission_plan
		FROM tenant_onboarding WHERE tenant_id = $1
	`, tenantID.String()).Scan(
		&state.Status, &state.CurrentStep, &businessJSON, &operationsJSON, &prioritiesJSON,
		&blueprintJSON, &permissionsJSON,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Onboarding{}, ErrOnboardingNotFound
	}
	if err != nil {
		return Onboarding{}, fmt.Errorf("load onboarding: %w", err)
	}
	for _, target := range []struct {
		name string
		data []byte
		to   any
	}{
		{"business profile", businessJSON, &state.Business},
		{"operating playbook", operationsJSON, &state.Operations},
		{"priorities", prioritiesJSON, &state.Priorities},
		{"boardroom blueprint", blueprintJSON, &state.Blueprint},
		{"permission plan", permissionsJSON, &state.Permissions},
	} {
		if err := json.Unmarshal(target.data, target.to); err != nil {
			return Onboarding{}, fmt.Errorf("decode %s: %w", target.name, err)
		}
	}
	return state, nil
}

func (s *Store) SaveOnboarding(ctx context.Context, tenantID domain.TenantID, state Onboarding) error {
	businessJSON, err := json.Marshal(state.Business)
	if err != nil {
		return err
	}
	operationsJSON, err := json.Marshal(state.Operations)
	if err != nil {
		return err
	}
	prioritiesJSON, err := json.Marshal(state.Priorities)
	if err != nil {
		return err
	}
	blueprintJSON, err := json.Marshal(state.Blueprint)
	if err != nil {
		return err
	}
	permissionsJSON, err := json.Marshal(state.Permissions)
	if err != nil {
		return err
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE tenant_onboarding
		SET status = $2, current_step = $3, business_profile = $4, operating_playbook = $5,
		    priorities = $6, boardroom_blueprint = $7, permission_plan = $8, updated_at = now()
		WHERE tenant_id = $1
	`, tenantID.String(), state.Status, state.CurrentStep, businessJSON, operationsJSON, prioritiesJSON, blueprintJSON, permissionsJSON)
	if err != nil {
		return fmt.Errorf("save onboarding: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrOnboardingNotFound
	}
	return nil
}

func (s *Store) CompleteOnboarding(ctx context.Context, tenantID domain.TenantID, state Onboarding) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin onboarding completion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var boardroomID string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM boardrooms WHERE status <> 'archived' ORDER BY created_at LIMIT 1 FOR UPDATE`).Scan(&boardroomID); err != nil {
		return fmt.Errorf("load onboarding boardroom: %w", err)
	}
	enabledPersonas := 0
	for _, persona := range state.Blueprint.Personas {
		if persona.Enabled {
			enabledPersonas++
		}
	}
	if enabledPersonas == 0 {
		return errors.New("complete onboarding: at least one persona must be enabled")
	}
	if _, err := tx.Exec(ctx, `
		UPDATE boardrooms SET name = $2, description = $3, max_turns = $4, updated_at = now() WHERE id = $1
	`, boardroomID, state.Blueprint.Name, state.Blueprint.Description, enabledPersonas); err != nil {
		return fmt.Errorf("update onboarding boardroom: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE tenant_settings SET display_name = $2, timezone = $3, updated_at = now() WHERE tenant_id = $1
	`, tenantID.String(), state.Business.BusinessName, state.Business.TimeZone); err != nil {
		return fmt.Errorf("update tenant business profile: %w", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE personas SET enabled = false, updated_at = now() WHERE boardroom_id = $1`, boardroomID); err != nil {
		return fmt.Errorf("disable previous personas: %w", err)
	}
	selectedTemplate := s.template
	if strings.TrimSpace(state.Business.Template) != "" {
		selectedTemplate = ParseBusinessTemplate(state.Business.Template)
	}
	for position, persona := range state.Blueprint.Personas {
		var personaID string
		instructions := personalizedInstructionsForTemplate(persona, state, selectedTemplate)
		if err := tx.QueryRow(ctx, `
			INSERT INTO personas (boardroom_id, name, role, system_instructions, position, enabled)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (boardroom_id, position) DO UPDATE
			SET name = EXCLUDED.name, role = EXCLUDED.role, system_instructions = EXCLUDED.system_instructions,
			    enabled = EXCLUDED.enabled, updated_at = now()
			RETURNING id::text
		`, boardroomID, persona.Name, persona.Role, instructions, position+1, persona.Enabled).Scan(&personaID); err != nil {
			return fmt.Errorf("apply persona %s: %w", persona.Role, err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM persona_tool_grants WHERE persona_id = $1`, personaID); err != nil {
			return fmt.Errorf("reset persona grants: %w", err)
		}
		if !persona.Enabled {
			continue
		}
		for _, capability := range personaCapabilities(persona.Key, state.Permissions) {
			if _, err := tx.Exec(ctx, `
				INSERT INTO persona_tool_grants (persona_id, capability) VALUES ($1, $2)
			`, personaID, string(capability)); err != nil {
				return fmt.Errorf("apply persona capability: %w", err)
			}
		}
	}

	state.Status = OnboardingCompleted
	state.CurrentStep = 6
	businessJSON, operationsJSON, prioritiesJSON, blueprintJSON, permissionsJSON, err := marshalOnboarding(state)
	if err != nil {
		return fmt.Errorf("encode completed onboarding: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE tenant_onboarding
		SET status = 'completed', current_step = 6, business_profile = $2, operating_playbook = $3,
		    priorities = $4, boardroom_blueprint = $5, permission_plan = $6,
		    completed_at = now(), updated_at = now()
		WHERE tenant_id = $1
	`, tenantID.String(), businessJSON, operationsJSON, prioritiesJSON, blueprintJSON, permissionsJSON); err != nil {
		return fmt.Errorf("complete onboarding: %w", err)
	}
	return tx.Commit(ctx)
}

func marshalOnboarding(state Onboarding) ([]byte, []byte, []byte, []byte, []byte, error) {
	businessJSON, err := json.Marshal(state.Business)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("business profile: %w", err)
	}
	operationsJSON, err := json.Marshal(state.Operations)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("operating playbook: %w", err)
	}
	prioritiesJSON, err := json.Marshal(state.Priorities)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("priorities: %w", err)
	}
	blueprintJSON, err := json.Marshal(state.Blueprint)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("boardroom blueprint: %w", err)
	}
	permissionsJSON, err := json.Marshal(state.Permissions)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("permission plan: %w", err)
	}
	return businessJSON, operationsJSON, prioritiesJSON, blueprintJSON, permissionsJSON, nil
}

func (s *Store) ResetOnboarding(ctx context.Context, tenantID domain.TenantID, displayName string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin onboarding reset: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		UPDATE tenant_onboarding
		SET status = 'not_started', current_step = 1, business_profile = '{}'::jsonb,
		    operating_playbook = '{}'::jsonb, priorities = '[]'::jsonb,
		    boardroom_blueprint = '{}'::jsonb, permission_plan = '{}'::jsonb,
		    completed_at = NULL, updated_at = now()
		WHERE tenant_id = $1
	`, tenantID.String()); err != nil {
		return fmt.Errorf("reset onboarding draft: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE tenant_settings SET display_name = $2, timezone = 'UTC', updated_at = now() WHERE tenant_id = $1
	`, tenantID.String(), strings.TrimSpace(displayName)); err != nil {
		return fmt.Errorf("reset tenant business profile: %w", err)
	}
	// Preserve connected source credentials, but close the current assessment
	// so the next visit begins a clean interview.
	if _, err := tx.Exec(ctx, `
		UPDATE baseline_assessments
		SET status = 'archived', updated_at = now()
		WHERE tenant_id = $1 AND status <> 'archived'
	`, tenantID.String()); err != nil {
		return fmt.Errorf("archive current business baseline: %w", err)
	}
	// Work items are tenant-local demo data. Clear both the visible queue and
	// ticket proposals from earlier agent runs so an old approval cannot
	// recreate stale work after the reset. The serial is reset as well so the
	// next onboarding plan looks like a new customer's first workspace.
	if _, err := tx.Exec(ctx, `DELETE FROM external_actions WHERE action_type = 'tickets.create'`); err != nil {
		return fmt.Errorf("clear demo ticket proposals: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM input_coordinator_messages`); err != nil {
		return fmt.Errorf("clear demo input conversation: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM business_knowledge_fact_history`); err != nil {
		return fmt.Errorf("clear demo business knowledge history: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM business_knowledge_facts`); err != nil {
		return fmt.Errorf("clear demo business knowledge: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM work_items`); err != nil {
		return fmt.Errorf("clear demo work queue: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT setval(pg_get_serial_sequence('work_items', 'number'), 1, false)`); err != nil {
		return fmt.Errorf("reset demo work item numbering: %w", err)
	}
	// A development reset must also restore a clean document corpus. Attachment
	// rows deliberately use RESTRICT so normal document deletion cannot silently
	// alter a conversation or run; the explicit demo reset removes those links
	// first. Document chunks and baseline evidence links then cascade safely.
	if _, err := tx.Exec(ctx, `DELETE FROM conversation_documents`); err != nil {
		return fmt.Errorf("clear conversation document attachments: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM boardroom_run_documents`); err != nil {
		return fmt.Errorf("clear boardroom run document attachments: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM documents`); err != nil {
		return fmt.Errorf("clear demo documents: %w", err)
	}
	if err := resetDefaultBoardroom(ctx, tx, s.template); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func personaCapabilities(key string, permissions PermissionPlan) []domain.Capability {
	// Public-web research is a baseline read-only capability for every agent.
	// Role-specific permissions below continue to govern business data and all
	// mutating actions.
	result := []domain.Capability{domain.CapabilityDocumentsRead, domain.CapabilityDocumentsWrite, domain.CapabilityWebSearch, domain.CapabilityWebRead, domain.CapabilityFinanceRead, domain.CapabilityFinanceManage}
	switch key {
	case "office_manager":
		result = append(result, domain.CapabilityTicketCreate)
		if permissions.ReadBusinessRecords {
			result = append(result, domain.CapabilityTicketRead, domain.CapabilityScheduleRead)
		}
		if permissions.DraftCustomerEmail {
			result = append(result, domain.CapabilityEmailDraft)
		}
		if permissions.ReadEmailInbox {
			result = append(result, domain.CapabilityEmailRead)
		}
		if permissions.SendEmail {
			result = append(result, domain.CapabilityEmailSend)
		}
	case "bookkeeper":
		if permissions.PrepareInvoiceDrafts {
			result = append(result, domain.CapabilityInvoicePrepare)
		}
		if permissions.ProposePayments {
			result = append(result, domain.CapabilityPaymentPropose)
		}
	case "dispatcher":
		if permissions.ReadBusinessRecords {
			result = append(result, domain.CapabilityScheduleRead, domain.CapabilityTicketRead)
		}
		if permissions.ProposeScheduleEdits {
			result = append(result, domain.CapabilitySchedulePropose)
		}
	case "business_developer":
		if permissions.ReadBusinessRecords {
			result = append(result, domain.CapabilityTicketRead)
		}
		result = append(result, domain.CapabilityTicketCreate)
		if permissions.DraftCustomerEmail {
			result = append(result, domain.CapabilityEmailDraft)
		}
		if permissions.ReadEmailInbox {
			result = append(result, domain.CapabilityEmailRead)
		}
		if permissions.SendEmail {
			result = append(result, domain.CapabilityEmailSend)
		}
	case "market_analyst":
	case "website_advisor":
	case "software_ops_manager", "saas_ops_manager":
		result = append(result, domain.CapabilityTicketCreate)
		if permissions.ReadBusinessRecords {
			result = append(result, domain.CapabilityTicketRead, domain.CapabilityScheduleRead)
		}
		if permissions.DraftCustomerEmail {
			result = append(result, domain.CapabilityEmailDraft)
		}
		if permissions.ReadEmailInbox {
			result = append(result, domain.CapabilityEmailRead)
		}
		if permissions.SendEmail {
			result = append(result, domain.CapabilityEmailSend)
		}
	case "revenue_analyst":
		if permissions.PrepareInvoiceDrafts {
			result = append(result, domain.CapabilityInvoicePrepare)
		}
		if permissions.ProposePayments {
			result = append(result, domain.CapabilityPaymentPropose)
		}
	case "customer_success":
		result = append(result, domain.CapabilityTicketCreate)
		if permissions.ReadBusinessRecords {
			result = append(result, domain.CapabilityTicketRead)
		}
		if permissions.DraftCustomerEmail {
			result = append(result, domain.CapabilityEmailDraft)
		}
		if permissions.ReadEmailInbox {
			result = append(result, domain.CapabilityEmailRead)
		}
		if permissions.SendEmail {
			result = append(result, domain.CapabilityEmailSend)
		}
	case "product_manager":
		result = append(result, domain.CapabilityTicketCreate)
		if permissions.ReadBusinessRecords {
			result = append(result, domain.CapabilityTicketRead)
		}
	case "engineering_manager":
		result = append(result, domain.CapabilityTicketCreate)
		if permissions.ReadBusinessRecords {
			result = append(result, domain.CapabilityTicketRead, domain.CapabilityScheduleRead)
		}
		if permissions.ProposeScheduleEdits {
			result = append(result, domain.CapabilitySchedulePropose)
		}
	case "service_delivery_manager":
		result = append(result, domain.CapabilityTicketCreate)
		if permissions.ReadBusinessRecords {
			result = append(result, domain.CapabilityTicketRead, domain.CapabilityScheduleRead)
		}
		if permissions.ProposeScheduleEdits {
			result = append(result, domain.CapabilitySchedulePropose)
		}
	case "technical_account_manager":
		result = append(result, domain.CapabilityTicketCreate)
		if permissions.ReadBusinessRecords {
			result = append(result, domain.CapabilityTicketRead)
		}
		if permissions.DraftCustomerEmail {
			result = append(result, domain.CapabilityEmailDraft)
		}
		if permissions.ReadEmailInbox {
			result = append(result, domain.CapabilityEmailRead)
		}
		if permissions.SendEmail {
			result = append(result, domain.CapabilityEmailSend)
		}
	case "reliability_advisor", "security_advisor", "cloud_operations_advisor":
		result = append(result, domain.CapabilityTicketCreate)
		if permissions.ReadBusinessRecords && permissions.CommentOnDocuments {
			result = append(result, domain.CapabilityDocumentsComment)
		}
	case "growth_marketer", "sales_developer":
		result = append(result, domain.CapabilityTicketCreate)
		if permissions.ReadBusinessRecords {
			result = append(result, domain.CapabilityTicketRead)
		}
		if permissions.DraftCustomerEmail {
			result = append(result, domain.CapabilityEmailDraft)
		}
		if permissions.ReadEmailInbox {
			result = append(result, domain.CapabilityEmailRead)
		}
		if permissions.SendEmail {
			result = append(result, domain.CapabilityEmailSend)
		}
	case "ux_researcher":
		if permissions.ReadBusinessRecords && permissions.CommentOnDocuments {
			result = append(result, domain.CapabilityDocumentsComment)
		}
	case "customer_experience":
		if permissions.ReadBusinessRecords {
			result = append(result, domain.CapabilityTicketRead)
		}
		result = append(result, domain.CapabilityTicketCreate)
		if permissions.DraftCustomerEmail {
			result = append(result, domain.CapabilityEmailDraft)
		}
		if permissions.ReadEmailInbox {
			result = append(result, domain.CapabilityEmailRead)
		}
		if permissions.SendEmail {
			result = append(result, domain.CapabilityEmailSend)
		}
	case "legal_advisor", "hr_safety":
		if permissions.ReadBusinessRecords && permissions.CommentOnDocuments {
			result = append(result, domain.CapabilityDocumentsComment)
		}
		if key == "hr_safety" {
			result = append(result, domain.CapabilityTicketCreate)
		}
	case "estimator":
		if permissions.ReadBusinessRecords {
			result = append(result, domain.CapabilityTicketRead)
		}
		result = append(result, domain.CapabilityTicketCreate)
	case "procurement":
		result = append(result, domain.CapabilityTicketCreate)
		if permissions.ProposePayments {
			result = append(result, domain.CapabilityPaymentPropose)
		}
	}
	return result
}

func personalizedInstructions(persona PersonaBlueprint, state Onboarding) string {
	return personalizedInstructionsForTemplate(persona, state, TemplateTrades)
}

func personalizedInstructionsForTemplate(persona PersonaBlueprint, state Onboarding, template BusinessTemplate) string {
	stageGuidance := "The business is operating today; distinguish documented facts from missing information."
	if state.Business.IsStarting() {
		stageGuidance = "This business is pre-launch. Treat every process, forecast, customer, price, and timeline as a hypothesis until the owner validates it. Do not invent current operations or imply that plans have already happened."
	}
	if template.IsSoftware() {
		return fmt.Sprintf(
			"%s\n\nBusiness stage: %s\n\nCompany context: %s is a %s software or IT services business serving %s. Website: %s. Products and services: %s. Team size: %d. Customer model: %s. Working rhythm: %s.\n\nOperating playbook: Demand and requests: %s Planning and service prioritization: %s Discovery or request to delivery: %s Delivery to customer and billing: %s Recurring billing, contracts, and renewals: %s Cloud, vendor, and subcontractor spend: %s Biggest bottleneck: %s Important exceptions: %s.\n\nPriorities: %s. Stay within granted capabilities; prepare or propose consequential actions for owner approval.",
			persona.Mission,
			stageGuidance,
			state.Business.BusinessName, state.Business.Trade, state.Business.ServiceArea,
			fallback(state.Business.WebsiteURL, "not yet provided"), fallback(state.Business.Services, "not yet documented"), state.Business.TeamSize,
			fallback(state.Business.CustomerMix, "not yet documented"), fallback(state.Business.WorkingHours, "not yet documented"),
			fallback(state.Operations.LeadIntake, "not yet documented"), fallback(state.Operations.Scheduling, "not yet documented"),
			fallback(state.Operations.EstimateToJob, "not yet documented"), fallback(state.Operations.JobToInvoice, "not yet documented"),
			fallback(state.Operations.Payments, "not yet documented"), fallback(state.Operations.VendorBills, "not yet documented"),
			fallback(state.Operations.BiggestBottleneck, "not yet documented"), fallback(state.Operations.ImportantExceptions, "none documented"),
			strings.Join(state.Priorities, "; "),
		)
	}
	return fmt.Sprintf(
		"%s\n\nBusiness stage: %s\n\nBusiness context: %s is a %s business serving %s. Website: %s. Services: %s. Team size: %d. Customer mix: %s. Working hours: %s.\n\nOperating playbook: Leads: %s Scheduling: %s Estimate to job: %s Job to invoice: %s Payments: %s Vendor bills: %s Biggest bottleneck: %s Important exceptions: %s.\n\nPriorities: %s. Stay within granted capabilities; prepare or propose consequential actions for owner approval.",
		persona.Mission,
		stageGuidance,
		state.Business.BusinessName, state.Business.Trade, state.Business.ServiceArea,
		fallback(state.Business.WebsiteURL, "not yet provided"), fallback(state.Business.Services, "not yet documented"), state.Business.TeamSize,
		fallback(state.Business.CustomerMix, "not yet documented"), fallback(state.Business.WorkingHours, "not yet documented"),
		fallback(state.Operations.LeadIntake, "not yet documented"), fallback(state.Operations.Scheduling, "not yet documented"),
		fallback(state.Operations.EstimateToJob, "not yet documented"), fallback(state.Operations.JobToInvoice, "not yet documented"),
		fallback(state.Operations.Payments, "not yet documented"), fallback(state.Operations.VendorBills, "not yet documented"),
		fallback(state.Operations.BiggestBottleneck, "not yet documented"), fallback(state.Operations.ImportantExceptions, "none documented"),
		strings.Join(state.Priorities, ", "),
	)
}

func fallback(value, defaultValue string) string {
	if strings.TrimSpace(value) == "" {
		return defaultValue
	}
	return strings.TrimSpace(value)
}

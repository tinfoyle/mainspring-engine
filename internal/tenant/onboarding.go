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
)

var ErrOnboardingNotFound = errors.New("tenant onboarding was not found")

type BusinessProfile struct {
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
	businessName := strings.TrimSpace(state.Business.BusinessName)
	if businessName == "" {
		businessName = "your business"
	}
	trade := strings.TrimSpace(state.Business.Trade)
	if trade == "" {
		trade = "trade service"
	}
	return BoardroomBlueprint{
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
	}
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
	for position, persona := range state.Blueprint.Personas {
		var personaID string
		instructions := personalizedInstructions(persona, state)
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
	if err := resetDefaultBoardroom(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func personaCapabilities(key string, permissions PermissionPlan) []domain.Capability {
	var result []domain.Capability
	if permissions.ReadBusinessRecords {
		result = append(result, domain.CapabilityDocumentsRead)
	}
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
		if permissions.ResearchPublicWeb {
			result = append(result, domain.CapabilityWebSearch)
		}
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
		if permissions.ResearchPublicWeb {
			result = append(result, domain.CapabilityWebSearch)
		}
	case "website_advisor":
		if permissions.ResearchPublicWeb {
			result = append(result, domain.CapabilityWebRead, domain.CapabilityWebSearch)
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
		if permissions.ResearchPublicWeb {
			result = append(result, domain.CapabilityWebSearch)
		}
		if permissions.ReadBusinessRecords && permissions.CommentOnDocuments {
			result = append(result, domain.CapabilityDocumentsComment)
		}
		if key == "hr_safety" {
			result = append(result, domain.CapabilityTicketCreate)
		}
	case "estimator":
		if permissions.ResearchPublicWeb {
			result = append(result, domain.CapabilityWebSearch)
		}
		if permissions.ReadBusinessRecords {
			result = append(result, domain.CapabilityTicketRead)
		}
		result = append(result, domain.CapabilityTicketCreate)
	case "procurement":
		if permissions.ResearchPublicWeb {
			result = append(result, domain.CapabilityWebSearch)
		}
		result = append(result, domain.CapabilityTicketCreate)
		if permissions.ProposePayments {
			result = append(result, domain.CapabilityPaymentPropose)
		}
	}
	return result
}

func personalizedInstructions(persona PersonaBlueprint, state Onboarding) string {
	return fmt.Sprintf(
		"%s\n\nBusiness context: %s is a %s business serving %s. Website: %s. Services: %s. Team size: %d. Customer mix: %s. Working hours: %s.\n\nOperating playbook: Leads: %s Scheduling: %s Estimate to job: %s Job to invoice: %s Payments: %s Vendor bills: %s Biggest bottleneck: %s Important exceptions: %s.\n\nPriorities: %s. Stay within granted capabilities; prepare or propose consequential actions for owner approval.",
		persona.Mission,
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

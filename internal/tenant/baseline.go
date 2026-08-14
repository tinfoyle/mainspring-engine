package tenant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

const (
	BaselinePhaseInterview    = "interview"
	BaselinePhaseSourceAccess = "source_access"
	BaselinePhaseInventory    = "inventory"
	BaselinePhaseGapReview    = "gap_review"
	BaselinePhasePlanApproval = "plan_approval"
	BaselinePhaseActive       = "active"
	BaselinePhaseReady        = "baseline_ready"
)

var (
	ErrBaselineNotFound   = errors.New("business baseline was not found")
	ErrBaselineTransition = errors.New("business baseline cannot make that transition")
)

type BaselineAssessment struct {
	ID                   string
	Status               string
	Phase                string
	CurrentQuestion      int
	Version              int
	LastAssessedAt       *time.Time
	NextReassessmentAt   *time.Time
	CompletedAt          *time.Time
	Messages             []BaselineMessage
	Facts                []BusinessFact
	Sources              []BaselineDataSource
	Requirements         []EvidenceRequirement
	PlanParentWorkItemID string
}

type BaselineMessage struct {
	ID          string
	Role        string
	QuestionKey string
	Body        string
	CreatedAt   time.Time
}

type BusinessFact struct {
	ID          string
	Key         string
	Value       string
	SourceType  string
	SourceRef   string
	Confidence  float64
	ConfirmedAt *time.Time
	UpdatedAt   time.Time
}

type BaselineDataSource struct {
	ID           string
	Type         string
	DisplayName  string
	Status       string
	Scope        map[string]any
	LastSyncedAt *time.Time
	LastError    string
}

type EvidenceRequirement struct {
	ID                    string
	Key                   string
	Domain                string
	Label                 string
	Rationale             string
	ExpectedArtifactTypes []string
	Required              bool
	Status                string
	Disposition           string
	Responsibility        string
	RenewalDueAt          *time.Time
	OwnerAnswer           string
	InterviewedAt         *time.Time
	Evidence              []EvidenceLink
	Research              []BaselineResearchRun
}

type BaselineResearchRun struct {
	ID        string
	Query     string
	Status    string
	LastError string
	CreatedAt time.Time
	Results   []BaselineResearchResult
}

type BaselineResearchResult struct {
	Title       string
	URL         string
	Description string
	CitationID  string
	RetrievedAt time.Time
}

type EvidenceLink struct {
	ID           string
	DocumentID   string
	DocumentName string
	SourceItemID string
	PublicURL    string
	Citation     string
	Confidence   float64
	VerifiedAt   *time.Time
}

type RecordSourceItemInput struct {
	SourceType string
	ExternalID string
	Name       string
	MediaType  string
	SourceURI  string
	ModifiedAt *time.Time
	SHA256     string
	Metadata   map[string]any
	DocumentID string
}

type baselineQuestion struct {
	Key         string
	Prompt      string
	Explanation string
	Optional    bool
}

var baselineQuestions = []baselineQuestion{
	{Key: "business_name", Prompt: "What name does the business operate under?", Explanation: "I will use this name in the baseline and every resulting work item."},
	{Key: "website_url", Prompt: "What is the business website? You can say “none” if there is not one yet.", Explanation: "A website gives me a public starting point without granting access to private systems.", Optional: true},
	{Key: "industry", Prompt: "What trade, industry, or business model best describes the company?", Explanation: "This determines which records, licenses, controls, and operating practices I should look for."},
	{Key: "primary_location", Prompt: "Where does the business primarily operate? Include the city and state or country.", Explanation: "Licensing, employment, tax, and insurance requirements depend on jurisdiction."},
	{Key: "services", Prompt: "What products or services produce revenue today?", Explanation: "This anchors the operating workflow and prevents generic recommendations."},
	{Key: "team_size", Prompt: "About how many people work in the business, including owners and regular contractors?", Explanation: "Team size changes the evidence and controls a business reasonably needs."},
	{Key: "immediate_concern", Prompt: "What is the most important uncertainty or operational problem you want Mainspring to resolve first?", Explanation: "I will prioritize the initial baseline plan around this concern."},
}

type evidenceSeed struct {
	Key, Domain, Label, Rationale, Responsibility string
	Artifacts                                     []string
}

type BaselineEvidenceScope struct {
	Profile      string
	Title        string
	Explanation  string
	Requirements []evidenceSeed
}

var baselineEvidenceSeeds = []evidenceSeed{
	{"identity_registration", "Business identity and ownership", "Business registration and ownership record", "Confirms the legal entity, trade names, standing, and accountable owners.", "owner", []string{"registration", "articles", "operating agreement"}},
	{"licenses_permits", "Legal, licensing, insurance, and compliance", "Required licenses and permits", "Shows the business and its workers are authorized for the services and jurisdictions involved.", "shared", []string{"license", "permit", "registration", "certificate"}},
	{"insurance", "Legal, licensing, insurance, and compliance", "Current insurance coverage", "Documents policy limits, exclusions, named insureds, and renewal dates.", "owner", []string{"certificate of insurance", "policy declarations"}},
	{"compliance_calendar", "Legal, licensing, insurance, and compliance", "Compliance and renewal calendar", "Turns licenses, filings, insurance, and recurring obligations into accountable dates.", "agent", []string{"renewal calendar", "filing schedule"}},
	{"financial_reporting", "Financial controls and reporting", "Recent financial statements", "Provides a documented view of revenue, margin, cash, and major expense categories.", "owner", []string{"profit and loss", "balance sheet", "cash flow"}},
	{"billing_collection", "Financial controls and reporting", "Billing and collection procedure", "Establishes how completed work becomes an invoice and how overdue balances are handled.", "shared", []string{"billing procedure", "accounts receivable aging"}},
	{"sales_pipeline", "Sales and pipeline", "Lead and sales pipeline record", "Shows how demand is captured, qualified, quoted, won, and lost.", "owner", []string{"pipeline export", "lead log", "sales process"}},
	{"service_workflow", "Operations and service delivery", "Service delivery workflow", "Documents the repeatable path from request through completion and closeout.", "agent", []string{"SOP", "workflow", "checklist"}},
	{"quality_closeout", "Operations and service delivery", "Quality and closeout checklist", "Defines the evidence needed before work is considered complete.", "agent", []string{"checklist", "quality policy"}},
	{"customer_terms", "Customer service and retention", "Customer terms and service commitments", "Clarifies promises, exclusions, escalation paths, and customer responsibilities.", "shared", []string{"contract", "terms", "service level agreement"}},
	{"customer_feedback", "Customer service and retention", "Customer issue and feedback record", "Makes recurring complaints, churn risks, and service recovery visible.", "owner", []string{"support export", "complaint log", "survey"}},
	{"role_responsibility", "Workforce and responsibilities", "Role and responsibility map", "Identifies who owns each operating decision and handoff.", "agent", []string{"organization chart", "role descriptions", "RACI"}},
	{"workforce_records", "Workforce and responsibilities", "Required workforce records", "Confirms onboarding, training, classification, safety, and policy evidence.", "owner", []string{"handbook", "training records", "contractor agreement"}},
	{"goals_scorecard", "Strategy and ownership", "Business goals and operating scorecard", "Connects owner priorities to measurable operational outcomes.", "shared", []string{"scorecard", "annual plan", "KPI report"}},
}

var softwareEvidenceSeeds = []evidenceSeed{
	{"identity_registration", "Business identity and ownership", "Business registration and ownership record", "Confirms the legal entity, trade names, standing, and accountable owners.", "owner", []string{"registration", "articles", "operating agreement"}},
	{"product_definition", "Product and commercial model", "Product, customer, and pricing definition", "Documents what the software does, who it serves, how it is packaged, and how the business earns revenue.", "shared", []string{"product brief", "service catalog", "pricing", "roadmap"}},
	{"financial_reporting", "Financial controls and reporting", "Recent financial statements", "Provides a documented view of revenue, recurring revenue, margin, cash, and major expense categories.", "owner", []string{"profit and loss", "balance sheet", "cash flow", "revenue report"}},
	{"billing_collection", "Financial controls and reporting", "Subscription billing and collection procedure", "Establishes how subscriptions or projects become invoices, how renewals are handled, and how overdue balances are managed.", "shared", []string{"billing procedure", "subscription report", "accounts receivable aging"}},
	{"sales_pipeline", "Sales and pipeline", "Lead and sales pipeline record", "Shows how demand is captured, qualified, demonstrated, contracted, won, and lost.", "owner", []string{"pipeline export", "lead log", "sales process"}},
	{"software_delivery", "Product and service delivery", "Development, release, and change workflow", "Documents how software changes are planned, reviewed, tested, released, and rolled back.", "agent", []string{"development workflow", "release checklist", "change policy", "deployment runbook"}},
	{"security_access_controls", "Security, privacy, and resilience", "Information security and access controls", "Identifies systems, privileged access, authentication standards, security ownership, and review practices.", "shared", []string{"security policy", "access control policy", "system inventory", "access review"}},
	{"privacy_data_handling", "Security, privacy, and resilience", "Privacy and customer-data handling record", "Documents what personal or customer data is collected, where it is stored, why it is used, and how requests and retention are handled.", "shared", []string{"privacy policy", "data inventory", "retention policy", "data processing agreement"}},
	{"incident_continuity", "Security, privacy, and resilience", "Incident response, backup, and continuity plan", "Defines how the company detects incidents, communicates with customers, restores service, and learns from failures.", "agent", []string{"incident response plan", "backup policy", "disaster recovery plan", "status procedure"}},
	{"customer_terms", "Customer service and retention", "Customer agreements and service commitments", "Clarifies subscriptions, acceptable use, privacy terms, support levels, exclusions, and customer responsibilities.", "shared", []string{"terms of service", "master service agreement", "service level agreement", "data processing agreement"}},
	{"customer_feedback", "Customer service and retention", "Support, customer issue, and feedback record", "Makes recurring support demand, product friction, churn risks, and service recovery visible.", "owner", []string{"support export", "issue log", "survey", "churn report"}},
	{"role_responsibility", "Workforce and responsibilities", "Role and responsibility map", "Identifies who owns product, engineering, security, support, revenue, and operating decisions.", "agent", []string{"organization chart", "role descriptions", "RACI"}},
	{"workforce_records", "Workforce and responsibilities", "Employment and contractor records", "Confirms onboarding, confidentiality, intellectual-property assignment, classification, and policy acknowledgement.", "owner", []string{"handbook", "employment agreement", "contractor agreement", "IP assignment"}},
	{"goals_scorecard", "Strategy and ownership", "Business goals and operating scorecard", "Connects owner priorities to measurable product, customer, revenue, reliability, and operating outcomes.", "shared", []string{"scorecard", "annual plan", "KPI report"}},
}

var professionalServiceEvidenceSeeds = []evidenceSeed{
	baselineEvidenceSeeds[0], baselineEvidenceSeeds[2], baselineEvidenceSeeds[3], baselineEvidenceSeeds[4], baselineEvidenceSeeds[5], baselineEvidenceSeeds[6],
	{"service_workflow", "Operations and service delivery", "Client delivery workflow", "Documents the repeatable path from discovery and scope through delivery, review, and handoff.", "agent", []string{"SOP", "workflow", "engagement checklist"}},
	{"quality_closeout", "Operations and service delivery", "Engagement quality and completion review", "Defines the review, acceptance, and evidence required before client work is considered complete.", "agent", []string{"quality checklist", "acceptance record", "completion review"}},
	baselineEvidenceSeeds[9], baselineEvidenceSeeds[10], baselineEvidenceSeeds[11], baselineEvidenceSeeds[12], baselineEvidenceSeeds[13],
}

var retailEvidenceSeeds = []evidenceSeed{
	baselineEvidenceSeeds[0], baselineEvidenceSeeds[1], baselineEvidenceSeeds[2], baselineEvidenceSeeds[3], baselineEvidenceSeeds[4], baselineEvidenceSeeds[5], baselineEvidenceSeeds[6],
	{"fulfillment_inventory", "Operations and service delivery", "Inventory, fulfillment, and returns workflow", "Documents how products are sourced or stocked, sold, delivered, reconciled, returned, and refunded.", "shared", []string{"inventory report", "fulfillment procedure", "returns policy"}},
	baselineEvidenceSeeds[9], baselineEvidenceSeeds[10], baselineEvidenceSeeds[11], baselineEvidenceSeeds[12], baselineEvidenceSeeds[13],
}

func BaselineEvidenceScopeForFacts(facts []BusinessFact) BaselineEvidenceScope {
	values := make(map[string]string, len(facts))
	for _, fact := range facts {
		values[fact.Key] = fact.Value
	}
	return baselineEvidenceScope(values)
}

func baselineEvidenceScope(facts map[string]string) BaselineEvidenceScope {
	description := strings.ToLower(strings.Join([]string{facts["industry"], facts["services"], facts["immediate_concern"]}, " "))
	teamSize, teamSizeErr := strconv.Atoi(strings.TrimSpace(facts["team_size"]))

	var scope BaselineEvidenceScope
	switch {
	case containsAny(description, "software", "saas", "app developer", "application developer", "technology platform", "cloud platform", "web platform", "mobile app"):
		scope = BaselineEvidenceScope{
			Profile: "software", Title: "Software and SaaS business",
			Explanation:  "Based on the business model and revenue description, Mia prioritized product definition, software delivery, security, privacy, resilience, subscriptions, customer agreements, and operating controls. Trade licensing, field-service closeout, and job-site records were left out.",
			Requirements: append([]evidenceSeed(nil), softwareEvidenceSeeds...),
		}
	case containsAny(description, "plumb", "hvac", "electric", "contractor", "construction", "roof", "landscap", "field service", "repair service", "home service"):
		scope = BaselineEvidenceScope{
			Profile: "field_service", Title: "Trade and field-service business",
			Explanation:  "Mia included jurisdictional licenses, insurance, field delivery, quality closeout, safety and workforce records because the company performs work at customer sites.",
			Requirements: append([]evidenceSeed(nil), baselineEvidenceSeeds...),
		}
	case containsAny(description, "consult", "agency", "accounting", "bookkeep", "professional service", "advisory", "design studio", "marketing service"):
		scope = BaselineEvidenceScope{
			Profile: "professional_services", Title: "Professional-services business",
			Explanation:  "Mia prioritized engagement scope, client delivery, professional risk, quality review, billing, customer commitments, and accountable ownership. Field-service permits and technician closeout records were left out.",
			Requirements: append([]evidenceSeed(nil), professionalServiceEvidenceSeeds...),
		}
	case containsAny(description, "retail", "ecommerce", "e-commerce", "online store", "shop", "consumer product", "merchant"):
		scope = BaselineEvidenceScope{
			Profile: "retail", Title: "Retail and commerce business",
			Explanation:  "Mia prioritized sales authorization, insurance, inventory, fulfillment, returns, customer terms, financial controls, and operating ownership.",
			Requirements: append([]evidenceSeed(nil), retailEvidenceSeeds...),
		}
	default:
		scope = BaselineEvidenceScope{
			Profile: "general", Title: "General service business",
			Explanation:  "Mia selected a balanced operating baseline from the industry and services described. Each topic can still be marked not applicable during the interview.",
			Requirements: append([]evidenceSeed(nil), professionalServiceEvidenceSeeds...),
		}
	}
	if (teamSizeErr == nil && teamSize <= 1) || strings.Contains(strings.ToLower(facts["team_size"]), "solo") {
		filtered := scope.Requirements[:0]
		for _, requirement := range scope.Requirements {
			if requirement.Key != "workforce_records" && requirement.Key != "role_responsibility" {
				filtered = append(filtered, requirement)
			}
		}
		scope.Requirements = filtered
		scope.Explanation += " Team-specific records were omitted because the company currently operates solo."
	}
	return scope
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func BaselineQuestions() []baselineQuestion {
	return append([]baselineQuestion(nil), baselineQuestions...)
}

func (assessment BaselineAssessment) CurrentPrompt() (baselineQuestion, bool) {
	if assessment.CurrentQuestion < 0 || assessment.CurrentQuestion >= len(baselineQuestions) {
		return baselineQuestion{}, false
	}
	return baselineQuestions[assessment.CurrentQuestion], true
}

func (s *Store) EnsureBaseline(ctx context.Context, tenantID domain.TenantID) (BaselineAssessment, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return BaselineAssessment{}, fmt.Errorf("begin baseline: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var assessmentID string
	err = tx.QueryRow(ctx, `
		SELECT id::text FROM baseline_assessments
		WHERE tenant_id=$1 AND status <> 'archived'
		ORDER BY created_at DESC LIMIT 1 FOR UPDATE
	`, tenantID.String()).Scan(&assessmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.QueryRow(ctx, `
			INSERT INTO baseline_assessments (tenant_id) VALUES ($1) RETURNING id::text
		`, tenantID.String()).Scan(&assessmentID); err != nil {
			return BaselineAssessment{}, fmt.Errorf("create baseline: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO baseline_interview_messages (assessment_id, role, question_key, body)
			VALUES ($1, 'agent', $2, $3)
		`, assessmentID, baselineQuestions[0].Key, baselineQuestions[0].Prompt); err != nil {
			return BaselineAssessment{}, fmt.Errorf("start baseline interview: %w", err)
		}
	} else if err != nil {
		return BaselineAssessment{}, fmt.Errorf("load current baseline: %w", err)
	}

	for _, source := range []struct {
		typeName, displayName, status string
		scope                         map[string]any
	}{
		{"uploads", "File uploads", "connected", map[string]any{"read_only": true}},
		{"email", "Email inbox", "available", map[string]any{"read_only": true, "folders": []string{"INBOX"}, "max_items": 100}},
		{"google_drive", "Google Drive", "available", map[string]any{"read_only": true, "folders": []string{}}},
		{"public_web", "Public web", "connected", map[string]any{"read_only": true, "authoritative_sources_only": true}},
	} {
		scope, _ := json.Marshal(source.scope)
		if _, err := tx.Exec(ctx, `
			INSERT INTO baseline_data_sources (tenant_id, source_type, display_name, status, scope)
			VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (tenant_id, source_type) DO NOTHING
		`, tenantID.String(), source.typeName, source.displayName, source.status, scope); err != nil {
			return BaselineAssessment{}, fmt.Errorf("ensure baseline source: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return BaselineAssessment{}, fmt.Errorf("commit baseline: %w", err)
	}
	return s.GetBaseline(ctx, tenantID)
}

func (s *Store) GetBaseline(ctx context.Context, tenantID domain.TenantID) (BaselineAssessment, error) {
	var assessment BaselineAssessment
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, status, phase, current_question, baseline_version,
		       last_assessed_at, next_reassessment_at, completed_at,
		       COALESCE((SELECT bwi.work_item_id::text FROM baseline_work_items bwi
		                 WHERE bwi.assessment_id=ba.id AND bwi.requirement_id IS NULL LIMIT 1), '')
		FROM baseline_assessments ba
		WHERE tenant_id=$1 AND status <> 'archived'
		ORDER BY created_at DESC LIMIT 1
	`, tenantID.String()).Scan(
		&assessment.ID, &assessment.Status, &assessment.Phase, &assessment.CurrentQuestion, &assessment.Version,
		&assessment.LastAssessedAt, &assessment.NextReassessmentAt, &assessment.CompletedAt, &assessment.PlanParentWorkItemID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return BaselineAssessment{}, ErrBaselineNotFound
	}
	if err != nil {
		return BaselineAssessment{}, fmt.Errorf("load baseline: %w", err)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id::text, role, question_key, body, created_at
		FROM baseline_interview_messages WHERE assessment_id=$1 ORDER BY message_order
	`, assessment.ID)
	if err != nil {
		return BaselineAssessment{}, fmt.Errorf("load baseline messages: %w", err)
	}
	for rows.Next() {
		var message BaselineMessage
		if err := rows.Scan(&message.ID, &message.Role, &message.QuestionKey, &message.Body, &message.CreatedAt); err != nil {
			rows.Close()
			return BaselineAssessment{}, fmt.Errorf("scan baseline message: %w", err)
		}
		assessment.Messages = append(assessment.Messages, message)
	}
	rows.Close()

	factRows, err := s.pool.Query(ctx, `
		SELECT id::text, fact_key, value, source_type, source_ref, confidence::float8, confirmed_at, updated_at
		FROM business_facts WHERE assessment_id=$1 ORDER BY created_at, fact_key
	`, assessment.ID)
	if err != nil {
		return BaselineAssessment{}, fmt.Errorf("load baseline facts: %w", err)
	}
	for factRows.Next() {
		var fact BusinessFact
		if err := factRows.Scan(&fact.ID, &fact.Key, &fact.Value, &fact.SourceType, &fact.SourceRef, &fact.Confidence, &fact.ConfirmedAt, &fact.UpdatedAt); err != nil {
			factRows.Close()
			return BaselineAssessment{}, fmt.Errorf("scan baseline fact: %w", err)
		}
		assessment.Facts = append(assessment.Facts, fact)
	}
	factRows.Close()

	sourceRows, err := s.pool.Query(ctx, `
		SELECT id::text, source_type, display_name, status, scope, last_synced_at, COALESCE(last_error, '')
		FROM baseline_data_sources WHERE tenant_id=$1 ORDER BY source_type
	`, tenantID.String())
	if err != nil {
		return BaselineAssessment{}, fmt.Errorf("load baseline sources: %w", err)
	}
	for sourceRows.Next() {
		var source BaselineDataSource
		var scopeJSON []byte
		if err := sourceRows.Scan(&source.ID, &source.Type, &source.DisplayName, &source.Status, &scopeJSON, &source.LastSyncedAt, &source.LastError); err != nil {
			sourceRows.Close()
			return BaselineAssessment{}, fmt.Errorf("scan baseline source: %w", err)
		}
		_ = json.Unmarshal(scopeJSON, &source.Scope)
		assessment.Sources = append(assessment.Sources, source)
	}
	sourceRows.Close()

	requirementRows, err := s.pool.Query(ctx, `
		SELECT id::text, requirement_key, domain, label, rationale, expected_artifact_types,
		       required, status, disposition, responsibility, renewal_due_at, owner_answer, interviewed_at
		FROM evidence_requirements WHERE assessment_id=$1 ORDER BY domain, created_at
	`, assessment.ID)
	if err != nil {
		return BaselineAssessment{}, fmt.Errorf("load baseline requirements: %w", err)
	}
	for requirementRows.Next() {
		var requirement EvidenceRequirement
		if err := requirementRows.Scan(
			&requirement.ID, &requirement.Key, &requirement.Domain, &requirement.Label, &requirement.Rationale,
			&requirement.ExpectedArtifactTypes, &requirement.Required, &requirement.Status, &requirement.Disposition,
			&requirement.Responsibility, &requirement.RenewalDueAt, &requirement.OwnerAnswer, &requirement.InterviewedAt,
		); err != nil {
			requirementRows.Close()
			return BaselineAssessment{}, fmt.Errorf("scan baseline requirement: %w", err)
		}
		evidenceRows, err := s.pool.Query(ctx, `
			SELECT el.id::text, COALESCE(el.document_id::text, ''), COALESCE(d.name, ''),
			       COALESCE(el.source_item_id::text, ''), el.public_url, el.citation,
			       el.confidence::float8, el.verified_at
			FROM evidence_links el LEFT JOIN documents d ON d.id=el.document_id
			WHERE el.requirement_id=$1 ORDER BY el.created_at
		`, requirement.ID)
		if err != nil {
			requirementRows.Close()
			return BaselineAssessment{}, fmt.Errorf("load evidence links: %w", err)
		}
		for evidenceRows.Next() {
			var link EvidenceLink
			if err := evidenceRows.Scan(&link.ID, &link.DocumentID, &link.DocumentName, &link.SourceItemID, &link.PublicURL, &link.Citation, &link.Confidence, &link.VerifiedAt); err != nil {
				evidenceRows.Close()
				requirementRows.Close()
				return BaselineAssessment{}, fmt.Errorf("scan evidence link: %w", err)
			}
			requirement.Evidence = append(requirement.Evidence, link)
		}
		evidenceRows.Close()
		researchRows, err := s.pool.Query(ctx, `
			SELECT id::text, query, status, COALESCE(last_error,''), created_at
			FROM baseline_research_runs WHERE requirement_id=$1 ORDER BY created_at DESC LIMIT 5
		`, requirement.ID)
		if err != nil {
			requirementRows.Close()
			return BaselineAssessment{}, fmt.Errorf("load baseline research: %w", err)
		}
		for researchRows.Next() {
			var run BaselineResearchRun
			if err := researchRows.Scan(&run.ID, &run.Query, &run.Status, &run.LastError, &run.CreatedAt); err != nil {
				researchRows.Close()
				requirementRows.Close()
				return BaselineAssessment{}, fmt.Errorf("scan baseline research: %w", err)
			}
			resultRows, err := s.pool.Query(ctx, `
				SELECT title,url,description,citation_id,retrieved_at
				FROM baseline_research_results WHERE research_run_id=$1 ORDER BY created_at
			`, run.ID)
			if err != nil {
				researchRows.Close()
				requirementRows.Close()
				return BaselineAssessment{}, fmt.Errorf("load baseline research results: %w", err)
			}
			for resultRows.Next() {
				var result BaselineResearchResult
				if err := resultRows.Scan(&result.Title, &result.URL, &result.Description, &result.CitationID, &result.RetrievedAt); err != nil {
					resultRows.Close()
					researchRows.Close()
					requirementRows.Close()
					return BaselineAssessment{}, fmt.Errorf("scan baseline research result: %w", err)
				}
				run.Results = append(run.Results, result)
			}
			resultRows.Close()
			requirement.Research = append(requirement.Research, run)
		}
		researchRows.Close()
		assessment.Requirements = append(assessment.Requirements, requirement)
	}
	requirementRows.Close()
	return assessment, nil
}

func (s *Store) BeginBaselineResearch(ctx context.Context, tenantID domain.TenantID, requirementID, query string) (string, error) {
	query = strings.TrimSpace(query)
	if _, err := uuid.Parse(requirementID); err != nil || query == "" || len(query) > 500 {
		return "", errors.New("baseline research request is invalid")
	}
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO baseline_research_runs (assessment_id,requirement_id,query)
		SELECT er.assessment_id,er.id,$3
		FROM evidence_requirements er JOIN baseline_assessments ba ON ba.id=er.assessment_id
		WHERE ba.tenant_id=$1 AND er.id=$2 AND ba.status <> 'archived'
		RETURNING id::text
	`, tenantID.String(), requirementID, query).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrBaselineNotFound
	}
	return id, err
}

func (s *Store) CompleteBaselineResearch(ctx context.Context, runID string, results []BaselineResearchResult, researchErr error) error {
	if _, err := uuid.Parse(runID); err != nil {
		return errors.New("baseline research run is invalid")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if researchErr != nil {
		if _, err := tx.Exec(ctx, `UPDATE baseline_research_runs SET status='failed',last_error=$2,completed_at=now() WHERE id=$1`, runID, researchErr.Error()); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	for _, result := range results {
		if _, err := tx.Exec(ctx, `
			INSERT INTO baseline_research_results (research_run_id,title,url,description,citation_id,retrieved_at)
			VALUES ($1,$2,$3,$4,$5,$6)
		`, runID, result.Title, result.URL, result.Description, result.CitationID, result.RetrievedAt); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE baseline_research_runs SET status='completed',completed_at=now() WHERE id=$1`, runID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) AnswerBaselineQuestion(ctx context.Context, tenantID domain.TenantID, answer string) error {
	answer = strings.TrimSpace(answer)
	if answer == "" || len(answer) > 2000 {
		return errors.New("an interview answer must contain between 1 and 2000 characters")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var assessmentID, phase string
	var current int
	if err := tx.QueryRow(ctx, `
		SELECT id::text, phase, current_question FROM baseline_assessments
		WHERE tenant_id=$1 AND status <> 'archived' FOR UPDATE
	`, tenantID.String()).Scan(&assessmentID, &phase, &current); err != nil {
		return ErrBaselineNotFound
	}
	if phase != BaselinePhaseInterview || current >= len(baselineQuestions) {
		return ErrBaselineTransition
	}
	question := baselineQuestions[current]
	if question.Key == "team_size" && !strings.EqualFold(answer, "not sure") {
		value, err := strconv.Atoi(answer)
		if err != nil || value < 1 || value > 100000 {
			return errors.New("team size must be a number between 1 and 100000, or “not sure”")
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO baseline_interview_messages (assessment_id, role, question_key, body)
		VALUES ($1, 'user', $2, $3)
	`, assessmentID, question.Key, answer); err != nil {
		return fmt.Errorf("save interview answer: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO business_facts (assessment_id, fact_key, value, source_type, confidence, confirmed_at)
		VALUES ($1,$2,$3,'owner',1,now())
		ON CONFLICT (assessment_id, fact_key) DO UPDATE
		SET value=EXCLUDED.value, source_type='owner', source_ref='', confidence=1,
		    confirmed_at=now(), updated_at=now()
	`, assessmentID, question.Key, answer); err != nil {
		return fmt.Errorf("save interview fact: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO business_knowledge_facts(fact_key,label,value,source_type,source_ref,confidence,confirmed_at)
		VALUES ($1,$2,$3,'owner',$4,1,now())
		ON CONFLICT (fact_key) DO UPDATE SET label=EXCLUDED.label,value=EXCLUDED.value,source_type='owner',
		    source_ref=EXCLUDED.source_ref,confidence=1,status='active',confirmed_at=now(),updated_at=now()
	`, sharedBaselineFactKey(question.Key), question.Prompt, answer, "baseline:"+assessmentID); err != nil {
		return fmt.Errorf("share interview fact: %w", err)
	}
	next := current + 1
	nextPhase := BaselinePhaseInterview
	if next < len(baselineQuestions) {
		if _, err := tx.Exec(ctx, `
			INSERT INTO baseline_interview_messages (assessment_id, role, question_key, body)
			VALUES ($1, 'agent', $2, $3)
		`, assessmentID, baselineQuestions[next].Key, baselineQuestions[next].Prompt); err != nil {
			return fmt.Errorf("save next interview prompt: %w", err)
		}
	} else {
		nextPhase = BaselinePhaseInventory
		if _, err := replaceBaselineEvidenceRequirements(ctx, tx, assessmentID); err != nil {
			return fmt.Errorf("scope baseline evidence: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO baseline_interview_messages (assessment_id, role, body)
			VALUES ($1, 'agent', 'I have enough context to start. Next, I will work through the evidence we need one topic at a time, using existing documents and public sources where they help.')
		`, assessmentID); err != nil {
			return fmt.Errorf("finish baseline interview: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE baseline_assessments SET current_question=$2, phase=$3, updated_at=now() WHERE id=$1
	`, assessmentID, next, nextPhase); err != nil {
		return fmt.Errorf("advance baseline interview: %w", err)
	}
	return tx.Commit(ctx)
}

func replaceBaselineEvidenceRequirements(ctx context.Context, tx pgx.Tx, assessmentID string) (BaselineEvidenceScope, error) {
	facts, err := baselineFactsForAssessment(ctx, tx, assessmentID)
	if err != nil {
		return BaselineEvidenceScope{}, err
	}
	scope := baselineEvidenceScope(facts)
	if _, err := tx.Exec(ctx, `DELETE FROM evidence_requirements WHERE assessment_id=$1`, assessmentID); err != nil {
		return BaselineEvidenceScope{}, err
	}
	if err := insertBaselineEvidenceRequirements(ctx, tx, assessmentID, scope.Requirements); err != nil {
		return BaselineEvidenceScope{}, err
	}
	return scope, nil
}

func baselineFactsForAssessment(ctx context.Context, tx pgx.Tx, assessmentID string) (map[string]string, error) {
	rows, err := tx.Query(ctx, `SELECT fact_key, value FROM business_facts WHERE assessment_id=$1`, assessmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	facts := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		facts[key] = value
	}
	return facts, rows.Err()
}

func insertBaselineEvidenceRequirements(ctx context.Context, tx pgx.Tx, assessmentID string, requirements []evidenceSeed) error {
	for _, requirement := range requirements {
		if _, err := tx.Exec(ctx, `
			INSERT INTO evidence_requirements (
				assessment_id, requirement_key, domain, label, rationale, expected_artifact_types, responsibility
			) VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (assessment_id, requirement_key) DO UPDATE SET
				domain=EXCLUDED.domain, label=EXCLUDED.label, rationale=EXCLUDED.rationale,
				expected_artifact_types=EXCLUDED.expected_artifact_types,
				responsibility=CASE WHEN evidence_requirements.interviewed_at IS NULL THEN EXCLUDED.responsibility ELSE evidence_requirements.responsibility END,
				updated_at=now()
		`, assessmentID, requirement.Key, requirement.Domain, requirement.Label, requirement.Rationale, requirement.Artifacts, requirement.Responsibility); err != nil {
			return err
		}
	}
	return nil
}

// ReconcileBaselineEvidenceScope upgrades an assessment created with the
// former one-size-fits-all checklist. Answers and evidence for still-relevant
// topics are preserved; only inapplicable topics are removed and newly relevant
// topics are added.
func (s *Store) ReconcileBaselineEvidenceScope(ctx context.Context, tenantID domain.TenantID) (bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var assessmentID, phase string
	if err := tx.QueryRow(ctx, `
		SELECT id::text, phase FROM baseline_assessments
		WHERE tenant_id=$1 AND status <> 'archived' FOR UPDATE
	`, tenantID.String()).Scan(&assessmentID, &phase); err != nil {
		return false, ErrBaselineNotFound
	}
	if phase != BaselinePhaseInventory {
		return false, nil
	}
	facts, err := baselineFactsForAssessment(ctx, tx, assessmentID)
	if err != nil {
		return false, err
	}
	scope := baselineEvidenceScope(facts)
	rows, err := tx.Query(ctx, `SELECT requirement_key FROM evidence_requirements WHERE assessment_id=$1`, assessmentID)
	if err != nil {
		return false, err
	}
	current := map[string]bool{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return false, err
		}
		current[key] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	if len(current) == len(scope.Requirements) {
		matches := true
		for _, requirement := range scope.Requirements {
			matches = matches && current[requirement.Key]
		}
		if matches {
			repaired, err := repairEvidenceInterviewInterpretations(ctx, tx, assessmentID)
			if err != nil {
				return false, err
			}
			if repaired == 0 {
				return false, nil
			}
			if err := tx.Commit(ctx); err != nil {
				return false, err
			}
			return true, nil
		}
	}
	desiredKeys := make([]string, 0, len(scope.Requirements))
	for _, requirement := range scope.Requirements {
		desiredKeys = append(desiredKeys, requirement.Key)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM evidence_requirements WHERE assessment_id=$1 AND NOT (requirement_key = ANY($2::text[]))`, assessmentID, desiredKeys); err != nil {
		return false, err
	}
	if err := insertBaselineEvidenceRequirements(ctx, tx, assessmentID, scope.Requirements); err != nil {
		return false, err
	}
	if _, err := repairEvidenceInterviewInterpretations(ctx, tx, assessmentID); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func repairEvidenceInterviewInterpretations(ctx context.Context, tx pgx.Tx, assessmentID string) (int64, error) {
	rows, err := tx.Query(ctx, `
		SELECT id::text, owner_answer
		FROM evidence_requirements
		WHERE assessment_id=$1 AND interviewed_at IS NOT NULL AND disposition='search_sources'
		  AND status NOT IN ('confirmed', 'not_applicable')
	`, assessmentID)
	if err != nil {
		return 0, err
	}
	type answeredRequirement struct{ id, answer string }
	var answered []answeredRequirement
	for rows.Next() {
		var requirement answeredRequirement
		if err := rows.Scan(&requirement.id, &requirement.answer); err != nil {
			rows.Close()
			return 0, err
		}
		answered = append(answered, requirement)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	var repaired int64
	for _, requirement := range answered {
		answer := strings.ToLower(requirement.answer)
		if !containsAny(answer, "need to create", "need to write", "need to document", "can create", "will create", "create this", "create it", "create one", "draft it", "make one") {
			continue
		}
		command, err := tx.Exec(ctx, `
			UPDATE evidence_requirements SET disposition=$2, responsibility=$3, updated_at=now() WHERE id=$1
		`, requirement.id, "create_it", "agent")
		if err != nil {
			return repaired, err
		}
		repaired += command.RowsAffected()
	}
	return repaired, nil
}

func (s *Store) SaveBaselineFact(ctx context.Context, tenantID domain.TenantID, key, value string) error {
	key, value = strings.TrimSpace(key), strings.TrimSpace(value)
	if value == "" || len(value) > 2000 {
		return errors.New("a business fact must contain between 1 and 2000 characters")
	}
	valid := false
	for _, question := range baselineQuestions {
		valid = valid || question.Key == key
	}
	if !valid {
		return errors.New("business fact key is invalid")
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE business_facts bf SET value=$3, source_type='owner', source_ref='', confidence=1,
		       confirmed_at=now(), updated_at=now()
		FROM baseline_assessments ba
		WHERE bf.assessment_id=ba.id AND ba.tenant_id=$1 AND ba.status <> 'archived' AND bf.fact_key=$2
	`, tenantID.String(), key, value)
	if err != nil {
		return fmt.Errorf("update baseline fact: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrBaselineNotFound
	}
	label := key
	for _, question := range baselineQuestions {
		if question.Key == key {
			label = question.Prompt
			break
		}
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO business_knowledge_facts(fact_key,label,value,source_type,confidence,confirmed_at)
		VALUES ($1,$2,$3,'owner',1,now())
		ON CONFLICT (fact_key) DO UPDATE SET label=EXCLUDED.label,value=EXCLUDED.value,source_type='owner',
		    confidence=1,status='active',confirmed_at=now(),updated_at=now()
	`, sharedBaselineFactKey(key), label, value); err != nil {
		return fmt.Errorf("update shared business fact: %w", err)
	}
	return nil
}

func sharedBaselineFactKey(key string) string {
	switch key {
	case "business_name":
		return "organization.legal_name"
	case "website_url":
		return "organization.website"
	case "industry":
		return "organization.industry"
	case "primary_location":
		return "organization.primary_jurisdiction"
	case "services":
		return "organization.services"
	case "team_size":
		return "workforce.structure"
	case "immediate_concern":
		return "organization.immediate_concern"
	default:
		return key
	}
}

func (s *Store) AdvanceBaseline(ctx context.Context, tenantID domain.TenantID, target string) error {
	allowed := map[string]string{
		BaselinePhaseSourceAccess + ":" + BaselinePhaseInventory: BaselinePhaseInventory,
		BaselinePhaseInventory + ":" + BaselinePhaseGapReview:    BaselinePhaseGapReview,
		BaselinePhaseGapReview + ":" + BaselinePhasePlanApproval: BaselinePhasePlanApproval,
	}
	var current string
	if err := s.pool.QueryRow(ctx, `SELECT phase FROM baseline_assessments WHERE tenant_id=$1 AND status <> 'archived'`, tenantID.String()).Scan(&current); err != nil {
		return ErrBaselineNotFound
	}
	if allowed[current+":"+target] == "" {
		return ErrBaselineTransition
	}
	if current == BaselinePhaseInventory && target == BaselinePhaseGapReview {
		var remaining int
		if err := s.pool.QueryRow(ctx, `
			SELECT count(*) FROM evidence_requirements er
			JOIN baseline_assessments ba ON ba.id=er.assessment_id
			WHERE ba.tenant_id=$1 AND ba.status <> 'archived' AND er.interviewed_at IS NULL
		`, tenantID.String()).Scan(&remaining); err != nil {
			return err
		}
		if remaining > 0 {
			return fmt.Errorf("finish the evidence interview before reviewing the plan (%d topics remain)", remaining)
		}
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE baseline_assessments SET phase=$2, updated_at=now()
		WHERE tenant_id=$1 AND status <> 'archived'
	`, tenantID.String(), target)
	return err
}

func (s *Store) ConfigureBaselineSource(ctx context.Context, tenantID domain.TenantID, sourceType, status string, scope map[string]any) error {
	if sourceType != "uploads" && sourceType != "email" && sourceType != "google_drive" && sourceType != "public_web" {
		return errors.New("baseline source type is invalid")
	}
	if status != "connected" && status != "disconnected" && status != "available" {
		return errors.New("baseline source status is invalid")
	}
	if scope == nil {
		scope = map[string]any{"read_only": true}
	}
	scope["read_only"] = true
	scopeJSON, _ := json.Marshal(scope)
	command, err := s.pool.Exec(ctx, `
		UPDATE baseline_data_sources SET status=$3, scope=$4, last_error=NULL, updated_at=now()
		WHERE tenant_id=$1 AND source_type=$2
	`, tenantID.String(), sourceType, status, scopeJSON)
	if err != nil {
		return fmt.Errorf("configure baseline source: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrBaselineNotFound
	}
	return nil
}

// SetBaselineSourceConnected reflects connector availability without replacing
// the folder, date, or item limits that the owner already selected.
func (s *Store) SetBaselineSourceConnected(ctx context.Context, tenantID domain.TenantID, sourceType string) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE baseline_data_sources SET status='connected', last_error=NULL, updated_at=now()
		WHERE tenant_id=$1 AND source_type=$2
	`, tenantID.String(), sourceType)
	if err != nil {
		return fmt.Errorf("mark baseline source connected: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrBaselineNotFound
	}
	return nil
}

func (s *Store) MarkBaselineSourceSync(ctx context.Context, tenantID domain.TenantID, sourceType string, syncErr error) {
	if syncErr != nil {
		_, _ = s.pool.Exec(ctx, `
			UPDATE baseline_data_sources SET status='error',last_error=$3,updated_at=now()
			WHERE tenant_id=$1 AND source_type=$2
		`, tenantID.String(), sourceType, syncErr.Error())
		return
	}
	_, _ = s.pool.Exec(ctx, `
		UPDATE baseline_data_sources SET status='connected',last_synced_at=now(),last_error=NULL,updated_at=now()
		WHERE tenant_id=$1 AND source_type=$2
	`, tenantID.String(), sourceType)
}

func (s *Store) SetEvidenceDisposition(ctx context.Context, tenantID domain.TenantID, requirementID, disposition, responsibility string, renewalDueAt *time.Time) error {
	if _, err := uuid.Parse(requirementID); err != nil {
		return errors.New("evidence requirement is invalid")
	}
	validDisposition := map[string]bool{"have_it": true, "search_sources": true, "create_it": true, "obtain_it": true, "not_applicable": true}
	validResponsibility := map[string]bool{"agent": true, "owner": true, "shared": true, "external": true}
	if !validDisposition[disposition] || !validResponsibility[responsibility] {
		return errors.New("evidence resolution is invalid")
	}
	status := "missing"
	if disposition == "not_applicable" {
		status = "not_applicable"
	} else if disposition == "have_it" || disposition == "search_sources" {
		status = "partial"
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE evidence_requirements er SET disposition=$3, responsibility=$4, status=$5, renewal_due_at=$6, updated_at=now()
		FROM baseline_assessments ba
		WHERE er.assessment_id=ba.id AND ba.tenant_id=$1 AND er.id=$2 AND ba.status <> 'archived'
	`, tenantID.String(), requirementID, disposition, responsibility, status, renewalDueAt)
	if err != nil {
		return fmt.Errorf("set evidence resolution: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrBaselineNotFound
	}
	return nil
}

func (s *Store) AnswerEvidenceInterview(ctx context.Context, tenantID domain.TenantID, requirementID, answer, choice string) error {
	answer, choice = strings.TrimSpace(answer), strings.TrimSpace(choice)
	if _, err := uuid.Parse(requirementID); err != nil {
		return errors.New("evidence requirement is invalid")
	}
	if answer == "" || len(answer) > 2000 {
		return errors.New("tell Mia what you know in 2000 characters or fewer")
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var defaultResponsibility string
	var evidenceCount int
	if err := tx.QueryRow(ctx, `
		SELECT er.responsibility, (SELECT count(*) FROM evidence_links el WHERE el.requirement_id=er.id)
		FROM evidence_requirements er JOIN baseline_assessments ba ON ba.id=er.assessment_id
		WHERE ba.tenant_id=$1 AND er.id=$2 AND ba.phase='inventory' AND ba.status <> 'archived'
		FOR UPDATE OF er
	`, tenantID.String(), requirementID).Scan(&defaultResponsibility, &evidenceCount); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrBaselineNotFound
		}
		return err
	}

	if choice == "confirm_evidence" {
		if evidenceCount == 0 {
			return errors.New("there is no proposed evidence to confirm")
		}
		if _, err := tx.Exec(ctx, `UPDATE evidence_links SET verified_at=COALESCE(verified_at,now()) WHERE requirement_id=$1`, requirementID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE evidence_requirements SET status='confirmed',disposition='have_it',responsibility='owner',
			       owner_answer=$2,interviewed_at=now(),updated_at=now() WHERE id=$1
		`, requirementID, answer); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}

	disposition, responsibility := interpretEvidenceInterviewAnswer(answer, choice, defaultResponsibility)
	status := "missing"
	if disposition == "not_applicable" {
		status = "not_applicable"
	} else if disposition == "have_it" || disposition == "search_sources" {
		status = "partial"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE evidence_requirements SET disposition=$2,responsibility=$3,status=$4,
		       owner_answer=$5,interviewed_at=now(),updated_at=now() WHERE id=$1
	`, requirementID, disposition, responsibility, status, answer); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func interpretEvidenceInterviewAnswer(answer, choice, defaultResponsibility string) (string, string) {
	choices := map[string][2]string{
		"have_it": {"have_it", "owner"}, "search_sources": {"search_sources", "agent"},
		"create_it": {"create_it", "agent"}, "obtain_it": {"obtain_it", "shared"},
		"not_applicable": {"not_applicable", "owner"}, "not_sure": {"search_sources", "agent"},
	}
	if result, ok := choices[choice]; ok {
		return result[0], result[1]
	}
	value := strings.ToLower(answer)
	containsAny := func(values ...string) bool {
		for _, candidate := range values {
			if strings.Contains(value, candidate) {
				return true
			}
		}
		return false
	}
	switch {
	case containsAny("not applicable", "doesn't apply", "does not apply", "n/a"):
		return "not_applicable", "owner"
	case containsAny("need to obtain", "need to get", "apply for", "purchase", "buy it", "outside professional"):
		return "obtain_it", "shared"
	case containsAny("need to create", "need to write", "need to document", "can create", "will create", "create this", "create it", "create one", "draft it", "make one"):
		return "create_it", "agent"
	case containsAny("i have", "we have", "already have", "yes", "exists"):
		return "have_it", "owner"
	case containsAny("search", "look for", "find it", "not sure", "don't know", "do not know"):
		return "search_sources", "agent"
	default:
		if defaultResponsibility == "" {
			defaultResponsibility = "shared"
		}
		return "search_sources", defaultResponsibility
	}
}

func (s *Store) EnsureBaselineMaintenance(ctx context.Context, tenantID domain.TenantID) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		UPDATE evidence_requirements er SET status='stale',updated_at=now()
		FROM baseline_assessments ba
		WHERE er.assessment_id=ba.id AND ba.tenant_id=$1 AND ba.status <> 'archived'
		  AND er.renewal_due_at < now() AND er.status='confirmed'
	`, tenantID.String()); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `
		SELECT er.assessment_id::text,er.id::text,er.label,er.rationale,er.responsibility,er.renewal_due_at
		FROM evidence_requirements er JOIN baseline_assessments ba ON ba.id=er.assessment_id
		WHERE ba.tenant_id=$1 AND ba.status <> 'archived' AND er.renewal_due_at IS NOT NULL
		  AND er.renewal_due_at <= now()+interval '30 days'
		  AND NOT EXISTS (
			SELECT 1 FROM work_items wi WHERE wi.baseline_requirement_id=er.id AND wi.status IN ('open','in_progress','waiting')
		  )
		FOR UPDATE OF er
	`, tenantID.String())
	if err != nil {
		return err
	}
	type renewal struct {
		assessmentID, requirementID, label, rationale, responsibility string
		dueAt                                                         time.Time
	}
	var renewals []renewal
	for rows.Next() {
		var item renewal
		if err := rows.Scan(&item.assessmentID, &item.requirementID, &item.label, &item.rationale, &item.responsibility, &item.dueAt); err != nil {
			rows.Close()
			return err
		}
		renewals = append(renewals, item)
	}
	rows.Close()
	for _, item := range renewals {
		var workItemID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO work_items (kind,title,description,priority,source,due_at,responsibility,baseline_requirement_id)
			VALUES ('ticket',$1,$2,CASE WHEN $3 < now() THEN 'urgent' ELSE 'high' END,'system',$3,$4,$5)
			RETURNING id::text
		`, "Renew: "+item.label, item.rationale, item.dueAt, item.responsibility, item.requirementID).Scan(&workItemID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO baseline_work_items (assessment_id,requirement_id,work_item_id) VALUES ($1,$2,$3)`, item.assessmentID, item.requirementID, workItemID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		WITH due AS (
			SELECT ba.id FROM baseline_assessments ba
			WHERE ba.tenant_id=$1 AND ba.status IN ('active','ready') AND ba.next_reassessment_at <= now()
			  AND NOT EXISTS (
				SELECT 1 FROM baseline_work_items bwi JOIN work_items wi ON wi.id=bwi.work_item_id
				WHERE bwi.assessment_id=ba.id AND bwi.requirement_id IS NULL
				  AND wi.title='Reassess the documented business baseline' AND wi.status IN ('open','in_progress','waiting')
			  )
		), created AS (
			INSERT INTO work_items (kind,title,description,priority,source,responsibility)
			SELECT 'ticket','Reassess the documented business baseline',
			       'Review changed operations, connected sources, expired evidence, and unresolved gaps. Preserve provenance and create only changed follow-up work.',
			       'high','system','shared' FROM due RETURNING id
		)
		INSERT INTO baseline_work_items (assessment_id,work_item_id)
		SELECT due.id,created.id FROM due CROSS JOIN created
	`, tenantID.String()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) RunBaselineMaintenance(ctx context.Context, tenantID domain.TenantID, interval time.Duration) {
	if interval <= 0 {
		interval = 12 * time.Hour
	}
	_ = s.EnsureBaselineMaintenance(ctx, tenantID)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = s.EnsureBaselineMaintenance(ctx, tenantID)
		}
	}
}

func (s *Store) LinkDocumentEvidence(ctx context.Context, tenantID domain.TenantID, requirementID, documentID, userID string) error {
	for _, value := range []string{requirementID, documentID, userID} {
		if _, err := uuid.Parse(value); err != nil {
			return errors.New("evidence link is invalid")
		}
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `
		INSERT INTO evidence_links (requirement_id, document_id, citation, confidence, verified_by, verified_at)
		SELECT er.id, d.id, d.name, 1, $4, now()
		FROM evidence_requirements er
		JOIN baseline_assessments ba ON ba.id=er.assessment_id
		JOIN documents d ON d.id=$3 AND d.status='ready'
		WHERE ba.tenant_id=$1 AND er.id=$2 AND ba.status <> 'archived'
		ON CONFLICT DO NOTHING
	`, tenantID.String(), requirementID, documentID, userID)
	if err != nil {
		return fmt.Errorf("link document evidence: %w", err)
	}
	if command.RowsAffected() == 0 {
		var exists bool
		_ = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM evidence_links WHERE requirement_id=$1 AND document_id=$2)`, requirementID, documentID).Scan(&exists)
		if !exists {
			return errors.New("the document or evidence requirement is unavailable")
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE evidence_requirements SET status='confirmed', disposition='have_it', responsibility='owner',
		       owner_answer='I attached this record.', interviewed_at=now(), updated_at=now() WHERE id=$1
	`, requirementID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) LinkDocumentCandidate(ctx context.Context, tenantID domain.TenantID, requirementID, documentID, citation string, confidence float64) error {
	if _, err := uuid.Parse(requirementID); err != nil {
		return errors.New("evidence requirement is invalid")
	}
	if _, err := uuid.Parse(documentID); err != nil {
		return errors.New("document candidate is invalid")
	}
	if confidence < 0 || confidence > 1 {
		return errors.New("document candidate confidence is invalid")
	}
	command, err := s.pool.Exec(ctx, `
		INSERT INTO evidence_links (requirement_id,document_id,citation,confidence)
		SELECT er.id,d.id,$4,$5
		FROM evidence_requirements er
		JOIN baseline_assessments ba ON ba.id=er.assessment_id
		JOIN documents d ON d.id=$3 AND d.status='ready'
		WHERE ba.tenant_id=$1 AND er.id=$2 AND ba.status <> 'archived'
		ON CONFLICT (requirement_id,document_id) WHERE document_id IS NOT NULL DO NOTHING
	`, tenantID.String(), requirementID, documentID, strings.TrimSpace(citation), confidence)
	if err != nil {
		return fmt.Errorf("link document candidate: %w", err)
	}
	if command.RowsAffected() > 0 {
		_, _ = s.pool.Exec(ctx, `UPDATE evidence_requirements SET status=CASE WHEN status='missing' THEN 'partial' ELSE status END,disposition='search_sources',updated_at=now() WHERE id=$1`, requirementID)
	}
	return nil
}

func (s *Store) AddPublicEvidence(ctx context.Context, tenantID domain.TenantID, requirementID, publicURL, citation string, confidence float64) error {
	publicURL, citation = strings.TrimSpace(publicURL), strings.TrimSpace(citation)
	if _, err := uuid.Parse(requirementID); err != nil || (!strings.HasPrefix(publicURL, "https://") && !strings.HasPrefix(publicURL, "http://")) {
		return errors.New("public evidence is invalid")
	}
	if confidence < 0 || confidence > 1 {
		return errors.New("public evidence confidence is invalid")
	}
	command, err := s.pool.Exec(ctx, `
		INSERT INTO evidence_links (requirement_id, public_url, citation, confidence, verified_at)
		SELECT er.id, $3, $4, $5, now()
		FROM evidence_requirements er JOIN baseline_assessments ba ON ba.id=er.assessment_id
		WHERE ba.tenant_id=$1 AND er.id=$2 AND ba.status <> 'archived'
	`, tenantID.String(), requirementID, publicURL, citation, confidence)
	if err != nil {
		return fmt.Errorf("add public evidence: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrBaselineNotFound
	}
	_, _ = s.pool.Exec(ctx, `UPDATE evidence_requirements SET status='confirmed', disposition='search_sources', responsibility='agent',
		owner_answer='Use this public source as evidence.', interviewed_at=now(), updated_at=now() WHERE id=$1`, requirementID)
	return nil
}

func (s *Store) RecordBaselineSourceItem(ctx context.Context, tenantID domain.TenantID, input RecordSourceItemInput) (string, error) {
	input.SourceType = strings.TrimSpace(input.SourceType)
	input.ExternalID = strings.TrimSpace(input.ExternalID)
	input.Name = strings.TrimSpace(input.Name)
	if input.ExternalID == "" || input.Name == "" || len(input.ExternalID) > 1000 || len(input.Name) > 500 {
		return "", errors.New("baseline source item is invalid")
	}
	if input.DocumentID != "" {
		if _, err := uuid.Parse(input.DocumentID); err != nil {
			return "", errors.New("baseline source document is invalid")
		}
	}
	metadata, _ := json.Marshal(input.Metadata)
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO baseline_source_items (
			data_source_id,external_id,name,media_type,source_uri,modified_at,content_sha256,metadata,document_id
		)
		SELECT ds.id,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,'')::uuid
		FROM baseline_data_sources ds WHERE ds.tenant_id=$1 AND ds.source_type=$2 AND ds.status='connected'
		ON CONFLICT (data_source_id,external_id) DO UPDATE
		SET name=EXCLUDED.name,media_type=EXCLUDED.media_type,source_uri=EXCLUDED.source_uri,
		    modified_at=EXCLUDED.modified_at,content_sha256=EXCLUDED.content_sha256,metadata=EXCLUDED.metadata,
		    document_id=COALESCE(EXCLUDED.document_id,baseline_source_items.document_id),updated_at=now()
		RETURNING id::text
	`, tenantID.String(), input.SourceType, input.ExternalID, input.Name, fallback(input.MediaType, "application/octet-stream"),
		input.SourceURI, input.ModifiedAt, input.SHA256, metadata, input.DocumentID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errors.New("the baseline data source is not connected")
	}
	if err != nil {
		return "", fmt.Errorf("record baseline source item: %w", err)
	}
	return id, nil
}

func (s *Store) LinkSourceItemEvidence(ctx context.Context, tenantID domain.TenantID, requirementID, sourceItemID, citation string, confidence float64) error {
	if _, err := uuid.Parse(requirementID); err != nil {
		return errors.New("evidence requirement is invalid")
	}
	if _, err := uuid.Parse(sourceItemID); err != nil {
		return errors.New("source item is invalid")
	}
	if confidence < 0 || confidence > 1 {
		return errors.New("source evidence confidence is invalid")
	}
	command, err := s.pool.Exec(ctx, `
		INSERT INTO evidence_links (requirement_id,source_item_id,citation,confidence)
		SELECT er.id,si.id,$4,$5
		FROM evidence_requirements er
		JOIN baseline_assessments ba ON ba.id=er.assessment_id
		JOIN baseline_source_items si ON si.id=$3
		JOIN baseline_data_sources ds ON ds.id=si.data_source_id AND ds.tenant_id=ba.tenant_id
		WHERE ba.tenant_id=$1 AND er.id=$2 AND ba.status <> 'archived'
	`, tenantID.String(), requirementID, sourceItemID, strings.TrimSpace(citation), confidence)
	if err != nil {
		return fmt.Errorf("link source evidence: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrBaselineNotFound
	}
	_, _ = s.pool.Exec(ctx, `
		UPDATE evidence_requirements SET status=CASE WHEN status='missing' THEN 'partial' ELSE status END,
		       disposition='search_sources', updated_at=now() WHERE id=$1
	`, requirementID)
	return nil
}

func MatchBaselineEvidence(requirements []EvidenceRequirement, text string) []string {
	text = strings.ToLower(text)
	keywords := map[string][]string{
		"identity_registration":    {"articles of", "business registration", "secretary of state", "operating agreement", "legal entity"},
		"licenses_permits":         {"license", "licence", "permit", "credential", "registration certificate"},
		"insurance":                {"insurance", "certificate of insurance", "policy declarations", "coverage"},
		"compliance_calendar":      {"renewal", "filing deadline", "compliance calendar"},
		"financial_reporting":      {"profit and loss", "balance sheet", "cash flow", "financial statement"},
		"billing_collection":       {"invoice", "accounts receivable", "collections", "billing procedure"},
		"sales_pipeline":           {"pipeline", "lead", "opportunity", "quote", "proposal"},
		"service_workflow":         {"workflow", "standard operating procedure", "dispatch", "service delivery"},
		"quality_closeout":         {"closeout", "quality", "inspection", "completion checklist"},
		"customer_terms":           {"terms", "service agreement", "contract", "service level"},
		"customer_feedback":        {"complaint", "feedback", "survey", "support ticket"},
		"role_responsibility":      {"role", "responsibility", "organization chart", "raci"},
		"workforce_records":        {"employee handbook", "training", "contractor agreement", "safety"},
		"goals_scorecard":          {"scorecard", "kpi", "goal", "annual plan"},
		"product_definition":       {"product brief", "service catalog", "pricing", "roadmap", "ideal customer"},
		"software_delivery":        {"release", "deployment", "change management", "pull request", "development workflow", "rollback"},
		"security_access_controls": {"security policy", "access control", "authentication", "system inventory", "access review"},
		"privacy_data_handling":    {"privacy", "data inventory", "retention", "data processing agreement", "personal data"},
		"incident_continuity":      {"incident response", "disaster recovery", "backup", "business continuity", "status page"},
		"fulfillment_inventory":    {"inventory", "fulfillment", "returns", "refund", "shipping"},
	}
	var result []string
	for _, requirement := range requirements {
		for _, keyword := range keywords[requirement.Key] {
			if strings.Contains(text, keyword) {
				result = append(result, requirement.ID)
				break
			}
		}
	}
	return result
}

func (s *Store) ApplyBaselineFactsToOnboarding(ctx context.Context, tenantID domain.TenantID, state Onboarding) (Onboarding, error) {
	assessment, err := s.GetBaseline(ctx, tenantID)
	if err != nil {
		return state, err
	}
	facts := make(map[string]string, len(assessment.Facts))
	for _, fact := range assessment.Facts {
		facts[fact.Key] = fact.Value
	}
	state.Business.BusinessName = fallback(facts["business_name"], state.Business.BusinessName)
	if !strings.EqualFold(facts["website_url"], "none") {
		state.Business.WebsiteURL = facts["website_url"]
	}
	state.Business.Trade = fallback(facts["industry"], state.Business.Trade)
	state.Business.ServiceArea = fallback(facts["primary_location"], state.Business.ServiceArea)
	state.Business.Services = fallback(facts["services"], state.Business.Services)
	if teamSize, err := strconv.Atoi(facts["team_size"]); err == nil {
		state.Business.TeamSize = teamSize
	}
	if state.Business.TimeZone == "" {
		state.Business.TimeZone = "America/New_York"
	}
	if state.Business.CustomerMix == "" {
		state.Business.CustomerMix = "mixed"
	}
	if concern := strings.TrimSpace(facts["immediate_concern"]); concern != "" {
		state.Priorities = []string{concern}
	}
	state.Permissions = DefaultPermissionPlan()
	template := s.template
	if state.Business.Template != "" {
		template = ParseBusinessTemplate(state.Business.Template)
	}
	state.Blueprint = GenerateBlueprintForTemplate(state, template)
	state.Status = OnboardingInProgress
	state.CurrentStep = 6
	return state, nil
}

func (s *Store) CreateBaselinePlan(ctx context.Context, tenantID domain.TenantID, userID string) (WorkItem, int, error) {
	if _, err := uuid.Parse(userID); err != nil {
		return WorkItem{}, 0, errors.New("baseline plan owner is invalid")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return WorkItem{}, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var assessmentID, phase string
	if err := tx.QueryRow(ctx, `
		SELECT id::text, phase FROM baseline_assessments
		WHERE tenant_id=$1 AND status <> 'archived' FOR UPDATE
	`, tenantID.String()).Scan(&assessmentID, &phase); err != nil {
		return WorkItem{}, 0, ErrBaselineNotFound
	}
	var existingID string
	if err := tx.QueryRow(ctx, `
		SELECT work_item_id::text FROM baseline_work_items
		WHERE assessment_id=$1 AND requirement_id IS NULL LIMIT 1
	`, assessmentID).Scan(&existingID); err == nil {
		if err := tx.Commit(ctx); err != nil {
			return WorkItem{}, 0, err
		}
		item, err := s.GetWorkItem(ctx, existingID)
		return item, 0, err
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return WorkItem{}, 0, err
	}
	if phase != BaselinePhasePlanApproval && phase != BaselinePhaseGapReview {
		return WorkItem{}, 0, ErrBaselineTransition
	}

	var parent WorkItem
	err = tx.QueryRow(ctx, `
		INSERT INTO work_items (kind,title,description,priority,source,created_by,assigned_user_id,responsibility)
		VALUES ('ticket','Establish the documented business baseline',
		        'Collect, verify, and maintain the minimum evidence needed to describe how the business operates and where material gaps remain.',
		        'high','system',$1,$1,'shared')
		RETURNING id::text, number, kind, title, description, status, priority, source, responsibility,
		          due_at, completed_at, created_at, updated_at
	`, userID).Scan(
		&parent.ID, &parent.Number, &parent.Kind, &parent.Title, &parent.Description, &parent.Status,
		&parent.Priority, &parent.Source, &parent.Responsibility, &parent.DueAt, &parent.CompletedAt, &parent.CreatedAt, &parent.UpdatedAt,
	)
	if err != nil {
		return WorkItem{}, 0, fmt.Errorf("create baseline parent: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO baseline_work_items (assessment_id, work_item_id) VALUES ($1,$2)`, assessmentID, parent.ID); err != nil {
		return WorkItem{}, 0, err
	}

	var mainManagerID string
	_ = tx.QueryRow(ctx, `SELECT id::text FROM personas WHERE enabled ORDER BY position LIMIT 1`).Scan(&mainManagerID)
	rows, err := tx.Query(ctx, `
		SELECT id::text, label, rationale, status, disposition, responsibility
		FROM evidence_requirements
		WHERE assessment_id=$1 AND required AND status NOT IN ('confirmed','not_applicable')
		ORDER BY domain, created_at
	`, assessmentID)
	if err != nil {
		return WorkItem{}, 0, err
	}
	type pendingRequirement struct{ id, label, rationale, status, disposition, responsibility string }
	var requirements []pendingRequirement
	for rows.Next() {
		var requirement pendingRequirement
		if err := rows.Scan(&requirement.id, &requirement.label, &requirement.rationale, &requirement.status, &requirement.disposition, &requirement.responsibility); err != nil {
			rows.Close()
			return WorkItem{}, 0, err
		}
		requirements = append(requirements, requirement)
	}
	rows.Close()
	for _, requirement := range requirements {
		title := baselineTaskTitle(requirement.disposition, requirement.label)
		assignedUserID, assignedPersonaID := "", ""
		switch requirement.responsibility {
		case "owner":
			assignedUserID = userID
		case "agent", "shared":
			assignedPersonaID = mainManagerID
		}
		var childID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO work_items (
				kind,title,description,priority,source,created_by,assigned_user_id,assigned_persona_id,
				parent_id,responsibility,baseline_requirement_id
			) VALUES ('ticket',$1,$2,'normal','system',$3,NULLIF($4,'')::uuid,NULLIF($5,'')::uuid,$6,$7,$8)
			RETURNING id::text
		`, title, requirement.rationale, userID, assignedUserID, assignedPersonaID, parent.ID, requirement.responsibility, requirement.id).Scan(&childID); err != nil {
			return WorkItem{}, 0, fmt.Errorf("create baseline task: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO baseline_work_items (assessment_id, requirement_id, work_item_id) VALUES ($1,$2,$3)
		`, assessmentID, requirement.id, childID); err != nil {
			return WorkItem{}, 0, err
		}
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `
		UPDATE baseline_assessments
		SET status='active', phase='active', last_assessed_at=$2, next_reassessment_at=$3, updated_at=now()
		WHERE id=$1
	`, assessmentID, now, now.AddDate(0, 3, 0)); err != nil {
		return WorkItem{}, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return WorkItem{}, 0, err
	}
	return parent, len(requirements), nil
}

func baselineTaskTitle(disposition, label string) string {
	prefix := "Resolve"
	switch disposition {
	case "have_it":
		prefix = "Locate and verify"
	case "search_sources":
		prefix = "Search connected sources for"
	case "create_it":
		prefix = "Create"
	case "obtain_it":
		prefix = "Obtain"
	}
	return prefix + ": " + label
}

func (s *Store) StartBaselineReassessment(ctx context.Context, tenantID domain.TenantID) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var assessmentID string
	if err := tx.QueryRow(ctx, `
		UPDATE baseline_assessments
		SET status='archived', updated_at=now()
		WHERE tenant_id=$1 AND status <> 'archived'
		RETURNING id::text
	`, tenantID.String()).Scan(&assessmentID); err != nil {
		return ErrBaselineNotFound
	}
	var newID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO baseline_assessments (tenant_id, baseline_version, phase, current_question)
		SELECT tenant_id, baseline_version+1, 'inventory', $2 FROM baseline_assessments WHERE id=$1
		RETURNING id::text
	`, assessmentID, len(baselineQuestions)).Scan(&newID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO business_facts (assessment_id,fact_key,value,source_type,source_ref,confidence,confirmed_at)
		SELECT $2,fact_key,value,source_type,source_ref,confidence,confirmed_at FROM business_facts WHERE assessment_id=$1
	`, assessmentID, newID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO evidence_requirements (
			assessment_id,requirement_key,domain,label,rationale,expected_artifact_types,required,status,disposition,responsibility,renewal_due_at
		)
		SELECT $2,requirement_key,domain,label,rationale,expected_artifact_types,required,
		       CASE WHEN renewal_due_at IS NOT NULL AND renewal_due_at < now() THEN 'stale' ELSE status END,
		       disposition,responsibility,renewal_due_at
		FROM evidence_requirements WHERE assessment_id=$1
	`, assessmentID, newID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO baseline_interview_messages (assessment_id,role,body)
		VALUES ($1,'agent','It is time to reassess the documented baseline. I preserved confirmed facts and marked expired evidence as stale. Review connected sources and changed operations next.')
	`, newID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

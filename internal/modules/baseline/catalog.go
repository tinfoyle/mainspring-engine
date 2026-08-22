package baseline

import (
	"sort"
	"strconv"
	"strings"
)

const (
	EvidenceCatalogVersion = "baseline-evidence-2026-08-22"
	ScopePolicyVersion     = "baseline-scope-v1"
)

type QuestionDefinition struct {
	Key         string
	Prompt      string
	Explanation string
	Optional    bool
}

type CatalogResponsibility string

const (
	CatalogOwner  CatalogResponsibility = "owner"
	CatalogShared CatalogResponsibility = "shared"
	CatalogAgent  CatalogResponsibility = "agent"
)

type EvidenceDefinition struct {
	Code           string
	Domain         string
	Title          string
	Rationale      string
	Responsibility CatalogResponsibility
	ArtifactHints  []string
	RenewAfterDays uint16
}

type ScopeFacts struct {
	Industry         string
	Services         string
	TeamSize         string
	ImmediateConcern string
}

type ScopeSelection struct {
	CatalogVersion     string
	ScopePolicyVersion string
	Profile            string
	Title              string
	Explanation        string
	Requirements       []EvidenceDefinition
}

var baselineQuestions = []QuestionDefinition{
	{Key: "organization.legal_name", Prompt: "What is the legal or registered name of the business?", Explanation: "The legal identity anchors the baseline and resulting Work."},
	{Key: "organization.website_url", Prompt: "What is the business website?", Explanation: "A public website provides a starting point without access to private systems.", Optional: true},
	{Key: "organization.industry", Prompt: "What trade, industry, or business model best describes the company?", Explanation: "Industry determines which records, licenses, controls, and practices are applicable."},
	{Key: "organization.primary_location", Prompt: "Where does the business primarily operate?", Explanation: "Jurisdiction affects licensing, employment, tax, and insurance evidence."},
	{Key: "organization.services", Prompt: "What products or services produce revenue today?", Explanation: "Revenue activity anchors the operating workflow and prevents generic recommendations."},
	{Key: "organization.team_size", Prompt: "How many people work in the business, including owners and regular contractors?", Explanation: "Team size changes the workforce evidence and controls a business reasonably needs."},
	{Key: "baseline.immediate_concern", Prompt: "What uncertainty or operating problem should the first baseline plan prioritize?", Explanation: "The first plan should reflect the owner's stated priority."},
}

var evidenceCatalog = map[string]EvidenceDefinition{
	"identity_registration":    {Code: "identity_registration", Domain: "Business identity and ownership", Title: "Business registration and ownership record", Rationale: "Confirms the legal entity, trade names, standing, and accountable owners.", Responsibility: CatalogOwner, ArtifactHints: []string{"registration", "articles", "operating agreement"}},
	"licenses_permits":         {Code: "licenses_permits", Domain: "Legal, licensing, insurance, and compliance", Title: "Required licenses and permits", Rationale: "Shows the business and its workers are authorized for the services and jurisdictions involved.", Responsibility: CatalogShared, ArtifactHints: []string{"license", "permit", "registration", "certificate"}, RenewAfterDays: 365},
	"insurance":                {Code: "insurance", Domain: "Legal, licensing, insurance, and compliance", Title: "Current insurance coverage", Rationale: "Documents policy limits, exclusions, named insureds, and renewal dates.", Responsibility: CatalogOwner, ArtifactHints: []string{"certificate of insurance", "policy declarations"}, RenewAfterDays: 365},
	"compliance_calendar":      {Code: "compliance_calendar", Domain: "Legal, licensing, insurance, and compliance", Title: "Compliance and renewal calendar", Rationale: "Turns filings, licenses, insurance, and recurring obligations into accountable dates.", Responsibility: CatalogAgent, ArtifactHints: []string{"renewal calendar", "filing schedule"}},
	"financial_reporting":      {Code: "financial_reporting", Domain: "Financial controls and reporting", Title: "Recent financial statements", Rationale: "Provides a documented view of revenue, margin, cash, and major expense categories.", Responsibility: CatalogOwner, ArtifactHints: []string{"profit and loss", "balance sheet", "cash flow"}, RenewAfterDays: 90},
	"billing_collection":       {Code: "billing_collection", Domain: "Financial controls and reporting", Title: "Billing and collection procedure", Rationale: "Establishes how completed work becomes revenue and overdue balances are handled.", Responsibility: CatalogShared, ArtifactHints: []string{"billing procedure", "accounts receivable aging"}},
	"sales_pipeline":           {Code: "sales_pipeline", Domain: "Sales and pipeline", Title: "Lead and sales pipeline record", Rationale: "Shows how demand is captured, qualified, proposed, won, and lost.", Responsibility: CatalogOwner, ArtifactHints: []string{"pipeline export", "lead log", "sales process"}, RenewAfterDays: 30},
	"service_workflow":         {Code: "service_workflow", Domain: "Operations and service delivery", Title: "Service delivery workflow", Rationale: "Documents the repeatable path from request through completion and closeout.", Responsibility: CatalogAgent, ArtifactHints: []string{"SOP", "workflow", "checklist"}},
	"quality_closeout":         {Code: "quality_closeout", Domain: "Operations and service delivery", Title: "Quality and closeout checklist", Rationale: "Defines the evidence needed before work is considered complete.", Responsibility: CatalogAgent, ArtifactHints: []string{"checklist", "quality policy"}},
	"customer_terms":           {Code: "customer_terms", Domain: "Customer service and retention", Title: "Customer terms and service commitments", Rationale: "Clarifies promises, exclusions, escalation paths, and customer responsibilities.", Responsibility: CatalogShared, ArtifactHints: []string{"contract", "terms", "service level agreement"}, RenewAfterDays: 365},
	"customer_feedback":        {Code: "customer_feedback", Domain: "Customer service and retention", Title: "Customer issue and feedback record", Rationale: "Makes recurring complaints, churn risks, and service recovery visible.", Responsibility: CatalogOwner, ArtifactHints: []string{"support export", "complaint log", "survey"}, RenewAfterDays: 90},
	"role_responsibility":      {Code: "role_responsibility", Domain: "Workforce and responsibilities", Title: "Role and responsibility map", Rationale: "Identifies who owns each operating decision and handoff.", Responsibility: CatalogAgent, ArtifactHints: []string{"organization chart", "role descriptions", "RACI"}},
	"workforce_records":        {Code: "workforce_records", Domain: "Workforce and responsibilities", Title: "Required workforce records", Rationale: "Confirms onboarding, training, classification, safety, and policy evidence.", Responsibility: CatalogOwner, ArtifactHints: []string{"handbook", "training records", "contractor agreement"}, RenewAfterDays: 365},
	"goals_scorecard":          {Code: "goals_scorecard", Domain: "Strategy and ownership", Title: "Business goals and operating scorecard", Rationale: "Connects owner priorities to measurable operational outcomes.", Responsibility: CatalogShared, ArtifactHints: []string{"scorecard", "annual plan", "KPI report"}, RenewAfterDays: 90},
	"product_definition":       {Code: "product_definition", Domain: "Product and commercial model", Title: "Product, customer, and pricing definition", Rationale: "Documents what the product does, who it serves, how it is packaged, and how the business earns revenue.", Responsibility: CatalogShared, ArtifactHints: []string{"product brief", "service catalog", "pricing", "roadmap"}, RenewAfterDays: 90},
	"software_delivery":        {Code: "software_delivery", Domain: "Product and service delivery", Title: "Development, release, and change workflow", Rationale: "Documents how software changes are planned, reviewed, tested, released, and rolled back.", Responsibility: CatalogAgent, ArtifactHints: []string{"development workflow", "release checklist", "change policy", "deployment runbook"}},
	"security_access_controls": {Code: "security_access_controls", Domain: "Security, privacy, and resilience", Title: "Information security and access controls", Rationale: "Identifies systems, privileged access, authentication standards, security ownership, and reviews.", Responsibility: CatalogShared, ArtifactHints: []string{"security policy", "access control policy", "system inventory", "access review"}, RenewAfterDays: 90},
	"privacy_data_handling":    {Code: "privacy_data_handling", Domain: "Security, privacy, and resilience", Title: "Privacy and customer-data handling record", Rationale: "Documents collected data, storage, purpose, requests, sharing, and retention.", Responsibility: CatalogShared, ArtifactHints: []string{"privacy policy", "data inventory", "retention policy", "data processing agreement"}, RenewAfterDays: 365},
	"incident_continuity":      {Code: "incident_continuity", Domain: "Security, privacy, and resilience", Title: "Incident response, backup, and continuity plan", Rationale: "Defines detection, communication, recovery, and learning from failures.", Responsibility: CatalogAgent, ArtifactHints: []string{"incident response plan", "backup policy", "disaster recovery plan", "status procedure"}, RenewAfterDays: 365},
	"fulfillment_inventory":    {Code: "fulfillment_inventory", Domain: "Operations and service delivery", Title: "Inventory, fulfillment, and returns workflow", Rationale: "Documents how products are sourced, stocked, sold, delivered, reconciled, returned, and refunded.", Responsibility: CatalogShared, ArtifactHints: []string{"inventory report", "fulfillment procedure", "returns policy"}},
}

var profileRequirements = map[string][]string{
	"field_service":         {"identity_registration", "licenses_permits", "insurance", "compliance_calendar", "financial_reporting", "billing_collection", "sales_pipeline", "service_workflow", "quality_closeout", "customer_terms", "customer_feedback", "role_responsibility", "workforce_records", "goals_scorecard"},
	"software":              {"identity_registration", "product_definition", "financial_reporting", "billing_collection", "sales_pipeline", "software_delivery", "security_access_controls", "privacy_data_handling", "incident_continuity", "customer_terms", "customer_feedback", "role_responsibility", "workforce_records", "goals_scorecard"},
	"professional_services": {"identity_registration", "insurance", "compliance_calendar", "financial_reporting", "billing_collection", "sales_pipeline", "service_workflow", "quality_closeout", "customer_terms", "customer_feedback", "role_responsibility", "workforce_records", "goals_scorecard"},
	"retail":                {"identity_registration", "licenses_permits", "insurance", "compliance_calendar", "financial_reporting", "billing_collection", "sales_pipeline", "fulfillment_inventory", "customer_terms", "customer_feedback", "role_responsibility", "workforce_records", "goals_scorecard"},
}

func BaselineQuestions() []QuestionDefinition {
	return append([]QuestionDefinition(nil), baselineQuestions...)
}

func BaselineQuestion(key string) (QuestionDefinition, bool) {
	key = strings.TrimSpace(key)
	for _, question := range baselineQuestions {
		if question.Key == key {
			return question, true
		}
	}
	return QuestionDefinition{}, false
}

func MissingRequiredQuestions(answered map[string]struct{}) []string {
	missing := make([]string, 0, len(baselineQuestions))
	for _, question := range baselineQuestions {
		if _, exists := answered[question.Key]; !question.Optional && !exists {
			missing = append(missing, question.Key)
		}
	}
	return missing
}

func EvidenceCatalog() []EvidenceDefinition {
	result := make([]EvidenceDefinition, 0, len(evidenceCatalog))
	for _, value := range evidenceCatalog {
		result = append(result, cloneEvidenceDefinition(value))
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Code < result[right].Code })
	return result
}

func SelectScope(facts ScopeFacts) ScopeSelection {
	description := strings.ToLower(strings.Join([]string{facts.Industry, facts.Services, facts.ImmediateConcern}, " "))
	profile, title, explanation := "professional_services", "General service business", "A balanced operating baseline was selected from the confirmed business description."
	switch {
	case containsCatalogTerm(description, "software", "saas", "app developer", "application developer", "technology platform", "cloud platform", "web platform", "mobile app"):
		profile, title, explanation = "software", "Software and SaaS business", "Product definition, software delivery, security, privacy, resilience, subscriptions, customer agreements, and operating controls apply to the confirmed model."
	case containsCatalogTerm(description, "plumb", "hvac", "electric", "contractor", "construction", "roof", "landscap", "field service", "repair service", "home service"):
		profile, title, explanation = "field_service", "Trade and field-service business", "Jurisdictional licenses, insurance, field delivery, quality closeout, safety, and workforce records apply because work occurs at customer sites."
	case containsCatalogTerm(description, "consult", "agency", "accounting", "bookkeep", "professional service", "advisory", "design studio", "marketing service"):
		profile, title, explanation = "professional_services", "Professional-services business", "Engagement scope, client delivery, professional risk, quality review, billing, customer commitments, and accountable ownership apply to the confirmed model."
	case containsCatalogTerm(description, "retail", "ecommerce", "e-commerce", "online store", "shop", "consumer product", "merchant"):
		profile, title, explanation = "retail", "Retail and commerce business", "Sales authorization, insurance, inventory, fulfillment, returns, customer terms, financial controls, and operating ownership apply to the confirmed model."
	}
	codes := append([]string(nil), profileRequirements[profile]...)
	teamSize, teamSizeErr := strconv.Atoi(strings.TrimSpace(facts.TeamSize))
	solo := (teamSizeErr == nil && teamSize <= 1) || strings.Contains(strings.ToLower(facts.TeamSize), "solo")
	requirements := make([]EvidenceDefinition, 0, len(codes))
	for _, code := range codes {
		if solo && (code == "workforce_records" || code == "role_responsibility") {
			continue
		}
		requirements = append(requirements, cloneEvidenceDefinition(evidenceCatalog[code]))
	}
	if solo {
		explanation += " Team-specific records are omitted because the confirmed business currently operates solo."
	}
	return ScopeSelection{CatalogVersion: EvidenceCatalogVersion, ScopePolicyVersion: ScopePolicyVersion, Profile: profile, Title: title, Explanation: explanation, Requirements: requirements}
}

func cloneEvidenceDefinition(value EvidenceDefinition) EvidenceDefinition {
	value.ArtifactHints = append([]string(nil), value.ArtifactHints...)
	return value
}

func containsCatalogTerm(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

package catalog

import "time"

type PackageCode string
type PackageMode string

const (
	PackageWork         PackageCode = "work"
	PackageAgents       PackageCode = "agents"
	PackageFinance      PackageCode = "finance"
	PackageMarketing    PackageCode = "marketing"
	PackageKnowledge    PackageCode = "knowledge"
	PackageIntegrations PackageCode = "integrations"

	ModeEnabled   PackageMode = "enabled"
	ModeReadOnly  PackageMode = "read_only"
	ModeSuspended PackageMode = "suspended"
)

type FeaturePackage struct {
	Code          PackageCode      `json:"code"`
	Version       uint64           `json:"version"`
	Name          string           `json:"name"`
	Description   string           `json:"description"`
	Dependencies  []PackageCode    `json:"dependencies,omitempty"`
	Features      []string         `json:"features"`
	DefaultLimits map[string]int64 `json:"default_limits,omitempty"`
}

type Plan struct {
	Code        string                      `json:"code"`
	Version     uint64                      `json:"version"`
	Name        string                      `json:"name"`
	Description string                      `json:"description"`
	Packages    map[PackageCode]PackageMode `json:"packages"`
}

type Offer struct {
	Code            string    `json:"code"`
	PlanCode        string    `json:"plan_code"`
	PlanVersion     uint64    `json:"plan_version"`
	Currency        string    `json:"currency"`
	AmountMinor     int64     `json:"amount_minor"`
	BillingInterval string    `json:"billing_interval"`
	Published       bool      `json:"-"`
	EffectiveFrom   time.Time `json:"effective_from"`
	StripePriceRef  string    `json:"-"`
}

type PublishedCatalog struct {
	Version     uint64           `json:"version"`
	PublishedAt time.Time        `json:"published_at"`
	Packages    []FeaturePackage `json:"packages"`
	Plans       []Plan           `json:"plans"`
	Offers      []Offer          `json:"offers"`
}

func Default(now time.Time) PublishedCatalog {
	packages := []FeaturePackage{
		{Code: PackageKnowledge, Version: 1, Name: "Knowledge", Description: "Source-attributed business facts, documents, evidence, and citations.", Features: []string{"knowledge.read", "knowledge.baseline"}, DefaultLimits: map[string]int64{"documents": 25}},
		{Code: PackageWork, Version: 1, Name: "Work", Description: "Accountable work across people and agents.", Features: []string{"work.read", "work.manage"}, DefaultLimits: map[string]int64{"active_items": 100}},
		{Code: PackageAgents, Version: 1, Name: "Agents", Description: "Governed specialist agents and coordinated boardrooms.", Dependencies: []PackageCode{PackageWork, PackageKnowledge}, Features: []string{"agents.configure", "agents.run"}, DefaultLimits: map[string]int64{"concurrent_runs": 2}},
		{Code: PackageFinance, Version: 1, Name: "Finance", Description: "Operational ledgers, accounts, entries, and reports.", Features: []string{"finance.read", "finance.post"}},
		{Code: PackageMarketing, Version: 1, Name: "Marketing", Description: "Brand knowledge, research, campaign planning, and content work.", Dependencies: []PackageCode{PackageKnowledge}, Features: []string{"marketing.read", "marketing.manage"}},
		{Code: PackageIntegrations, Version: 1, Name: "Integrations", Description: "Scoped, observable external connectors.", Features: []string{"integrations.read", "integrations.connect"}},
	}
	free := Plan{Code: "free", Version: 1, Name: "Free", Description: "A real Spyglass Account for exploring the operating model.", Packages: map[PackageCode]PackageMode{PackageKnowledge: ModeEnabled}}
	return PublishedCatalog{Version: 1, PublishedAt: now.UTC(), Packages: packages, Plans: []Plan{free}, Offers: []Offer{{Code: "free-v1", PlanCode: "free", PlanVersion: 1, Currency: "USD", AmountMinor: 0, BillingInterval: "none", Published: true, EffectiveFrom: now.UTC()}}}
}

func (c PublishedCatalog) Plan(code string) (Plan, bool) {
	for _, plan := range c.Plans {
		if plan.Code == code {
			return plan, true
		}
	}
	return Plan{}, false
}

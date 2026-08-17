package catalog

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

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
	team := Plan{Code: "team", Version: 1, Name: "Team", Description: "A focused operating surface for a growing team.", Packages: map[PackageCode]PackageMode{PackageKnowledge: ModeEnabled, PackageWork: ModeEnabled, PackageIntegrations: ModeEnabled}}
	operating := Plan{Code: "operating", Version: 1, Name: "Operating", Description: "The coordinated Spyglass operating system.", Packages: map[PackageCode]PackageMode{PackageKnowledge: ModeEnabled, PackageWork: ModeEnabled, PackageAgents: ModeEnabled, PackageFinance: ModeEnabled, PackageMarketing: ModeEnabled, PackageIntegrations: ModeEnabled}}
	return PublishedCatalog{Version: 2, PublishedAt: now.UTC(), Packages: packages, Plans: []Plan{free, team, operating}, Offers: []Offer{
		{Code: "free-v1", PlanCode: "free", PlanVersion: 1, Currency: "USD", AmountMinor: 0, BillingInterval: "none", Published: true, EffectiveFrom: now.UTC()},
		{Code: "team-monthly-v1", PlanCode: "team", PlanVersion: 1, Currency: "USD", AmountMinor: 4900, BillingInterval: "month", Published: true, EffectiveFrom: now.UTC()},
		{Code: "operating-monthly-v1", PlanCode: "operating", PlanVersion: 1, Currency: "USD", AmountMinor: 14900, BillingInterval: "month", Published: true, EffectiveFrom: now.UTC()},
	}}
}

func (c PublishedCatalog) Plan(code string) (Plan, bool) {
	for _, plan := range c.Plans {
		if plan.Code == code {
			return plan, true
		}
	}
	return Plan{}, false
}

func (c PublishedCatalog) Validate() error {
	if c.Version == 0 {
		return errors.New("catalog version is required")
	}
	packages := make(map[PackageCode]FeaturePackage, len(c.Packages))
	for _, item := range c.Packages {
		if item.Code == "" || item.Version == 0 || strings.TrimSpace(item.Name) == "" {
			return errors.New("package code, version, and name are required")
		}
		if _, exists := packages[item.Code]; exists {
			return fmt.Errorf("duplicate package %q", item.Code)
		}
		packages[item.Code] = item
	}
	for _, item := range c.Packages {
		for _, dependency := range item.Dependencies {
			if dependency == item.Code {
				return fmt.Errorf("package %q depends on itself", item.Code)
			}
			if _, exists := packages[dependency]; !exists {
				return fmt.Errorf("package %q has unknown dependency %q", item.Code, dependency)
			}
		}
	}
	if hasDependencyCycle(packages) {
		return errors.New("package dependencies contain a cycle")
	}
	plans := make(map[string]Plan, len(c.Plans))
	for _, plan := range c.Plans {
		if plan.Code == "" || plan.Version == 0 || strings.TrimSpace(plan.Name) == "" {
			return errors.New("plan code, version, and name are required")
		}
		if _, exists := plans[plan.Code]; exists {
			return fmt.Errorf("duplicate plan %q", plan.Code)
		}
		for code, mode := range plan.Packages {
			definition, exists := packages[code]
			if !exists {
				return fmt.Errorf("plan %q contains unknown package %q", plan.Code, code)
			}
			if mode != ModeEnabled && mode != ModeReadOnly && mode != ModeSuspended {
				return fmt.Errorf("plan %q contains invalid package mode", plan.Code)
			}
			if mode != ModeSuspended {
				for _, dependency := range definition.Dependencies {
					dependencyMode, included := plan.Packages[dependency]
					if !included || dependencyMode == ModeSuspended {
						return fmt.Errorf("plan %q package %q requires package %q", plan.Code, code, dependency)
					}
				}
			}
		}
		plans[plan.Code] = plan
	}
	offers := make(map[string]struct{}, len(c.Offers))
	for _, offer := range c.Offers {
		if offer.Code == "" || offer.PlanCode == "" || offer.PlanVersion == 0 {
			return errors.New("offer code and plan version are required")
		}
		if _, exists := offers[offer.Code]; exists {
			return fmt.Errorf("duplicate offer %q", offer.Code)
		}
		plan, exists := plans[offer.PlanCode]
		if !exists || plan.Version != offer.PlanVersion {
			return fmt.Errorf("offer %q references an unknown plan version", offer.Code)
		}
		if offer.AmountMinor < 0 || len(offer.Currency) != 3 || offer.Currency != strings.ToUpper(offer.Currency) {
			return fmt.Errorf("offer %q has invalid money", offer.Code)
		}
		if offer.BillingInterval != "none" && offer.BillingInterval != "month" && offer.BillingInterval != "year" {
			return fmt.Errorf("offer %q has invalid billing interval", offer.Code)
		}
		offers[offer.Code] = struct{}{}
	}
	return nil
}

func hasDependencyCycle(packages map[PackageCode]FeaturePackage) bool {
	const (
		visiting = 1
		visited  = 2
	)
	states := map[PackageCode]int{}
	var visit func(PackageCode) bool
	visit = func(code PackageCode) bool {
		if states[code] == visiting {
			return true
		}
		if states[code] == visited {
			return false
		}
		states[code] = visiting
		for _, dependency := range packages[code].Dependencies {
			if visit(dependency) {
				return true
			}
		}
		states[code] = visited
		return false
	}
	for code := range packages {
		if visit(code) {
			return true
		}
	}
	return false
}

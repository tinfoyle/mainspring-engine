package catalog

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type PackageCode string
type PackageMode string
type LimitCode string
type LimitKind string
type LimitCombineRule string

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

	LimitKindCapacity LimitKind = "capacity"

	LimitReplace LimitCombineRule = "replace"
	LimitAdd     LimitCombineRule = "add"
	LimitMaximum LimitCombineRule = "maximum"
	LimitMinimum LimitCombineRule = "minimum"
)

type FeaturePackage struct {
	Code          PackageCode         `json:"code"`
	Version       uint64              `json:"version"`
	Name          string              `json:"name"`
	Description   string              `json:"description"`
	Dependencies  []PackageCode       `json:"dependencies,omitempty"`
	Features      []string            `json:"features"`
	DefaultLimits map[LimitCode]int64 `json:"default_limits,omitempty"`
}

// LimitDefinition gives a numeric entitlement value stable enforcement
// semantics. Capacity is the first supported kind: callers atomically reserve
// and release units against the effective Account maximum.
type LimitDefinition struct {
	Code                  LimitCode        `json:"code"`
	PackageCode           PackageCode      `json:"package_code"`
	Name                  string           `json:"name"`
	Unit                  string           `json:"unit"`
	Kind                  LimitKind        `json:"kind"`
	Combine               LimitCombineRule `json:"combine"`
	ReservationTTLSeconds int64            `json:"reservation_ttl_seconds,omitempty"`
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
	Version     uint64            `json:"version"`
	PublishedAt time.Time         `json:"published_at"`
	Packages    []FeaturePackage  `json:"packages"`
	Limits      []LimitDefinition `json:"limits,omitempty"`
	Plans       []Plan            `json:"plans"`
	Offers      []Offer           `json:"offers"`
}

func Default(now time.Time) PublishedCatalog {
	packages := []FeaturePackage{
		{Code: PackageKnowledge, Version: 1, Name: "Knowledge", Description: "Source-attributed business facts, documents, evidence, and citations.", Features: []string{"knowledge.read", "knowledge.baseline"}, DefaultLimits: map[LimitCode]int64{"documents": 25}},
		{Code: PackageWork, Version: 1, Name: "Work", Description: "Accountable work across people and agents.", Features: []string{"work.read", "work.manage"}, DefaultLimits: map[LimitCode]int64{"active_items": 100}},
		{Code: PackageAgents, Version: 1, Name: "Agents", Description: "Governed specialist agents and coordinated boardrooms.", Dependencies: []PackageCode{PackageWork, PackageKnowledge}, Features: []string{"agents.configure", "agents.run"}, DefaultLimits: map[LimitCode]int64{"concurrent_runs": 2}},
		{Code: PackageFinance, Version: 1, Name: "Finance", Description: "Operational ledgers, accounts, entries, and reports.", Features: []string{"finance.read", "finance.post"}},
		{Code: PackageMarketing, Version: 1, Name: "Marketing", Description: "Brand knowledge, research, campaign planning, and content work.", Dependencies: []PackageCode{PackageKnowledge}, Features: []string{"marketing.read", "marketing.manage"}},
		{Code: PackageIntegrations, Version: 1, Name: "Integrations", Description: "Scoped, observable external connectors.", Features: []string{"integrations.read", "integrations.connect"}},
	}
	free := Plan{Code: "free", Version: 1, Name: "Free", Description: "A real Spyglass Account for exploring the operating model.", Packages: map[PackageCode]PackageMode{PackageKnowledge: ModeEnabled}}
	team := Plan{Code: "team", Version: 1, Name: "Team", Description: "A focused operating surface for a growing team.", Packages: map[PackageCode]PackageMode{PackageKnowledge: ModeEnabled, PackageWork: ModeEnabled, PackageIntegrations: ModeEnabled}}
	operating := Plan{Code: "operating", Version: 1, Name: "Operating", Description: "The coordinated Spyglass operating system.", Packages: map[PackageCode]PackageMode{PackageKnowledge: ModeEnabled, PackageWork: ModeEnabled, PackageAgents: ModeEnabled, PackageFinance: ModeEnabled, PackageMarketing: ModeEnabled, PackageIntegrations: ModeEnabled}}
	limits := []LimitDefinition{
		{Code: "documents", PackageCode: PackageKnowledge, Name: "Documents", Unit: "document", Kind: LimitKindCapacity, Combine: LimitMaximum},
		{Code: "active_items", PackageCode: PackageWork, Name: "Active work items", Unit: "work_item", Kind: LimitKindCapacity, Combine: LimitMaximum},
		{Code: "concurrent_runs", PackageCode: PackageAgents, Name: "Concurrent agent runs", Unit: "run", Kind: LimitKindCapacity, Combine: LimitMaximum, ReservationTTLSeconds: 3600},
	}
	return PublishedCatalog{Version: 2, PublishedAt: now.UTC(), Packages: packages, Limits: limits, Plans: []Plan{free, team, operating}, Offers: []Offer{
		{Code: "free-v1", PlanCode: "free", PlanVersion: 1, Currency: "USD", AmountMinor: 0, BillingInterval: "none", Published: true, EffectiveFrom: now.UTC()},
		{Code: "team-monthly-v1", PlanCode: "team", PlanVersion: 1, Currency: "USD", AmountMinor: 4900, BillingInterval: "month", Published: true, EffectiveFrom: now.UTC()},
		{Code: "operating-monthly-v1", PlanCode: "operating", PlanVersion: 1, Currency: "USD", AmountMinor: 14900, BillingInterval: "month", Published: true, EffectiveFrom: now.UTC()},
	}}
}

// EffectiveLimitDefinitions keeps immutable pre-definition Catalog versions
// executable during rollback. Legacy default limits receive conservative
// capacity/replace semantics; every newly governed draft must be explicit.
func (c PublishedCatalog) EffectiveLimitDefinitions() []LimitDefinition {
	definitions := append([]LimitDefinition(nil), c.Limits...)
	known := make(map[string]struct{}, len(definitions))
	for _, definition := range definitions {
		known[limitIdentity(definition.PackageCode, definition.Code)] = struct{}{}
	}
	for _, item := range c.Packages {
		for code := range item.DefaultLimits {
			if _, exists := known[limitIdentity(item.Code, code)]; exists {
				continue
			}
			definitions = append(definitions, LimitDefinition{Code: code, PackageCode: item.Code, Name: string(code), Unit: "unit", Kind: LimitKindCapacity, Combine: LimitReplace})
		}
	}
	return definitions
}

func (c PublishedCatalog) ValidateGoverned() error {
	if err := c.Validate(); err != nil {
		return err
	}
	definitions := make(map[string]struct{}, len(c.Limits))
	for _, definition := range c.Limits {
		definitions[limitIdentity(definition.PackageCode, definition.Code)] = struct{}{}
	}
	for _, item := range c.Packages {
		for code := range item.DefaultLimits {
			if _, exists := definitions[limitIdentity(item.Code, code)]; !exists {
				return fmt.Errorf("package %q default limit %q requires an explicit definition", item.Code, code)
			}
		}
	}
	return nil
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
	if len(c.Packages) == 0 || len(c.Plans) == 0 || len(c.Offers) == 0 {
		return errors.New("catalog packages, plans, and offers are required")
	}
	packages := make(map[PackageCode]FeaturePackage, len(c.Packages))
	for _, item := range c.Packages {
		if !validMachineCode(string(item.Code)) || item.Version == 0 || strings.TrimSpace(item.Name) == "" {
			return errors.New("package code, version, and name are required")
		}
		if _, exists := packages[item.Code]; exists {
			return fmt.Errorf("duplicate package %q", item.Code)
		}
		for code, value := range item.DefaultLimits {
			if !validMachineCode(string(code)) || value < 0 {
				return fmt.Errorf("package %q has an invalid default limit", item.Code)
			}
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
	limits := make(map[string]struct{}, len(c.Limits))
	for _, definition := range c.Limits {
		identity := limitIdentity(definition.PackageCode, definition.Code)
		if !validMachineCode(string(definition.Code)) || !validMachineCode(string(definition.PackageCode)) || strings.TrimSpace(definition.Name) == "" || !validMachineCode(definition.Unit) {
			return errors.New("limit code, package, name, and unit are required")
		}
		if _, exists := packages[definition.PackageCode]; !exists {
			return fmt.Errorf("limit %q belongs to unknown package %q", definition.Code, definition.PackageCode)
		}
		if _, exists := limits[identity]; exists {
			return fmt.Errorf("duplicate package limit %q", identity)
		}
		if definition.Kind != LimitKindCapacity {
			return fmt.Errorf("limit %q has unsupported kind", identity)
		}
		if definition.Combine != LimitReplace && definition.Combine != LimitAdd && definition.Combine != LimitMaximum && definition.Combine != LimitMinimum {
			return fmt.Errorf("limit %q has invalid combination rule", identity)
		}
		if definition.ReservationTTLSeconds < 0 || definition.ReservationTTLSeconds > int64((30*24*time.Hour)/time.Second) {
			return fmt.Errorf("limit %q has an invalid reservation TTL", identity)
		}
		limits[identity] = struct{}{}
	}
	if len(c.Limits) > 0 {
		for _, item := range c.Packages {
			for code := range item.DefaultLimits {
				if _, exists := limits[limitIdentity(item.Code, code)]; !exists {
					return fmt.Errorf("package %q default limit %q has no definition", item.Code, code)
				}
			}
		}
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
	free, exists := plans["free"]
	if !exists || len(free.Packages) == 0 {
		return errors.New("catalog requires a non-empty free plan")
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

func limitIdentity(packageCode PackageCode, limitCode LimitCode) string {
	return string(packageCode) + "/" + string(limitCode)
}

func validMachineCode(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
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

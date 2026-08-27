package catalog

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

type PackageCode string
type PackageMode string
type LimitCode string
type LimitKind string
type LimitCombineRule string
type AIComplexity string

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

	AIComplexitySimple    AIComplexity = "simple"
	AIComplexityEfficient AIComplexity = "efficient"
	AIComplexityBalanced  AIComplexity = "balanced"
	AIComplexityThorough  AIComplexity = "thorough"
	AIComplexityAdvanced  AIComplexity = "advanced"
)

var AIComplexities = []AIComplexity{AIComplexitySimple, AIComplexityEfficient, AIComplexityBalanced, AIComplexityThorough, AIComplexityAdvanced}

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

// AITokenRenewalGrant defines the provider-neutral quantity appended for one
// successfully paid positive subscription service period. The definition is
// immutable within a Catalog publication; Stripe projections freeze its
// Catalog version and code on the resulting Account grant.
type AITokenRenewalGrant struct {
	Code       string `json:"code"`
	Version    uint64 `json:"version"`
	Quantity   int64  `json:"quantity"`
	Disclosure string `json:"disclosure"`
}

// AITokenBundle is a separately purchasable, non-recurring top-up. Provider
// Price references remain in the private mapping table rather than this
// customer-safe Catalog record.
type AITokenBundle struct {
	Code          string    `json:"code"`
	Version       uint64    `json:"version"`
	Quantity      int64     `json:"quantity"`
	Currency      string    `json:"currency"`
	AmountMinor   int64     `json:"amount_minor"`
	EffectiveFrom time.Time `json:"effective_from"`
	Disclosure    string    `json:"disclosure"`
}

// CommissioningOffer is the optional one-time onboarding service. It grants
// no product entitlement and is intentionally distinct from recurring Offers
// and AI Token bundles, while sharing the Catalog's immutable pricing and
// private provider-Price mapping controls.
type CommissioningOffer struct {
	Code          string    `json:"code"`
	Version       uint64    `json:"version"`
	Currency      string    `json:"currency"`
	AmountMinor   int64     `json:"amount_minor"`
	EffectiveFrom time.Time `json:"effective_from"`
	Disclosure    string    `json:"disclosure"`
}

type AITokenPromotion struct {
	Code                  string    `json:"code"`
	Version               uint64    `json:"version"`
	Quantity              int64     `json:"quantity"`
	EffectiveFrom         time.Time `json:"effective_from"`
	EffectiveUntil        time.Time `json:"effective_until"`
	ExpiresAfterDays      int64     `json:"expires_after_days"`
	RedemptionsPerAccount int64     `json:"redemptions_per_account"`
	IssuanceCap           int64     `json:"issuance_cap"`
	Stacking              string    `json:"stacking"`
	Disclosure            string    `json:"disclosure"`
}

// AIComplexityRate maps the customer-facing complexity control to one private,
// reviewed execution target and an exact customer AI Token schedule. The
// public API deliberately projects only the complexity and token fields.
type AIComplexityRate struct {
	Code                       string       `json:"code"`
	Version                    uint64       `json:"version"`
	Complexity                 AIComplexity `json:"complexity"`
	InputPerThousand           int64        `json:"input_per_thousand"`
	CachedInputPerThousand     int64        `json:"cached_input_per_thousand"`
	OutputPerThousand          int64        `json:"output_per_thousand"`
	ToolInvocation             int64        `json:"tool_invocation"`
	MinimumCharge              int64        `json:"minimum_charge"`
	MaximumReservation         int64        `json:"maximum_reservation"`
	EstimatedMinimum           int64        `json:"estimated_minimum"`
	EstimatedMaximum           int64        `json:"estimated_maximum"`
	InternalProvider           string       `json:"internal_provider"`
	InternalModel              string       `json:"internal_model"`
	InternalFallbackModels     []string     `json:"internal_fallback_models,omitempty"`
	InternalReasoningEffort    string       `json:"internal_reasoning_effort,omitempty"`
	InternalAdapterVersion     uint64       `json:"internal_adapter_version"`
	InternalModelPolicyVersion uint64       `json:"internal_model_policy_version"`
}

type PublishedCatalog struct {
	Version             uint64               `json:"version"`
	PublishedAt         time.Time            `json:"published_at"`
	Packages            []FeaturePackage     `json:"packages"`
	Limits              []LimitDefinition    `json:"limits,omitempty"`
	Plans               []Plan               `json:"plans"`
	Offers              []Offer              `json:"offers"`
	AITokenRenewalGrant *AITokenRenewalGrant `json:"ai_token_renewal_grant,omitempty"`
	AITokenBundles      []AITokenBundle      `json:"ai_token_bundles,omitempty"`
	CommissioningOffer  *CommissioningOffer  `json:"commissioning_offer,omitempty"`
	AITokenPromotions   []AITokenPromotion   `json:"ai_token_promotions,omitempty"`
	AIComplexityRates   []AIComplexityRate   `json:"ai_complexity_rates,omitempty"`
}

func Default(now time.Time) PublishedCatalog {
	packages := []FeaturePackage{
		{Code: PackageKnowledge, Version: 1, Name: "Knowledge", Description: "Source-attributed business facts, documents, evidence, and citations.", Features: []string{"knowledge.read", "knowledge.baseline"}, DefaultLimits: map[LimitCode]int64{"documents": 1000}},
		{Code: PackageWork, Version: 1, Name: "Work", Description: "Accountable work across people and agents.", Features: []string{"work.read", "work.manage"}, DefaultLimits: map[LimitCode]int64{"active_items": 100}},
		{Code: PackageAgents, Version: 1, Name: "Agents", Description: "Governed specialist agents and coordinated boardrooms.", Dependencies: []PackageCode{PackageWork, PackageKnowledge}, Features: []string{"agents.configure", "agents.run"}, DefaultLimits: map[LimitCode]int64{"concurrent_runs": 2}},
		{Code: PackageFinance, Version: 1, Name: "Finance", Description: "Operational ledgers, accounts, entries, and reports.", Features: []string{"finance.read", "finance.post"}},
		{Code: PackageMarketing, Version: 1, Name: "Marketing", Description: "Brand knowledge, research, campaign planning, and content work.", Dependencies: []PackageCode{PackageKnowledge}, Features: []string{"marketing.read", "marketing.manage"}},
		{Code: PackageIntegrations, Version: 1, Name: "Integrations", Description: "Scoped, observable external connectors.", Dependencies: []PackageCode{PackageKnowledge}, Features: []string{"integrations.read", "integrations.connect"}},
	}
	team := Plan{Code: "team", Version: 2, Name: "Infinite Ocean Team", Description: "The complete Infinite Ocean operating system for one team.", Packages: map[PackageCode]PackageMode{PackageKnowledge: ModeEnabled, PackageWork: ModeEnabled, PackageAgents: ModeEnabled, PackageFinance: ModeEnabled, PackageMarketing: ModeEnabled, PackageIntegrations: ModeEnabled}}
	limits := []LimitDefinition{
		{Code: "documents", PackageCode: PackageKnowledge, Name: "Documents", Unit: "document", Kind: LimitKindCapacity, Combine: LimitMaximum},
		{Code: "active_items", PackageCode: PackageWork, Name: "Active work items", Unit: "work_item", Kind: LimitKindCapacity, Combine: LimitMaximum},
		{Code: "concurrent_runs", PackageCode: PackageAgents, Name: "Concurrent agent runs", Unit: "run", Kind: LimitKindCapacity, Combine: LimitMaximum, ReservationTTLSeconds: 3600},
	}
	renewalGrant := &AITokenRenewalGrant{Code: "team_renewal_v1", Version: 1, Quantity: 10_000, Disclosure: "Included with each successfully paid monthly team service period; unused included Tokens expire when the next paid renewal grant commits."}
	bundles := []AITokenBundle{{Code: "tokens_10k_v1", Version: 1, Quantity: 10_000, Currency: "USD", AmountMinor: 1000, EffectiveFrom: now.UTC(), Disclosure: "One-time team AI Token top-up. Purchased Tokens do not expire while the team Account remains active."}}
	commissioning := &CommissioningOffer{Code: "commissioning_v1", Version: 1, Currency: "USD", AmountMinor: 25_000, EffectiveFrom: now.UTC(), Disclosure: "Optional one-time onboarding and commissioning service. It grants no additional software access and earns no Affiliate commission."}
	rates := []AIComplexityRate{
		{Code: "simple_v1", Version: 1, Complexity: AIComplexitySimple, InputPerThousand: 1, CachedInputPerThousand: 1, OutputPerThousand: 4, ToolInvocation: 10, MinimumCharge: 5, MaximumReservation: 1_000, EstimatedMinimum: 5, EstimatedMaximum: 250, InternalProvider: "configurable", InternalModel: "simple", InternalAdapterVersion: 1, InternalModelPolicyVersion: 1},
		{Code: "efficient_v1", Version: 1, Complexity: AIComplexityEfficient, InputPerThousand: 2, CachedInputPerThousand: 1, OutputPerThousand: 8, ToolInvocation: 15, MinimumCharge: 10, MaximumReservation: 1_500, EstimatedMinimum: 10, EstimatedMaximum: 500, InternalProvider: "configurable", InternalModel: "efficient", InternalAdapterVersion: 1, InternalModelPolicyVersion: 1},
		{Code: "balanced_v1", Version: 1, Complexity: AIComplexityBalanced, InputPerThousand: 4, CachedInputPerThousand: 1, OutputPerThousand: 16, ToolInvocation: 25, MinimumCharge: 20, MaximumReservation: 2_500, EstimatedMinimum: 20, EstimatedMaximum: 1_000, InternalProvider: "configurable", InternalModel: "balanced", InternalAdapterVersion: 1, InternalModelPolicyVersion: 1},
		{Code: "thorough_v1", Version: 1, Complexity: AIComplexityThorough, InputPerThousand: 8, CachedInputPerThousand: 2, OutputPerThousand: 32, ToolInvocation: 50, MinimumCharge: 40, MaximumReservation: 5_000, EstimatedMinimum: 40, EstimatedMaximum: 2_000, InternalProvider: "configurable", InternalModel: "thorough", InternalAdapterVersion: 1, InternalModelPolicyVersion: 1},
		{Code: "advanced_v1", Version: 1, Complexity: AIComplexityAdvanced, InputPerThousand: 16, CachedInputPerThousand: 4, OutputPerThousand: 64, ToolInvocation: 100, MinimumCharge: 80, MaximumReservation: 10_000, EstimatedMinimum: 80, EstimatedMaximum: 4_000, InternalProvider: "configurable", InternalModel: "advanced", InternalAdapterVersion: 1, InternalModelPolicyVersion: 1},
	}
	return PublishedCatalog{Version: 3, PublishedAt: now.UTC(), Packages: packages, Limits: limits, Plans: []Plan{team}, Offers: []Offer{
		{Code: "team-monthly-v2", PlanCode: "team", PlanVersion: 2, Currency: "USD", AmountMinor: 5000, BillingInterval: "month", Published: true, EffectiveFrom: now.UTC()},
	}, AITokenRenewalGrant: renewalGrant, AITokenBundles: bundles, CommissioningOffer: commissioning, AIComplexityRates: rates}
}

// EffectiveLimitDefinitions keeps immutable pre-definition Catalog versions
// executable during rollback. Legacy default limits receive conservative
// capacity/replace semantics; every newly governed draft must be explicit.
func (c PublishedCatalog) EffectiveLimitDefinitions() []LimitDefinition {
	definitions := append([]LimitDefinition{}, c.Limits...)
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
	if err := c.validateAITokenCommerce(true); err != nil {
		return err
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
	if err := c.validateAITokenCommerce(false); err != nil {
		return err
	}
	return nil
}

func (c PublishedCatalog) validateAITokenCommerce(required bool) error {
	configured := c.AITokenRenewalGrant != nil || len(c.AITokenBundles) > 0 || c.CommissioningOffer != nil || len(c.AITokenPromotions) > 0 || len(c.AIComplexityRates) > 0
	if !configured {
		if required {
			return errors.New("governed catalog requires AI Token commerce and rates")
		}
		return nil
	}
	if c.AITokenRenewalGrant == nil || !validMachineCode(c.AITokenRenewalGrant.Code) || c.AITokenRenewalGrant.Version == 0 || c.AITokenRenewalGrant.Quantity <= 0 || strings.TrimSpace(c.AITokenRenewalGrant.Disclosure) == "" {
		return errors.New("AI Token renewal grant is invalid")
	}
	bundleCodes := map[string]struct{}{}
	for _, bundle := range c.AITokenBundles {
		if !validMachineCode(bundle.Code) || bundle.Version == 0 || bundle.Quantity <= 0 || bundle.AmountMinor <= 0 || bundle.Currency != "USD" || bundle.EffectiveFrom.IsZero() || strings.TrimSpace(bundle.Disclosure) == "" {
			return fmt.Errorf("AI Token bundle %q is invalid", bundle.Code)
		}
		if _, exists := bundleCodes[bundle.Code]; exists {
			return fmt.Errorf("duplicate AI Token bundle %q", bundle.Code)
		}
		bundleCodes[bundle.Code] = struct{}{}
	}
	if len(c.AITokenBundles) == 0 {
		return errors.New("AI Token top-up bundle is required")
	}
	if c.CommissioningOffer == nil || !validMachineCode(c.CommissioningOffer.Code) || c.CommissioningOffer.Version == 0 || c.CommissioningOffer.AmountMinor <= 0 || c.CommissioningOffer.Currency != "USD" || c.CommissioningOffer.EffectiveFrom.IsZero() || strings.TrimSpace(c.CommissioningOffer.Disclosure) == "" {
		return errors.New("commissioning offer is invalid")
	}
	promotionCodes := map[string]struct{}{}
	for _, promotion := range c.AITokenPromotions {
		if !validMachineCode(promotion.Code) || promotion.Version == 0 || promotion.Quantity <= 0 || promotion.EffectiveFrom.IsZero() || !promotion.EffectiveUntil.After(promotion.EffectiveFrom) || promotion.ExpiresAfterDays < 1 || promotion.ExpiresAfterDays > 3650 || promotion.RedemptionsPerAccount < 1 || promotion.RedemptionsPerAccount > 100 || promotion.IssuanceCap < promotion.RedemptionsPerAccount || promotion.Stacking != "none" || strings.TrimSpace(promotion.Disclosure) == "" {
			return fmt.Errorf("AI Token promotion %q is invalid", promotion.Code)
		}
		if _, exists := promotionCodes[promotion.Code]; exists {
			return fmt.Errorf("duplicate AI Token promotion %q", promotion.Code)
		}
		promotionCodes[promotion.Code] = struct{}{}
	}
	rateCodes, complexities := map[string]struct{}{}, map[AIComplexity]struct{}{}
	for _, rate := range c.AIComplexityRates {
		if err := ValidateAIComplexityRate(rate); err != nil {
			return fmt.Errorf("AI complexity rate %q is invalid: %w", rate.Code, err)
		}
		if _, exists := rateCodes[rate.Code]; exists {
			return fmt.Errorf("duplicate AI complexity rate %q", rate.Code)
		}
		if _, exists := complexities[rate.Complexity]; exists {
			return fmt.Errorf("duplicate AI complexity %q", rate.Complexity)
		}
		rateCodes[rate.Code], complexities[rate.Complexity] = struct{}{}, struct{}{}
	}
	for _, complexity := range AIComplexities {
		if _, exists := complexities[complexity]; !exists {
			return fmt.Errorf("AI complexity %q is missing", complexity)
		}
	}
	return nil
}

// ValidateAIComplexityRate checks the complete private immutable rate snapshot
// exchanged between admission and cell workers. Customer-facing projections
// omit the internal fields and do not call this validator.
func ValidateAIComplexityRate(rate AIComplexityRate) error {
	if !validMachineCode(rate.Code) || rate.Version == 0 || !validComplexity(rate.Complexity) || rate.InputPerThousand <= 0 || rate.CachedInputPerThousand <= 0 || rate.OutputPerThousand <= 0 || rate.ToolInvocation < 0 || rate.MinimumCharge <= 0 || rate.MaximumReservation < rate.MinimumCharge || rate.EstimatedMinimum < rate.MinimumCharge || rate.EstimatedMaximum < rate.EstimatedMinimum || rate.EstimatedMaximum > rate.MaximumReservation || !validExecutionCode.MatchString(rate.InternalProvider) || !validExecutionCode.MatchString(rate.InternalModel) || len(rate.InternalFallbackModels) > 2 || (rate.InternalReasoningEffort != "" && !validExecutionCode.MatchString(rate.InternalReasoningEffort)) || rate.InternalAdapterVersion == 0 || rate.InternalModelPolicyVersion == 0 {
		return errors.New("AI complexity rate fields are invalid")
	}
	models := append([]string{rate.InternalModel}, rate.InternalFallbackModels...)
	for index, model := range models {
		if !validExecutionCode.MatchString(model) || slices.Contains(models[:index], model) {
			return errors.New("AI complexity rate model targets are invalid")
		}
	}
	return nil
}

func validComplexity(value AIComplexity) bool {
	for _, complexity := range AIComplexities {
		if value == complexity {
			return true
		}
	}
	return false
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

package entitlements

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type GrantSource string

const (
	SourceFreePlan        GrantSource = "free_plan"
	SourceSubscription    GrantSource = "subscription"
	SourceTrial           GrantSource = "trial"
	SourcePromotion       GrantSource = "promotion"
	SourceSupportOverride GrantSource = "support_override"
	SourceGrandfathered   GrantSource = "grandfathered"
)

type Grant struct {
	ID              ids.GrantID
	AccountID       ids.AccountID
	PackageCode     catalog.PackageCode
	PackageVersion  uint64
	Mode            catalog.PackageMode
	Source          GrantSource
	SourceReference string
	Limits          map[catalog.LimitCode]int64
	StartsAt        time.Time
	EndsAt          *time.Time
	Priority        int
	Reason          string
}

type PackageAccess struct {
	Code          catalog.PackageCode               `json:"code"`
	Version       uint64                            `json:"version"`
	Mode          catalog.PackageMode               `json:"mode"`
	Limits        map[catalog.LimitCode]int64       `json:"limits,omitempty"`
	LimitPolicies map[catalog.LimitCode]LimitPolicy `json:"limit_policies,omitempty"`
	Sources       []GrantSource                     `json:"sources"`
}

// LimitPolicy contains only authorization semantics. Catalog display metadata
// stays outside the snapshot so copy edits do not advance entitlement versions.
type LimitPolicy struct {
	Kind                  catalog.LimitKind        `json:"kind"`
	Combine               catalog.LimitCombineRule `json:"combine"`
	ReservationTTLSeconds int64                    `json:"reservation_ttl_seconds,omitempty"`
}

type Snapshot struct {
	AccountID      ids.AccountID   `json:"account_id"`
	Version        uint64          `json:"version"`
	CatalogVersion uint64          `json:"catalog_version"`
	EvaluatedAt    time.Time       `json:"evaluated_at"`
	Packages       []PackageAccess `json:"packages"`
}

func FreePlanGrants(accountID ids.AccountID, plan catalog.Plan, packages []catalog.FeaturePackage, idGenerator ids.Generator, now time.Time) ([]Grant, error) {
	definitions := make(map[catalog.PackageCode]catalog.FeaturePackage, len(packages))
	for _, definition := range packages {
		definitions[definition.Code] = definition
	}
	grants := make([]Grant, 0, len(plan.Packages))
	for code, mode := range plan.Packages {
		definition, ok := definitions[code]
		if !ok {
			return nil, errors.New("free plan references an unknown package")
		}
		limits := make(map[catalog.LimitCode]int64, len(definition.DefaultLimits))
		for name, value := range definition.DefaultLimits {
			limits[name] = value
		}
		grants = append(grants, Grant{ID: ids.GrantID(idGenerator.New()), AccountID: accountID, PackageCode: code, PackageVersion: definition.Version, Mode: mode, Source: SourceFreePlan, SourceReference: plan.Code, Limits: limits, StartsAt: now.UTC(), Priority: 10, Reason: "current free plan"})
	}
	return grants, nil
}

func Evaluate(accountID ids.AccountID, version uint64, publication catalog.PublishedCatalog, grants []Grant, now time.Time) (Snapshot, error) {
	if accountID == "" || version == 0 || publication.Version == 0 {
		return Snapshot{}, errors.New("account ID, entitlement version, and Catalog version are required")
	}
	active := make(map[catalog.PackageCode][]Grant)
	packageDefinitions := make(map[catalog.PackageCode]catalog.FeaturePackage, len(publication.Packages))
	for _, definition := range publication.Packages {
		packageDefinitions[definition.Code] = definition
	}
	definitions := publication.EffectiveLimitDefinitions()
	limitDefinitions := make(map[catalog.PackageCode]map[catalog.LimitCode]catalog.LimitDefinition)
	for _, definition := range definitions {
		if limitDefinitions[definition.PackageCode] == nil {
			limitDefinitions[definition.PackageCode] = map[catalog.LimitCode]catalog.LimitDefinition{}
		}
		limitDefinitions[definition.PackageCode][definition.Code] = definition
	}
	grantIDs := make(map[ids.GrantID]struct{}, len(grants))
	for _, grant := range grants {
		if grant.AccountID != accountID {
			return Snapshot{}, errors.New("grant belongs to another account")
		}
		if grant.StartsAt.After(now) || (grant.EndsAt != nil && !grant.EndsAt.After(now)) {
			continue
		}
		if grant.ID == "" || grant.PackageCode == "" || grant.PackageVersion == 0 || (grant.Mode != catalog.ModeEnabled && grant.Mode != catalog.ModeReadOnly && grant.Mode != catalog.ModeSuspended) {
			return Snapshot{}, errors.New("grant ID, package version, and mode are invalid")
		}
		if _, exists := grantIDs[grant.ID]; exists {
			return Snapshot{}, errors.New("duplicate entitlement grant ID")
		}
		grantIDs[grant.ID] = struct{}{}
		if _, exists := packageDefinitions[grant.PackageCode]; !exists {
			return Snapshot{}, fmt.Errorf("grant references unknown package %q", grant.PackageCode)
		}
		for code, value := range grant.Limits {
			if code == "" || value < 0 {
				return Snapshot{}, errors.New("grant limit is invalid")
			}
			if _, exists := limitDefinitions[grant.PackageCode][code]; !exists {
				return Snapshot{}, fmt.Errorf("grant limit %q is not defined for package %q", code, grant.PackageCode)
			}
		}
		active[grant.PackageCode] = append(active[grant.PackageCode], grant)
	}
	codes := make([]string, 0, len(active))
	for code := range active {
		codes = append(codes, string(code))
	}
	sort.Strings(codes)
	packages := make([]PackageAccess, 0, len(codes))
	for _, raw := range codes {
		code := catalog.PackageCode(raw)
		group := active[code]
		sort.Slice(group, func(i, j int) bool { return grantPrecedes(group[i], group[j]) })
		winner := group[0]
		limitCodes := map[catalog.LimitCode]struct{}{}
		for _, grant := range group {
			for code := range grant.Limits {
				limitCodes[code] = struct{}{}
			}
		}
		limits := make(map[catalog.LimitCode]int64, len(limitCodes))
		policies := make(map[catalog.LimitCode]LimitPolicy, len(limitCodes))
		for limitCode := range limitCodes {
			definition, exists := limitDefinitions[code][limitCode]
			if !exists {
				return Snapshot{}, fmt.Errorf("limit %q is not defined for package %q", limitCode, code)
			}
			value, err := combineLimit(group, limitCode, definition.Combine)
			if err != nil {
				return Snapshot{}, err
			}
			limits[limitCode], policies[limitCode] = value, LimitPolicy{Kind: definition.Kind, Combine: definition.Combine, ReservationTTLSeconds: definition.ReservationTTLSeconds}
		}
		sources := make([]GrantSource, 0, len(group))
		for _, grant := range group {
			sources = append(sources, grant.Source)
		}
		packages = append(packages, PackageAccess{Code: code, Version: winner.PackageVersion, Mode: winner.Mode, Limits: limits, LimitPolicies: policies, Sources: sources})
	}
	applyDependencyModes(packages, packageDefinitions)
	return Snapshot{AccountID: accountID, Version: version, CatalogVersion: publication.Version, EvaluatedAt: now.UTC(), Packages: packages}, nil
}

func applyDependencyModes(packages []PackageAccess, definitions map[catalog.PackageCode]catalog.FeaturePackage) {
	effective := make(map[catalog.PackageCode]*PackageAccess, len(packages))
	for index := range packages {
		effective[packages[index].Code] = &packages[index]
	}
	for changed := true; changed; {
		changed = false
		for index := range packages {
			item := &packages[index]
			if item.Mode == catalog.ModeSuspended {
				continue
			}
			for _, dependency := range definitions[item.Code].Dependencies {
				required, exists := effective[dependency]
				if !exists || required.Mode == catalog.ModeSuspended {
					item.Mode, changed = catalog.ModeSuspended, true
					break
				}
				if required.Mode == catalog.ModeReadOnly && item.Mode == catalog.ModeEnabled {
					item.Mode, changed = catalog.ModeReadOnly, true
				}
			}
		}
	}
}

func grantPrecedes(left, right Grant) bool {
	if left.Priority != right.Priority {
		return left.Priority > right.Priority
	}
	if left.ID != right.ID {
		return left.ID < right.ID
	}
	if left.Source != right.Source {
		return left.Source < right.Source
	}
	return left.SourceReference < right.SourceReference
}

func combineLimit(grants []Grant, code catalog.LimitCode, rule catalog.LimitCombineRule) (int64, error) {
	values := make([]int64, 0, len(grants))
	for _, grant := range grants {
		if value, exists := grant.Limits[code]; exists {
			values = append(values, value)
		}
	}
	if len(values) == 0 {
		return 0, errors.New("limit has no values")
	}
	switch rule {
	case catalog.LimitReplace:
		return values[0], nil
	case catalog.LimitAdd:
		var total int64
		for _, value := range values {
			if value > math.MaxInt64-total {
				return 0, errors.New("combined limit exceeds integer range")
			}
			total += value
		}
		return total, nil
	case catalog.LimitMaximum:
		value := values[0]
		for _, candidate := range values[1:] {
			if candidate > value {
				value = candidate
			}
		}
		return value, nil
	case catalog.LimitMinimum:
		value := values[0]
		for _, candidate := range values[1:] {
			if candidate < value {
				value = candidate
			}
		}
		return value, nil
	default:
		return 0, errors.New("limit has an unsupported combination rule")
	}
}

func (s Snapshot) Allows(code catalog.PackageCode, mutation bool) bool {
	access, exists := s.Package(code)
	if !exists || access.Mode == catalog.ModeSuspended {
		return false
	}
	return !mutation || access.Mode == catalog.ModeEnabled
}

func (s Snapshot) Package(code catalog.PackageCode) (PackageAccess, bool) {
	for _, access := range s.Packages {
		if access.Code == code {
			return access, true
		}
	}
	return PackageAccess{}, false
}

package entitlements

import (
	"errors"
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
	Limits          map[string]int64
	StartsAt        time.Time
	EndsAt          *time.Time
	Priority        int
	Reason          string
}

type PackageAccess struct {
	Code    catalog.PackageCode `json:"code"`
	Version uint64              `json:"version"`
	Mode    catalog.PackageMode `json:"mode"`
	Limits  map[string]int64    `json:"limits,omitempty"`
	Sources []GrantSource       `json:"sources"`
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
		limits := make(map[string]int64, len(definition.DefaultLimits))
		for name, value := range definition.DefaultLimits {
			limits[name] = value
		}
		grants = append(grants, Grant{ID: ids.GrantID(idGenerator.New()), AccountID: accountID, PackageCode: code, PackageVersion: definition.Version, Mode: mode, Source: SourceFreePlan, SourceReference: plan.Code, Limits: limits, StartsAt: now.UTC(), Priority: 10, Reason: "current free plan"})
	}
	return grants, nil
}

func Evaluate(accountID ids.AccountID, version uint64, catalogVersion uint64, grants []Grant, now time.Time) (Snapshot, error) {
	if accountID == "" {
		return Snapshot{}, errors.New("account ID is required")
	}
	active := make(map[catalog.PackageCode][]Grant)
	for _, grant := range grants {
		if grant.AccountID != accountID {
			return Snapshot{}, errors.New("grant belongs to another account")
		}
		if grant.StartsAt.After(now) || (grant.EndsAt != nil && !grant.EndsAt.After(now)) {
			continue
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
		sort.SliceStable(group, func(i, j int) bool { return group[i].Priority > group[j].Priority })
		winner := group[0]
		limits := make(map[string]int64, len(winner.Limits))
		for key, value := range winner.Limits {
			limits[key] = value
		}
		sources := make([]GrantSource, 0, len(group))
		for _, grant := range group {
			sources = append(sources, grant.Source)
		}
		packages = append(packages, PackageAccess{Code: code, Version: winner.PackageVersion, Mode: winner.Mode, Limits: limits, Sources: sources})
	}
	return Snapshot{AccountID: accountID, Version: version, CatalogVersion: catalogVersion, EvaluatedAt: now.UTC(), Packages: packages}, nil
}

func (s Snapshot) Allows(code catalog.PackageCode, mutation bool) bool {
	for _, access := range s.Packages {
		if access.Code != code {
			continue
		}
		if access.Mode == catalog.ModeSuspended {
			return false
		}
		return !mutation || access.Mode == catalog.ModeEnabled
	}
	return false
}

package integrationexecution

import (
	"context"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

// CurrentAuthority reads one current global Account/entitlement snapshot. The
// connector worker's authenticated process and split database credential bind
// this workload boundary; no browser or Agent identity is accepted here.
type CurrentAuthority struct {
	source access.WorkloadStateSource
	cellID ids.CellID
}

func NewCurrentAuthority(source access.WorkloadStateSource, cellID ids.CellID) (*CurrentAuthority, error) {
	if source == nil || !routecontext.ValidCellID(cellID) {
		return nil, ErrInvalid
	}
	return &CurrentAuthority{source: source, cellID: cellID}, nil
}

func (authority *CurrentAuthority) Authorize(ctx context.Context, claim Claim) error {
	if authority == nil || authority.source == nil || ids.Validate(string(claim.AccountID)) != nil {
		return ErrInvalid
	}
	state, err := authority.source.WorkloadAccessState(ctx, claim.AccountID)
	if err != nil {
		return err
	}
	if state.Account.ID != claim.AccountID || state.Entitlements.AccountID != claim.AccountID ||
		state.Account.EntitlementVersion == 0 || state.Account.EntitlementVersion != state.Entitlements.Version ||
		state.Account.PlacementGeneration == 0 || state.Account.CellID != authority.cellID {
		return &access.DeniedError{Code: access.DenialCorruptContext}
	}
	if state.Account.State != accounts.AccountActive {
		return &access.DeniedError{Code: access.DenialAccountUnavailable}
	}
	for _, code := range []catalog.PackageCode{catalog.PackageMarketing, catalog.PackageIntegrations} {
		if err := requireEnabledPackage(state.Entitlements.Packages, code); err != nil {
			return err
		}
	}
	return nil
}

func requireEnabledPackage(packages []entitlements.PackageAccess, code catalog.PackageCode) error {
	var current entitlements.PackageAccess
	count := 0
	for _, candidate := range packages {
		if candidate.Code == code {
			current, count = candidate, count+1
		}
	}
	if count == 0 || current.Mode == catalog.ModeSuspended {
		return &access.DeniedError{Code: access.DenialPackageNotEntitled, Package: code}
	}
	if count != 1 || current.Version == 0 {
		return &access.DeniedError{Code: access.DenialCorruptContext, Package: code}
	}
	if current.Mode == catalog.ModeReadOnly {
		return &access.DeniedError{Code: access.DenialPackageReadOnly, Package: code}
	}
	if current.Mode != catalog.ModeEnabled {
		return &access.DeniedError{Code: access.DenialCorruptContext, Package: code}
	}
	return nil
}

var _ Authority = (*CurrentAuthority)(nil)

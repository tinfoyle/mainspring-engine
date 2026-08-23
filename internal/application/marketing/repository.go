// Package marketing defines the authorized application boundary for governed
// campaigns and release snapshots. Connector effects remain outside it.
package marketing

import (
	"context"
	"errors"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrInvalid    = errors.New("marketing command is invalid")
	ErrNotFound   = errors.New("marketing record was not found")
	ErrConflict   = errors.New("marketing command conflicts with durable state")
	ErrRepository = errors.New("marketing repository unavailable")
)

type Mutation struct {
	EventID       string
	Kind          string
	Actor         domain.Actor
	CorrelationID string
	At            time.Time
}

func (value Mutation) Valid() bool {
	validKind := map[string]bool{"created": true, "revised": true, "asset_revised": true, "submitted": true, "approved": true,
		"activated": true, "paused": true, "completed": true, "archived": true, "cancelled": true}[value.Kind]
	return ids.Validate(value.EventID) == nil && ids.Validate(value.CorrelationID) == nil && value.Actor.Valid() && validKind && !value.At.IsZero()
}

type Store interface {
	CreateCampaign(context.Context, domain.CampaignDraftInput, accounts.MembershipRole, Mutation) (domain.Campaign, bool, error)
	GetCampaign(context.Context, ids.AccountID, ids.MarketingCampaignID) (domain.Campaign, error)
	ListCampaigns(context.Context, ids.AccountID, CampaignListQuery) (CampaignPage, error)
	ReviseCampaign(context.Context, ids.AccountID, ids.MarketingCampaignID, domain.CampaignRevision, Mutation) (domain.Campaign, error)
	CreateAssetRevision(context.Context, domain.AssetRevisionInput, accounts.MembershipRole, Mutation) (domain.AssetRevision, bool, error)
	CreateReleasePlan(context.Context, domain.ReleasePlanInput, accounts.MembershipRole, Mutation) (domain.ReleasePlan, bool, error)
	GetReleasePlan(context.Context, ids.AccountID, ids.MarketingReleaseID) (domain.ReleasePlan, error)
	ListReleasePlans(context.Context, ids.AccountID, ReleaseListQuery) (ReleasePage, error)
	SubmitRelease(context.Context, ids.AccountID, ids.MarketingReleaseID, uint64, uint64, domain.Actor, accounts.MembershipRole, Mutation) (domain.ReleasePlan, error)
	ApproveRelease(context.Context, ids.AccountID, ids.MarketingReleaseID, uint64, ids.ConsequentialApprovalID, domain.Actor, accounts.MembershipRole, Mutation) (domain.ReleasePlan, error)
	CancelRelease(context.Context, ids.AccountID, ids.MarketingReleaseID, uint64, domain.Actor, accounts.MembershipRole, Mutation) (domain.ReleasePlan, error)
	ActivateCampaign(context.Context, ids.AccountID, ids.MarketingCampaignID, ids.MarketingReleaseID, uint64, domain.Actor, accounts.MembershipRole, Mutation) (domain.Campaign, error)
	PauseCampaign(context.Context, ids.AccountID, ids.MarketingCampaignID, uint64, domain.Actor, accounts.MembershipRole, Mutation) (domain.Campaign, error)
	CompleteCampaign(context.Context, ids.AccountID, ids.MarketingCampaignID, uint64, domain.Actor, accounts.MembershipRole, Mutation) (domain.Campaign, error)
	ArchiveCampaign(context.Context, ids.AccountID, ids.MarketingCampaignID, uint64, domain.Actor, accounts.MembershipRole, Mutation) (domain.Campaign, error)
}

func classify(err error) error {
	if err == nil || errors.Is(err, ErrInvalid) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) {
		return err
	}
	if errors.Is(err, domain.ErrConflict) {
		return ErrConflict
	}
	if errors.Is(err, domain.ErrInvalid) || errors.Is(err, domain.ErrState) || errors.Is(err, domain.ErrRole) || errors.Is(err, domain.ErrApproval) {
		return errors.Join(ErrInvalid, err)
	}
	return errors.Join(ErrRepository, err)
}

func ClassifyForAdapter(err error) error { return classify(err) }

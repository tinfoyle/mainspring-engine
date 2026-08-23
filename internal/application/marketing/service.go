package marketing

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const PackageCode = catalog.PackageMarketing

type Authorizer interface {
	Authorize(context.Context, access.Actor, ids.AccountID, access.Requirement) (access.AccountContext, error)
}

type Clock interface{ Now() time.Time }

type Service struct {
	authorizer Authorizer
	store      Store
	clock      Clock
}

func New(authorizer Authorizer, store Store, clock Clock) (*Service, error) {
	if authorizer == nil || store == nil || clock == nil {
		return nil, errors.New("Marketing dependencies are required")
	}
	return &Service{authorizer: authorizer, store: store, clock: clock}, nil
}

type CreateCampaignCommand struct {
	Actor      access.Actor
	AccountID  ids.AccountID
	RequestID  string
	Name       string
	Objective  string
	Audience   string
	Channels   []domain.Channel
	Provenance domain.Provenance
}

func (service *Service) CreateCampaign(ctx context.Context, command CreateCampaignCommand) (domain.Campaign, bool, error) {
	authorized, err := service.authorizeDraft(ctx, command.Actor, command.AccountID)
	if err != nil || ids.Validate(command.RequestID) != nil {
		if err != nil {
			return domain.Campaign{}, false, err
		}
		return domain.Campaign{}, false, ErrInvalid
	}
	actor, provenance, err := draftActor(command.Actor, command.Provenance)
	if err != nil {
		return domain.Campaign{}, false, err
	}
	now := service.clock.Now().UTC()
	return service.store.CreateCampaign(ctx, domain.CampaignDraftInput{ID: ids.MarketingCampaignID(command.RequestID), AccountID: command.AccountID,
		Name: command.Name, Objective: command.Objective, Audience: command.Audience, Channels: command.Channels, CreatedBy: actor, Provenance: provenance, CreatedAt: now},
		authorized.Role, mutation(command.RequestID, "created", actor, now))
}

func (service *Service) GetCampaign(ctx context.Context, actor access.Actor, accountID ids.AccountID, campaignID ids.MarketingCampaignID) (domain.Campaign, error) {
	if _, err := service.authorize(ctx, actor, accountID, false, false); err != nil {
		return domain.Campaign{}, err
	}
	if ids.Validate(string(campaignID)) != nil {
		return domain.Campaign{}, ErrInvalid
	}
	return service.store.GetCampaign(ctx, accountID, campaignID)
}

func (service *Service) ListCampaigns(ctx context.Context, actor access.Actor, accountID ids.AccountID, query CampaignListQuery) (CampaignPage, error) {
	if _, err := service.authorize(ctx, actor, accountID, false, false); err != nil {
		return CampaignPage{}, err
	}
	query, err := query.normalized()
	if err != nil {
		return CampaignPage{}, err
	}
	return service.store.ListCampaigns(ctx, accountID, query)
}

type ReviseCampaignCommand struct {
	Actor           access.Actor
	AccountID       ids.AccountID
	RequestID       string
	CampaignID      ids.MarketingCampaignID
	ExpectedVersion uint64
	Name            string
	Objective       string
	Audience        string
	Channels        []domain.Channel
}

func (service *Service) ReviseCampaign(ctx context.Context, command ReviseCampaignCommand) (domain.Campaign, error) {
	authorized, actor, now, err := service.userCommand(ctx, command.Actor, command.AccountID, command.RequestID, command.ExpectedVersion, false)
	if err != nil || ids.Validate(string(command.CampaignID)) != nil {
		if err != nil {
			return domain.Campaign{}, err
		}
		return domain.Campaign{}, ErrInvalid
	}
	revision := domain.CampaignRevision{Name: command.Name, Objective: command.Objective, Audience: command.Audience, Channels: command.Channels,
		ExpectedVersion: command.ExpectedVersion, Actor: actor, Role: authorized.Role, At: now}
	return service.store.ReviseCampaign(ctx, command.AccountID, command.CampaignID, revision, mutation(command.RequestID, "revised", actor, now))
}

type createAssetRevisionCommand struct {
	Actor            access.Actor
	AccountID        ids.AccountID
	RequestID        string
	CampaignID       ids.MarketingCampaignID
	AssetID          ids.MarketingAssetID
	Kind             domain.AssetKind
	Title            string
	MediaType        string
	ContentReference string
	ContentSHA256    [32]byte
	ContentBytes     uint64
	AlternativeText  string
	Provenance       domain.Provenance
}

func (service *Service) createAssetRevision(ctx context.Context, command createAssetRevisionCommand) (domain.AssetRevision, bool, error) {
	authorized, err := service.authorizeDraft(ctx, command.Actor, command.AccountID)
	_, referenceErr := domain.ObjectVersionFromContentReference(command.ContentReference)
	if err != nil || referenceErr != nil || ids.Validate(command.RequestID) != nil || ids.Validate(string(command.CampaignID)) != nil || ids.Validate(string(command.AssetID)) != nil {
		if err != nil {
			return domain.AssetRevision{}, false, err
		}
		return domain.AssetRevision{}, false, ErrInvalid
	}
	actor, provenance, err := draftActor(command.Actor, command.Provenance)
	if err != nil {
		return domain.AssetRevision{}, false, err
	}
	now := service.clock.Now().UTC()
	input := domain.AssetRevisionInput{ID: ids.MarketingAssetRevisionID(command.RequestID), AccountID: command.AccountID, CampaignID: command.CampaignID,
		AssetID: command.AssetID, Kind: command.Kind, Title: command.Title, MediaType: command.MediaType, ContentReference: command.ContentReference,
		ContentSHA256: command.ContentSHA256, ContentBytes: command.ContentBytes, AlternativeText: command.AlternativeText, CreatedBy: actor, Provenance: provenance, CreatedAt: now}
	return service.store.CreateAssetRevision(ctx, input, authorized.Role, mutation(command.RequestID, "asset_revised", actor, now))
}

func (service *Service) ListAssetRevisions(ctx context.Context, actor access.Actor, accountID ids.AccountID, query AssetRevisionListQuery) (AssetRevisionPage, error) {
	if _, err := service.authorize(ctx, actor, accountID, false, false); err != nil {
		return AssetRevisionPage{}, err
	}
	query, err := query.normalized()
	if err != nil {
		return AssetRevisionPage{}, err
	}
	return service.store.ListAssetRevisions(ctx, accountID, query)
}

type CreateReleaseCommand struct {
	Actor            access.Actor
	AccountID        ids.AccountID
	RequestID        string
	CampaignID       ids.MarketingCampaignID
	CampaignVersion  uint64
	Name             string
	Channels         []domain.Channel
	AssetRevisionIDs []ids.MarketingAssetRevisionID
	Provenance       domain.Provenance
}

func (service *Service) CreateRelease(ctx context.Context, command CreateReleaseCommand) (domain.ReleasePlan, bool, error) {
	authorized, err := service.authorizeDraft(ctx, command.Actor, command.AccountID)
	if err != nil || ids.Validate(command.RequestID) != nil || ids.Validate(string(command.CampaignID)) != nil || command.CampaignVersion == 0 {
		if err != nil {
			return domain.ReleasePlan{}, false, err
		}
		return domain.ReleasePlan{}, false, ErrInvalid
	}
	actor, provenance, err := draftActor(command.Actor, command.Provenance)
	if err != nil {
		return domain.ReleasePlan{}, false, err
	}
	now := service.clock.Now().UTC()
	input := domain.ReleasePlanInput{ID: ids.MarketingReleaseID(command.RequestID), AccountID: command.AccountID, CampaignID: command.CampaignID,
		CampaignVersion: command.CampaignVersion, Name: command.Name, Channels: command.Channels, AssetRevisionIDs: command.AssetRevisionIDs,
		CreatedBy: actor, Provenance: provenance, CreatedAt: now}
	return service.store.CreateReleasePlan(ctx, input, authorized.Role, mutation(command.RequestID, "created", actor, now))
}

func (service *Service) GetRelease(ctx context.Context, actor access.Actor, accountID ids.AccountID, releaseID ids.MarketingReleaseID) (domain.ReleasePlan, error) {
	if _, err := service.authorize(ctx, actor, accountID, false, false); err != nil {
		return domain.ReleasePlan{}, err
	}
	if ids.Validate(string(releaseID)) != nil {
		return domain.ReleasePlan{}, ErrInvalid
	}
	return service.store.GetReleasePlan(ctx, accountID, releaseID)
}

func (service *Service) ListReleases(ctx context.Context, actor access.Actor, accountID ids.AccountID, query ReleaseListQuery) (ReleasePage, error) {
	if _, err := service.authorize(ctx, actor, accountID, false, false); err != nil {
		return ReleasePage{}, err
	}
	query, err := query.normalized()
	if err != nil {
		return ReleasePage{}, err
	}
	return service.store.ListReleasePlans(ctx, accountID, query)
}

type ReleaseTransitionCommand struct {
	Actor           access.Actor
	AccountID       ids.AccountID
	RequestID       string
	ReleaseID       ids.MarketingReleaseID
	ExpectedVersion uint64
	CampaignVersion uint64
	ApprovalID      ids.ConsequentialApprovalID
}

func (service *Service) SubmitRelease(ctx context.Context, command ReleaseTransitionCommand) (domain.ReleasePlan, error) {
	authorized, actor, now, err := service.userCommand(ctx, command.Actor, command.AccountID, command.RequestID, command.ExpectedVersion, false)
	if err != nil || ids.Validate(string(command.ReleaseID)) != nil || command.CampaignVersion == 0 {
		if err != nil {
			return domain.ReleasePlan{}, err
		}
		return domain.ReleasePlan{}, ErrInvalid
	}
	return service.store.SubmitRelease(ctx, command.AccountID, command.ReleaseID, command.ExpectedVersion, command.CampaignVersion, actor, authorized.Role,
		mutation(command.RequestID, "submitted", actor, now))
}

func (service *Service) ApproveRelease(ctx context.Context, command ReleaseTransitionCommand) (domain.ReleasePlan, error) {
	authorized, actor, now, err := service.userCommand(ctx, command.Actor, command.AccountID, command.RequestID, command.ExpectedVersion, true)
	if err != nil || ids.Validate(string(command.ReleaseID)) != nil || ids.Validate(string(command.ApprovalID)) != nil {
		if err != nil {
			return domain.ReleasePlan{}, err
		}
		return domain.ReleasePlan{}, ErrInvalid
	}
	return service.store.ApproveRelease(ctx, command.AccountID, command.ReleaseID, command.ExpectedVersion, command.ApprovalID, actor, authorized.Role,
		mutation(command.RequestID, "approved", actor, now))
}

func (service *Service) CancelRelease(ctx context.Context, command ReleaseTransitionCommand) (domain.ReleasePlan, error) {
	authorized, actor, now, err := service.userCommand(ctx, command.Actor, command.AccountID, command.RequestID, command.ExpectedVersion, true)
	if err != nil || ids.Validate(string(command.ReleaseID)) != nil {
		if err != nil {
			return domain.ReleasePlan{}, err
		}
		return domain.ReleasePlan{}, ErrInvalid
	}
	return service.store.CancelRelease(ctx, command.AccountID, command.ReleaseID, command.ExpectedVersion, actor, authorized.Role,
		mutation(command.RequestID, "cancelled", actor, now))
}

type CampaignTransitionCommand struct {
	Actor           access.Actor
	AccountID       ids.AccountID
	RequestID       string
	CampaignID      ids.MarketingCampaignID
	ReleaseID       ids.MarketingReleaseID
	ExpectedVersion uint64
}

func (service *Service) ActivateCampaign(ctx context.Context, command CampaignTransitionCommand) (domain.Campaign, error) {
	return service.campaignTransition(ctx, command, "activated")
}

func (service *Service) PauseCampaign(ctx context.Context, command CampaignTransitionCommand) (domain.Campaign, error) {
	return service.campaignTransition(ctx, command, "paused")
}

func (service *Service) CompleteCampaign(ctx context.Context, command CampaignTransitionCommand) (domain.Campaign, error) {
	return service.campaignTransition(ctx, command, "completed")
}

func (service *Service) ArchiveCampaign(ctx context.Context, command CampaignTransitionCommand) (domain.Campaign, error) {
	return service.campaignTransition(ctx, command, "archived")
}

func (service *Service) campaignTransition(ctx context.Context, command CampaignTransitionCommand, kind string) (domain.Campaign, error) {
	authorized, actor, now, err := service.userCommand(ctx, command.Actor, command.AccountID, command.RequestID, command.ExpectedVersion, true)
	if err != nil || ids.Validate(string(command.CampaignID)) != nil || (kind == "activated" && ids.Validate(string(command.ReleaseID)) != nil) {
		if err != nil {
			return domain.Campaign{}, err
		}
		return domain.Campaign{}, ErrInvalid
	}
	mutation := mutation(command.RequestID, kind, actor, now)
	switch kind {
	case "activated":
		return service.store.ActivateCampaign(ctx, command.AccountID, command.CampaignID, command.ReleaseID, command.ExpectedVersion, actor, authorized.Role, mutation)
	case "paused":
		return service.store.PauseCampaign(ctx, command.AccountID, command.CampaignID, command.ExpectedVersion, actor, authorized.Role, mutation)
	case "completed":
		return service.store.CompleteCampaign(ctx, command.AccountID, command.CampaignID, command.ExpectedVersion, actor, authorized.Role, mutation)
	default:
		return service.store.ArchiveCampaign(ctx, command.AccountID, command.CampaignID, command.ExpectedVersion, actor, authorized.Role, mutation)
	}
}

func (service *Service) authorize(ctx context.Context, actor access.Actor, accountID ids.AccountID, mutation, manage bool) (access.AccountContext, error) {
	if !actor.Valid() || ids.Validate(string(accountID)) != nil || (mutation && actor.UserID == "") {
		return access.AccountContext{}, ErrInvalid
	}
	requirement := access.Requirement{Package: PackageCode, Mutation: mutation}
	if mutation {
		requirement.Roles = []accounts.MembershipRole{accounts.RoleOwner, accounts.RoleAdministrator, accounts.RoleMember}
		if manage {
			requirement.Roles = requirement.Roles[:2]
		}
	}
	return service.authorizer.Authorize(ctx, actor, accountID, requirement)
}

func (service *Service) authorizeDraft(ctx context.Context, actor access.Actor, accountID ids.AccountID) (access.AccountContext, error) {
	if !actor.Valid() || ids.Validate(string(accountID)) != nil {
		return access.AccountContext{}, ErrInvalid
	}
	requirement := access.Requirement{Package: PackageCode, Mutation: true}
	if actor.UserID != "" {
		requirement.Roles = []accounts.MembershipRole{accounts.RoleOwner, accounts.RoleAdministrator, accounts.RoleMember}
	}
	return service.authorizer.Authorize(ctx, actor, accountID, requirement)
}

func (service *Service) userCommand(ctx context.Context, actor access.Actor, accountID ids.AccountID, requestID string, expected uint64, manage bool) (access.AccountContext, domain.Actor, time.Time, error) {
	authorized, err := service.authorize(ctx, actor, accountID, true, manage)
	if err != nil {
		return access.AccountContext{}, domain.Actor{}, time.Time{}, err
	}
	if ids.Validate(requestID) != nil || expected == 0 {
		return access.AccountContext{}, domain.Actor{}, time.Time{}, ErrInvalid
	}
	now := service.clock.Now().UTC()
	return authorized, domain.Actor{Kind: domain.ActorUser, ID: string(actor.UserID)}, now, nil
}

func draftActor(actor access.Actor, provenance domain.Provenance) (domain.Actor, domain.Provenance, error) {
	if actor.UserID != "" {
		if provenance.Origin == "" {
			provenance.Origin = domain.OriginHuman
		}
		if provenance.Origin != domain.OriginHuman || provenance.RunID != "" || provenance.InvocationID != "" {
			return domain.Actor{}, domain.Provenance{}, ErrInvalid
		}
		return domain.Actor{Kind: domain.ActorUser, ID: string(actor.UserID)}, provenance, nil
	}
	invocationID := strings.TrimPrefix(actor.WorkloadID, "runner-invocation:")
	if invocationID == actor.WorkloadID || ids.Validate(invocationID) != nil || provenance.Origin != domain.OriginAgent ||
		string(provenance.InvocationID) != invocationID || ids.Validate(string(provenance.RunID)) != nil {
		return domain.Actor{}, domain.Provenance{}, ErrInvalid
	}
	return domain.Actor{Kind: domain.ActorWorkload, ID: actor.WorkloadID}, provenance, nil
}

func mutation(requestID, kind string, actor domain.Actor, at time.Time) Mutation {
	return Mutation{EventID: requestID, Kind: kind, Actor: actor, CorrelationID: requestID, At: at}
}

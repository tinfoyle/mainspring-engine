package marketing

import (
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type ReleaseState string

const (
	ReleaseDraft     ReleaseState = "draft"
	ReleaseSubmitted ReleaseState = "submitted"
	ReleaseApproved  ReleaseState = "approved"
	ReleaseCancelled ReleaseState = "cancelled"
)

type ReleasePlan struct {
	ID               ids.MarketingReleaseID         `json:"id"`
	AccountID        ids.AccountID                  `json:"account_id"`
	CampaignID       ids.MarketingCampaignID        `json:"campaign_id"`
	CampaignVersion  uint64                         `json:"campaign_version"`
	Name             string                         `json:"name"`
	Channels         []Channel                      `json:"channels"`
	AssetRevisionIDs []ids.MarketingAssetRevisionID `json:"asset_revision_ids"`
	State            ReleaseState                   `json:"state"`
	ApprovalID       ids.ConsequentialApprovalID    `json:"approval_id,omitempty"`
	Version          uint64                         `json:"version"`
	CreatedBy        Actor                          `json:"created_by"`
	Provenance       Provenance                     `json:"provenance"`
	SubmittedBy      *Actor                         `json:"submitted_by,omitempty"`
	ApprovedBy       *Actor                         `json:"approved_by,omitempty"`
	CreatedAt        time.Time                      `json:"created_at"`
	UpdatedAt        time.Time                      `json:"updated_at"`
}

type ReleasePlanInput struct {
	ID               ids.MarketingReleaseID
	AccountID        ids.AccountID
	CampaignID       ids.MarketingCampaignID
	CampaignVersion  uint64
	Name             string
	Channels         []Channel
	AssetRevisionIDs []ids.MarketingAssetRevisionID
	CreatedBy        Actor
	Provenance       Provenance
	CreatedAt        time.Time
}

func NewReleasePlan(input ReleasePlanInput, role accounts.MembershipRole) (ReleasePlan, error) {
	if !input.CreatedBy.valid() || !input.Provenance.valid(input.CreatedBy) || (input.CreatedBy.Kind == ActorUser && !canDraft(role)) {
		return ReleasePlan{}, ErrRole
	}
	value := ReleasePlan{ID: input.ID, AccountID: input.AccountID, CampaignID: input.CampaignID, CampaignVersion: input.CampaignVersion,
		Name: strings.TrimSpace(input.Name), Channels: append([]Channel(nil), input.Channels...), AssetRevisionIDs: append([]ids.MarketingAssetRevisionID(nil), input.AssetRevisionIDs...),
		State: ReleaseDraft, Version: 1, CreatedBy: input.CreatedBy, Provenance: input.Provenance, CreatedAt: input.CreatedAt.UTC(), UpdatedAt: input.CreatedAt.UTC()}
	return RestoreReleasePlan(value)
}

func RestoreReleasePlan(value ReleasePlan) (ReleasePlan, error) {
	value.Name, value.CreatedAt, value.UpdatedAt = strings.TrimSpace(value.Name), value.CreatedAt.UTC(), value.UpdatedAt.UTC()
	channels, channelErr := normalizeChannels(value.Channels)
	assets, assetErr := normalizeAssetRevisionIDs(value.AssetRevisionIDs)
	if channelErr != nil || assetErr != nil || ids.Validate(string(value.ID)) != nil || ids.Validate(string(value.AccountID)) != nil || ids.Validate(string(value.CampaignID)) != nil ||
		value.CampaignVersion == 0 || !validText(value.Name, MaximumReleaseNameBytes, true) ||
		(value.State != ReleaseDraft && value.State != ReleaseSubmitted && value.State != ReleaseApproved && value.State != ReleaseCancelled) ||
		value.Version == 0 || !value.CreatedBy.valid() || !value.Provenance.valid(value.CreatedBy) || value.CreatedAt.IsZero() || value.UpdatedAt.Before(value.CreatedAt) {
		return ReleasePlan{}, ErrInvalid
	}
	if value.State == ReleaseDraft {
		if value.SubmittedBy != nil || value.ApprovedBy != nil || value.ApprovalID != "" {
			return ReleasePlan{}, ErrInvalid
		}
	} else if value.SubmittedBy == nil || value.SubmittedBy.Kind != ActorUser || !value.SubmittedBy.valid() {
		return ReleasePlan{}, ErrInvalid
	}
	if value.State == ReleaseApproved {
		if value.ApprovedBy == nil || value.ApprovedBy.Kind != ActorUser || !value.ApprovedBy.valid() || ids.Validate(string(value.ApprovalID)) != nil {
			return ReleasePlan{}, ErrInvalid
		}
	} else if value.ApprovedBy != nil || value.ApprovalID != "" {
		return ReleasePlan{}, ErrInvalid
	}
	value.Channels, value.AssetRevisionIDs = channels, assets
	return value, nil
}

func (value ReleasePlan) Submit(expectedVersion, currentCampaignVersion uint64, actor Actor, role accounts.MembershipRole, at time.Time) (ReleasePlan, error) {
	if expectedVersion != value.Version {
		return ReleasePlan{}, ErrConflict
	}
	if value.State != ReleaseDraft || currentCampaignVersion != value.CampaignVersion {
		return ReleasePlan{}, ErrState
	}
	if actor.Kind != ActorUser || !actor.valid() || !canDraft(role) {
		return ReleasePlan{}, ErrRole
	}
	if !validTime(at, value.UpdatedAt) {
		return ReleasePlan{}, ErrInvalid
	}
	value.State, value.SubmittedBy, value.Version, value.UpdatedAt = ReleaseSubmitted, &actor, value.Version+1, at.UTC()
	return RestoreReleasePlan(value)
}

func (value ReleasePlan) Approve(expectedVersion uint64, approvalID ids.ConsequentialApprovalID, actor Actor, role accounts.MembershipRole, at time.Time) (ReleasePlan, error) {
	if expectedVersion != value.Version {
		return ReleasePlan{}, ErrConflict
	}
	if value.State != ReleaseSubmitted {
		return ReleasePlan{}, ErrState
	}
	if actor.Kind != ActorUser || !actor.valid() || !canManage(role) {
		return ReleasePlan{}, ErrRole
	}
	if ids.Validate(string(approvalID)) != nil || !validTime(at, value.UpdatedAt) {
		return ReleasePlan{}, ErrApproval
	}
	value.State, value.ApprovalID, value.ApprovedBy, value.Version, value.UpdatedAt = ReleaseApproved, approvalID, &actor, value.Version+1, at.UTC()
	return RestoreReleasePlan(value)
}

func (value ReleasePlan) Cancel(expectedVersion uint64, actor Actor, role accounts.MembershipRole, at time.Time) (ReleasePlan, error) {
	if expectedVersion != value.Version {
		return ReleasePlan{}, ErrConflict
	}
	if value.State == ReleaseDraft {
		return ReleasePlan{}, ErrState
	}
	if actor.Kind != ActorUser || !actor.valid() || !canManage(role) {
		return ReleasePlan{}, ErrRole
	}
	if value.State == ReleaseCancelled {
		return value, nil
	}
	if !validTime(at, value.UpdatedAt) {
		return ReleasePlan{}, ErrInvalid
	}
	value.State, value.ApprovalID, value.ApprovedBy, value.Version, value.UpdatedAt = ReleaseCancelled, "", nil, value.Version+1, at.UTC()
	return RestoreReleasePlan(value)
}

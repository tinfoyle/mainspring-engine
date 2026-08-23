package marketing

import (
	"slices"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type CampaignState string

const (
	CampaignDraft     CampaignState = "draft"
	CampaignActive    CampaignState = "active"
	CampaignPaused    CampaignState = "paused"
	CampaignCompleted CampaignState = "completed"
	CampaignArchived  CampaignState = "archived"
)

type Campaign struct {
	ID              ids.MarketingCampaignID `json:"id"`
	AccountID       ids.AccountID           `json:"account_id"`
	Name            string                  `json:"name"`
	Objective       string                  `json:"objective"`
	Audience        string                  `json:"audience"`
	Channels        []Channel               `json:"channels"`
	State           CampaignState           `json:"state"`
	ActiveReleaseID ids.MarketingReleaseID  `json:"active_release_id,omitempty"`
	Version         uint64                  `json:"version"`
	CreatedBy       Actor                   `json:"created_by"`
	Provenance      Provenance              `json:"provenance"`
	CreatedAt       time.Time               `json:"created_at"`
	UpdatedAt       time.Time               `json:"updated_at"`
}

type CampaignDraftInput struct {
	ID         ids.MarketingCampaignID
	AccountID  ids.AccountID
	Name       string
	Objective  string
	Audience   string
	Channels   []Channel
	CreatedBy  Actor
	Provenance Provenance
	CreatedAt  time.Time
}

func NewCampaign(input CampaignDraftInput, role accounts.MembershipRole) (Campaign, error) {
	if !input.CreatedBy.valid() || !input.Provenance.valid(input.CreatedBy) || (input.CreatedBy.Kind == ActorUser && !canDraft(role)) {
		return Campaign{}, ErrRole
	}
	value := Campaign{ID: input.ID, AccountID: input.AccountID, Name: strings.TrimSpace(input.Name), Objective: strings.TrimSpace(input.Objective),
		Audience: strings.TrimSpace(input.Audience), Channels: append([]Channel(nil), input.Channels...), State: CampaignDraft, Version: 1,
		CreatedBy: input.CreatedBy, Provenance: input.Provenance, CreatedAt: input.CreatedAt.UTC(), UpdatedAt: input.CreatedAt.UTC()}
	return RestoreCampaign(value)
}

func RestoreCampaign(value Campaign) (Campaign, error) {
	value.Name, value.Objective, value.Audience = strings.TrimSpace(value.Name), strings.TrimSpace(value.Objective), strings.TrimSpace(value.Audience)
	value.CreatedAt, value.UpdatedAt = value.CreatedAt.UTC(), value.UpdatedAt.UTC()
	channels, err := normalizeChannels(value.Channels)
	if err != nil || ids.Validate(string(value.ID)) != nil || ids.Validate(string(value.AccountID)) != nil ||
		!validText(value.Name, MaximumNameBytes, true) || !validText(value.Objective, MaximumObjectiveBytes, true) || !validText(value.Audience, MaximumAudienceBytes, true) ||
		(value.State != CampaignDraft && value.State != CampaignActive && value.State != CampaignPaused && value.State != CampaignCompleted && value.State != CampaignArchived) ||
		value.Version == 0 || !value.CreatedBy.valid() || !value.Provenance.valid(value.CreatedBy) || value.CreatedAt.IsZero() || value.UpdatedAt.Before(value.CreatedAt) {
		return Campaign{}, ErrInvalid
	}
	if value.State == CampaignActive && ids.Validate(string(value.ActiveReleaseID)) != nil {
		return Campaign{}, ErrInvalid
	}
	if value.ActiveReleaseID != "" && ids.Validate(string(value.ActiveReleaseID)) != nil {
		return Campaign{}, ErrInvalid
	}
	value.Channels = channels
	return value, nil
}

type CampaignRevision struct {
	Name            string
	Objective       string
	Audience        string
	Channels        []Channel
	ExpectedVersion uint64
	Actor           Actor
	Role            accounts.MembershipRole
	At              time.Time
}

func (value Campaign) Revise(command CampaignRevision) (Campaign, error) {
	if command.ExpectedVersion != value.Version {
		return Campaign{}, ErrConflict
	}
	if value.State != CampaignDraft && value.State != CampaignPaused {
		return Campaign{}, ErrState
	}
	if command.Actor.Kind != ActorUser || !command.Actor.valid() || !canDraft(command.Role) {
		return Campaign{}, ErrRole
	}
	if !validTime(command.At, value.UpdatedAt) {
		return Campaign{}, ErrInvalid
	}
	value.Name, value.Objective, value.Audience = strings.TrimSpace(command.Name), strings.TrimSpace(command.Objective), strings.TrimSpace(command.Audience)
	value.Channels, value.Version, value.UpdatedAt = append([]Channel(nil), command.Channels...), value.Version+1, command.At.UTC()
	return RestoreCampaign(value)
}

func (value Campaign) Activate(release ReleasePlan, expectedVersion uint64, actor Actor, role accounts.MembershipRole, at time.Time) (Campaign, error) {
	if expectedVersion != value.Version {
		return Campaign{}, ErrConflict
	}
	if value.State != CampaignDraft && value.State != CampaignPaused {
		return Campaign{}, ErrState
	}
	if actor.Kind != ActorUser || !actor.valid() || !canManage(role) {
		return Campaign{}, ErrRole
	}
	if release.State != ReleaseApproved || release.AccountID != value.AccountID || release.CampaignID != value.ID || release.CampaignVersion != value.Version ||
		!slices.Equal(release.Channels, value.Channels) || !validTime(at, value.UpdatedAt) {
		return Campaign{}, ErrApproval
	}
	value.State, value.ActiveReleaseID, value.Version, value.UpdatedAt = CampaignActive, release.ID, value.Version+1, at.UTC()
	return RestoreCampaign(value)
}

func (value Campaign) Pause(expectedVersion uint64, actor Actor, role accounts.MembershipRole, at time.Time) (Campaign, error) {
	return value.transition(expectedVersion, actor, role, at, CampaignActive, CampaignPaused)
}

func (value Campaign) Complete(expectedVersion uint64, actor Actor, role accounts.MembershipRole, at time.Time) (Campaign, error) {
	if value.State != CampaignActive && value.State != CampaignPaused {
		return Campaign{}, ErrState
	}
	return value.transition(expectedVersion, actor, role, at, value.State, CampaignCompleted)
}

func (value Campaign) Archive(expectedVersion uint64, actor Actor, role accounts.MembershipRole, at time.Time) (Campaign, error) {
	if value.State == CampaignActive || value.State == CampaignArchived {
		return Campaign{}, ErrState
	}
	return value.transition(expectedVersion, actor, role, at, value.State, CampaignArchived)
}

func (value Campaign) transition(expectedVersion uint64, actor Actor, role accounts.MembershipRole, at time.Time, from, to CampaignState) (Campaign, error) {
	if expectedVersion != value.Version {
		return Campaign{}, ErrConflict
	}
	if value.State != from {
		return Campaign{}, ErrState
	}
	if actor.Kind != ActorUser || !actor.valid() || !canManage(role) {
		return Campaign{}, ErrRole
	}
	if !validTime(at, value.UpdatedAt) {
		return Campaign{}, ErrInvalid
	}
	value.State, value.Version, value.UpdatedAt = to, value.Version+1, at.UTC()
	return RestoreCampaign(value)
}

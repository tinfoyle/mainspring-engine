// Package marketing owns Account-scoped campaign intent, immutable creative
// revisions, and human-approved release snapshots. It deliberately has no
// connector, provider SDK, transport, database, or delivery dependency.
package marketing

import (
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	MaximumNameBytes             = 160
	MaximumObjectiveBytes        = 4000
	MaximumAudienceBytes         = 4000
	MaximumAssetTitleBytes       = 240
	MaximumAlternativeTextBytes  = 1000
	MaximumContentReferenceBytes = 500
	MaximumMediaTypeBytes        = 100
	MaximumReleaseNameBytes      = 160
	MaximumReleaseAssets         = 100
)

var (
	ErrInvalid  = errors.New("marketing aggregate is invalid")
	ErrConflict = errors.New("marketing aggregate version conflict")
	ErrState    = errors.New("marketing state change is not allowed")
	ErrRole     = errors.New("role cannot perform the marketing command")
	ErrApproval = errors.New("marketing release approval is invalid")
)

type ActorKind string

const (
	ActorUser     ActorKind = "user"
	ActorWorkload ActorKind = "workload"
)

type Actor struct {
	Kind ActorKind `json:"kind"`
	ID   string    `json:"id"`
}

func (actor Actor) valid() bool {
	if actor.ID == "" || actor.ID != strings.TrimSpace(actor.ID) || len(actor.ID) > 200 || strings.ContainsRune(actor.ID, '\x00') {
		return false
	}
	if actor.Kind == ActorUser {
		return ids.Validate(actor.ID) == nil
	}
	return actor.Kind == ActorWorkload
}

type Origin string

const (
	OriginHuman Origin = "human"
	OriginAgent Origin = "agent"
)

type Provenance struct {
	Origin       Origin                `json:"origin"`
	RunID        ids.RunID             `json:"run_id,omitempty"`
	InvocationID ids.AgentInvocationID `json:"invocation_id,omitempty"`
}

func (value Provenance) valid(actor Actor) bool {
	switch value.Origin {
	case OriginHuman:
		return actor.Kind == ActorUser && value.RunID == "" && value.InvocationID == ""
	case OriginAgent:
		return actor.Kind == ActorWorkload && ids.Validate(string(value.RunID)) == nil && ids.Validate(string(value.InvocationID)) == nil
	default:
		return false
	}
}

func canDraft(role accounts.MembershipRole) bool {
	return role == accounts.RoleOwner || role == accounts.RoleAdministrator || role == accounts.RoleMember
}

func canManage(role accounts.MembershipRole) bool {
	return role == accounts.RoleOwner || role == accounts.RoleAdministrator
}

func validText(value string, maximum int, required bool) bool {
	return value == strings.TrimSpace(value) && (!required || value != "") && utf8.ValidString(value) && len(value) <= maximum && !strings.ContainsRune(value, '\x00')
}

func validTime(at time.Time, notBefore time.Time) bool {
	return !at.IsZero() && !at.UTC().Before(notBefore.UTC())
}

type Channel string

const (
	ChannelEmail Channel = "email"
	ChannelWeb   Channel = "web"
)

func normalizeChannels(values []Channel) ([]Channel, error) {
	values = append([]Channel(nil), values...)
	if len(values) == 0 || len(values) > 2 {
		return nil, ErrInvalid
	}
	for index, value := range values {
		if (value != ChannelEmail && value != ChannelWeb) || slices.Contains(values[:index], value) {
			return nil, ErrInvalid
		}
	}
	slices.Sort(values)
	return values, nil
}

func normalizeAssetRevisionIDs(values []ids.MarketingAssetRevisionID) ([]ids.MarketingAssetRevisionID, error) {
	values = append([]ids.MarketingAssetRevisionID(nil), values...)
	if len(values) == 0 || len(values) > MaximumReleaseAssets {
		return nil, ErrInvalid
	}
	for index, value := range values {
		if ids.Validate(string(value)) != nil || slices.Contains(values[:index], value) {
			return nil, ErrInvalid
		}
	}
	slices.Sort(values)
	return values, nil
}

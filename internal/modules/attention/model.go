// Package attention owns customer-visible requests that require a person to
// supply information, review Work, or authorize a consequential proposal.
// These concepts deliberately remain separate from Work and agent execution.
package attention

import (
	"errors"
	"strings"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrInvalid             = errors.New("attention aggregate is invalid")
	ErrState               = errors.New("attention state change is not allowed")
	ErrConflict            = errors.New("attention aggregate version conflict")
	ErrRole                = errors.New("role cannot perform the attention command")
	ErrReasonRequired      = errors.New("attention command reason is required")
	ErrRequirementMismatch = errors.New("information does not satisfy the exact requirement")
	ErrReviewer            = errors.New("only the assigned reviewer can decide this review")
	ErrSelfApproval        = errors.New("independent approval is required")
	ErrExpired             = errors.New("consequential approval has expired")
)

type ActorKind string

const (
	ActorUser     ActorKind = "user"
	ActorWorkload ActorKind = "workload"
)

type Actor struct {
	Kind ActorKind
	ID   string
}

func (actor Actor) Valid() bool {
	value := strings.TrimSpace(actor.ID)
	if value == "" || len(value) > 200 || value != actor.ID {
		return false
	}
	if actor.Kind == ActorUser {
		return ids.Validate(value) == nil
	}
	return actor.Kind == ActorWorkload
}

func validID(value string) bool { return ids.Validate(value) == nil }

func validReason(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) >= 3 && len(value) <= 1000
}

func canParticipate(role accounts.MembershipRole) bool {
	return role == accounts.RoleOwner || role == accounts.RoleAdministrator || role == accounts.RoleMember
}

func canManage(role accounts.MembershipRole) bool {
	return role == accounts.RoleOwner || role == accounts.RoleAdministrator
}

func sameActor(left, right Actor) bool {
	return left.Kind == right.Kind && left.ID == right.ID
}

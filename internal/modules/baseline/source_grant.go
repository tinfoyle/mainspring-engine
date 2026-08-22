package baseline

import (
	"sort"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	MaximumSourceFolders      = 50
	MaximumSourceFolderLength = 512
)

type SourceKind string

const (
	SourceEmail       SourceKind = "email"
	SourceGoogleDrive SourceKind = "google_drive"
)

func (kind SourceKind) valid() bool { return kind == SourceEmail || kind == SourceGoogleDrive }

type SourceGrantState string

const (
	SourceGrantActive  SourceGrantState = "active"
	SourceGrantRevoked SourceGrantState = "revoked"
)

type SourceScope struct {
	Folders []string
	Since   *time.Time
	Until   *time.Time
}

func (scope SourceScope) normalized() SourceScope {
	scope.Folders = append([]string(nil), scope.Folders...)
	for index := range scope.Folders {
		scope.Folders[index] = strings.TrimSpace(scope.Folders[index])
	}
	sort.Strings(scope.Folders)
	if scope.Since != nil {
		value := scope.Since.UTC()
		scope.Since = &value
	}
	if scope.Until != nil {
		value := scope.Until.UTC()
		scope.Until = &value
	}
	return scope
}

func (scope SourceScope) valid(kind SourceKind) bool {
	if len(scope.Folders) == 0 || len(scope.Folders) > MaximumSourceFolders || (kind == SourceEmail && len(scope.Folders) > 20) {
		return false
	}
	for index, folder := range scope.Folders {
		if folder == "" || len(folder) > MaximumSourceFolderLength || (index > 0 && folder == scope.Folders[index-1]) {
			return false
		}
	}
	if kind == SourceGoogleDrive && (scope.Since != nil || scope.Until != nil) {
		return false
	}
	return scope.Since == nil || scope.Until == nil || !scope.Since.After(*scope.Until)
}

type SourceGrantDraft struct {
	ID           ids.BaselineSourceGrantID
	AccountID    ids.AccountID
	AssessmentID ids.BaselineAssessmentID
	ConnectionID string
	Kind         SourceKind
	Scope        SourceScope
	GrantedBy    Actor
}

type SourceGrant struct {
	SourceGrantDraft
	State     SourceGrantState
	RevokedBy *Actor
	Reason    string
	Version   uint64
	CreatedAt time.Time
	UpdatedAt time.Time
	RevokedAt *time.Time
}

func NewSourceGrant(draft SourceGrantDraft, now time.Time) (SourceGrant, error) {
	return RestoreSourceGrant(SourceGrant{SourceGrantDraft: draft, State: SourceGrantActive, Version: 1, CreatedAt: now, UpdatedAt: now})
}

func RestoreSourceGrant(value SourceGrant) (SourceGrant, error) {
	value.ConnectionID = strings.TrimSpace(value.ConnectionID)
	value.Scope = value.Scope.normalized()
	value.Reason = strings.TrimSpace(value.Reason)
	value.CreatedAt, value.UpdatedAt = value.CreatedAt.UTC(), value.UpdatedAt.UTC()
	if value.RevokedBy != nil {
		actor := *value.RevokedBy
		value.RevokedBy = &actor
	}
	if value.RevokedAt != nil {
		at := value.RevokedAt.UTC()
		value.RevokedAt = &at
	}
	if ids.Validate(string(value.ID)) != nil || ids.Validate(string(value.AccountID)) != nil || ids.Validate(string(value.AssessmentID)) != nil || ids.Validate(value.ConnectionID) != nil || !value.Kind.valid() || !value.Scope.valid(value.Kind) || !value.GrantedBy.valid() || value.Version == 0 || value.CreatedAt.IsZero() || value.UpdatedAt.Before(value.CreatedAt) {
		return SourceGrant{}, ErrInvalid
	}
	switch value.State {
	case SourceGrantActive:
		if value.Version != 1 || value.RevokedBy != nil || value.RevokedAt != nil || value.Reason != "" || !value.UpdatedAt.Equal(value.CreatedAt) {
			return SourceGrant{}, ErrInvalid
		}
	case SourceGrantRevoked:
		if value.Version != 2 || value.RevokedBy == nil || !value.RevokedBy.valid() || value.RevokedAt == nil || value.RevokedAt.Before(value.CreatedAt) || !value.UpdatedAt.Equal(*value.RevokedAt) || !validReason(value.Reason) {
			return SourceGrant{}, ErrInvalid
		}
	default:
		return SourceGrant{}, ErrInvalid
	}
	return value, nil
}

func (grant SourceGrant) Revoke(actor Actor, role accounts.MembershipRole, reason string, expected uint64, at time.Time) (SourceGrant, error) {
	if expected != grant.Version {
		return SourceGrant{}, ErrConflict
	}
	if !canManage(role) {
		return SourceGrant{}, ErrRole
	}
	if grant.State != SourceGrantActive || !actor.valid() || at.IsZero() || at.Before(grant.UpdatedAt) {
		return SourceGrant{}, ErrState
	}
	result := grant
	result.State, result.RevokedBy, result.Reason, result.Version = SourceGrantRevoked, &actor, reason, grant.Version+1
	result.UpdatedAt, result.RevokedAt = at, &at
	return RestoreSourceGrant(result)
}

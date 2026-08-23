package accountexport

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrNotFound          = errors.New("Account export request not found")
	ErrStateConflict     = errors.New("Account export request state conflict")
	ErrLeaseConflict     = errors.New("Account export request lease conflict")
	ErrArtifactConflict  = errors.New("Account export artifact conflicts with existing content")
	ErrArtifactIntegrity = errors.New("Account export artifact integrity check failed")
)

type State string

const (
	StateQueued    State = "queued"
	StateBuilding  State = "building"
	StateAvailable State = "available"
	StateFailed    State = "failed"
	StateDeleting  State = "deleting"
	StateDeleted   State = "deleted"
	StateCanceled  State = "canceled"
)

type Artifact struct {
	Reference string            `json:"-"`
	SHA256    [sha256.Size]byte `json:"-"`
	Bytes     int64             `json:"bytes"`
}

type Status struct {
	ID                  string        `json:"id"`
	AccountID           ids.AccountID `json:"account_id"`
	RequestedBy         ids.UserID    `json:"requested_by"`
	State               State         `json:"state"`
	CellID              ids.CellID    `json:"cell_id"`
	PlacementGeneration uint64        `json:"placement_generation"`
	AccountVersion      uint64        `json:"account_version"`
	AttemptCount        uint64        `json:"attempt_count"`
	ErrorCode           string        `json:"error_code,omitempty"`
	ArtifactBytes       int64         `json:"artifact_bytes,omitempty"`
	Version             uint64        `json:"version"`
	RequestedAt         time.Time     `json:"requested_at"`
	ExpiresAt           time.Time     `json:"expires_at"`
	AvailableAt         *time.Time    `json:"available_at,omitempty"`
	DeletedAt           *time.Time    `json:"deleted_at,omitempty"`
}

type CreateMutation struct {
	ID                  string
	EventID             string
	AccountID           ids.AccountID
	RequestedBy         ids.UserID
	CellID              ids.CellID
	PlacementGeneration uint64
	RequestedAt         time.Time
	ExpiresAt           time.Time
}

type CancelMutation struct {
	ID, EventID     string
	AccountID       ids.AccountID
	RequestedBy     ids.UserID
	ExpectedVersion uint64
	At              time.Time
}

type Work struct {
	ID                  string
	AccountID           ids.AccountID
	RequestedBy         ids.UserID
	CellID              ids.CellID
	PlacementGeneration uint64
	AccountVersion      uint64
	AttemptCount        uint64
	Version             uint64
	LeaseID             string
	RequestedAt         time.Time
	ExpiresAt           time.Time
}

type CompleteMutation struct {
	Work        Work
	EventID     string
	Snapshot    Snapshot
	Artifact    Artifact
	AvailableAt time.Time
}

type FailureMutation struct {
	Work          Work
	EventID       string
	ErrorCode     string
	NextAttemptAt time.Time
	Permanent     bool
	At            time.Time
}

type DeletionWork struct {
	ID, LeaseID string
	AccountID   ids.AccountID
	Version     uint64
	Artifact    Artifact
}

type RequestStore interface {
	Create(context.Context, CreateMutation) (Status, error)
	Get(context.Context, ids.AccountID, string) (Status, error)
	List(context.Context, ids.AccountID, uint64) ([]Status, error)
	Cancel(context.Context, CancelMutation) (Status, error)
}

type BuildStore interface {
	ClaimBuild(context.Context, ids.CellID, time.Time, time.Duration, string, string) (Work, bool, error)
	Complete(context.Context, CompleteMutation) (Status, error)
	RecordFailure(context.Context, FailureMutation) (Status, error)
}

type ExpiryStore interface {
	ClaimDeletion(context.Context, time.Time, time.Duration, string, string) (DeletionWork, bool, error)
	CompleteDeletion(context.Context, DeletionWork, time.Time, string) (Status, error)
}

type Authorizer interface {
	Authorize(context.Context, access.Actor, ids.AccountID, access.Requirement) (access.AccountContext, error)
}

type Clock interface{ Now() time.Time }

type Service struct {
	store      RequestStore
	authorizer Authorizer
	ids        ids.Generator
	clock      Clock
	retention  time.Duration
}

func NewService(store RequestStore, authorizer Authorizer, generator ids.Generator, clock Clock, retention time.Duration) (*Service, error) {
	if store == nil || authorizer == nil || generator == nil || clock == nil || retention < time.Hour || retention > 30*24*time.Hour {
		return nil, ErrInvalid
	}
	return &Service{store: store, authorizer: authorizer, ids: generator, clock: clock, retention: retention}, nil
}

type CreateCommand struct {
	AccountID ids.AccountID
	Actor     ids.UserID
	Session   sessions.Session
}

func (service *Service) Create(ctx context.Context, command CreateCommand) (Status, error) {
	account, err := service.authorizer.Authorize(ctx, access.Actor{UserID: command.Actor}, command.AccountID, access.Requirement{Roles: []accounts.MembershipRole{accounts.RoleOwner}})
	if err != nil {
		return Status{}, err
	}
	now := service.clock.Now().UTC()
	if err := strongauth.Require(command.Session, command.Actor, now); err != nil {
		return Status{}, err
	}
	return service.store.Create(ctx, CreateMutation{ID: service.ids.New(), EventID: service.ids.New(), AccountID: command.AccountID, RequestedBy: command.Actor,
		CellID: account.CellID, PlacementGeneration: account.PlacementGeneration, RequestedAt: now, ExpiresAt: now.Add(service.retention)})
}

func (service *Service) Get(ctx context.Context, accountID ids.AccountID, actor ids.UserID, id string) (Status, error) {
	if _, err := service.authorizer.Authorize(ctx, access.Actor{UserID: actor}, accountID, access.Requirement{Roles: []accounts.MembershipRole{accounts.RoleOwner}}); err != nil {
		return Status{}, err
	}
	if ids.Validate(id) != nil {
		return Status{}, ErrInvalid
	}
	return service.store.Get(ctx, accountID, id)
}

func (service *Service) List(ctx context.Context, accountID ids.AccountID, actor ids.UserID, limit uint64) ([]Status, error) {
	if _, err := service.authorizer.Authorize(ctx, access.Actor{UserID: actor}, accountID, access.Requirement{Roles: []accounts.MembershipRole{accounts.RoleOwner}}); err != nil {
		return nil, err
	}
	if limit == 0 || limit > 100 {
		return nil, ErrInvalid
	}
	return service.store.List(ctx, accountID, limit)
}

type CancelCommand struct {
	AccountID       ids.AccountID
	Actor           ids.UserID
	ID              string
	ExpectedVersion uint64
	Session         sessions.Session
}

func (service *Service) Cancel(ctx context.Context, command CancelCommand) (Status, error) {
	if _, err := service.authorizer.Authorize(ctx, access.Actor{UserID: command.Actor}, command.AccountID, access.Requirement{Roles: []accounts.MembershipRole{accounts.RoleOwner}}); err != nil {
		return Status{}, err
	}
	now := service.clock.Now().UTC()
	if err := strongauth.Require(command.Session, command.Actor, now); err != nil {
		return Status{}, err
	}
	if ids.Validate(command.ID) != nil || command.ExpectedVersion == 0 {
		return Status{}, ErrInvalid
	}
	return service.store.Cancel(ctx, CancelMutation{ID: command.ID, EventID: service.ids.New(), AccountID: command.AccountID,
		RequestedBy: command.Actor, ExpectedVersion: command.ExpectedVersion, At: now})
}

func validArtifact(value Artifact) bool {
	return value.Reference != "" && len(value.Reference) <= 1000 && strings.TrimSpace(value.Reference) == value.Reference &&
		!strings.ContainsAny(value.Reference, "\x00\r\n") && value.SHA256 != [sha256.Size]byte{} && value.Bytes > 0 && value.Bytes <= MaximumArtifactBytes
}

func validErrorCode(value string) bool {
	return validCode.MatchString(value) && len(value) <= 64
}

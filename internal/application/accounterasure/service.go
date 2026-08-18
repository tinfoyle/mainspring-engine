// Package accounterasure owns the reviewed, non-destructive preparation
// boundary for post-retention Account erasure.
package accounterasure

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type State string
type ExportDisposition string

const (
	StatePrepared      State = "prepared"
	StateApproved      State = "approved"
	StateCanceled      State = "canceled"
	StateCellErasing   State = "cell_erasing"
	StateCellErased    State = "cell_erased"
	StateGlobalErasing State = "global_erasing"

	ExportArtifact      ExportDisposition = "artifact"
	ExportNotApplicable ExportDisposition = "not_applicable"
)

var (
	ErrInvalidChange  = errors.New("Account erasure operator change is invalid")
	ErrNotFound       = errors.New("Account erasure target was not found")
	ErrNotEligible    = errors.New("Account is not eligible for erasure preparation")
	ErrStateConflict  = errors.New("Account erasure request state changed")
	ErrReviewRequired = errors.New("Account erasure approver must differ from requester")
	ErrCellMismatch   = errors.New("Account erasure cell does not match the configured cell database")
	validEnvironment  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,99}$`)
)

type Target struct {
	AccountID           ids.AccountID
	CellID              ids.CellID
	PlacementGeneration uint64
	AccountVersion      uint64
}

type CellAttestation struct {
	Target
	NamespaceState        string
	UnfinishedReleaseJobs int64
	ObservedAt            time.Time
}

type Request struct {
	ID, ClosureRequestID                                        string
	AccountID                                                   ids.AccountID
	State                                                       State
	CellID                                                      ids.CellID
	PlacementGeneration, AccountVersion, PolicyVersion, Version uint64
	ExportDisposition                                           ExportDisposition
	ExportReference                                             string
	ExportSHA256                                                []byte
	ExportExpiresAt                                             *time.Time
	ExportReason                                                string
	BackupExpiresAt                                             time.Time
	CellNamespaceState, Environment                             string
	CellAttestedAt, RequestedAt                                 time.Time
	RequestedBy, RequestReason                                  string
	ApprovedBy, ApproveReason                                   string
	ApprovedAt                                                  *time.Time
	CanceledBy, CancelReason                                    string
	CanceledAt                                                  *time.Time
	AccountFingerprint, OperatorEvidenceSHA256                  []byte
	CellRequestVersion                                          uint64
	ExecutionLeaseID                                            string
	LeaseExpiresAt, CellErasedAt                                *time.Time
	CellRowCounts                                               map[string]int64
	CellTombstoneSHA256                                         []byte
}

type ExportEvidence struct {
	Disposition ExportDisposition
	Reference   string
	SHA256      []byte
	ExpiresAt   *time.Time
	Reason      string
}

type PrepareCommand struct {
	AccountID       ids.AccountID
	PolicyVersion   uint64
	Export          ExportEvidence
	BackupExpiresAt time.Time
	Actor, Reason   string
	Environment     string
}

type Change struct {
	EventID                    string
	Actor, Reason, Environment string
}

type Store interface {
	Target(context.Context, ids.AccountID) (Target, error)
	Prepare(context.Context, string, PrepareCommand, CellAttestation, Change) (Request, error)
	Inspect(context.Context, string, Change) (Request, error)
	Approve(context.Context, string, uint64, CellAttestation, Change) (Request, error)
	Cancel(context.Context, string, uint64, Change) (Request, error)
}

type CellStore interface {
	Attest(context.Context, Target) (CellAttestation, error)
}

type Clock interface{ Now() time.Time }

type Service struct {
	store Store
	cell  CellStore
	ids   ids.Generator
	clock Clock
}

func NewService(store Store, cell CellStore, generator ids.Generator, clock Clock) (*Service, error) {
	if store == nil || cell == nil || generator == nil || clock == nil {
		return nil, errors.New("Account erasure administration dependencies are required")
	}
	return &Service{store: store, cell: cell, ids: generator, clock: clock}, nil
}

func (s *Service) Prepare(ctx context.Context, command PrepareCommand) (Request, error) {
	if ids.Validate(string(command.AccountID)) != nil || command.PolicyVersion == 0 || !validExport(command.Export, s.clock.Now().UTC()) || !command.BackupExpiresAt.After(s.clock.Now().UTC()) {
		return Request{}, ErrInvalidChange
	}
	change, err := s.change(command.Actor, command.Reason, command.Environment)
	if err != nil {
		return Request{}, err
	}
	target, err := s.store.Target(ctx, command.AccountID)
	if err != nil {
		return Request{}, err
	}
	attestation, err := s.cell.Attest(ctx, target)
	if err != nil {
		return Request{}, err
	}
	if err := validAttestation(target, attestation); err != nil {
		return Request{}, err
	}
	requestID := s.ids.New()
	if ids.Validate(requestID) != nil {
		return Request{}, ErrInvalidChange
	}
	command.Export.Reference = strings.TrimSpace(command.Export.Reference)
	command.Export.Reason = strings.TrimSpace(command.Export.Reason)
	return s.store.Prepare(ctx, requestID, command, attestation, change)
}

func (s *Service) Inspect(ctx context.Context, requestID, actor, reason, environment string) (Request, error) {
	if ids.Validate(requestID) != nil {
		return Request{}, ErrInvalidChange
	}
	change, err := s.change(actor, reason, environment)
	if err != nil {
		return Request{}, err
	}
	return s.store.Inspect(ctx, requestID, change)
}

func (s *Service) Approve(ctx context.Context, requestID string, expectedVersion uint64, actor, reason, environment string) (Request, error) {
	if ids.Validate(requestID) != nil || expectedVersion == 0 {
		return Request{}, ErrInvalidChange
	}
	inspectionChange, err := s.change(actor, reason, environment)
	if err != nil {
		return Request{}, err
	}
	request, err := s.store.Inspect(ctx, requestID, inspectionChange)
	if err != nil {
		return Request{}, err
	}
	if request.RequestedBy == inspectionChange.Actor {
		return Request{}, ErrReviewRequired
	}
	if request.State != StatePrepared || request.Version != expectedVersion {
		return Request{}, ErrStateConflict
	}
	target := Target{AccountID: request.AccountID, CellID: request.CellID, PlacementGeneration: request.PlacementGeneration, AccountVersion: request.AccountVersion}
	attestation, err := s.cell.Attest(ctx, target)
	if err != nil {
		return Request{}, err
	}
	if err := validAttestation(target, attestation); err != nil {
		return Request{}, err
	}
	approvalChange, err := s.change(actor, reason, environment)
	if err != nil {
		return Request{}, err
	}
	return s.store.Approve(ctx, requestID, expectedVersion, attestation, approvalChange)
}

func (s *Service) Cancel(ctx context.Context, requestID string, expectedVersion uint64, actor, reason, environment string) (Request, error) {
	if ids.Validate(requestID) != nil || expectedVersion == 0 {
		return Request{}, ErrInvalidChange
	}
	change, err := s.change(actor, reason, environment)
	if err != nil {
		return Request{}, err
	}
	return s.store.Cancel(ctx, requestID, expectedVersion, change)
}

func (s *Service) change(actor, reason, environment string) (Change, error) {
	actor, reason, environment = strings.TrimSpace(actor), strings.TrimSpace(reason), strings.TrimSpace(environment)
	if actor == "" || len(actor) > 200 || strings.ContainsAny(actor, "\r\n") || len(reason) < 8 || len(reason) > 500 || strings.ContainsAny(reason, "\r\n") || !validEnvironment.MatchString(environment) {
		return Change{}, ErrInvalidChange
	}
	eventID := s.ids.New()
	if ids.Validate(eventID) != nil {
		return Change{}, ErrInvalidChange
	}
	return Change{EventID: eventID, Actor: actor, Reason: reason, Environment: environment}, nil
}

func validExport(export ExportEvidence, now time.Time) bool {
	reference, reason := strings.TrimSpace(export.Reference), strings.TrimSpace(export.Reason)
	switch export.Disposition {
	case ExportArtifact:
		return len(reference) >= 1 && len(reference) <= 2048 && !strings.ContainsAny(reference, "\r\n") && len(export.SHA256) == 32 && export.ExpiresAt != nil && export.ExpiresAt.After(now) && reason == ""
	case ExportNotApplicable:
		return reference == "" && len(export.SHA256) == 0 && export.ExpiresAt == nil && len(reason) >= 8 && len(reason) <= 500 && !strings.ContainsAny(reason, "\r\n")
	default:
		return false
	}
}

func validAttestation(target Target, attestation CellAttestation) error {
	if attestation.AccountID != target.AccountID || attestation.CellID != target.CellID || attestation.PlacementGeneration != target.PlacementGeneration || attestation.AccountVersion != target.AccountVersion {
		return ErrCellMismatch
	}
	if (attestation.NamespaceState != "frozen" && attestation.NamespaceState != "disabled") || attestation.UnfinishedReleaseJobs != 0 || attestation.ObservedAt.IsZero() {
		return ErrNotEligible
	}
	return nil
}

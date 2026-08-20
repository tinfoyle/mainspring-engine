// Package accountmovement owns the resumable cross-cell Account move workflow.
package accountmovement

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type State string

const (
	StatePrepared   State = "prepared"
	StateDrained    State = "drained"
	StateCopied     State = "copied"
	StateReady      State = "ready"
	StateSwitched   State = "switched"
	StatePaused     State = "paused"
	StateRetiring   State = "retiring"
	StateRolledBack State = "rolled_back"
	StateCompleted  State = "completed"
)

var (
	ErrInvalid       = errors.New("Account move command is invalid")
	ErrNotFound      = errors.New("Account move was not found")
	ErrStateConflict = errors.New("Account move state changed")
	ErrReconcile     = errors.New("Account move reconciliation failed")
	validEnvironment = regexp.MustCompile(`^[a-z][a-z0-9-]{0,99}$`)
)

type Move struct {
	ID                    string
	AccountID             ids.AccountID
	State                 State
	ResumeState           State
	SourceCellID          ids.CellID
	DestinationCellID     ids.CellID
	SourceGeneration      uint64
	DestinationGeneration uint64
	RollbackGeneration    uint64
	AccountVersion        uint64
	Version               uint64
	LeaseID               string
	LeaseExpiresAt        *time.Time
	SourceHighWatermark   string
	SourceManifest        map[string]int64
	DestinationManifest   map[string]int64
	SourceDigest          []byte
	DestinationDigest     []byte
	RollbackWindow        time.Duration
	RollbackExpiresAt     *time.Time
	PreparedAt            time.Time
	Environment           string
}

type Evidence struct {
	HighWatermark string
	Manifest      map[string]int64
	Digest        []byte
}

type Change struct {
	EventID, Actor, Reason, Environment string
}

type PrepareCommand struct {
	AccountID         ids.AccountID
	DestinationCellID ids.CellID
	RollbackWindow    time.Duration
	Actor, Reason     string
	Environment       string
}

type Store interface {
	Prepare(context.Context, string, PrepareCommand, Change) (Move, error)
	Inspect(context.Context, string, Change) (Move, error)
	Claim(context.Context, string, uint64, string, time.Duration, Change) (Move, error)
	Advance(context.Context, Move, string, Evidence, Change) (Move, error)
	Switch(context.Context, Move, Change) (Move, error)
	Pause(context.Context, string, uint64, bool, Change) (Move, error)
	Rollback(context.Context, Move, Change) (Move, error)
	BeginRetirement(context.Context, Move, Change) (Move, error)
	Complete(context.Context, Move, Change) (Move, error)
}

type Cells interface {
	FreezeAndStage(context.Context, Move) error
	Copy(context.Context, Move) (Evidence, error)
	Reconcile(context.Context, Move) (Evidence, error)
	ActivateDestination(context.Context, Move) error
	ActivateRollback(context.Context, Move) error
	RetireSource(context.Context, Move) error
}

type Clock interface{ Now() time.Time }

type Service struct {
	store Store
	cells Cells
	ids   ids.Generator
	clock Clock
	lease time.Duration
}

func NewService(store Store, cells Cells, generator ids.Generator, clock Clock, lease time.Duration) (*Service, error) {
	if store == nil || cells == nil || generator == nil || clock == nil || lease < 30*time.Second || lease > time.Hour {
		return nil, errors.New("Account move dependencies and a 30s-1h lease are required")
	}
	return &Service{store: store, cells: cells, ids: generator, clock: clock, lease: lease}, nil
}

func (s *Service) Prepare(ctx context.Context, command PrepareCommand) (Move, error) {
	if ids.Validate(string(command.AccountID)) != nil || !validCell(command.DestinationCellID) ||
		command.RollbackWindow < 5*time.Minute || command.RollbackWindow > 7*24*time.Hour || command.RollbackWindow%time.Second != 0 {
		return Move{}, ErrInvalid
	}
	change, err := s.change(command.Actor, command.Reason, command.Environment)
	if err != nil {
		return Move{}, err
	}
	return s.store.Prepare(ctx, s.ids.New(), command, change)
}

func (s *Service) Inspect(ctx context.Context, moveID, actor, reason, environment string) (Move, error) {
	if ids.Validate(moveID) != nil {
		return Move{}, ErrInvalid
	}
	change, err := s.change(actor, reason, environment)
	if err != nil {
		return Move{}, err
	}
	return s.store.Inspect(ctx, moveID, change)
}

// Advance performs exactly one restartable phase. Repeated calls converge on
// a switched move; source retirement remains an explicit post-window action.
func (s *Service) Advance(ctx context.Context, moveID, actor, reason, environment string) (Move, error) {
	move, change, err := s.inspectForMutation(ctx, moveID, actor, reason, environment)
	if err != nil {
		return Move{}, err
	}
	if move.State == StateSwitched {
		if err := s.cells.ActivateDestination(ctx, move); err != nil {
			return Move{}, err
		}
		return move, nil
	}
	if move.State == StateRolledBack {
		if err := s.cells.ActivateRollback(ctx, move); err != nil {
			return Move{}, err
		}
		return move, nil
	}
	change, err = s.change(actor, reason, environment)
	if err != nil {
		return Move{}, err
	}
	leaseID := s.ids.New()
	claimed, err := s.store.Claim(ctx, move.ID, move.Version, leaseID, s.lease, change)
	if err != nil {
		return Move{}, err
	}
	change, err = s.change(actor, reason, environment)
	if err != nil {
		return Move{}, err
	}
	switch claimed.State {
	case StatePrepared:
		if err := s.cells.FreezeAndStage(ctx, claimed); err != nil {
			return Move{}, err
		}
		return s.store.Advance(ctx, claimed, "drain", Evidence{}, change)
	case StateDrained:
		evidence, err := s.cells.Copy(ctx, claimed)
		if err != nil {
			return Move{}, err
		}
		return s.store.Advance(ctx, claimed, "copy", evidence, change)
	case StateCopied:
		evidence, err := s.cells.Reconcile(ctx, claimed)
		if err != nil {
			return Move{}, err
		}
		return s.store.Advance(ctx, claimed, "reconcile", evidence, change)
	case StateReady:
		result, err := s.store.Switch(ctx, claimed, change)
		if err != nil {
			return Move{}, err
		}
		if err := s.cells.ActivateDestination(ctx, result); err != nil {
			return Move{}, err
		}
		return result, nil
	default:
		return Move{}, ErrStateConflict
	}
}

func (s *Service) Pause(ctx context.Context, moveID string, expectedVersion uint64, pause bool, actor, reason, environment string) (Move, error) {
	if ids.Validate(moveID) != nil || expectedVersion == 0 {
		return Move{}, ErrInvalid
	}
	change, err := s.change(actor, reason, environment)
	if err != nil {
		return Move{}, err
	}
	return s.store.Pause(ctx, moveID, expectedVersion, pause, change)
}

func (s *Service) Rollback(ctx context.Context, moveID, actor, reason, environment string) (Move, error) {
	move, change, err := s.inspectForMutation(ctx, moveID, actor, reason, environment)
	if err != nil {
		return Move{}, err
	}
	if move.State == StateRolledBack {
		if err := s.cells.ActivateRollback(ctx, move); err != nil {
			return Move{}, err
		}
		return move, nil
	}
	if move.State != StateSwitched || move.RollbackExpiresAt == nil || !s.clock.Now().UTC().Before(*move.RollbackExpiresAt) {
		return Move{}, ErrStateConflict
	}
	change, err = s.change(actor, reason, environment)
	if err != nil {
		return Move{}, err
	}
	claimed, err := s.store.Claim(ctx, move.ID, move.Version, s.ids.New(), s.lease, change)
	if err != nil {
		return Move{}, err
	}
	change, err = s.change(actor, reason, environment)
	if err != nil {
		return Move{}, err
	}
	result, err := s.store.Rollback(ctx, claimed, change)
	if err != nil {
		return Move{}, err
	}
	if err := s.cells.ActivateRollback(ctx, result); err != nil {
		return Move{}, err
	}
	return result, nil
}

func (s *Service) Retire(ctx context.Context, moveID, actor, reason, environment string) (Move, error) {
	move, change, err := s.inspectForMutation(ctx, moveID, actor, reason, environment)
	if err != nil {
		return Move{}, err
	}
	if move.State == StateCompleted {
		return move, nil
	}
	startedNow := false
	if move.State == StateSwitched {
		if move.RollbackExpiresAt == nil || s.clock.Now().UTC().Before(*move.RollbackExpiresAt) {
			return Move{}, ErrStateConflict
		}
		change, err = s.change(actor, reason, environment)
		if err != nil {
			return Move{}, err
		}
		move, err = s.store.Claim(ctx, move.ID, move.Version, s.ids.New(), s.lease, change)
		if err != nil {
			return Move{}, err
		}
		change, err = s.change(actor, reason, environment)
		if err != nil {
			return Move{}, err
		}
		move, err = s.store.BeginRetirement(ctx, move, change)
		if err != nil {
			return Move{}, err
		}
		startedNow = true
	}
	if move.State != StateRetiring {
		return Move{}, ErrStateConflict
	}
	if !startedNow {
		if move.LeaseID != "" && move.LeaseExpiresAt != nil && s.clock.Now().UTC().Before(*move.LeaseExpiresAt) {
			return Move{}, ErrStateConflict
		}
		change, err = s.change(actor, reason, environment)
		if err != nil {
			return Move{}, err
		}
		move, err = s.store.Claim(ctx, move.ID, move.Version, s.ids.New(), s.lease, change)
		if err != nil {
			return Move{}, err
		}
	}
	if err := s.cells.RetireSource(ctx, move); err != nil {
		return Move{}, err
	}
	change, err = s.change(actor, reason, environment)
	if err != nil {
		return Move{}, err
	}
	return s.store.Complete(ctx, move, change)
}

func (s *Service) inspectForMutation(ctx context.Context, moveID, actor, reason, environment string) (Move, Change, error) {
	change, err := s.change(actor, reason, environment)
	if err != nil || ids.Validate(moveID) != nil {
		return Move{}, Change{}, ErrInvalid
	}
	move, err := s.store.Inspect(ctx, moveID, change)
	return move, change, err
}

func (s *Service) change(actor, reason, environment string) (Change, error) {
	actor, reason = strings.TrimSpace(actor), strings.TrimSpace(reason)
	if len(actor) < 3 || len(actor) > 200 || len(reason) < 8 || len(reason) > 500 ||
		strings.ContainsAny(actor+reason, "\r\n\x00") || !validEnvironment.MatchString(environment) {
		return Change{}, ErrInvalid
	}
	eventID := s.ids.New()
	if ids.Validate(eventID) != nil {
		return Change{}, ErrInvalid
	}
	return Change{EventID: eventID, Actor: actor, Reason: reason, Environment: environment}, nil
}

func validCell(value ids.CellID) bool {
	if len(value) < 1 || len(value) > 63 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range value[1:] {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return false
		}
	}
	return true
}

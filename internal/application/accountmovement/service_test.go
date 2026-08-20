package accountmovement

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestAdvanceRetriesDestinationActivationAfterDurableSwitch(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	store := &movementStore{move: movementFixture(StateReady, now)}
	cells := &movementCells{destinationErr: errors.New("destination unavailable")}
	service := movementService(t, store, cells, now)

	if _, err := service.Advance(context.Background(), store.move.ID, "operator@example.com", "switch prepared Account move", "test"); err == nil {
		t.Fatal("expected destination activation failure")
	}
	if store.switches != 1 || store.move.State != StateSwitched {
		t.Fatalf("switches=%d state=%s", store.switches, store.move.State)
	}

	cells.destinationErr = nil
	restarted := movementService(t, store, cells, now)
	move, err := restarted.Advance(context.Background(), store.move.ID, "operator@example.com", "resume destination activation", "test")
	if err != nil || move.State != StateSwitched {
		t.Fatalf("move=%+v err=%v", move, err)
	}
	if store.switches != 1 || cells.destinationActivations != 2 {
		t.Fatalf("switches=%d destination activations=%d", store.switches, cells.destinationActivations)
	}
}

func TestRollbackRetriesCellActivationAfterDurablePlacementRollback(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	store := &movementStore{move: movementFixture(StateSwitched, now)}
	expires := now.Add(time.Hour)
	store.move.RollbackExpiresAt = &expires
	cells := &movementCells{rollbackErr: errors.New("source unavailable")}
	service := movementService(t, store, cells, now)

	if _, err := service.Rollback(context.Background(), store.move.ID, "operator@example.com", "rollback switched Account move", "test"); err == nil {
		t.Fatal("expected rollback cell activation failure")
	}
	if store.rollbacks != 1 || store.move.State != StateRolledBack {
		t.Fatalf("rollbacks=%d state=%s", store.rollbacks, store.move.State)
	}

	cells.rollbackErr = nil
	restarted := movementService(t, store, cells, now)
	move, err := restarted.Rollback(context.Background(), store.move.ID, "operator@example.com", "resume rollback cell activation", "test")
	if err != nil || move.State != StateRolledBack {
		t.Fatalf("move=%+v err=%v", move, err)
	}
	if store.rollbacks != 1 || cells.rollbackActivations != 2 {
		t.Fatalf("rollbacks=%d rollback activations=%d", store.rollbacks, cells.rollbackActivations)
	}
}

func movementFixture(state State, now time.Time) Move {
	return Move{
		ID: "10000000-0000-4000-8000-000000000001", AccountID: ids.AccountID("20000000-0000-4000-8000-000000000001"),
		State: state, SourceCellID: "cell-us-east-01", DestinationCellID: "cell-us-west-01",
		SourceGeneration: 1, DestinationGeneration: 2, AccountVersion: 1, Version: 4,
		RollbackWindow: 5 * time.Minute, PreparedAt: now, Environment: "test",
	}
}

func movementService(t *testing.T, store Store, cells Cells, now time.Time) *Service {
	t.Helper()
	service, err := NewService(store, cells, &movementIDs{}, movementClock{now: now}, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type movementIDs struct{ next int }

func (generator *movementIDs) New() string {
	generator.next++
	return fmt.Sprintf("30000000-0000-4000-8000-%012d", generator.next)
}

type movementClock struct{ now time.Time }

func (clock movementClock) Now() time.Time { return clock.now }

type movementStore struct {
	move                Move
	switches, rollbacks int
}

func (store *movementStore) Prepare(context.Context, string, PrepareCommand, Change) (Move, error) {
	return Move{}, errors.New("unexpected prepare")
}
func (store *movementStore) Inspect(context.Context, string, Change) (Move, error) {
	return store.move, nil
}
func (store *movementStore) Claim(_ context.Context, _ string, _ uint64, leaseID string, lease time.Duration, _ Change) (Move, error) {
	expires := time.Now().Add(lease)
	store.move.LeaseID, store.move.LeaseExpiresAt = leaseID, &expires
	return store.move, nil
}
func (store *movementStore) Advance(context.Context, Move, string, Evidence, Change) (Move, error) {
	return Move{}, errors.New("unexpected advance")
}
func (store *movementStore) Switch(context.Context, Move, Change) (Move, error) {
	store.switches++
	store.move.State, store.move.Version = StateSwitched, store.move.Version+1
	return store.move, nil
}
func (store *movementStore) Pause(context.Context, string, uint64, bool, Change) (Move, error) {
	return Move{}, errors.New("unexpected pause")
}
func (store *movementStore) Rollback(context.Context, Move, Change) (Move, error) {
	store.rollbacks++
	store.move.State, store.move.RollbackGeneration, store.move.Version = StateRolledBack, 3, store.move.Version+1
	return store.move, nil
}
func (store *movementStore) BeginRetirement(context.Context, Move, Change) (Move, error) {
	return Move{}, errors.New("unexpected retirement")
}
func (store *movementStore) Complete(context.Context, Move, Change) (Move, error) {
	return Move{}, errors.New("unexpected completion")
}

type movementCells struct {
	destinationErr, rollbackErr                 error
	destinationActivations, rollbackActivations int
}

func (*movementCells) FreezeAndStage(context.Context, Move) error {
	return errors.New("unexpected freeze")
}
func (*movementCells) Copy(context.Context, Move) (Evidence, error) {
	return Evidence{}, errors.New("unexpected copy")
}
func (*movementCells) Reconcile(context.Context, Move) (Evidence, error) {
	return Evidence{}, errors.New("unexpected reconciliation")
}
func (cells *movementCells) ActivateDestination(context.Context, Move) error {
	cells.destinationActivations++
	return cells.destinationErr
}
func (cells *movementCells) ActivateRollback(context.Context, Move) error {
	cells.rollbackActivations++
	return cells.rollbackErr
}
func (*movementCells) RetireSource(context.Context, Move) error {
	return errors.New("unexpected retirement")
}

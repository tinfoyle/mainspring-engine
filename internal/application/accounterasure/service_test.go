package accounterasure_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/accounterasure"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type sequenceIDs struct{ values []string }

func (g *sequenceIDs) New() string { value := g.values[0]; g.values = g.values[1:]; return value }

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type fakeStore struct {
	target   accounterasure.Target
	request  accounterasure.Request
	changes  []accounterasure.Change
	approved bool
	canceled bool
}

func (s *fakeStore) Target(context.Context, ids.AccountID) (accounterasure.Target, error) {
	return s.target, nil
}
func (s *fakeStore) Prepare(_ context.Context, id string, command accounterasure.PrepareCommand, _ accounterasure.CellAttestation, change accounterasure.Change) (accounterasure.Request, error) {
	s.changes = append(s.changes, change)
	s.request = accounterasure.Request{ID: id, AccountID: command.AccountID, State: accounterasure.StatePrepared, CellID: s.target.CellID, PlacementGeneration: s.target.PlacementGeneration, AccountVersion: s.target.AccountVersion, Version: 1, RequestedBy: change.Actor}
	return s.request, nil
}
func (s *fakeStore) Inspect(_ context.Context, _ string, change accounterasure.Change) (accounterasure.Request, error) {
	s.changes = append(s.changes, change)
	return s.request, nil
}
func (s *fakeStore) Approve(_ context.Context, _ string, _ uint64, _ accounterasure.CellAttestation, change accounterasure.Change) (accounterasure.Request, error) {
	s.changes = append(s.changes, change)
	s.approved = true
	s.request.State = accounterasure.StateApproved
	s.request.Version++
	return s.request, nil
}
func (s *fakeStore) Cancel(_ context.Context, _ string, _ uint64, change accounterasure.Change) (accounterasure.Request, error) {
	s.changes = append(s.changes, change)
	s.canceled = true
	s.request.State = accounterasure.StateCanceled
	s.request.Version++
	return s.request, nil
}

type fakeCell struct {
	attestation accounterasure.CellAttestation
	err         error
}

func (c fakeCell) Attest(context.Context, accounterasure.Target) (accounterasure.CellAttestation, error) {
	return c.attestation, c.err
}

func TestPrepareApproveRequiresIndependentOperatorAndFreshCellReadiness(t *testing.T) {
	now := time.Date(2026, 8, 18, 20, 0, 0, 0, time.UTC)
	target := accounterasure.Target{AccountID: "11111111-1111-4111-8111-111111111111", CellID: "cell-a", PlacementGeneration: 3, AccountVersion: 7}
	store := &fakeStore{target: target}
	cell := fakeCell{attestation: accounterasure.CellAttestation{Target: target, NamespaceState: "frozen", ObservedAt: now}}
	generator := &sequenceIDs{values: []string{"20000000-0000-4000-8000-000000000001", "20000000-0000-4000-8000-000000000002", "20000000-0000-4000-8000-000000000003", "20000000-0000-4000-8000-000000000004", "20000000-0000-4000-8000-000000000005"}}
	service, _ := accounterasure.NewService(store, cell, generator, fixedClock{now})
	expires := now.Add(24 * time.Hour)
	prepared, err := service.Prepare(context.Background(), accounterasure.PrepareCommand{AccountID: target.AccountID, PolicyVersion: 1, Export: accounterasure.ExportEvidence{Disposition: accounterasure.ExportArtifact, Reference: "object://restricted/export", SHA256: make([]byte, 32), ExpiresAt: &expires}, BackupExpiresAt: now.Add(30 * 24 * time.Hour), Actor: "requester@example.com", Reason: "prepare reviewed Account erasure", Environment: "test"})
	if err != nil || prepared.State != accounterasure.StatePrepared || prepared.Version != 1 {
		t.Fatalf("prepared=%+v err=%v", prepared, err)
	}
	if _, err := service.Approve(context.Background(), prepared.ID, prepared.Version, "requester@example.com", "attempt self approval", "test"); !errors.Is(err, accounterasure.ErrReviewRequired) {
		t.Fatalf("self approval=%v", err)
	}
	approved, err := service.Approve(context.Background(), prepared.ID, prepared.Version, "reviewer@example.com", "approve reviewed Account erasure", "test")
	if err != nil || !store.approved || approved.State != accounterasure.StateApproved || approved.Version != 2 {
		t.Fatalf("approved=%+v worked=%v err=%v", approved, store.approved, err)
	}
}

func TestPreparationRejectsIncompleteEvidenceAndCellMismatch(t *testing.T) {
	now := time.Date(2026, 8, 18, 20, 0, 0, 0, time.UTC)
	target := accounterasure.Target{AccountID: "11111111-1111-4111-8111-111111111111", CellID: "cell-a", PlacementGeneration: 3, AccountVersion: 7}
	store := &fakeStore{target: target}
	generator := &sequenceIDs{values: []string{"30000000-0000-4000-8000-000000000001", "30000000-0000-4000-8000-000000000002", "30000000-0000-4000-8000-000000000003"}}
	service, _ := accounterasure.NewService(store, fakeCell{attestation: accounterasure.CellAttestation{Target: accounterasure.Target{AccountID: target.AccountID, CellID: "cell-b", PlacementGeneration: 3, AccountVersion: 7}, NamespaceState: "frozen", ObservedAt: now}}, generator, fixedClock{now})
	if _, err := service.Prepare(context.Background(), accounterasure.PrepareCommand{AccountID: target.AccountID, PolicyVersion: 1, Export: accounterasure.ExportEvidence{Disposition: accounterasure.ExportArtifact}, BackupExpiresAt: now.Add(time.Hour), Actor: "operator@example.com", Reason: "prepare Account erasure", Environment: "test"}); !errors.Is(err, accounterasure.ErrInvalidChange) {
		t.Fatalf("incomplete export=%v", err)
	}
	if _, err := service.Prepare(context.Background(), accounterasure.PrepareCommand{AccountID: target.AccountID, PolicyVersion: 1, Export: accounterasure.ExportEvidence{Disposition: accounterasure.ExportNotApplicable, Reason: "approved policy exception"}, BackupExpiresAt: now.Add(time.Hour), Actor: "operator@example.com", Reason: "prepare Account erasure", Environment: "test"}); !errors.Is(err, accounterasure.ErrCellMismatch) {
		t.Fatalf("cell mismatch=%v", err)
	}
}

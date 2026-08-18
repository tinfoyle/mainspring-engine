package accounterasure_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/accounterasure"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type fakeCellExecutor struct {
	command accounterasure.CellEraseCommand
	result  accounterasure.CellTombstone
}

func (s *fakeCellExecutor) Erase(_ context.Context, command accounterasure.CellEraseCommand) (accounterasure.CellTombstone, error) {
	s.command = command
	return s.result, nil
}
func (s *fakeCellExecutor) AttestErasure(_ context.Context, _ ids.CellID, _ string, _ []byte) (accounterasure.CellTombstone, error) {
	return s.result, nil
}

func TestCellExecutionValidatesContentFreeEvidence(t *testing.T) {
	now := time.Date(2026, 8, 18, 22, 0, 0, 0, time.UTC)
	store := &fakeCellExecutor{result: accounterasure.CellTombstone{RequestID: "11111111-1111-4111-8111-111111111111"}}
	service, _ := accounterasure.NewCellExecutionService(store, fixedClock{now})
	command := accounterasure.CellEraseCommand{RequestID: "11111111-1111-4111-8111-111111111111", AccountID: "22222222-2222-4222-8222-222222222222", CellID: "cell-a", PlacementGeneration: 3, AccountFingerprint: make([]byte, 32), PolicyVersion: 1, RequestVersion: 2, Environment: "test", OperatorEvidenceSHA256: make([]byte, 32), BackupExpiresAt: now.Add(24 * time.Hour)}
	if _, err := service.Erase(context.Background(), command); err != nil || store.command.RequestID != command.RequestID {
		t.Fatalf("erase command=%+v err=%v", store.command, err)
	}
	command.OperatorEvidenceSHA256 = nil
	if _, err := service.Erase(context.Background(), command); !errors.Is(err, accounterasure.ErrInvalidChange) {
		t.Fatalf("missing operator evidence=%v", err)
	}
}

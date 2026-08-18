package accounterasure

import (
	"context"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type CellEraseCommand struct {
	RequestID              string
	AccountID              ids.AccountID
	CellID                 ids.CellID
	PlacementGeneration    uint64
	AccountFingerprint     []byte
	PolicyVersion          uint64
	RequestVersion         uint64
	Environment            string
	ExportSHA256           []byte
	OperatorEvidenceSHA256 []byte
	BackupExpiresAt        time.Time
}

type CellTombstone struct {
	RequestID              string
	AccountFingerprint     []byte
	PlacementGeneration    uint64
	PolicyVersion          uint64
	RequestVersion         uint64
	Environment            string
	ErasedAt               time.Time
	RowCounts              map[string]int64
	ExportSHA256           []byte
	OperatorEvidenceSHA256 []byte
	BackupExpiresAt        time.Time
	LedgerSequence         uint64
	LedgerRoot             []byte
}

type CellExecutor interface {
	Erase(context.Context, CellEraseCommand) (CellTombstone, error)
	AttestErasure(context.Context, ids.CellID, string, []byte) (CellTombstone, error)
}

type CellExecutionService struct {
	store CellExecutor
	clock Clock
}

func NewCellExecutionService(store CellExecutor, clock Clock) (*CellExecutionService, error) {
	if store == nil || clock == nil {
		return nil, ErrInvalidChange
	}
	return &CellExecutionService{store: store, clock: clock}, nil
}

func (s *CellExecutionService) Erase(ctx context.Context, command CellEraseCommand) (CellTombstone, error) {
	if !validCellEraseCommand(command, s.clock.Now().UTC()) {
		return CellTombstone{}, ErrInvalidChange
	}
	return s.store.Erase(ctx, command)
}

func (s *CellExecutionService) Attest(ctx context.Context, cellID ids.CellID, requestID string, fingerprint []byte) (CellTombstone, error) {
	if strings.TrimSpace(string(cellID)) == "" || ids.Validate(requestID) != nil || len(fingerprint) != 32 {
		return CellTombstone{}, ErrInvalidChange
	}
	return s.store.AttestErasure(ctx, cellID, requestID, fingerprint)
}

func validCellEraseCommand(command CellEraseCommand, now time.Time) bool {
	return ids.Validate(command.RequestID) == nil && ids.Validate(string(command.AccountID)) == nil &&
		strings.TrimSpace(string(command.CellID)) != "" && command.PlacementGeneration > 0 && len(command.AccountFingerprint) == 32 &&
		command.PolicyVersion > 0 && command.RequestVersion > 0 && validEnvironment.MatchString(command.Environment) &&
		(len(command.ExportSHA256) == 0 || len(command.ExportSHA256) == 32) && len(command.OperatorEvidenceSHA256) == 32 &&
		command.BackupExpiresAt.After(now)
}

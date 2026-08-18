package accounterasure

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type ExecutionClaim struct {
	RequestID              string
	EventID                string
	AccountID              ids.AccountID
	ExpectedVersion        uint64
	LeaseID                string
	LeaseDuration          time.Duration
	AccountFingerprint     []byte
	OperatorEvidenceSHA256 []byte
	Change                 Change
}

type CellRecord struct {
	RequestID           string
	EventID             string
	ExpectedVersion     uint64
	LeaseID             string
	AccountFingerprint  []byte
	CellErasedAt        time.Time
	CellRowCounts       map[string]int64
	CellTombstoneSHA256 []byte
	Change              Change
}

type GlobalFinalize struct {
	RequestID           string
	AccountID           ids.AccountID
	ExpectedVersion     uint64
	LeaseID             string
	AccountFingerprint  []byte
	CellTombstoneSHA256 []byte
	Environment         string
}

type GlobalTombstone struct {
	RequestID                            string
	AccountFingerprint                   []byte
	PolicyVersion, FinalRequestVersion   uint64
	Environment                          string
	PreparedAt, ApprovedAt, CellErasedAt time.Time
	CompletedAt                          time.Time
	CellRowCounts, GlobalRowCounts       map[string]int64
	ExportSHA256, CellTombstoneSHA256    []byte
	OperatorEvidenceSHA256               []byte
	BackupExpiresAt                      time.Time
	LedgerSequence                       uint64
	LedgerRoot                           []byte
}

type ExecutionStore interface {
	ClaimExecution(context.Context, ExecutionClaim) (Request, error)
	RecordCellErasure(context.Context, CellRecord) (Request, error)
	FinalizeGlobalErasure(context.Context, GlobalFinalize) (GlobalTombstone, error)
	AttestGlobalErasure(context.Context, string, []byte) (GlobalTombstone, error)
}

type ExecuteCommand struct {
	RequestID       string
	AccountID       ids.AccountID
	ExpectedVersion uint64
	LeaseDuration   time.Duration
	Actor           string
	Reason          string
	Environment     string
}

type ExecutionService struct {
	global      ExecutionStore
	cell        CellExecutor
	ids         ids.Generator
	clock       Clock
	evidenceKey []byte
}

func NewExecutionService(global ExecutionStore, cell CellExecutor, generator ids.Generator, clock Clock, evidenceKey []byte) (*ExecutionService, error) {
	if global == nil || cell == nil || generator == nil || clock == nil || len(evidenceKey) < 32 {
		return nil, ErrInvalidChange
	}
	return &ExecutionService{global: global, cell: cell, ids: generator, clock: clock, evidenceKey: bytes.Clone(evidenceKey)}, nil
}

func (s *ExecutionService) Execute(ctx context.Context, command ExecuteCommand) (GlobalTombstone, error) {
	if ids.Validate(command.RequestID) != nil || ids.Validate(string(command.AccountID)) != nil || command.ExpectedVersion == 0 ||
		command.LeaseDuration < 30*time.Second || command.LeaseDuration > time.Hour {
		return GlobalTombstone{}, ErrInvalidChange
	}
	fingerprint := keyedDigest(s.evidenceKey, "spyglass-account-erasure-fingerprint-v1", string(command.AccountID))
	completed, err := s.global.AttestGlobalErasure(ctx, command.RequestID, fingerprint)
	if err == nil {
		return completed, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return GlobalTombstone{}, err
	}
	change, err := s.change(command.Actor, command.Reason, command.Environment)
	if err != nil {
		return GlobalTombstone{}, err
	}
	operatorEvidence := keyedDigest(s.evidenceKey, "spyglass-account-erasure-operator-evidence-v1", strings.Join([]string{command.RequestID, string(command.AccountID), change.Actor, change.Reason, change.Environment}, "\x00"))
	request, leaseID, err := s.claim(ctx, command, change, fingerprint, operatorEvidence)
	if err != nil {
		return GlobalTombstone{}, err
	}

	if request.State == StateCellErasing {
		cellResult, err := s.cell.Erase(ctx, CellEraseCommand{
			RequestID: request.ID, AccountID: request.AccountID, CellID: request.CellID,
			PlacementGeneration: request.PlacementGeneration, AccountFingerprint: fingerprint,
			PolicyVersion: request.PolicyVersion, RequestVersion: request.CellRequestVersion,
			Environment: request.Environment, ExportSHA256: request.ExportSHA256,
			OperatorEvidenceSHA256: operatorEvidence, BackupExpiresAt: request.BackupExpiresAt,
		})
		if err != nil {
			return GlobalTombstone{}, err
		}
		if err := verifyCellTombstone(request, cellResult, fingerprint, operatorEvidence); err != nil {
			return GlobalTombstone{}, err
		}
		cellDigest, err := digestCellTombstone(cellResult)
		if err != nil {
			return GlobalTombstone{}, err
		}
		recordChange, err := s.change(command.Actor, command.Reason, command.Environment)
		if err != nil {
			return GlobalTombstone{}, err
		}
		request, err = s.global.RecordCellErasure(ctx, CellRecord{
			RequestID: request.ID, EventID: recordChange.EventID, ExpectedVersion: request.Version,
			LeaseID: leaseID, AccountFingerprint: fingerprint, CellErasedAt: cellResult.ErasedAt,
			CellRowCounts: cellResult.RowCounts, CellTombstoneSHA256: cellDigest, Change: recordChange,
		})
		if err != nil {
			return GlobalTombstone{}, err
		}
		command.ExpectedVersion = request.Version
		change, err = s.change(command.Actor, command.Reason, command.Environment)
		if err != nil {
			return GlobalTombstone{}, err
		}
		request, leaseID, err = s.claim(ctx, command, change, fingerprint, operatorEvidence)
		if err != nil {
			return GlobalTombstone{}, err
		}
	}
	if request.State != StateGlobalErasing {
		return GlobalTombstone{}, ErrStateConflict
	}
	cellAttestation, err := s.cell.AttestErasure(ctx, request.CellID, request.ID, fingerprint)
	if err != nil {
		return GlobalTombstone{}, err
	}
	if err := verifyCellTombstone(request, cellAttestation, fingerprint, operatorEvidence); err != nil {
		return GlobalTombstone{}, err
	}
	attestedDigest, err := digestCellTombstone(cellAttestation)
	if err != nil || !bytes.Equal(attestedDigest, request.CellTombstoneSHA256) {
		return GlobalTombstone{}, ErrStateConflict
	}
	return s.global.FinalizeGlobalErasure(ctx, GlobalFinalize{
		RequestID: request.ID, AccountID: request.AccountID, ExpectedVersion: request.Version,
		LeaseID: leaseID, AccountFingerprint: fingerprint, CellTombstoneSHA256: attestedDigest,
		Environment: request.Environment,
	})
}

func (s *ExecutionService) claim(ctx context.Context, command ExecuteCommand, change Change, fingerprint, operatorEvidence []byte) (Request, string, error) {
	leaseID := s.ids.New()
	if ids.Validate(leaseID) != nil {
		return Request{}, "", ErrInvalidChange
	}
	request, err := s.global.ClaimExecution(ctx, ExecutionClaim{
		RequestID: command.RequestID, EventID: change.EventID, AccountID: command.AccountID,
		ExpectedVersion: command.ExpectedVersion, LeaseID: leaseID, LeaseDuration: command.LeaseDuration,
		AccountFingerprint: fingerprint, OperatorEvidenceSHA256: operatorEvidence, Change: change,
	})
	return request, leaseID, err
}

func (s *ExecutionService) change(actor, reason, environment string) (Change, error) {
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

func keyedDigest(key []byte, domain, value string) []byte {
	digest := hmac.New(sha256.New, key)
	_, _ = digest.Write([]byte(domain))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(value))
	return digest.Sum(nil)
}

func verifyCellTombstone(request Request, result CellTombstone, fingerprint, operatorEvidence []byte) error {
	if result.RequestID != request.ID || !bytes.Equal(result.AccountFingerprint, fingerprint) ||
		result.PlacementGeneration != request.PlacementGeneration || result.PolicyVersion != request.PolicyVersion ||
		result.RequestVersion != request.CellRequestVersion || result.Environment != request.Environment ||
		!bytes.Equal(result.ExportSHA256, request.ExportSHA256) || !bytes.Equal(result.OperatorEvidenceSHA256, operatorEvidence) ||
		!result.BackupExpiresAt.Equal(request.BackupExpiresAt) || result.ErasedAt.IsZero() {
		return ErrStateConflict
	}
	for name, count := range result.RowCounts {
		if strings.TrimSpace(name) == "" || count < 0 {
			return ErrStateConflict
		}
	}
	return nil
}

func digestCellTombstone(value CellTombstone) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(raw)
	return digest[:], nil
}

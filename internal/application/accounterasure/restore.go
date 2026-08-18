package accounterasure

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const RestoreDirectiveVersion = 1

var ErrInvalidRestoreDirective = errors.New("Account erasure restore directive is invalid")

type RestoreCheckpoint struct {
	PreviousSequence uint64 `json:"previous_sequence"`
	PreviousRoot     []byte `json:"previous_root"`
	Sequence         uint64 `json:"sequence"`
	Root             []byte `json:"root"`
}

type RestoreDirective struct {
	Version                      int               `json:"version"`
	RequestID                    string            `json:"request_id"`
	AccountID                    ids.AccountID     `json:"account_id"`
	CellID                       ids.CellID        `json:"cell_id"`
	TombstonePlacementGeneration uint64            `json:"tombstone_placement_generation"`
	AccountFingerprint           []byte            `json:"account_fingerprint"`
	PolicyVersion                uint64            `json:"policy_version"`
	CellRequestVersion           uint64            `json:"cell_request_version"`
	FinalRequestVersion          uint64            `json:"final_request_version"`
	Environment                  string            `json:"environment"`
	PreparedAt                   time.Time         `json:"prepared_at"`
	ApprovedAt                   time.Time         `json:"approved_at"`
	CellErasedAt                 time.Time         `json:"cell_erased_at"`
	CompletedAt                  time.Time         `json:"completed_at"`
	ExportSHA256                 []byte            `json:"export_sha256,omitempty"`
	CellTombstoneSHA256          []byte            `json:"cell_tombstone_sha256"`
	OperatorEvidenceSHA256       []byte            `json:"operator_evidence_sha256"`
	BackupExpiresAt              time.Time         `json:"backup_expires_at"`
	CellCheckpoint               RestoreCheckpoint `json:"cell_checkpoint"`
	GlobalCheckpoint             RestoreCheckpoint `json:"global_checkpoint"`
}

type SignedRestoreDirective struct {
	Directive RestoreDirective `json:"directive"`
	Signature []byte           `json:"signature"`
}

type RestoreTarget struct {
	Completed           bool
	CellID              ids.CellID
	PlacementGeneration uint64
}

type CellRestoreReplay struct {
	Directive                   RestoreDirective
	RestoredPlacementGeneration uint64
}

type GlobalRestoreReplay struct {
	Directive                   RestoreDirective
	RestoredCellID              ids.CellID
	RestoredPlacementGeneration uint64
}

type RestoreGlobalStore interface {
	ResolveRestore(context.Context, string, ids.AccountID, []byte) (RestoreTarget, error)
	ReplayGlobalRestore(context.Context, GlobalRestoreReplay) (GlobalTombstone, error)
	AttestGlobalErasure(context.Context, string, []byte) (GlobalTombstone, error)
}

type RestoreCellStore interface {
	ReplayCellRestore(context.Context, CellRestoreReplay) (CellTombstone, error)
}

type RestoreService struct {
	global     RestoreGlobalStore
	cell       RestoreCellStore
	signingKey []byte
}

func NewRestoreService(global RestoreGlobalStore, cell RestoreCellStore, signingKey []byte) (*RestoreService, error) {
	if global == nil || cell == nil || len(signingKey) != 32 {
		return nil, ErrInvalidRestoreDirective
	}
	return &RestoreService{global: global, cell: cell, signingKey: bytes.Clone(signingKey)}, nil
}

func SignRestoreDirective(directive RestoreDirective, signingKey []byte) (SignedRestoreDirective, error) {
	if len(signingKey) != 32 || !validRestoreDirective(directive) {
		return SignedRestoreDirective{}, ErrInvalidRestoreDirective
	}
	signature, err := restoreDirectiveSignature(directive, signingKey)
	if err != nil {
		return SignedRestoreDirective{}, err
	}
	return SignedRestoreDirective{Directive: directive, Signature: signature}, nil
}

func (s *RestoreService) Replay(ctx context.Context, signed SignedRestoreDirective, configuredCellID ids.CellID, environment string) (GlobalTombstone, error) {
	if !validRestoreDirective(signed.Directive) || len(signed.Signature) != sha256.Size || configuredCellID == "" || environment != signed.Directive.Environment {
		return GlobalTombstone{}, ErrInvalidRestoreDirective
	}
	expectedSignature, err := restoreDirectiveSignature(signed.Directive, s.signingKey)
	if err != nil || !hmac.Equal(expectedSignature, signed.Signature) {
		return GlobalTombstone{}, ErrInvalidRestoreDirective
	}
	directive := signed.Directive
	target, err := s.global.ResolveRestore(ctx, directive.RequestID, directive.AccountID, directive.AccountFingerprint)
	if err != nil {
		return GlobalTombstone{}, err
	}
	if target.Completed {
		result, err := s.global.AttestGlobalErasure(ctx, directive.RequestID, directive.AccountFingerprint)
		if err != nil {
			return GlobalTombstone{}, err
		}
		if err := verifyRestoredGlobalTombstone(directive, result); err != nil {
			return GlobalTombstone{}, err
		}
		return result, nil
	}
	if target.CellID != configuredCellID || directive.CellID != configuredCellID || target.PlacementGeneration == 0 {
		return GlobalTombstone{}, ErrCellMismatch
	}
	cellResult, err := s.cell.ReplayCellRestore(ctx, CellRestoreReplay{Directive: directive, RestoredPlacementGeneration: target.PlacementGeneration})
	if err != nil {
		return GlobalTombstone{}, err
	}
	if err := verifyRestoredCellTombstone(directive, cellResult); err != nil {
		return GlobalTombstone{}, err
	}
	result, err := s.global.ReplayGlobalRestore(ctx, GlobalRestoreReplay{Directive: directive, RestoredCellID: target.CellID, RestoredPlacementGeneration: target.PlacementGeneration})
	if err != nil {
		return GlobalTombstone{}, err
	}
	if err := verifyRestoredGlobalTombstone(directive, result); err != nil {
		return GlobalTombstone{}, err
	}
	return result, nil
}

func verifyRestoredGlobalTombstone(directive RestoreDirective, result GlobalTombstone) error {
	if result.RequestID != directive.RequestID || !bytes.Equal(result.AccountFingerprint, directive.AccountFingerprint) ||
		result.PolicyVersion != directive.PolicyVersion || result.FinalRequestVersion != directive.FinalRequestVersion ||
		result.Environment != directive.Environment || !result.PreparedAt.Equal(directive.PreparedAt) ||
		!result.ApprovedAt.Equal(directive.ApprovedAt) || !result.CellErasedAt.Equal(directive.CellErasedAt) ||
		!result.CompletedAt.Equal(directive.CompletedAt) || !bytes.Equal(result.ExportSHA256, directive.ExportSHA256) ||
		!bytes.Equal(result.CellTombstoneSHA256, directive.CellTombstoneSHA256) ||
		!bytes.Equal(result.OperatorEvidenceSHA256, directive.OperatorEvidenceSHA256) ||
		!result.BackupExpiresAt.Equal(directive.BackupExpiresAt) || result.LedgerSequence != directive.GlobalCheckpoint.Sequence ||
		!bytes.Equal(result.LedgerRoot, directive.GlobalCheckpoint.Root) {
		return ErrStateConflict
	}
	return nil
}

func validRestoreDirective(value RestoreDirective) bool {
	return value.Version == RestoreDirectiveVersion && ids.Validate(value.RequestID) == nil && ids.Validate(string(value.AccountID)) == nil &&
		value.CellID != "" && value.TombstonePlacementGeneration > 0 && len(value.AccountFingerprint) == 32 &&
		value.PolicyVersion > 0 && value.CellRequestVersion > 0 && value.FinalRequestVersion > 0 && validEnvironment.MatchString(value.Environment) &&
		!value.PreparedAt.IsZero() && !value.ApprovedAt.Before(value.PreparedAt) && !value.CellErasedAt.Before(value.ApprovedAt) &&
		!value.CompletedAt.Before(value.CellErasedAt) && value.BackupExpiresAt.After(value.CompletedAt) &&
		(len(value.ExportSHA256) == 0 || len(value.ExportSHA256) == 32) && len(value.CellTombstoneSHA256) == 32 &&
		len(value.OperatorEvidenceSHA256) == 32 && validRestoreCheckpoint(value.CellCheckpoint) && validRestoreCheckpoint(value.GlobalCheckpoint)
}

func validRestoreCheckpoint(value RestoreCheckpoint) bool {
	return value.Sequence == value.PreviousSequence+1 && len(value.PreviousRoot) == 32 && len(value.Root) == 32 &&
		(value.PreviousSequence != 0 || bytes.Equal(value.PreviousRoot, make([]byte, 32))) && !bytes.Equal(value.Root, make([]byte, 32))
}

func restoreDirectiveSignature(directive RestoreDirective, key []byte) ([]byte, error) {
	raw, err := json.Marshal(directive)
	if err != nil {
		return nil, err
	}
	digest := hmac.New(sha256.New, key)
	_, _ = digest.Write([]byte("spyglass-account-erasure-restore-directive-v1"))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write(raw)
	return digest.Sum(nil), nil
}

func verifyRestoredCellTombstone(directive RestoreDirective, result CellTombstone) error {
	if result.RequestID != directive.RequestID || !bytes.Equal(result.AccountFingerprint, directive.AccountFingerprint) ||
		result.PlacementGeneration != directive.TombstonePlacementGeneration || result.PolicyVersion != directive.PolicyVersion ||
		result.RequestVersion != directive.CellRequestVersion || result.Environment != directive.Environment ||
		!result.ErasedAt.Equal(directive.CellErasedAt) || !bytes.Equal(result.ExportSHA256, directive.ExportSHA256) ||
		!bytes.Equal(result.OperatorEvidenceSHA256, directive.OperatorEvidenceSHA256) || !result.BackupExpiresAt.Equal(directive.BackupExpiresAt) ||
		result.LedgerSequence != directive.CellCheckpoint.Sequence || !bytes.Equal(result.LedgerRoot, directive.CellCheckpoint.Root) {
		return ErrStateConflict
	}
	return nil
}

// Package accounterasureadmin wires the short-lived, reviewed Account erasure
// preparation and cross-store execution command.
package accounterasureadmin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/accounterasure"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Config struct {
	GlobalDatabaseURL, CellDatabaseURL string
	CellID                             ids.CellID
	Action                             string
	Actor, Reason, Environment         string
	ConfirmEnvironment                 string
	AccountID, ConfirmAccountID        ids.AccountID
	RequestID                          string
	ExpectedVersion, PolicyVersion     uint64
	Export                             accounterasure.ExportEvidence
	BackupExpiresAt                    time.Time
	EvidenceKey                        []byte
	RestoreSigningKey                  []byte
	RestoreDirectiveFile               string
	LeaseDuration                      time.Duration
	MaxGlobalConns, MaxCellConns       int32
}

type unusedCell struct{}

func (unusedCell) Attest(context.Context, accounterasure.Target) (accounterasure.CellAttestation, error) {
	return accounterasure.CellAttestation{}, errors.New("cell attestation is unavailable for this action")
}

func Run(ctx context.Context, config Config, logger *slog.Logger) error {
	if err := validateConfig(config, logger); err != nil {
		return err
	}
	globalConfig, err := pgxpool.ParseConfig(config.GlobalDatabaseURL)
	if err != nil {
		return err
	}
	if config.MaxGlobalConns > 0 {
		globalConfig.MaxConns = config.MaxGlobalConns
	}
	globalPool, err := pgxpool.NewWithConfig(ctx, globalConfig)
	if err != nil {
		return err
	}
	defer globalPool.Close()
	if err := globalPool.Ping(ctx); err != nil {
		return err
	}

	var cellPool *pgxpool.Pool
	var cellStore accounterasure.CellStore = unusedCell{}
	var cellExecutor accounterasure.CellExecutor
	if config.Action == "prepare" || config.Action == "approve" || config.Action == "execute" || config.Action == "restore-replay" {
		cellConfig, err := pgxpool.ParseConfig(config.CellDatabaseURL)
		if err != nil {
			return err
		}
		if config.MaxCellConns > 0 {
			cellConfig.MaxConns = config.MaxCellConns
		}
		cellPool, err = pgxpool.NewWithConfig(ctx, cellConfig)
		if err != nil {
			return err
		}
		defer cellPool.Close()
		if err := cellPool.Ping(ctx); err != nil {
			return err
		}
		cellRepository := postgres.NewAccountErasureCellRepository(cellPool, config.CellID)
		cellStore = cellRepository
		cellExecutor = cellRepository
	}
	globalRepository := postgres.NewAccountErasureRepository(globalPool)
	if config.Action == "restore-replay" {
		signed, err := readRestoreDirective(config.RestoreDirectiveFile)
		if err != nil {
			return err
		}
		if signed.Directive.RequestID != config.RequestID || signed.Directive.AccountID != config.AccountID || signed.Directive.CellID != config.CellID {
			return accounterasure.ErrInvalidRestoreDirective
		}
		restoreCellStore, ok := cellExecutor.(accounterasure.RestoreCellStore)
		if !ok {
			return errors.New("Account erasure restore cell authority is unavailable")
		}
		service, err := accounterasure.NewRestoreService(globalRepository, restoreCellStore, config.RestoreSigningKey)
		if err != nil {
			return err
		}
		result, err := service.Replay(ctx, signed, config.CellID, config.Environment)
		if err != nil {
			return err
		}
		logger.Info("Spyglass Account erasure restore replay complete", "request_id", result.RequestID, "global_ledger_sequence", result.LedgerSequence, "global_ledger_root", fmt.Sprintf("%x", result.LedgerRoot), "environment", result.Environment)
		return nil
	}
	if config.Action == "execute" {
		service, err := accounterasure.NewExecutionService(globalRepository, cellExecutor, ids.RandomGenerator{}, registration.SystemClock{}, config.EvidenceKey)
		if err != nil {
			return err
		}
		result, err := service.Execute(ctx, accounterasure.ExecuteCommand{
			RequestID: config.RequestID, AccountID: config.AccountID, ExpectedVersion: config.ExpectedVersion,
			LeaseDuration: config.LeaseDuration, Actor: config.Actor, Reason: config.Reason, Environment: config.Environment,
		})
		if err != nil {
			return err
		}
		logger.Info("Spyglass Account erasure execution complete", "request_id", result.RequestID, "state", "completed", "policy_version", result.PolicyVersion, "environment", result.Environment, "completed_at", result.CompletedAt, "backup_expires_at", result.BackupExpiresAt)
		return nil
	}
	service, err := accounterasure.NewService(globalRepository, cellStore, ids.RandomGenerator{}, registration.SystemClock{})
	if err != nil {
		return err
	}
	var result accounterasure.Request
	switch config.Action {
	case "prepare":
		result, err = service.Prepare(ctx, accounterasure.PrepareCommand{AccountID: config.AccountID, PolicyVersion: config.PolicyVersion, Export: config.Export, BackupExpiresAt: config.BackupExpiresAt, Actor: config.Actor, Reason: config.Reason, Environment: config.Environment})
	case "inspect":
		result, err = service.Inspect(ctx, config.RequestID, config.Actor, config.Reason, config.Environment)
	case "approve":
		result, err = service.Approve(ctx, config.RequestID, config.ExpectedVersion, config.Actor, config.Reason, config.Environment)
	case "cancel":
		result, err = service.Cancel(ctx, config.RequestID, config.ExpectedVersion, config.Actor, config.Reason, config.Environment)
	default:
		return fmt.Errorf("unsupported Account erasure operator action %q", config.Action)
	}
	if err != nil {
		return err
	}
	attributes := []any{"action", config.Action, "request_id", result.ID, "state", result.State, "request_version", result.Version, "environment", result.Environment, "actor", config.Actor, "account_id", result.AccountID, "cell_id", result.CellID, "placement_generation", result.PlacementGeneration, "policy_version", result.PolicyVersion}
	if config.Action == "inspect" {
		attributes = append(attributes, "export_disposition", result.ExportDisposition, "export_reference", result.ExportReference, "export_sha256", fmt.Sprintf("%x", result.ExportSHA256), "export_expires_at", result.ExportExpiresAt, "backup_expires_at", result.BackupExpiresAt, "requested_by", result.RequestedBy, "approved_by", result.ApprovedBy)
	}
	logger.Info("Spyglass Account erasure operator action complete", attributes...)
	return nil
}

func validateConfig(config Config, logger *slog.Logger) error {
	if config.GlobalDatabaseURL == "" || config.Action == "" || config.Actor == "" || config.Reason == "" || config.Environment == "" || logger == nil {
		return errors.New("Account erasure operator global database, action, actor, reason, environment, and logger are required")
	}
	if config.ConfirmEnvironment == "" || config.ConfirmEnvironment != config.Environment {
		return errors.New("Account erasure operator environment confirmation must exactly match the target environment")
	}
	switch config.Action {
	case "prepare":
		if config.CellDatabaseURL == "" || config.CellID == "" || ids.Validate(string(config.AccountID)) != nil || config.ConfirmAccountID != config.AccountID || config.PolicyVersion == 0 || config.BackupExpiresAt.IsZero() {
			return accounterasure.ErrInvalidChange
		}
	case "approve":
		if config.CellDatabaseURL == "" || config.CellID == "" || ids.Validate(config.RequestID) != nil || config.ExpectedVersion == 0 {
			return accounterasure.ErrInvalidChange
		}
	case "inspect":
		if ids.Validate(config.RequestID) != nil {
			return accounterasure.ErrInvalidChange
		}
	case "cancel":
		if ids.Validate(config.RequestID) != nil || config.ExpectedVersion == 0 {
			return accounterasure.ErrInvalidChange
		}
	case "execute":
		if config.CellDatabaseURL == "" || config.CellID == "" || ids.Validate(config.RequestID) != nil ||
			ids.Validate(string(config.AccountID)) != nil || config.ConfirmAccountID != config.AccountID ||
			config.ExpectedVersion == 0 || len(config.EvidenceKey) != 32 ||
			config.LeaseDuration < 30*time.Second || config.LeaseDuration > time.Hour {
			return accounterasure.ErrInvalidChange
		}
	case "restore-replay":
		if config.CellDatabaseURL == "" || config.CellID == "" || ids.Validate(config.RequestID) != nil ||
			ids.Validate(string(config.AccountID)) != nil || config.ConfirmAccountID != config.AccountID ||
			len(config.RestoreSigningKey) != 32 || config.RestoreDirectiveFile == "" {
			return accounterasure.ErrInvalidRestoreDirective
		}
	default:
		return fmt.Errorf("unsupported Account erasure operator action %q", config.Action)
	}
	return nil
}

func readRestoreDirective(path string) (accounterasure.SignedRestoreDirective, error) {
	file, err := os.Open(path)
	if err != nil {
		return accounterasure.SignedRestoreDirective{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return accounterasure.SignedRestoreDirective{}, accounterasure.ErrInvalidRestoreDirective
	}
	decoder := json.NewDecoder(io.LimitReader(file, (64<<10)+1))
	decoder.DisallowUnknownFields()
	var result accounterasure.SignedRestoreDirective
	if err := decoder.Decode(&result); err != nil {
		return accounterasure.SignedRestoreDirective{}, accounterasure.ErrInvalidRestoreDirective
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return accounterasure.SignedRestoreDirective{}, accounterasure.ErrInvalidRestoreDirective
	}
	return result, nil
}

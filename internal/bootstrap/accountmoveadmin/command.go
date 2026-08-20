// Package accountmoveadmin wires the short-lived cross-cell Account movement command.
package accountmoveadmin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/accountmovement"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Config struct {
	GlobalDatabaseURL      string
	SourceCellDatabaseURL  string
	DestinationDatabaseURL string
	Action                 string
	MoveID                 string
	AccountID              ids.AccountID
	ConfirmAccountID       ids.AccountID
	SourceCellID           ids.CellID
	DestinationCellID      ids.CellID
	ExpectedVersion        uint64
	RollbackWindow         time.Duration
	Lease                  time.Duration
	Actor, Reason          string
	Environment            string
	ConfirmEnvironment     string
	MaxGlobalConns         int32
	MaxCellConns           int32
}

func Run(ctx context.Context, config Config, logger *slog.Logger) error {
	if err := validate(config, logger); err != nil {
		return err
	}
	global, err := openMovementPool(ctx, config.GlobalDatabaseURL, config.MaxGlobalConns)
	if err != nil {
		return err
	}
	defer global.Close()
	store := postgres.NewAccountMovementRepository(global)
	var cells accountmovement.Cells = unavailableCells{}
	var source, destination *pgxpool.Pool
	if needsCells(config.Action) {
		source, err = openMovementPool(ctx, config.SourceCellDatabaseURL, config.MaxCellConns)
		if err != nil {
			return err
		}
		defer source.Close()
		destination, err = openMovementPool(ctx, config.DestinationDatabaseURL, config.MaxCellConns)
		if err != nil {
			return err
		}
		defer destination.Close()
		mover, err := postgres.NewAccountCellMover(source, destination)
		if err != nil {
			return err
		}
		cells = &boundedCells{delegate: mover, source: config.SourceCellID, destination: config.DestinationCellID}
	}
	service, err := accountmovement.NewService(store, cells, ids.RandomGenerator{}, registration.SystemClock{}, config.Lease)
	if err != nil {
		return err
	}
	var result accountmovement.Move
	switch config.Action {
	case "prepare":
		result, err = service.Prepare(ctx, accountmovement.PrepareCommand{AccountID: config.AccountID, DestinationCellID: config.DestinationCellID, RollbackWindow: config.RollbackWindow, Actor: config.Actor, Reason: config.Reason, Environment: config.Environment})
	case "inspect":
		result, err = service.Inspect(ctx, config.MoveID, config.Actor, config.Reason, config.Environment)
	case "advance":
		result, err = service.Advance(ctx, config.MoveID, config.Actor, config.Reason, config.Environment)
	case "pause":
		result, err = service.Pause(ctx, config.MoveID, config.ExpectedVersion, true, config.Actor, config.Reason, config.Environment)
	case "resume":
		result, err = service.Pause(ctx, config.MoveID, config.ExpectedVersion, false, config.Actor, config.Reason, config.Environment)
	case "rollback":
		result, err = service.Rollback(ctx, config.MoveID, config.Actor, config.Reason, config.Environment)
	case "retire":
		result, err = service.Retire(ctx, config.MoveID, config.Actor, config.Reason, config.Environment)
	default:
		return fmt.Errorf("unsupported Account move action %q", config.Action)
	}
	if err != nil {
		return err
	}
	logger.Info("Spyglass Account move operator action complete", "action", config.Action, "move_id", result.ID, "state", result.State,
		"move_version", result.Version, "account_id", result.AccountID, "source_cell_id", result.SourceCellID,
		"destination_cell_id", result.DestinationCellID, "source_generation", result.SourceGeneration,
		"destination_generation", result.DestinationGeneration, "rollback_generation", result.RollbackGeneration,
		"environment", result.Environment)
	return nil
}

func validate(config Config, logger *slog.Logger) error {
	if logger == nil || config.GlobalDatabaseURL == "" || config.Actor == "" || config.Reason == "" || config.Environment == "" ||
		config.ConfirmEnvironment != config.Environment || config.Lease < 30*time.Second || config.Lease > time.Hour {
		return accountmovement.ErrInvalid
	}
	switch config.Action {
	case "prepare":
		if ids.Validate(string(config.AccountID)) != nil || config.ConfirmAccountID != config.AccountID || config.DestinationCellID == "" ||
			config.RollbackWindow < 5*time.Minute || config.RollbackWindow > 7*24*time.Hour {
			return accountmovement.ErrInvalid
		}
	case "inspect":
		if ids.Validate(config.MoveID) != nil {
			return accountmovement.ErrInvalid
		}
	case "pause", "resume":
		if ids.Validate(config.MoveID) != nil || config.ExpectedVersion == 0 {
			return accountmovement.ErrInvalid
		}
	case "advance", "rollback", "retire":
		if ids.Validate(config.MoveID) != nil || config.SourceCellDatabaseURL == "" || config.DestinationDatabaseURL == "" ||
			config.SourceCellID == "" || config.DestinationCellID == "" || config.SourceCellID == config.DestinationCellID {
			return accountmovement.ErrInvalid
		}
	default:
		return accountmovement.ErrInvalid
	}
	return nil
}

func needsCells(action string) bool {
	return action == "advance" || action == "rollback" || action == "retire"
}

func openMovementPool(ctx context.Context, databaseURL string, maximum int32) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	if maximum > 0 {
		config.MaxConns = maximum
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

type boundedCells struct {
	delegate            accountmovement.Cells
	source, destination ids.CellID
}

func (c *boundedCells) valid(move accountmovement.Move) error {
	if c.delegate == nil || move.SourceCellID != c.source || move.DestinationCellID != c.destination {
		return errors.New("configured Account move cell databases do not match the durable move")
	}
	return nil
}
func (c *boundedCells) FreezeAndStage(ctx context.Context, move accountmovement.Move) error {
	if err := c.valid(move); err != nil {
		return err
	}
	return c.delegate.FreezeAndStage(ctx, move)
}
func (c *boundedCells) Copy(ctx context.Context, move accountmovement.Move) (accountmovement.Evidence, error) {
	if err := c.valid(move); err != nil {
		return accountmovement.Evidence{}, err
	}
	return c.delegate.Copy(ctx, move)
}
func (c *boundedCells) Reconcile(ctx context.Context, move accountmovement.Move) (accountmovement.Evidence, error) {
	if err := c.valid(move); err != nil {
		return accountmovement.Evidence{}, err
	}
	return c.delegate.Reconcile(ctx, move)
}
func (c *boundedCells) ActivateDestination(ctx context.Context, move accountmovement.Move) error {
	if err := c.valid(move); err != nil {
		return err
	}
	return c.delegate.ActivateDestination(ctx, move)
}
func (c *boundedCells) ActivateRollback(ctx context.Context, move accountmovement.Move) error {
	if err := c.valid(move); err != nil {
		return err
	}
	return c.delegate.ActivateRollback(ctx, move)
}
func (c *boundedCells) RetireSource(ctx context.Context, move accountmovement.Move) error {
	if err := c.valid(move); err != nil {
		return err
	}
	return c.delegate.RetireSource(ctx, move)
}

type unavailableCells struct{}

func (unavailableCells) FreezeAndStage(context.Context, accountmovement.Move) error {
	return errors.New("cell databases are unavailable for this action")
}
func (unavailableCells) Copy(context.Context, accountmovement.Move) (accountmovement.Evidence, error) {
	return accountmovement.Evidence{}, errors.New("cell databases are unavailable for this action")
}
func (unavailableCells) Reconcile(context.Context, accountmovement.Move) (accountmovement.Evidence, error) {
	return accountmovement.Evidence{}, errors.New("cell databases are unavailable for this action")
}
func (unavailableCells) ActivateDestination(context.Context, accountmovement.Move) error {
	return errors.New("cell databases are unavailable for this action")
}
func (unavailableCells) ActivateRollback(context.Context, accountmovement.Move) error {
	return errors.New("cell databases are unavailable for this action")
}
func (unavailableCells) RetireSource(context.Context, accountmovement.Move) error {
	return errors.New("cell databases are unavailable for this action")
}

// Package passkeyadmin wires the short-lived, audited passkey envelope-key
// inspection and re-encryption command. It never serves customer traffic.
package passkeyadmin

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/passkeys"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
)

type Config struct {
	DatabaseURL                        string
	Action, Actor, Reason, Environment string
	ConfirmEnvironment                 string
	EncryptionKeys                     map[int][]byte
	ActiveKeyVersion                   int
	Batch                              int
	MaxDatabaseConns                   int32
}

func Run(ctx context.Context, config Config, logger *slog.Logger) error {
	if err := validateConfig(config, logger); err != nil {
		return err
	}
	poolConfig, err := pgxpool.ParseConfig(config.DatabaseURL)
	if err != nil {
		return err
	}
	if config.MaxDatabaseConns > 0 {
		poolConfig.MaxConns = config.MaxDatabaseConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return err
	}
	cipher, err := passkeys.NewCipherKeyring(config.EncryptionKeys, config.ActiveKeyVersion)
	if err != nil {
		return err
	}
	repository, err := postgres.NewPasskeyRepository(pool, cipher)
	if err != nil {
		return err
	}
	service, err := passkeys.NewRotationService(repository, registration.SystemClock{})
	if err != nil {
		return err
	}
	if config.Action == "inspect" {
		status, err := service.Inspect(ctx, config.Actor, config.Reason, config.Environment)
		if err != nil {
			return err
		}
		logger.Info("Spyglass passkey encryption inspection complete", "active_key_version", status.ActiveVersion, "credential_versions", status.CredentialVersions, "ceremony_versions", status.CeremonyVersions, "environment", config.Environment)
		return nil
	}
	result, err := service.Reencrypt(ctx, config.Batch, config.Actor, config.Reason, config.Environment)
	if err != nil {
		return err
	}
	logger.Info("Spyglass passkey envelope re-encryption batch complete", "active_key_version", result.ActiveVersion, "updated", result.Updated, "credential_versions", result.CredentialVersions, "ceremony_versions", result.CeremonyVersions, "environment", config.Environment)
	return nil
}

func validateConfig(config Config, logger *slog.Logger) error {
	if config.DatabaseURL == "" || config.Actor == "" || config.Reason == "" || config.Environment == "" || logger == nil ||
		config.ConfirmEnvironment != config.Environment || config.ActiveKeyVersion <= 0 || len(config.EncryptionKeys) == 0 {
		return passkeys.ErrInvalidRotation
	}
	if _, exists := config.EncryptionKeys[config.ActiveKeyVersion]; !exists {
		return passkeys.ErrInvalidRotation
	}
	if _, err := passkeys.NewCipherKeyring(config.EncryptionKeys, config.ActiveKeyVersion); err != nil {
		return passkeys.ErrInvalidRotation
	}
	switch config.Action {
	case "inspect":
		if config.Batch != 0 {
			return passkeys.ErrInvalidRotation
		}
	case "reencrypt":
		if config.Batch < 1 || config.Batch > passkeys.MaximumRotationBatch {
			return passkeys.ErrInvalidRotation
		}
	default:
		return errors.New("passkey admin action must be inspect or reencrypt")
	}
	return nil
}

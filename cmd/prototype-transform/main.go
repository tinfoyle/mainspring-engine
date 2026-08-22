// Command prototype-transform creates a sealed, review-first migration bundle
// from one retained prototype tenant database. The source credential is read
// only from SPYGLASS_PROTOTYPE_DATABASE_URL so it is not exposed in argv.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/prototypemigration"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func main() {
	if err := run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "prototype-transform:", err)
		os.Exit(1)
	}
}

type options struct {
	tenantID  string
	accountID string
	output    string
}

func run(arguments []string, getenv func(string) string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("prototype-transform", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var config options
	flags.StringVar(&config.tenantID, "tenant-id", "", "prototype tenant UUID")
	flags.StringVar(&config.accountID, "account-id", "", "destination Spyglass Account UUID")
	flags.StringVar(&config.output, "output", "", "new private bundle directory")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 || ids.Validate(config.tenantID) != nil || ids.Validate(config.accountID) != nil || strings.TrimSpace(config.output) == "" {
		return errors.New("-tenant-id, -account-id, and -output are required")
	}
	databaseURL := strings.TrimSpace(getenv("SPYGLASS_PROTOTYPE_DATABASE_URL"))
	if databaseURL == "" {
		return errors.New("SPYGLASS_PROTOTYPE_DATABASE_URL is required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return errors.New("prototype database URL is invalid")
	}
	poolConfig.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return fmt.Errorf("open prototype database: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("verify prototype database: %w", err)
	}
	source, err := postgresadapter.NewPrototypeMigrationSource(pool)
	if err != nil {
		return err
	}
	snapshot, err := source.Snapshot(ctx, config.tenantID)
	if err != nil {
		return err
	}
	bundle, err := prototypemigration.NewTransformer().Transform(ids.AccountID(config.accountID), snapshot)
	if err != nil {
		return err
	}
	if err := writeBundle(config.output, bundle); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "manifest=%s facts=%d importable_claims=%d document_revisions=%d importable_documents=%d baselines=%d unresolved=%d\n", bundle.Manifest.ContentSHA256, bundle.Manifest.Totals.FactRecords, bundle.Manifest.Totals.ImportableClaims, bundle.Manifest.Totals.DocumentRevisions, bundle.Manifest.Totals.ImportableDocuments, bundle.Manifest.Totals.BaselineAssessments, bundle.Manifest.Totals.UnresolvedRecords)
	return err
}

type rollbackCheckpoint struct {
	Version           string `json:"version"`
	AccountID         string `json:"account_id"`
	TenantID          string `json:"tenant_id"`
	SourceCheckpoint  string `json:"source_checkpoint"`
	ManifestSHA256    string `json:"manifest_sha256"`
	DestinationWrites bool   `json:"destination_writes"`
}

func writeBundle(output string, bundle prototypemigration.Bundle) error {
	if err := prototypemigration.Verify(bundle); err != nil {
		return err
	}
	absolute, err := filepath.Abs(output)
	if err != nil {
		return fmt.Errorf("resolve bundle output: %w", err)
	}
	if _, err := os.Lstat(absolute); err == nil {
		return errors.New("bundle output already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect bundle output: %w", err)
	}
	parent := filepath.Dir(absolute)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create bundle parent: %w", err)
	}
	temporary, err := os.MkdirTemp(parent, ".prototype-transform-")
	if err != nil {
		return fmt.Errorf("create temporary bundle: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(temporary)
		}
	}()
	if err := os.Chmod(temporary, 0o700); err != nil {
		return err
	}
	manifest, err := json.MarshalIndent(bundle.Manifest, "", "  ")
	if err != nil {
		return err
	}
	unresolved, err := json.MarshalIndent(bundle.Manifest.Unresolved, "", "  ")
	if err != nil {
		return err
	}
	checkpoint, err := json.MarshalIndent(rollbackCheckpoint{Version: bundle.Manifest.Version, AccountID: string(bundle.Manifest.AccountID), TenantID: bundle.Manifest.Source.TenantID, SourceCheckpoint: bundle.Manifest.Source.Checkpoint, ManifestSHA256: bundle.Manifest.ContentSHA256, DestinationWrites: false}, "", "  ")
	if err != nil {
		return err
	}
	for name, body := range map[string][]byte{"manifest.json": append(manifest, '\n'), "unresolved.json": append(unresolved, '\n'), "rollback-checkpoint.json": append(checkpoint, '\n')} {
		if err := os.WriteFile(filepath.Join(temporary, name), body, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	objectsRoot := filepath.Join(temporary, "objects")
	if err := os.Mkdir(objectsRoot, 0o700); err != nil {
		return err
	}
	for relative, body := range bundle.Objects {
		clean := filepath.Clean(filepath.FromSlash(relative))
		if filepath.IsAbs(clean) || clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.Dir(clean) != "objects" {
			return errors.New("bundle object path is invalid")
		}
		target := filepath.Join(temporary, clean)
		if err := os.WriteFile(target, body, 0o600); err != nil {
			return fmt.Errorf("write bundle object: %w", err)
		}
	}
	if err := os.Rename(temporary, absolute); err != nil {
		return fmt.Errorf("publish bundle: %w", err)
	}
	committed = true
	return nil
}

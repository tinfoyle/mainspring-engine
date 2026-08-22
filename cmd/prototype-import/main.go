// Command prototype-import idempotently admits one sealed prototype bundle to
// the final Knowledge boundary and emits a no-overwrite reconciliation seal.
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/prototypebundle"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/s3objects"
	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/application/prototypemigration"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func main() {
	if err := run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "prototype-import:", err)
		os.Exit(1)
	}
}

type options struct{ bundle, certificate string }

type config struct {
	globalDatabaseURL, cellDatabaseURL         string
	objectEndpoint, objectRegion, objectBucket string
	objectAccessKey, objectSecretKey           string
	objectSecure, objectSSE                    bool
}

func run(arguments []string, getenv func(string) string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("prototype-import", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options options
	flags.StringVar(&options.bundle, "bundle", "", "sealed prototype bundle directory")
	flags.StringVar(&options.certificate, "certificate", "", "new reconciliation certificate JSON")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(options.bundle) == "" || strings.TrimSpace(options.certificate) == "" {
		return errors.New("-bundle and -certificate are required")
	}
	config, err := configFromEnvironment(getenv)
	if err != nil {
		return err
	}
	bundle, err := prototypebundle.Load(options.bundle)
	if err != nil {
		return fmt.Errorf("load sealed bundle: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	globalPool, err := openPool(ctx, config.globalDatabaseURL)
	if err != nil {
		return fmt.Errorf("open global database: %w", err)
	}
	defer globalPool.Close()
	cellPool, err := openPool(ctx, config.cellDatabaseURL)
	if err != nil {
		return fmt.Errorf("open cell database: %w", err)
	}
	defer cellPool.Close()
	cell, err := database.NewCellPool(cellPool)
	if err != nil {
		return err
	}
	objects, err := s3objects.New(s3objects.Config{Endpoint: config.objectEndpoint, Region: config.objectRegion, Bucket: config.objectBucket, AccessKey: config.objectAccessKey, SecretKey: config.objectSecretKey, Secure: config.objectSecure, ServerSideEncryption: config.objectSSE})
	if err != nil {
		return err
	}
	if err := objects.Verify(ctx); err != nil {
		return err
	}
	accessRepository := postgres.NewAccessRepository(globalPool)
	authorizer, err := access.NewWorkloadAuthorizer(accessRepository)
	if err != nil {
		return err
	}
	clock := registration.SystemClock{}
	knowledgeRepository, err := postgres.NewKnowledgeRepository(cell)
	if err != nil {
		return err
	}
	knowledgeService, err := knowledgeapp.New(authorizer, knowledgeRepository, clock)
	if err != nil {
		return err
	}
	documentService, err := knowledgeapp.NewDocumentService(authorizer, knowledgeRepository, clock)
	if err != nil {
		return err
	}
	documentAdmission, err := knowledgeapp.NewDocumentAdmissionService(documentService, objects)
	if err != nil {
		return err
	}
	destination, err := postgres.NewPrototypeMigrationDestination(cell, documentAdmission, knowledgeService, objects)
	if err != nil {
		return err
	}
	ledger, err := postgres.NewPrototypeMigrationLedger(cell, ids.RandomGenerator{})
	if err != nil {
		return err
	}
	importer, err := prototypemigration.NewImporter(ledger, destination, clock)
	if err != nil {
		return err
	}
	result, err := importer.Import(ctx, bundle)
	if err != nil {
		if errors.Is(err, prototypemigration.ErrReconciliation) {
			_, _ = fmt.Fprintf(stdout, "run=%s state=%s reconciliation=pending documents=%d chunks=%d evidence=%d claims=%d citations=%d\n", result.Run.ID, result.Run.State, result.Reconciliation.Documents, result.Reconciliation.Chunks, result.Reconciliation.Evidence, result.Reconciliation.Claims, result.Reconciliation.Citations)
			return errors.New("destination processing or reconciliation is incomplete; run the document worker and retry the same command")
		}
		return err
	}
	if result.Run.State != prototypemigration.RunReconciled {
		return prototypemigration.ErrReconciliation
	}
	certificate := reconciliationCertificate{
		Version: bundle.Manifest.Version, AccountID: string(bundle.Manifest.AccountID), SourceTenantID: bundle.Manifest.Source.TenantID,
		SourceCheckpoint: bundle.Manifest.Source.Checkpoint, ManifestSHA256: bundle.Manifest.ContentSHA256, RunID: result.Run.ID,
		ReconciliationSHA256: hex.EncodeToString(result.Reconciliation.ContentSHA256[:]), Documents: result.Reconciliation.Documents,
		DocumentObjects: result.Reconciliation.DocumentObjects, Chunks: result.Reconciliation.Chunks, Evidence: result.Reconciliation.Evidence,
		Claims: result.Reconciliation.Claims, Citations: result.Reconciliation.Citations, Unresolved: result.Reconciliation.Unresolved,
		CompletedAt: time.Now().UTC(), OwnerReviewRequired: true,
	}
	if err := writeCertificate(options.certificate, certificate); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "run=%s state=reconciled reconciliation_sha256=%s certificate=%s owner_review_required=true\n", result.Run.ID, certificate.ReconciliationSHA256, options.certificate)
	return err
}

func openPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	parsed, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("database URL is invalid")
	}
	parsed.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, parsed)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func configFromEnvironment(getenv func(string) string) (config, error) {
	value := config{
		globalDatabaseURL: strings.TrimSpace(getenv("SPYGLASS_PROTOTYPE_IMPORT_GLOBAL_DATABASE_URL")),
		cellDatabaseURL:   strings.TrimSpace(getenv("SPYGLASS_PROTOTYPE_IMPORT_CELL_DATABASE_URL")),
		objectEndpoint:    strings.TrimSpace(getenv("SPYGLASS_PROTOTYPE_IMPORT_OBJECT_ENDPOINT")),
		objectRegion:      strings.TrimSpace(getenv("SPYGLASS_PROTOTYPE_IMPORT_OBJECT_REGION")),
		objectBucket:      strings.TrimSpace(getenv("SPYGLASS_PROTOTYPE_IMPORT_OBJECT_BUCKET")),
		objectAccessKey:   getenv("SPYGLASS_PROTOTYPE_IMPORT_OBJECT_ACCESS_KEY"),
		objectSecretKey:   getenv("SPYGLASS_PROTOTYPE_IMPORT_OBJECT_SECRET_KEY"),
	}
	if value.globalDatabaseURL == "" || value.cellDatabaseURL == "" || value.objectEndpoint == "" || value.objectBucket == "" || value.objectAccessKey == "" || value.objectSecretKey == "" {
		return config{}, errors.New("prototype import database and object-store environment is incomplete")
	}
	var err error
	if value.objectSecure, err = parseRequiredBool(getenv("SPYGLASS_PROTOTYPE_IMPORT_OBJECT_SECURE"), "SPYGLASS_PROTOTYPE_IMPORT_OBJECT_SECURE"); err != nil {
		return config{}, err
	}
	if value.objectSSE, err = parseRequiredBool(getenv("SPYGLASS_PROTOTYPE_IMPORT_OBJECT_SSE"), "SPYGLASS_PROTOTYPE_IMPORT_OBJECT_SSE"); err != nil {
		return config{}, err
	}
	return value, nil
}

func parseRequiredBool(raw, name string) (bool, error) {
	if raw != "true" && raw != "false" {
		return false, fmt.Errorf("%s must be true or false", name)
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s is invalid", name)
	}
	return value, nil
}

type reconciliationCertificate struct {
	Version              string    `json:"version"`
	AccountID            string    `json:"account_id"`
	SourceTenantID       string    `json:"source_tenant_id"`
	SourceCheckpoint     string    `json:"source_checkpoint"`
	ManifestSHA256       string    `json:"manifest_sha256"`
	RunID                string    `json:"run_id"`
	ReconciliationSHA256 string    `json:"reconciliation_sha256"`
	Documents            uint64    `json:"documents"`
	DocumentObjects      uint64    `json:"document_objects"`
	Chunks               uint64    `json:"chunks"`
	Evidence             uint64    `json:"evidence"`
	Claims               uint64    `json:"claims"`
	Citations            uint64    `json:"citations"`
	Unresolved           uint64    `json:"unresolved"`
	CompletedAt          time.Time `json:"completed_at"`
	OwnerReviewRequired  bool      `json:"owner_review_required"`
}

func writeCertificate(path string, value reconciliationCertificate) error {
	absolute, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil || strings.TrimSpace(path) == "" || value.ReconciliationSHA256 == "" || !value.OwnerReviewRequired {
		return errors.New("reconciliation certificate is invalid")
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return fmt.Errorf("create certificate directory: %w", err)
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(absolute, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("reconciliation certificate already exists")
		}
		return fmt.Errorf("create reconciliation certificate: %w", err)
	}
	success := false
	defer func() {
		_ = file.Close()
		if !success {
			_ = os.Remove(absolute)
		}
	}()
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	success = true
	return nil
}

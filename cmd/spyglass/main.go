package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/stripe"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/accountapi"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/billingworker"
	catalogcommand "github.com/tinfoyle/spyglass-engine/internal/bootstrap/catalogadmin"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/development"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/entitlementworker"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/notificationworker"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	mode := ""
	if len(os.Args) > 1 {
		mode = os.Args[1]
	} else if os.Getenv("SPYGLASS_ENV") == "development" {
		mode = "development"
	}
	var err error
	switch mode {
	case "development":
		err = runDevelopment(ctx, logger)
	case "account-api":
		err = runAccountAPI(ctx, logger)
	case "billing-worker":
		err = runBillingWorker(ctx, logger)
	case "notification-worker":
		err = runNotificationWorker(ctx, logger)
	case "entitlement-worker":
		err = runEntitlementWorker(ctx, logger)
	case "catalog-admin":
		err = runCatalogAdmin(ctx, logger)
	case "migrate":
		err = runMigrate(ctx, logger)
	default:
		err = errors.New("usage: spyglass development | account-api | billing-worker | notification-worker | entitlement-worker | catalog-admin <action> | migrate")
	}
	if err != nil {
		logger.Error("Spyglass process stopped", "mode", mode, "error", err)
		os.Exit(1)
	}
}

func runCatalogAdmin(ctx context.Context, logger *slog.Logger) error {
	if len(os.Args) != 3 {
		return errors.New("usage: spyglass catalog-admin draft|map-price|request-review|approve|publish|retire")
	}
	databaseURL, err := requiredEnv("SPYGLASS_DATABASE_URL")
	if err != nil {
		return err
	}
	actor, err := requiredEnv("SPYGLASS_OPERATOR_ID")
	if err != nil {
		return err
	}
	reason, err := requiredEnv("SPYGLASS_OPERATOR_REASON")
	if err != nil {
		return err
	}
	maxConns, err := int32Env("SPYGLASS_MAX_DATABASE_CONNS", 2)
	if err != nil {
		return err
	}
	config := catalogcommand.Config{DatabaseURL: databaseURL, Action: os.Args[2], Actor: actor, Reason: reason, MaxDatabaseConns: maxConns}
	if config.Action == "draft" {
		filename, err := requiredEnv("SPYGLASS_CATALOG_FILE")
		if err != nil {
			return err
		}
		config.CatalogJSON, err = os.ReadFile(filename)
		if err != nil {
			return fmt.Errorf("read catalog file: %w", err)
		}
	} else {
		config.Version, err = uint64Env("SPYGLASS_CATALOG_VERSION")
		if err != nil {
			return err
		}
	}
	if config.Action == "map-price" {
		config.OfferCode, err = requiredEnv("SPYGLASS_CATALOG_OFFER_CODE")
		if err != nil {
			return err
		}
		config.StripeMode, err = requiredEnv("SPYGLASS_STRIPE_MODE")
		if err != nil {
			return err
		}
		config.PriceID, err = requiredEnv("SPYGLASS_STRIPE_PRICE_ID")
		if err != nil {
			return err
		}
	}
	if config.Action == "publish" && os.Getenv("SPYGLASS_CATALOG_EFFECTIVE_AT") != "" {
		config.EffectiveAt, err = time.Parse(time.RFC3339, os.Getenv("SPYGLASS_CATALOG_EFFECTIVE_AT"))
		if err != nil {
			return errors.New("SPYGLASS_CATALOG_EFFECTIVE_AT must be RFC3339")
		}
	}
	startup, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return catalogcommand.Run(startup, config, logger)
}

func runMigrate(ctx context.Context, logger *slog.Logger) error {
	databaseURL, err := requiredEnv("SPYGLASS_DATABASE_URL")
	if err != nil {
		return err
	}
	target := migrations.Target(os.Getenv("SPYGLASS_MIGRATION_TARGET"))
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("open migration database: %w", err)
	}
	defer pool.Close()
	result, err := migrations.Apply(ctx, pool, target)
	if err != nil {
		return err
	}
	logger.Info("Spyglass migrations complete", "target", target, "applied", len(result.Applied))
	return nil
}

func runDevelopment(ctx context.Context, logger *slog.Logger) error {
	if os.Getenv("SPYGLASS_ENV") != "development" {
		return errors.New("development mode requires SPYGLASS_ENV=development")
	}
	return serveHTTP(ctx, httpAddress(":8080"), development.Handler(logger), logger)
}

func runAccountAPI(ctx context.Context, logger *slog.Logger) error {
	config, err := productionConfig()
	if err != nil {
		return err
	}
	startup, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	server, err := accountapi.New(startup, accountapi.Config{DatabaseURL: config.databaseURL, StripeWebhookSecret: config.stripeWebhookSecret, StripeSecretKey: config.stripeSecretKey, StripeAPIVersion: config.stripeAPIVersion, StripeMode: config.stripeMode, MaxDatabaseConns: config.maxDatabaseConns, AppOrigin: config.appOrigin, PublicOrigin: config.publicOrigin, NotificationEncryptionKey: config.notificationEncryptionKey, NetworkActorKey: config.networkActorKey, TrustedProxyCIDRs: config.trustedProxyCIDRs, CatalogRefreshInterval: config.catalogRefreshInterval}, logger)
	if err != nil {
		return err
	}
	defer server.Close()
	return serveHTTP(ctx, httpAddress(":8080"), server.Handler, logger)
}

func runBillingWorker(ctx context.Context, logger *slog.Logger) error {
	databaseURL, err := requiredEnv("SPYGLASS_DATABASE_URL")
	if err != nil {
		return err
	}
	secretKey, err := requiredEnv("SPYGLASS_STRIPE_SECRET_KEY")
	if err != nil {
		return err
	}
	mode, err := requiredEnv("SPYGLASS_STRIPE_MODE")
	if err != nil {
		return err
	}
	maxConns, err := int32Env("SPYGLASS_MAX_DATABASE_CONNS", 5)
	if err != nil {
		return err
	}
	poll, err := durationEnv("SPYGLASS_BILLING_POLL_INTERVAL", time.Second)
	if err != nil {
		return err
	}
	startup, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	worker, err := billingworker.New(startup, billingworker.Config{DatabaseURL: databaseURL, StripeSecretKey: secretKey, StripeAPIVersion: envOr("SPYGLASS_STRIPE_API_VERSION", stripe.DefaultAPIVersion), StripeMode: mode, MaxDatabaseConns: maxConns, PollInterval: poll}, logger)
	if err != nil {
		return err
	}
	defer worker.Close()
	return serveWorker(ctx, "billing", envOr("SPYGLASS_HEALTH_ADDRESS", ":8081"), worker, logger)
}

func runNotificationWorker(ctx context.Context, logger *slog.Logger) error {
	databaseURL, err := requiredEnv("SPYGLASS_DATABASE_URL")
	if err != nil {
		return err
	}
	key, err := base64KeyEnv("SPYGLASS_NOTIFICATION_ENCRYPTION_KEY")
	if err != nil {
		return err
	}
	address, err := requiredEnv("SPYGLASS_SMTP_ADDRESS")
	if err != nil {
		return err
	}
	serverName, err := requiredEnv("SPYGLASS_SMTP_SERVER_NAME")
	if err != nil {
		return err
	}
	fromAddress, err := requiredEnv("SPYGLASS_SMTP_FROM_ADDRESS")
	if err != nil {
		return err
	}
	appOrigin, err := requiredEnv("SPYGLASS_APP_ORIGIN")
	if err != nil {
		return err
	}
	maxConns, err := int32Env("SPYGLASS_MAX_DATABASE_CONNS", 5)
	if err != nil {
		return err
	}
	poll, err := durationEnv("SPYGLASS_NOTIFICATION_POLL_INTERVAL", time.Second)
	if err != nil {
		return err
	}
	startup, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	worker, err := notificationworker.New(startup, notificationworker.Config{DatabaseURL: databaseURL, NotificationEncryptionKey: key, SMTPAddress: address, SMTPServerName: serverName, SMTPUsername: os.Getenv("SPYGLASS_SMTP_USERNAME"), SMTPPassword: os.Getenv("SPYGLASS_SMTP_PASSWORD"), SMTPFromAddress: fromAddress, SMTPFromName: envOr("SPYGLASS_SMTP_FROM_NAME", "Infinite Ocean"), AppOrigin: appOrigin, MaxDatabaseConns: maxConns, PollInterval: poll}, logger)
	if err != nil {
		return err
	}
	defer worker.Close()
	return serveWorker(ctx, "notification", envOr("SPYGLASS_HEALTH_ADDRESS", ":8081"), worker, logger)
}

func runEntitlementWorker(ctx context.Context, logger *slog.Logger) error {
	databaseURL, err := requiredEnv("SPYGLASS_DATABASE_URL")
	if err != nil {
		return err
	}
	maxConns, err := int32Env("SPYGLASS_MAX_DATABASE_CONNS", 5)
	if err != nil {
		return err
	}
	poll, err := durationEnv("SPYGLASS_ENTITLEMENT_POLL_INTERVAL", time.Second)
	if err != nil {
		return err
	}
	batch, err := int32Env("SPYGLASS_ENTITLEMENT_SEED_BATCH", 100)
	if err != nil || batch > 1000 {
		return errors.New("SPYGLASS_ENTITLEMENT_SEED_BATCH must be between 1 and 1000")
	}
	startup, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	worker, err := entitlementworker.New(startup, entitlementworker.Config{DatabaseURL: databaseURL, MaxDatabaseConns: maxConns, PollInterval: poll, SeedBatch: int(batch)}, logger)
	if err != nil {
		return err
	}
	defer worker.Close()
	return serveWorker(ctx, "entitlement", envOr("SPYGLASS_HEALTH_ADDRESS", ":8081"), worker, logger)
}

type runnableWorker interface {
	Run(context.Context) error
	Ready(context.Context) error
}

func serveWorker(ctx context.Context, name, healthAddress string, worker runnableWorker, logger *slog.Logger) error {
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	httpServer := newHTTPServer(healthAddress, workerHealth(worker))
	errorsChannel := make(chan error, 2)
	go func() { errorsChannel <- worker.Run(runCtx) }()
	go func() {
		logger.Info("Spyglass worker health listening", "worker", name, "address", httpServer.Addr)
		err := httpServer.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errorsChannel <- err
	}()
	select {
	case <-ctx.Done():
	case err := <-errorsChannel:
		if err != nil {
			return err
		}
	}
	stop()
	shutdown, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	return httpServer.Shutdown(shutdown)
}

type persistentConfig struct {
	databaseURL, stripeWebhookSecret, stripeSecretKey, stripeAPIVersion, stripeMode, appOrigin, publicOrigin string
	notificationEncryptionKey                                                                                []byte
	networkActorKey                                                                                          []byte
	trustedProxyCIDRs                                                                                        []string
	maxDatabaseConns                                                                                         int32
	catalogRefreshInterval                                                                                   time.Duration
}

func productionConfig() (persistentConfig, error) {
	var result persistentConfig
	var err error
	fields := []struct {
		name   string
		target *string
	}{{"SPYGLASS_DATABASE_URL", &result.databaseURL}, {"SPYGLASS_STRIPE_WEBHOOK_SECRET", &result.stripeWebhookSecret}, {"SPYGLASS_STRIPE_SECRET_KEY", &result.stripeSecretKey}, {"SPYGLASS_STRIPE_MODE", &result.stripeMode}, {"SPYGLASS_APP_ORIGIN", &result.appOrigin}, {"SPYGLASS_PUBLIC_ORIGIN", &result.publicOrigin}}
	for _, field := range fields {
		*field.target, err = requiredEnv(field.name)
		if err != nil {
			return persistentConfig{}, err
		}
	}
	result.stripeAPIVersion = envOr("SPYGLASS_STRIPE_API_VERSION", stripe.DefaultAPIVersion)
	result.notificationEncryptionKey, err = base64KeyEnv("SPYGLASS_NOTIFICATION_ENCRYPTION_KEY")
	if err != nil {
		return persistentConfig{}, err
	}
	result.networkActorKey, err = base64KeyEnv("SPYGLASS_NETWORK_ACTOR_KEY")
	if err != nil {
		return persistentConfig{}, err
	}
	result.trustedProxyCIDRs = csvEnv("SPYGLASS_TRUSTED_PROXY_CIDRS")
	result.maxDatabaseConns, err = int32Env("SPYGLASS_MAX_DATABASE_CONNS", 10)
	if err != nil {
		return persistentConfig{}, err
	}
	result.catalogRefreshInterval, err = durationEnv("SPYGLASS_CATALOG_REFRESH_INTERVAL", 5*time.Second)
	return result, err
}

func csvEnv(name string) []string {
	var result []string
	for _, value := range strings.Split(os.Getenv(name), ",") {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func base64KeyEnv(name string) ([]byte, error) {
	raw, err := requiredEnv(name)
	if err != nil {
		return nil, err
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(decoded) != 32 {
		return nil, fmt.Errorf("%s must be standard base64 encoding of exactly 32 bytes", name)
	}
	return decoded, nil
}

func serveHTTP(ctx context.Context, address string, handler http.Handler, logger *slog.Logger) error {
	server := newHTTPServer(address, handler)
	errorsChannel := make(chan error, 1)
	go func() {
		logger.Info("Spyglass HTTP listening", "address", address)
		err := server.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errorsChannel <- err
	}()
	select {
	case <-ctx.Done():
	case err := <-errorsChannel:
		return err
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return server.Shutdown(shutdown)
}

func newHTTPServer(address string, handler http.Handler) *http.Server {
	return &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
}

type readiness interface{ Ready(context.Context) error }

func workerHealth(worker readiness) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"alive"}`))
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := worker.Ready(ctx); err != nil {
			http.Error(w, `{"status":"unavailable"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})
	return mux
}

func requiredEnv(name string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}
func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
func int32Env(name string, fallback int32) (int32, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return int32(value), nil
}
func uint64Env(name string) (uint64, error) {
	raw, err := requiredEnv(name)
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return value, nil
}
func durationEnv(name string, fallback time.Duration) (time.Duration, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return value, nil
}
func httpAddress(fallback string) string { return envOr("SPYGLASS_HTTP_ADDRESS", fallback) }

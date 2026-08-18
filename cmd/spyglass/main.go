package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/smtp"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/stripe"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/accountapi"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/billingworker"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/development"
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
	case "migrate":
		err = runMigrate(ctx, logger)
	default:
		err = errors.New("usage: spyglass development | account-api | billing-worker | migrate")
	}
	if err != nil {
		logger.Error("Spyglass process stopped", "mode", mode, "error", err)
		os.Exit(1)
	}
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
	sender, err := smtp.New(smtp.Config{Address: config.smtpAddress, ServerName: config.smtpServerName, Username: config.smtpUsername, Password: config.smtpPassword, FromAddress: config.smtpFromAddress, FromName: config.smtpFromName, AppOrigin: config.appOrigin})
	if err != nil {
		return err
	}
	startup, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	server, err := accountapi.New(startup, accountapi.Config{DatabaseURL: config.databaseURL, StripeWebhookSecret: config.stripeWebhookSecret, StripeSecretKey: config.stripeSecretKey, StripeAPIVersion: config.stripeAPIVersion, StripeMode: config.stripeMode, MaxDatabaseConns: config.maxDatabaseConns, AppOrigin: config.appOrigin, PublicOrigin: config.publicOrigin}, sender, logger)
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
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	health := workerHealth(worker)
	httpServer := newHTTPServer(envOr("SPYGLASS_HEALTH_ADDRESS", ":8081"), health)
	errorsChannel := make(chan error, 2)
	go func() { errorsChannel <- worker.Run(runCtx) }()
	go func() {
		logger.Info("Spyglass billing worker health listening", "address", httpServer.Addr)
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
	databaseURL, stripeWebhookSecret, stripeSecretKey, stripeAPIVersion, stripeMode, appOrigin, publicOrigin, smtpAddress, smtpServerName, smtpUsername, smtpPassword, smtpFromAddress, smtpFromName string
	maxDatabaseConns                                                                                                                                                                                 int32
}

func productionConfig() (persistentConfig, error) {
	var result persistentConfig
	var err error
	fields := []struct {
		name   string
		target *string
	}{{"SPYGLASS_DATABASE_URL", &result.databaseURL}, {"SPYGLASS_STRIPE_WEBHOOK_SECRET", &result.stripeWebhookSecret}, {"SPYGLASS_STRIPE_SECRET_KEY", &result.stripeSecretKey}, {"SPYGLASS_STRIPE_MODE", &result.stripeMode}, {"SPYGLASS_APP_ORIGIN", &result.appOrigin}, {"SPYGLASS_PUBLIC_ORIGIN", &result.publicOrigin}, {"SPYGLASS_SMTP_ADDRESS", &result.smtpAddress}, {"SPYGLASS_SMTP_SERVER_NAME", &result.smtpServerName}, {"SPYGLASS_SMTP_FROM_ADDRESS", &result.smtpFromAddress}}
	for _, field := range fields {
		*field.target, err = requiredEnv(field.name)
		if err != nil {
			return persistentConfig{}, err
		}
	}
	result.stripeAPIVersion = envOr("SPYGLASS_STRIPE_API_VERSION", stripe.DefaultAPIVersion)
	result.smtpUsername = os.Getenv("SPYGLASS_SMTP_USERNAME")
	result.smtpPassword = os.Getenv("SPYGLASS_SMTP_PASSWORD")
	result.smtpFromName = envOr("SPYGLASS_SMTP_FROM_NAME", "Infinite Ocean")
	result.maxDatabaseConns, err = int32Env("SPYGLASS_MAX_DATABASE_CONNS", 10)
	return result, err
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

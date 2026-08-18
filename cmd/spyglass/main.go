package main

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
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
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/application/routecanary"
	"github.com/tinfoyle/spyglass-engine/internal/application/routeretention"
	"github.com/tinfoyle/spyglass-engine/internal/application/workreconciliation"
	workreleaseapp "github.com/tinfoyle/spyglass-engine/internal/application/workreleaseadmin"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/accountapi"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/accountlifecycleworker"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/admissionapi"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/appapi"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/approuter"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/billingworker"
	catalogcommand "github.com/tinfoyle/spyglass-engine/internal/bootstrap/catalogadmin"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/development"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/entitlementworker"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/notificationworker"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/routereceiptworker"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/workreconciler"
	workreleasecommand "github.com/tinfoyle/spyglass-engine/internal/bootstrap/workreleaseadmin"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/workloadidentity"
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
	case "app-router":
		err = runAppRouter(ctx, logger)
	case "app-api":
		err = runAppAPI(ctx, logger)
	case "admission-api":
		err = runAdmissionAPI(ctx, logger)
	case "billing-worker":
		err = runBillingWorker(ctx, logger)
	case "notification-worker":
		err = runNotificationWorker(ctx, logger)
	case "entitlement-worker":
		err = runEntitlementWorker(ctx, logger)
	case "account-lifecycle-worker":
		err = runAccountLifecycleWorker(ctx, logger)
	case "work-reconciler":
		err = runWorkReconciler(ctx, logger)
	case "route-receipt-worker":
		err = runRouteReceiptWorker(ctx, logger)
	case "route-canary":
		err = runRouteCanary(ctx, logger)
	case "work-release-admin":
		err = runWorkReleaseAdmin(ctx, logger)
	case "catalog-admin":
		err = runCatalogAdmin(ctx, logger)
	case "migrate":
		err = runMigrate(ctx, logger)
	default:
		err = errors.New("usage: spyglass development | account-api | app-router | app-api | admission-api | billing-worker | notification-worker | entitlement-worker | account-lifecycle-worker | work-reconciler | route-receipt-worker | route-canary | work-release-admin <action> | catalog-admin <action> | migrate")
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

func runWorkReleaseAdmin(ctx context.Context, logger *slog.Logger) error {
	if len(os.Args) != 3 || (os.Args[2] != "inspect" && os.Args[2] != "requeue") {
		return errors.New("usage: spyglass work-release-admin inspect|requeue")
	}
	databaseURL, err := requiredEnv("SPYGLASS_CELL_DATABASE_URL")
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
	environment, err := requiredEnv("SPYGLASS_ENVIRONMENT")
	if err != nil {
		return err
	}
	confirmation, err := requiredEnv("SPYGLASS_CONFIRM_ENVIRONMENT")
	if err != nil {
		return err
	}
	maxConns, err := int32Env("SPYGLASS_MAX_DATABASE_CONNS", 2)
	if err != nil {
		return err
	}
	config := workreleasecommand.Config{DatabaseURL: databaseURL, Action: os.Args[2], Actor: actor, Reason: reason, Environment: environment, ConfirmEnvironment: confirmation, InspectLimit: workreleaseapp.DefaultInspectLimit, MaxDatabaseConns: maxConns}
	if config.Action == "inspect" {
		limit, err := int64Env("SPYGLASS_WORK_RELEASE_INSPECT_LIMIT", workreleaseapp.DefaultInspectLimit)
		if err != nil {
			return err
		}
		config.InspectLimit = int(limit)
	}
	if config.Action == "requeue" {
		accountID, err := requiredEnv("SPYGLASS_WORK_ACCOUNT_ID")
		if err != nil {
			return err
		}
		itemID, err := requiredEnv("SPYGLASS_WORK_ITEM_ID")
		if err != nil {
			return err
		}
		reservationID, err := requiredEnv("SPYGLASS_WORK_RESERVATION_ID")
		if err != nil {
			return err
		}
		config.Target = workreleaseapp.Target{AccountID: ids.AccountID(accountID), WorkItemID: ids.WorkItemID(itemID), ReservationID: reservationID}
	}
	startup, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return workreleasecommand.Run(startup, config, logger)
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
	server, err := accountapi.New(startup, accountapi.Config{DatabaseURL: config.databaseURL, StripeWebhookSecret: config.stripeWebhookSecret, StripeSecretKey: config.stripeSecretKey, StripeAPIVersion: config.stripeAPIVersion, StripeMode: config.stripeMode, MaxDatabaseConns: config.maxDatabaseConns, AppOrigin: config.appOrigin, PublicOrigin: config.publicOrigin, NotificationEncryptionKey: config.notificationEncryptionKey, NetworkActorKey: config.networkActorKey, PasskeyEncryptionKey: config.passkeyEncryptionKey, PasskeyRPID: config.passkeyRPID, TrustedProxyCIDRs: config.trustedProxyCIDRs, CatalogRefreshInterval: config.catalogRefreshInterval}, logger)
	if err != nil {
		return err
	}
	defer server.Close()
	return serveHTTP(ctx, httpAddress(":8080"), server.Handler, logger)
}

func runAppRouter(ctx context.Context, logger *slog.Logger) error {
	developmentMode := os.Getenv("SPYGLASS_ENV") == "development"
	databaseURL, err := requiredEnv("SPYGLASS_DATABASE_URL")
	if err != nil {
		return err
	}
	issuer, err := requiredEnv("SPYGLASS_ROUTE_ISSUER")
	if err != nil {
		return err
	}
	keyID, err := requiredEnv("SPYGLASS_ROUTE_SIGNING_KEY_ID")
	if err != nil {
		return err
	}
	key, err := base64KeyEnv("SPYGLASS_ROUTE_SIGNING_KEY")
	if err != nil {
		return err
	}
	appOrigin, err := requiredEnv("SPYGLASS_APP_ORIGIN")
	if err != nil {
		return err
	}
	maxConns, err := int32Env("SPYGLASS_MAX_DATABASE_CONNS", 10)
	if err != nil {
		return err
	}
	lifetime, err := durationEnv("SPYGLASS_ROUTE_CONTEXT_TTL", 20*time.Second)
	if err != nil {
		return err
	}
	directoryTTL, err := durationEnv("SPYGLASS_DIRECTORY_CACHE_TTL", 30*time.Second)
	if err != nil {
		return err
	}
	directoryCapacityValue, err := int32Env("SPYGLASS_DIRECTORY_CACHE_CAPACITY", 10000)
	if err != nil {
		return err
	}
	directoryCapacity := int(directoryCapacityValue)
	var cellTransport http.RoundTripper
	if !developmentMode {
		cellTransport, err = workloadidentity.NewClientTransport(workloadTLSFilesEnv())
		if err != nil {
			return err
		}
	}
	startup, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	server, err := approuter.New(startup, approuter.Config{DatabaseURL: databaseURL, MaxDatabaseConns: maxConns, RouteIssuer: issuer, RouteSigningKeyID: keyID, RouteSigningKey: key, RouteLifetime: lifetime, DirectoryCacheTTL: directoryTTL, DirectoryCapacity: directoryCapacity, CellTransport: cellTransport, SessionCookieName: os.Getenv("SPYGLASS_SESSION_COOKIE_NAME"), SecureCookies: true, TrustedOrigins: []string{appOrigin}, AllowHTTPCells: developmentMode}, logger, registration.SystemClock{})
	if err != nil {
		return err
	}
	defer server.Close()
	return serveHTTP(ctx, httpAddress(":8080"), server.Handler, logger)
}

func runAppAPI(ctx context.Context, logger *slog.Logger) error {
	developmentMode := os.Getenv("SPYGLASS_ENV") == "development"
	databaseURL, err := requiredEnv("SPYGLASS_DATABASE_URL")
	if err != nil {
		return err
	}
	cellID, err := requiredEnv("SPYGLASS_CELL_ID")
	if err != nil {
		return err
	}
	issuer, err := requiredEnv("SPYGLASS_ROUTE_ISSUER")
	if err != nil {
		return err
	}
	keys, err := routeVerifyKeysEnv("SPYGLASS_ROUTE_VERIFY_KEYS")
	if err != nil {
		return err
	}
	admissionOrigin, err := requiredEnv("SPYGLASS_WORK_ADMISSION_ORIGIN")
	if err != nil {
		return err
	}
	maxConns, err := int32Env("SPYGLASS_MAX_DATABASE_CONNS", 10)
	if err != nil {
		return err
	}
	maxBody, err := int64Env("SPYGLASS_MAX_REQUEST_BODY_BYTES", 1<<20)
	if err != nil || maxBody > 16<<20 {
		return errors.New("SPYGLASS_MAX_REQUEST_BODY_BYTES must be between 1 and 16777216")
	}
	var admissionTransport http.RoundTripper
	var serverTLS *tls.Config
	if !developmentMode {
		files := workloadTLSFilesEnv()
		admissionTransport, err = workloadidentity.NewClientTransport(files)
		if err != nil {
			return err
		}
		serverTLS, err = workloadidentity.NewServerConfig(files)
		if err != nil {
			return err
		}
	}
	startup, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	server, err := appapi.New(startup, appapi.Config{DatabaseURL: databaseURL, CellID: ids.CellID(cellID), RouteIssuer: issuer, RouteVerifyKeys: keys, MaxDatabaseConns: maxConns, MaxRequestBody: maxBody, AdmissionOrigin: admissionOrigin, AdmissionTransport: admissionTransport, AllowHTTPAdmission: developmentMode}, logger, registration.SystemClock{})
	if err != nil {
		return err
	}
	defer server.Close()
	if developmentMode {
		return serveHTTP(ctx, httpAddress(":8080"), server.Handler, logger)
	}
	secured, err := workloadidentity.RequireClientIdentity(server.Handler, csvEnv("SPYGLASS_WORKLOAD_CLIENT_IDENTITIES"), logger)
	if err != nil {
		return err
	}
	return serveHTTPS(ctx, httpAddress(":8443"), secured, serverTLS, logger)
}

func runAdmissionAPI(ctx context.Context, logger *slog.Logger) error {
	developmentMode := os.Getenv("SPYGLASS_ENV") == "development"
	databaseURL, err := requiredEnv("SPYGLASS_DATABASE_URL")
	if err != nil {
		return err
	}
	issuer, err := requiredEnv("SPYGLASS_ROUTE_ISSUER")
	if err != nil {
		return err
	}
	keys, err := routeVerifyKeysEnv("SPYGLASS_ROUTE_VERIFY_KEYS")
	if err != nil {
		return err
	}
	rawCells := csvEnv("SPYGLASS_ADMISSION_CELL_IDS")
	if len(rawCells) == 0 {
		return errors.New("SPYGLASS_ADMISSION_CELL_IDS is required")
	}
	cells := make([]ids.CellID, len(rawCells))
	for index, value := range rawCells {
		cells[index] = ids.CellID(value)
	}
	maxConns, err := int32Env("SPYGLASS_MAX_DATABASE_CONNS", 10)
	if err != nil {
		return err
	}
	maxBody, err := int64Env("SPYGLASS_ADMISSION_MAX_REQUEST_BODY_BYTES", 64<<10)
	if err != nil || maxBody > 1<<20 {
		return errors.New("SPYGLASS_ADMISSION_MAX_REQUEST_BODY_BYTES must be between 1 and 1048576")
	}
	var serverTLS *tls.Config
	if !developmentMode {
		serverTLS, err = workloadidentity.NewServerConfig(workloadTLSFilesEnv())
		if err != nil {
			return err
		}
	}
	startup, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	server, err := admissionapi.New(startup, admissionapi.Config{DatabaseURL: databaseURL, RouteIssuer: issuer, RouteVerifyKeys: keys, CellIDs: cells, MaxDatabaseConns: maxConns, MaxRequestBody: maxBody}, logger, registration.SystemClock{})
	if err != nil {
		return err
	}
	defer server.Close()
	if developmentMode {
		return serveHTTP(ctx, httpAddress(":8080"), server.Handler, logger)
	}
	secured, err := workloadidentity.RequireClientIdentity(server.Handler, csvEnv("SPYGLASS_WORKLOAD_CLIENT_IDENTITIES"), logger)
	if err != nil {
		return err
	}
	return serveHTTPS(ctx, httpAddress(":8443"), secured, serverTLS, logger)
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

func runAccountLifecycleWorker(ctx context.Context, logger *slog.Logger) error {
	databaseURL, err := requiredEnv("SPYGLASS_DATABASE_URL")
	if err != nil {
		return err
	}
	maxConns, err := int32Env("SPYGLASS_MAX_DATABASE_CONNS", 5)
	if err != nil {
		return err
	}
	poll, err := durationEnv("SPYGLASS_ACCOUNT_CLOSURE_POLL_INTERVAL", time.Second)
	if err != nil {
		return err
	}
	lease, err := durationEnv("SPYGLASS_ACCOUNT_CLOSURE_LEASE", 2*time.Minute)
	if err != nil || lease > 30*time.Minute {
		return errors.New("SPYGLASS_ACCOUNT_CLOSURE_LEASE must be at most 30m")
	}
	retention, err := durationEnv("SPYGLASS_ACCOUNT_CLOSURE_RETENTION", 30*24*time.Hour)
	if err != nil || retention < 7*24*time.Hour || retention > 365*24*time.Hour {
		return errors.New("SPYGLASS_ACCOUNT_CLOSURE_RETENTION must be between 168h and 8760h")
	}
	blockedRetry, err := durationEnv("SPYGLASS_ACCOUNT_CLOSURE_BLOCKED_RETRY", 24*time.Hour)
	if err != nil || blockedRetry < time.Hour || blockedRetry > 7*24*time.Hour {
		return errors.New("SPYGLASS_ACCOUNT_CLOSURE_BLOCKED_RETRY must be between 1h and 168h")
	}
	startup, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	worker, err := accountlifecycleworker.New(startup, accountlifecycleworker.Config{DatabaseURL: databaseURL, MaxDatabaseConns: maxConns, PollInterval: poll, Lease: lease, Retention: retention, BlockedRetry: blockedRetry}, logger)
	if err != nil {
		return err
	}
	defer worker.Close()
	return serveWorker(ctx, "account-lifecycle", envOr("SPYGLASS_HEALTH_ADDRESS", ":8081"), worker, logger)
}

func runWorkReconciler(ctx context.Context, logger *slog.Logger) error {
	cellDatabaseURL, err := requiredEnv("SPYGLASS_CELL_DATABASE_URL")
	if err != nil {
		return err
	}
	globalDatabaseURL, err := requiredEnv("SPYGLASS_GLOBAL_DATABASE_URL")
	if err != nil {
		return err
	}
	cellMaxConns, err := int32Env("SPYGLASS_CELL_MAX_DATABASE_CONNS", 4)
	if err != nil {
		return err
	}
	globalMaxConns, err := int32Env("SPYGLASS_GLOBAL_MAX_DATABASE_CONNS", 4)
	if err != nil {
		return err
	}
	poll, err := durationEnv("SPYGLASS_WORK_RECONCILE_POLL_INTERVAL", time.Second)
	if err != nil {
		return err
	}
	lease, err := durationEnv("SPYGLASS_WORK_RECONCILE_LEASE", workreconciliation.DefaultLease)
	if err != nil || lease < time.Second || lease > 30*time.Minute {
		return errors.New("SPYGLASS_WORK_RECONCILE_LEASE must be between 1s and 30m")
	}
	maxAttempts, err := int32Env("SPYGLASS_WORK_RECONCILE_MAX_ATTEMPTS", workreconciliation.DefaultMaxAttempts)
	if err != nil || maxAttempts > 100 {
		return errors.New("SPYGLASS_WORK_RECONCILE_MAX_ATTEMPTS must be between 1 and 100")
	}
	cleanupInterval, err := durationEnv("SPYGLASS_WORK_RELEASE_CLEANUP_INTERVAL", time.Hour)
	if err != nil || cleanupInterval < time.Minute || cleanupInterval > 24*time.Hour {
		return errors.New("SPYGLASS_WORK_RELEASE_CLEANUP_INTERVAL must be between 1m and 24h")
	}
	completedRetention, err := durationEnv("SPYGLASS_WORK_RELEASE_COMPLETED_RETENTION", workreconciliation.DefaultRetention)
	if err != nil || completedRetention < 24*time.Hour || completedRetention > 365*24*time.Hour {
		return errors.New("SPYGLASS_WORK_RELEASE_COMPLETED_RETENTION must be between 24h and 8760h")
	}
	pruneBatch, err := int32Env("SPYGLASS_WORK_RELEASE_PRUNE_BATCH", workreconciliation.DefaultPruneBatch)
	if err != nil || pruneBatch > workreconciliation.MaximumPruneBatch {
		return errors.New("SPYGLASS_WORK_RELEASE_PRUNE_BATCH must be between 1 and 1000")
	}
	startup, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	worker, err := workreconciler.New(startup, workreconciler.Config{CellDatabaseURL: cellDatabaseURL, GlobalDatabaseURL: globalDatabaseURL, CellMaxDatabaseConns: cellMaxConns, GlobalMaxDatabaseConns: globalMaxConns, PollInterval: poll, Lease: lease, MaxAttempts: int(maxAttempts), CleanupInterval: cleanupInterval, CompletedRetention: completedRetention, PruneBatch: int(pruneBatch)}, logger)
	if err != nil {
		return err
	}
	defer worker.Close()
	return serveWorker(ctx, "work-reconciler", envOr("SPYGLASS_HEALTH_ADDRESS", ":8081"), worker, logger)
}

func runRouteReceiptWorker(ctx context.Context, logger *slog.Logger) error {
	databaseURL, err := requiredEnv("SPYGLASS_DATABASE_URL")
	if err != nil {
		return err
	}
	maxConns, err := int32Env("SPYGLASS_MAX_DATABASE_CONNS", 5)
	if err != nil {
		return err
	}
	poll, err := durationEnv("SPYGLASS_ROUTE_RECEIPT_POLL_INTERVAL", time.Second)
	if err != nil || poll < 100*time.Millisecond || poll > time.Minute {
		return errors.New("SPYGLASS_ROUTE_RECEIPT_POLL_INTERVAL must be between 100ms and 1m")
	}
	lease, err := durationEnv("SPYGLASS_ROUTE_RECEIPT_LEASE", routeretention.DefaultLease)
	if err != nil {
		return err
	}
	retention, err := durationEnv("SPYGLASS_ROUTE_RECEIPT_RETENTION", routeretention.DefaultRetention)
	if err != nil {
		return err
	}
	batch, err := int32Env("SPYGLASS_ROUTE_RECEIPT_PRUNE_BATCH", routeretention.DefaultPruneBatch)
	if err != nil {
		return err
	}
	if err := routeretention.ValidateBounds(lease, retention, int(batch)); err != nil {
		return err
	}
	startup, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	worker, err := routereceiptworker.New(startup, routereceiptworker.Config{DatabaseURL: databaseURL, MaxDatabaseConns: maxConns, PollInterval: poll, Lease: lease, Retention: retention, PruneBatch: int(batch)}, logger)
	if err != nil {
		return err
	}
	defer worker.Close()
	return serveWorker(ctx, "route-receipt", envOr("SPYGLASS_HEALTH_ADDRESS", ":8081"), worker, logger)
}

func runRouteCanary(ctx context.Context, logger *slog.Logger) error {
	target, err := requiredEnv("SPYGLASS_ROUTE_CANARY_TARGET")
	if err != nil {
		return err
	}
	if target != "cell" && target != "admission" {
		return errors.New("SPYGLASS_ROUTE_CANARY_TARGET must be cell or admission")
	}
	origin, err := requiredEnv("SPYGLASS_ROUTE_CANARY_ORIGIN")
	if err != nil {
		return err
	}
	cellID, err := requiredEnv("SPYGLASS_CELL_ID")
	if err != nil {
		return err
	}
	accountID, err := requiredEnv("SPYGLASS_ROUTE_CANARY_ACCOUNT_ID")
	if err != nil {
		return err
	}
	generation, err := uint64Env("SPYGLASS_ROUTE_CANARY_PLACEMENT_GENERATION")
	if err != nil {
		return err
	}
	entitlementVersion, err := uint64Env("SPYGLASS_ROUTE_CANARY_ENTITLEMENT_VERSION")
	if err != nil {
		return err
	}
	issuer, err := requiredEnv("SPYGLASS_ROUTE_ISSUER")
	if err != nil {
		return err
	}
	keyID, err := requiredEnv("SPYGLASS_ROUTE_SIGNING_KEY_ID")
	if err != nil {
		return err
	}
	key, err := base64KeyEnv("SPYGLASS_ROUTE_SIGNING_KEY")
	if err != nil {
		return err
	}
	timeout, err := durationEnv("SPYGLASS_ROUTE_CANARY_TIMEOUT", routecanary.DefaultTimeout)
	if err != nil || timeout < time.Second || timeout > 30*time.Second {
		return errors.New("SPYGLASS_ROUTE_CANARY_TIMEOUT must be between 1s and 30s")
	}
	transport, err := workloadidentity.NewClientTransport(workloadTLSFilesEnv())
	if err != nil {
		return err
	}
	defer transport.CloseIdleConnections()
	probeContext, cancel := context.WithTimeout(ctx, timeout+time.Second)
	defer cancel()
	config := routecanary.Config{
		Origin: origin, CellID: ids.CellID(cellID), AccountID: ids.AccountID(accountID),
		PlacementGeneration: generation, EntitlementVersion: entitlementVersion,
		Issuer: issuer, KeyID: keyID, SigningKey: key, Timeout: timeout,
		Transport: transport, Clock: registration.SystemClock{}, IDs: ids.RandomGenerator{},
	}
	var result routecanary.Result
	if target == "cell" {
		result, err = routecanary.Probe(probeContext, config)
	} else {
		result, err = routecanary.ProbeAdmission(probeContext, config)
	}
	if err != nil {
		return err
	}
	logger.Info("Route rotation canary verified", "target", target, "cell_id", result.CellID, "key_id", result.KeyID, "placement_generation", result.PlacementGeneration)
	return nil
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
	databaseURL, stripeWebhookSecret, stripeSecretKey, stripeAPIVersion, stripeMode, appOrigin, publicOrigin, passkeyRPID string
	notificationEncryptionKey                                                                                             []byte
	networkActorKey                                                                                                       []byte
	passkeyEncryptionKey                                                                                                  []byte
	trustedProxyCIDRs                                                                                                     []string
	maxDatabaseConns                                                                                                      int32
	catalogRefreshInterval                                                                                                time.Duration
}

func productionConfig() (persistentConfig, error) {
	var result persistentConfig
	var err error
	fields := []struct {
		name   string
		target *string
	}{{"SPYGLASS_DATABASE_URL", &result.databaseURL}, {"SPYGLASS_STRIPE_WEBHOOK_SECRET", &result.stripeWebhookSecret}, {"SPYGLASS_STRIPE_SECRET_KEY", &result.stripeSecretKey}, {"SPYGLASS_STRIPE_MODE", &result.stripeMode}, {"SPYGLASS_APP_ORIGIN", &result.appOrigin}, {"SPYGLASS_PUBLIC_ORIGIN", &result.publicOrigin}, {"SPYGLASS_PASSKEY_RP_ID", &result.passkeyRPID}}
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
	result.passkeyEncryptionKey, err = base64KeyEnv("SPYGLASS_PASSKEY_ENCRYPTION_KEY")
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

func serveHTTPS(ctx context.Context, address string, handler http.Handler, config *tls.Config, logger *slog.Logger) error {
	if config == nil {
		return errors.New("workload TLS server configuration is required")
	}
	server := newHTTPServer(address, handler)
	server.TLSConfig = config
	errorsChannel := make(chan error, 1)
	go func() {
		logger.Info("Spyglass HTTPS listening", "address", address)
		err := server.ListenAndServeTLS("", "")
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

type statusReporter interface {
	Status(context.Context) (any, error)
}

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
	mux.HandleFunc("GET /health/status", func(w http.ResponseWriter, r *http.Request) {
		reporter, ok := worker.(statusReporter)
		if !ok {
			http.NotFound(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		status, err := reporter.Status(ctx)
		if err != nil {
			http.Error(w, `{"status":"unavailable"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(status)
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
func int64Env(name string, fallback int64) (int64, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return value, nil
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

func workloadTLSFilesEnv() workloadidentity.Files {
	return workloadidentity.Files{Certificate: os.Getenv("SPYGLASS_WORKLOAD_CERT_FILE"), PrivateKey: os.Getenv("SPYGLASS_WORKLOAD_KEY_FILE"), TrustBundle: os.Getenv("SPYGLASS_WORKLOAD_CA_FILE")}
}

func routeVerifyKeysEnv(name string) (map[string][]byte, error) {
	values, err := keyValueEnv(name)
	if err != nil {
		return nil, err
	}
	result := make(map[string][]byte, len(values))
	for keyID, encoded := range values {
		value, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(value) != 32 {
			return nil, fmt.Errorf("%s key %q must be standard base64 encoding of exactly 32 bytes", name, keyID)
		}
		result[keyID] = value
	}
	return result, nil
}

func keyValueEnv(name string) (map[string]string, error) {
	raw, err := requiredEnv(name)
	if err != nil {
		return nil, err
	}
	result := map[string]string{}
	for _, entry := range strings.Split(raw, ",") {
		key, value, found := strings.Cut(entry, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !found || key == "" || value == "" || strings.ContainsAny(key, " \t\r\n") {
			return nil, fmt.Errorf("%s must contain comma-separated key=value entries", name)
		}
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("%s contains duplicate key %q", name, key)
		}
		result[key] = value
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("%s must not be empty", name)
	}
	return result, nil
}

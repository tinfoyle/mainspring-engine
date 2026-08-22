package main

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/dockerengine"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/dockerlauncherhttp"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/kubernetes"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/runnerbrokerhttp"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/stripe"
	"github.com/tinfoyle/spyglass-engine/internal/application/accounterasure"
	"github.com/tinfoyle/spyglass-engine/internal/application/agentdispatch"
	"github.com/tinfoyle/spyglass-engine/internal/application/agentprojection"
	agentqueueapp "github.com/tinfoyle/spyglass-engine/internal/application/agentqueueadmin"
	baselinemaintenanceapp "github.com/tinfoyle/spyglass-engine/internal/application/baselinemaintenance"
	"github.com/tinfoyle/spyglass-engine/internal/application/identitymaintenance"
	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/application/modelgateway"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/application/routecanary"
	"github.com/tinfoyle/spyglass-engine/internal/application/routeretention"
	"github.com/tinfoyle/spyglass-engine/internal/application/runneragents"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercontrol"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnerexecution"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnerwork"
	"github.com/tinfoyle/spyglass-engine/internal/application/workreconciliation"
	workreleaseapp "github.com/tinfoyle/spyglass-engine/internal/application/workreleaseadmin"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/accountapi"
	accounterasurecommand "github.com/tinfoyle/spyglass-engine/internal/bootstrap/accounterasureadmin"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/accountlifecycleworker"
	accountmovecommand "github.com/tinfoyle/spyglass-engine/internal/bootstrap/accountmoveadmin"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/admissionapi"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/agentdispatchworker"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/agentprojectionworker"
	agentqueuecommand "github.com/tinfoyle/spyglass-engine/internal/bootstrap/agentqueueadmin"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/appapi"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/approuter"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/baselinemaintenanceworker"
	billingcommand "github.com/tinfoyle/spyglass-engine/internal/bootstrap/billingadmin"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/billingworker"
	catalogcommand "github.com/tinfoyle/spyglass-engine/internal/bootstrap/catalogadmin"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/development"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/entitlementworker"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/identitymaintenanceworker"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/knowledgedocumentworker"
	modelgatewaybootstrap "github.com/tinfoyle/spyglass-engine/internal/bootstrap/modelgatewayapi"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/notificationworker"
	passkeycommand "github.com/tinfoyle/spyglass-engine/internal/bootstrap/passkeyadmin"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/routereceiptworker"
	runnerbrokerbootstrap "github.com/tinfoyle/spyglass-engine/internal/bootstrap/runnerbrokerapi"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/runnercontroller"
	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/workreconciler"
	workreleasecommand "github.com/tinfoyle/spyglass-engine/internal/bootstrap/workreleaseadmin"
	"github.com/tinfoyle/spyglass-engine/internal/platform/buildinfo"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/observability"
	"github.com/tinfoyle/spyglass-engine/internal/platform/operatorauth"
	"github.com/tinfoyle/spyglass-engine/internal/platform/restoregate"
	"github.com/tinfoyle/spyglass-engine/internal/platform/workloadidentity"
	"github.com/tinfoyle/spyglass-engine/internal/transport/dockerlauncherapi"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

const modelGatewayWriteTimeout = 5*time.Minute + 10*time.Second

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if len(os.Args) == 2 && os.Args[1] == "version" {
		if err := json.NewEncoder(os.Stdout).Encode(buildinfo.Current()); err != nil {
			logger.Error("Spyglass version output failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if err := runHealthcheck(os.Args[2:]); err != nil {
			logger.Error("Spyglass healthcheck failed", "error", err)
			os.Exit(1)
		}
		return
	}
	release := buildinfo.Current()
	logger.Info("Spyglass process starting", "version", release.Version, "revision", release.Revision, "built_at", release.BuiltAt)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	mode := ""
	if len(os.Args) > 1 {
		mode = os.Args[1]
	} else if os.Getenv("SPYGLASS_ENV") == "development" {
		mode = "development"
	}
	tracing, err := tracingFromEnvironment(ctx, mode, release)
	if err != nil {
		stop()
		logger.Error("Spyglass tracing configuration failed", "mode", mode, "error", err)
		os.Exit(1)
	}
	ctx = observability.WithTracing(ctx, tracing)
	err = nil
	switch mode {
	case "development":
		err = runDevelopment(ctx, logger)
	case "account-api":
		err = runAccountAPI(ctx, logger)
	case "app-router":
		err = runAppRouter(ctx, logger, false)
	case "tool-router":
		err = runAppRouter(ctx, logger, true)
	case "app-api":
		err = runAppAPI(ctx, logger)
	case "admission-api":
		err = runAdmissionAPI(ctx, logger)
	case "billing-worker":
		err = runBillingWorker(ctx, logger)
	case "billing-admin":
		err = runBillingAdmin(ctx, logger)
	case "notification-worker":
		err = runNotificationWorker(ctx, logger)
	case "entitlement-worker":
		err = runEntitlementWorker(ctx, logger)
	case "account-lifecycle-worker":
		err = runAccountLifecycleWorker(ctx, logger)
	case "identity-maintenance-worker":
		err = runIdentityMaintenanceWorker(ctx, logger)
	case "work-reconciler":
		err = runWorkReconciler(ctx, logger)
	case "runner-controller":
		err = runRunnerController(ctx, logger)
	case "docker-runner-launcher":
		err = runDockerRunnerLauncher(ctx, logger)
	case "runner-broker":
		err = runRunnerBroker(ctx, logger)
	case "model-gateway":
		err = runModelGateway(ctx, logger)
	case "runner-invocation":
		err = runRunnerInvocation(ctx)
	case "agent-projection-worker":
		err = runAgentProjectionWorker(ctx, logger)
	case "knowledge-document-worker":
		err = runKnowledgeDocumentWorker(ctx, logger)
	case "baseline-maintenance-worker":
		err = runBaselineMaintenanceWorker(ctx, logger)
	case "agent-dispatch-worker":
		err = runAgentDispatchWorker(ctx, logger)
	case "agent-queue-admin":
		err = runAgentQueueAdmin(ctx, logger)
	case "route-receipt-worker":
		err = runRouteReceiptWorker(ctx, logger)
	case "route-canary":
		err = runRouteCanary(ctx, logger)
	case "work-release-admin":
		err = runWorkReleaseAdmin(ctx, logger)
	case "account-erasure-admin":
		err = runAccountErasureAdmin(ctx, logger)
	case "account-move-admin":
		err = runAccountMoveAdmin(ctx, logger)
	case "passkey-admin":
		err = runPasskeyAdmin(ctx, logger)
	case "catalog-admin":
		err = runCatalogAdmin(ctx, logger)
	case "migrate":
		err = runMigrate(ctx, logger)
	default:
		err = errors.New("usage: spyglass version | development | account-api | app-router | tool-router | app-api | admission-api | billing-worker | billing-admin <action> | notification-worker | entitlement-worker | account-lifecycle-worker | identity-maintenance-worker | work-reconciler | runner-controller | docker-runner-launcher | runner-broker | model-gateway | runner-invocation --broker-url=<url> --invocation-id=<uuid> --identity-token-file=<path> --broker-ca-file=<path> | agent-dispatch-worker | agent-projection-worker | knowledge-document-worker | baseline-maintenance-worker | agent-queue-admin <action> | route-receipt-worker | route-canary | work-release-admin <action> | account-erasure-admin <action> | account-move-admin <action> | passkey-admin <action> | catalog-admin <action> | migrate")
	}
	stop()
	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	shutdownErr := tracing.Shutdown(shutdownContext)
	shutdownCancel()
	if err == nil && shutdownErr != nil {
		err = fmt.Errorf("flush OpenTelemetry traces: %w", shutdownErr)
	}
	if err != nil {
		logger.Error("Spyglass process stopped", "mode", mode, "error", err)
		os.Exit(1)
	}
}

func runRunnerInvocation(ctx context.Context) error {
	flags := flag.NewFlagSet("runner-invocation", flag.ContinueOnError)
	var usage strings.Builder
	flags.SetOutput(&usage)
	brokerURL := flags.String("broker-url", "", "runner broker URL")
	invocationID := flags.String("invocation-id", "", "runner invocation UUID")
	identityTokenFile := flags.String("identity-token-file", "", "projected identity token file")
	brokerCAFile := flags.String("broker-ca-file", "", "broker root CA file")
	if err := flags.Parse(os.Args[2:]); err != nil || flags.NArg() != 0 || strings.TrimSpace(*brokerCAFile) == "" {
		return errors.New("runner invocation arguments are invalid")
	}
	client, err := runnerbrokerhttp.New(runnerbrokerhttp.Config{BrokerURL: *brokerURL, InvocationID: *invocationID, IdentityTokenFile: *identityTokenFile, RootCAFile: *brokerCAFile})
	if err != nil {
		return err
	}
	service, err := runnerexecution.New(client, client, []runnerexecution.Definition{
		{Kind: runnerwork.SummarySnapshotKind, Executor: runnerwork.SummarySnapshotExecutor{}},
		{Kind: runneragents.TurnExecutionKind, Executor: runneragents.TurnExecutor{}},
	})
	if err != nil {
		return err
	}
	return service.Run(ctx)
}

func runBillingAdmin(ctx context.Context, logger *slog.Logger) error {
	if len(os.Args) != 3 || (os.Args[2] != "inspect" && os.Args[2] != "replay-event" && os.Args[2] != "refresh-subscription") {
		return errors.New("usage: spyglass billing-admin inspect|replay-event|refresh-subscription")
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
	environment, err := requiredEnv("SPYGLASS_ENVIRONMENT")
	if err != nil {
		return err
	}
	confirmation, err := requiredEnv("SPYGLASS_CONFIRM_ENVIRONMENT")
	if err != nil {
		return err
	}
	mode, err := requiredEnv("SPYGLASS_STRIPE_MODE")
	if err != nil {
		return err
	}
	maxConns, err := int32Env("SPYGLASS_MAX_DATABASE_CONNS", 2)
	if err != nil {
		return err
	}
	config := billingcommand.Config{DatabaseURL: databaseURL, Action: os.Args[2], Actor: actor, Reason: reason, Environment: environment, ConfirmEnvironment: confirmation, Mode: mode, InspectLimit: 50, MaxDatabaseConns: maxConns}
	if config.Action == "inspect" {
		limit, err := int64Env("SPYGLASS_BILLING_INSPECT_LIMIT", 50)
		if err != nil {
			return err
		}
		config.InspectLimit = int(limit)
	} else if config.Action == "replay-event" {
		config.TargetID, err = requiredEnv("SPYGLASS_STRIPE_EVENT_ID")
		if err != nil {
			return err
		}
	} else {
		config.TargetID, err = requiredEnv("SPYGLASS_STRIPE_SUBSCRIPTION_ID")
		if err != nil {
			return err
		}
	}
	config.Reason, err = requireOperatorAuthorization(logger, "billing-admin", config.Action, config.Actor, config.Reason, config.Environment, operatorScope(map[string]string{"stripe_mode": config.Mode, "inspect_limit": strconv.Itoa(config.InspectLimit), "target_id": config.TargetID}))
	if err != nil {
		return err
	}
	startup, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return billingcommand.Run(startup, config, logger)
}

func runPasskeyAdmin(ctx context.Context, logger *slog.Logger) error {
	if len(os.Args) != 3 || (os.Args[2] != "inspect" && os.Args[2] != "reencrypt") {
		return errors.New("usage: spyglass passkey-admin inspect|reencrypt")
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
	environment, err := requiredEnv("SPYGLASS_ENVIRONMENT")
	if err != nil {
		return err
	}
	confirmation, err := requiredEnv("SPYGLASS_CONFIRM_ENVIRONMENT")
	if err != nil {
		return err
	}
	keys, active, err := versionedEncryptionKeysEnv("SPYGLASS_PASSKEY_ENCRYPTION_KEYS", "SPYGLASS_PASSKEY_ENCRYPTION_ACTIVE_VERSION")
	if err != nil {
		return err
	}
	maxConns, err := int32Env("SPYGLASS_MAX_DATABASE_CONNS", 2)
	if err != nil {
		return err
	}
	config := passkeycommand.Config{DatabaseURL: databaseURL, Action: os.Args[2], Actor: actor, Reason: reason, Environment: environment, ConfirmEnvironment: confirmation, EncryptionKeys: keys, ActiveKeyVersion: active, MaxDatabaseConns: maxConns}
	if config.Action == "reencrypt" {
		batch, err := int32Env("SPYGLASS_PASSKEY_REENCRYPT_BATCH", 100)
		if err != nil {
			return err
		}
		config.Batch = int(batch)
	}
	config.Reason, err = requireOperatorAuthorization(logger, "passkey-admin", config.Action, config.Actor, config.Reason, config.Environment, operatorScope(map[string]string{
		"active_key_version": strconv.Itoa(config.ActiveKeyVersion),
		"batch":              strconv.Itoa(config.Batch),
		"key_versions":       encryptionKeyVersions(config.EncryptionKeys),
	}))
	if err != nil {
		return err
	}
	return passkeycommand.Run(ctx, config, logger)
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
	environment, err := requiredEnv("SPYGLASS_ENVIRONMENT")
	if err != nil {
		return err
	}
	confirmation, err := requiredEnv("SPYGLASS_CONFIRM_ENVIRONMENT")
	if err != nil {
		return err
	}
	if confirmation != environment {
		return errors.New("SPYGLASS_CONFIRM_ENVIRONMENT must exactly match SPYGLASS_ENVIRONMENT")
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
	scope := map[string]string{
		"version":      strconv.FormatUint(config.Version, 10),
		"offer_code":   config.OfferCode,
		"stripe_mode":  config.StripeMode,
		"stripe_price": config.PriceID,
	}
	if len(config.CatalogJSON) > 0 {
		scope["catalog_sha256"] = operatorauth.Digest(string(config.CatalogJSON))
	}
	if !config.EffectiveAt.IsZero() {
		scope["effective_at"] = config.EffectiveAt.UTC().Format(time.RFC3339Nano)
	}
	config.Reason, err = requireOperatorAuthorization(logger, "catalog-admin", config.Action, config.Actor, config.Reason, environment, operatorScope(scope))
	if err != nil {
		return err
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
	config.Reason, err = requireOperatorAuthorization(logger, "work-release-admin", config.Action, config.Actor, config.Reason, config.Environment, operatorScope(map[string]string{
		"inspect_limit":  strconv.Itoa(config.InspectLimit),
		"account_id":     string(config.Target.AccountID),
		"work_item_id":   string(config.Target.WorkItemID),
		"reservation_id": config.Target.ReservationID,
	}))
	if err != nil {
		return err
	}
	startup, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return workreleasecommand.Run(startup, config, logger)
}

func runAgentQueueAdmin(ctx context.Context, logger *slog.Logger) error {
	if len(os.Args) != 3 || (os.Args[2] != "inspect" && os.Args[2] != "requeue") {
		return errors.New("usage: spyglass agent-queue-admin inspect|requeue")
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
	queue, err := requiredEnv("SPYGLASS_AGENT_QUEUE")
	if err != nil {
		return err
	}
	maxConns, err := int32Env("SPYGLASS_MAX_DATABASE_CONNS", 2)
	if err != nil {
		return err
	}
	config := agentqueuecommand.Config{DatabaseURL: databaseURL, Action: os.Args[2], Queue: queue, Actor: actor, Reason: reason, Environment: environment, ConfirmEnvironment: confirmation, InspectLimit: agentqueueapp.DefaultInspectLimit, MaxDatabaseConns: maxConns}
	if config.Action == "inspect" {
		limit, err := int64Env("SPYGLASS_AGENT_QUEUE_INSPECT_LIMIT", agentqueueapp.DefaultInspectLimit)
		if err != nil {
			return err
		}
		config.InspectLimit = int(limit)
	} else {
		accountID, err := requiredEnv("SPYGLASS_AGENT_ACCOUNT_ID")
		if err != nil {
			return err
		}
		invocationID, err := requiredEnv("SPYGLASS_AGENT_INVOCATION_ID")
		if err != nil {
			return err
		}
		config.Target = agentqueueapp.Target{Queue: queue, AccountID: ids.AccountID(accountID), InvocationID: invocationID}
	}
	config.Reason, err = requireOperatorAuthorization(logger, "agent-queue-admin", config.Action, config.Actor, config.Reason, config.Environment, operatorScope(map[string]string{
		"queue": config.Queue, "inspect_limit": strconv.Itoa(config.InspectLimit), "account_id": string(config.Target.AccountID), "invocation_id": config.Target.InvocationID,
	}))
	if err != nil {
		return err
	}
	startup, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return agentqueuecommand.Run(startup, config, logger)
}

func runAccountErasureAdmin(ctx context.Context, logger *slog.Logger) error {
	if len(os.Args) != 3 || (os.Args[2] != "prepare" && os.Args[2] != "inspect" && os.Args[2] != "approve" && os.Args[2] != "cancel" && os.Args[2] != "execute" && os.Args[2] != "restore-replay") {
		return errors.New("usage: spyglass account-erasure-admin prepare|inspect|approve|cancel|execute|restore-replay")
	}
	globalDatabaseURL, err := requiredEnv("SPYGLASS_GLOBAL_DATABASE_URL")
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
	globalMaxConns, err := int32Env("SPYGLASS_GLOBAL_MAX_DATABASE_CONNS", 2)
	if err != nil {
		return err
	}
	config := accounterasurecommand.Config{GlobalDatabaseURL: globalDatabaseURL, Action: os.Args[2], Actor: actor, Reason: reason, Environment: environment, ConfirmEnvironment: confirmation, MaxGlobalConns: globalMaxConns}
	if config.Action == "prepare" || config.Action == "approve" || config.Action == "execute" || config.Action == "restore-replay" {
		config.CellDatabaseURL, err = requiredEnv("SPYGLASS_CELL_DATABASE_URL")
		if err != nil {
			return err
		}
		cellID, err := requiredEnv("SPYGLASS_CELL_ID")
		if err != nil {
			return err
		}
		config.CellID = ids.CellID(cellID)
		config.MaxCellConns, err = int32Env("SPYGLASS_CELL_MAX_DATABASE_CONNS", 2)
		if err != nil {
			return err
		}
	}
	if config.Action != "prepare" {
		config.RequestID, err = requiredEnv("SPYGLASS_ACCOUNT_ERASURE_REQUEST_ID")
		if err != nil {
			return err
		}
	}
	if config.Action == "approve" || config.Action == "cancel" || config.Action == "execute" {
		config.ExpectedVersion, err = uint64Env("SPYGLASS_ACCOUNT_ERASURE_VERSION")
		if err != nil {
			return err
		}
	}
	if config.Action == "execute" {
		accountID, err := requiredEnv("SPYGLASS_ACCOUNT_ID")
		if err != nil {
			return err
		}
		confirmedAccountID, err := requiredEnv("SPYGLASS_CONFIRM_ACCOUNT_ID")
		if err != nil {
			return err
		}
		config.AccountID, config.ConfirmAccountID = ids.AccountID(accountID), ids.AccountID(confirmedAccountID)
		config.EvidenceKey, err = base64KeyEnv("SPYGLASS_ACCOUNT_ERASURE_EVIDENCE_KEY")
		if err != nil {
			return err
		}
		config.LeaseDuration, err = durationEnv("SPYGLASS_ACCOUNT_ERASURE_LEASE", 5*time.Minute)
		if err != nil {
			return err
		}
	}
	if config.Action == "restore-replay" {
		accountID, err := requiredEnv("SPYGLASS_ACCOUNT_ID")
		if err != nil {
			return err
		}
		confirmedAccountID, err := requiredEnv("SPYGLASS_CONFIRM_ACCOUNT_ID")
		if err != nil {
			return err
		}
		config.AccountID, config.ConfirmAccountID = ids.AccountID(accountID), ids.AccountID(confirmedAccountID)
		config.RestoreSigningKey, err = base64KeyEnv("SPYGLASS_ACCOUNT_ERASURE_RESTORE_SIGNING_KEY")
		if err != nil {
			return err
		}
		config.RestoreDirectiveFile, err = requiredEnv("SPYGLASS_ACCOUNT_ERASURE_RESTORE_DIRECTIVE_FILE")
		if err != nil {
			return err
		}
	}
	if config.Action == "prepare" {
		accountID, err := requiredEnv("SPYGLASS_ACCOUNT_ID")
		if err != nil {
			return err
		}
		confirmedAccountID, err := requiredEnv("SPYGLASS_CONFIRM_ACCOUNT_ID")
		if err != nil {
			return err
		}
		config.AccountID, config.ConfirmAccountID = ids.AccountID(accountID), ids.AccountID(confirmedAccountID)
		config.PolicyVersion, err = uint64Env("SPYGLASS_ACCOUNT_ERASURE_POLICY_VERSION")
		if err != nil {
			return err
		}
		backupExpiry, err := requiredEnv("SPYGLASS_ACCOUNT_ERASURE_BACKUP_EXPIRES_AT")
		if err != nil {
			return err
		}
		config.BackupExpiresAt, err = time.Parse(time.RFC3339, backupExpiry)
		if err != nil {
			return errors.New("SPYGLASS_ACCOUNT_ERASURE_BACKUP_EXPIRES_AT must be RFC3339")
		}
		disposition, err := requiredEnv("SPYGLASS_ACCOUNT_ERASURE_EXPORT_DISPOSITION")
		if err != nil {
			return err
		}
		config.Export.Disposition = accounterasure.ExportDisposition(disposition)
		switch config.Export.Disposition {
		case accounterasure.ExportArtifact:
			config.Export.Reference, err = requiredEnv("SPYGLASS_ACCOUNT_ERASURE_EXPORT_REFERENCE")
			if err != nil {
				return err
			}
			digest, err := requiredEnv("SPYGLASS_ACCOUNT_ERASURE_EXPORT_SHA256")
			if err != nil {
				return err
			}
			config.Export.SHA256, err = hex.DecodeString(digest)
			if err != nil || len(config.Export.SHA256) != 32 {
				return errors.New("SPYGLASS_ACCOUNT_ERASURE_EXPORT_SHA256 must be exactly 64 hexadecimal characters")
			}
			exportExpiry, err := requiredEnv("SPYGLASS_ACCOUNT_ERASURE_EXPORT_EXPIRES_AT")
			if err != nil {
				return err
			}
			parsedExpiry, err := time.Parse(time.RFC3339, exportExpiry)
			if err != nil {
				return errors.New("SPYGLASS_ACCOUNT_ERASURE_EXPORT_EXPIRES_AT must be RFC3339")
			}
			config.Export.ExpiresAt = &parsedExpiry
		case accounterasure.ExportNotApplicable:
			config.Export.Reason, err = requiredEnv("SPYGLASS_ACCOUNT_ERASURE_EXPORT_REASON")
			if err != nil {
				return err
			}
		default:
			return errors.New("SPYGLASS_ACCOUNT_ERASURE_EXPORT_DISPOSITION must be artifact or not_applicable")
		}
	}
	config.Reason, err = requireOperatorAuthorization(logger, "account-erasure-admin", config.Action, config.Actor, config.Reason, config.Environment, operatorScope(map[string]string{
		"request_id":             config.RequestID,
		"account_id":             string(config.AccountID),
		"confirm_account_id":     string(config.ConfirmAccountID),
		"cell_id":                string(config.CellID),
		"expected_version":       strconv.FormatUint(config.ExpectedVersion, 10),
		"policy_version":         strconv.FormatUint(config.PolicyVersion, 10),
		"export_disposition":     string(config.Export.Disposition),
		"export_reference":       config.Export.Reference,
		"export_sha256":          hex.EncodeToString(config.Export.SHA256),
		"export_expires_at":      formatOptionalTimePointer(config.Export.ExpiresAt),
		"export_reason_sha256":   operatorauth.Digest(config.Export.Reason),
		"backup_expires_at":      formatOptionalTime(config.BackupExpiresAt),
		"lease_nanoseconds":      strconv.FormatInt(int64(config.LeaseDuration), 10),
		"restore_directive_file": config.RestoreDirectiveFile,
	}))
	if err != nil {
		return err
	}
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return accounterasurecommand.Run(startup, config, logger)
}

func runAccountMoveAdmin(ctx context.Context, logger *slog.Logger) error {
	if len(os.Args) != 3 || (os.Args[2] != "prepare" && os.Args[2] != "inspect" && os.Args[2] != "advance" &&
		os.Args[2] != "pause" && os.Args[2] != "resume" && os.Args[2] != "rollback" && os.Args[2] != "retire") {
		return errors.New("usage: spyglass account-move-admin prepare|inspect|advance|pause|resume|rollback|retire")
	}
	globalDatabaseURL, err := requiredEnv("SPYGLASS_GLOBAL_DATABASE_URL")
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
	globalMaxConns, err := int32Env("SPYGLASS_GLOBAL_MAX_DATABASE_CONNS", 2)
	if err != nil {
		return err
	}
	lease, err := durationEnv("SPYGLASS_ACCOUNT_MOVE_LEASE", 15*time.Minute)
	if err != nil {
		return err
	}
	config := accountmovecommand.Config{GlobalDatabaseURL: globalDatabaseURL, Action: os.Args[2], Actor: actor, Reason: reason,
		Environment: environment, ConfirmEnvironment: confirmation, MaxGlobalConns: globalMaxConns, Lease: lease}
	if config.Action == "prepare" {
		accountID, err := requiredEnv("SPYGLASS_ACCOUNT_ID")
		if err != nil {
			return err
		}
		confirmedAccountID, err := requiredEnv("SPYGLASS_CONFIRM_ACCOUNT_ID")
		if err != nil {
			return err
		}
		destinationCellID, err := requiredEnv("SPYGLASS_ACCOUNT_MOVE_DESTINATION_CELL_ID")
		if err != nil {
			return err
		}
		config.AccountID, config.ConfirmAccountID = ids.AccountID(accountID), ids.AccountID(confirmedAccountID)
		config.DestinationCellID = ids.CellID(destinationCellID)
		config.RollbackWindow, err = durationEnv("SPYGLASS_ACCOUNT_MOVE_ROLLBACK_WINDOW", 24*time.Hour)
		if err != nil {
			return err
		}
	} else {
		config.MoveID, err = requiredEnv("SPYGLASS_ACCOUNT_MOVE_ID")
		if err != nil {
			return err
		}
	}
	if config.Action == "pause" || config.Action == "resume" {
		config.ExpectedVersion, err = uint64Env("SPYGLASS_ACCOUNT_MOVE_VERSION")
		if err != nil {
			return err
		}
	}
	if config.Action == "advance" || config.Action == "rollback" || config.Action == "retire" {
		config.SourceCellDatabaseURL, err = requiredEnv("SPYGLASS_SOURCE_CELL_DATABASE_URL")
		if err != nil {
			return err
		}
		config.DestinationDatabaseURL, err = requiredEnv("SPYGLASS_DESTINATION_CELL_DATABASE_URL")
		if err != nil {
			return err
		}
		sourceCellID, err := requiredEnv("SPYGLASS_ACCOUNT_MOVE_SOURCE_CELL_ID")
		if err != nil {
			return err
		}
		destinationCellID, err := requiredEnv("SPYGLASS_ACCOUNT_MOVE_DESTINATION_CELL_ID")
		if err != nil {
			return err
		}
		config.SourceCellID, config.DestinationCellID = ids.CellID(sourceCellID), ids.CellID(destinationCellID)
		config.MaxCellConns, err = int32Env("SPYGLASS_CELL_MAX_DATABASE_CONNS", 2)
		if err != nil {
			return err
		}
	}
	config.Reason, err = requireOperatorAuthorization(logger, "account-move-admin", config.Action, config.Actor, config.Reason, config.Environment, operatorScope(map[string]string{
		"move_id": config.MoveID, "account_id": string(config.AccountID), "confirm_account_id": string(config.ConfirmAccountID),
		"source_cell_id": string(config.SourceCellID), "destination_cell_id": string(config.DestinationCellID),
		"expected_version": strconv.FormatUint(config.ExpectedVersion, 10), "rollback_window_nanoseconds": strconv.FormatInt(int64(config.RollbackWindow), 10),
		"lease_nanoseconds": strconv.FormatInt(int64(config.Lease), 10),
	}))
	if err != nil {
		return err
	}
	operationTimeout, err := durationEnv("SPYGLASS_ACCOUNT_MOVE_OPERATION_TIMEOUT", time.Hour)
	if err != nil || operationTimeout < 5*time.Minute || operationTimeout > 24*time.Hour {
		return errors.New("SPYGLASS_ACCOUNT_MOVE_OPERATION_TIMEOUT must be between 5m and 24h")
	}
	operation, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	return accountmovecommand.Run(operation, config, logger)
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
	return serveHTTP(ctx, "development", httpAddress(":8080"), development.Handler(logger), logger)
}

func runAccountAPI(ctx context.Context, logger *slog.Logger) error {
	config, err := productionConfig()
	if err != nil {
		return err
	}
	restoreGate, err := openRequiredRestoreGate(ctx, config.databaseURL, restoregate.Global, "SPYGLASS_")
	if err != nil {
		return err
	}
	defer restoreGate.Close()
	startup, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	stripeClient := &http.Client{Transport: observability.TracingFromContext(ctx).ExternalTransport(nil), Timeout: 15 * time.Second, CheckRedirect: rejectOutboundRedirect}
	server, err := accountapi.New(startup, accountapi.Config{DatabaseURL: config.databaseURL, StripeWebhookSecret: config.stripeWebhookSecret, StripeSecretKey: config.stripeSecretKey, StripeAPIVersion: config.stripeAPIVersion, StripeMode: config.stripeMode, StripeHTTPClient: stripeClient, MaxDatabaseConns: config.maxDatabaseConns, AppOrigin: config.appOrigin, PublicOrigin: config.publicOrigin, NotificationEncryptionKey: config.notificationEncryptionKey, NetworkActorKey: config.networkActorKey, PasskeyEncryptionKeys: config.passkeyEncryptionKeys, PasskeyActiveKeyVersion: config.passkeyActiveKeyVersion, PasskeyRPID: config.passkeyRPID, TrustedProxyCIDRs: config.trustedProxyCIDRs, CatalogRefreshInterval: config.catalogRefreshInterval}, logger)
	if err != nil {
		return err
	}
	defer server.Close()
	return serveHTTP(ctx, "account-api", httpAddress(":8080"), withRestoreGate([]*restoregate.Gate{restoreGate}, server.Handler), logger)
}

func runAppRouter(ctx context.Context, logger *slog.Logger, privateTLS bool) error {
	developmentMode := os.Getenv("SPYGLASS_ENV") == "development"
	databaseURL, err := requiredEnv("SPYGLASS_DATABASE_URL")
	if err != nil {
		return err
	}
	restoreGate, err := openRequiredRestoreGate(ctx, databaseURL, restoregate.Global, "SPYGLASS_")
	if err != nil {
		return err
	}
	defer restoreGate.Close()
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
	toolIssuer, err := requiredEnv("SPYGLASS_TOOL_CONTEXT_ISSUER")
	if err != nil {
		return err
	}
	toolVerifyKeys, err := routeVerifyKeysEnv("SPYGLASS_TOOL_CONTEXT_VERIFY_KEYS")
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
	cellTransport = observability.TracingFromContext(ctx).Transport(cellTransport)
	startup, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	server, err := approuter.New(startup, approuter.Config{DatabaseURL: databaseURL, MaxDatabaseConns: maxConns, RouteIssuer: issuer, RouteSigningKeyID: keyID, RouteSigningKey: key, RouteLifetime: lifetime, ToolIssuer: toolIssuer, ToolVerifyKeys: toolVerifyKeys, DirectoryCacheTTL: directoryTTL, DirectoryCapacity: directoryCapacity, CellTransport: cellTransport, SessionCookieName: os.Getenv("SPYGLASS_SESSION_COOKIE_NAME"), SecureCookies: true, TrustedOrigins: []string{appOrigin}, AllowHTTPCells: developmentMode}, logger, registration.SystemClock{})
	if err != nil {
		return err
	}
	defer server.Close()
	handler := withRestoreGate([]*restoregate.Gate{restoreGate}, server.Handler)
	if !privateTLS {
		return serveHTTP(ctx, "app-router", httpAddress(":8080"), handler, logger)
	}
	serverTLS, err := workloadidentity.NewServerConfig(workloadTLSFilesEnv())
	if err != nil {
		return err
	}
	secured, err := workloadidentity.RequireClientIdentity(handler, csvEnv("SPYGLASS_WORKLOAD_CLIENT_IDENTITIES"), logger)
	if err != nil {
		return err
	}
	return serveHTTPS(ctx, "tool-router", httpAddress(":8443"), secured, serverTLS, logger)
}

func runAppAPI(ctx context.Context, logger *slog.Logger) error {
	developmentMode := os.Getenv("SPYGLASS_ENV") == "development"
	databaseURL, err := requiredEnv("SPYGLASS_DATABASE_URL")
	if err != nil {
		return err
	}
	restoreGate, err := openRequiredRestoreGate(ctx, databaseURL, restoregate.Cell, "SPYGLASS_")
	if err != nil {
		return err
	}
	defer restoreGate.Close()
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
	objectAccessKey, err := requiredEnv("SPYGLASS_OBJECT_STORE_ACCESS_KEY")
	if err != nil {
		return err
	}
	objectSecretKey, err := requiredEnv("SPYGLASS_OBJECT_STORE_SECRET_KEY")
	if err != nil {
		return err
	}
	objectSecure, err := boolEnv("SPYGLASS_OBJECT_STORE_SECURE", false)
	if err != nil {
		return err
	}
	objectSSE, err := boolEnv("SPYGLASS_OBJECT_STORE_SERVER_SIDE_ENCRYPTION", true)
	if err != nil {
		return err
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
	admissionTransport = observability.TracingFromContext(ctx).Transport(admissionTransport)
	startup, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	server, err := appapi.New(startup, appapi.Config{
		DatabaseURL: databaseURL, CellID: ids.CellID(cellID), RouteIssuer: issuer, RouteVerifyKeys: keys, MaxDatabaseConns: maxConns, MaxRequestBody: maxBody,
		AdmissionOrigin: admissionOrigin, AdmissionTransport: admissionTransport, AllowHTTPAdmission: developmentMode,
		ObjectEndpoint: envOr("SPYGLASS_OBJECT_STORE_ENDPOINT", "object-store:9000"), ObjectRegion: os.Getenv("SPYGLASS_OBJECT_STORE_REGION"), ObjectBucket: envOr("SPYGLASS_OBJECT_STORE_BUCKET", "spyglass-documents"),
		ObjectAccessKey: objectAccessKey, ObjectSecretKey: objectSecretKey, ObjectSecure: objectSecure, ObjectSSE: objectSSE,
	}, logger, registration.SystemClock{})
	if err != nil {
		return err
	}
	defer server.Close()
	if developmentMode {
		return serveHTTP(ctx, "app-api", httpAddress(":8080"), withRestoreGate([]*restoregate.Gate{restoreGate}, server.Handler), logger)
	}
	secured, err := workloadidentity.RequireClientIdentity(server.Handler, csvEnv("SPYGLASS_WORKLOAD_CLIENT_IDENTITIES"), logger)
	if err != nil {
		return err
	}
	return serveHTTPS(ctx, "app-api", httpAddress(":8443"), withRestoreGate([]*restoregate.Gate{restoreGate}, secured), serverTLS, logger)
}

func runAdmissionAPI(ctx context.Context, logger *slog.Logger) error {
	developmentMode := os.Getenv("SPYGLASS_ENV") == "development"
	databaseURL, err := requiredEnv("SPYGLASS_DATABASE_URL")
	if err != nil {
		return err
	}
	restoreGate, err := openRequiredRestoreGate(ctx, databaseURL, restoregate.Global, "SPYGLASS_")
	if err != nil {
		return err
	}
	defer restoreGate.Close()
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
		return serveHTTP(ctx, "admission-api", httpAddress(":8080"), withRestoreGate([]*restoregate.Gate{restoreGate}, server.Handler), logger)
	}
	secured, err := workloadidentity.RequireClientIdentity(server.Handler, csvEnv("SPYGLASS_WORKLOAD_CLIENT_IDENTITIES"), logger)
	if err != nil {
		return err
	}
	return serveHTTPS(ctx, "admission-api", httpAddress(":8443"), withRestoreGate([]*restoregate.Gate{restoreGate}, secured), serverTLS, logger)
}

func runBillingWorker(ctx context.Context, logger *slog.Logger) error {
	databaseURL, err := requiredEnv("SPYGLASS_DATABASE_URL")
	if err != nil {
		return err
	}
	restoreGate, err := openRequiredRestoreGate(ctx, databaseURL, restoregate.Global, "SPYGLASS_")
	if err != nil {
		return err
	}
	defer restoreGate.Close()
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
	return serveWorker(ctx, "billing", envOr("SPYGLASS_HEALTH_ADDRESS", ":8081"), &restoreGatedWorker{worker: worker, gates: []*restoregate.Gate{restoreGate}}, logger)
}

func runNotificationWorker(ctx context.Context, logger *slog.Logger) error {
	databaseURL, err := requiredEnv("SPYGLASS_DATABASE_URL")
	if err != nil {
		return err
	}
	restoreGate, err := openRequiredRestoreGate(ctx, databaseURL, restoregate.Global, "SPYGLASS_")
	if err != nil {
		return err
	}
	defer restoreGate.Close()
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
	worker, err := notificationworker.New(startup, notificationworker.Config{DatabaseURL: databaseURL, NotificationEncryptionKey: key, SMTPAddress: address, SMTPServerName: serverName, SMTPUsername: os.Getenv("SPYGLASS_SMTP_USERNAME"), SMTPPassword: os.Getenv("SPYGLASS_SMTP_PASSWORD"), SMTPFromAddress: fromAddress, SMTPFromName: envOr("SPYGLASS_SMTP_FROM_NAME", "Infinite Ocean"), SMTPRootCAFile: os.Getenv("SPYGLASS_SMTP_ROOT_CA_FILE"), AppOrigin: appOrigin, MaxDatabaseConns: maxConns, PollInterval: poll}, logger)
	if err != nil {
		return err
	}
	defer worker.Close()
	return serveWorker(ctx, "notification", envOr("SPYGLASS_HEALTH_ADDRESS", ":8081"), &restoreGatedWorker{worker: worker, gates: []*restoregate.Gate{restoreGate}}, logger)
}

func runEntitlementWorker(ctx context.Context, logger *slog.Logger) error {
	databaseURL, err := requiredEnv("SPYGLASS_DATABASE_URL")
	if err != nil {
		return err
	}
	restoreGate, err := openRequiredRestoreGate(ctx, databaseURL, restoregate.Global, "SPYGLASS_")
	if err != nil {
		return err
	}
	defer restoreGate.Close()
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
	return serveWorker(ctx, "entitlement", envOr("SPYGLASS_HEALTH_ADDRESS", ":8081"), &restoreGatedWorker{worker: worker, gates: []*restoregate.Gate{restoreGate}}, logger)
}

func runAccountLifecycleWorker(ctx context.Context, logger *slog.Logger) error {
	databaseURL, err := requiredEnv("SPYGLASS_DATABASE_URL")
	if err != nil {
		return err
	}
	restoreGate, err := openRequiredRestoreGate(ctx, databaseURL, restoregate.Global, "SPYGLASS_")
	if err != nil {
		return err
	}
	defer restoreGate.Close()
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
	return serveWorker(ctx, "account-lifecycle", envOr("SPYGLASS_HEALTH_ADDRESS", ":8081"), &restoreGatedWorker{worker: worker, gates: []*restoregate.Gate{restoreGate}}, logger)
}

func runIdentityMaintenanceWorker(ctx context.Context, logger *slog.Logger) error {
	databaseURL, err := requiredEnv("SPYGLASS_DATABASE_URL")
	if err != nil {
		return err
	}
	restoreGate, err := openRequiredRestoreGate(ctx, databaseURL, restoregate.Global, "SPYGLASS_")
	if err != nil {
		return err
	}
	defer restoreGate.Close()
	maxConns, err := int32Env("SPYGLASS_MAX_DATABASE_CONNS", 3)
	if err != nil {
		return err
	}
	interval, err := durationEnv("SPYGLASS_IDENTITY_MAINTENANCE_INTERVAL", time.Hour)
	if err != nil || interval < time.Minute || interval > 24*time.Hour {
		return errors.New("SPYGLASS_IDENTITY_MAINTENANCE_INTERVAL must be between 1m and 24h")
	}
	retention, err := durationEnv("SPYGLASS_PASSKEY_CEREMONY_RETENTION", identitymaintenance.DefaultRetention)
	if err != nil {
		return err
	}
	batch, err := int32Env("SPYGLASS_IDENTITY_MAINTENANCE_PRUNE_BATCH", identitymaintenance.DefaultBatch)
	if err != nil {
		return err
	}
	alertBacklog, err := int32Env("SPYGLASS_IDENTITY_MAINTENANCE_ALERT_BACKLOG", 10000)
	if err != nil {
		return err
	}
	if alertBacklog > 10000000 {
		return errors.New("SPYGLASS_IDENTITY_MAINTENANCE_ALERT_BACKLOG must be at most 10000000")
	}
	if err := identitymaintenance.ValidateBounds(retention, int(batch)); err != nil {
		return err
	}
	startup, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	worker, err := identitymaintenanceworker.New(startup, identitymaintenanceworker.Config{DatabaseURL: databaseURL, MaxDatabaseConns: maxConns, Interval: interval, Retention: retention, PruneBatch: int(batch), AlertBacklog: uint64(alertBacklog)}, logger)
	if err != nil {
		return err
	}
	defer worker.Close()
	return serveWorker(ctx, "identity-maintenance", envOr("SPYGLASS_HEALTH_ADDRESS", ":8081"), &restoreGatedWorker{worker: worker, gates: []*restoregate.Gate{restoreGate}}, logger)
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
	cellRestoreGate, err := openRequiredRestoreGate(ctx, cellDatabaseURL, restoregate.Cell, "SPYGLASS_CELL_")
	if err != nil {
		return err
	}
	defer cellRestoreGate.Close()
	globalRestoreGate, err := openRequiredRestoreGate(ctx, globalDatabaseURL, restoregate.Global, "SPYGLASS_GLOBAL_")
	if err != nil {
		return err
	}
	defer globalRestoreGate.Close()
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
	return serveWorker(ctx, "work-reconciler", envOr("SPYGLASS_HEALTH_ADDRESS", ":8081"), &restoreGatedWorker{worker: worker, gates: []*restoregate.Gate{cellRestoreGate, globalRestoreGate}}, logger)
}

func runRunnerController(ctx context.Context, logger *slog.Logger) error {
	databaseURL, err := requiredEnv("SPYGLASS_CELL_DATABASE_URL")
	if err != nil {
		return err
	}
	restoreGate, err := openRequiredRestoreGate(ctx, databaseURL, restoregate.Cell, "SPYGLASS_")
	if err != nil {
		return err
	}
	defer restoreGate.Close()
	maxConns, err := int32Env("SPYGLASS_CELL_MAX_DATABASE_CONNS", 4)
	if err != nil {
		return err
	}
	poll, err := durationEnv("SPYGLASS_RUNNER_CONTROL_POLL_INTERVAL", time.Second)
	if err != nil || poll < 100*time.Millisecond || poll > time.Minute {
		return errors.New("SPYGLASS_RUNNER_CONTROL_POLL_INTERVAL must be between 100ms and 1m")
	}
	lease, err := durationEnv("SPYGLASS_RUNNER_CONTROL_LEASE", runnercontrol.DefaultLease)
	if err != nil || lease < time.Second || lease > 30*time.Minute {
		return errors.New("SPYGLASS_RUNNER_CONTROL_LEASE must be between 1s and 30m")
	}
	maxAttempts, err := int32Env("SPYGLASS_RUNNER_CONTROL_MAX_ATTEMPTS", runnercontrol.DefaultMaxAttempts)
	if err != nil || maxAttempts > 100 {
		return errors.New("SPYGLASS_RUNNER_CONTROL_MAX_ATTEMPTS must be between 1 and 100")
	}
	inspectionBatch, err := int32Env("SPYGLASS_RUNNER_INSPECTION_BATCH", 100)
	if err != nil || inspectionBatch > 1000 {
		return errors.New("SPYGLASS_RUNNER_INSPECTION_BATCH must be between 1 and 1000")
	}
	cleanupInterval, err := durationEnv("SPYGLASS_RUNNER_PAYLOAD_CLEANUP_INTERVAL", time.Hour)
	if err != nil || cleanupInterval < time.Minute || cleanupInterval > 24*time.Hour {
		return errors.New("SPYGLASS_RUNNER_PAYLOAD_CLEANUP_INTERVAL must be between 1m and 24h")
	}
	payloadRetention, err := durationEnv("SPYGLASS_RUNNER_PAYLOAD_RETENTION", runnercontrol.DefaultPayloadRetention)
	if err != nil || payloadRetention < time.Hour || payloadRetention > 30*24*time.Hour {
		return errors.New("SPYGLASS_RUNNER_PAYLOAD_RETENTION must be between 1h and 720h")
	}
	pruneBatch, err := int32Env("SPYGLASS_RUNNER_PAYLOAD_PRUNE_BATCH", runnercontrol.DefaultPruneBatch)
	if err != nil || pruneBatch > runnercontrol.MaximumPruneBatch {
		return errors.New("SPYGLASS_RUNNER_PAYLOAD_PRUNE_BATCH must be between 1 and 1000")
	}
	substrate, err := requiredEnv("SPYGLASS_RUNNER_SUBSTRATE")
	if err != nil {
		return err
	}
	var launcher runnercontrol.Launcher
	var kubernetesConfig kubernetes.Config
	switch substrate {
	case "kubernetes":
		namespace, requiredErr := requiredEnv("SPYGLASS_RUNNER_NAMESPACE")
		if requiredErr != nil {
			return requiredErr
		}
		image, requiredErr := requiredEnv("SPYGLASS_RUNNER_IMAGE")
		if requiredErr != nil {
			return requiredErr
		}
		serviceAccount, requiredErr := requiredEnv("SPYGLASS_RUNNER_SERVICE_ACCOUNT")
		if requiredErr != nil {
			return requiredErr
		}
		runtimeClass, requiredErr := requiredEnv("SPYGLASS_RUNNER_RUNTIME_CLASS")
		if requiredErr != nil {
			return requiredErr
		}
		brokerURL, requiredErr := requiredEnv("SPYGLASS_RUNNER_BROKER_URL")
		if requiredErr != nil {
			return requiredErr
		}
		brokerCAConfigMap, requiredErr := requiredEnv("SPYGLASS_RUNNER_BROKER_CA_CONFIG_MAP")
		if requiredErr != nil {
			return requiredErr
		}
		deadline, durationErr := durationEnv("SPYGLASS_RUNNER_ACTIVE_DEADLINE", 15*time.Minute)
		if durationErr != nil || deadline < 30*time.Second || deadline > 24*time.Hour || deadline%time.Second != 0 {
			return errors.New("SPYGLASS_RUNNER_ACTIVE_DEADLINE must be whole seconds between 30s and 24h")
		}
		retention, durationErr := durationEnv("SPYGLASS_RUNNER_JOB_RETENTION", time.Hour)
		if durationErr != nil || retention < time.Minute || retention > 7*24*time.Hour || retention%time.Second != 0 {
			return errors.New("SPYGLASS_RUNNER_JOB_RETENTION must be whole seconds between 1m and 168h")
		}
		kubernetesConfig = kubernetes.Config{Namespace: namespace, RunnerImage: image, RunnerServiceAccount: serviceAccount, RunnerRuntimeClass: runtimeClass, BrokerURL: brokerURL, RunnerBrokerCAConfigMap: brokerCAConfigMap, Profiles: kubernetesRunnerProfiles(), ActiveDeadlineSeconds: int64(deadline / time.Second), TTLSecondsAfterFinished: int64(retention / time.Second)}
	case "docker-stage":
		if !dockerStageEnvironment() {
			return errors.New("docker-stage runner substrate requires stage or local-secure verification environment")
		}
		origin, requiredErr := requiredEnv("SPYGLASS_DOCKER_LAUNCHER_ORIGIN")
		if requiredErr != nil {
			return requiredErr
		}
		token, requiredErr := requiredEnv("SPYGLASS_DOCKER_LAUNCHER_CONTROLLER_TOKEN")
		if requiredErr != nil {
			return requiredErr
		}
		transport, transportErr := workloadidentity.NewClientTransport(workloadTLSFilesEnv())
		if transportErr != nil {
			return transportErr
		}
		defer transport.CloseIdleConnections()
		launcher, err = dockerlauncherhttp.New(dockerlauncherhttp.Config{Origin: origin, Token: token, HTTPClient: &http.Client{Transport: observability.TracingFromContext(ctx).Transport(transport), Timeout: 15 * time.Second, CheckRedirect: rejectOutboundRedirect}})
		if err != nil {
			return err
		}
	default:
		return errors.New("SPYGLASS_RUNNER_SUBSTRATE must be kubernetes or docker-stage")
	}
	startup, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	worker, err := runnercontroller.New(startup, runnercontroller.Config{
		CellDatabaseURL: databaseURL, MaxDatabaseConns: maxConns, PollInterval: poll, Lease: lease,
		MaxAttempts: int(maxAttempts), InspectionBatch: int(inspectionBatch), CleanupInterval: cleanupInterval,
		PayloadRetention: payloadRetention, PruneBatch: int(pruneBatch),
		Kubernetes: kubernetesConfig, Launcher: launcher,
	}, logger)
	if err != nil {
		return err
	}
	defer worker.Close()
	return serveWorker(ctx, "runner-controller", envOr("SPYGLASS_HEALTH_ADDRESS", ":8081"), &restoreGatedWorker{worker: worker, gates: []*restoregate.Gate{restoreGate}}, logger)
}

func kubernetesRunnerProfiles() map[string]kubernetes.ResourceProfile {
	return map[string]kubernetes.ResourceProfile{
		"agent-small":  {CPURequest: "250m", CPULimit: "1", MemoryRequest: "256Mi", MemoryLimit: "1Gi", EphemeralStorageLimit: "1Gi"},
		"agent-medium": {CPURequest: "500m", CPULimit: "2", MemoryRequest: "512Mi", MemoryLimit: "2Gi", EphemeralStorageLimit: "2Gi"},
		"agent-large":  {CPURequest: "1", CPULimit: "4", MemoryRequest: "1Gi", MemoryLimit: "4Gi", EphemeralStorageLimit: "4Gi"},
	}
}

func dockerRunnerProfiles() map[string]dockerengine.ResourceProfile {
	return map[string]dockerengine.ResourceProfile{
		"agent-small":  {NanoCPUs: 1_000_000_000, MemoryBytes: 1 << 30, PidsLimit: 128, WorkTmpfsBytes: 1 << 30},
		"agent-medium": {NanoCPUs: 2_000_000_000, MemoryBytes: 2 << 30, PidsLimit: 256, WorkTmpfsBytes: 2 << 30},
		"agent-large":  {NanoCPUs: 4_000_000_000, MemoryBytes: 4 << 30, PidsLimit: 512, WorkTmpfsBytes: 4 << 30},
	}
}

func dockerStageEnvironment() bool {
	environment := os.Getenv("SPYGLASS_ENVIRONMENT")
	return environment == "stage" || environment == "local-secure"
}

func runDockerRunnerLauncher(ctx context.Context, logger *slog.Logger) error {
	if !dockerStageEnvironment() || os.Getenv("SPYGLASS_RUNNER_SUBSTRATE") != "docker-stage" {
		return errors.New("docker-runner-launcher requires stage/local-secure environment and docker-stage substrate")
	}
	image, err := requiredEnv("SPYGLASS_RUNNER_IMAGE")
	if err != nil {
		return err
	}
	network, err := requiredEnv("SPYGLASS_DOCKER_RUNNER_NETWORK")
	if err != nil {
		return err
	}
	brokerURL, err := requiredEnv("SPYGLASS_RUNNER_BROKER_URL")
	if err != nil {
		return err
	}
	identityDirectory, err := requiredEnv("SPYGLASS_DOCKER_RUNNER_IDENTITY_DIRECTORY")
	if err != nil {
		return err
	}
	brokerCAFile, err := requiredEnv("SPYGLASS_DOCKER_RUNNER_BROKER_CA_FILE")
	if err != nil {
		return err
	}
	controllerToken, err := requiredEnv("SPYGLASS_DOCKER_LAUNCHER_CONTROLLER_TOKEN")
	if err != nil {
		return err
	}
	brokerToken, err := requiredEnv("SPYGLASS_DOCKER_LAUNCHER_BROKER_TOKEN")
	if err != nil {
		return err
	}
	deadline, err := durationEnv("SPYGLASS_RUNNER_ACTIVE_DEADLINE", 15*time.Minute)
	if err != nil || deadline < 30*time.Second || deadline > 24*time.Hour || deadline%time.Second != 0 {
		return errors.New("SPYGLASS_RUNNER_ACTIVE_DEADLINE must be whole seconds between 30s and 24h")
	}
	retention, err := durationEnv("SPYGLASS_RUNNER_JOB_RETENTION", time.Hour)
	if err != nil || retention < time.Minute || retention > 7*24*time.Hour || retention%time.Second != 0 {
		return errors.New("SPYGLASS_RUNNER_JOB_RETENTION must be whole seconds between 1m and 168h")
	}
	cleanupInterval, err := durationEnv("SPYGLASS_DOCKER_RUNNER_CLEANUP_INTERVAL", time.Minute)
	if err != nil || cleanupInterval < 10*time.Second || cleanupInterval > time.Hour {
		return errors.New("SPYGLASS_DOCKER_RUNNER_CLEANUP_INTERVAL must be between 10s and 1h")
	}
	cleanupBatch, err := int32Env("SPYGLASS_DOCKER_RUNNER_CLEANUP_BATCH", 100)
	if err != nil || cleanupBatch > 1000 {
		return errors.New("SPYGLASS_DOCKER_RUNNER_CLEANUP_BATCH must be between 1 and 1000")
	}
	launcher, err := dockerengine.New(dockerengine.Config{
		SocketPath: os.Getenv("SPYGLASS_DOCKER_ENGINE_SOCKET"), RunnerImage: image, Network: network,
		BrokerURL: brokerURL, IdentityDirectory: identityDirectory, BrokerCAFile: brokerCAFile,
		Profiles: dockerRunnerProfiles(), ActiveDeadline: deadline, Retention: retention,
		AllowLocalImage:                  os.Getenv("SPYGLASS_ENVIRONMENT") == "local-secure",
		AllowPermissionlessIdentityFiles: os.Getenv("SPYGLASS_ENVIRONMENT") == "local-secure",
	})
	if err != nil {
		return err
	}
	server, err := dockerlauncherapi.New(launcher, dockerlauncherapi.Config{ControllerToken: controllerToken, BrokerToken: brokerToken}, logger)
	if err != nil {
		return err
	}
	serverTLS, err := workloadidentity.NewServerConfig(workloadTLSFilesEnv())
	if err != nil {
		return err
	}
	secured, err := workloadidentity.RequireClientIdentity(server.Handler(), csvEnv("SPYGLASS_WORKLOAD_CLIENT_IDENTITIES"), logger)
	if err != nil {
		return err
	}
	go runDockerRunnerCleanup(ctx, launcher, cleanupInterval, int(cleanupBatch), logger)
	return serveHTTPS(ctx, "docker-runner-launcher", httpAddress(":8443"), secured, serverTLS, logger)
}

func runDockerRunnerCleanup(ctx context.Context, launcher *dockerengine.RunnerContainers, interval time.Duration, batch int, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			removed, err := launcher.CleanupExpired(ctx, now.UTC(), batch)
			if err != nil {
				logger.Error("Clean expired Docker runners", "error", err)
			} else if removed > 0 {
				logger.Info("Cleaned expired Docker runners", "count", removed)
			}
		}
	}
}

func runRunnerBroker(ctx context.Context, logger *slog.Logger) error {
	developmentMode := os.Getenv("SPYGLASS_ENV") == "development"
	databaseURL, err := requiredEnv("SPYGLASS_CELL_DATABASE_URL")
	if err != nil {
		return err
	}
	restoreGate, err := openRequiredRestoreGate(ctx, databaseURL, restoregate.Cell, "SPYGLASS_")
	if err != nil {
		return err
	}
	defer restoreGate.Close()
	brokerAudience, err := requiredEnv("SPYGLASS_RUNNER_BROKER_URL")
	if err != nil {
		return err
	}
	substrate, err := requiredEnv("SPYGLASS_RUNNER_SUBSTRATE")
	if err != nil {
		return err
	}
	var namespace, runnerServiceAccount string
	var identityVerifier runnerbroker.IdentityVerifier
	switch substrate {
	case "kubernetes":
		namespace, err = requiredEnv("SPYGLASS_RUNNER_NAMESPACE")
		if err != nil {
			return err
		}
		runnerServiceAccount, err = requiredEnv("SPYGLASS_RUNNER_SERVICE_ACCOUNT")
		if err != nil {
			return err
		}
	case "docker-stage":
		if !dockerStageEnvironment() {
			return errors.New("docker-stage runner substrate requires stage or local-secure verification environment")
		}
		origin, requiredErr := requiredEnv("SPYGLASS_DOCKER_LAUNCHER_ORIGIN")
		if requiredErr != nil {
			return requiredErr
		}
		token, requiredErr := requiredEnv("SPYGLASS_DOCKER_LAUNCHER_BROKER_TOKEN")
		if requiredErr != nil {
			return requiredErr
		}
		launcherTransport, transportErr := workloadidentity.NewClientTransport(workloadTLSFilesEnv())
		if transportErr != nil {
			return transportErr
		}
		defer launcherTransport.CloseIdleConnections()
		identityVerifier, err = dockerlauncherhttp.New(dockerlauncherhttp.Config{Origin: origin, Token: token, HTTPClient: &http.Client{Transport: observability.TracingFromContext(ctx).Transport(launcherTransport), Timeout: 15 * time.Second, CheckRedirect: rejectOutboundRedirect}})
		if err != nil {
			return err
		}
	default:
		return errors.New("SPYGLASS_RUNNER_SUBSTRATE must be kubernetes or docker-stage")
	}
	keys, activeVersion, err := versionedEncryptionKeysEnv("SPYGLASS_RUNNER_ENCRYPTION_KEYS", "SPYGLASS_RUNNER_ENCRYPTION_ACTIVE_VERSION")
	if err != nil {
		return err
	}
	toolRouterOrigin, err := requiredEnv("SPYGLASS_TOOL_ROUTER_ORIGIN")
	if err != nil {
		return err
	}
	modelGatewayOrigin, err := requiredEnv("SPYGLASS_MODEL_GATEWAY_ORIGIN")
	if err != nil {
		return err
	}
	toolIssuer, err := requiredEnv("SPYGLASS_TOOL_CONTEXT_ISSUER")
	if err != nil {
		return err
	}
	toolSigningKeyID, err := requiredEnv("SPYGLASS_TOOL_CONTEXT_SIGNING_KEY_ID")
	if err != nil {
		return err
	}
	toolSigningKey, err := base64KeyEnv("SPYGLASS_TOOL_CONTEXT_SIGNING_KEY")
	if err != nil {
		return err
	}
	toolLifetime, err := durationEnv("SPYGLASS_TOOL_CONTEXT_TTL", 10*time.Second)
	if err != nil || toolLifetime > 15*time.Second {
		return errors.New("SPYGLASS_TOOL_CONTEXT_TTL must be between 1ns and 15s")
	}
	maxConns, err := int32Env("SPYGLASS_CELL_MAX_DATABASE_CONNS", 10)
	if err != nil {
		return err
	}
	maxBody, err := int64Env("SPYGLASS_RUNNER_BROKER_MAX_REQUEST_BODY_BYTES", int64(2<<20))
	if err != nil || maxBody > 2<<20 {
		return errors.New("SPYGLASS_RUNNER_BROKER_MAX_REQUEST_BODY_BYTES must be between 1 and 2097152")
	}
	stripeSecretKey, err := requiredEnv("SPYGLASS_STRIPE_SECRET_KEY")
	if err != nil {
		return err
	}
	serverTLS, err := workloadidentity.NewServerConfig(workloadTLSFilesEnv())
	if err != nil {
		return err
	}
	var toolTransport http.RoundTripper
	var modelTransport http.RoundTripper
	if !developmentMode {
		toolTransport, err = workloadidentity.NewClientTransport(workloadTLSFilesEnv())
		if err != nil {
			return err
		}
		modelTransport, err = workloadidentity.NewClientTransport(workloadTLSFilesEnv())
		if err != nil {
			return err
		}
	} else {
		toolTransport = http.DefaultTransport
		modelTransport = http.DefaultTransport
	}
	toolTransport = observability.TracingFromContext(ctx).Transport(toolTransport)
	modelTransport = observability.TracingFromContext(ctx).Transport(modelTransport)
	stripeTransport := http.DefaultTransport.(*http.Transport).Clone()
	stripeTransport.Proxy = nil
	stripeHTTPClient := &http.Client{Transport: observability.TracingFromContext(ctx).ExternalTransport(stripeTransport), Timeout: 20 * time.Second, CheckRedirect: rejectOutboundRedirect}
	startup, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	server, err := runnerbrokerbootstrap.New(startup, runnerbrokerbootstrap.Config{
		CellDatabaseURL: databaseURL, BrokerAudience: brokerAudience, Namespace: namespace,
		RunnerServiceAccount: runnerServiceAccount, EncryptionKeys: keys, ActiveKeyVersion: activeVersion,
		IdentityVerifier: identityVerifier,
		MaxDatabaseConns: maxConns, MaxRequestBody: maxBody, ToolRouterOrigin: toolRouterOrigin,
		ToolIssuer: toolIssuer, ToolSigningKeyID: toolSigningKeyID, ToolSigningKey: toolSigningKey,
		ToolLifetime: toolLifetime, ToolTransport: toolTransport, ModelGatewayOrigin: modelGatewayOrigin,
		ModelTransport: modelTransport, StripeSecretKey: stripeSecretKey,
		StripeAPIVersion: os.Getenv("SPYGLASS_STRIPE_API_VERSION"), StripeHTTPClient: stripeHTTPClient,
	}, logger)
	if err != nil {
		return err
	}
	defer server.Close()
	return serveHTTPSWithWriteTimeout(ctx, "runner-broker", httpAddress(":8443"), withRestoreGate([]*restoregate.Gate{restoreGate}, server.Handler), serverTLS, modelGatewayWriteTimeout, logger)
}

func runModelGateway(ctx context.Context, logger *slog.Logger) error {
	developmentMode := os.Getenv("SPYGLASS_ENV") == "development"
	apiKey, err := requiredEnv("SPYGLASS_OPENAI_API_KEY")
	if err != nil {
		return err
	}
	maxBody, err := int64Env("SPYGLASS_MODEL_GATEWAY_MAX_REQUEST_BODY_BYTES", 256<<10)
	if err != nil || maxBody > 256<<10 {
		return errors.New("SPYGLASS_MODEL_GATEWAY_MAX_REQUEST_BODY_BYTES must be between 1 and 262144")
	}
	providerTransport := http.DefaultTransport.(*http.Transport).Clone()
	providerTransport.Proxy = nil
	server, err := modelgatewaybootstrap.New(modelgatewaybootstrap.Config{
		OpenAIAPIKey: apiKey, OpenAIOrigin: os.Getenv("SPYGLASS_OPENAI_ORIGIN"), OpenAIClient: &http.Client{Transport: observability.TracingFromContext(ctx).ExternalTransport(providerTransport), Timeout: modelgateway.MaximumProviderTimeout, CheckRedirect: rejectOutboundRedirect}, MaxRequestBody: maxBody,
	}, logger)
	if err != nil {
		return err
	}
	if developmentMode {
		return serveHTTPWithWriteTimeout(ctx, "model-gateway", httpAddress(":8080"), server.Handler, modelGatewayWriteTimeout, logger)
	}
	serverTLS, err := workloadidentity.NewServerConfig(workloadTLSFilesEnv())
	if err != nil {
		return err
	}
	secured, err := workloadidentity.RequireClientIdentity(server.Handler, csvEnv("SPYGLASS_WORKLOAD_CLIENT_IDENTITIES"), logger)
	if err != nil {
		return err
	}
	return serveHTTPSWithWriteTimeout(ctx, "model-gateway", httpAddress(":8443"), secured, serverTLS, modelGatewayWriteTimeout, logger)
}

func runAgentProjectionWorker(ctx context.Context, logger *slog.Logger) error {
	databaseURL, err := requiredEnv("SPYGLASS_CELL_DATABASE_URL")
	if err != nil {
		return err
	}
	restoreGate, err := openRequiredRestoreGate(ctx, databaseURL, restoregate.Cell, "SPYGLASS_")
	if err != nil {
		return err
	}
	defer restoreGate.Close()
	keys, activeVersion, err := versionedEncryptionKeysEnv("SPYGLASS_RUNNER_ENCRYPTION_KEYS", "SPYGLASS_RUNNER_ENCRYPTION_ACTIVE_VERSION")
	if err != nil {
		return err
	}
	maxConns, err := int32Env("SPYGLASS_CELL_MAX_DATABASE_CONNS", 5)
	if err != nil {
		return err
	}
	poll, err := durationEnv("SPYGLASS_AGENT_PROJECTION_POLL_INTERVAL", time.Second)
	if err != nil || poll < 100*time.Millisecond || poll > time.Minute {
		return errors.New("SPYGLASS_AGENT_PROJECTION_POLL_INTERVAL must be between 100ms and 1m")
	}
	lease, err := durationEnv("SPYGLASS_AGENT_PROJECTION_LEASE", agentprojection.DefaultLease)
	if err != nil || lease < time.Second || lease > 30*time.Minute || lease%time.Second != 0 {
		return errors.New("SPYGLASS_AGENT_PROJECTION_LEASE must be whole seconds between 1s and 30m")
	}
	maxAttempts, err := int32Env("SPYGLASS_AGENT_PROJECTION_MAX_ATTEMPTS", agentprojection.DefaultMaxAttempts)
	if err != nil || maxAttempts > agentprojection.MaximumMaxAttempts {
		return fmt.Errorf("SPYGLASS_AGENT_PROJECTION_MAX_ATTEMPTS must be between 1 and %d", agentprojection.MaximumMaxAttempts)
	}
	startup, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	worker, err := agentprojectionworker.New(startup, agentprojectionworker.Config{
		CellDatabaseURL: databaseURL, MaxDatabaseConns: maxConns, PollInterval: poll, Lease: lease,
		MaxAttempts: int(maxAttempts), EncryptionKeys: keys, ActiveKeyVersion: activeVersion,
	}, logger)
	if err != nil {
		return err
	}
	defer worker.Close()
	return serveWorker(ctx, "agent-projection", envOr("SPYGLASS_HEALTH_ADDRESS", ":8081"), &restoreGatedWorker{worker: worker, gates: []*restoregate.Gate{restoreGate}}, logger)
}

func runKnowledgeDocumentWorker(ctx context.Context, logger *slog.Logger) error {
	databaseURL, err := requiredEnv("SPYGLASS_CELL_DATABASE_URL")
	if err != nil {
		return err
	}
	restoreGate, err := openRequiredRestoreGate(ctx, databaseURL, restoregate.Cell, "SPYGLASS_")
	if err != nil {
		return err
	}
	defer restoreGate.Close()
	maxConns, err := int32Env("SPYGLASS_CELL_MAX_DATABASE_CONNS", 5)
	if err != nil {
		return err
	}
	poll, err := durationEnv("SPYGLASS_KNOWLEDGE_DOCUMENT_POLL_INTERVAL", time.Second)
	if err != nil || poll < 100*time.Millisecond || poll > time.Minute {
		return errors.New("SPYGLASS_KNOWLEDGE_DOCUMENT_POLL_INTERVAL must be between 100ms and 1m")
	}
	lease, err := durationEnv("SPYGLASS_KNOWLEDGE_DOCUMENT_LEASE", knowledgeapp.DefaultDocumentProcessingLease)
	if err != nil || lease < time.Second || lease > 30*time.Minute || lease%time.Second != 0 {
		return errors.New("SPYGLASS_KNOWLEDGE_DOCUMENT_LEASE must be whole seconds between 1s and 30m")
	}
	maxAttempts, err := int32Env("SPYGLASS_KNOWLEDGE_DOCUMENT_MAX_ATTEMPTS", knowledgeapp.DefaultDocumentProcessingMaxAttempts)
	if err != nil || maxAttempts > knowledgeapp.MaximumDocumentProcessingMaxAttempts {
		return fmt.Errorf("SPYGLASS_KNOWLEDGE_DOCUMENT_MAX_ATTEMPTS must be between 1 and %d", knowledgeapp.MaximumDocumentProcessingMaxAttempts)
	}
	objectSecure, err := boolEnv("SPYGLASS_OBJECT_STORE_SECURE", false)
	if err != nil {
		return err
	}
	objectSSE, err := boolEnv("SPYGLASS_OBJECT_STORE_SERVER_SIDE_ENCRYPTION", true)
	if err != nil {
		return err
	}
	malwareTimeout, err := durationEnv("SPYGLASS_CLAMAV_OPERATION_TIMEOUT", 2*time.Minute)
	if err != nil || malwareTimeout > 5*time.Minute {
		return errors.New("SPYGLASS_CLAMAV_OPERATION_TIMEOUT must be at most 5m")
	}
	extractorTimeout, err := durationEnv("SPYGLASS_TIKA_TIMEOUT", 2*time.Minute)
	if err != nil || extractorTimeout > 5*time.Minute {
		return errors.New("SPYGLASS_TIKA_TIMEOUT must be at most 5m")
	}
	config := knowledgedocumentworker.Config{
		CellDatabaseURL: databaseURL, MaxDatabaseConns: maxConns, PollInterval: poll, Lease: lease, MaxAttempts: int(maxAttempts),
		ObjectEndpoint: envOr("SPYGLASS_OBJECT_STORE_ENDPOINT", "object-store:9000"), ObjectRegion: os.Getenv("SPYGLASS_OBJECT_STORE_REGION"),
		ObjectBucket: envOr("SPYGLASS_OBJECT_STORE_BUCKET", "spyglass-documents"), ObjectSecure: objectSecure, ObjectSSE: objectSSE,
		MalwareAddress: envOr("SPYGLASS_CLAMAV_ADDRESS", "malware-scanner:3310"), MalwareTimeout: malwareTimeout,
		ExtractorEndpoint: envOr("SPYGLASS_TIKA_ENDPOINT", "http://document-extractor:9998"), ExtractorTimeout: extractorTimeout,
	}
	config.ObjectAccessKey, err = requiredEnv("SPYGLASS_OBJECT_STORE_ACCESS_KEY")
	if err != nil {
		return err
	}
	config.ObjectSecretKey, err = requiredEnv("SPYGLASS_OBJECT_STORE_SECRET_KEY")
	if err != nil {
		return err
	}
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	worker, err := knowledgedocumentworker.New(startup, config, logger)
	if err != nil {
		return err
	}
	defer worker.Close()
	return serveWorker(ctx, "knowledge-document", envOr("SPYGLASS_HEALTH_ADDRESS", ":8081"), &restoreGatedWorker{worker: worker, gates: []*restoregate.Gate{restoreGate}}, logger)
}

func runBaselineMaintenanceWorker(ctx context.Context, logger *slog.Logger) error {
	globalDatabaseURL, err := requiredEnv("SPYGLASS_GLOBAL_DATABASE_URL")
	if err != nil {
		return err
	}
	cellDatabaseURL, err := requiredEnv("SPYGLASS_CELL_DATABASE_URL")
	if err != nil {
		return err
	}
	globalRestore, err := openRequiredRestoreGate(ctx, globalDatabaseURL, restoregate.Global, "SPYGLASS_GLOBAL_")
	if err != nil {
		return err
	}
	defer globalRestore.Close()
	cellRestore, err := openRequiredRestoreGate(ctx, cellDatabaseURL, restoregate.Cell, "SPYGLASS_CELL_")
	if err != nil {
		return err
	}
	defer cellRestore.Close()
	cellID, err := requiredEnv("SPYGLASS_CELL_ID")
	if err != nil {
		return err
	}
	globalConns, err := int32Env("SPYGLASS_GLOBAL_MAX_DATABASE_CONNS", 3)
	if err != nil {
		return err
	}
	cellConns, err := int32Env("SPYGLASS_CELL_MAX_DATABASE_CONNS", 3)
	if err != nil {
		return err
	}
	poll, err := durationEnv("SPYGLASS_BASELINE_MAINTENANCE_POLL_INTERVAL", time.Second)
	if err != nil || poll < 100*time.Millisecond || poll > time.Minute {
		return errors.New("SPYGLASS_BASELINE_MAINTENANCE_POLL_INTERVAL must be between 100ms and 1m")
	}
	lease, err := durationEnv("SPYGLASS_BASELINE_MAINTENANCE_LEASE", baselinemaintenanceapp.DefaultLease)
	if err != nil || lease < time.Second || lease > 30*time.Minute || lease%time.Second != 0 {
		return errors.New("SPYGLASS_BASELINE_MAINTENANCE_LEASE must be whole seconds between 1s and 30m")
	}
	maxAttempts, err := int32Env("SPYGLASS_BASELINE_MAINTENANCE_MAX_ATTEMPTS", baselinemaintenanceapp.DefaultMaxAttempts)
	if err != nil || maxAttempts < 1 || maxAttempts > 100 {
		return errors.New("SPYGLASS_BASELINE_MAINTENANCE_MAX_ATTEMPTS must be between 1 and 100")
	}
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	worker, err := baselinemaintenanceworker.New(startup, baselinemaintenanceworker.Config{
		GlobalDatabaseURL: globalDatabaseURL, CellDatabaseURL: cellDatabaseURL, CellID: ids.CellID(cellID),
		MaxGlobalConns: globalConns, MaxCellConns: cellConns, PollInterval: poll, Lease: lease, MaxAttempts: int(maxAttempts),
	}, logger)
	if err != nil {
		return err
	}
	defer worker.Close()
	return serveWorker(ctx, "baseline-maintenance", envOr("SPYGLASS_HEALTH_ADDRESS", ":8081"), &restoreGatedWorker{worker: worker, gates: []*restoregate.Gate{globalRestore, cellRestore}}, logger)
}

func runAgentDispatchWorker(ctx context.Context, logger *slog.Logger) error {
	developmentMode := os.Getenv("SPYGLASS_ENV") == "development"
	databaseURL, err := requiredEnv("SPYGLASS_CELL_DATABASE_URL")
	if err != nil {
		return err
	}
	restoreGate, err := openRequiredRestoreGate(ctx, databaseURL, restoregate.Cell, "SPYGLASS_")
	if err != nil {
		return err
	}
	defer restoreGate.Close()
	cellID, err := requiredEnv("SPYGLASS_CELL_ID")
	if err != nil {
		return err
	}
	admissionOrigin, err := requiredEnv("SPYGLASS_WORK_ADMISSION_ORIGIN")
	if err != nil {
		return err
	}
	var admissionTransport http.RoundTripper
	if !developmentMode {
		admissionTransport, err = workloadidentity.NewClientTransport(workloadTLSFilesEnv())
		if err != nil {
			return err
		}
	}
	admissionTransport = observability.TracingFromContext(ctx).Transport(admissionTransport)
	keys, activeVersion, err := versionedEncryptionKeysEnv("SPYGLASS_RUNNER_ENCRYPTION_KEYS", "SPYGLASS_RUNNER_ENCRYPTION_ACTIVE_VERSION")
	if err != nil {
		return err
	}
	maxConns, err := int32Env("SPYGLASS_CELL_MAX_DATABASE_CONNS", 5)
	if err != nil {
		return err
	}
	poll, err := durationEnv("SPYGLASS_AGENT_DISPATCH_POLL_INTERVAL", time.Second)
	if err != nil || poll < 100*time.Millisecond || poll > time.Minute {
		return errors.New("SPYGLASS_AGENT_DISPATCH_POLL_INTERVAL must be between 100ms and 1m")
	}
	lease, err := durationEnv("SPYGLASS_AGENT_DISPATCH_LEASE", agentdispatch.DefaultLease)
	if err != nil || lease < time.Second || lease > 30*time.Minute || lease%time.Second != 0 {
		return errors.New("SPYGLASS_AGENT_DISPATCH_LEASE must be whole seconds between 1s and 30m")
	}
	maxAttempts, err := int32Env("SPYGLASS_AGENT_DISPATCH_MAX_ATTEMPTS", agentdispatch.DefaultMaxAttempts)
	if err != nil || maxAttempts > agentdispatch.MaximumMaxAttempts {
		return fmt.Errorf("SPYGLASS_AGENT_DISPATCH_MAX_ATTEMPTS must be between 1 and %d", agentdispatch.MaximumMaxAttempts)
	}
	startup, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	worker, err := agentdispatchworker.New(startup, agentdispatchworker.Config{
		CellDatabaseURL: databaseURL, CellID: ids.CellID(cellID), AdmissionOrigin: admissionOrigin,
		AdmissionTransport: admissionTransport, AllowHTTPAdmission: developmentMode,
		MaxDatabaseConns: maxConns, PollInterval: poll, Lease: lease,
		MaxAttempts: int(maxAttempts), EncryptionKeys: keys, ActiveKeyVersion: activeVersion,
	}, logger)
	if err != nil {
		return err
	}
	defer worker.Close()
	return serveWorker(ctx, "agent-dispatch", envOr("SPYGLASS_HEALTH_ADDRESS", ":8081"), &restoreGatedWorker{worker: worker, gates: []*restoregate.Gate{restoreGate}}, logger)
}

func runRouteReceiptWorker(ctx context.Context, logger *slog.Logger) error {
	databaseURL, err := requiredEnv("SPYGLASS_DATABASE_URL")
	if err != nil {
		return err
	}
	restoreGate, err := openRequiredRestoreGate(ctx, databaseURL, restoregate.Cell, "SPYGLASS_")
	if err != nil {
		return err
	}
	defer restoreGate.Close()
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
	return serveWorker(ctx, "route-receipt", envOr("SPYGLASS_HEALTH_ADDRESS", ":8081"), &restoreGatedWorker{worker: worker, gates: []*restoregate.Gate{restoreGate}}, logger)
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
	tracedTransport := observability.TracingFromContext(ctx).Transport(transport)
	probeContext, cancel := context.WithTimeout(ctx, timeout+time.Second)
	defer cancel()
	config := routecanary.Config{
		Origin: origin, CellID: ids.CellID(cellID), AccountID: ids.AccountID(accountID),
		PlacementGeneration: generation, EntitlementVersion: entitlementVersion,
		Issuer: issuer, KeyID: keyID, SigningKey: key, Timeout: timeout,
		Transport: tracedTransport, Clock: registration.SystemClock{}, IDs: ids.RandomGenerator{},
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
	httpServer := newHTTPServer(healthAddress, observability.TracingFromContext(ctx).Handler(workerHealth(name, worker)))
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
	passkeyEncryptionKeys                                                                                                 map[int][]byte
	passkeyActiveKeyVersion                                                                                               int
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
	result.passkeyEncryptionKeys, result.passkeyActiveKeyVersion, err = versionedEncryptionKeysEnv("SPYGLASS_PASSKEY_ENCRYPTION_KEYS", "SPYGLASS_PASSKEY_ENCRYPTION_ACTIVE_VERSION")
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

func versionedEncryptionKeysEnv(keysName, activeName string) (map[int][]byte, int, error) {
	values, err := keyValueEnv(keysName)
	if err != nil {
		return nil, 0, err
	}
	activeRaw, err := requiredEnv(activeName)
	if err != nil {
		return nil, 0, err
	}
	active, err := strconv.Atoi(activeRaw)
	if err != nil || active <= 0 {
		return nil, 0, fmt.Errorf("%s must be a positive integer", activeName)
	}
	result := make(map[int][]byte, len(values))
	for rawVersion, encoded := range values {
		version, err := strconv.Atoi(rawVersion)
		if err != nil || version <= 0 {
			return nil, 0, fmt.Errorf("%s version %q must be a positive integer", keysName, rawVersion)
		}
		if _, exists := result[version]; exists {
			return nil, 0, fmt.Errorf("%s contains duplicate normalized version %d", keysName, version)
		}
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(decoded) != 32 {
			return nil, 0, fmt.Errorf("%s version %d must be standard base64 encoding of exactly 32 bytes", keysName, version)
		}
		result[version] = decoded
	}
	if _, exists := result[active]; !exists {
		return nil, 0, fmt.Errorf("%s version %d is absent from %s", activeName, active, keysName)
	}
	return result, active, nil
}

func requireOperatorAuthorization(logger *slog.Logger, mode, action, actor, reason, environment, scope string) (string, error) {
	if logger == nil {
		return "", errors.New("operator authorization logger is required")
	}
	keys, err := routeVerifyKeysEnv("SPYGLASS_OPERATOR_AUTH_VERIFY_KEYS")
	if err != nil {
		return "", err
	}
	issuer, err := requiredEnv("SPYGLASS_OPERATOR_AUTH_ISSUER")
	if err != nil {
		return "", err
	}
	expectedAction := mode + ":" + action
	logger.Info("Operator authorization scope prepared", "actor", actor, "action", expectedAction, "environment", environment,
		"reason_sha256", operatorauth.Digest(reason), "scope_sha256", operatorauth.Digest(scope))
	token, err := requiredEnv("SPYGLASS_OPERATOR_AUTHORIZATION")
	if err != nil {
		return "", err
	}
	allowBreakGlass := false
	if raw := os.Getenv("SPYGLASS_ALLOW_BREAK_GLASS"); raw != "" {
		if raw != "true" {
			return "", errors.New("SPYGLASS_ALLOW_BREAK_GLASS must be exactly true when set")
		}
		confirmation, err := requiredEnv("SPYGLASS_CONFIRM_BREAK_GLASS_ENVIRONMENT")
		if err != nil {
			return "", err
		}
		if confirmation != environment {
			return "", errors.New("SPYGLASS_CONFIRM_BREAK_GLASS_ENVIRONMENT must exactly match SPYGLASS_ENVIRONMENT")
		}
		allowBreakGlass = true
	}
	evidence, err := operatorauth.Verify(operatorauth.Request{
		Token: token, VerifyKeys: keys, Issuer: issuer, Actor: actor, Action: expectedAction,
		Environment: environment, Reason: reason, Scope: scope, AllowBreakGlass: allowBreakGlass, Now: time.Now().UTC(),
	})
	if err != nil {
		return "", errors.New("operator authorization was rejected")
	}
	auditedReason := reason + " [authorization=" + evidence.AuthorizationID + ";mode=" + evidence.Mode + "]"
	if len(auditedReason) > 500 {
		return "", errors.New("SPYGLASS_OPERATOR_REASON is too long after authorization evidence is attached")
	}
	fields := []any{"authorization_id", evidence.AuthorizationID, "mode", evidence.Mode, "key_id", evidence.KeyID, "expires_at", evidence.ExpiresAt}
	if evidence.IncidentID != "" {
		fields = append(fields, "incident_id", evidence.IncidentID, "approval_count", len(evidence.Approvers))
	}
	logger.Info("Operator authorization verified", fields...)
	return auditedReason, nil
}

func operatorScope(values map[string]string) string {
	raw, err := json.Marshal(values)
	if err != nil {
		panic("operator authorization scope is not serializable: " + err.Error())
	}
	return string(raw)
}

func encryptionKeyVersions(values map[int][]byte) string {
	versions := make([]int, 0, len(values))
	for version := range values {
		versions = append(versions, version)
	}
	sort.Ints(versions)
	parts := make([]string, len(versions))
	for index, version := range versions {
		parts[index] = strconv.Itoa(version)
	}
	return strings.Join(parts, ",")
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func formatOptionalTimePointer(value *time.Time) string {
	if value == nil {
		return ""
	}
	return formatOptionalTime(*value)
}

func serveHTTP(ctx context.Context, service, address string, handler http.Handler, logger *slog.Logger) error {
	return serveHTTPWithWriteTimeout(ctx, service, address, handler, 30*time.Second, logger)
}

func serveHTTPWithWriteTimeout(ctx context.Context, service, address string, handler http.Handler, writeTimeout time.Duration, logger *slog.Logger) error {
	if writeTimeout <= 0 {
		return errors.New("HTTP write timeout must be positive")
	}
	metrics, err := observability.NewHTTPMetrics(service)
	if err != nil {
		return err
	}
	server := newHTTPServer(address, observability.TracingFromContext(ctx).Handler(metrics.Handler(handler)))
	server.WriteTimeout = writeTimeout
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

func serveHTTPS(ctx context.Context, service, address string, handler http.Handler, config *tls.Config, logger *slog.Logger) error {
	return serveHTTPSWithWriteTimeout(ctx, service, address, handler, config, 30*time.Second, logger)
}

func serveHTTPSWithWriteTimeout(ctx context.Context, service, address string, handler http.Handler, config *tls.Config, writeTimeout time.Duration, logger *slog.Logger) error {
	if config == nil {
		return errors.New("workload TLS server configuration is required")
	}
	if writeTimeout <= 0 {
		return errors.New("HTTPS write timeout must be positive")
	}
	metrics, err := observability.NewHTTPMetrics(service)
	if err != nil {
		return err
	}
	server := newHTTPServer(address, observability.TracingFromContext(ctx).Handler(metrics.Handler(handler)))
	server.WriteTimeout = writeTimeout
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

func workerHealth(name string, worker readiness) http.Handler {
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
		reporter, ok := workerStatusReporter(worker)
		if !ok {
			http.NotFound(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := worker.Ready(ctx); err != nil {
			http.Error(w, `{"status":"unavailable"}`, http.StatusServiceUnavailable)
			return
		}
		status, err := reporter.Status(ctx)
		if err != nil {
			http.Error(w, `{"status":"unavailable"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(status)
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		reporter, ok := workerStatusReporter(worker)
		if !ok {
			http.NotFound(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := worker.Ready(ctx); err != nil {
			http.Error(w, "metrics unavailable", http.StatusServiceUnavailable)
			return
		}
		status, err := reporter.Status(ctx)
		if err != nil {
			http.Error(w, "metrics unavailable", http.StatusServiceUnavailable)
			return
		}
		metrics, err := observability.RenderWorkerMetrics(name, status)
		if err != nil {
			http.Error(w, "metrics unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write(metrics)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}

func workerStatusReporter(worker readiness) (statusReporter, bool) {
	reporter, ok := worker.(statusReporter)
	if !ok {
		if gated, gatedOK := worker.(*restoreGatedWorker); gatedOK {
			reporter, ok = gated.worker.(statusReporter)
		}
	}
	return reporter, ok
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
func boolEnv(name string, fallback bool) (bool, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean", name)
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

func tracingFromEnvironment(ctx context.Context, service string, release buildinfo.Info) (*observability.Tracing, error) {
	endpoint := os.Getenv("SPYGLASS_OTEL_TRACES_ENDPOINT")
	if endpoint == "" {
		return observability.DisabledTracing(), nil
	}
	if service == "runner-invocation" {
		return nil, errors.New("runner-invocation trace export is not supported by the sandbox credential boundary")
	}
	environment, err := requiredEnv("SPYGLASS_ENVIRONMENT")
	if err != nil {
		return nil, err
	}
	ratioRaw, err := requiredEnv("SPYGLASS_OTEL_TRACE_SAMPLE_RATIO")
	if err != nil {
		return nil, err
	}
	ratio, err := strconv.ParseFloat(ratioRaw, 64)
	if err != nil || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
		return nil, errors.New("SPYGLASS_OTEL_TRACE_SAMPLE_RATIO must be a finite number")
	}
	accountKey, err := base64KeyEnv("SPYGLASS_TRACE_ACCOUNT_HASH_KEY")
	if err != nil {
		return nil, err
	}
	transport, err := workloadidentity.NewClientTransport(workloadTLSFilesEnv())
	if err != nil {
		return nil, fmt.Errorf("configure trace exporter workload identity: %w", err)
	}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: rejectOutboundRedirect}
	tracing, err := observability.NewTracing(ctx, observability.TracingConfig{Service: service, Environment: environment, Revision: release.Revision, CellID: os.Getenv("SPYGLASS_CELL_ID"), Endpoint: endpoint, SampleRatio: ratio, AccountHashKey: accountKey, HTTPClient: client})
	if err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}
	return tracing, nil
}

func rejectOutboundRedirect(*http.Request, []*http.Request) error {
	return errors.New("outbound redirects are not allowed")
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

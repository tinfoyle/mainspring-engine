package development

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/memory"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/placement"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/transport/httpapi"
)

func Handler(logger *slog.Logger) http.Handler {
	clock := registration.SystemClock{}
	publishedCatalog := catalog.Default(clock.Now())
	store := memory.NewStore(publishedCatalog, []placement.Cell{{ID: ids.CellID("cell-us-east-01"), Region: "us-east", State: "active", SoftLimit: 1000}})
	verification := &memory.VerificationSink{}
	service := registration.NewService(store, verification, store, publishedCatalog, ids.RandomGenerator{}, clock)
	options := make([]httpapi.Option, 0, 1)
	if secret := os.Getenv("SPYGLASS_STRIPE_WEBHOOK_SECRET"); secret != "" {
		verifier, err := billing.NewSignatureVerifier(secret, 5*time.Minute, clock)
		if err != nil {
			panic(err)
		}
		webhook, err := billing.NewWebhookService(verifier, memory.NewBillingInbox(), "test", clock)
		if err != nil {
			panic(err)
		}
		options = append(options, httpapi.WithBillingWebhook(webhook))
	}
	return httpapi.NewServer(service, store.Catalog, verification, true, logger, options...).Handler()
}

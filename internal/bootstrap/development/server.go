package development

import (
	"log/slog"
	"net/http"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/memory"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
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
	return httpapi.NewServer(service, store.Catalog, verification, true, logger).Handler()
}

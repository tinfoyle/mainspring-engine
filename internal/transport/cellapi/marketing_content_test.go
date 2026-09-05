package cellapi

import (
	"context"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"
)

type marketingDownloadStub struct{ calls int }

func (stub *marketingDownloadStub) Download(context.Context, access.Actor, ids.AccountID, ids.MarketingCampaignID, ids.MarketingAssetRevisionID) (domain.AssetRevision, []byte, error) {
	stub.calls++
	return domain.AssetRevision{Title: "Audit content", MediaType: "text/html"}, []byte("<script>untrusted()</script>"), nil
}
func TestMarketingDownloadIsAccountBoundAndAlwaysAnAttachment(t *testing.T) {
	content := &marketingDownloadStub{}
	server, err := New(claimAcceptor{claims: marketingQueryClaims()}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithMarketing(&marketingQueryTransportService{now: time.Now()}), WithMarketingContent(content))
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/accounts/" + marketingAccountID + "/marketing/campaigns/" + marketingCampaignID + "/asset-revisions/" + marketingAssetRevisionID + "/content"
	response := marketingQueryRequest(server.Handler(), path)
	if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Disposition"), "attachment;") || response.Header().Get("Content-Type") != "application/octet-stream" || response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("download=%d %v", response.Code, response.Header())
	}
	response = marketingQueryRequest(server.Handler(), strings.Replace(path, marketingAccountID, marketingUserID, 1))
	if response.Code != http.StatusNotFound || content.calls != 1 {
		t.Fatalf("cross-account download=%d calls=%d", response.Code, content.calls)
	}
}

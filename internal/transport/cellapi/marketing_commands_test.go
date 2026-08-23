package cellapi

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	marketingapp "github.com/tinfoyle/spyglass-engine/internal/application/marketing"
	marketingdomain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	"github.com/tinfoyle/spyglass-engine/internal/testsupport/openapifixture"
)

const (
	marketingOperationID = "a7000000-0000-4000-8000-000000000007"
	marketingApprovalID  = "a8000000-0000-4000-8000-000000000008"
)

type marketingCommandTransportService struct {
	called     string
	version    uint64
	digest     [32]byte
	provenance marketingdomain.Provenance
	releaseID  ids.MarketingReleaseID
	approvalID ids.ConsequentialApprovalID
	assetID    ids.MarketingAssetID
}

func marketingCommandCampaign(version uint64) marketingdomain.Campaign {
	now := time.Date(2026, 8, 23, 2, 0, 0, 0, time.UTC)
	return marketingdomain.Campaign{ID: marketingCampaignID, AccountID: marketingAccountID, Name: "Autumn launch", Objective: "Introduce the governed release",
		Audience: "Existing operators", Channels: []marketingdomain.Channel{marketingdomain.ChannelEmail, marketingdomain.ChannelWeb}, State: marketingdomain.CampaignDraft,
		Version: version, CreatedBy: marketingdomain.Actor{Kind: marketingdomain.ActorUser, ID: marketingUserID}, Provenance: marketingdomain.Provenance{Origin: marketingdomain.OriginHuman},
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now}
}

func marketingCommandRelease(version uint64) marketingdomain.ReleasePlan {
	now := time.Date(2026, 8, 23, 2, 0, 0, 0, time.UTC)
	return marketingdomain.ReleasePlan{ID: marketingReleaseID, AccountID: marketingAccountID, CampaignID: marketingCampaignID, CampaignVersion: 2,
		Name: "Autumn release", Channels: []marketingdomain.Channel{marketingdomain.ChannelEmail, marketingdomain.ChannelWeb},
		AssetRevisionIDs: []ids.MarketingAssetRevisionID{marketingAssetRevisionID}, State: marketingdomain.ReleaseDraft, Version: version,
		CreatedBy: marketingdomain.Actor{Kind: marketingdomain.ActorUser, ID: marketingUserID}, Provenance: marketingdomain.Provenance{Origin: marketingdomain.OriginHuman},
		CreatedAt: now, UpdatedAt: now}
}

func (service *marketingCommandTransportService) CreateCampaign(_ context.Context, command marketingapp.CreateCampaignCommand) (marketingdomain.Campaign, bool, error) {
	service.called, service.provenance = "campaign_create", command.Provenance
	return marketingCommandCampaign(1), true, nil
}

func (service *marketingCommandTransportService) ReviseCampaign(_ context.Context, command marketingapp.ReviseCampaignCommand) (marketingdomain.Campaign, error) {
	service.called, service.version = "campaign_revise", command.ExpectedVersion
	return marketingCommandCampaign(command.ExpectedVersion + 1), nil
}

func (service *marketingCommandTransportService) CreateAssetRevision(_ context.Context, command marketingapp.CreateAssetRevisionCommand) (marketingdomain.AssetRevision, bool, error) {
	service.called, service.digest, service.provenance, service.assetID = "asset_create", command.ContentSHA256, command.Provenance, command.AssetID
	value := marketingdomain.AssetRevision{ID: marketingAssetRevisionID, AccountID: marketingAccountID, CampaignID: marketingCampaignID, AssetID: command.AssetID,
		Revision: 1, Kind: command.Kind, Title: command.Title, MediaType: command.MediaType, ContentReference: command.ContentReference,
		ContentSHA256: command.ContentSHA256, ContentBytes: command.ContentBytes, AlternativeText: command.AlternativeText,
		CreatedBy: marketingdomain.Actor{Kind: marketingdomain.ActorUser, ID: marketingUserID}, Provenance: command.Provenance, CreatedAt: time.Date(2026, 8, 23, 2, 0, 0, 0, time.UTC)}
	return value, true, nil
}

func (service *marketingCommandTransportService) CreateRelease(_ context.Context, command marketingapp.CreateReleaseCommand) (marketingdomain.ReleasePlan, bool, error) {
	service.called, service.provenance = "release_create", command.Provenance
	return marketingCommandRelease(1), true, nil
}

func (service *marketingCommandTransportService) SubmitRelease(_ context.Context, command marketingapp.ReleaseTransitionCommand) (marketingdomain.ReleasePlan, error) {
	service.called, service.version = "release_submit", command.ExpectedVersion
	return marketingCommandRelease(command.ExpectedVersion + 1), nil
}

func (service *marketingCommandTransportService) ApproveRelease(_ context.Context, command marketingapp.ReleaseTransitionCommand) (marketingdomain.ReleasePlan, error) {
	service.called, service.version, service.approvalID = "release_approve", command.ExpectedVersion, command.ApprovalID
	return marketingCommandRelease(command.ExpectedVersion + 1), nil
}

func (service *marketingCommandTransportService) CancelRelease(_ context.Context, command marketingapp.ReleaseTransitionCommand) (marketingdomain.ReleasePlan, error) {
	service.called, service.version = "release_cancel", command.ExpectedVersion
	return marketingCommandRelease(command.ExpectedVersion + 1), nil
}

func (service *marketingCommandTransportService) ActivateCampaign(_ context.Context, command marketingapp.CampaignTransitionCommand) (marketingdomain.Campaign, error) {
	service.called, service.version, service.releaseID = "campaign_activate", command.ExpectedVersion, command.ReleaseID
	return marketingCommandCampaign(command.ExpectedVersion + 1), nil
}

func (service *marketingCommandTransportService) PauseCampaign(_ context.Context, command marketingapp.CampaignTransitionCommand) (marketingdomain.Campaign, error) {
	service.called, service.version = "campaign_pause", command.ExpectedVersion
	return marketingCommandCampaign(command.ExpectedVersion + 1), nil
}

func (service *marketingCommandTransportService) CompleteCampaign(_ context.Context, command marketingapp.CampaignTransitionCommand) (marketingdomain.Campaign, error) {
	service.called, service.version = "campaign_complete", command.ExpectedVersion
	return marketingCommandCampaign(command.ExpectedVersion + 1), nil
}

func (service *marketingCommandTransportService) ArchiveCampaign(_ context.Context, command marketingapp.CampaignTransitionCommand) (marketingdomain.Campaign, error) {
	service.called, service.version = "campaign_archive", command.ExpectedVersion
	return marketingCommandCampaign(command.ExpectedVersion + 1), nil
}

func TestMarketingCommandRoutesBindReplayVersionAndTypedBodies(t *testing.T) {
	service := &marketingCommandTransportService{}
	server, err := New(claimAcceptor{claims: marketingCommandClaims()}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithMarketingCommands(service))
	if err != nil {
		t.Fatal(err)
	}
	contract, err := openapifixture.Load(filepath.Join("..", "..", "..", "api", "spyglass.openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/accounts/" + marketingAccountID + "/marketing"
	digest := strings.Repeat("11", 32)
	tests := []struct {
		name, method, target, body, wantCall string
		version                              uint64
		status                               int
	}{
		{name: "create campaign", method: http.MethodPost, target: base + "/campaigns", body: `{"name":"Autumn launch","objective":"Introduce the governed release","audience":"Existing operators","channels":["email","web"]}`, wantCall: "campaign_create", status: http.StatusCreated},
		{name: "revise campaign", method: http.MethodPut, target: base + "/campaigns/" + marketingCampaignID, body: `{"name":"Autumn launch","objective":"Introduce the governed release","audience":"Existing operators","channels":["email","web"]}`, wantCall: "campaign_revise", version: 2, status: http.StatusOK},
		{name: "archive campaign", method: http.MethodDelete, target: base + "/campaigns/" + marketingCampaignID, wantCall: "campaign_archive", version: 2, status: http.StatusOK},
		{name: "create asset revision", method: http.MethodPost, target: base + "/campaigns/" + marketingCampaignID + "/asset-revisions", body: `{"asset_id":"` + marketingAssetID + `","kind":"image","title":"Campaign hero","media_type":"image/png","content_reference":"objects/marketing/hero.png","content_sha256":"` + digest + `","content_bytes":2048,"alternative_text":"Spyglass campaign hero"}`, wantCall: "asset_create", status: http.StatusCreated},
		{name: "create release", method: http.MethodPost, target: base + "/campaigns/" + marketingCampaignID + "/releases", body: `{"campaign_version":2,"name":"Autumn release","channels":["email","web"],"asset_revision_ids":["` + marketingAssetRevisionID + `"]}`, wantCall: "release_create", status: http.StatusCreated},
		{name: "submit release", method: http.MethodPost, target: base + "/releases/" + marketingReleaseID + "/submissions", body: `{"campaign_version":2}`, wantCall: "release_submit", version: 1, status: http.StatusOK},
		{name: "approve release", method: http.MethodPost, target: base + "/releases/" + marketingReleaseID + "/approvals", body: `{"approval_id":"` + marketingApprovalID + `"}`, wantCall: "release_approve", version: 2, status: http.StatusOK},
		{name: "cancel release", method: http.MethodPost, target: base + "/releases/" + marketingReleaseID + "/cancellations", wantCall: "release_cancel", version: 3, status: http.StatusOK},
		{name: "activate campaign", method: http.MethodPost, target: base + "/campaigns/" + marketingCampaignID + "/activations", body: `{"release_id":"` + marketingReleaseID + `"}`, wantCall: "campaign_activate", version: 2, status: http.StatusOK},
		{name: "pause campaign", method: http.MethodPost, target: base + "/campaigns/" + marketingCampaignID + "/pauses", wantCall: "campaign_pause", version: 3, status: http.StatusOK},
		{name: "complete campaign", method: http.MethodPost, target: base + "/campaigns/" + marketingCampaignID + "/completions", wantCall: "campaign_complete", version: 4, status: http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := marketingCommandRequest(server.Handler(), test.method, test.target, test.body, test.version)
			if response.Code != test.status || service.called != test.wantCall {
				t.Fatalf("status=%d body=%s called=%s", response.Code, response.Body.String(), service.called)
			}
			if test.version != 0 && service.version != test.version {
				t.Fatalf("version=%d want=%d", service.version, test.version)
			}
			if err := contract.ValidateResponse(test.method, test.target, response.Code, response.Header(), response.Body.Bytes()); err != nil {
				t.Fatal(err)
			}
		})
	}
	if service.provenance.Origin != marketingdomain.OriginHuman || service.assetID != marketingAssetID || service.digest[0] != 0x11 || service.digest[31] != 0x11 ||
		service.approvalID != marketingApprovalID || service.releaseID != marketingReleaseID {
		t.Fatalf("transport mapping provenance=%+v asset=%s digest=%x approval=%s release=%s", service.provenance, service.assetID, service.digest, service.approvalID, service.releaseID)
	}
}

func TestMarketingCommandsFailClosedOnAuthorityVersionAndDigest(t *testing.T) {
	service := &marketingCommandTransportService{}
	server, _ := New(claimAcceptor{claims: marketingCommandClaims()}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithMarketingCommands(service))
	base := "/api/v1/accounts/" + marketingAccountID + "/marketing"
	missingVersion := marketingCommandRequest(server.Handler(), http.MethodPut, base+"/campaigns/"+marketingCampaignID, `{}`, 0)
	if missingVersion.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing version=%d body=%s", missingVersion.Code, missingVersion.Body.String())
	}
	invalidDigest := marketingCommandRequest(server.Handler(), http.MethodPost, base+"/campaigns/"+marketingCampaignID+"/asset-revisions",
		`{"asset_id":"`+marketingAssetID+`","kind":"image","title":"Hero","media_type":"image/png","content_reference":"hero","content_sha256":"`+strings.Repeat("AA", 32)+`","content_bytes":1,"alternative_text":"Hero"}`, 0)
	if invalidDigest.Code != http.StatusBadRequest {
		t.Fatalf("invalid digest=%d body=%s", invalidDigest.Code, invalidDigest.Body.String())
	}
	wrongAccount := marketingCommandRequest(server.Handler(), http.MethodPost, "/api/v1/accounts/a9000000-0000-4000-8000-000000000009/marketing/campaigns", `{}`, 0)
	if wrongAccount.Code != http.StatusNotFound {
		t.Fatalf("cross Account=%d body=%s", wrongAccount.Code, wrongAccount.Body.String())
	}
}

func marketingCommandRequest(handler http.Handler, method, target, body string, version uint64) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set(RouteContextHeader, "accepted-by-test-boundary")
	request.Header.Set("Idempotency-Key", marketingOperationID)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if version != 0 {
		request.Header.Set("If-Match", `W/"`+fmt.Sprint(version)+`"`)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func marketingCommandClaims() routecontext.Claims {
	claims := marketingQueryClaims()
	claims.Authority.Role = "owner"
	claims.Authority.OperationID = marketingOperationID
	return claims
}

var _ MarketingCommandService = (*marketingCommandTransportService)(nil)

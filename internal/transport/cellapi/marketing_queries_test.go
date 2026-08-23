package cellapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	marketingapp "github.com/tinfoyle/spyglass-engine/internal/application/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	marketingdomain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	"github.com/tinfoyle/spyglass-engine/internal/testsupport/openapifixture"
)

const (
	marketingAccountID       = "a1000000-0000-4000-8000-000000000001"
	marketingUserID          = "a2000000-0000-4000-8000-000000000002"
	marketingCampaignID      = "a3000000-0000-4000-8000-000000000003"
	marketingAssetID         = "a4000000-0000-4000-8000-000000000004"
	marketingAssetRevisionID = "a5000000-0000-4000-8000-000000000005"
	marketingReleaseID       = "a6000000-0000-4000-8000-000000000006"
)

type marketingQueryTransportService struct {
	campaignQuery marketingapp.CampaignListQuery
	assetQuery    marketingapp.AssetRevisionListQuery
	releaseQuery  marketingapp.ReleaseListQuery
	now           time.Time
}

func (service *marketingQueryTransportService) campaign() marketingdomain.Campaign {
	return marketingdomain.Campaign{ID: marketingCampaignID, AccountID: marketingAccountID, Name: "Autumn launch", Objective: "Introduce the governed release",
		Audience: "Existing operators", Channels: []marketingdomain.Channel{marketingdomain.ChannelEmail, marketingdomain.ChannelWeb}, State: marketingdomain.CampaignDraft,
		Version: 2, CreatedBy: marketingdomain.Actor{Kind: marketingdomain.ActorUser, ID: marketingUserID}, Provenance: marketingdomain.Provenance{Origin: marketingdomain.OriginHuman},
		CreatedAt: service.now.Add(-time.Hour), UpdatedAt: service.now}
}

func (service *marketingQueryTransportService) release() marketingdomain.ReleasePlan {
	return marketingdomain.ReleasePlan{ID: marketingReleaseID, AccountID: marketingAccountID, CampaignID: marketingCampaignID, CampaignVersion: 2,
		Name: "Autumn release", Channels: []marketingdomain.Channel{marketingdomain.ChannelEmail, marketingdomain.ChannelWeb},
		AssetRevisionIDs: []ids.MarketingAssetRevisionID{marketingAssetRevisionID}, State: marketingdomain.ReleaseDraft, Version: 1,
		CreatedBy: marketingdomain.Actor{Kind: marketingdomain.ActorUser, ID: marketingUserID}, Provenance: marketingdomain.Provenance{Origin: marketingdomain.OriginHuman},
		CreatedAt: service.now, UpdatedAt: service.now}
}

func (service *marketingQueryTransportService) GetCampaign(context.Context, access.Actor, ids.AccountID, ids.MarketingCampaignID) (marketingdomain.Campaign, error) {
	return service.campaign(), nil
}

func (service *marketingQueryTransportService) ListCampaigns(_ context.Context, _ access.Actor, _ ids.AccountID, query marketingapp.CampaignListQuery) (marketingapp.CampaignPage, error) {
	service.campaignQuery = query
	value := service.campaign()
	return marketingapp.CampaignPage{Items: []marketingdomain.Campaign{value}, NextCursor: &marketingapp.CampaignCursor{UpdatedAt: value.UpdatedAt, ID: value.ID}}, nil
}

func (service *marketingQueryTransportService) ListAssetRevisions(_ context.Context, _ access.Actor, _ ids.AccountID, query marketingapp.AssetRevisionListQuery) (marketingapp.AssetRevisionPage, error) {
	service.assetQuery = query
	digest := [32]byte{}
	for index := range digest {
		digest[index] = 0x11
	}
	value := marketingdomain.AssetRevision{ID: marketingAssetRevisionID, AccountID: marketingAccountID, CampaignID: marketingCampaignID,
		AssetID: marketingAssetID, Revision: 3, Kind: marketingdomain.AssetImage, Title: "Campaign hero", MediaType: "image/png",
		ContentReference: "objects/marketing/hero.png", ContentSHA256: digest, ContentBytes: 2048, AlternativeText: "Spyglass campaign hero",
		CreatedBy: marketingdomain.Actor{Kind: marketingdomain.ActorUser, ID: marketingUserID}, Provenance: marketingdomain.Provenance{Origin: marketingdomain.OriginHuman}, CreatedAt: service.now}
	return marketingapp.AssetRevisionPage{Items: []marketingdomain.AssetRevision{value}, NextCursor: &marketingapp.AssetRevisionCursor{AssetID: value.AssetID, Revision: value.Revision}}, nil
}

func (service *marketingQueryTransportService) GetRelease(context.Context, access.Actor, ids.AccountID, ids.MarketingReleaseID) (marketingdomain.ReleasePlan, error) {
	return service.release(), nil
}

func (service *marketingQueryTransportService) ListReleases(_ context.Context, _ access.Actor, _ ids.AccountID, query marketingapp.ReleaseListQuery) (marketingapp.ReleasePage, error) {
	service.releaseQuery = query
	value := service.release()
	return marketingapp.ReleasePage{Items: []marketingdomain.ReleasePlan{value}, NextCursor: &marketingapp.ReleaseCursor{CreatedAt: value.CreatedAt, ID: value.ID}}, nil
}

func TestMarketingQueryRoutesBindAccountAndValidateTypedResponses(t *testing.T) {
	now := time.Date(2026, 8, 23, 1, 0, 0, 0, time.UTC)
	service := &marketingQueryTransportService{now: now}
	server, err := New(claimAcceptor{claims: marketingQueryClaims()}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithMarketing(service))
	if err != nil {
		t.Fatal(err)
	}
	contract, err := openapifixture.Load(filepath.Join("..", "..", "..", "api", "spyglass.openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/accounts/" + marketingAccountID + "/marketing"
	tests := []struct {
		name   string
		target string
		check  func(*httptest.ResponseRecorder)
	}{
		{name: "campaign page", target: base + "/campaigns?state=draft&limit=25", check: func(response *httptest.ResponseRecorder) {
			if service.campaignQuery.State != marketingdomain.CampaignDraft || service.campaignQuery.Limit != 25 || !strings.Contains(response.Body.String(), `"next_cursor":"`) {
				t.Fatalf("campaign query=%+v body=%s", service.campaignQuery, response.Body.String())
			}
		}},
		{name: "campaign detail", target: base + "/campaigns/" + marketingCampaignID, check: func(response *httptest.ResponseRecorder) {
			if response.Header().Get("ETag") != `W/"2"` {
				t.Fatalf("campaign ETag=%q", response.Header().Get("ETag"))
			}
		}},
		{name: "asset revisions", target: base + "/campaigns/" + marketingCampaignID + "/asset-revisions?asset_id=" + marketingAssetID + "&limit=10", check: func(response *httptest.ResponseRecorder) {
			if service.assetQuery.CampaignID != marketingCampaignID || service.assetQuery.AssetID != marketingAssetID || service.assetQuery.Limit != 10 ||
				!strings.Contains(response.Body.String(), `"content_sha256":"1111111111111111111111111111111111111111111111111111111111111111"`) {
				t.Fatalf("asset query=%+v body=%s", service.assetQuery, response.Body.String())
			}
		}},
		{name: "release page", target: base + "/campaigns/" + marketingCampaignID + "/releases?limit=5", check: func(response *httptest.ResponseRecorder) {
			if service.releaseQuery.CampaignID != marketingCampaignID || service.releaseQuery.Limit != 5 || !strings.Contains(response.Body.String(), `"next_cursor":"`) {
				t.Fatalf("release query=%+v body=%s", service.releaseQuery, response.Body.String())
			}
		}},
		{name: "release detail", target: base + "/releases/" + marketingReleaseID, check: func(response *httptest.ResponseRecorder) {
			if response.Header().Get("ETag") != `W/"1"` {
				t.Fatalf("release ETag=%q", response.Header().Get("ETag"))
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := marketingQueryRequest(server.Handler(), test.target)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			test.check(response)
			if err := contract.ValidateResponse(http.MethodGet, strings.Split(test.target, "?")[0], response.Code, response.Header(), response.Body.Bytes()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMarketingQueryRoutesRejectCrossAccountAndCursorKindConfusion(t *testing.T) {
	service := &marketingQueryTransportService{now: time.Date(2026, 8, 23, 1, 0, 0, 0, time.UTC)}
	server, _ := New(claimAcceptor{claims: marketingQueryClaims()}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithMarketing(service))
	wrong := marketingQueryRequest(server.Handler(), "/api/v1/accounts/a9000000-0000-4000-8000-000000000009/marketing/campaigns")
	if wrong.Code != http.StatusNotFound {
		t.Fatalf("cross Account=%d body=%s", wrong.Code, wrong.Body.String())
	}
	releaseCursor := encodeMarketingCursor(marketingCursorEnvelope{Version: 1, Kind: "release", Date: service.now, ID: marketingReleaseID})
	confused := marketingQueryRequest(server.Handler(), "/api/v1/accounts/"+marketingAccountID+"/marketing/campaigns?cursor="+releaseCursor)
	if confused.Code != http.StatusBadRequest || !strings.Contains(confused.Body.String(), "invalid_marketing_query") {
		t.Fatalf("cursor confusion=%d body=%s", confused.Code, confused.Body.String())
	}
	unknown := marketingQueryRequest(server.Handler(), "/api/v1/accounts/"+marketingAccountID+"/marketing/campaigns?unknown=value")
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown query=%d body=%s", unknown.Code, unknown.Body.String())
	}
}

func marketingQueryRequest(handler http.Handler, target string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set(RouteContextHeader, "accepted-by-test-boundary")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func marketingQueryClaims() routecontext.Claims {
	return routecontext.Claims{Authority: routecontext.Authority{AccountID: ids.AccountID(marketingAccountID), ActorKind: "user", ActorID: marketingUserID,
		Role: "viewer", CellID: "cell-us-east-01", PlacementGeneration: 1, EntitlementVersion: 3,
		PackageAccess: &routecontext.PackageAccess{Code: "marketing", Version: 1, Mode: "enabled"}}}
}

var _ MarketingQueryService = (*marketingQueryTransportService)(nil)

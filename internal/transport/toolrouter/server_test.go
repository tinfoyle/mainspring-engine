package toolrouter

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountdirectory"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	"github.com/tinfoyle/spyglass-engine/internal/platform/toolcontext"
	"github.com/tinfoyle/spyglass-engine/internal/transport/cellapi"
)

const (
	testAccount    = "10000000-0000-4000-8000-000000000001"
	testInvocation = "20000000-0000-4000-8000-000000000002"
	testPod        = "30000000-0000-4000-8000-000000000003"
	testOperation  = "40000000-0000-4000-8000-000000000004"
	testRequest    = "50000000-0000-4000-8000-000000000005"
)

type acceptingBoundary struct {
	claims  toolcontext.Claims
	binding routecontext.Binding
}

func (a *acceptingBoundary) Accept(_ context.Context, _ string, binding routecontext.Binding) (toolcontext.Claims, error) {
	a.binding = binding
	return a.claims, nil
}

type workloadAuthorizer struct {
	actor       access.Actor
	requirement access.Requirement
}

func (a *workloadAuthorizer) Authorize(_ context.Context, actor access.Actor, accountID ids.AccountID, requirement access.Requirement) (access.AccountContext, error) {
	a.actor = actor
	a.requirement = requirement
	if accountID != testAccount {
		return access.AccountContext{}, &access.DeniedError{Code: access.DenialCorruptContext}
	}
	mode := catalog.ModeReadOnly
	if requirement.Mutation {
		mode = catalog.ModeEnabled
	}
	return access.AccountContext{AccountID: accountID, CellID: "cell-a", PlacementGeneration: 3, EntitlementVersion: 7, PackageAccess: &entitlements.PackageAccess{Code: requirement.Package, Version: 1, Mode: mode}}, nil
}

type directory struct{ origin url.URL }

func (d directory) Resolve(_ context.Context, _ ids.AccountID, _ ids.CellID, _ uint64) (accountdirectory.Route, error) {
	return accountdirectory.Route{CellID: "cell-a", PlacementGeneration: 3, Origin: d.origin}, nil
}

type routeSigner struct {
	authority routecontext.Authority
	binding   routecontext.Binding
}

func (s *routeSigner) Issue(_ string, authority routecontext.Authority, binding routecontext.Binding) (string, error) {
	s.authority = authority
	s.binding = binding
	return "cell-route-token", nil
}

func TestFinanceDraftDispatchReauthorizesMutationAndPreservesAgentProvenance(t *testing.T) {
	boundary := &acceptingBoundary{claims: toolcontext.Claims{Authority: toolcontext.Authority{
		RequestID: "60000000-0000-4000-8000-000000000006", AccountID: testAccount, InvocationID: testInvocation,
		PodUID: testPod, OperationID: testOperation, Capability: FinanceEntryDraftCapability,
	}}}
	authorizer := &workloadAuthorizer{}
	signer := &routeSigner{}
	cellOrigin, _ := url.Parse("http://cell.internal")
	runID := "70000000-0000-4000-8000-000000000007"
	ledgerID := "80000000-0000-4000-8000-000000000008"
	input := `{"ledger_id":"` + ledgerID + `","run_id":"` + runID + `","entry_date":"2026-08-22T00:00:00Z","description":"Accrual draft","reference":"AGENT-1","currency":"USD","lines":[{"account_id":"90000000-0000-4000-8000-000000000009","memo":"Accrual","debit_minor":100,"credit_minor":0},{"account_id":"a0000000-0000-4000-8000-00000000000a","memo":"Accrual","debit_minor":0,"credit_minor":100}],"evidence":[]}`
	transport := roundTrip(func(request *http.Request) (*http.Response, error) {
		wantPath := "/internal/v1/accounts/" + testAccount + "/finance/ledgers/" + ledgerID + "/entries:draft"
		if request.Method != http.MethodPost || request.URL.Path != wantPath || request.Header.Get("Idempotency-Key") != testOperation || request.Header.Get("Authorization") != "" {
			t.Fatalf("unexpected cell request: %s %s headers=%v", request.Method, request.URL, request.Header)
		}
		raw, _ := io.ReadAll(request.Body)
		if strings.Contains(string(raw), `"ledger_id"`) || !strings.Contains(string(raw), `"run_id":"`+runID+`"`) {
			t.Fatalf("unexpected routed body: %s", raw)
		}
		return &http.Response{StatusCode: http.StatusCreated, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"` + testOperation + `","state":"draft"}`))}, nil
	})
	server, err := New(boundary, authorizer, directory{origin: *cellOrigin}, signer, fixedIDs{}, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{Transport: transport, AllowHTTPCells: true})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/tools:invoke", strings.NewReader(input))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(ContextHeader, "tool-proof")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"state":"draft"`) {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
	if authorizer.requirement.Package != catalog.PackageFinance || !authorizer.requirement.Mutation || signer.authority.ActorID != "runner-invocation:"+testInvocation || signer.binding.Method != http.MethodPost {
		t.Fatalf("requirement=%+v authority=%+v binding=%+v", authorizer.requirement, signer.authority, signer.binding)
	}
}

type fixedIDs struct{}

func (fixedIDs) New() string { return testRequest }

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestWorkSummaryDispatchReauthorizesAndRoutesAsWorkload(t *testing.T) {
	boundary := &acceptingBoundary{claims: toolcontext.Claims{Authority: toolcontext.Authority{
		RequestID: "60000000-0000-4000-8000-000000000006", AccountID: testAccount, InvocationID: testInvocation,
		PodUID: testPod, OperationID: testOperation, Capability: WorkSummaryCapability,
	}}}
	authorizer := &workloadAuthorizer{}
	signer := &routeSigner{}
	cellOrigin, _ := url.Parse("http://cell.internal")
	transport := roundTrip(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Path != "/api/v1/accounts/"+testAccount+"/work-items/summary" || request.Header.Get(cellapi.RouteContextHeader) != "cell-route-token" {
			t.Fatalf("unexpected cell request: %s %s headers=%v", request.Method, request.URL, request.Header)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"active":4,"in_progress":2,"waiting":1,"urgent":1,"done":8}`))}, nil
	})
	server, err := New(boundary, authorizer, directory{origin: *cellOrigin}, signer, fixedIDs{}, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{Transport: transport, AllowHTTPCells: true})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/tools:invoke", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(ContextHeader, "tool-proof")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"active":4`) {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
	if boundary.binding.Method != http.MethodPost || boundary.binding.Target != "/internal/v1/tools:invoke" {
		t.Fatalf("dispatch was not request-bound: %#v", boundary.binding)
	}
	if authorizer.actor.WorkloadID != "runner-invocation:"+testInvocation || authorizer.actor.UserID != "" {
		t.Fatalf("unexpected actor: %#v", authorizer.actor)
	}
	if signer.authority.ActorKind != "workload" || signer.authority.Role != "" || signer.authority.OperationID != testOperation || signer.authority.PackageAccess == nil || signer.authority.PackageAccess.Mode != "read_only" {
		t.Fatalf("unexpected cell authority: %#v", signer.authority)
	}
}

func TestToolDispatchRejectsInputAndUnavailableCapabilityBeforeCell(t *testing.T) {
	for _, test := range []struct {
		name, body, capability string
		status                 int
	}{
		{"input", `{"scope":"all"}`, WorkSummaryCapability, http.StatusBadRequest},
		{"capability", `{}`, "finance.balance.read", http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			boundary := &acceptingBoundary{claims: toolcontext.Claims{Authority: toolcontext.Authority{AccountID: testAccount, InvocationID: testInvocation, OperationID: testOperation, Capability: test.capability}}}
			origin, _ := url.Parse("http://cell.internal")
			server, _ := New(boundary, &workloadAuthorizer{}, directory{origin: *origin}, &routeSigner{}, fixedIDs{}, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{Transport: roundTrip(func(*http.Request) (*http.Response, error) { t.Fatal("cell called"); return nil, nil }), AllowHTTPCells: true})
			request := httptest.NewRequest(http.MethodPost, "/internal/v1/tools:invoke", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(ContextHeader, "proof")
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestFinanceReadDispatchUsesBoundedAccountRoutes(t *testing.T) {
	ledgers, ok := dispatchCapability(FinanceLedgersReadCapability, testAccount, []byte(`{}`))
	if !ok || ledgers.method != http.MethodGet || ledgers.target != "/api/v1/accounts/"+testAccount+"/finance/ledgers?limit=100" || ledgers.requirement.Package != catalog.PackageFinance || ledgers.requirement.Mutation {
		t.Fatalf("ledger dispatch=%+v ok=%v", ledgers, ok)
	}
	ledgerID := "80000000-0000-4000-8000-000000000008"
	accounts, ok := dispatchCapability(FinanceAccountsReadCapability, testAccount, []byte(`{"ledger_id":"`+ledgerID+`"}`))
	if !ok || accounts.target != "/api/v1/accounts/"+testAccount+"/finance/ledgers/"+ledgerID+"/accounts?limit=100" || accounts.requirement.Package != catalog.PackageFinance {
		t.Fatalf("account dispatch=%+v ok=%v", accounts, ok)
	}
	if _, ok := dispatchCapability(FinanceAccountsReadCapability, testAccount, []byte(`{"ledger_id":"invalid"}`)); ok {
		t.Fatal("invalid Finance Ledger was routed")
	}
}

func TestMarketingDispatchUsesBoundedReadsAndDraftOnlyMutationRoutes(t *testing.T) {
	campaignID := "81000000-0000-4000-8000-000000000008"
	assetID := "82000000-0000-4000-8000-000000000008"
	revisionID := "83000000-0000-4000-8000-000000000008"
	runID := "84000000-0000-4000-8000-000000000008"
	campaigns, ok := dispatchCapability(MarketingCampaignsReadCapability, testAccount, []byte(`{"state":"draft"}`))
	if !ok || campaigns.target != "/api/v1/accounts/"+testAccount+"/marketing/campaigns?limit=100&state=draft" || campaigns.requirement.Package != catalog.PackageMarketing || campaigns.requirement.Mutation {
		t.Fatalf("campaign dispatch=%+v ok=%v", campaigns, ok)
	}
	assets, ok := dispatchCapability(MarketingAssetsReadCapability, testAccount, []byte(`{"campaign_id":"`+campaignID+`","asset_id":"`+assetID+`"}`))
	if !ok || assets.target != "/api/v1/accounts/"+testAccount+"/marketing/campaigns/"+campaignID+"/asset-revisions?asset_id="+assetID+"&limit=100" || assets.requirement.Package != catalog.PackageMarketing {
		t.Fatalf("asset dispatch=%+v ok=%v", assets, ok)
	}
	releases, ok := dispatchCapability(MarketingReleasesReadCapability, testAccount, []byte(`{"campaign_id":"`+campaignID+`"}`))
	if !ok || releases.target != "/api/v1/accounts/"+testAccount+"/marketing/campaigns/"+campaignID+"/releases?limit=100" || releases.requirement.Package != catalog.PackageMarketing {
		t.Fatalf("release dispatch=%+v ok=%v", releases, ok)
	}
	campaign, ok := dispatchCapability(MarketingCampaignDraftCapability, testAccount, []byte(`{"run_id":"`+runID+`","name":"Launch","objective":"Announce","audience":"Customers","channels":["email"]}`))
	if !ok || campaign.target != "/internal/v1/accounts/"+testAccount+"/marketing/campaigns:draft" || !campaign.requirement.Mutation || strings.Contains(string(campaign.body), `"campaign_id"`) {
		t.Fatalf("campaign draft=%+v body=%s ok=%v", campaign, campaign.body, ok)
	}
	asset, ok := dispatchCapability(MarketingAssetDraftCapability, testAccount, []byte(`{"run_id":"`+runID+`","campaign_id":"`+campaignID+`","asset_id":"`+assetID+`","title":"Launch copy","content":"A precise launch message."}`))
	if !ok || asset.target != "/internal/v1/accounts/"+testAccount+"/marketing/campaigns/"+campaignID+"/asset-revisions:draft" || !asset.requirement.Mutation || strings.Contains(string(asset.body), `"campaign_id"`) {
		t.Fatalf("asset draft=%+v body=%s ok=%v", asset, asset.body, ok)
	}
	if strings.Contains(string(asset.body), "content_reference") || strings.Contains(string(asset.body), "content_sha256") || strings.Contains(string(asset.body), "media_type") {
		t.Fatalf("asset draft exposes internal object metadata: %s", asset.body)
	}
	if _, ok := dispatchCapability(MarketingAssetDraftCapability, testAccount, []byte(`{"run_id":"`+runID+`","campaign_id":"`+campaignID+`","asset_id":"`+assetID+`","title":"Legacy","content":"copy","content_reference":"objects/copy"}`)); ok {
		t.Fatal("legacy caller-supplied object reference was routed")
	}
	release, ok := dispatchCapability(MarketingReleaseDraftCapability, testAccount, []byte(`{"run_id":"`+runID+`","campaign_id":"`+campaignID+`","campaign_version":2,"name":"Release","channels":["email"],"asset_revision_ids":["`+revisionID+`"]}`))
	if !ok || release.target != "/internal/v1/accounts/"+testAccount+"/marketing/campaigns/"+campaignID+"/releases:draft" || !release.requirement.Mutation || strings.Contains(string(release.body), `"campaign_id"`) {
		t.Fatalf("release draft=%+v body=%s ok=%v", release, release.body, ok)
	}
	if _, ok := dispatchCapability(MarketingCampaignsReadCapability, testAccount, []byte(`{"state":"invented"}`)); ok {
		t.Fatal("invalid Marketing state was routed")
	}
}

func TestWebResearchDispatchSeparatesReadOnlySearchFromCrossPackageCapture(t *testing.T) {
	connectionID := "85000000-0000-4000-8000-000000000008"
	search, ok := dispatchCapability(WebResearchSearchCapability, testAccount,
		[]byte(`{"connection_id":"`+connectionID+`","query":"current rule","limit":3}`))
	if !ok || search.method != http.MethodPost || search.target != "/api/v1/accounts/"+testAccount+"/integrations/web-research/search" ||
		search.requirement.Package != catalog.PackageIntegrations || search.requirement.Mutation || search.additionalRequirement != nil ||
		!strings.Contains(string(search.body), `"query":"current rule"`) {
		t.Fatalf("search=%+v body=%s ok=%v", search, search.body, ok)
	}
	read, ok := dispatchCapability(WebResearchReadCapability, testAccount,
		[]byte(`{"connection_id":"`+connectionID+`","url":"https://research.example/rule"}`))
	if !ok || read.method != http.MethodPost || read.target != "/api/v1/accounts/"+testAccount+"/integrations/web-research/read" ||
		read.requirement.Package != catalog.PackageIntegrations || !read.requirement.Mutation || !read.createdOK ||
		read.additionalRequirement == nil || read.additionalRequirement.Package != catalog.PackageKnowledge || !read.additionalRequirement.Mutation {
		t.Fatalf("read=%+v body=%s ok=%v", read, read.body, ok)
	}
	for _, invalid := range []string{
		`{"connection_id":"invalid","query":"rule"}`,
		`{"connection_id":"` + connectionID + `","query":"rule","unexpected":true}`,
		`{"connection_id":"` + connectionID + `","url":"http://research.example/rule","unexpected":true}`,
	} {
		if _, ok := dispatchCapability(WebResearchSearchCapability, testAccount, []byte(invalid)); ok {
			t.Fatalf("invalid web research input was routed: %s", invalid)
		}
	}
}

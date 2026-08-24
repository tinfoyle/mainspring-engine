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

	webresearchapp "github.com/tinfoyle/spyglass-engine/internal/application/webresearch"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	"github.com/tinfoyle/spyglass-engine/internal/testsupport/openapifixture"
)

const (
	webResearchAccount    = "d2000000-0000-4000-8000-000000000002"
	webResearchUser       = "d3000000-0000-4000-8000-000000000003"
	webResearchConnection = "d4000000-0000-4000-8000-000000000004"
	webResearchOperation  = "d5000000-0000-4000-8000-000000000005"
	webResearchDocument   = "d6000000-0000-4000-8000-000000000006"
	webResearchRevision   = "d7000000-0000-4000-8000-000000000007"
)

type webResearchTransportStub struct {
	search webresearchapp.SearchCommand
	read   webresearchapp.ReadCommand
	now    time.Time
}

func (stub *webResearchTransportStub) Search(_ context.Context, command webresearchapp.SearchCommand) (webresearchapp.SearchResult, error) {
	stub.search = command
	return webresearchapp.SearchResult{Query: command.Query, Items: []webresearchapp.SearchItem{{
		CitationID: "web:" + strings.Repeat("a", 64), Title: "Authoritative rule", URL: "https://research.example/rule",
		Description: "Current guidance", RetrievedAt: stub.now,
	}}}, nil
}

func (stub *webResearchTransportStub) Read(_ context.Context, command webresearchapp.ReadCommand) (webresearchapp.ReadResult, error) {
	stub.read = command
	return webresearchapp.ReadResult{CaptureID: webResearchOperation, CitationID: "web:" + strings.Repeat("b", 64),
		URL: "https://research.example/rule", Title: "Authoritative rule", MediaType: "text/html",
		ContentSHA256: strings.Repeat("c", 64), DocumentID: webResearchDocument, DocumentRevisionID: webResearchRevision,
		Excerpt: "Current guidance", RetrievedAt: stub.now}, nil
}

func TestWebResearchRoutesBindTypedAuthorityAndOpenAPI(t *testing.T) {
	now := time.Date(2026, 8, 24, 20, 0, 0, 0, time.UTC)
	stub := &webResearchTransportStub{now: now}
	claims := routecontext.Claims{Authority: routecontext.Authority{AccountID: webResearchAccount, ActorKind: "user", ActorID: webResearchUser,
		Role: "owner", RequestID: webResearchOperation, OperationID: webResearchOperation, CellID: "cell-us-east-01",
		PlacementGeneration: 1, EntitlementVersion: 1, PackageAccess: &routecontext.PackageAccess{Code: "integrations", Version: 1, Mode: "enabled"}}}
	server, err := New(claimAcceptor{claims: claims}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithWebResearch(stub))
	if err != nil {
		t.Fatal(err)
	}
	contract, err := openapifixture.Load(filepath.Join("..", "..", "..", "api", "spyglass.openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/accounts/" + webResearchAccount + "/integrations/web-research"

	search := httptest.NewRequest(http.MethodPost, base+"/search", strings.NewReader(`{"connection_id":"`+webResearchConnection+`","query":"current rule","limit":3}`))
	search.Header.Set(RouteContextHeader, "accepted-by-test-boundary")
	search.Header.Set("Content-Type", "application/json")
	searchResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(searchResponse, search)
	if searchResponse.Code != http.StatusOK || stub.search.OperationID != webResearchOperation || stub.search.ConnectionID != webResearchConnection || stub.search.Limit != 3 {
		t.Fatalf("status=%d command=%+v body=%s", searchResponse.Code, stub.search, searchResponse.Body.String())
	}
	if err := contract.ValidateResponse(http.MethodPost, base+"/search", searchResponse.Code, searchResponse.Header(), searchResponse.Body.Bytes()); err != nil {
		t.Fatal(err)
	}

	read := httptest.NewRequest(http.MethodPost, base+"/read", strings.NewReader(`{"connection_id":"`+webResearchConnection+`","url":"https://research.example/rule"}`))
	read.Header.Set(RouteContextHeader, "accepted-by-test-boundary")
	read.Header.Set("Content-Type", "application/json")
	read.Header.Set("Idempotency-Key", webResearchOperation)
	readResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(readResponse, read)
	if readResponse.Code != http.StatusCreated || stub.read.OperationID != webResearchOperation || stub.read.ConnectionID != webResearchConnection {
		t.Fatalf("status=%d command=%+v body=%s", readResponse.Code, stub.read, readResponse.Body.String())
	}
	if err := contract.ValidateResponse(http.MethodPost, base+"/read", readResponse.Code, readResponse.Header(), readResponse.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
}

func TestWebResearchReadRejectsUnboundIdempotencyKey(t *testing.T) {
	stub := &webResearchTransportStub{}
	claims := routecontext.Claims{Authority: routecontext.Authority{AccountID: webResearchAccount, ActorKind: "user", ActorID: webResearchUser,
		Role: "owner", RequestID: webResearchOperation, OperationID: webResearchOperation, CellID: "cell-us-east-01",
		PlacementGeneration: 1, EntitlementVersion: 1, PackageAccess: &routecontext.PackageAccess{Code: "integrations", Version: 1, Mode: "enabled"}}}
	server, _ := New(claimAcceptor{claims: claims}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithWebResearch(stub))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+webResearchAccount+"/integrations/web-research/read",
		strings.NewReader(`{"connection_id":"`+webResearchConnection+`","url":"https://research.example/rule"}`))
	request.Header.Set(RouteContextHeader, "accepted-by-test-boundary")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "d8000000-0000-4000-8000-000000000008")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || stub.read.OperationID != "" || !strings.Contains(response.Body.String(), "invalid_idempotency_key") {
		t.Fatalf("status=%d command=%+v body=%s", response.Code, stub.read, response.Body.String())
	}
}

var _ WebResearchService = (*webResearchTransportStub)(nil)

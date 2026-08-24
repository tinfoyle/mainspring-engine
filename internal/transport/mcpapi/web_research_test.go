package mcpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	webresearchapp "github.com/tinfoyle/spyglass-engine/internal/application/webresearch"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
)

type webResearchMCPStub struct {
	search webresearchapp.SearchCommand
	read   webresearchapp.ReadCommand
	now    time.Time
}

func (stub *webResearchMCPStub) Search(_ context.Context, command webresearchapp.SearchCommand) (webresearchapp.SearchResult, error) {
	stub.search = command
	return webresearchapp.SearchResult{Query: command.Query, Items: []webresearchapp.SearchItem{{CitationID: "web:" + strings.Repeat("a", 64),
		Title: "Current rule", URL: "https://research.example/rule", RetrievedAt: stub.now}}}, nil
}

func (stub *webResearchMCPStub) Read(_ context.Context, command webresearchapp.ReadCommand) (webresearchapp.ReadResult, error) {
	stub.read = command
	return webresearchapp.ReadResult{CaptureID: mcpOperation, CitationID: "web:" + strings.Repeat("b", 64),
		URL: "https://research.example/rule", Title: "Current rule", MediaType: "text/html", ContentSHA256: strings.Repeat("c", 64),
		DocumentID: mcpWork, DocumentRevisionID: "31000000-0000-4000-8000-000000000003", RetrievedAt: stub.now}, nil
}

func TestWebResearchMCPToolsAreTypedAndCrossPackageClassified(t *testing.T) {
	primary, ok := ToolRequirement("spyglass_integrations_web_read")
	secondary, secondaryOK := AdditionalToolRequirement("spyglass_integrations_web_read")
	if !ok || !secondaryOK || primary.Package != catalog.PackageIntegrations || !primary.Mutation ||
		secondary.Package != catalog.PackageKnowledge || !secondary.Mutation {
		t.Fatalf("primary=%+v ok=%t secondary=%+v ok=%t", primary, ok, secondary, secondaryOK)
	}
	if _, unexpected := AdditionalToolRequirement("spyglass_integrations_web_search"); unexpected {
		t.Fatal("read-only web search unexpectedly has a secondary package requirement")
	}

	stub := &webResearchMCPStub{now: time.Date(2026, 8, 24, 20, 0, 0, 0, time.UTC)}
	authority := &testAuthority{}
	server, err := New(authority, &attentionStub{}, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{Version: "0.3.0-test",
		MaxBody: DefaultMaxBody, TrustedOrigins: []string{"https://trusted.example"},
		ResourceMetadataURL: "https://auth.infiniteocean.net/.well-known/oauth-protected-resource"}, WithWebResearch(stub))
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	transport := &mcp.StreamableClientTransport{Endpoint: httpServer.URL, MaxRetries: -1,
		HTTPClient: &http.Client{Transport: bearerRoundTripper{base: http.DefaultTransport}}}
	client := mcp.NewClient(&mcp.Implementation{Name: "web-research-test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	searched, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_integrations_web_search", Arguments: map[string]any{
		"account_id": mcpAccount, "operation_id": mcpOperation, "connection_id": mcpIntegrationConnection, "query": "current rule", "limit": 3,
	}})
	if err != nil || searched.IsError || stub.search.OperationID != mcpOperation || stub.search.Limit != 3 {
		t.Fatalf("searched=%+v err=%v command=%+v", searched, err, stub.search)
	}
	read, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "spyglass_integrations_web_read", Arguments: map[string]any{
		"account_id": mcpAccount, "operation_id": mcpOperation, "connection_id": mcpIntegrationConnection, "url": "https://research.example/rule",
	}})
	if err != nil || read.IsError || stub.read.OperationID != mcpOperation || stub.read.URL != "https://research.example/rule" {
		t.Fatalf("read=%+v err=%v command=%+v", read, err, stub.read)
	}
	if len(authority.requirements) != 2 || authority.requirements[0].Package != catalog.PackageIntegrations || authority.requirements[0].Mutation ||
		authority.requirements[1].Package != catalog.PackageIntegrations || !authority.requirements[1].Mutation {
		t.Fatalf("requirements=%+v", authority.requirements)
	}
}

var _ WebResearchService = (*webResearchMCPStub)(nil)

package tenant

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
	toolbroker "github.com/tinfoyle/mainspring-engine/internal/tools"
	"github.com/tinfoyle/mainspring-engine/internal/webresearch"
)

const testMCPToken = "test-mcp-token-that-is-longer-than-thirty-two-bytes"

type mcpBearerTransport struct {
	token string
}

type mcpWebProvider struct {
	search webresearch.SearchRequest
}

func (provider *mcpWebProvider) Search(_ context.Context, request webresearch.SearchRequest) ([]webresearch.SearchResult, error) {
	provider.search = request
	return []webresearch.SearchResult{{CitationID: "web:test", Title: "Official source", URL: "https://agency.example/rule"}}, nil
}

func (provider *mcpWebProvider) Read(_ context.Context, request webresearch.ReadRequest) (webresearch.ReadResult, error) {
	return webresearch.ReadResult{CitationID: "web:test", URL: request.URL, Content: "evidence"}, nil
}

func (transport mcpBearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	copy.Header.Set("Authorization", "Bearer "+transport.token)
	return http.DefaultTransport.RoundTrip(copy)
}

func TestMCPStreamableHTTPRequiresBearerAndListsSurface(t *testing.T) {
	server := &Server{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		config: ServerConfig{MCPToken: testMCPToken, MCPUserEmail: "owner@example.test"},
	}
	httpServer := httptest.NewServer(server.mcpHTTPHandler())
	defer httpServer.Close()

	request, err := http.NewRequest(http.MethodPost, httpServer.URL, bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated MCP status = %d; want %d", response.StatusCode, http.StatusUnauthorized)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "mainspring-test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             httpServer.URL,
		HTTPClient:           &http.Client{Transport: mcpBearerTransport{token: testMCPToken}},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	defer session.Close()

	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list MCP tools: %v", err)
	}
	wanted := map[string]bool{
		"mainspring_list_agents":             false,
		"mainspring_list_documents":          false,
		"mainspring_search_documents":        false,
		"mainspring_upload_document":         false,
		"mainspring_list_work_items":         false,
		"mainspring_get_work_item":           false,
		"mainspring_create_work_item":        false,
		"mainspring_update_work_item_status": false,
		"mainspring_message_ticket":          false,
		"mainspring_get_run":                 false,
		"mainspring_list_approvals":          false,
		"mainspring_decide_approval":         false,
		"mainspring_query_finance":           false,
		"mainspring_manage_finance":          false,
	}
	for _, tool := range listed.Tools {
		if _, ok := wanted[tool.Name]; !ok {
			t.Fatalf("unexpected MCP tool %q", tool.Name)
		}
		wanted[tool.Name] = true
		if tool.InputSchema == nil || tool.Annotations == nil {
			t.Fatalf("tool %q is missing its schema or safety annotations", tool.Name)
		}
	}
	for name, found := range wanted {
		if !found {
			t.Errorf("MCP tool %q was not advertised", name)
		}
	}
}

func TestMCPDueAt(t *testing.T) {
	date, err := parseMCPDueAt("2026-08-12")
	if err != nil {
		t.Fatal(err)
	}
	if date.Format("2006-01-02 15:04:05.999999999") != "2026-08-12 23:59:59.999999999" {
		t.Fatalf("date-only due time = %s", date)
	}
	if _, err := parseMCPDueAt("tomorrow"); err == nil {
		t.Fatal("invalid due_at should fail")
	}
}

func TestMCPWebResearchUsesTheGovernedBrokerSurface(t *testing.T) {
	issuer, err := toolbroker.NewTokenIssuer([]byte("test-secret-that-is-at-least-thirty-two-bytes"), 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	provider := &mcpWebProvider{}
	broker := toolbroker.NewBroker(issuer, nil)
	if err := toolbroker.RegisterWebResearch(broker, provider); err != nil {
		t.Fatal(err)
	}
	server := &Server{config: ServerConfig{TenantID: domain.NewTenantID()}}
	server.SetToolBroker(issuer, broker)
	if !server.mcpHasTool(toolbroker.WebSearchTool, domain.CapabilityWebSearch) || !server.mcpHasTool(toolbroker.WebReadTool, domain.CapabilityWebRead) {
		t.Fatal("configured web tools were not exposed to MCP")
	}
	_, output, err := server.mcpInvokeBroker(context.Background(), User{ID: domain.NewPersonaID().String()}, domain.CapabilityWebSearch, toolbroker.WebSearchTool, mcpSearchWebInput{
		Query: "licensing rule", Limit: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(output)
	if provider.search.Query != "licensing rule" || provider.search.Limit != 3 || !bytes.Contains(encoded, []byte("web:test")) {
		t.Fatalf("unexpected broker result %s and request %#v", encoded, provider.search)
	}
}

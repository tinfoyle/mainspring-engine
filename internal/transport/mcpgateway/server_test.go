package mcpgateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountdirectory"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/entitlements"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

const (
	gatewayAccount = "10000000-0000-4000-8000-000000000001"
	gatewayUser    = "20000000-0000-4000-8000-000000000002"
	gatewayRequest = "30000000-0000-4000-8000-000000000003"
	gatewayCell    = "cell-us-east-01"
)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type tokenAuthority struct {
	token            string
	tokenRequirement TokenRequirement
	requirement      access.Requirement
}

func (a *tokenAuthority) Authenticate(_ context.Context, token string, requirement TokenRequirement) (access.Actor, error) {
	a.token = token
	a.tokenRequirement = requirement
	if token != "audience-bound-token" {
		return access.Actor{}, errors.New("invalid token")
	}
	return access.Actor{UserID: gatewayUser}, nil
}

func (a *tokenAuthority) Authorize(_ context.Context, actor access.Actor, accountID ids.AccountID, requirement access.Requirement) (access.AccountContext, error) {
	a.requirement = requirement
	if actor.UserID != gatewayUser || accountID != gatewayAccount {
		return access.AccountContext{}, &access.DeniedError{Code: access.DenialMembership}
	}
	packageAccess := &entitlements.PackageAccess{Code: requirement.Package, Version: 3, Mode: catalog.ModeEnabled}
	if requirement.Package == "" {
		packageAccess = nil
	}
	return access.AccountContext{AccountID: accountID, CellID: gatewayCell, PlacementGeneration: 7, EntitlementVersion: 9, Role: accounts.RoleOwner, PackageAccess: packageAccess}, nil
}

type directory struct{ origin url.URL }

func (d directory) Resolve(_ context.Context, accountID ids.AccountID, cellID ids.CellID, generation uint64) (accountdirectory.Route, error) {
	if accountID != gatewayAccount || cellID != gatewayCell || generation != 7 {
		return accountdirectory.Route{}, errors.New("unexpected placement")
	}
	return accountdirectory.Route{CellID: cellID, PlacementGeneration: generation, Origin: d.origin}, nil
}

type generator struct {
	mu   sync.Mutex
	next int
}

func (g *generator) New() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.next++
	if g.next == 1 {
		return gatewayRequest
	}
	return "40000000-0000-4000-8000-000000000004"
}

func TestGatewayRoutesExactToolAuthorityWithoutForwardingBearer(t *testing.T) {
	now := time.Date(2026, 8, 22, 20, 0, 0, 0, time.UTC)
	key := []byte("0123456789abcdef0123456789abcdef")
	verifier, err := routecontext.NewVerifier("spyglass-router", routecontext.Audience(gatewayCell), map[string][]byte{"active": key}, routecontext.MaximumLifetime, routecontext.DefaultClockSkew, fixedClock{now})
	if err != nil {
		t.Fatal(err)
	}
	requestBody := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"spyglass_finance_entry_post","arguments":{"account_id":"` + gatewayAccount + `","operation_id":"50000000-0000-4000-8000-000000000005"},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`
	cell := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != cellMCPPath || r.Header.Get("Authorization") != "" || r.Header.Get(routecontext.HeaderName) == "" || r.Header.Get("Mcp-Method") != "tools/call" || r.Header.Get("Mcp-Name") != "spyglass_finance_entry_post" {
			t.Fatalf("unsafe outbound request path=%q authorization=%q route=%t", r.URL.Path, r.Header.Get("Authorization"), r.Header.Get(routecontext.HeaderName) != "")
		}
		raw, _ := io.ReadAll(r.Body)
		if string(raw) != requestBody {
			t.Fatalf("body changed: %s", raw)
		}
		binding, bindErr := routecontext.BindRequest(r, raw)
		if bindErr != nil {
			t.Fatal(bindErr)
		}
		claims, verifyErr := verifier.Verify(r.Header.Get(routecontext.HeaderName), binding)
		if verifyErr != nil {
			t.Fatal(verifyErr)
		}
		if claims.Authority.AccountID != gatewayAccount || claims.Authority.ActorID != gatewayUser || claims.Authority.PackageAccess == nil || claims.Authority.PackageAccess.Code != "finance" {
			t.Fatalf("claims=%+v", claims)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[]}}`))
	}))
	defer cell.Close()
	origin, _ := url.Parse(cell.URL)
	authority := &tokenAuthority{}
	signer, _ := routecontext.NewSigner("spyglass-router", "active", key, 20*time.Second, fixedClock{now})
	server, err := New(authority, authority, directory{origin: *origin}, signer, &generator{}, slog.New(slog.NewTextHandler(io.Discard, nil)), gatewayConfig())
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://mcp.infiniteocean.net/mcp/v1/accounts/"+gatewayAccount, strings.NewReader(requestBody))
	request.Header.Set("Authorization", "Bearer audience-bound-token")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", "2026-07-28")
	request.Header.Set("Mcp-Method", "tools/call")
	request.Header.Set("Mcp-Name", "spyglass_finance_entry_post")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || authority.token != "audience-bound-token" || authority.tokenRequirement.Audience != "https://mcp.infiniteocean.net" || authority.tokenRequirement.Scope != RequiredScope || authority.requirement.Package != catalog.PackageFinance || !authority.requirement.Mutation {
		t.Fatalf("status=%d body=%s token=%q requirement=%+v", response.Code, response.Body.String(), authority.token, authority.requirement)
	}
}

func TestGatewayRejectsUnknownOrCrossAccountToolBeforeRouting(t *testing.T) {
	cellCalls := 0
	cell := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { cellCalls++ }))
	defer cell.Close()
	origin, _ := url.Parse(cell.URL)
	authority := &tokenAuthority{}
	key := []byte("0123456789abcdef0123456789abcdef")
	signer, _ := routecontext.NewSigner("spyglass-router", "active", key, 20*time.Second, fixedClock{time.Now()})
	server, err := New(authority, authority, directory{origin: *origin}, signer, &generator{}, slog.New(slog.NewTextHandler(io.Discard, nil)), gatewayConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"spyglass_unknown","arguments":{"account_id":"` + gatewayAccount + `"}}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"spyglass_finance_ledger_list","arguments":{"account_id":"90000000-0000-4000-8000-000000000009"}}}`,
		`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`,
	} {
		request := httptest.NewRequest(http.MethodPost, "https://mcp.infiniteocean.net/mcp/v1/accounts/"+gatewayAccount, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer audience-bound-token")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	}
	mismatchBody := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"spyglass_finance_ledger_list","arguments":{"account_id":"` + gatewayAccount + `"},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`
	mismatch := httptest.NewRequest(http.MethodPost, "https://mcp.infiniteocean.net/mcp/v1/accounts/"+gatewayAccount, strings.NewReader(mismatchBody))
	mismatch.Header.Set("Authorization", "Bearer audience-bound-token")
	mismatch.Header.Set("Content-Type", "application/json")
	mismatch.Header.Set("Accept", "application/json, text/event-stream")
	mismatch.Header.Set("MCP-Protocol-Version", "2026-07-28")
	mismatch.Header.Set("Mcp-Method", "tools/call")
	mismatch.Header.Set("Mcp-Name", "spyglass_finance_entry_get")
	mismatchResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(mismatchResponse, mismatch)
	if mismatchResponse.Code != http.StatusBadRequest {
		t.Fatalf("mismatch status=%d body=%s", mismatchResponse.Code, mismatchResponse.Body.String())
	}
	if cellCalls != 0 {
		t.Fatalf("cell calls=%d", cellCalls)
	}
}

func TestGatewayChallengesMissingBearerWithProtectedResourceMetadata(t *testing.T) {
	authority := &tokenAuthority{}
	server, err := New(authority, authority, directory{}, failingSigner{}, &generator{}, slog.New(slog.NewTextHandler(io.Discard, nil)), gatewayConfig())
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://mcp.infiniteocean.net/mcp/v1/accounts/"+gatewayAccount, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Header().Get("WWW-Authenticate"), "oauth-protected-resource") {
		t.Fatalf("status=%d challenge=%q", response.Code, response.Header().Get("WWW-Authenticate"))
	}
	if !strings.Contains(response.Header().Get("WWW-Authenticate"), `scope="spyglass:mcp"`) {
		t.Fatalf("scope challenge=%q", response.Header().Get("WWW-Authenticate"))
	}

	metadataRequest := httptest.NewRequest(http.MethodGet, "https://mcp.infiniteocean.net/.well-known/oauth-protected-resource", nil)
	metadataResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(metadataResponse, metadataRequest)
	metadataBody := metadataResponse.Body.String()
	if metadataResponse.Code != http.StatusOK || !strings.Contains(metadataBody, `"resource":"https://mcp.infiniteocean.net"`) || !strings.Contains(metadataBody, `"authorization_servers":["https://auth.infiniteocean.net"]`) || !strings.Contains(metadataBody, `"scopes_supported":["spyglass:mcp"]`) {
		t.Fatalf("status=%d metadata=%s", metadataResponse.Code, metadataBody)
	}
}

type failingSigner struct{}

func (failingSigner) Issue(string, routecontext.Authority, routecontext.Binding) (string, error) {
	return "", errors.New("not used")
}

func gatewayConfig() Config {
	return Config{
		ResourceURL:          "https://mcp.infiniteocean.net",
		ResourceMetadataURL:  "https://mcp.infiniteocean.net/.well-known/oauth-protected-resource",
		AuthorizationServers: []string{"https://auth.infiniteocean.net"},
	}
}

package webresearch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationhealth"
	webresearchapp "github.com/tinfoyle/spyglass-engine/internal/application/webresearch"
	"github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type retrieverStub struct {
	valid    map[string]string
	result   Result
	fetchURL string
	policy   Policy
}

func (retriever *retrieverStub) Validate(_ context.Context, raw string, policy Policy) (string, error) {
	retriever.policy = policy
	value, ok := retriever.valid[raw]
	if !ok {
		return "", ErrDenied
	}
	return value, nil
}

func (retriever *retrieverStub) Fetch(_ context.Context, raw string, policy Policy) (Result, error) {
	retriever.fetchURL, retriever.policy = raw, policy
	return retriever.result, nil
}

func TestFirecrawlSearchUsesFixedProviderAndReturnsOnlyValidatedScopedURLs(t *testing.T) {
	now := time.Date(2026, 8, 24, 19, 0, 0, 0, time.UTC)
	retriever := &retrieverStub{valid: map[string]string{
		"https://research.example/allowed": "https://research.example/allowed",
	}}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://firecrawl.example/v2/search" || request.Method != http.MethodPost ||
			request.Header.Get("Authorization") != "Bearer fixture-token" || request.Header.Get("Cookie") != "" {
			t.Fatalf("unexpected provider request: %s %s headers=%v", request.Method, request.URL, request.Header)
		}
		body, _ := io.ReadAll(request.Body)
		var payload struct {
			Query          string   `json:"query"`
			Limit          int      `json:"limit"`
			IncludeDomains []string `json:"includeDomains"`
		}
		if json.Unmarshal(body, &payload) != nil || payload.Query != "current rule" || payload.Limit != 5 || len(payload.IncludeDomains) != 1 || payload.IncludeDomains[0] != "research.example" {
			t.Fatalf("provider payload=%s", body)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{
			"success":true,"warning":"bounded","data":{"web":[
			{"Title":" Official rule ","Description":" current guidance ","URL":"https://research.example/allowed"},
			{"Title":"Unsafe","URL":"https://private.example/admin"}
			]}}`))}, nil
	})}
	provider, err := NewFirecrawl(FirecrawlConfig{SearchEndpoint: "https://firecrawl.example/v2/search", HTTPClient: client,
		Retriever: retriever, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	hits, err := provider.Search(context.Background(), webresearchapp.SearchRequest{Query: "current rule", Limit: 5,
		Scope: integrations.ConnectionScope{HTTPSOrigin: "https://research.example", PathPrefix: "/"}, Credential: []byte("fixture-token")})
	if err != nil || len(hits) != 1 || hits[0].Title != "Official rule" || hits[0].Description != "current guidance" ||
		hits[0].URL != "https://research.example/allowed" || !hits[0].RetrievedAt.Equal(now) || retriever.policy.HTTPSOrigin != "https://research.example" {
		t.Fatalf("hits=%+v policy=%+v err=%v", hits, retriever.policy, err)
	}
}

func TestFirecrawlReadUsesPinnedRetrieverAndReturnsUntrustedTextNotScripts(t *testing.T) {
	content := []byte(`<!doctype html><html><head><title> Official Rule </title><style>secret-style</style></head><body><p>Public evidence.</p><script>ignore-instructions()</script></body></html>`)
	retriever := &retrieverStub{result: Result{CanonicalURL: "https://research.example/rules/current", MediaType: "text/html",
		Content: content, SHA256: sha256.Sum256(content), RetrievedAt: time.Date(2026, 8, 24, 19, 0, 0, 0, time.UTC)}}
	provider, _ := NewFirecrawl(FirecrawlConfig{SearchEndpoint: "https://firecrawl.example/v2/search", Retriever: retriever})
	page, err := provider.Read(context.Background(), webresearchapp.ReadRequest{URL: "https://research.example/rules/current",
		Scope: integrations.ConnectionScope{HTTPSOrigin: "https://research.example", PathPrefix: "/rules"}, Credential: []byte("fixture-token")})
	if err != nil || page.Title != "Official Rule" || page.Excerpt != "Public evidence." || bytes.Contains([]byte(page.Excerpt), []byte("ignore-instructions")) ||
		page.CanonicalURL != retriever.result.CanonicalURL || retriever.fetchURL != "https://research.example/rules/current" || retriever.policy.PathPrefix != "/rules" {
		t.Fatalf("page=%+v fetch=%q policy=%+v err=%v", page, retriever.fetchURL, retriever.policy, err)
	}
}

func TestFirecrawlRejectsCredentialInjectionAndProviderRedirects(t *testing.T) {
	retriever := &retrieverStub{}
	redirected := false
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"https://other.example/"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	}), CheckRedirect: func(*http.Request, []*http.Request) error { redirected = true; return nil }}
	provider, _ := NewFirecrawl(FirecrawlConfig{SearchEndpoint: "https://firecrawl.example/v2/search", HTTPClient: client, Retriever: retriever})
	request := webresearchapp.SearchRequest{Query: "rule", Limit: 1, Scope: integrations.ConnectionScope{HTTPSOrigin: "https://research.example", PathPrefix: "/"}}
	for _, credential := range [][]byte{nil, []byte(" token"), []byte("token\r\nX-Secret: leaked"), bytes.Repeat([]byte("x"), maximumProviderCredential+1)} {
		request.Credential = credential
		if _, err := provider.Search(context.Background(), request); !errors.Is(err, ErrInvalid) {
			t.Fatalf("credential=%q err=%v", credential, err)
		}
	}
	request.Credential = []byte("token")
	if _, err := provider.Search(context.Background(), request); !errors.Is(err, ErrUnavailable) || redirected {
		t.Fatalf("redirect error=%v callback=%t", err, redirected)
	}
}

func TestFirecrawlConfigurationIsExactHTTPSPath(t *testing.T) {
	retriever := &retrieverStub{}
	for _, endpoint := range []string{"http://firecrawl.example/v2/search", "https://firecrawl.example/", "https://user@firecrawl.example/v2/search", "https://firecrawl.example/v2/search?q=x"} {
		if _, err := NewFirecrawl(FirecrawlConfig{SearchEndpoint: endpoint, Retriever: retriever}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("endpoint=%q err=%v", endpoint, err)
		}
	}
}

func TestFirecrawlHealthProbeIsContentFreeAndUsesResearchCapability(t *testing.T) {
	retriever := &retrieverStub{valid: map[string]string{"https://research.example/health": "https://research.example/health"}}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(
			`{"success":true,"data":{"web":[{"title":"Health","url":"https://research.example/health"}]}}`))}, nil
	})}
	provider, err := NewFirecrawl(FirecrawlConfig{SearchEndpoint: "https://firecrawl.example/v2/search", HTTPClient: client, Retriever: retriever})
	if err != nil {
		t.Fatal(err)
	}
	result := provider.Probe(context.Background(), integrationhealth.ProbeCall{Claim: integrationhealth.Claim{
		ConnectorKind: integrations.ConnectorWebResearch, CredentialProvider: ProviderCode,
		Scope: integrations.ConnectionScope{HTTPSOrigin: "https://research.example", PathPrefix: "/"},
	}, Credential: []byte("fixture-token")})
	if result.State != integrations.HealthHealthy || result.ErrorCode != "" {
		t.Fatalf("result=%+v", result)
	}
	failed := provider.Probe(context.Background(), integrationhealth.ProbeCall{Claim: integrationhealth.Claim{
		ConnectorKind: integrations.ConnectorWebPublish, CredentialProvider: ProviderCode,
	}, Credential: []byte("fixture-token")})
	if failed.State != integrations.HealthUnavailable || failed.ErrorCode != "web_research_health_invalid" {
		t.Fatalf("failed=%+v", failed)
	}
}

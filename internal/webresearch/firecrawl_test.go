package webresearch

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type resolverStub struct {
	addresses map[string][]net.IPAddr
}

func (r resolverStub) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	return r.addresses[host], nil
}

func TestFirecrawlSearchUsesMetadataOnlyAndFiltersUnsafeResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v2/search" || request.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("unexpected request: %s auth=%q", request.URL.Path, request.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if _, exists := body["scrapeOptions"]; exists {
			t.Fatal("search should not scrape every result")
		}
		if body["query"] != "licensing requirements" || body["limit"] != float64(3) {
			t.Fatalf("unexpected search payload: %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"web":[{"title":"Official guidance","description":"Evidence","url":"https://agency.example/guidance"},{"title":"Unsafe","url":"http://127.0.0.1/admin"}]}}`))
	}))
	defer server.Close()

	client, err := NewFirecrawl(server.URL, "secret", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	client.resolver = resolverStub{addresses: map[string][]net.IPAddr{"agency.example": {{IP: net.ParseIP("93.184.216.34")}}}}
	client.now = func() time.Time { return time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC) }
	results, err := client.Search(context.Background(), SearchRequest{Query: "licensing requirements", Limit: 3, RecencyDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].CitationID == "" || results[0].URL != "https://agency.example/guidance" {
		t.Fatalf("unexpected results: %#v", results)
	}
}

func TestFirecrawlReadDisablesTLSBypassAndBoundsContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["skipTlsVerification"] != false || body["onlyMainContent"] != true {
			t.Fatalf("unsafe scrape payload: %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"markdown":"` + strings.Repeat("a", 800) + `","metadata":{"title":"Page","sourceURL":"https://agency.example/guidance"}}}`))
	}))
	defer server.Close()

	client, _ := NewFirecrawl(server.URL, "", time.Second)
	client.resolver = resolverStub{addresses: map[string][]net.IPAddr{"agency.example": {{IP: net.ParseIP("93.184.216.34")}}}}
	result, err := client.Read(context.Background(), ReadRequest{URL: "https://agency.example/guidance", MaxCharacters: 500})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) != 500 || result.CitationID == "" || result.Title != "Page" {
		t.Fatalf("unexpected read result: %#v", result)
	}
}

func TestFirecrawlReadRejectsPrivateAndCredentialedTargets(t *testing.T) {
	client, _ := NewFirecrawl("http://firecrawl:3002", "", time.Second)
	client.resolver = resolverStub{addresses: map[string][]net.IPAddr{
		"private.example": {{IP: net.ParseIP("10.0.0.8")}},
	}}
	for _, target := range []string{"http://localhost/admin", "http://private.example/admin", "https://user:pass@example.com/"} {
		if _, err := client.Read(context.Background(), ReadRequest{URL: target, MaxCharacters: 500}); err == nil {
			t.Fatalf("expected %q to be rejected", target)
		}
	}
}

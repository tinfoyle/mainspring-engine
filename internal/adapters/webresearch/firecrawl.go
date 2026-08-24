package webresearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationhealth"
	webresearchapp "github.com/tinfoyle/spyglass-engine/internal/application/webresearch"
	integrationsdomain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
)

const (
	ProviderCode               = "firecrawl"
	maximumSearchResponseBytes = int64(4 << 20)
	maximumProviderCredential  = 8 << 10
)

type Retriever interface {
	Validate(context.Context, string, Policy) (string, error)
	Fetch(context.Context, string, Policy) (Result, error)
}

type FirecrawlConfig struct {
	SearchEndpoint string
	HTTPClient     *http.Client
	Retriever      Retriever
	Now            func() time.Time
}

type Firecrawl struct {
	endpoint  *url.URL
	client    *http.Client
	retriever Retriever
	now       func() time.Time
}

func NewFirecrawl(config FirecrawlConfig) (*Firecrawl, error) {
	endpoint, err := url.Parse(strings.TrimSpace(config.SearchEndpoint))
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" ||
		endpoint.Path != "/v2/search" || config.Retriever == nil {
		return nil, ErrInvalid
	}
	if config.HTTPClient == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		transport.DisableCompression = true
		transport.MaxResponseHeaderBytes = DefaultMaximumHeaderBytes
		config.HTTPClient = &http.Client{Transport: transport, Timeout: DefaultTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Firecrawl redirects are denied") }}
	}
	clientCopy := *config.HTTPClient
	if clientCopy.Timeout == 0 || clientCopy.Timeout > time.Minute {
		clientCopy.Timeout = DefaultTimeout
	}
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("Firecrawl redirects are denied") }
	if config.Now == nil {
		config.Now = time.Now
	}
	copy := *endpoint
	return &Firecrawl{endpoint: &copy, client: &clientCopy, retriever: config.Retriever, now: config.Now}, nil
}

func (provider *Firecrawl) Search(ctx context.Context, request webresearchapp.SearchRequest) ([]webresearchapp.SearchHit, error) {
	credential, err := providerCredential(request.Credential)
	if provider == nil || ctx == nil || ctx.Err() != nil || err != nil || strings.TrimSpace(request.Query) == "" ||
		len(request.Query) > webresearchapp.MaximumQueryBytes || request.Limit < 1 || request.Limit > webresearchapp.MaximumResults {
		return nil, ErrInvalid
	}
	origin, err := url.Parse(request.Scope.HTTPSOrigin)
	if err != nil || origin.Hostname() == "" {
		return nil, ErrInvalid
	}
	payload, err := json.Marshal(map[string]any{"query": request.Query, "limit": request.Limit, "sources": []string{"web"},
		"includeDomains": []string{origin.Hostname()}, "ignoreInvalidURLs": true})
	if err != nil {
		return nil, ErrInvalid
	}
	outbound, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, ErrInvalid
	}
	outbound.Header.Set("Authorization", "Bearer "+credential)
	outbound.Header.Set("Content-Type", "application/json")
	outbound.Header.Set("Accept", "application/json")
	response, err := provider.client.Do(outbound)
	if err != nil {
		return nil, errors.Join(ErrUnavailable, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumSearchResponseBytes+1))
	if err != nil {
		return nil, errors.Join(ErrUnavailable, err)
	}
	if int64(len(body)) > maximumSearchResponseBytes {
		return nil, ErrTooLarge
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, fmt.Errorf("%w: Firecrawl HTTP %d", ErrUnavailable, response.StatusCode)
	}
	var envelope struct {
		Success bool `json:"success"`
		Data    struct {
			Web []struct {
				Title, Description, URL string
			} `json:"web"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &envelope) != nil || !envelope.Success || len(envelope.Data.Web) > request.Limit {
		return nil, ErrUnavailable
	}
	retrievedAt := provider.now().UTC()
	result := make([]webresearchapp.SearchHit, 0, len(envelope.Data.Web))
	policy := Policy{HTTPSOrigin: request.Scope.HTTPSOrigin, PathPrefix: request.Scope.PathPrefix}
	for _, item := range envelope.Data.Web {
		canonical, validateErr := provider.retriever.Validate(ctx, item.URL, policy)
		if validateErr != nil {
			continue
		}
		title := truncate(strings.TrimSpace(item.Title), webresearchapp.MaximumResultTextBytes)
		if title == "" {
			title = canonical
		}
		result = append(result, webresearchapp.SearchHit{Title: title, URL: canonical,
			Description: truncate(strings.TrimSpace(item.Description), webresearchapp.MaximumResultTextBytes), RetrievedAt: retrievedAt})
	}
	return result, nil
}

func (provider *Firecrawl) Read(ctx context.Context, request webresearchapp.ReadRequest) (webresearchapp.ReadPage, error) {
	if provider == nil || ctx == nil || ctx.Err() != nil {
		return webresearchapp.ReadPage{}, ErrInvalid
	}
	if _, err := providerCredential(request.Credential); err != nil {
		return webresearchapp.ReadPage{}, err
	}
	result, err := provider.retriever.Fetch(ctx, request.URL, Policy{HTTPSOrigin: request.Scope.HTTPSOrigin, PathPrefix: request.Scope.PathPrefix})
	if err != nil {
		return webresearchapp.ReadPage{}, err
	}
	title, excerpt := pageText(result.MediaType, result.Content)
	return webresearchapp.ReadPage{CanonicalURL: result.CanonicalURL, Title: title, MediaType: result.MediaType,
		Content: result.Content, Excerpt: truncate(excerpt, webresearchapp.MaximumExcerptBytes), SHA256: result.SHA256,
		RetrievedAt: result.RetrievedAt}, nil
}

func (provider *Firecrawl) Probe(ctx context.Context, call integrationhealth.ProbeCall) integrationhealth.ProbeResult {
	if provider == nil || call.Claim.ConnectorKind != integrationsdomain.ConnectorWebResearch ||
		call.Claim.CredentialProvider != ProviderCode || call.Claim.Scope.HTTPSOrigin == "" {
		return integrationhealth.ProbeResult{State: integrationsdomain.HealthUnavailable, ErrorCode: "web_research_health_invalid"}
	}
	hits, err := provider.Search(ctx, webresearchapp.SearchRequest{Query: "site health", Limit: 1,
		Scope: call.Claim.Scope, Credential: call.Credential})
	for index := range hits {
		hits[index] = webresearchapp.SearchHit{}
	}
	if err != nil {
		return integrationhealth.ProbeResult{State: integrationsdomain.HealthUnavailable, ErrorCode: "web_research_provider_unavailable"}
	}
	return integrationhealth.ProbeResult{State: integrationsdomain.HealthHealthy}
}

func providerCredential(value []byte) (string, error) {
	if len(value) == 0 || len(value) > maximumProviderCredential || !utf8.Valid(value) {
		return "", ErrInvalid
	}
	credential := string(value)
	if credential != strings.TrimSpace(credential) || strings.ContainsAny(credential, "\x00\r\n") {
		return "", ErrInvalid
	}
	return credential, nil
}

func pageText(mediaType string, content []byte) (string, string) {
	if mediaType == "text/plain" {
		return "", strings.TrimSpace(string(content))
	}
	if mediaType != "text/html" {
		return "", ""
	}
	root, err := html.Parse(bytes.NewReader(content))
	if err != nil {
		return "", ""
	}
	var title string
	words := make([]string, 0, 128)
	var visit func(*html.Node, bool)
	visit = func(node *html.Node, ignored bool) {
		if node.Type == html.ElementNode {
			switch node.Data {
			case "script", "style", "noscript", "svg", "template":
				ignored = true
			}
		}
		if node.Type == html.TextNode && !ignored {
			value := strings.Join(strings.Fields(node.Data), " ")
			if value != "" {
				if node.Parent != nil && node.Parent.Type == html.ElementNode && node.Parent.Data == "title" && title == "" {
					title = value
				} else if node.Parent == nil || node.Parent.Data != "title" {
					words = append(words, value)
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child, ignored)
		}
	}
	visit(root, false)
	return truncate(title, webresearchapp.MaximumResultTextBytes), strings.Join(words, " ")
}

func truncate(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	value = value[:maximum]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

var _ webresearchapp.Provider = (*Firecrawl)(nil)
var _ integrationhealth.Probe = (*Firecrawl)(nil)

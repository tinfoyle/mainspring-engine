package webresearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const maximumFirecrawlResponseBytes = 4 << 20

type IPResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type Firecrawl struct {
	baseURL        *url.URL
	token          string
	client         *http.Client
	resolver       IPResolver
	now            func() time.Time
	requestTimeout time.Duration
}

func NewFirecrawl(baseURL, token string, timeout time.Duration) (*Firecrawl, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("Firecrawl base URL must be an absolute HTTP or HTTPS URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("Firecrawl base URL must use HTTP or HTTPS")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Firecrawl{
		baseURL: parsed, token: strings.TrimSpace(token), client: &http.Client{Timeout: timeout},
		resolver: net.DefaultResolver, now: time.Now, requestTimeout: timeout,
	}, nil
}

func (f *Firecrawl) Search(ctx context.Context, request SearchRequest) ([]SearchResult, error) {
	payload := map[string]any{
		"query": request.Query, "limit": request.Limit, "sources": []string{"web"},
		"ignoreInvalidURLs": true, "timeout": min(f.requestTimeout.Milliseconds(), int64(300000)),
	}
	if len(request.IncludeDomains) > 0 {
		payload["includeDomains"] = request.IncludeDomains
	}
	if len(request.ExcludeDomains) > 0 {
		payload["excludeDomains"] = request.ExcludeDomains
	}
	if request.RecencyDays > 0 {
		end := f.now().UTC()
		start := end.AddDate(0, 0, -request.RecencyDays)
		payload["tbs"] = fmt.Sprintf("cdr:1,cd_min:%s,cd_max:%s", start.Format("01/02/2006"), end.Format("01/02/2006"))
	}
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Web []struct {
				Title       string `json:"title"`
				Description string `json:"description"`
				URL         string `json:"url"`
			} `json:"web"`
		} `json:"data"`
		Warning string `json:"warning"`
	}
	if err := f.post(ctx, "/v2/search", payload, &response); err != nil {
		return nil, err
	}
	if !response.Success {
		return nil, errors.New("Firecrawl search was not successful")
	}
	retrievedAt := f.now().UTC()
	results := make([]SearchResult, 0, min(request.Limit, len(response.Data.Web)))
	for _, item := range response.Data.Web {
		if len(results) == request.Limit {
			break
		}
		if err := validatePublicURL(ctx, f.resolver, item.URL); err != nil {
			continue
		}
		results = append(results, SearchResult{
			CitationID: CitationID(item.URL), Title: strings.TrimSpace(item.Title), URL: item.URL,
			Description: truncateUTF8(strings.TrimSpace(item.Description), 1000), RetrievedAt: retrievedAt,
		})
	}
	return results, nil
}

func (f *Firecrawl) Read(ctx context.Context, request ReadRequest) (ReadResult, error) {
	if err := validatePublicURL(ctx, f.resolver, request.URL); err != nil {
		return ReadResult{}, err
	}
	payload := map[string]any{
		"url": request.URL, "formats": []string{"markdown"}, "onlyMainContent": true,
		"skipTlsVerification": false, "timeout": min(f.requestTimeout.Milliseconds(), int64(300000)),
	}
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Markdown string `json:"markdown"`
			Metadata struct {
				Title       string `json:"title"`
				Description string `json:"description"`
				SourceURL   string `json:"sourceURL"`
				URL         string `json:"url"`
			} `json:"metadata"`
		} `json:"data"`
	}
	if err := f.post(ctx, "/v2/scrape", payload, &response); err != nil {
		return ReadResult{}, err
	}
	if !response.Success {
		return ReadResult{}, errors.New("Firecrawl scrape was not successful")
	}
	resolvedURL := strings.TrimSpace(response.Data.Metadata.SourceURL)
	if resolvedURL == "" {
		resolvedURL = strings.TrimSpace(response.Data.Metadata.URL)
	}
	if resolvedURL == "" {
		resolvedURL = request.URL
	}
	if err := validatePublicURL(ctx, f.resolver, resolvedURL); err != nil {
		return ReadResult{}, errors.New("Firecrawl returned an unsafe source URL")
	}
	return ReadResult{
		CitationID: CitationID(resolvedURL), Title: strings.TrimSpace(response.Data.Metadata.Title), URL: resolvedURL,
		Description: truncateUTF8(strings.TrimSpace(response.Data.Metadata.Description), 1000),
		Content:     truncateUTF8(strings.TrimSpace(response.Data.Markdown), request.MaxCharacters), RetrievedAt: f.now().UTC(),
	}, nil
}

func (f *Firecrawl) post(ctx context.Context, path string, input, output any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	endpoint := *f.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if f.token != "" {
		request.Header.Set("Authorization", "Bearer "+f.token)
	}
	response, err := f.client.Do(request)
	if err != nil {
		return fmt.Errorf("call Firecrawl: %w", err)
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, maximumFirecrawlResponseBytes+1)
	responseBody, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("read Firecrawl response: %w", err)
	}
	if len(responseBody) > maximumFirecrawlResponseBytes {
		return errors.New("Firecrawl response exceeded the configured limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Firecrawl returned HTTP %d", response.StatusCode)
	}
	if err := json.Unmarshal(responseBody, output); err != nil {
		return fmt.Errorf("decode Firecrawl response: %w", err)
	}
	return nil
}

func validatePublicURL(ctx context.Context, resolver IPResolver, rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return errors.New("web URL must be an absolute HTTP or HTTPS URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("web URL must use HTTP or HTTPS")
	}
	if parsed.User != nil {
		return errors.New("web URL must not include credentials")
	}
	if port := parsed.Port(); port != "" && port != "80" && port != "443" {
		return errors.New("web URL may only use ports 80 or 443")
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".home.arpa") {
		return errors.New("web URL must reference a public host")
	}
	addresses, err := resolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		return errors.New("web URL host could not be resolved")
	}
	for _, address := range addresses {
		value, ok := netip.AddrFromSlice(address.IP)
		if !ok || !isPublicAddress(value.Unmap()) {
			return errors.New("web URL resolved to a non-public address")
		}
	}
	return nil
}

func isPublicAddress(address netip.Addr) bool {
	if !address.IsValid() || address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	for _, prefix := range []netip.Prefix{
		netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("2001:db8::/32"),
	} {
		if prefix.Contains(address) {
			return false
		}
	}
	return address.IsGlobalUnicast()
}

func truncateUTF8(value string, maximum int) string {
	if maximum <= 0 || len(value) <= maximum {
		return value
	}
	if utf8.RuneCountInString(value) <= maximum {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	return string(runes[:maximum])
}

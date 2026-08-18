// staging-cert records a sanitized, non-mutating certification of one deployed
// Infinite Ocean website and Spyglass Account API release.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"
)

const maxEvidenceBody = 4 << 20

var (
	revisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	publicPaths     = []string{"/", "/about", "/packages", "/pricing", "/privacy", "/product", "/security", "/signup", "/terms"}
)

type config struct {
	Environment      string
	WebsiteOrigin    string
	AppOrigin        string
	Revision         string
	ImageDigest      string
	CatalogVersion   uint64
	ExpectedPackages []string
	ExpectedOffers   []string
	Output           string
	Timeout          time.Duration
}

type report struct {
	SchemaVersion  int       `json:"schema_version"`
	Environment    string    `json:"environment"`
	Revision       string    `json:"revision"`
	ImageDigest    string    `json:"image_digest"`
	WebsiteOrigin  string    `json:"website_origin"`
	AppOrigin      string    `json:"app_origin"`
	StartedAt      time.Time `json:"started_at"`
	CompletedAt    time.Time `json:"completed_at"`
	CatalogVersion uint64    `json:"catalog_version"`
	Success        bool      `json:"success"`
	Checks         []check   `json:"checks"`
}

type check struct {
	Name         string `json:"name"`
	Target       string `json:"target"`
	Status       int    `json:"status,omitempty"`
	DurationMS   int64  `json:"duration_ms"`
	BodySHA256   string `json:"body_sha256,omitempty"`
	ContentBytes int    `json:"content_bytes,omitempty"`
	Passed       bool   `json:"passed"`
	Error        string `json:"error,omitempty"`
}

type catalogProjection struct {
	Version     uint64    `json:"version"`
	PublishedAt time.Time `json:"published_at"`
	Packages    []struct {
		Code string `json:"code"`
	} `json:"packages"`
	Plans []struct {
		Code string `json:"code"`
	} `json:"plans"`
	Offers []struct {
		Code string `json:"code"`
	} `json:"offers"`
}

func main() {
	var value config
	var packages, offers string
	flag.StringVar(&value.Environment, "environment", "staging", "exact environment name")
	flag.StringVar(&value.WebsiteOrigin, "website-origin", "", "exact public Infinite Ocean HTTPS origin")
	flag.StringVar(&value.AppOrigin, "app-origin", "", "exact public Spyglass HTTPS origin")
	flag.StringVar(&value.Revision, "revision", "", "full lowercase Git revision")
	flag.StringVar(&value.ImageDigest, "image-digest", "", "release image digest (sha256:...)")
	flag.Uint64Var(&value.CatalogVersion, "catalog-version", 0, "expected published Catalog version")
	flag.StringVar(&packages, "packages", "", "comma-separated required package codes")
	flag.StringVar(&offers, "offers", "", "comma-separated required effective offer codes")
	flag.StringVar(&value.Output, "output", "-", "new evidence JSON file, or - for stdout")
	flag.DurationVar(&value.Timeout, "timeout", 10*time.Second, "per-request timeout from 1s through 60s")
	flag.Parse()
	value.ExpectedPackages = splitSet(packages)
	value.ExpectedOffers = splitSet(offers)
	if err := value.validate(); err != nil {
		fmt.Fprintln(os.Stderr, "configuration:", err)
		os.Exit(2)
	}

	client := &http.Client{Timeout: value.Timeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("redirects are not accepted") }}
	evidence := certify(context.Background(), value, client, time.Now)
	if err := writeReport(value.Output, evidence); err != nil {
		fmt.Fprintln(os.Stderr, "write evidence:", err)
		os.Exit(2)
	}
	if !evidence.Success {
		os.Exit(1)
	}
}

func (c config) validate() error {
	if !regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`).MatchString(c.Environment) {
		return errors.New("environment must be a lowercase machine name")
	}
	for name, raw := range map[string]string{"website-origin": c.WebsiteOrigin, "app-origin": c.AppOrigin} {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.String() != raw {
			return fmt.Errorf("%s must be an exact HTTPS origin", name)
		}
	}
	if c.WebsiteOrigin == c.AppOrigin {
		return errors.New("website-origin and app-origin must be separate origins")
	}
	if !revisionPattern.MatchString(c.Revision) {
		return errors.New("revision must be exactly 40 lowercase hexadecimal characters")
	}
	if !digestPattern.MatchString(c.ImageDigest) {
		return errors.New("image-digest must be sha256 followed by 64 lowercase hexadecimal characters")
	}
	if c.CatalogVersion == 0 || len(c.ExpectedPackages) == 0 || len(c.ExpectedOffers) == 0 {
		return errors.New("catalog-version, packages, and offers are required")
	}
	if c.Timeout < time.Second || c.Timeout > time.Minute {
		return errors.New("timeout must be from 1s through 60s")
	}
	return nil
}

func certify(ctx context.Context, cfg config, client *http.Client, now func() time.Time) report {
	started := now().UTC()
	result := report{SchemaVersion: 1, Environment: cfg.Environment, Revision: cfg.Revision, ImageDigest: cfg.ImageDigest, WebsiteOrigin: cfg.WebsiteOrigin, AppOrigin: cfg.AppOrigin, StartedAt: started, CatalogVersion: cfg.CatalogVersion}
	for _, path := range publicPaths {
		name := "website " + path
		result.Checks = append(result.Checks, perform(ctx, client, name, cfg.WebsiteOrigin+path, func(response *http.Response, body []byte) error {
			if err := requireResponse(response, http.StatusOK, "text/html"); err != nil {
				return err
			}
			if err := requirePublicHeaders(response, true); err != nil {
				return err
			}
			if strings.Contains(strings.ToLower(string(body)), "mainspring") {
				return errors.New("historical product name is present")
			}
			if path == "/signup" {
				text := strings.ToLower(string(body))
				if !strings.Contains(text, strings.ToLower(cfg.AppOrigin+"/signup")) || strings.Contains(text, `name="email"`) || strings.Contains(text, `name="account_name"`) {
					return errors.New("signup is not a GET-only private-origin handoff")
				}
			}
			return nil
		}))
	}
	for _, target := range []struct {
		name string
		url  string
	}{
		{name: "website Catalog proxy", url: cfg.WebsiteOrigin + "/api/catalog"},
		{name: "Account API Catalog", url: cfg.AppOrigin + "/api/v1/catalog/public"},
	} {
		result.Checks = append(result.Checks, perform(ctx, client, target.name, target.url, func(response *http.Response, body []byte) error {
			if err := requireResponse(response, http.StatusOK, "application/json"); err != nil {
				return err
			}
			if err := requirePublicHeaders(response, false); err != nil {
				return err
			}
			return verifyCatalog(body, cfg)
		}))
	}
	result.Success = true
	for _, item := range result.Checks {
		result.Success = result.Success && item.Passed
	}
	result.CompletedAt = now().UTC()
	return result
}

func perform(ctx context.Context, client *http.Client, name, target string, validate func(*http.Response, []byte) error) check {
	started := time.Now()
	item := check{Name: name, Target: target}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err == nil {
		request.Header.Set("Accept", "text/html,application/json")
		var response *http.Response
		response, err = client.Do(request)
		if err == nil {
			defer response.Body.Close()
			item.Status = response.StatusCode
			var body []byte
			body, err = io.ReadAll(io.LimitReader(response.Body, maxEvidenceBody+1))
			if err == nil && len(body) > maxEvidenceBody {
				err = errors.New("response exceeds 4 MiB evidence bound")
			}
			if err == nil {
				sum := sha256.Sum256(body)
				item.BodySHA256, item.ContentBytes = hex.EncodeToString(sum[:]), len(body)
				err = validate(response, body)
			}
		}
	}
	item.DurationMS = time.Since(started).Milliseconds()
	item.Passed = err == nil
	if err != nil {
		item.Error = err.Error()
	}
	return item
}

func requireResponse(response *http.Response, status int, mediaType string) error {
	if response.StatusCode != status {
		return fmt.Errorf("status %d, expected %d", response.StatusCode, status)
	}
	if !strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), mediaType) {
		return fmt.Errorf("Content-Type is not %s", mediaType)
	}
	return nil
}

func requirePublicHeaders(response *http.Response, html bool) error {
	if !strings.Contains(response.Header.Get("Strict-Transport-Security"), "max-age=") {
		return errors.New("Strict-Transport-Security is missing")
	}
	if response.Header.Get("X-Content-Type-Options") != "nosniff" {
		return errors.New("X-Content-Type-Options is not nosniff")
	}
	if html {
		policy := response.Header.Get("Content-Security-Policy")
		if !strings.Contains(policy, "default-src 'self'") || !strings.Contains(policy, "frame-ancestors 'none'") || !strings.Contains(policy, "script-src 'self'") {
			return errors.New("Content-Security-Policy is incomplete")
		}
		if response.Header.Get("X-Frame-Options") != "DENY" {
			return errors.New("X-Frame-Options is not DENY")
		}
		if response.Header.Get("Referrer-Policy") != "strict-origin-when-cross-origin" || response.Header.Get("Cross-Origin-Opener-Policy") != "same-origin" || response.Header.Get("Cross-Origin-Resource-Policy") != "same-origin" {
			return errors.New("referrer or cross-origin isolation policy is incomplete")
		}
		permissions := response.Header.Get("Permissions-Policy")
		if !strings.Contains(permissions, "camera=()") || !strings.Contains(permissions, "microphone=()") || !strings.Contains(permissions, "payment=()") {
			return errors.New("Permissions-Policy is incomplete")
		}
	}
	return nil
}

func verifyCatalog(body []byte, cfg config) error {
	if strings.Contains(strings.ToLower(string(body)), "stripe") {
		return errors.New("Catalog contains a provider reference")
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	var catalog catalogProjection
	if err := decoder.Decode(&catalog); err != nil {
		return fmt.Errorf("decode Catalog projection: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("Catalog contains more than one JSON value")
	}
	if catalog.Version != cfg.CatalogVersion || catalog.PublishedAt.IsZero() || len(catalog.Plans) == 0 {
		return errors.New("Catalog version, publication time, or Plans do not match certification input")
	}
	packages := make([]string, 0, len(catalog.Packages))
	for _, item := range catalog.Packages {
		packages = append(packages, item.Code)
	}
	offers := make([]string, 0, len(catalog.Offers))
	for _, item := range catalog.Offers {
		offers = append(offers, item.Code)
	}
	for _, expected := range cfg.ExpectedPackages {
		if !slices.Contains(packages, expected) {
			return fmt.Errorf("required package %q is missing", expected)
		}
	}
	for _, expected := range cfg.ExpectedOffers {
		if !slices.Contains(offers, expected) {
			return fmt.Errorf("required offer %q is missing", expected)
		}
	}
	return nil
}

func splitSet(raw string) []string {
	seen := map[string]bool{}
	var result []string
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item != "" && !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	slices.Sort(result)
	return result
}

func writeReport(path string, value report) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if path == "-" {
		_, err = os.Stdout.Write(raw)
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err = file.Write(raw); err != nil {
		return err
	}
	return file.Sync()
}

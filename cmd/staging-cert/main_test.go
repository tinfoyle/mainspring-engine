package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const catalogFixture = `{"version":2,"published_at":"2026-08-18T12:00:00Z","packages":[{"code":"knowledge","version":1,"name":"Knowledge","description":"Facts","features":["knowledge.read"]},{"code":"work","version":1,"name":"Work","description":"Work","features":["work.read"]}],"limits":[],"plans":[{"code":"free","version":1,"name":"Free","description":"Explore","packages":{"knowledge":"enabled"}}],"offers":[{"code":"free-v1","plan_code":"free","plan_version":1,"currency":"USD","amount_minor":0,"billing_interval":"none","effective_from":"2026-08-18T12:00:00Z"}]}`

func TestCertifyProducesSanitizedSuccessfulEvidence(t *testing.T) {
	var appOrigin string
	website := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		certificationHeaders(w, strings.HasPrefix(r.URL.Path, "/api/"))
		if r.URL.Path == "/api/catalog" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(catalogFixture))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><a href="` + appOrigin + `/signup">Create account</a></body></html>`))
	}))
	defer website.Close()
	app := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		certificationHeaders(w, true)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(catalogFixture))
	}))
	defer app.Close()
	appOrigin = app.URL
	client := trustedClient(t, website, app)
	cfg := validConfig(website.URL, app.URL)
	now := func() time.Time { return time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC) }

	result := certify(context.Background(), cfg, client, now)
	if !result.Success || len(result.Checks) != len(publicPaths)+2 {
		t.Fatalf("unexpected report: %+v", result)
	}
	for _, item := range result.Checks {
		if !item.Passed || item.BodySHA256 == "" || item.ContentBytes == 0 || item.Error != "" {
			t.Fatalf("unexpected check: %+v", item)
		}
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), catalogFixture) {
		t.Fatal("evidence retained a response body")
	}
}

func TestCertifyFailsClosedOnMissingEdgePolicy(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html></html>"))
	}))
	defer server.Close()
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		certificationHeaders(w, true)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(catalogFixture))
	}))
	defer other.Close()

	result := certify(context.Background(), validConfig(server.URL, other.URL), trustedClient(t, server, other), time.Now)
	if result.Success || result.Checks[0].Passed || !strings.Contains(result.Checks[0].Error, "Strict-Transport-Security") {
		t.Fatalf("missing policy was accepted: %+v", result.Checks[0])
	}
}

func TestConfigAndEvidenceFileAreStrict(t *testing.T) {
	cfg := validConfig("https://infiniteocean.net", "https://app.infiniteocean.net")
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	bad := cfg
	bad.WebsiteOrigin = "https://infiniteocean.net/path"
	if err := bad.validate(); err == nil {
		t.Fatal("origin path was accepted")
	}
	path := filepath.Join(t.TempDir(), "evidence.json")
	if err := writeReport(path, report{SchemaVersion: 1}); err != nil {
		t.Fatal(err)
	}
	if err := writeReport(path, report{SchemaVersion: 1}); err == nil {
		t.Fatal("existing evidence was overwritten")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("evidence permissions=%v", info.Mode().Perm())
	}
}

func validConfig(website, app string) config {
	return config{Environment: "staging", WebsiteOrigin: website, AppOrigin: app, Revision: strings.Repeat("a", 40), ImageDigest: "sha256:" + strings.Repeat("b", 64), CatalogVersion: 2, ExpectedPackages: []string{"knowledge", "work"}, ExpectedOffers: []string{"free-v1"}, Output: "-", Timeout: 5 * time.Second}
}

func certificationHeaders(w http.ResponseWriter, api bool) {
	w.Header().Set("Strict-Transport-Security", "max-age=31536000")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !api {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; script-src 'self'")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), payment=()")
	}
}

func trustedClient(t *testing.T, servers ...*httptest.Server) *http.Client {
	t.Helper()
	roots := x509.NewCertPool()
	for _, server := range servers {
		roots.AddCert(server.Certificate())
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	return &http.Client{Transport: transport, Timeout: 5 * time.Second}
}

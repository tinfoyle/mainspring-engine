// Command web-research-fixture serves a deterministic local-only HTTPS search
// provider and public origin. It exists solely to certify the real hardened
// web-research boundary without contacting the public Internet.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

type fixture struct {
	origin  string
	token   string
	search  atomic.Uint64
	reads   atomic.Uint64
	changed atomic.Bool
}

func main() {
	address := envOr("WEB_RESEARCH_FIXTURE_ADDRESS", ":9443")
	origin := strings.TrimSuffix(strings.TrimSpace(os.Getenv("WEB_RESEARCH_FIXTURE_PUBLIC_ORIGIN")), "/")
	token := os.Getenv("WEB_RESEARCH_FIXTURE_TOKEN")
	certificateFile := envOr("WEB_RESEARCH_FIXTURE_CERTIFICATE_FILE", "/run/web-research-fixture/tls.crt")
	keyFile := envOr("WEB_RESEARCH_FIXTURE_KEY_FILE", "/run/web-research-fixture/tls.key")
	rootCAFile := envOr("WEB_RESEARCH_FIXTURE_ROOT_CA_FILE", "/run/web-research-fixture/ca.crt")
	if origin == "" || token == "" || token != strings.TrimSpace(token) || strings.ContainsAny(token, "\x00\r\n") {
		log.Fatal("web research fixture configuration is invalid")
	}
	if len(os.Args) == 2 && (os.Args[1] == "healthcheck" || os.Args[1] == "control-change" || os.Args[1] == "stats") {
		if err := fixtureCommand(os.Args[1], address, origin, rootCAFile, token); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) != 1 {
		log.Fatal("web-research-fixture accepts only the optional healthcheck command")
	}
	certificate, err := tls.LoadX509KeyPair(certificateFile, keyFile)
	if err != nil {
		log.Fatal(err)
	}
	value := &fixture{origin: origin, token: token}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", value.health)
	mux.HandleFunc("POST /v2/search", value.searchHandler)
	mux.HandleFunc("GET /authoritative/article", value.article)
	mux.HandleFunc("GET /authoritative/redirect", value.redirect)
	mux.HandleFunc("POST /control/change", value.change)
	mux.HandleFunc("GET /stats", value.stats)
	server := &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second,
		WriteTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 32 << 10,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServeTLS("", "") }()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	case err := <-done:
		log.Fatal(err)
	}
}

func (fixture *fixture) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (fixture *fixture) searchHandler(w http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Authorization") != "Bearer "+fixture.token || request.Header.Get("Content-Type") != "application/json" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"success": false})
		return
	}
	var input struct {
		Query             string   `json:"query"`
		Limit             int      `json:"limit"`
		Sources           []string `json:"sources"`
		IncludeDomains    []string `json:"includeDomains"`
		IgnoreInvalidURLs bool     `json:"ignoreInvalidURLs"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, request.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || strings.TrimSpace(input.Query) == "" || input.Limit < 1 || input.Limit > 10 ||
		len(input.Sources) != 1 || input.Sources[0] != "web" || len(input.IncludeDomains) != 1 || !strings.Contains(fixture.origin, "://"+input.IncludeDomains[0]) || !input.IgnoreInvalidURLs {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false})
		return
	}
	fixture.search.Add(1)
	items := []map[string]string{{"title": "Authoritative local research", "description": "A deterministic source used to certify scoped public-web retrieval.", "url": fixture.origin + "/authoritative/article"},
		{"title": "Scoped redirect", "description": "A same-origin redirect that is independently revalidated.", "url": fixture.origin + "/authoritative/redirect"},
		{"title": "Rejected private result", "description": "This result must never cross the adapter boundary.", "url": "https://localhost/secret"}}
	if len(items) > input.Limit {
		items = items[:input.Limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"web": items}})
}

func (fixture *fixture) article(w http.ResponseWriter, _ *http.Request) {
	fixture.reads.Add(1)
	version := "one"
	if fixture.changed.Load() {
		version = "two"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte("<!doctype html><html><head><title>Authoritative fixture</title><script>secretNoise()</script></head><body><main><h1>Scoped research</h1><p>Deterministic content version " + version + ".</p></main></body></html>"))
}

func (fixture *fixture) redirect(w http.ResponseWriter, _ *http.Request) {
	fixture.reads.Add(1)
	w.Header().Set("Location", fixture.origin+"/authoritative/article")
	w.WriteHeader(http.StatusFound)
}

func (fixture *fixture) change(w http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Authorization") != "Bearer "+fixture.token {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	fixture.changed.Store(true)
	w.WriteHeader(http.StatusNoContent)
}

func (fixture *fixture) stats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"searches": fixture.search.Load(), "reads": fixture.reads.Load(), "changed": fixture.changed.Load()})
}

func fixtureCommand(command, address, origin, rootCAFile, token string) error {
	pem, err := os.ReadFile(rootCAFile)
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return errors.New("fixture CA is invalid")
	}
	host := strings.TrimPrefix(address, ":")
	if host == address {
		host = strings.TrimPrefix(address, "0.0.0.0:")
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots,
		ServerName: strings.TrimPrefix(strings.Split(origin, "://")[1], "//")}}, Timeout: 3 * time.Second}
	method, path := http.MethodGet, "/health"
	if command == "control-change" {
		method, path = http.MethodPost, "/control/change"
	} else if command == "stats" {
		path = "/stats"
	}
	request, err := http.NewRequest(method, "https://127.0.0.1:"+host+path, nil)
	if err != nil {
		return err
	}
	if command == "control-change" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	expected := http.StatusOK
	if command == "control-change" {
		expected = http.StatusNoContent
	}
	if response.StatusCode != expected {
		return errors.New("fixture is not ready")
	}
	if command == "stats" {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 4097))
		if readErr != nil || len(body) > 4096 || !json.Valid(body) {
			return errors.New("fixture statistics are invalid")
		}
		_, _ = fmt.Print(string(body))
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

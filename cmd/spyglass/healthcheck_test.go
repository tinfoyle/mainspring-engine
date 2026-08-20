package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthcheckAcceptsReadyEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/health/ready" || request.Header.Get("Accept") != "application/json" {
			t.Fatalf("request path=%q accept=%q", request.URL.Path, request.Header.Get("Accept"))
		}
		_, _ = response.Write([]byte(`{"status":"ready"}`))
	}))
	defer server.Close()
	if err := runHealthcheck([]string{"--url=" + server.URL + "/health/ready"}); err != nil {
		t.Fatal(err)
	}
}

func TestHealthcheckFailsClosed(t *testing.T) {
	redirect := httptest.NewServer(http.RedirectHandler("https://example.invalid", http.StatusFound))
	defer redirect.Close()
	if err := runHealthcheck([]string{"--url=" + redirect.URL}); err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("redirect error=%v", err)
	}

	large := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Length", fmt.Sprint(healthcheckMaximumResponseBytes+1))
		_, _ = response.Write(make([]byte, healthcheckMaximumResponseBytes+1))
	}))
	defer large.Close()
	if err := runHealthcheck([]string{"--url=" + large.URL}); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("large response error=%v", err)
	}

	for name, arguments := range map[string][]string{
		"credentials":  {"--url=http://user:secret@example.invalid/health/ready"},
		"query":        {"--url=http://example.invalid/health/ready?verbose=true"},
		"tls-on-http":  {"--url=http://example.invalid/health/ready", "--ca-file=ca.pem"},
		"partial-mtls": {"--url=https://example.invalid/health/ready", "--cert-file=client.pem"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := runHealthcheck(arguments); err == nil {
				t.Fatal("invalid healthcheck configuration was accepted")
			}
		})
	}
}

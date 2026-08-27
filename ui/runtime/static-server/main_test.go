package main

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestHandlerServesHealthAssetsAndSPAFallback(t *testing.T) {
	assets := fstest.MapFS{
		"index.html":       &fstest.MapFile{Data: []byte(`<div id="app"></div>`)},
		"ui-assets/app.js": &fstest.MapFile{Data: []byte(`console.log("ready")`)},
	}
	handler := newHandler(assets)

	tests := []struct {
		name        string
		path        string
		wantBody    string
		wantCache   string
		contentType string
	}{
		{name: "health", path: "/health/ready", wantBody: "ready", wantCache: "no-store", contentType: "text/plain; charset=utf-8"},
		{name: "asset", path: "/ui-assets/app.js", wantBody: `console.log("ready")`, contentType: "text/javascript; charset=utf-8"},
		{name: "spa fallback", path: "/app/your-turn", wantBody: `<div id="app"></div>`, wantCache: "no-cache", contentType: "text/html; charset=utf-8"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
			}
			if strings.TrimSpace(response.Body.String()) != test.wantBody {
				t.Fatalf("body = %q, want %q", response.Body.String(), test.wantBody)
			}
			if got := response.Header().Get("Cache-Control"); got != test.wantCache {
				t.Fatalf("Cache-Control = %q, want %q", got, test.wantCache)
			}
			if got := response.Header().Get("Content-Type"); got != test.contentType {
				t.Fatalf("Content-Type = %q, want %q", got, test.contentType)
			}
			if got := response.Header().Get("Content-Security-Policy"); !strings.Contains(got, "frame-ancestors 'none'") {
				t.Fatalf("Content-Security-Policy = %q", got)
			}
		})
	}
}

func TestHandlerReturnsUnavailableWithoutIndex(t *testing.T) {
	var assets fs.FS = fstest.MapFS{}
	request := httptest.NewRequest(http.MethodGet, "/missing", nil)
	response := httptest.NewRecorder()

	newHandler(assets).ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

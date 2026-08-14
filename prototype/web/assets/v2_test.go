package assets

import (
	"strings"
	"testing"
)

func TestV2BundleUsesPersistentClientNavigation(t *testing.T) {
	script, err := Files.ReadFile("v2.js")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, contract := range []string{"pushState", "popstate", "EventSource", "refetchInterval", "refetchOnWindowFocus", "refetchOnReconnect", "/api/v2/work", "/api/v2/documents", "/api/v2/baseline", "/api/v2/finance", "/api/v2/inbox"} {
		if !strings.Contains(text, contract) {
			t.Fatalf("v2.js does not contain %q", contract)
		}
	}
	for _, forbidden := range []string{"window.location.reload", "htmx:beforeRequest", "htmx:afterSwap"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("v2.js must not refresh workspace state via %q", forbidden)
		}
	}
}

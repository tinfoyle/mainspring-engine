package components

import (
	"context"
	"strings"
	"testing"
)

func TestV2AppPageProvidesPersistentClientRoot(t *testing.T) {
	var output strings.Builder
	err := V2AppPage("Work", "Infinite Ocean LLC", UserView{
		DisplayName: "Owner", Email: "owner@example.com", Role: "owner", Development: true,
	}, "csrf-token").Render(context.Background(), &output)
	if err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, expected := range []string{
		`id="mainspring-v2-root"`, `data-tenant="Infinite Ocean LLC"`, `data-csrf="csrf-token"`,
		`src="/assets/v2.js"`, `href="/assets/v2.css"`,
	} {
		if !strings.Contains(html, expected) {
			t.Fatalf("V2 shell missing %q", expected)
		}
	}
	if strings.Contains(html, "htmx.min.js") {
		t.Fatal("V2 shell should not load the legacy page mutation runtime")
	}
}

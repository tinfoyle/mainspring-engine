package browserapp

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountaccess"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestWorkTemplateExposesRoutedQueueOnlyForAvailablePackage(t *testing.T) {
	pages, err := template.New("pages").Parse(pageTemplates)
	if err != nil {
		t.Fatal(err)
	}
	accountID := ids.AccountID("01J00000000000000000000000")
	data := pageData{
		Title:         "Work",
		Page:          "work",
		Selected:      &accountaccess.Choice{AccountID: accountID, DisplayName: "Northstar Studio"},
		Choices:       []accountaccess.Choice{{AccountID: accountID, DisplayName: "Northstar Studio"}},
		WorkAvailable: true,
		Script:        "/assets/work.js",
	}
	var rendered bytes.Buffer
	if err := pages.ExecuteTemplate(&rendered, "work", data); err != nil {
		t.Fatal(err)
	}
	body := rendered.String()
	for _, expected := range []string{
		`src="/assets/work.js"`,
		`class="active" href="/app/work"`,
		`data-account-id="01J00000000000000000000000"`,
		`id="work-filters"`,
		`id="work-detail"`,
		`data-summary="urgent"`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("Work shell missing %q: %s", expected, body)
		}
	}
	if strings.Contains(body, "capacity_reservation") || strings.Contains(body, "capacity_released") {
		t.Fatalf("Work shell exposed internal capacity bookkeeping: %s", body)
	}
}

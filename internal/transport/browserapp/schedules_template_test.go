package browserapp

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountaccess"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestSchedulesTemplateExposesRoutedLifecycleWorkspace(t *testing.T) {
	pages, err := template.New("pages").Parse(pageTemplates)
	if err != nil {
		t.Fatal(err)
	}
	accountID := ids.AccountID("01J00000000000000000000000")
	data := pageData{Title: "Schedules", Page: "schedules", Selected: &accountaccess.Choice{AccountID: accountID, DisplayName: "Northstar Studio"},
		Choices: []accountaccess.Choice{{AccountID: accountID, DisplayName: "Northstar Studio"}}, AgentsAvailable: true, Script: "/assets/schedules.js"}
	var rendered bytes.Buffer
	if err := pages.ExecuteTemplate(&rendered, "schedules", data); err != nil {
		t.Fatal(err)
	}
	body := rendered.String()
	for _, expected := range []string{
		`src="/assets/schedules.js?v=3"`,
		`class="active" aria-current="page" href="/app/schedules"`,
		`data-account-id="01J00000000000000000000000"`,
		`data-read-only="false"`,
		`id="schedules-list"`,
		`id="schedule-form"`,
		`id="schedule-boardroom"`,
		`id="schedule-personas"`,
		`name="timezone"`,
		`value="catch_up_one"`,
		`value="manager_led"`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("Schedule shell missing %q: %s", expected, body)
		}
	}
}

func TestSchedulesTemplateKeepsMutationsOutOfReadOnlyAccess(t *testing.T) {
	pages, _ := template.New("pages").Parse(pageTemplates)
	accountID := ids.AccountID("01J00000000000000000000000")
	data := pageData{Title: "Schedules", Page: "schedules", Selected: &accountaccess.Choice{AccountID: accountID, DisplayName: "Northstar Studio"}, AgentsAvailable: true, AgentsReadOnly: true, Script: "/assets/schedules.js"}
	var rendered bytes.Buffer
	if err := pages.ExecuteTemplate(&rendered, "schedules", data); err != nil {
		t.Fatal(err)
	}
	body := rendered.String()
	if !strings.Contains(body, `data-read-only="true"`) || !strings.Contains(body, `id="schedules-list"`) || strings.Contains(body, `id="schedule-form"`) {
		t.Fatalf("read-only Schedule mutation surface: %s", body)
	}
}

func TestSchedulesScriptUsesReplaySafeAccountScopedBoundary(t *testing.T) {
	raw, err := assets.ReadFile("assets/schedules.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)
	for _, expected := range []string{
		"/api/v1/accounts/${encodeURIComponent(accountID)}",
		`credentials: "same-origin"`,
		`pendingOperations.set(fingerprint, operationID)`,
		`"Idempotency-Key": operationID`,
		`/schedules`,
		`expected_version`,
		`/triggers`,
		`"DELETE"`,
		`"PUT"`,
		"document.createElement",
		"textContent",
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("Schedule client is missing boundary %q", expected)
		}
	}
	for _, forbidden := range []string{"innerHTML", "localStorage", "sessionStorage", "document.cookie", "cell_id", "schedule_version"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("Schedule client contains forbidden browser behavior or internal field %q", forbidden)
		}
	}
}

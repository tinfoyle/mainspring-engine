package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestExecuteMeasuresWeightedSyntheticActorsWithoutContent(t *testing.T) {
	counts := map[string]int{}
	paths := map[string]int{}
	var lock sync.Mutex
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lock.Lock()
		counts[r.Header.Get("Cookie")]++
		paths[r.URL.Path]++
		lock.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","customer_content":"must not enter evidence"}`))
	}))
	defer server.Close()
	p := validPlan(server.URL)
	p.DurationSeconds = 1 // execution is tested directly; production plan validation requires at least 10.
	p.RequestsPerSecond = 80
	p.Concurrency = 8
	p.Actors = []actor{
		{Name: "hot-account", Weight: 3, CookieEnv: "SPYGLASS_LOAD_COOKIE_HOT", AccountIDEnv: "SPYGLASS_LOAD_ACCOUNT_ID_HOT"},
		{Name: "small-account", Weight: 1, CookieEnv: "SPYGLASS_LOAD_COOKIE_SMALL", AccountIDEnv: "SPYGLASS_LOAD_ACCOUNT_ID_SMALL"},
	}
	p.Routes = []route{{Name: "work-summary", Path: "/api/v1/accounts/{account_id}/work-items/summary", Weight: 1, Status: http.StatusOK, ContentType: "application/json"}}
	hotID := "00000000-0000-4000-8000-000000000001"
	smallID := "00000000-0000-4000-8000-000000000002"
	credentials := []credential{{actor: p.Actors[0], cookie: "session=secret-hot", accountID: hotID}, {actor: p.Actors[1], cookie: "session=secret-small", accountID: smallID}}
	now := func() time.Time { return time.Date(2026, 8, 18, 20, 0, 0, 0, time.UTC) }

	report := execute(context.Background(), p, []byte(`{"reviewed":"plan"}`), credentials, server.Client(), now)
	if !report.Success || len(report.Results) != 2 || report.PlannedRequests != 80 || report.ScheduledRequests != 80 || report.CompletedRequests != 80 {
		t.Fatalf("unexpected report: %+v", report)
	}
	byActor := map[string]result{}
	for _, item := range report.Results {
		byActor[item.Actor] = item
		if !item.Passed || item.Errors != 0 || item.P95MS > p.MaxP95MS {
			t.Fatalf("unexpected result: %+v", item)
		}
	}
	if byActor["hot-account"].Samples <= byActor["small-account"].Samples*2 {
		t.Fatalf("weighted schedule was not represented: %+v", byActor)
	}
	raw, _ := json.Marshal(report)
	text := string(raw)
	for _, forbidden := range []string{"secret-hot", "secret-small", hotID, smallID, "customer_content", server.URL, "{account_id}", "/api/v1/accounts/"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("evidence leaked %q: %s", forbidden, text)
		}
	}
	lock.Lock()
	defer lock.Unlock()
	if counts["session=secret-hot"] == 0 || counts["session=secret-small"] == 0 {
		t.Fatalf("synthetic credentials were not exercised: %+v", counts)
	}
	if paths["/api/v1/accounts/"+hotID+"/work-items/summary"] == 0 || paths["/api/v1/accounts/"+smallID+"/work-items/summary"] == 0 {
		t.Fatalf("actor account identifiers were not substituted: %+v", paths)
	}
}

func TestPlanAndCredentialValidationFailClosed(t *testing.T) {
	p := validPlan("https://app.staging.infiniteocean.net")
	if err := p.validate(); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		edit func(*plan)
	}{
		{name: "http origin", edit: func(value *plan) { value.Origin = "http://app.staging.infiniteocean.net" }},
		{name: "mutation path masquerade", edit: func(value *plan) { value.Routes[0].Path = "https://other.example/api" }},
		{name: "short run", edit: func(value *plan) { value.DurationSeconds = 9 }},
		{name: "uncovered weights", edit: func(value *plan) {
			value.Actors[0].Weight = 100
			value.Routes[0].Weight = 100
			value.RequestsPerSecond = 1
		}},
		{name: "credential name", edit: func(value *plan) { value.Actors[0].CookieEnv = "HOME" }},
		{name: "account identifier name", edit: func(value *plan) { value.Actors[0].AccountIDEnv = "HOME" }},
		{name: "unknown template", edit: func(value *plan) { value.Routes[0].Path = "/api/v1/accounts/{tenant_id}" }},
		{name: "template without actor account", edit: func(value *plan) { value.Routes[0].Path = "/api/v1/accounts/{account_id}" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := p
			candidate.Actors = append([]actor(nil), p.Actors...)
			candidate.Routes = append([]route(nil), p.Routes...)
			test.edit(&candidate)
			if err := candidate.validate(); err == nil {
				t.Fatal("invalid plan was accepted")
			}
		})
	}
	cookieActor := []actor{{Name: "account", Weight: 1, CookieEnv: "SPYGLASS_LOAD_COOKIE_ONE"}}
	if _, err := loadCredentials(cookieActor, func(string) (string, bool) { return "cookie\r\ninjected", true }); err == nil {
		t.Fatal("header injection was accepted")
	}
	accountActor := []actor{{Name: "account", Weight: 1, AccountIDEnv: "SPYGLASS_LOAD_ACCOUNT_ID_ONE"}}
	if _, err := loadCredentials(accountActor, func(string) (string, bool) { return "not-an-id", true }); err == nil {
		t.Fatal("invalid account identifier was accepted")
	}
	duplicateActors := []actor{
		{Name: "one", Weight: 1, AccountIDEnv: "SPYGLASS_LOAD_ACCOUNT_ID_ONE"},
		{Name: "two", Weight: 1, AccountIDEnv: "SPYGLASS_LOAD_ACCOUNT_ID_TWO"},
	}
	if _, err := loadCredentials(duplicateActors, func(string) (string, bool) { return "00000000-0000-4000-8000-000000000001", true }); err == nil {
		t.Fatal("duplicate synthetic account identifiers were accepted")
	}
}

func TestExecuteRejectsPartialRunAndMediaTypePrefixes(t *testing.T) {
	p := validPlan("https://app.staging.infiniteocean.net")
	p.DurationSeconds = 1
	p.RequestsPerSecond = 10
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report := execute(ctx, p, []byte(`{"reviewed":"plan"}`), []credential{{actor: p.Actors[0]}}, http.DefaultClient, time.Now)
	if report.Success || report.PlannedRequests != 10 || report.ScheduledRequests >= report.PlannedRequests || report.CompletedRequests != report.ScheduledRequests {
		t.Fatalf("partial execution was accepted: %+v", report)
	}
	if !matchesMediaType("application/json; charset=utf-8", "application/json") {
		t.Fatal("valid parameterized media type was rejected")
	}
	if matchesMediaType("application/json-malicious", "application/json") {
		t.Fatal("media type prefix was accepted")
	}
}

func TestReadPlanIsStrictAndEvidenceIsImmutable(t *testing.T) {
	p := validPlan("https://app.staging.infiniteocean.net")
	raw, _ := json.Marshal(p)
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readPlan(path); err != nil {
		t.Fatal(err)
	}
	unknown := append(append([]byte(nil), raw[:len(raw)-1]...), []byte(`,"unknown":true}`)...)
	if err := os.WriteFile(path, unknown, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readPlan(path); err == nil {
		t.Fatal("unknown plan field was accepted")
	}
	if err := os.WriteFile(path, append(raw, []byte(` {}`)...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readPlan(path); err == nil {
		t.Fatal("trailing JSON was accepted")
	}

	evidencePath := filepath.Join(t.TempDir(), "load.json")
	if err := writeReport(evidencePath, report{SchemaVersion: 1}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(evidencePath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("evidence permissions = %v, %v", info, err)
	}
	if err := writeReport(evidencePath, report{SchemaVersion: 1}); err == nil {
		t.Fatal("existing evidence was overwritten")
	}
}

func validPlan(origin string) plan {
	return plan{
		SchemaVersion: 1, Environment: "staging", Revision: strings.Repeat("a", 40), ImageDigest: "sha256:" + strings.Repeat("b", 64), Origin: origin,
		WarmupSeconds: 0, DurationSeconds: 10, RequestsPerSecond: 10, Concurrency: 2, MaxP95MS: 500, MaxErrorRate: 0.01,
		Actors: []actor{{Name: "public-actor", Weight: 1}},
		Routes: []route{{Name: "public-catalog", Path: "/api/v1/catalog/public", Weight: 1, Status: http.StatusOK, ContentType: "application/json"}},
	}
}

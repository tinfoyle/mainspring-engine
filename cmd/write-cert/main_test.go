package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestExecuteProvesExactWorkReplayWithoutLeakingSyntheticIdentity(t *testing.T) {
	var lock sync.Mutex
	calls := map[string]int{}
	bodies := map[string]string{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Accept") != "application/json" {
			t.Errorf("unexpected request method=%s headers=%v", r.Method, r.Header)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		operationID := r.Header.Get("Idempotency-Key")
		if operationID == "" || !strings.HasPrefix(r.URL.Path, "/api/v1/accounts/") || !strings.HasSuffix(r.URL.Path, "/work-items") {
			t.Errorf("unexpected request path=%s operation=%q", r.URL.Path, operationID)
			http.Error(w, "invalid identity", http.StatusBadRequest)
			return
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["kind"] != "todo" || body["priority"] != "normal" || body["reason"] != "synthetic write certification" {
			t.Errorf("unexpected body=%v", body)
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		accountID := strings.Split(r.URL.Path, "/")[4]
		raw := fmt.Sprintf(`{"id":%q,"version":1,"title":%q}`, operationID, body["title"])
		lock.Lock()
		if previous, exists := bodies[operationID]; exists && previous != raw {
			lock.Unlock()
			t.Error("operation payload changed")
			http.Error(w, "changed operation", http.StatusConflict)
			return
		}
		bodies[operationID] = raw
		calls[r.Header.Get("Cookie")]++
		lock.Unlock()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("ETag", `W/"1"`)
		w.Header().Set("Location", "/api/v1/accounts/"+accountID+"/work-items/"+operationID)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(raw))
	}))
	defer server.Close()

	p := validPlan(server.URL)
	p.Operations = 40
	p.OperationsPerSecond = 50
	p.Concurrency = 8
	p.Actors = []actor{
		{Name: "hot-account", Weight: 3, CookieEnv: "SPYGLASS_WRITE_COOKIE_HOT", AccountIDEnv: "SPYGLASS_WRITE_ACCOUNT_ID_HOT"},
		{Name: "small-account", Weight: 1, CookieEnv: "SPYGLASS_WRITE_COOKIE_SMALL", AccountIDEnv: "SPYGLASS_WRITE_ACCOUNT_ID_SMALL"},
	}
	hotID, smallID := "11000000-0000-4000-8000-000000000001", "12000000-0000-4000-8000-000000000002"
	credentials := []credential{{actor: p.Actors[0], cookie: "session=secret-hot", accountID: hotID}, {actor: p.Actors[1], cookie: "session=secret-small", accountID: smallID}}
	now := func() time.Time { return time.Date(2026, 8, 18, 20, 0, 0, 0, time.UTC) }

	evidence := execute(context.Background(), p, []byte(`{"reviewed":"write-plan"}`), credentials, server.Client(), now)
	if !evidence.Success || evidence.ScheduledOperations != 40 || evidence.CompletedOperations != 40 || evidence.PlannedRequests != 80 || evidence.CompletedRequests != 80 || len(evidence.Results) != 2 {
		t.Fatalf("unexpected evidence=%+v", evidence)
	}
	byActor := map[string]actorResult{}
	for _, item := range evidence.Results {
		byActor[item.Actor] = item
		if !item.Passed || item.Errors != 0 || item.Requests != item.Operations*2 || item.CreateStatusCodes["201"] != item.Operations || item.ReplayStatusCodes["201"] != item.Operations {
			t.Fatalf("unexpected actor result=%+v", item)
		}
	}
	if byActor["hot-account"].Operations != byActor["small-account"].Operations*3 {
		t.Fatalf("weighted actor schedule=%+v", byActor)
	}
	raw, _ := json.Marshal(evidence)
	text := string(raw)
	for _, forbidden := range []string{"secret-hot", "secret-small", hotID, smallID, p.RunID, server.URL, "/api/v1/accounts/", "Certification", "work-items"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("evidence leaked %q: %s", forbidden, text)
		}
	}
	lock.Lock()
	defer lock.Unlock()
	if calls["session=secret-hot"] != byActor["hot-account"].Requests || calls["session=secret-small"] != byActor["small-account"].Requests || len(bodies) != 40 {
		t.Fatalf("request exercise calls=%v bodies=%d", calls, len(bodies))
	}
}

func TestExecuteRejectsChangedReplayIdentity(t *testing.T) {
	var calls int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		version := calls
		operationID := r.Header.Get("Idempotency-Key")
		accountID := strings.Split(r.URL.Path, "/")[4]
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", fmt.Sprintf(`W/"%d"`, version))
		w.Header().Set("Location", "/api/v1/accounts/"+accountID+"/work-items/"+operationID)
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(w, `{"id":%q,"version":%d}`, operationID, version)
	}))
	defer server.Close()
	p := validPlan(server.URL)
	p.Operations, p.OperationsPerSecond = 1, 50
	evidence := execute(context.Background(), p, []byte(`{}`), []credential{{actor: p.Actors[0], cookie: "session=secret", accountID: "11000000-0000-4000-8000-000000000001"}}, server.Client(), time.Now)
	if evidence.Success || len(evidence.Results) != 1 || evidence.Results[0].ErrorCodes["idempotency_mismatch"] != 1 {
		t.Fatalf("changed replay was accepted: %+v", evidence)
	}
}

func TestExecuteRejectsUnrelatedWorkIdentity(t *testing.T) {
	const unrelatedID = "91000000-0000-4000-8000-000000000009"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accountID := strings.Split(r.URL.Path, "/")[4]
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `W/"1"`)
		w.Header().Set("Location", "/api/v1/accounts/"+accountID+"/work-items/"+unrelatedID)
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(w, `{"id":%q,"version":1}`, unrelatedID)
	}))
	defer server.Close()
	p := validPlan(server.URL)
	p.Operations, p.OperationsPerSecond = 1, 50
	evidence := execute(context.Background(), p, []byte(`{}`), []credential{{actor: p.Actors[0], cookie: "session=secret", accountID: "11000000-0000-4000-8000-000000000001"}}, server.Client(), time.Now)
	if evidence.Success || len(evidence.Results) != 1 || evidence.Results[0].ErrorCodes["create_shape"] != 1 {
		t.Fatalf("unrelated Work identity was accepted: %+v", evidence)
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
		{"http origin", func(value *plan) { value.Origin = "http://app.staging.infiniteocean.net" }},
		{"origin path", func(value *plan) { value.Origin += "/api" }},
		{"invalid run", func(value *plan) { value.RunID = "not-a-uuid" }},
		{"too many operations", func(value *plan) { value.Operations = 501 }},
		{"excess rate", func(value *plan) { value.OperationsPerSecond = 51 }},
		{"excess concurrency", func(value *plan) { value.Concurrency = 51 }},
		{"uncovered weight", func(value *plan) { value.Actors[0].Weight = 11 }},
		{"cookie environment", func(value *plan) { value.Actors[0].CookieEnv = "HOME" }},
		{"account environment", func(value *plan) { value.Actors[0].AccountIDEnv = "HOME" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := p
			candidate.Actors = append([]actor(nil), p.Actors...)
			test.edit(&candidate)
			if candidate.validate() == nil {
				t.Fatal("invalid plan was accepted")
			}
		})
	}
	actors := []actor{{Name: "one", Weight: 1, CookieEnv: "SPYGLASS_WRITE_COOKIE_ONE", AccountIDEnv: "SPYGLASS_WRITE_ACCOUNT_ID_ONE"}}
	if _, err := loadCredentials(actors, func(name string) (string, bool) {
		if strings.Contains(name, "COOKIE") {
			return "cookie\r\ninjected", true
		}
		return "11000000-0000-4000-8000-000000000001", true
	}); err == nil {
		t.Fatal("header injection was accepted")
	}
	duplicate := append(actors, actor{Name: "two", Weight: 1, CookieEnv: "SPYGLASS_WRITE_COOKIE_TWO", AccountIDEnv: "SPYGLASS_WRITE_ACCOUNT_ID_TWO"})
	if _, err := loadCredentials(duplicate, func(name string) (string, bool) {
		if strings.Contains(name, "COOKIE") {
			return "session=valid", true
		}
		return "11000000-0000-4000-8000-000000000001", true
	}); err == nil {
		t.Fatal("duplicate synthetic Account was accepted")
	}
}

func TestReadPlanIsStrictAndEvidenceIsImmutable(t *testing.T) {
	p := validPlan("https://app.staging.infiniteocean.net")
	raw, _ := json.Marshal(p)
	planPath := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(planPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readPlan(planPath); err != nil {
		t.Fatal(err)
	}
	unknown := append(append([]byte(nil), raw[:len(raw)-1]...), []byte(`,"unknown":true}`)...)
	if err := os.WriteFile(planPath, unknown, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readPlan(planPath); err == nil {
		t.Fatal("unknown plan field was accepted")
	}
	if err := os.WriteFile(planPath, append(raw, []byte(` {}`)...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readPlan(planPath); err == nil {
		t.Fatal("trailing JSON was accepted")
	}
	evidencePath := filepath.Join(t.TempDir(), "write-evidence.json")
	if err := writeReport(evidencePath, report{SchemaVersion: 1}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(evidencePath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("evidence permissions=%v err=%v", info, err)
	}
	if err := writeReport(evidencePath, report{SchemaVersion: 1}); err == nil {
		t.Fatal("existing evidence was overwritten")
	}
}

func validPlan(origin string) plan {
	return plan{SchemaVersion: 1, Environment: "staging", Revision: strings.Repeat("a", 40), ImageDigest: "sha256:" + strings.Repeat("b", 64), Origin: origin,
		RunID: "31000000-0000-4000-8000-000000000001", Operations: 10, OperationsPerSecond: 10, Concurrency: 2, RequestTimeoutSeconds: 5,
		MaxCreateP95MS: 1000, MaxReplayP95MS: 1000, MaxErrorRate: 0.01,
		Actors: []actor{{Name: "synthetic-account", Weight: 1, CookieEnv: "SPYGLASS_WRITE_COOKIE_ONE", AccountIDEnv: "SPYGLASS_WRITE_ACCOUNT_ID_ONE"}}}
}

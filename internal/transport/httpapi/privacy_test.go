package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/privacytoken"
	"github.com/tinfoyle/spyglass-engine/internal/application/analyticsingest"
	"github.com/tinfoyle/spyglass-engine/internal/application/privacyconsent"
	"github.com/tinfoyle/spyglass-engine/internal/modules/analytics"
	"github.com/tinfoyle/spyglass-engine/internal/modules/privacy"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/transport/httpapi"
)

type privacyMemory struct {
	mu        sync.Mutex
	decisions []privacy.Decision
	events    []analyticsingest.AcceptedEvent
}

func (m *privacyMemory) Append(_ context.Context, decision privacy.Decision) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.decisions = append(m.decisions, decision)
	return nil
}

func (m *privacyMemory) Current(_ context.Context, subjectID ids.ConsentSubjectID, surface privacy.Surface) (privacy.Decision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for index := len(m.decisions) - 1; index >= 0; index-- {
		decision := m.decisions[index]
		if decision.SubjectID == subjectID && decision.Surface == surface {
			return decision, nil
		}
	}
	return privacy.Decision{}, privacyconsent.ErrNotFound
}

func (m *privacyMemory) History(_ context.Context, subjectID ids.ConsentSubjectID, limit int) ([]privacy.Decision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]privacy.Decision, 0)
	for index := len(m.decisions) - 1; index >= 0 && len(result) < limit; index-- {
		if m.decisions[index].SubjectID == subjectID {
			result = append(result, m.decisions[index])
		}
	}
	return result, nil
}

func (m *privacyMemory) Erase(_ context.Context, subjectID ids.ConsentSubjectID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	decisions := m.decisions[:0]
	for _, decision := range m.decisions {
		if decision.SubjectID != subjectID {
			decisions = append(decisions, decision)
		}
	}
	m.decisions = decisions
	events := m.events[:0]
	for _, event := range m.events {
		if event.Envelope.SubjectID != subjectID {
			events = append(events, event)
		}
	}
	m.events = events
	return nil
}

func (m *privacyMemory) AppendEvent(_ context.Context, event analyticsingest.AcceptedEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
	return nil
}

type privacySink struct{ memory *privacyMemory }

func (s privacySink) Append(ctx context.Context, event analyticsingest.AcceptedEvent) error {
	return s.memory.AppendEvent(ctx, event)
}

type sequenceIDs struct {
	mu     sync.Mutex
	values []string
}

func (g *sequenceIDs) New() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	value := g.values[0]
	g.values = g.values[1:]
	return value
}

type privacyClock struct{ now time.Time }

func (c privacyClock) Now() time.Time { return c.now }

func TestPrivacyConsentGatesAnalyticsAndWithdrawalStopsIngestion(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	memory := &privacyMemory{}
	generator := &sequenceIDs{values: []string{
		"10000000-0000-4000-8000-000000000001", "10000000-0000-4000-8000-000000000002",
		"10000000-0000-4000-8000-000000000003", "10000000-0000-4000-8000-000000000004",
	}}
	consent, _ := privacyconsent.New(memory, generator, privacyClock{now}, 1)
	ingestion, _ := analyticsingest.New(memory, privacySink{memory}, analytics.LaunchRegistry(), privacyClock{now}, 1)
	tokens, _ := privacytoken.New([]byte("0123456789abcdef0123456789abcdef"))
	handler := httpapi.NewServer(nil, nil, nil, false, slog.Default(), httpapi.WithPrivacy(consent, ingestion, tokens,
		httpapi.PrivacyHTTPConfig{PublicOrigin: "https://web.example.test", AppOrigin: "https://app.example.test", Secure: true})).Handler()

	cookie := putConsent(t, handler, nil, false, false)
	if status := postAnalytics(t, handler, cookie, "10000000-0000-4000-8000-000000000010", now); status != http.StatusForbidden {
		t.Fatalf("analytics without consent status=%d", status)
	}
	cookie = putConsent(t, handler, cookie, true, false)
	if status := postAnalytics(t, handler, cookie, "10000000-0000-4000-8000-000000000011", now); status != http.StatusNoContent {
		t.Fatalf("analytics with consent status=%d", status)
	}
	if len(memory.events) != 1 || memory.events[0].ConsentDecisionID != memory.decisions[1].ID {
		t.Fatalf("events=%+v decisions=%+v", memory.events, memory.decisions)
	}
	cookie = putConsent(t, handler, cookie, false, false)
	if status := postAnalytics(t, handler, cookie, "10000000-0000-4000-8000-000000000012", now); status != http.StatusForbidden || len(memory.events) != 1 {
		t.Fatalf("analytics after withdrawal status=%d events=%d", status, len(memory.events))
	}
	history := httptest.NewRequest(http.MethodGet, "https://web.example.test/api/v1/privacy/consent/history", nil)
	history.AddCookie(cookie)
	historyResponse := httptest.NewRecorder()
	handler.ServeHTTP(historyResponse, history)
	if historyResponse.Code != http.StatusOK || !bytes.Contains(historyResponse.Body.Bytes(), []byte(`"decisions"`)) || len(memory.decisions) != 3 {
		t.Fatalf("history status=%d body=%s decisions=%d", historyResponse.Code, historyResponse.Body.String(), len(memory.decisions))
	}
	erase := httptest.NewRequest(http.MethodDelete, "https://web.example.test/api/v1/privacy/data", nil)
	erase.Header.Set("Origin", "https://web.example.test")
	erase.AddCookie(cookie)
	eraseResponse := httptest.NewRecorder()
	handler.ServeHTTP(eraseResponse, erase)
	if eraseResponse.Code != http.StatusNoContent || len(memory.decisions) != 0 || len(memory.events) != 0 || len(eraseResponse.Result().Cookies()) != 1 || eraseResponse.Result().Cookies()[0].MaxAge != -1 {
		t.Fatalf("erase status=%d decisions=%d events=%d cookies=%+v", eraseResponse.Code, len(memory.decisions), len(memory.events), eraseResponse.Result().Cookies())
	}
}

func putConsent(t *testing.T, handler http.Handler, cookie *http.Cookie, analyticsAllowed, marketingAllowed bool) *http.Cookie {
	t.Helper()
	payload, _ := json.Marshal(map[string]bool{"analytics": analyticsAllowed, "marketing": marketingAllowed})
	request := httptest.NewRequest(http.MethodPut, "https://web.example.test/api/v1/privacy/consent", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://web.example.test")
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || len(response.Result().Cookies()) != 1 {
		t.Fatalf("put consent status=%d body=%s cookies=%d", response.Code, response.Body.String(), len(response.Result().Cookies()))
	}
	return response.Result().Cookies()[0]
}

func postAnalytics(t *testing.T, handler http.Handler, cookie *http.Cookie, eventID string, now time.Time) int {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{"event_id": eventID, "name": "landing_viewed", "occurred_at": now, "fields": map[string]string{"device_class": "phone"}})
	request := httptest.NewRequest(http.MethodPost, "https://web.example.test/api/v1/analytics/events", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://web.example.test")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response.Code
}

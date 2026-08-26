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

	"github.com/tinfoyle/spyglass-engine/internal/adapters/conversiontoken"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/memory"
	"github.com/tinfoyle/spyglass-engine/internal/adapters/privacytoken"
	"github.com/tinfoyle/spyglass-engine/internal/application/analyticsconversion"
	"github.com/tinfoyle/spyglass-engine/internal/application/analyticsingest"
	"github.com/tinfoyle/spyglass-engine/internal/application/privacyconsent"
	"github.com/tinfoyle/spyglass-engine/internal/modules/analytics"
	"github.com/tinfoyle/spyglass-engine/internal/modules/privacy"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/transport/httpapi"
)

type privacyMemory struct {
	mu        sync.Mutex
	decisions []privacy.Decision
	events    []analyticsingest.AcceptedEvent
	owners    map[ids.ConsentSubjectID]ids.UserID
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

func (m *privacyMemory) Link(_ context.Context, subjectID ids.ConsentSubjectID, userID ids.UserID, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.owners == nil {
		m.owners = make(map[ids.ConsentSubjectID]ids.UserID)
	}
	if owner := m.owners[subjectID]; owner != "" && owner != userID {
		return privacyconsent.ErrSubjectOwned
	}
	m.owners[subjectID] = userID
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

type conversionMemory struct {
	handoff analytics.HandoffReference
	event   analytics.Envelope
}

func (m *conversionMemory) Append(_ context.Context, handoff analytics.HandoffReference, event analytics.Envelope) error {
	m.handoff, m.event = handoff, event
	return nil
}

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

func TestPrivateConsentHistoryReplacesSubjectOwnedByDifferentUser(t *testing.T) {
	now := time.Date(2026, 8, 25, 18, 0, 0, 0, time.UTC)
	memoryRepository := &privacyMemory{}
	privacyIDs := &sequenceIDs{values: []string{
		"10000000-0000-4000-8000-000000000061", "10000000-0000-4000-8000-000000000062",
		"10000000-0000-4000-8000-000000000063", "10000000-0000-4000-8000-000000000064",
	}}
	consent, _ := privacyconsent.New(memoryRepository, privacyIDs, privacyClock{now}, 1)
	ingestion, _ := analyticsingest.New(memoryRepository, privacySink{memoryRepository}, analytics.LaunchRegistry(), privacyClock{now}, 1)
	first, err := consent.Set(context.Background(), privacyconsent.SetCommand{Surface: privacy.SurfacePrivate, Analytics: true})
	if err != nil {
		t.Fatal(err)
	}
	firstUser := ids.UserID("20000000-0000-4000-8000-000000000061")
	secondUser := ids.UserID("20000000-0000-4000-8000-000000000062")
	if err := consent.Link(context.Background(), first.SubjectID, firstUser); err != nil {
		t.Fatal(err)
	}
	tokens, _ := privacytoken.New([]byte("0123456789abcdef0123456789abcdef"))
	privacyReference, err := tokens.Sign(privacy.PreferenceReference{SubjectID: first.SubjectID, PolicyVersion: first.PolicyVersion, Surface: first.Surface})
	if err != nil {
		t.Fatal(err)
	}
	sessionStore := memory.NewSessionStore()
	sessionIDs := &sequenceIDs{values: []string{"30000000-0000-4000-8000-000000000061"}}
	sessionService, _ := sessions.NewService(sessionStore, sessionIDs, privacyClock{now}, 24*time.Hour, 2*time.Hour, time.Hour)
	issued, err := sessionService.Issue(context.Background(), secondUser, 1)
	if err != nil {
		t.Fatal(err)
	}
	handler := httpapi.NewServer(nil, nil, nil, false, slog.Default(),
		httpapi.WithAuthentication(nil, sessionService, httpapi.SessionCookie{Name: "spyglass_test_session", Secure: true}),
		httpapi.WithPrivacy(consent, ingestion, tokens, httpapi.PrivacyHTTPConfig{
			PublicOrigin: "https://web.example.test", AppOrigin: "https://app.example.test", CookieName: "spyglass_test_privacy", Secure: true,
		})).Handler()

	request := httptest.NewRequest(http.MethodGet, "https://app.example.test/api/v1/privacy/consent/history", nil)
	request.AddCookie(&http.Cookie{Name: "spyglass_test_privacy", Value: privacyReference})
	request.AddCookie(&http.Cookie{Name: "spyglass_test_session", Value: issued.Token})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var history struct {
		Decisions []privacy.Decision `json:"decisions"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &history); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || len(history.Decisions) != 1 {
		t.Fatalf("history status=%d body=%s", response.Code, response.Body.String())
	}
	if history.Decisions[0].SubjectID == first.SubjectID || !history.Decisions[0].Analytics || history.Decisions[0].Marketing {
		t.Fatalf("history exposed or changed the prior subject: %+v", history.Decisions)
	}
	if owner := memoryRepository.owners[history.Decisions[0].SubjectID]; owner != secondUser {
		t.Fatalf("replacement owner=%s want=%s", owner, secondUser)
	}
	if cookies := response.Result().Cookies(); len(cookies) != 1 || cookies[0].Name != "spyglass_test_privacy" {
		t.Fatalf("replacement cookies=%+v", cookies)
	}
}

func TestAnalyticsHandoffMirrorsPrivateMilestoneWithoutSharingPrivacySubject(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	memory := &privacyMemory{}
	generator := &sequenceIDs{values: []string{
		"10000000-0000-4000-8000-000000000021", "10000000-0000-4000-8000-000000000022",
		"10000000-0000-4000-8000-000000000023", "10000000-0000-4000-8000-000000000024",
		"10000000-0000-4000-8000-000000000025",
	}}
	clock := privacyClock{now}
	consent, _ := privacyconsent.New(memory, generator, clock, 1)
	ingestion, _ := analyticsingest.New(memory, privacySink{memory}, analytics.LaunchRegistry(), clock, 1)
	privacyTokens, _ := privacytoken.New([]byte("0123456789abcdef0123456789abcdef"))
	conversionTokens, _ := conversiontoken.New([]byte("0123456789abcdef0123456789abcdef"))
	conversionRepository := &conversionMemory{}
	conversion, _ := analyticsconversion.New(conversionRepository, clock)
	handler := httpapi.NewServer(nil, nil, nil, false, slog.Default(),
		httpapi.WithPrivacy(consent, ingestion, privacyTokens, httpapi.PrivacyHTTPConfig{
			PublicOrigin: "https://web.example.test", AppOrigin: "https://app.example.test", Secure: true,
		}),
		httpapi.WithAnalyticsConversion(conversion, conversionTokens, httpapi.AnalyticsConversionHTTPConfig{
			CookieDomain: "example.test", Secure: true, Lifetime: time.Hour,
		})).Handler()

	publicPrivacy := putConsentForOrigin(t, handler, "https://web.example.test", nil, true, false)
	handoffID := "10000000-0000-4000-8000-000000000031"
	handoffPayload, _ := json.Marshal(map[string]any{"event_id": handoffID, "name": "signup_handoff_started", "occurred_at": now, "fields": map[string]string{"offer_code": "team-monthly-v1"}})
	handoffRequest := httptest.NewRequest(http.MethodPost, "https://web.example.test/api/v1/analytics/events", bytes.NewReader(handoffPayload))
	handoffRequest.Header.Set("Content-Type", "application/json")
	handoffRequest.Header.Set("Origin", "https://web.example.test")
	handoffRequest.AddCookie(publicPrivacy)
	handoffResponse := httptest.NewRecorder()
	handler.ServeHTTP(handoffResponse, handoffRequest)
	if handoffResponse.Code != http.StatusNoContent || len(handoffResponse.Result().Cookies()) != 1 {
		t.Fatalf("handoff status=%d cookies=%+v", handoffResponse.Code, handoffResponse.Result().Cookies())
	}
	handoffCookie := handoffResponse.Result().Cookies()[0]
	if handoffCookie.Name != "__Secure-spyglass_analytics_handoff" || handoffCookie.Domain != "example.test" || handoffCookie.Path != "/api/v1" || !handoffCookie.HttpOnly || !handoffCookie.Secure {
		t.Fatalf("handoff cookie=%+v", handoffCookie)
	}

	privatePrivacy := putConsentForOrigin(t, handler, "https://app.example.test", nil, true, false)
	privateEventID := "10000000-0000-4000-8000-000000000032"
	privatePayload, _ := json.Marshal(map[string]any{"event_id": privateEventID, "name": "registration_started", "occurred_at": now, "fields": map[string]string{"offer_code": "team-monthly-v1"}})
	privateRequest := httptest.NewRequest(http.MethodPost, "https://app.example.test/api/v1/analytics/events", bytes.NewReader(privatePayload))
	privateRequest.Header.Set("Content-Type", "application/json")
	privateRequest.Header.Set("Origin", "https://app.example.test")
	privateRequest.AddCookie(privatePrivacy)
	privateRequest.AddCookie(handoffCookie)
	privateResponse := httptest.NewRecorder()
	handler.ServeHTTP(privateResponse, privateRequest)
	if privateResponse.Code != http.StatusNoContent || conversionRepository.handoff.ReceiptEventID != ids.AnalyticsEventID(handoffID) || conversionRepository.handoff.SubjectID != memory.decisions[0].SubjectID {
		t.Fatalf("private status=%d handoff=%+v", privateResponse.Code, conversionRepository.handoff)
	}
	if conversionRepository.event.ID != ids.AnalyticsEventID(privateEventID) || conversionRepository.event.SubjectID != memory.decisions[1].SubjectID || conversionRepository.event.SubjectID == conversionRepository.handoff.SubjectID {
		t.Fatalf("private event=%+v decisions=%+v", conversionRepository.event, memory.decisions)
	}
	withdrawPayload, _ := json.Marshal(map[string]bool{"analytics": false, "marketing": false})
	withdraw := httptest.NewRequest(http.MethodPut, "https://web.example.test/api/v1/privacy/consent", bytes.NewReader(withdrawPayload))
	withdraw.Header.Set("Content-Type", "application/json")
	withdraw.Header.Set("Origin", "https://web.example.test")
	withdraw.AddCookie(publicPrivacy)
	withdraw.AddCookie(handoffCookie)
	withdrawResponse := httptest.NewRecorder()
	handler.ServeHTTP(withdrawResponse, withdraw)
	withdrawCookies := withdrawResponse.Result().Cookies()
	if withdrawResponse.Code != http.StatusOK || len(withdrawCookies) != 2 || withdrawCookies[1].Name != "__Secure-spyglass_analytics_handoff" || withdrawCookies[1].MaxAge != -1 {
		t.Fatalf("withdraw status=%d cookies=%+v", withdrawResponse.Code, withdrawCookies)
	}

	erase := httptest.NewRequest(http.MethodDelete, "https://app.example.test/api/v1/privacy/data", nil)
	erase.Header.Set("Origin", "https://app.example.test")
	erase.AddCookie(privatePrivacy)
	erase.AddCookie(handoffCookie)
	eraseResponse := httptest.NewRecorder()
	handler.ServeHTTP(eraseResponse, erase)
	if eraseResponse.Code != http.StatusNoContent || len(memory.decisions) != 0 || len(eraseResponse.Result().Cookies()) != 2 {
		t.Fatalf("erase status=%d decisions=%d cookies=%+v", eraseResponse.Code, len(memory.decisions), eraseResponse.Result().Cookies())
	}
}

func putConsent(t *testing.T, handler http.Handler, cookie *http.Cookie, analyticsAllowed, marketingAllowed bool) *http.Cookie {
	return putConsentForOrigin(t, handler, "https://web.example.test", cookie, analyticsAllowed, marketingAllowed)
}

func putConsentForOrigin(t *testing.T, handler http.Handler, origin string, cookie *http.Cookie, analyticsAllowed, marketingAllowed bool) *http.Cookie {
	t.Helper()
	payload, _ := json.Marshal(map[string]bool{"analytics": analyticsAllowed, "marketing": marketingAllowed})
	request := httptest.NewRequest(http.MethodPut, origin+"/api/v1/privacy/consent", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", origin)
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

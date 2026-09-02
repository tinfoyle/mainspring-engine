// Command stripe-fixture provides a deterministic, local-only subset of the
// Stripe API and emits signed webhook events for the commercial journey
// certificate. It is intentionally not linked into the production binary.
package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type config struct {
	address, secretKey, webhookSecret, webhookURL, hostedOrigin, completionToken string
}

type checkout struct {
	ID, CustomerID, AccountID, OfferCode, OfferVersion, PriceID string
	ExpiresAt                                                   int64
}

type subscription struct {
	ID, CustomerID, AccountID, OfferCode, OfferVersion, PriceID string
	PeriodStart, PeriodEnd                                      int64
}

type server struct {
	config        config
	client        *http.Client
	mu            sync.RWMutex
	customers     map[string]string
	checkouts     map[string]checkout
	subscriptions map[string]subscription
	idempotency   map[string]string
}

func main() {
	c := config{
		address:         envOr("STRIPE_FIXTURE_ADDRESS", ":8080"),
		secretKey:       required("STRIPE_FIXTURE_SECRET_KEY"),
		webhookSecret:   required("STRIPE_FIXTURE_WEBHOOK_SECRET"),
		webhookURL:      required("STRIPE_FIXTURE_WEBHOOK_URL"),
		hostedOrigin:    required("STRIPE_FIXTURE_HOSTED_ORIGIN"),
		completionToken: required("STRIPE_FIXTURE_COMPLETION_TOKEN"),
	}
	if err := validateConfig(c); err != nil {
		log.Fatal(err)
	}
	s := newServer(c, &http.Client{Timeout: 5 * time.Second})
	log.Printf("local Stripe fixture listening on %s", c.address)
	log.Fatal(http.ListenAndServe(c.address, s.handler()))
}

func newServer(c config, client *http.Client) *server {
	return &server{config: c, client: client, customers: map[string]string{}, checkouts: map[string]checkout{}, subscriptions: map[string]subscription{}, idempotency: map[string]string{}}
}

func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("POST /v1/customers", s.createCustomer)
	mux.HandleFunc("POST /v1/checkout/sessions", s.createCheckout)
	mux.HandleFunc("GET /v1/subscriptions/{subscriptionID}", s.retrieveSubscription)
	mux.HandleFunc("GET /checkout/{sessionID}", s.hostedCheckout)
	mux.HandleFunc("POST /test/checkout/{sessionID}/complete", s.completeCheckout)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}

func (s *server) authenticateProvider(w http.ResponseWriter, r *http.Request) bool {
	username, password, ok := r.BasicAuth()
	if !ok || username != s.config.secretKey || password != "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]string{"type": "authentication_error", "code": "invalid_api_key"}})
		return false
	}
	return true
}

func (s *server) createCustomer(w http.ResponseWriter, r *http.Request) {
	if !s.authenticateProvider(w, r) || !parseForm(w, r) {
		return
	}
	accountID := r.Form.Get("metadata[spyglass_account_id]")
	if accountID == "" || r.Form.Get("email") == "" || r.Form.Get("name") == "" {
		writeProviderError(w, http.StatusBadRequest, "invalid_request_error", "invalid_customer")
		return
	}
	id := "cus_fixture_" + shortHash(accountID)
	s.mu.Lock()
	s.customers[id] = accountID
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "object": "customer", "email": r.Form.Get("email"), "name": r.Form.Get("name"), "metadata": map[string]string{"spyglass_account_id": accountID}})
}

func (s *server) createCheckout(w http.ResponseWriter, r *http.Request) {
	if !s.authenticateProvider(w, r) || !parseForm(w, r) {
		return
	}
	if r.Form.Get("mode") != "subscription" {
		writeProviderError(w, http.StatusBadRequest, "invalid_request_error", "unsupported_mode")
		return
	}
	accountID := r.Form.Get("metadata[spyglass_account_id]")
	customerID := r.Form.Get("customer")
	requestID := r.Form.Get("metadata[spyglass_checkout_request_id]")
	entry := checkout{CustomerID: customerID, AccountID: accountID, OfferCode: r.Form.Get("metadata[spyglass_offer_code]"), OfferVersion: r.Form.Get("metadata[spyglass_offer_version]"), PriceID: r.Form.Get("line_items[0][price]"), ExpiresAt: time.Now().Add(30 * time.Minute).Unix()}
	if accountID == "" || customerID == "" || requestID == "" || entry.OfferCode == "" || entry.OfferVersion == "" || entry.PriceID == "" {
		writeProviderError(w, http.StatusBadRequest, "invalid_request_error", "invalid_checkout")
		return
	}
	s.mu.Lock()
	if s.customers[customerID] != accountID {
		s.mu.Unlock()
		writeProviderError(w, http.StatusBadRequest, "invalid_request_error", "customer_mismatch")
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if existing := s.idempotency[key]; existing != "" {
		entry = s.checkouts[existing]
	} else {
		entry.ID = "cs_test_fixture_" + shortHash(accountID+":"+requestID)
		s.checkouts[entry.ID] = entry
		if key != "" {
			s.idempotency[key] = entry.ID
		}
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"id": entry.ID, "object": "checkout.session", "url": strings.TrimRight(s.config.hostedOrigin, "/") + "/checkout/" + entry.ID, "expires_at": entry.ExpiresAt})
}

func (s *server) retrieveSubscription(w http.ResponseWriter, r *http.Request) {
	if !s.authenticateProvider(w, r) {
		return
	}
	s.mu.RLock()
	entry, ok := s.subscriptions[r.PathValue("subscriptionID")]
	s.mu.RUnlock()
	if !ok {
		writeProviderError(w, http.StatusNotFound, "invalid_request_error", "resource_missing")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": entry.ID, "object": "subscription", "customer": entry.CustomerID, "status": "active", "created": entry.PeriodStart,
		"metadata": map[string]string{"spyglass_account_id": entry.AccountID, "spyglass_offer_code": entry.OfferCode, "spyglass_offer_version": entry.OfferVersion},
		"items":    map[string]any{"data": []any{map[string]any{"current_period_start": entry.PeriodStart, "current_period_end": entry.PeriodEnd, "price": map[string]string{"id": entry.PriceID}}}},
	})
}

func (s *server) hostedCheckout(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	entry, ok := s.checkouts[r.PathValue("sessionID")]
	s.mu.RUnlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, "<!doctype html><html><head><title>Local Stripe Checkout</title></head><body><main><h1>Test checkout</h1><p>$50.00 USD per month for %s.</p><p>This deterministic page never contacts Stripe or charges a card.</p></main></body></html>", html.EscapeString(entry.OfferCode))
}

func (s *server) completeCheckout(w http.ResponseWriter, r *http.Request) {
	if !hmac.Equal([]byte(r.Header.Get("X-Stripe-Fixture-Token")), []byte(s.config.completionToken)) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "fixture completion denied"})
		return
	}
	s.mu.Lock()
	entry, ok := s.checkouts[r.PathValue("sessionID")]
	if !ok {
		s.mu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "checkout session not found"})
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	subscriptionID := "sub_fixture_" + shortHash(entry.ID)
	invoiceID := "in_fixture_" + shortHash(entry.ID)
	subscriptionEntry := subscription{ID: subscriptionID, CustomerID: entry.CustomerID, AccountID: entry.AccountID, OfferCode: entry.OfferCode, OfferVersion: entry.OfferVersion, PriceID: entry.PriceID, PeriodStart: now.Unix(), PeriodEnd: now.AddDate(0, 1, 0).Unix()}
	s.subscriptions[subscriptionID] = subscriptionEntry
	s.mu.Unlock()

	metadata := map[string]string{"spyglass_account_id": entry.AccountID, "spyglass_offer_code": entry.OfferCode, "spyglass_offer_version": entry.OfferVersion}
	parent := map[string]any{"subscription_details": map[string]any{"subscription": subscriptionID, "metadata": metadata}}
	invoice := map[string]any{"id": invoiceID, "object": "invoice", "customer": entry.CustomerID, "subscription": subscriptionID, "amount_due": 5000, "amount_paid": 5000, "total": 5000, "currency": "usd", "status": "paid", "billing_reason": "subscription_create", "parent": parent, "metadata": metadata, "lines": map[string]any{"data": []any{map[string]any{"id": "il_fixture_" + shortHash(entry.ID), "amount": 5000, "currency": "usd", "pricing": map[string]any{"price_details": map[string]string{"price": entry.PriceID}}}}}}
	events := []struct {
		id, kind string
		object   map[string]any
	}{
		{"evt_fixture_paid_" + shortHash(entry.ID), "invoice.paid", invoice},
		{"evt_fixture_finalized_" + shortHash(entry.ID), "invoice.finalized", invoice},
		{"evt_fixture_created_" + shortHash(entry.ID), "invoice.created", invoice},
		{"evt_fixture_subscription_" + shortHash(entry.ID), "customer.subscription.created", map[string]any{"id": subscriptionID, "object": "subscription", "customer": entry.CustomerID, "status": "active", "metadata": metadata}},
		{"evt_fixture_checkout_" + shortHash(entry.ID), "checkout.session.completed", map[string]any{"id": entry.ID, "object": "checkout.session", "client_reference_id": entry.AccountID, "customer": entry.CustomerID, "subscription": subscriptionID, "mode": "subscription", "payment_status": "paid", "status": "complete", "metadata": metadata}},
	}
	for index, event := range events {
		if err := s.deliverWebhook(r.Context(), event.id, event.kind, now.Add(time.Duration(index)*time.Second), event.object); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
	}
	// Prove provider-event idempotency with one exact duplicate delivery.
	if err := s.deliverWebhook(r.Context(), events[0].id, events[0].kind, now, events[0].object); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session_id": entry.ID, "subscription_id": subscriptionID, "invoice_id": invoiceID, "events_delivered": len(events) + 1})
}

func (s *server) deliverWebhook(ctx context.Context, id, kind string, created time.Time, object map[string]any) error {
	payload, err := json.Marshal(map[string]any{"id": id, "object": "event", "type": kind, "created": created.Unix(), "livemode": false, "data": map[string]any{"object": object}})
	if err != nil {
		return err
	}
	timestamp := time.Now().UTC().Unix()
	mac := hmac.New(sha256.New, []byte(s.config.webhookSecret))
	_, _ = mac.Write(append([]byte(strconv.FormatInt(timestamp, 10)+"."), payload...))
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.config.webhookURL, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Stripe-Signature", fmt.Sprintf("t=%d,v1=%s", timestamp, hex.EncodeToString(mac.Sum(nil))))
	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("webhook %s returned %d: %s", kind, response.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func validateConfig(c config) error {
	if !strings.HasPrefix(c.secretKey, "sk_test_") || !strings.HasPrefix(c.webhookSecret, "whsec_") || len(c.completionToken) < 24 {
		return errors.New("local Stripe fixture credentials are invalid")
	}
	for name, raw := range map[string]string{"webhook URL": c.webhookURL, "hosted origin": c.hostedOrigin} {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("%s is invalid", name)
		}
	}
	return nil
}

func parseForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		writeProviderError(w, http.StatusBadRequest, "invalid_request_error", "invalid_form")
		return false
	}
	return true
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}

func writeProviderError(w http.ResponseWriter, status int, kind, code string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"type": kind, "code": code}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func required(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		log.Fatalf("%s is required", name)
	}
	return value
}

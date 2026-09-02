package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func TestCheckoutCompletionEmitsCurrentSignedIdempotentJourney(t *testing.T) {
	const secretKey = "sk_test_fixture"
	const webhookSecret = "whsec_fixture"
	const completionToken = "fixture-completion-token-123456"
	var mu sync.Mutex
	var deliveries []map[string]any
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		parts := strings.Split(r.Header.Get("Stripe-Signature"), ",")
		if len(parts) != 2 || !strings.HasPrefix(parts[0], "t=") || !strings.HasPrefix(parts[1], "v1=") {
			t.Fatal("fixture omitted Stripe signature")
		}
		mac := hmac.New(sha256.New, []byte(webhookSecret))
		_, _ = mac.Write(append([]byte(strings.TrimPrefix(parts[0], "t=")+"."), raw...))
		if !hmac.Equal([]byte(strings.TrimPrefix(parts[1], "v1=")), []byte(hex.EncodeToString(mac.Sum(nil)))) {
			t.Fatal("fixture signature did not cover the exact payload")
		}
		var event map[string]any
		if err := json.Unmarshal(raw, &event); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		deliveries = append(deliveries, event)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer webhook.Close()

	fixture := httptest.NewServer(newServer(config{secretKey: secretKey, webhookSecret: webhookSecret, webhookURL: webhook.URL, hostedOrigin: "https://stripe.infiniteocean.localhost:8444", completionToken: completionToken}, webhook.Client()).handler())
	defer fixture.Close()

	accountID := "11111111-1111-4111-8111-111111111111"
	customer := providerForm(t, fixture.Client(), secretKey, fixture.URL+"/v1/customers", url.Values{
		"email": {"owner@example.test"}, "name": {"Wrench Works"}, "metadata[spyglass_account_id]": {accountID},
	})
	var customerResult struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(customer, &customerResult) != nil || customerResult.ID == "" {
		t.Fatal("customer fixture response omitted ID")
	}
	checkoutBody := providerForm(t, fixture.Client(), secretKey, fixture.URL+"/v1/checkout/sessions", url.Values{
		"mode": {"subscription"}, "customer": {customerResult.ID}, "metadata[spyglass_account_id]": {accountID},
		"metadata[spyglass_checkout_request_id]": {"22222222-2222-4222-8222-222222222222"},
		"metadata[spyglass_offer_code]":          {"team-monthly-v2"}, "metadata[spyglass_offer_version]": {"2"},
		"line_items[0][price]": {"price_team_monthly_test"},
	})
	var checkoutResult struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(checkoutBody, &checkoutResult) != nil || checkoutResult.ID == "" {
		t.Fatal("checkout fixture response omitted ID")
	}
	request, _ := http.NewRequest(http.MethodPost, fixture.URL+"/test/checkout/"+checkoutResult.ID+"/complete", nil)
	request.Header.Set("X-Stripe-Fixture-Token", completionToken)
	response, err := fixture.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	completion, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(completion), `"events_delivered":6`) {
		t.Fatalf("completion status=%d body=%s", response.StatusCode, completion)
	}
	if len(deliveries) != 6 || deliveries[0]["id"] != deliveries[5]["id"] {
		t.Fatalf("expected five events plus an exact duplicate, got %d", len(deliveries))
	}
	object := deliveries[0]["data"].(map[string]any)["object"].(map[string]any)
	if object["status"] != "paid" {
		t.Fatal("invoice.paid fixture must use current status=paid shape")
	}
	if _, legacy := object["paid"]; legacy {
		t.Fatal("invoice.paid fixture must not restore Stripe's deprecated paid boolean")
	}
}

func providerForm(t *testing.T, client *http.Client, secretKey, target string, values url.Values) []byte {
	t.Helper()
	request, _ := http.NewRequest(http.MethodPost, target, strings.NewReader(values.Encode()))
	request.SetBasicAuth(secretKey, "")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Idempotency-Key", "33333333-3333-4333-8333-333333333333")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("provider request status=%d body=%s", response.StatusCode, body)
	}
	return body
}

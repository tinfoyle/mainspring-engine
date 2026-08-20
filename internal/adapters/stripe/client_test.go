package stripe

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestCheckoutPinsVersionIdempotencyAndMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Stripe-Version") != DefaultAPIVersion || r.Header.Get("Idempotency-Key") != "checkout-key" {
			t.Errorf("missing safety headers: %v", r.Header)
		}
		body, _ := io.ReadAll(r.Body)
		form := string(body)
		for _, expected := range []string{"line_items%5B0%5D%5Bprice%5D=price_private", "metadata%5Bspyglass_account_id%5D=11111111-1111-4111-8111-111111111111", "subscription_data%5Bmetadata%5D%5Bspyglass_offer_code%5D=team"} {
			if !strings.Contains(form, expected) {
				t.Errorf("form missing %q: %s", expected, form)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"cs_test_1","url":"https://checkout.stripe.com/c/pay/test","expires_at":1786971600}`))
	}))
	defer server.Close()
	client, _ := New("sk_test_not_a_real_secret", DefaultAPIVersion, server.Client())
	client.baseURL = server.URL
	session, err := client.CreateCheckoutSession(context.Background(), billing.CreateCheckoutCommand{AccountID: ids.AccountID("11111111-1111-4111-8111-111111111111"), CustomerID: "cus_1", StripePriceID: "price_private", OfferCode: "team", OfferVersion: 2, SuccessURL: "https://app.infiniteocean.net/success", CancelURL: "https://app.infiniteocean.net/cancel", IdempotencyKey: "checkout-key"})
	if err != nil || session.ID != "cs_test_1" {
		t.Fatalf("session=%+v err=%v", session, err)
	}
}

func TestRetrieveSubscriptionTranslatesCurrentItemPeriods(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"sub_1","customer":"cus_1","status":"active","created":1,"metadata":{"spyglass_account_id":"11111111-1111-4111-8111-111111111111","spyglass_offer_code":"team","spyglass_offer_version":"2"},"items":{"data":[{"current_period_start":1786968000,"current_period_end":1789646400,"price":{"id":"price_private"}}]}}`))
	}))
	defer server.Close()
	client, _ := New("sk_test_not_a_real_secret", DefaultAPIVersion, server.Client())
	client.baseURL = server.URL
	result, err := client.RetrieveSubscription(context.Background(), "sub_1")
	if err != nil || result.State != "active" || len(result.PriceIDs) != 1 || result.PriceIDs[0] != "price_private" || result.OfferVersion != 2 || result.ObjectVersion == "" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestRetrieveSubscriptionIdentifiesPausedCollection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"sub_1","customer":"cus_1","status":"active","pause_collection":{"behavior":"void"},"items":{"data":[]}}`))
	}))
	defer server.Close()
	client, _ := New("sk_test_not_a_real_secret", DefaultAPIVersion, server.Client())
	client.baseURL = server.URL
	result, err := client.RetrieveSubscription(context.Background(), "sub_1")
	if err != nil || !result.CollectionPaused {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

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
		for _, expected := range []string{"line_items%5B0%5D%5Bprice%5D=price_private", "line_items%5B1%5D%5Bprice%5D=price_commissioning", "metadata%5Bspyglass_account_id%5D=11111111-1111-4111-8111-111111111111", "subscription_data%5Bmetadata%5D%5Bspyglass_offer_code%5D=team", "metadata%5Bspyglass_affiliate_attribution_id%5D=22222222-2222-4222-8222-222222222222", "subscription_data%5Bmetadata%5D%5Bspyglass_affiliate_attribution_id%5D=22222222-2222-4222-8222-222222222222", "subscription_data%5Bmetadata%5D%5Bspyglass_commissioning_code%5D=commissioning_v1"} {
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
	session, err := client.CreateCheckoutSession(context.Background(), billing.CreateCheckoutCommand{AccountID: ids.AccountID("11111111-1111-4111-8111-111111111111"), CustomerID: "cus_1", StripePriceID: "price_private", OfferCode: "team", OfferVersion: 2, AffiliateAttributionID: ids.ReferralAttributionID("22222222-2222-4222-8222-222222222222"), RequestID: "33333333-3333-4333-8333-333333333333", CommissioningPriceID: "price_commissioning", CommissioningCode: "commissioning_v1", CommissioningVersion: 1, SuccessURL: "https://app.infiniteocean.net/success", CancelURL: "https://app.infiniteocean.net/cancel", IdempotencyKey: "checkout-key"})
	if err != nil || session.ID != "cs_test_1" {
		t.Fatalf("session=%+v err=%v", session, err)
	}
}

func TestOneTimeCheckoutPinsPurchaseMetadataToSessionAndPayment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form := string(body)
		for _, expected := range []string{"mode=payment", "line_items%5B0%5D%5Bprice%5D=price_tokens", "metadata%5Bspyglass_purchase_kind%5D=ai_token_top_up", "metadata%5Bspyglass_checkout_request_id%5D=33333333-3333-4333-8333-333333333333", "payment_intent_data%5Bmetadata%5D%5Bspyglass_item_code%5D=tokens_10k_v1"} {
			if !strings.Contains(form, expected) {
				t.Errorf("form missing %q: %s", expected, form)
			}
		}
		_, _ = w.Write([]byte(`{"id":"cs_test_purchase","url":"https://checkout.stripe.com/c/pay/purchase","expires_at":1786971600}`))
	}))
	defer server.Close()
	client, _ := New("sk_test_not_a_real_secret", DefaultAPIVersion, server.Client())
	client.baseURL = server.URL
	command := billing.CreateOneTimeCheckoutCommand{AccountID: "11111111-1111-4111-8111-111111111111", CustomerID: "cus_1", StripePriceID: "price_tokens", Kind: billing.PurchaseAITokenTopUp, ItemCode: "tokens_10k_v1", ItemVersion: 1, CatalogVersion: 3, RequestID: "33333333-3333-4333-8333-333333333333", SuccessURL: "https://app.infiniteocean.net/success", CancelURL: "https://app.infiniteocean.net/cancel", IdempotencyKey: "purchase-key"}
	if result, err := client.CreateOneTimeCheckoutSession(context.Background(), command); err != nil || result.ID != "cs_test_purchase" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestCustomerBalanceCreditIsNegativeAndIdempotentlyBound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/customers/cus_affiliate/balance_transactions" || r.Header.Get("Idempotency-Key") != "credit-key" {
			t.Errorf("request path=%q headers=%v", r.URL.Path, r.Header)
		}
		body, _ := io.ReadAll(r.Body)
		form := string(body)
		for _, expected := range []string{"amount=-1000", "currency=usd", "metadata%5Bspyglass_account_id%5D=11111111-1111-4111-8111-111111111111", "metadata%5Bspyglass_reference_id%5D=33333333-3333-4333-8333-333333333333"} {
			if !strings.Contains(form, expected) {
				t.Errorf("form missing %q: %s", expected, form)
			}
		}
		_, _ = w.Write([]byte(`{"id":"cbtxn_affiliate","customer":"cus_affiliate","amount":-1000,"currency":"usd"}`))
	}))
	defer server.Close()
	client, _ := New("sk_test_not_a_real_secret", DefaultAPIVersion, server.Client())
	client.baseURL = server.URL
	result, err := client.CreateCustomerBalanceCredit(context.Background(), billing.CreateCustomerBalanceCreditCommand{
		AccountID: "11111111-1111-4111-8111-111111111111", CustomerID: "cus_affiliate", AmountMinor: 1000,
		Currency: "USD", Reference: "33333333-3333-4333-8333-333333333333", IdempotencyKey: "credit-key",
	})
	if err != nil || result.ID != "cbtxn_affiliate" || result.AmountMinor != 1000 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestCustomerBalanceDebitCompensatesAReversedAffiliateCredit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/customers/cus_affiliate/balance_transactions" || r.Header.Get("Idempotency-Key") != "debit-key" {
			t.Errorf("request path=%q headers=%v", r.URL.Path, r.Header)
		}
		body, _ := io.ReadAll(r.Body)
		form := string(body)
		for _, expected := range []string{"amount=1000", "currency=usd", "metadata%5Bspyglass_account_id%5D=11111111-1111-4111-8111-111111111111", "metadata%5Bspyglass_reference_id%5D=33333333-3333-4333-8333-333333333333"} {
			if !strings.Contains(form, expected) {
				t.Errorf("form missing %q: %s", expected, form)
			}
		}
		_, _ = w.Write([]byte(`{"id":"cbtxn_reversal","customer":"cus_affiliate","amount":1000,"currency":"usd"}`))
	}))
	defer server.Close()
	client, _ := New("sk_test_not_a_real_secret", DefaultAPIVersion, server.Client())
	client.baseURL = server.URL
	result, err := client.CreateCustomerBalanceDebit(context.Background(), billing.CreateCustomerBalanceDebitCommand{
		AccountID: "11111111-1111-4111-8111-111111111111", CustomerID: "cus_affiliate", AmountMinor: 1000,
		Currency: "USD", Reference: "33333333-3333-4333-8333-333333333333", IdempotencyKey: "debit-key",
	})
	if err != nil || result.ID != "cbtxn_reversal" || result.AmountMinor != 1000 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestRetrieveSubscriptionTranslatesCurrentItemPeriods(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"sub_1","customer":"cus_1","status":"active","created":1,"metadata":{"spyglass_account_id":"11111111-1111-4111-8111-111111111111","spyglass_offer_code":"team","spyglass_offer_version":"2","spyglass_affiliate_attribution_id":"22222222-2222-4222-8222-222222222222"},"items":{"data":[{"current_period_start":1786968000,"current_period_end":1789646400,"price":{"id":"price_private"}}]}}`))
	}))
	defer server.Close()
	client, _ := New("sk_test_not_a_real_secret", DefaultAPIVersion, server.Client())
	client.baseURL = server.URL
	result, err := client.RetrieveSubscription(context.Background(), "sub_1")
	if err != nil || result.State != "active" || len(result.PriceIDs) != 1 || result.PriceIDs[0] != "price_private" || result.OfferVersion != 2 || result.AffiliateAttributionID != "22222222-2222-4222-8222-222222222222" || result.ObjectVersion == "" {
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

func TestCancelSubscriptionUsesDeleteAndStableIdempotency(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/v1/subscriptions/sub_lifecycle" || r.Header.Get("Idempotency-Key") != "subscription-lifecycle/lifecycle-id" {
			t.Errorf("request method=%q path=%q headers=%v", r.Method, r.URL.Path, r.Header)
		}
		_, _ = w.Write([]byte(`{"id":"sub_lifecycle","customer":"cus_1","status":"canceled","items":{"data":[]}}`))
	}))
	defer server.Close()
	client, _ := New("sk_test_not_a_real_secret", DefaultAPIVersion, server.Client())
	client.baseURL = server.URL
	if err := client.CancelSubscription(context.Background(), "sub_lifecycle", "subscription-lifecycle/lifecycle-id"); err != nil {
		t.Fatal(err)
	}
}

package billing_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/memory"
	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
)

type fixedClock struct{ value time.Time }

func (c fixedClock) Now() time.Time { return c.value }

func TestWebhookVerifiesAndDeduplicatesBeforeProjection(t *testing.T) {
	now := time.Date(2026, 8, 17, 18, 0, 0, 0, time.UTC)
	secret := "whsec_test_secret_value"
	verifier, err := billing.NewSignatureVerifier(secret, 5*time.Minute, fixedClock{now})
	if err != nil {
		t.Fatal(err)
	}
	inbox := memory.NewBillingInbox()
	service, err := billing.NewWebhookService(verifier, inbox, "test", fixedClock{now})
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(fmt.Sprintf(`{"id":"evt_phase2","type":"customer.subscription.updated","created":%d,"livemode":false,"data":{"object":{"id":"sub_123","metadata":{"spyglass_account_id":"11111111-1111-4111-8111-111111111111"}}}}`, now.Unix()))
	header := sign(secret, now.Unix(), payload)

	first, err := service.Ingest(context.Background(), payload, header)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Accepted {
		t.Fatal("first delivery was not accepted")
	}
	second, err := service.Ingest(context.Background(), payload, header)
	if err != nil {
		t.Fatal(err)
	}
	if second.Accepted {
		t.Fatal("duplicate delivery was accepted twice")
	}
	entry, ok := inbox.Entry("evt_phase2")
	if !ok || entry.ProviderObjectID != "sub_123" || entry.AccountID != "11111111-1111-4111-8111-111111111111" || entry.ProcessingState != "accepted" {
		t.Fatalf("unexpected inbox entry: %#v", entry)
	}
}

func TestWebhookRejectsTamperingStaleSignaturesAndWrongMode(t *testing.T) {
	now := time.Date(2026, 8, 17, 18, 0, 0, 0, time.UTC)
	secret := "whsec_test_secret_value"
	verifier, _ := billing.NewSignatureVerifier(secret, 5*time.Minute, fixedClock{now})
	testService, _ := billing.NewWebhookService(verifier, memory.NewBillingInbox(), "test", fixedClock{now})
	payload := []byte(fmt.Sprintf(`{"id":"evt_phase2","type":"invoice.paid","created":%d,"livemode":false,"data":{"object":{"id":"in_123"}}}`, now.Unix()))
	if _, err := testService.Ingest(context.Background(), append(payload, ' '), sign(secret, now.Unix(), payload)); err != billing.ErrInvalidSignature {
		t.Fatalf("expected tamper rejection, got %v", err)
	}
	if _, err := testService.Ingest(context.Background(), payload, sign(secret, now.Add(-10*time.Minute).Unix(), payload)); err != billing.ErrStaleSignature {
		t.Fatalf("expected stale rejection, got %v", err)
	}
	livePayload := []byte(fmt.Sprintf(`{"id":"evt_live","type":"invoice.paid","created":%d,"livemode":true,"data":{"object":{"id":"in_live"}}}`, now.Unix()))
	if _, err := testService.Ingest(context.Background(), livePayload, sign(secret, now.Unix(), livePayload)); err != billing.ErrWrongMode {
		t.Fatalf("expected mode rejection, got %v", err)
	}
}

func sign(secret string, timestamp int64, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(append([]byte(fmt.Sprintf("%d.", timestamp)), payload...))
	return fmt.Sprintf("t=%d,v1=%s", timestamp, hex.EncodeToString(mac.Sum(nil)))
}

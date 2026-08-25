package conversiontoken_test

import (
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/conversiontoken"
	"github.com/tinfoyle/spyglass-engine/internal/modules/analytics"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestTokenBindsReceiptSubjectAndExpiry(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	signer, _ := conversiontoken.New([]byte("0123456789abcdef0123456789abcdef"))
	claims := analytics.HandoffReference{
		ReceiptEventID: ids.AnalyticsEventID("10000000-0000-4000-8000-000000000001"),
		SubjectID:      ids.ConsentSubjectID("10000000-0000-4000-8000-000000000002"),
		ExpiresAt:      now.Add(time.Hour),
	}
	token, err := signer.Sign(claims, now)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := signer.Verify(token, now)
	if err != nil || verified != claims {
		t.Fatalf("verified=%+v err=%v", verified, err)
	}
	if _, err := signer.Verify(token, claims.ExpiresAt); err == nil {
		t.Fatal("expired token was accepted")
	}
	if _, err := signer.Verify(token+"x", now); err == nil {
		t.Fatal("tampered token was accepted")
	}
}

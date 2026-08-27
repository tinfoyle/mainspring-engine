package memory

import (
	"context"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/aitokenledger"
	"github.com/tinfoyle/spyglass-engine/internal/modules/aitokens"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestAITokenReservationRetryKeepsOriginalRateAfterCatalogChange(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	accountID := ids.AccountID("10000000-0000-4000-8000-000000000001")
	requestID := "20000000-0000-4000-8000-000000000002"
	ledger := NewAITokenLedger()
	grant, err := aitokens.NewGrant("30000000-0000-4000-8000-000000000003", accountID, aitokens.OriginPurchased, "tokens_v1", 1, "payment_1", 10_000, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ledger.Issue(context.Background(), grant, false); err != nil {
		t.Fatal(err)
	}
	originalRate := catalog.AIComplexityRate{Code: "balanced_v1", Version: 1, Complexity: catalog.AIComplexityBalanced, InputPerThousand: 4, CachedInputPerThousand: 1,
		OutputPerThousand: 16, MinimumCharge: 20, MaximumReservation: 2500, InternalProvider: "kimi", InternalModel: "kimi-v1", InternalAdapterVersion: 1, InternalModelPolicyVersion: 1}
	first, _, err := ledger.Reserve(context.Background(), aitokens.Reservation{ID: "40000000-0000-4000-8000-000000000004", AccountID: accountID, RequestID: requestID, Rate: originalRate, Maximum: originalRate.MaximumReservation, State: aitokens.ReservationActive}, now)
	if err != nil {
		t.Fatal(err)
	}
	changedRate := originalRate
	changedRate.Code, changedRate.Version, changedRate.InputPerThousand, changedRate.InternalModel = "balanced_v2", 2, 40, "kimi-v2"
	retried, balance, err := ledger.Reserve(context.Background(), aitokens.Reservation{ID: "50000000-0000-4000-8000-000000000005", AccountID: accountID, RequestID: requestID, Rate: changedRate, Maximum: changedRate.MaximumReservation, State: aitokens.ReservationActive}, now.Add(time.Minute))
	if err != nil || retried.ID != first.ID || retried.Rate.Code != originalRate.Code || retried.Rate.InternalModel != originalRate.InternalModel || balance.Reserved != originalRate.MaximumReservation {
		t.Fatalf("first=%+v retried=%+v balance=%+v err=%v", first, retried, balance, err)
	}
}

func TestAITokenPromotionEnforcesAccountAndCampaignCaps(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	firstAccount := ids.AccountID("10000000-0000-4000-8000-000000000001")
	secondAccount := ids.AccountID("10000000-0000-4000-8000-000000000002")
	ledger := NewAITokenLedger()
	grant := promotionGrant(t, "30000000-0000-4000-8000-000000000003", firstAccount, "40000000-0000-4000-8000-000000000004", now)
	if _, _, err := ledger.Issue(context.Background(), grant, false); err != aitokens.ErrInvalidGrant {
		t.Fatalf("generic promotion issue bypass error = %v", err)
	}
	first, _, err := ledger.RedeemPromotion(context.Background(), grant, 3, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	retried, _, err := ledger.RedeemPromotion(context.Background(), grant, 3, 1, 2)
	if err != nil || retried.ID != first.ID {
		t.Fatalf("retry=%+v err=%v", retried, err)
	}
	secondForAccount := promotionGrant(t, "30000000-0000-4000-8000-000000000005", firstAccount, "40000000-0000-4000-8000-000000000006", now)
	if _, _, err := ledger.RedeemPromotion(context.Background(), secondForAccount, 3, 1, 2); err != aitokenledger.ErrPromotionAccountLimit {
		t.Fatalf("Account limit error = %v", err)
	}
	second := promotionGrant(t, "30000000-0000-4000-8000-000000000007", secondAccount, "40000000-0000-4000-8000-000000000008", now)
	if _, _, err := ledger.RedeemPromotion(context.Background(), second, 3, 1, 2); err != nil {
		t.Fatal(err)
	}
	third := promotionGrant(t, "30000000-0000-4000-8000-000000000009", ids.AccountID("10000000-0000-4000-8000-000000000009"), "40000000-0000-4000-8000-000000000010", now)
	if _, _, err := ledger.RedeemPromotion(context.Background(), third, 3, 1, 2); err != aitokenledger.ErrPromotionIssuanceLimit {
		t.Fatalf("campaign limit error = %v", err)
	}
}

func promotionGrant(t *testing.T, id string, accountID ids.AccountID, requestID string, now time.Time) aitokens.Grant {
	t.Helper()
	expiresAt := now.Add(30 * 24 * time.Hour)
	grant, err := aitokens.NewGrant(ids.AITokenGrantID(id), accountID, aitokens.OriginPromotion, "launch_bonus", 7, requestID, 1_000, &expiresAt, now)
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

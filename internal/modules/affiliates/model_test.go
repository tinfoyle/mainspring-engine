package affiliates_test

import (
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/affiliates"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	affiliateID   = "10000000-0000-4000-8000-000000000001"
	userID        = "10000000-0000-4000-8000-000000000002"
	affiliateAcct = "10000000-0000-4000-8000-000000000003"
	referredAcct  = "10000000-0000-4000-8000-000000000004"
	attributionID = "10000000-0000-4000-8000-000000000005"
	checkoutID    = "10000000-0000-4000-8000-000000000006"
	ruleID        = "10000000-0000-4000-8000-000000000007"
	entryID       = "10000000-0000-4000-8000-000000000008"
)

func TestEnrollmentAndAttributionLockExactlyOneSubscription(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	enrollment, err := affiliates.NewEnrollment(ids.AffiliateID(affiliateID), ids.UserID(userID), ids.AccountID(affiliateAcct), " ocean-ab12 ", 1, 2, now)
	if err != nil || enrollment.PublicCode != "OCEAN-AB12" || enrollment.State != affiliates.EnrollmentActive {
		t.Fatalf("enrollment=%+v err=%v", enrollment, err)
	}
	attribution, err := affiliates.NewAttribution(ids.ReferralAttributionID(attributionID), enrollment, ids.AccountID(referredAcct), checkoutID, "team-monthly-v1", 4, now)
	if err != nil {
		t.Fatal(err)
	}
	locked, err := attribution.Lock("sub_eligible", now.Add(time.Minute))
	if err != nil || locked.State != affiliates.AttributionLocked || locked.Version != 2 {
		t.Fatalf("locked=%+v err=%v", locked, err)
	}
	replay, err := locked.Lock("sub_eligible", now.Add(2*time.Minute))
	if err != nil || replay.SubscriptionID != locked.SubscriptionID || replay.Version != locked.Version {
		t.Fatalf("exact replay=%+v err=%v", replay, err)
	}
	if _, err := locked.Lock("sub_changed", now.Add(2*time.Minute)); !errors.Is(err, affiliates.ErrAttributionLocked) {
		t.Fatalf("changed lock returned %v", err)
	}
}

func TestSelfReferralFailsClosed(t *testing.T) {
	now := time.Now().UTC()
	enrollment, _ := affiliates.NewEnrollment(ids.AffiliateID(affiliateID), ids.UserID(userID), ids.AccountID(affiliateAcct), "OCEAN-AB12", 1, 2, now)
	_, err := affiliates.NewAttribution(ids.ReferralAttributionID(attributionID), enrollment, ids.AccountID(affiliateAcct), checkoutID, "team-monthly-v1", 4, now)
	if !errors.Is(err, affiliates.ErrSelfReferral) {
		t.Fatalf("self referral returned %v", err)
	}
}

func TestEnrollmentLifecycleMakesClosureTerminal(t *testing.T) {
	now := time.Now().UTC()
	enrollment, _ := affiliates.NewEnrollment(ids.AffiliateID(affiliateID), ids.UserID(userID), ids.AccountID(affiliateAcct), "OCEAN-AB12", 1, 2, now)
	if err := enrollment.CanTransition(affiliates.EnrollmentSuspended); err != nil {
		t.Fatal(err)
	}
	enrollment.State = affiliates.EnrollmentSuspended
	if err := enrollment.CanTransition(affiliates.EnrollmentActive); err != nil {
		t.Fatal(err)
	}
	enrollment.State = affiliates.EnrollmentClosed
	if !errors.Is(enrollment.CanTransition(affiliates.EnrollmentActive), affiliates.ErrInvalidEnrollment) {
		t.Fatal("closed enrollment was reopened")
	}
}

func TestCommissionRuleProducesPendingEarningAndImmutableReversal(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	enrollment, _ := affiliates.NewEnrollment(ids.AffiliateID(affiliateID), ids.UserID(userID), ids.AccountID(affiliateAcct), "OCEAN-AB12", 1, 2, now)
	attribution, _ := affiliates.NewAttribution(ids.ReferralAttributionID(attributionID), enrollment, ids.AccountID(referredAcct), checkoutID, "team-monthly-v1", 4, now)
	attribution, _ = attribution.Lock("sub_eligible", now)
	rule := affiliates.CommissionRule{ID: ids.CommissionRuleID(ruleID), Version: 2, OfferCode: "team-monthly-v1", Currency: "USD", EligibleInvoiceMinor: 5000, CommissionMinor: 1000, InitialInvoiceQualifies: false, HoldDays: 30, EffectiveFrom: now}
	if err := rule.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := affiliates.NewEarnedEntry(ids.CommissionEntryID(entryID), attribution, rule, "in_first", "pi_first", 1, now); !errors.Is(err, affiliates.ErrInvalidCommission) {
		t.Fatalf("first invoice returned %v", err)
	}
	earning, err := affiliates.NewEarnedEntry(ids.CommissionEntryID(entryID), attribution, rule, "in_renewal", "pi_renewal", 2, now)
	if err != nil || earning.AmountMinor != 1000 || earning.State != affiliates.CommissionPending || !earning.AvailableAt.Equal(now) {
		t.Fatalf("earning=%+v err=%v", earning, err)
	}
	discounted, err := rule.CommissionAmount(4500)
	if err != nil || discounted != 900 {
		t.Fatalf("discounted commission=%d err=%v", discounted, err)
	}
	reversal, err := affiliates.NewReversalEntry(ids.CommissionEntryID("10000000-0000-4000-8000-000000000009"), earning, "in_renewal", now.Add(time.Hour))
	if err != nil || reversal.Kind != affiliates.CommissionReversal || reversal.ReversesID == nil || *reversal.ReversesID != earning.ID || reversal.State != affiliates.CommissionSettled {
		t.Fatalf("reversal=%+v err=%v", reversal, err)
	}
}

func TestCommissionRuleRejectsCommissionAboveEligibleRevenue(t *testing.T) {
	rule := affiliates.CommissionRule{ID: ids.CommissionRuleID(ruleID), Version: 1, OfferCode: "team-monthly-v1", Currency: "USD", EligibleInvoiceMinor: 5000, CommissionMinor: 6000, EffectiveFrom: time.Now()}
	if !errors.Is(rule.Validate(), affiliates.ErrInvalidRule) {
		t.Fatal("overfunded rule accepted")
	}
}

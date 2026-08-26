package affiliateprogram_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/affiliateprogram"
	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/affiliates"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	userID        = "10000000-0000-4000-8000-000000000001"
	affiliateAcct = "10000000-0000-4000-8000-000000000002"
	referredAcct  = "10000000-0000-4000-8000-000000000003"
)

type repository struct {
	enrollment  affiliates.Enrollment
	attribution affiliates.Attribution
	rule        affiliates.CommissionRule
	entries     []affiliates.CommissionEntry
	dataExport  affiliateprogram.DataExport
	canSettle   bool
	owned       map[ids.AccountID]bool
}

func (r *repository) CanSettleToAccount(_ context.Context, _ ids.UserID, accountID ids.AccountID) (bool, error) {
	if r.owned != nil {
		return r.owned[accountID], nil
	}
	return r.canSettle, nil
}
func (r *repository) CreateEnrollment(_ context.Context, enrollment affiliates.Enrollment) error {
	r.enrollment = enrollment
	return nil
}
func (r *repository) ReplaceEnrollmentCode(_ context.Context, userID ids.UserID, expectedVersion uint64, code string, _ time.Time) (affiliates.Enrollment, error) {
	if r.enrollment.UserID != userID {
		return affiliates.Enrollment{}, affiliateprogram.ErrEnrollmentNotFound
	}
	if r.enrollment.Version != expectedVersion {
		return affiliates.Enrollment{}, affiliateprogram.ErrEnrollmentConflict
	}
	replaced, err := r.enrollment.ReplacePublicCode(code)
	if err != nil {
		return affiliates.Enrollment{}, affiliateprogram.ErrEnrollmentState
	}
	r.enrollment = replaced
	return replaced, nil
}
func (r *repository) EnrollmentByUser(context.Context, ids.UserID) (affiliates.Enrollment, error) {
	return r.enrollment, nil
}
func (r *repository) EnrollmentByCode(_ context.Context, code string) (affiliates.Enrollment, error) {
	if r.enrollment.PublicCode != code {
		return affiliates.Enrollment{}, errors.New("not found")
	}
	return r.enrollment, nil
}
func (r *repository) CreateAttribution(_ context.Context, attribution affiliates.Attribution) error {
	r.attribution = attribution
	return nil
}
func (r *repository) LockAttribution(_ context.Context, _ ids.ReferralAttributionID, subscriptionID string, now time.Time) (affiliates.Attribution, error) {
	locked, err := r.attribution.Lock(subscriptionID, now)
	if err == nil {
		r.attribution = locked
	}
	return locked, err
}
func (r *repository) AttributionBySubscription(_ context.Context, subscriptionID string) (affiliates.Attribution, error) {
	if r.attribution.SubscriptionID != subscriptionID || subscriptionID == "" {
		return affiliates.Attribution{}, affiliateprogram.ErrAttributionNotFound
	}
	return r.attribution, nil
}
func (r *repository) CommissionRule(context.Context, uint64) (affiliates.CommissionRule, error) {
	return r.rule, nil
}
func (r *repository) AppendCommission(_ context.Context, entry affiliates.CommissionEntry) (affiliates.CommissionEntry, error) {
	r.entries = append(r.entries, entry)
	return entry, nil
}
func (r *repository) RecordPaidCommission(_ context.Context, id, _ ids.CommissionEntryID, attribution affiliates.Attribution, rule affiliates.CommissionRule, invoiceID, paymentIntentID string, initial bool, now time.Time) (affiliates.CommissionEntry, error) {
	cycle := uint32(1)
	if !initial {
		cycle = 2
		for _, entry := range r.entries {
			if entry.SubscriptionID == attribution.SubscriptionID && entry.Kind == affiliates.CommissionEarned && entry.Cycle >= cycle {
				cycle = entry.Cycle + 1
			}
		}
	}
	entry, err := affiliates.NewEarnedEntry(id, attribution, rule, invoiceID, paymentIntentID, cycle, now)
	if err != nil {
		return affiliates.CommissionEntry{}, err
	}
	r.entries = append(r.entries, entry)
	return entry, nil
}

func (r *repository) RecordAdverseCommission(_ context.Context, id ids.CommissionEntryID, evidence affiliates.AdverseBillingEvidence, now time.Time) (affiliates.CommissionEntry, bool, error) {
	for _, original := range r.entries {
		if original.Kind == affiliates.CommissionEarned && original.PaymentIntentID == evidence.PaymentIntentID {
			reversal, err := affiliates.NewReversalEntry(id, original, original.InvoiceID, now)
			if err != nil {
				return affiliates.CommissionEntry{}, false, err
			}
			r.entries = append(r.entries, reversal)
			return reversal, true, nil
		}
	}
	return affiliates.CommissionEntry{}, false, nil
}

func (r *repository) AttributionByCheckoutRequest(_ context.Context, requestID string) (affiliates.Attribution, error) {
	if r.attribution.CheckoutRequestID == requestID {
		return r.attribution, nil
	}
	return affiliates.Attribution{}, affiliateprogram.ErrAttributionNotFound
}
func (r *repository) StatementSnapshot(_ context.Context, affiliateID ids.AffiliateID) (uint64, []affiliates.CommissionEntry, error) {
	count := uint64(0)
	if r.attribution.AffiliateID == affiliateID && r.attribution.State == affiliates.AttributionLocked {
		count = 1
	}
	return count, append([]affiliates.CommissionEntry{}, r.entries...), nil
}
func (r *repository) DataExport(context.Context, ids.UserID) (affiliateprogram.DataExport, error) {
	return r.dataExport, nil
}

type generator struct{ values []string }

func (g *generator) New() string {
	value := g.values[0]
	g.values = g.values[1:]
	return value
}

type codes struct{ code string }

func (c codes) NewCode() (string, error) { return c.code, nil }

type clock struct{ now time.Time }

func (c clock) Now() time.Time { return c.now }

func strongSession(now time.Time) sessions.Session {
	return sessions.Session{UserID: userID, ReauthenticatedAt: now, ReauthenticationMethod: sessions.AuthenticationMethodPasskey}
}

func TestAffiliateDataExportRequiresStrongAuthenticationAndOwnedIdentity(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	repository := &repository{dataExport: affiliateprogram.DataExport{
		SchemaVersion:     affiliateprogram.AffiliateDataExportSchemaVersion,
		GeneratedAt:       now,
		Enrollment:        &affiliateprogram.DataExportEnrollment{UserID: userID},
		PublicCodes:       []affiliateprogram.DataExportPublicCode{},
		EnrollmentEvents:  []affiliateprogram.DataExportLifecycleEvent{},
		CommissionEntries: []affiliateprogram.DataExportCommission{},
		SupportRequests:   []affiliateprogram.DataExportSupportRequest{},
		SupportEvents:     []affiliateprogram.DataExportSupportEvent{},
	}}
	service, _ := affiliateprogram.New(repository, &generator{values: []string{"10000000-0000-4000-8000-000000000010"}}, codes{"IO-PARTNER1"}, clock{now}, 2, 3)

	if _, err := service.Export(context.Background(), affiliateprogram.ExportCommand{UserID: userID, Session: sessions.Session{UserID: userID}}); !errors.Is(err, strongauth.ErrRequired) {
		t.Fatalf("weak export returned %v", err)
	}
	value, err := service.Export(context.Background(), affiliateprogram.ExportCommand{UserID: userID, Session: strongSession(now)})
	if err != nil || value.Enrollment == nil || value.Enrollment.UserID != userID {
		t.Fatalf("export=%+v err=%v", value, err)
	}
	repository.dataExport.Enrollment.UserID = "10000000-0000-4000-8000-000000000099"
	if _, err := service.Export(context.Background(), affiliateprogram.ExportCommand{UserID: userID, Session: strongSession(now)}); err == nil {
		t.Fatal("cross-identity Affiliate export was accepted")
	}
}

func TestEnrollRequiresCurrentTermsAndOwnedSettlementAccount(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	repository := &repository{owned: map[ids.AccountID]bool{ids.AccountID(affiliateAcct): true}}
	service, _ := affiliateprogram.New(repository, &generator{values: []string{"10000000-0000-4000-8000-000000000010"}}, codes{"IO-PARTNER1"}, clock{now}, 2, 3)
	if _, err := service.Enroll(context.Background(), affiliateprogram.EnrollCommand{UserID: ids.UserID(userID), Session: strongSession(now), SettlementAccountID: ids.AccountID(affiliateAcct), AcceptedTermsVersion: 1}); !errors.Is(err, affiliateprogram.ErrTermsRequired) {
		t.Fatalf("old terms returned %v", err)
	}
	enrollment, err := service.Enroll(context.Background(), affiliateprogram.EnrollCommand{UserID: ids.UserID(userID), Session: strongSession(now), SettlementAccountID: ids.AccountID(affiliateAcct), AcceptedTermsVersion: 2})
	if err != nil || enrollment.PublicCode != "IO-PARTNER1" || enrollment.RuleVersion != 3 {
		t.Fatalf("enrollment=%+v err=%v", enrollment, err)
	}
	repository.owned = nil
	repository.canSettle = false
	service, _ = affiliateprogram.New(repository, &generator{values: []string{"10000000-0000-4000-8000-000000000011"}}, codes{"IO-PARTNER2"}, clock{now}, 2, 3)
	if _, err := service.Enroll(context.Background(), affiliateprogram.EnrollCommand{UserID: ids.UserID(userID), Session: strongSession(now), SettlementAccountID: ids.AccountID(affiliateAcct), AcceptedTermsVersion: 2}); !errors.Is(err, affiliateprogram.ErrSettlementAccountDenied) {
		t.Fatalf("foreign settlement account returned %v", err)
	}
}

func TestReplaceCodeRequiresStrongAuthenticationAndExactActiveVersion(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	enrollment, _ := affiliates.NewEnrollment(ids.AffiliateID("10000000-0000-4000-8000-000000000010"), ids.UserID(userID), "", "IO-PARTNER1", 2, 3, now)
	repository := &repository{enrollment: enrollment}
	service, _ := affiliateprogram.New(repository, &generator{values: []string{"10000000-0000-4000-8000-000000000011"}}, codes{"IO-PARTNER2"}, clock{now}, 2, 3)

	if _, err := service.ReplaceCode(context.Background(), affiliateprogram.ReplaceCodeCommand{UserID: ids.UserID(userID), Session: sessions.Session{UserID: userID}, ExpectedVersion: 1}); !errors.Is(err, strongauth.ErrRequired) {
		t.Fatalf("weak replacement returned %v", err)
	}
	if _, err := service.ReplaceCode(context.Background(), affiliateprogram.ReplaceCodeCommand{UserID: ids.UserID(userID), Session: strongSession(now), ExpectedVersion: 2}); !errors.Is(err, affiliateprogram.ErrEnrollmentConflict) {
		t.Fatalf("stale replacement returned %v", err)
	}
	replaced, err := service.ReplaceCode(context.Background(), affiliateprogram.ReplaceCodeCommand{UserID: ids.UserID(userID), Session: strongSession(now), ExpectedVersion: 1})
	if err != nil || replaced.PublicCode != "IO-PARTNER2" || replaced.Version != 2 {
		t.Fatalf("replacement=%+v err=%v", replaced, err)
	}
	repository.enrollment.State = affiliates.EnrollmentSuspended
	if _, err := service.ReplaceCode(context.Background(), affiliateprogram.ReplaceCodeCommand{UserID: ids.UserID(userID), Session: strongSession(now), ExpectedVersion: 2}); !errors.Is(err, affiliateprogram.ErrEnrollmentState) {
		t.Fatalf("suspended replacement returned %v", err)
	}
}

func TestReferralAndPaidRenewalProduceOneLedgerEntry(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	repository := &repository{owned: map[ids.AccountID]bool{ids.AccountID(affiliateAcct): true}}
	repository.rule = affiliates.CommissionRule{ID: ids.CommissionRuleID("10000000-0000-4000-8000-000000000020"), Version: 3, OfferCode: "team-monthly-v1", Currency: "USD", EligibleInvoiceMinor: 5000, CommissionMinor: 1000, InitialInvoiceQualifies: false, HoldDays: 30, EffectiveFrom: now}
	service, _ := affiliateprogram.New(repository, &generator{values: []string{
		"10000000-0000-4000-8000-000000000010",
		"10000000-0000-4000-8000-000000000011",
		"10000000-0000-4000-8000-000000000012",
		"10000000-0000-4000-8000-000000000013",
	}}, codes{"IO-PARTNER1"}, clock{now}, 2, 3)
	_, _ = service.Enroll(context.Background(), affiliateprogram.EnrollCommand{UserID: ids.UserID(userID), Session: strongSession(now), SettlementAccountID: ids.AccountID(affiliateAcct), AcceptedTermsVersion: 2})
	attribution, err := service.Reserve(context.Background(), affiliateprogram.ReserveCommand{PublicCode: "io-partner1", ReferredAccountID: ids.AccountID(referredAcct), CheckoutRequestID: "10000000-0000-4000-8000-000000000030", OfferCode: "team-monthly-v1", OfferVersion: 4})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Lock(context.Background(), attribution.ID, "sub_paid"); err != nil {
		t.Fatal(err)
	}
	entry, err := service.RecordPaidInvoice(context.Background(), affiliateprogram.PaidInvoice{SubscriptionID: "sub_paid", InvoiceID: "in_renewal", PaymentIntentID: "pi_renewal", AmountPaidMinor: 5000, Currency: "usd", Initial: false})
	if err != nil || entry.AmountMinor != 1000 || len(repository.entries) != 1 {
		t.Fatalf("entry=%+v err=%v entries=%d", entry, err, len(repository.entries))
	}
	statement, err := service.Statement(context.Background(), ids.UserID(userID))
	if err != nil || statement.ReferredSubscriptions != 1 || statement.PendingMinor != 1000 || statement.Currency != "USD" {
		t.Fatalf("statement=%+v err=%v", statement, err)
	}
}

func TestPaidInvoiceMustMatchFrozenRule(t *testing.T) {
	now := time.Now().UTC()
	repository := &repository{rule: affiliates.CommissionRule{ID: ids.CommissionRuleID("10000000-0000-4000-8000-000000000020"), Version: 3, OfferCode: "team-monthly-v1", Currency: "USD", EligibleInvoiceMinor: 5000, CommissionMinor: 1000, InitialInvoiceQualifies: true, EffectiveFrom: now}}
	repository.attribution = affiliates.Attribution{ID: ids.ReferralAttributionID("10000000-0000-4000-8000-000000000011"), AffiliateID: ids.AffiliateID("10000000-0000-4000-8000-000000000010"), OfferCode: "team-monthly-v1", RuleVersion: 3, State: affiliates.AttributionLocked, SubscriptionID: "sub_paid"}
	service, _ := affiliateprogram.New(repository, &generator{values: []string{"10000000-0000-4000-8000-000000000012"}}, codes{"IO-PARTNER1"}, clock{now}, 2, 3)
	if _, err := service.RecordPaidInvoice(context.Background(), affiliateprogram.PaidInvoice{SubscriptionID: "sub_paid", InvoiceID: "in_discounted", PaymentIntentID: "pi_discounted", AmountPaidMinor: 4900, Currency: "USD", Initial: true}); !errors.Is(err, affiliateprogram.ErrInvoiceIneligible) {
		t.Fatalf("discounted invoice returned %v", err)
	}
}

func TestReserveRejectsAnyAccountOwnedByAffiliateUser(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	repository := &repository{owned: map[ids.AccountID]bool{ids.AccountID(referredAcct): true}}
	enrollment, _ := affiliates.NewEnrollment(ids.AffiliateID("10000000-0000-4000-8000-000000000010"), ids.UserID(userID), "", "IO-PARTNER1", 2, 3, now)
	repository.enrollment = enrollment
	service, _ := affiliateprogram.New(repository, &generator{values: []string{"10000000-0000-4000-8000-000000000011"}}, codes{"IO-UNUSED1"}, clock{now}, 2, 3)
	_, err := service.Reserve(context.Background(), affiliateprogram.ReserveCommand{
		PublicCode: "IO-PARTNER1", ReferredAccountID: ids.AccountID(referredAcct),
		CheckoutRequestID: "10000000-0000-4000-8000-000000000012", OfferCode: "spyglass-pro", OfferVersion: 1,
	})
	if !errors.Is(err, affiliates.ErrSelfReferral) {
		t.Fatalf("self-referral returned %v", err)
	}
}

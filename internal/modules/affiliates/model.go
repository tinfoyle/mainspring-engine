package affiliates

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type EnrollmentState string
type AttributionState string
type CommissionKind string
type CommissionState string

const (
	EnrollmentActive    EnrollmentState = "active"
	EnrollmentSuspended EnrollmentState = "suspended"
	EnrollmentClosed    EnrollmentState = "closed"

	AttributionReserved AttributionState = "reserved"
	AttributionLocked   AttributionState = "locked"
	AttributionCanceled AttributionState = "canceled"

	CommissionEarned   CommissionKind = "earned"
	CommissionReversal CommissionKind = "reversal"

	CommissionPending CommissionState = "pending"
	CommissionSettled CommissionState = "settled"
)

var (
	ErrInvalidEnrollment  = errors.New("affiliate enrollment is invalid")
	ErrInvalidAttribution = errors.New("affiliate attribution is invalid")
	ErrSelfReferral       = errors.New("affiliate cannot refer its own account")
	ErrAttributionLocked  = errors.New("affiliate attribution is already locked")
	ErrInvalidRule        = errors.New("affiliate commission rule is invalid")
	ErrInvalidCommission  = errors.New("affiliate commission entry is invalid")
)

var publicCodePattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9-]{4,22}[A-Z0-9]$`)

type Enrollment struct {
	ID                  ids.AffiliateID `json:"affiliate_id"`
	UserID              ids.UserID      `json:"user_id"`
	SettlementAccountID ids.AccountID   `json:"settlement_account_id,omitempty"`
	PublicCode          string          `json:"public_code"`
	TermsVersion        uint64          `json:"terms_version"`
	RuleVersion         uint64          `json:"rule_version"`
	State               EnrollmentState `json:"state"`
	Version             uint64          `json:"version"`
	CreatedAt           time.Time       `json:"created_at"`
}

func NewEnrollment(id ids.AffiliateID, userID ids.UserID, settlementAccountID ids.AccountID, publicCode string, termsVersion, ruleVersion uint64, now time.Time) (Enrollment, error) {
	publicCode = NormalizeCode(publicCode)
	if ids.Validate(string(id)) != nil || ids.Validate(string(userID)) != nil || (settlementAccountID != "" && ids.Validate(string(settlementAccountID)) != nil) || !publicCodePattern.MatchString(publicCode) || termsVersion == 0 || ruleVersion == 0 || now.IsZero() {
		return Enrollment{}, ErrInvalidEnrollment
	}
	return Enrollment{ID: id, UserID: userID, SettlementAccountID: settlementAccountID, PublicCode: publicCode, TermsVersion: termsVersion, RuleVersion: ruleVersion, State: EnrollmentActive, Version: 1, CreatedAt: now.UTC()}, nil
}

func NormalizeCode(code string) string { return strings.ToUpper(strings.TrimSpace(code)) }

type Attribution struct {
	ID                ids.ReferralAttributionID `json:"attribution_id"`
	AffiliateID       ids.AffiliateID           `json:"affiliate_id"`
	ReferredAccountID ids.AccountID             `json:"referred_account_id"`
	CheckoutRequestID string                    `json:"checkout_request_id"`
	OfferCode         string                    `json:"offer_code"`
	OfferVersion      uint64                    `json:"offer_version"`
	RuleVersion       uint64                    `json:"rule_version"`
	State             AttributionState          `json:"state"`
	SubscriptionID    string                    `json:"-"`
	Version           uint64                    `json:"version"`
	CreatedAt         time.Time                 `json:"created_at"`
	LockedAt          *time.Time                `json:"locked_at,omitempty"`
}

func NewAttribution(id ids.ReferralAttributionID, enrollment Enrollment, referredAccountID ids.AccountID, checkoutRequestID, offerCode string, offerVersion uint64, now time.Time) (Attribution, error) {
	if enrollment.State != EnrollmentActive || ids.Validate(string(id)) != nil || ids.Validate(string(referredAccountID)) != nil || ids.Validate(checkoutRequestID) != nil || !validCode(offerCode) || offerVersion == 0 || enrollment.RuleVersion == 0 || now.IsZero() {
		return Attribution{}, ErrInvalidAttribution
	}
	if enrollment.SettlementAccountID != "" && enrollment.SettlementAccountID == referredAccountID {
		return Attribution{}, ErrSelfReferral
	}
	return Attribution{ID: id, AffiliateID: enrollment.ID, ReferredAccountID: referredAccountID, CheckoutRequestID: checkoutRequestID, OfferCode: offerCode, OfferVersion: offerVersion, RuleVersion: enrollment.RuleVersion, State: AttributionReserved, Version: 1, CreatedAt: now.UTC()}, nil
}

func (a Attribution) Lock(subscriptionID string, now time.Time) (Attribution, error) {
	if a.State == AttributionLocked {
		if a.SubscriptionID == subscriptionID {
			return a, nil
		}
		return Attribution{}, ErrAttributionLocked
	}
	if a.State != AttributionReserved || !strings.HasPrefix(subscriptionID, "sub_") || len(subscriptionID) > 200 || strings.ContainsAny(subscriptionID, "\r\n\t ") || now.IsZero() || now.Before(a.CreatedAt) {
		return Attribution{}, ErrInvalidAttribution
	}
	locked := now.UTC()
	a.State, a.SubscriptionID, a.LockedAt, a.Version = AttributionLocked, subscriptionID, &locked, a.Version+1
	return a, nil
}

type CommissionRule struct {
	ID                      ids.CommissionRuleID `json:"rule_id"`
	Version                 uint64               `json:"version"`
	OfferCode               string               `json:"offer_code"`
	Currency                string               `json:"currency"`
	EligibleInvoiceMinor    int64                `json:"eligible_invoice_minor"`
	CommissionMinor         int64                `json:"commission_minor"`
	InitialInvoiceQualifies bool                 `json:"initial_invoice_qualifies"`
	MaximumCycles           uint32               `json:"maximum_cycles,omitempty"`
	HoldDays                uint16               `json:"hold_days"`
	EffectiveFrom           time.Time            `json:"effective_from"`
}

func (r CommissionRule) Validate() error {
	if ids.Validate(string(r.ID)) != nil || r.Version == 0 || !validCode(r.OfferCode) || len(r.Currency) != 3 || r.Currency != strings.ToUpper(r.Currency) || r.EligibleInvoiceMinor <= 0 || r.CommissionMinor <= 0 || r.CommissionMinor > r.EligibleInvoiceMinor || r.HoldDays > 180 || r.EffectiveFrom.IsZero() {
		return ErrInvalidRule
	}
	return nil
}

type CommissionEntry struct {
	ID             ids.CommissionEntryID     `json:"entry_id"`
	AffiliateID    ids.AffiliateID           `json:"affiliate_id"`
	AttributionID  ids.ReferralAttributionID `json:"attribution_id"`
	RuleVersion    uint64                    `json:"rule_version"`
	SubscriptionID string                    `json:"-"`
	InvoiceID      string                    `json:"-"`
	Cycle          uint32                    `json:"cycle"`
	Kind           CommissionKind            `json:"kind"`
	State          CommissionState           `json:"state"`
	AmountMinor    int64                     `json:"amount_minor"`
	Currency       string                    `json:"currency"`
	ReversesID     *ids.CommissionEntryID    `json:"reverses_entry_id,omitempty"`
	AvailableAt    time.Time                 `json:"available_at"`
	CreatedAt      time.Time                 `json:"created_at"`
}

func NewEarnedEntry(id ids.CommissionEntryID, attribution Attribution, rule CommissionRule, invoiceID string, cycle uint32, now time.Time) (CommissionEntry, error) {
	if attribution.State != AttributionLocked || attribution.RuleVersion != rule.Version || attribution.OfferCode != rule.OfferCode || rule.Validate() != nil || ids.Validate(string(id)) != nil || !validProviderID(invoiceID, "in_") || cycle == 0 || now.IsZero() || (cycle == 1 && !rule.InitialInvoiceQualifies) || (rule.MaximumCycles > 0 && cycle > rule.MaximumCycles) {
		return CommissionEntry{}, ErrInvalidCommission
	}
	return CommissionEntry{ID: id, AffiliateID: attribution.AffiliateID, AttributionID: attribution.ID, RuleVersion: rule.Version, SubscriptionID: attribution.SubscriptionID, InvoiceID: invoiceID, Cycle: cycle, Kind: CommissionEarned, State: CommissionPending, AmountMinor: rule.CommissionMinor, Currency: rule.Currency, AvailableAt: now.UTC().Add(time.Duration(rule.HoldDays) * 24 * time.Hour), CreatedAt: now.UTC()}, nil
}

func NewReversalEntry(id ids.CommissionEntryID, original CommissionEntry, invoiceID string, now time.Time) (CommissionEntry, error) {
	if original.Kind != CommissionEarned || ids.Validate(string(id)) != nil || !validProviderID(invoiceID, "in_") || now.IsZero() || now.Before(original.CreatedAt) {
		return CommissionEntry{}, ErrInvalidCommission
	}
	reverses := original.ID
	return CommissionEntry{ID: id, AffiliateID: original.AffiliateID, AttributionID: original.AttributionID, RuleVersion: original.RuleVersion, SubscriptionID: original.SubscriptionID, InvoiceID: invoiceID, Cycle: original.Cycle, Kind: CommissionReversal, State: CommissionSettled, AmountMinor: original.AmountMinor, Currency: original.Currency, ReversesID: &reverses, AvailableAt: now.UTC(), CreatedAt: now.UTC()}, nil
}

func validProviderID(value, prefix string) bool {
	return strings.HasPrefix(value, prefix) && len(value) <= 200 && !strings.ContainsAny(value, "\r\n\t ")
}

func validCode(value string) bool {
	if value == "" || len(value) > 100 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

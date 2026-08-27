package affiliateprogram

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/affiliates"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrTermsRequired           = errors.New("current affiliate terms must be accepted")
	ErrSettlementAccountDenied = errors.New("affiliate settlement account is not owned by the user")
	ErrCodeUnavailable         = errors.New("affiliate code is unavailable")
	ErrInvoiceIneligible       = errors.New("invoice is not eligible for affiliate commission")
	ErrAttributionNotFound     = errors.New("affiliate checkout attribution was not found")
	ErrAttributionConflict     = errors.New("affiliate checkout attribution conflicts with the existing request")
	ErrProgramUnavailable      = errors.New("affiliate program rule is unavailable")
	ErrEnrollmentNotFound      = errors.New("Affiliate enrollment was not found")
	ErrEnrollmentRestricted    = errors.New("Affiliate enrollment is restricted after verified erasure")
	ErrEnrollmentState         = errors.New("Affiliate enrollment does not permit public code replacement")
	ErrEnrollmentConflict      = errors.New("Affiliate enrollment changed before public code replacement")
)

type Clock interface{ Now() time.Time }

type CodeGenerator interface{ NewCode() (string, error) }

type Repository interface {
	CanSettleToAccount(context.Context, ids.UserID, ids.AccountID) (bool, error)
	CreateEnrollment(context.Context, affiliates.Enrollment) error
	ReplaceEnrollmentCode(context.Context, ids.UserID, uint64, string, time.Time) (affiliates.Enrollment, error)
	EnrollmentByUser(context.Context, ids.UserID) (affiliates.Enrollment, error)
	EnrollmentByCode(context.Context, string) (affiliates.Enrollment, error)
	CreateAttribution(context.Context, affiliates.Attribution) error
	AttributionByCheckoutRequest(context.Context, string) (affiliates.Attribution, error)
	LockAttribution(context.Context, ids.ReferralAttributionID, string, time.Time) (affiliates.Attribution, error)
	AttributionBySubscription(context.Context, string) (affiliates.Attribution, error)
	CommissionRule(context.Context, uint64) (affiliates.CommissionRule, error)
	EligibleInvoiceLines(context.Context, affiliates.Attribution, string, []InvoiceLine) ([]InvoiceLine, int64, error)
	AppendCommission(context.Context, affiliates.CommissionEntry) (affiliates.CommissionEntry, error)
	RecordPaidCommission(context.Context, ids.CommissionEntryID, ids.CommissionEntryID, ids.CommissionEntryID, affiliates.Attribution, affiliates.CommissionRule, PaidInvoice, []InvoiceLine, int64, time.Time) (affiliates.CommissionEntry, error)
	RecordAdverseCommission(context.Context, ids.CommissionEntryID, affiliates.AdverseBillingEvidence, time.Time) (affiliates.CommissionEntry, bool, error)
	RecordSubscriptionTermination(context.Context, string, ids.CommissionEntryID, time.Time) (affiliates.CommissionEntry, bool, error)
	StatementSnapshot(context.Context, ids.AffiliateID) (uint64, []affiliates.CommissionEntry, error)
	SettlementSnapshot(context.Context, ids.AffiliateID) (SettlementSnapshot, error)
	DataExport(context.Context, ids.UserID) (DataExport, error)
}

type Service struct {
	repository   Repository
	ids          ids.Generator
	codes        CodeGenerator
	clock        Clock
	termsVersion uint64
	ruleVersion  uint64
}

func New(repository Repository, generator ids.Generator, codes CodeGenerator, clock Clock, termsVersion, ruleVersion uint64) (*Service, error) {
	if repository == nil || generator == nil || codes == nil || clock == nil || termsVersion == 0 || ruleVersion == 0 {
		return nil, errors.New("affiliate program dependencies and versions are required")
	}
	return &Service{repository: repository, ids: generator, codes: codes, clock: clock, termsVersion: termsVersion, ruleVersion: ruleVersion}, nil
}

type EnrollCommand struct {
	UserID               ids.UserID
	Session              sessions.Session
	SettlementAccountID  ids.AccountID
	AcceptedTermsVersion uint64
}

func (s *Service) Enroll(ctx context.Context, command EnrollCommand) (affiliates.Enrollment, error) {
	if err := strongauth.Require(command.Session, command.UserID, s.clock.Now()); err != nil {
		return affiliates.Enrollment{}, err
	}
	if command.AcceptedTermsVersion != s.termsVersion {
		return affiliates.Enrollment{}, ErrTermsRequired
	}
	if command.SettlementAccountID != "" {
		allowed, err := s.repository.CanSettleToAccount(ctx, command.UserID, command.SettlementAccountID)
		if err != nil {
			return affiliates.Enrollment{}, err
		}
		if !allowed {
			return affiliates.Enrollment{}, ErrSettlementAccountDenied
		}
	}
	code, err := s.codes.NewCode()
	if err != nil {
		return affiliates.Enrollment{}, err
	}
	enrollment, err := affiliates.NewEnrollment(ids.AffiliateID(s.ids.New()), command.UserID, command.SettlementAccountID, code, s.termsVersion, s.ruleVersion, s.clock.Now())
	if err != nil {
		return affiliates.Enrollment{}, err
	}
	if err := s.repository.CreateEnrollment(ctx, enrollment); err != nil {
		return affiliates.Enrollment{}, err
	}
	return enrollment, nil
}

func (s *Service) Current(ctx context.Context, userID ids.UserID) (affiliates.Enrollment, error) {
	if ids.Validate(string(userID)) != nil {
		return affiliates.Enrollment{}, affiliates.ErrInvalidEnrollment
	}
	return s.repository.EnrollmentByUser(ctx, userID)
}

type ReplaceCodeCommand struct {
	UserID          ids.UserID
	Session         sessions.Session
	ExpectedVersion uint64
}

func (s *Service) ReplaceCode(ctx context.Context, command ReplaceCodeCommand) (affiliates.Enrollment, error) {
	if err := strongauth.Require(command.Session, command.UserID, s.clock.Now()); err != nil {
		return affiliates.Enrollment{}, err
	}
	if command.ExpectedVersion == 0 {
		return affiliates.Enrollment{}, affiliates.ErrInvalidEnrollment
	}
	enrollment, err := s.repository.EnrollmentByUser(ctx, command.UserID)
	if err != nil {
		return affiliates.Enrollment{}, err
	}
	if enrollment.State != affiliates.EnrollmentActive {
		return affiliates.Enrollment{}, ErrEnrollmentState
	}
	if enrollment.Version != command.ExpectedVersion {
		return affiliates.Enrollment{}, ErrEnrollmentConflict
	}
	code, err := s.codes.NewCode()
	if err != nil {
		return affiliates.Enrollment{}, err
	}
	if _, err := enrollment.ReplacePublicCode(code); err != nil {
		return affiliates.Enrollment{}, err
	}
	return s.repository.ReplaceEnrollmentCode(ctx, command.UserID, command.ExpectedVersion, code, s.clock.Now())
}

type ReserveCommand struct {
	PublicCode        string
	ReferredAccountID ids.AccountID
	CheckoutRequestID string
	OfferCode         string
	OfferVersion      uint64
}

func (s *Service) Reserve(ctx context.Context, command ReserveCommand) (affiliates.Attribution, error) {
	enrollment, err := s.repository.EnrollmentByCode(ctx, affiliates.NormalizeCode(command.PublicCode))
	if err != nil {
		return affiliates.Attribution{}, ErrCodeUnavailable
	}
	if existing, lookupErr := s.repository.AttributionByCheckoutRequest(ctx, command.CheckoutRequestID); lookupErr == nil {
		if existing.AffiliateID != enrollment.ID || existing.ReferredAccountID != command.ReferredAccountID ||
			existing.OfferCode != command.OfferCode || existing.OfferVersion != command.OfferVersion {
			return affiliates.Attribution{}, ErrAttributionConflict
		}
		return existing, nil
	} else if !errors.Is(lookupErr, ErrAttributionNotFound) {
		return affiliates.Attribution{}, lookupErr
	}
	if enrollment.TermsVersion != s.termsVersion || enrollment.RuleVersion != s.ruleVersion {
		return affiliates.Attribution{}, ErrTermsRequired
	}
	selfReferral, err := s.repository.CanSettleToAccount(ctx, enrollment.UserID, command.ReferredAccountID)
	if err != nil {
		return affiliates.Attribution{}, err
	}
	if selfReferral {
		return affiliates.Attribution{}, affiliates.ErrSelfReferral
	}
	rule, err := s.repository.CommissionRule(ctx, enrollment.RuleVersion)
	if err != nil || rule.OfferCode != command.OfferCode || rule.EffectiveFrom.After(s.clock.Now()) {
		return affiliates.Attribution{}, ErrProgramUnavailable
	}
	attribution, err := affiliates.NewAttribution(ids.ReferralAttributionID(s.ids.New()), enrollment, command.ReferredAccountID, command.CheckoutRequestID, command.OfferCode, command.OfferVersion, s.clock.Now())
	if err != nil {
		return affiliates.Attribution{}, err
	}
	if err := s.repository.CreateAttribution(ctx, attribution); err != nil {
		return affiliates.Attribution{}, err
	}
	return attribution, nil
}

// ReserveCheckout is the narrow commercial-access adapter. It keeps the
// checkout service independent from Affiliate application DTOs.
func (s *Service) ReserveCheckout(ctx context.Context, publicCode string, accountID ids.AccountID, requestID, offerCode string, offerVersion uint64) (ids.ReferralAttributionID, error) {
	value, err := s.Reserve(ctx, ReserveCommand{PublicCode: publicCode, ReferredAccountID: accountID,
		CheckoutRequestID: requestID, OfferCode: offerCode, OfferVersion: offerVersion})
	return value.ID, err
}

func (s *Service) CheckoutAttribution(ctx context.Context, requestID string) (ids.ReferralAttributionID, bool, error) {
	value, err := s.repository.AttributionByCheckoutRequest(ctx, requestID)
	if errors.Is(err, ErrAttributionNotFound) {
		return "", false, nil
	}
	return value.ID, err == nil, err
}

func (s *Service) Lock(ctx context.Context, attributionID ids.ReferralAttributionID, subscriptionID string) (affiliates.Attribution, error) {
	if ids.Validate(string(attributionID)) != nil {
		return affiliates.Attribution{}, affiliates.ErrInvalidAttribution
	}
	return s.repository.LockAttribution(ctx, attributionID, subscriptionID, s.clock.Now())
}

type PaidInvoice struct {
	SubscriptionID   string
	InvoiceID        string
	PaymentIntentID  string
	PaymentIntentIDs []string
	AmountPaidMinor  int64
	Currency         string
	Initial          bool
	Mode             string
	OccurredAt       time.Time
	Lines            []InvoiceLine
}

type InvoiceLine struct {
	ID              string
	ProviderPriceID string
	AmountMinor     int64
	Currency        string
}

func (s *Service) RecordPaidInvoice(ctx context.Context, paid PaidInvoice) (affiliates.CommissionEntry, error) {
	attribution, err := s.repository.AttributionBySubscription(ctx, paid.SubscriptionID)
	if err != nil {
		return affiliates.CommissionEntry{}, err
	}
	rule, err := s.repository.CommissionRule(ctx, attribution.RuleVersion)
	if err != nil {
		return affiliates.CommissionEntry{}, err
	}
	if strings.ToUpper(paid.Currency) != rule.Currency || paid.AmountPaidMinor <= 0 || (paid.Mode != "test" && paid.Mode != "live") {
		return affiliates.CommissionEntry{}, ErrInvoiceIneligible
	}
	eligibleLines, eligibleMinor, err := s.repository.EligibleInvoiceLines(ctx, attribution, paid.Mode, paid.Lines)
	if err != nil || eligibleMinor <= 0 {
		return affiliates.CommissionEntry{}, ErrInvoiceIneligible
	}
	commissionMinor, err := rule.CommissionAmount(eligibleMinor)
	if err != nil {
		return affiliates.CommissionEntry{}, ErrInvoiceIneligible
	}
	stored, err := s.repository.RecordPaidCommission(ctx, ids.CommissionEntryID(s.ids.New()), ids.CommissionEntryID(s.ids.New()), ids.CommissionEntryID(s.ids.New()),
		attribution, rule, paid, eligibleLines, commissionMinor, s.clock.Now())
	if err != nil {
		return affiliates.CommissionEntry{}, err
	}
	return stored, nil
}

func (s *Service) RecordSubscriptionTermination(ctx context.Context, subscriptionID string, occurredAt time.Time) (affiliates.CommissionEntry, bool, error) {
	if !strings.HasPrefix(subscriptionID, "sub_") || occurredAt.IsZero() {
		return affiliates.CommissionEntry{}, false, affiliates.ErrInvalidCommission
	}
	return s.repository.RecordSubscriptionTermination(ctx, subscriptionID, ids.CommissionEntryID(s.ids.New()), occurredAt.UTC())
}

// RecordAdverseBilling projects only verified Stripe refund/dispute evidence.
// A reversal is appended once cumulative refunds or a lost dispute reaches the
// original eligible invoice amount; partial adverse events remain evidence but
// do not partially debit the fixed commission.
func (s *Service) RecordAdverseBilling(ctx context.Context, evidence affiliates.AdverseBillingEvidence) (affiliates.CommissionEntry, bool, error) {
	if evidence.Validate() != nil {
		return affiliates.CommissionEntry{}, false, affiliates.ErrInvalidAdverse
	}
	return s.repository.RecordAdverseCommission(ctx, ids.CommissionEntryID(s.ids.New()), evidence, s.clock.Now())
}

type Statement struct {
	AffiliateID           ids.AffiliateID              `json:"affiliate_id"`
	ReferredSubscriptions uint64                       `json:"referred_subscriptions"`
	Currency              string                       `json:"currency"`
	PendingMinor          int64                        `json:"pending_minor"`
	AvailableMinor        int64                        `json:"available_minor"`
	ReservedMinor         int64                        `json:"reserved_minor"`
	SettledMinor          int64                        `json:"settled_minor"`
	ReversedMinor         int64                        `json:"reversed_minor"`
	VoidedMinor           int64                        `json:"voided_minor"`
	CheckThresholdMinor   int64                        `json:"check_threshold_minor"`
	CheckEligible         bool                         `json:"check_eligible"`
	Entries               []affiliates.CommissionEntry `json:"entries"`
}

type SettlementSnapshot struct {
	AvailableMinor      int64
	ReservedMinor       int64
	SettledMinor        int64
	CheckThresholdMinor int64
}

func (s *Service) Statement(ctx context.Context, userID ids.UserID) (Statement, error) {
	enrollment, err := s.Current(ctx, userID)
	if err != nil {
		return Statement{}, err
	}
	referredSubscriptions, entries, err := s.repository.StatementSnapshot(ctx, enrollment.ID)
	if err != nil {
		return Statement{}, err
	}
	statement := Statement{AffiliateID: enrollment.ID, ReferredSubscriptions: referredSubscriptions, Entries: entries}
	matured := make(map[ids.CommissionEntryID]struct{})
	voided := make(map[ids.CommissionEntryID]struct{})
	reversed := make(map[ids.CommissionEntryID]struct{})
	for _, entry := range entries {
		if statement.Currency == "" {
			statement.Currency = entry.Currency
		}
		if statement.Currency != entry.Currency {
			return Statement{}, affiliates.ErrInvalidCommission
		}
		switch entry.Kind {
		case affiliates.CommissionMaturity:
			if entry.SourceID != nil {
				matured[*entry.SourceID] = struct{}{}
			}
		case affiliates.CommissionVoid:
			if entry.SourceID != nil {
				voided[*entry.SourceID] = struct{}{}
			}
			statement.VoidedMinor += entry.AmountMinor
		case affiliates.CommissionReversal:
			if entry.ReversesID != nil {
				reversed[*entry.ReversesID] = struct{}{}
			}
			statement.ReversedMinor += entry.AmountMinor
		}
	}
	for _, entry := range entries {
		if entry.Kind != affiliates.CommissionEarned {
			continue
		}
		if _, exists := reversed[entry.ID]; exists {
			continue
		}
		if _, exists := voided[entry.ID]; exists {
			continue
		}
		if _, exists := matured[entry.ID]; exists {
			statement.AvailableMinor += entry.AmountMinor
		} else {
			statement.PendingMinor += entry.AmountMinor
		}
	}
	settlement, err := s.repository.SettlementSnapshot(ctx, enrollment.ID)
	if err != nil {
		return Statement{}, err
	}
	statement.AvailableMinor = settlement.AvailableMinor
	statement.ReservedMinor = settlement.ReservedMinor
	statement.SettledMinor = settlement.SettledMinor
	statement.CheckThresholdMinor = settlement.CheckThresholdMinor
	statement.CheckEligible = settlement.AvailableMinor >= settlement.CheckThresholdMinor && settlement.CheckThresholdMinor > 0
	return statement, nil
}

func (s *Service) TermsVersion() uint64 { return s.termsVersion }
func (s *Service) RuleVersion() uint64  { return s.ruleVersion }

type RandomCodeGenerator struct{}

func (RandomCodeGenerator) NewCode() (string, error) {
	var value [7]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return "IO-" + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(value[:]), nil
}

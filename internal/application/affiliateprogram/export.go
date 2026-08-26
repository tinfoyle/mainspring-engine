package affiliateprogram

import (
	"context"
	"errors"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/affiliates"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const AffiliateDataExportSchemaVersion uint64 = 1

// DataExport is the customer-owned Affiliate portability artifact. Its shape is
// deliberately separate from the operational models: referred-customer,
// payment-provider, staff-actor, and free-form reason fields cannot enter it.
type DataExport struct {
	SchemaVersion      uint64                     `json:"schema_version"`
	GeneratedAt        time.Time                  `json:"generated_at"`
	Enrollment         *DataExportEnrollment      `json:"enrollment,omitempty"`
	PublicCodes        []DataExportPublicCode     `json:"public_codes"`
	EnrollmentEvents   []DataExportLifecycleEvent `json:"enrollment_events"`
	AttributionSummary DataExportAttribution      `json:"attribution_summary"`
	CommissionEntries  []DataExportCommission     `json:"commission_entries"`
	SupportRequests    []DataExportSupportRequest `json:"support_requests"`
	SupportEvents      []DataExportSupportEvent   `json:"support_events"`
}

type DataExportEnrollment struct {
	AffiliateID         ids.AffiliateID            `json:"affiliate_id"`
	UserID              ids.UserID                 `json:"user_id"`
	SettlementAccountID ids.AccountID              `json:"settlement_account_id,omitempty"`
	PublicCode          string                     `json:"public_code"`
	TermsVersion        uint64                     `json:"terms_version"`
	RuleVersion         uint64                     `json:"rule_version"`
	State               affiliates.EnrollmentState `json:"state"`
	Version             uint64                     `json:"version"`
	CreatedAt           time.Time                  `json:"created_at"`
	UpdatedAt           time.Time                  `json:"updated_at"`
}

type DataExportPublicCode struct {
	PublicCode        string     `json:"public_code"`
	EnrollmentVersion uint64     `json:"enrollment_version"`
	ActivatedAt       time.Time  `json:"activated_at"`
	ReplacedAt        *time.Time `json:"replaced_at,omitempty"`
}

type DataExportLifecycleEvent struct {
	EventID    string                     `json:"event_id"`
	Version    uint64                     `json:"version"`
	Action     string                     `json:"action"`
	State      affiliates.EnrollmentState `json:"state"`
	OccurredAt time.Time                  `json:"occurred_at"`
}

type DataExportAttribution struct {
	Total      uint64     `json:"total"`
	Reserved   uint64     `json:"reserved"`
	Locked     uint64     `json:"locked"`
	Canceled   uint64     `json:"canceled"`
	EarliestAt *time.Time `json:"earliest_at,omitempty"`
	LatestAt   *time.Time `json:"latest_at,omitempty"`
}

type DataExportCommission struct {
	EntryID         ids.CommissionEntryID      `json:"entry_id"`
	RuleVersion     uint64                     `json:"rule_version"`
	Cycle           uint32                     `json:"cycle"`
	Kind            affiliates.CommissionKind  `json:"kind"`
	State           affiliates.CommissionState `json:"state"`
	AmountMinor     int64                      `json:"amount_minor"`
	Currency        string                     `json:"currency"`
	ReversesEntryID ids.CommissionEntryID      `json:"reverses_entry_id,omitempty"`
	AvailableAt     time.Time                  `json:"available_at"`
	CreatedAt       time.Time                  `json:"created_at"`
}

type DataExportSupportRequest struct {
	RequestID         ids.AffiliateSupportRequestID `json:"request_id"`
	Kind              affiliates.SupportKind        `json:"kind"`
	CommissionEntryID ids.CommissionEntryID         `json:"commission_entry_id,omitempty"`
	State             affiliates.SupportState       `json:"state"`
	Outcome           affiliates.SupportOutcome     `json:"outcome,omitempty"`
	Version           uint64                        `json:"version"`
	CreatedAt         time.Time                     `json:"created_at"`
	UpdatedAt         time.Time                     `json:"updated_at"`
}

type DataExportSupportEvent struct {
	EventID    ids.AffiliateSupportEventID   `json:"event_id"`
	RequestID  ids.AffiliateSupportRequestID `json:"request_id"`
	Version    uint64                        `json:"version"`
	Action     string                        `json:"action"`
	State      affiliates.SupportState       `json:"state"`
	Outcome    affiliates.SupportOutcome     `json:"outcome,omitempty"`
	OccurredAt time.Time                     `json:"occurred_at"`
}

type ExportCommand struct {
	UserID  ids.UserID
	Session sessions.Session
}

func (s *Service) Export(ctx context.Context, command ExportCommand) (DataExport, error) {
	if err := strongauth.Require(command.Session, command.UserID, s.clock.Now()); err != nil {
		return DataExport{}, err
	}
	value, err := s.repository.DataExport(ctx, command.UserID)
	if err != nil {
		return DataExport{}, err
	}
	if value.SchemaVersion != AffiliateDataExportSchemaVersion || value.GeneratedAt.IsZero() ||
		value.PublicCodes == nil || value.EnrollmentEvents == nil || value.CommissionEntries == nil ||
		value.SupportRequests == nil || value.SupportEvents == nil {
		return DataExport{}, errors.New("invalid Affiliate data export")
	}
	if value.Enrollment != nil && value.Enrollment.UserID != command.UserID {
		return DataExport{}, errors.New("Affiliate data export identity mismatch")
	}
	return value, nil
}

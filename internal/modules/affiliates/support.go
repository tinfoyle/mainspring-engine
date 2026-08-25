package affiliates

import (
	"errors"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type SupportKind string
type SupportState string
type SupportOutcome string

const (
	SupportEnrollmentAppeal SupportKind = "enrollment_appeal"
	SupportCommissionReview SupportKind = "commission_review"

	SupportSubmitted SupportState = "submitted"
	SupportInReview  SupportState = "in_review"
	SupportResolved  SupportState = "resolved"
	SupportDeclined  SupportState = "declined"
	SupportCanceled  SupportState = "canceled"

	SupportApproved SupportOutcome = "approved"
	SupportDenied   SupportOutcome = "denied"
)

var ErrInvalidSupportRequest = errors.New("Affiliate support request is invalid")

// SupportRequest is intentionally structured and content-free. It gives an
// Affiliate a review/appeal channel without collecting customer names, free
// text, payment data, or details about a referred business.
type SupportRequest struct {
	ID                ids.AffiliateSupportRequestID `json:"request_id"`
	AffiliateID       ids.AffiliateID               `json:"affiliate_id"`
	UserID            ids.UserID                    `json:"-"`
	Kind              SupportKind                   `json:"kind"`
	CommissionEntryID ids.CommissionEntryID         `json:"commission_entry_id,omitempty"`
	State             SupportState                  `json:"state"`
	Outcome           SupportOutcome                `json:"outcome,omitempty"`
	Version           uint64                        `json:"version"`
	CreatedAt         time.Time                     `json:"created_at"`
	UpdatedAt         time.Time                     `json:"updated_at"`
}

func NewSupportRequest(id ids.AffiliateSupportRequestID, enrollment Enrollment, kind SupportKind, entryID ids.CommissionEntryID, now time.Time) (SupportRequest, error) {
	value := SupportRequest{ID: id, AffiliateID: enrollment.ID, UserID: enrollment.UserID, Kind: kind,
		CommissionEntryID: entryID, State: SupportSubmitted, Version: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
	if enrollment.Validate() != nil || value.Validate() != nil {
		return SupportRequest{}, ErrInvalidSupportRequest
	}
	return value, nil
}

func (r SupportRequest) Validate() error {
	if ids.Validate(string(r.ID)) != nil || ids.Validate(string(r.AffiliateID)) != nil || ids.Validate(string(r.UserID)) != nil ||
		r.Version == 0 || r.CreatedAt.IsZero() || r.UpdatedAt.IsZero() || r.UpdatedAt.Before(r.CreatedAt) {
		return ErrInvalidSupportRequest
	}
	switch r.Kind {
	case SupportEnrollmentAppeal:
		if r.CommissionEntryID != "" {
			return ErrInvalidSupportRequest
		}
	case SupportCommissionReview:
		if ids.Validate(string(r.CommissionEntryID)) != nil {
			return ErrInvalidSupportRequest
		}
	default:
		return ErrInvalidSupportRequest
	}
	switch r.State {
	case SupportSubmitted, SupportInReview, SupportCanceled:
		if r.Outcome != "" {
			return ErrInvalidSupportRequest
		}
	case SupportResolved:
		if r.Outcome != SupportApproved {
			return ErrInvalidSupportRequest
		}
	case SupportDeclined:
		if r.Outcome != SupportDenied {
			return ErrInvalidSupportRequest
		}
	default:
		return ErrInvalidSupportRequest
	}
	return nil
}

func (r SupportRequest) Cancel(now time.Time) (SupportRequest, error) {
	if r.Validate() != nil || r.State != SupportSubmitted || now.IsZero() || now.Before(r.UpdatedAt) {
		return SupportRequest{}, ErrInvalidSupportRequest
	}
	r.State, r.Version, r.UpdatedAt = SupportCanceled, r.Version+1, now.UTC()
	return r, nil
}

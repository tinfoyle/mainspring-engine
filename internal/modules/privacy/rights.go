package privacy

import (
	"errors"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type RightsKind string
type RightsScope string
type RightsState string

const (
	RightsAccess      RightsKind = "access"
	RightsCorrection  RightsKind = "correction"
	RightsErasure     RightsKind = "erasure"
	RightsRestriction RightsKind = "restriction"
	RightsObjection   RightsKind = "objection"
	RightsPortability RightsKind = "portability"

	RightsIdentity  RightsScope = "identity"
	RightsAccount   RightsScope = "account"
	RightsAffiliate RightsScope = "affiliate"
	RightsAnalytics RightsScope = "analytics"

	RightsSubmitted          RightsState = "submitted"
	RightsInReview           RightsState = "in_review"
	RightsCompleted          RightsState = "completed"
	RightsPartiallyCompleted RightsState = "partially_completed"
	RightsDeclined           RightsState = "declined"
	RightsCanceled           RightsState = "canceled"
)

var ErrInvalidRightsRequest = errors.New("privacy rights request is invalid")

type RightsRequest struct {
	ID            ids.PrivacyRightsRequestID `json:"request_id"`
	UserID        ids.UserID                 `json:"-"`
	Version       uint64                     `json:"-"`
	Kind          RightsKind                 `json:"kind"`
	Scope         RightsScope                `json:"scope"`
	State         RightsState                `json:"state"`
	VerifiedAt    time.Time                  `json:"verified_at"`
	RequestedAt   time.Time                  `json:"requested_at"`
	ResponseDueAt time.Time                  `json:"response_due_at"`
	UpdatedAt     time.Time                  `json:"updated_at"`
}

func NewRightsRequest(id ids.PrivacyRightsRequestID, userID ids.UserID, kind RightsKind, scope RightsScope, now time.Time) (RightsRequest, error) {
	request := RightsRequest{ID: id, UserID: userID, Version: 1, Kind: kind, Scope: scope, State: RightsSubmitted,
		VerifiedAt: now.UTC(), RequestedAt: now.UTC(), ResponseDueAt: now.UTC().AddDate(0, 1, 0), UpdatedAt: now.UTC()}
	if err := request.Validate(); err != nil {
		return RightsRequest{}, err
	}
	return request, nil
}

func (r RightsRequest) Validate() error {
	if ids.Validate(string(r.ID)) != nil || ids.Validate(string(r.UserID)) != nil || r.Version == 0 || !validRightsKind(r.Kind) || !validRightsScope(r.Scope) ||
		!validRightsState(r.State) || r.VerifiedAt.IsZero() || r.RequestedAt.IsZero() || r.ResponseDueAt.IsZero() || r.UpdatedAt.IsZero() ||
		r.VerifiedAt.After(r.RequestedAt) || !r.ResponseDueAt.After(r.RequestedAt) || r.UpdatedAt.Before(r.RequestedAt) {
		return ErrInvalidRightsRequest
	}
	return nil
}

func validRightsKind(kind RightsKind) bool {
	switch kind {
	case RightsAccess, RightsCorrection, RightsErasure, RightsRestriction, RightsObjection, RightsPortability:
		return true
	default:
		return false
	}
}

func validRightsScope(scope RightsScope) bool {
	switch scope {
	case RightsIdentity, RightsAccount, RightsAffiliate, RightsAnalytics:
		return true
	default:
		return false
	}
}

func validRightsState(state RightsState) bool {
	switch state {
	case RightsSubmitted, RightsInReview, RightsCompleted, RightsPartiallyCompleted, RightsDeclined, RightsCanceled:
		return true
	default:
		return false
	}
}

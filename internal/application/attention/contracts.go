// Package attention defines the transport-neutral command/query persistence
// boundary for the customer-visible Attention aggregates.
package attention

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	domain "github.com/tinfoyle/spyglass-engine/internal/modules/attention"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const MaxPageSize = 100

var (
	ErrInvalidCommand = errors.New("attention command is invalid")
	ErrNotFound       = errors.New("attention aggregate not found")
	ErrConflict       = errors.New("attention aggregate version conflict")
	ErrConstraint     = errors.New("attention aggregate constraint failed")
	ErrCorrupt        = errors.New("attention persistence is corrupt")
	reasonCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,99}$`)
)

type Mutation struct {
	Actor         domain.Actor
	ReasonCode    string
	CorrelationID string
	At            time.Time
}

func (mutation Mutation) Valid() bool {
	return mutation.Actor.Valid() && (mutation.ReasonCode == "" || reasonCodePattern.MatchString(mutation.ReasonCode)) &&
		len(strings.TrimSpace(mutation.CorrelationID)) >= 1 && len(strings.TrimSpace(mutation.CorrelationID)) <= 200 && !mutation.At.IsZero()
}

type InformationListQuery struct {
	State          domain.InformationRequestState
	ParentWorkItem ids.WorkItemID
	AfterUpdatedAt *time.Time
	AfterID        ids.InformationRequestID
	Limit          int
}

type WorkReviewListQuery struct {
	State          domain.WorkReviewState
	ReviewerID     ids.UserID
	WorkItemID     ids.WorkItemID
	AfterUpdatedAt *time.Time
	AfterID        ids.WorkReviewID
	Limit          int
}

type ApprovalListQuery struct {
	State          domain.ConsequentialApprovalState
	WorkItemID     ids.WorkItemID
	AfterUpdatedAt *time.Time
	AfterID        ids.ConsequentialApprovalID
	Limit          int
}

type InformationPage struct {
	Items      []domain.InformationRequest
	NextCursor *InformationCursor
}
type InformationCursor struct {
	UpdatedAt time.Time
	ID        ids.InformationRequestID
}
type WorkReviewPage struct {
	Items      []domain.WorkReview
	NextCursor *WorkReviewCursor
}
type WorkReviewCursor struct {
	UpdatedAt time.Time
	ID        ids.WorkReviewID
}
type ApprovalPage struct {
	Items      []domain.ConsequentialApproval
	NextCursor *ApprovalCursor
}
type ApprovalCursor struct {
	UpdatedAt time.Time
	ID        ids.ConsequentialApprovalID
}

type Repository interface {
	CreateInformation(context.Context, domain.InformationRequest, Mutation) (domain.InformationRequest, error)
	GetInformation(context.Context, ids.AccountID, ids.InformationRequestID) (domain.InformationRequest, error)
	UpdateInformation(context.Context, domain.InformationRequest, uint64, Mutation) (domain.InformationRequest, error)
	ListInformation(context.Context, ids.AccountID, InformationListQuery) (InformationPage, error)

	CreateWorkReview(context.Context, domain.WorkReview, Mutation) (domain.WorkReview, error)
	GetWorkReview(context.Context, ids.AccountID, ids.WorkReviewID) (domain.WorkReview, error)
	UpdateWorkReview(context.Context, domain.WorkReview, uint64, Mutation) (domain.WorkReview, error)
	ListWorkReviews(context.Context, ids.AccountID, WorkReviewListQuery) (WorkReviewPage, error)

	CreateApproval(context.Context, domain.ConsequentialApproval, Mutation) (domain.ConsequentialApproval, error)
	GetApproval(context.Context, ids.AccountID, ids.ConsequentialApprovalID) (domain.ConsequentialApproval, error)
	UpdateApproval(context.Context, domain.ConsequentialApproval, uint64, Mutation) (domain.ConsequentialApproval, error)
	ListApprovals(context.Context, ids.AccountID, ApprovalListQuery) (ApprovalPage, error)
}

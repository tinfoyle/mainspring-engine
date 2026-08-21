package attention

import (
	"crypto/sha256"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type WorkReviewState string
type WorkReviewDecision string

const (
	WorkReviewOpen             WorkReviewState = "open"
	WorkReviewApproved         WorkReviewState = "approved"
	WorkReviewChangesRequested WorkReviewState = "changes_requested"
	WorkReviewCanceled         WorkReviewState = "canceled"
	WorkReviewInvalidated      WorkReviewState = "invalidated"

	ReviewApprove        WorkReviewDecision = "approve"
	ReviewRequestChanges WorkReviewDecision = "request_changes"
)

type ReviewDecisionRecord struct {
	Decision  WorkReviewDecision
	Reason    string
	DecidedBy Actor
	DecidedAt time.Time
}

type WorkReviewDraft struct {
	ID             ids.WorkReviewID
	AccountID      ids.AccountID
	WorkItemID     ids.WorkItemID
	WorkVersion    uint64
	ProposalSHA256 [sha256.Size]byte
	Question       string
	RequestedBy    Actor
	ReviewerID     ids.UserID
}

type WorkReview struct {
	WorkReviewDraft
	State         WorkReviewState
	Decision      *ReviewDecisionRecord
	CanceledBy    *Actor
	CancelReason  string
	InvalidatedAt *time.Time
	Version       uint64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func NewWorkReview(draft WorkReviewDraft, now time.Time) (WorkReview, error) {
	draft.Question = strings.TrimSpace(draft.Question)
	review := WorkReview{WorkReviewDraft: draft, State: WorkReviewOpen, Version: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
	return RestoreWorkReview(review)
}

func RestoreWorkReview(review WorkReview) (WorkReview, error) {
	review.Question = strings.TrimSpace(review.Question)
	review.CancelReason = strings.TrimSpace(review.CancelReason)
	review.CreatedAt, review.UpdatedAt = review.CreatedAt.UTC(), review.UpdatedAt.UTC()
	if !validID(string(review.ID)) || !validID(string(review.AccountID)) || !validID(string(review.WorkItemID)) || review.WorkVersion == 0 ||
		review.ProposalSHA256 == ([sha256.Size]byte{}) || len(review.Question) < 3 || len(review.Question) > 4000 || !review.RequestedBy.Valid() ||
		!validID(string(review.ReviewerID)) || review.Version == 0 || review.CreatedAt.IsZero() || review.UpdatedAt.Before(review.CreatedAt) {
		return WorkReview{}, ErrInvalid
	}
	if review.Decision != nil {
		decision := *review.Decision
		decision.Reason = strings.TrimSpace(decision.Reason)
		decision.DecidedAt = decision.DecidedAt.UTC()
		if (decision.Decision != ReviewApprove && decision.Decision != ReviewRequestChanges) || !validReason(decision.Reason) ||
			!decision.DecidedBy.Valid() || decision.DecidedBy.Kind != ActorUser || decision.DecidedBy.ID != string(review.ReviewerID) || decision.DecidedAt.Before(review.CreatedAt) {
			return WorkReview{}, ErrInvalid
		}
		review.Decision = &decision
	}
	switch review.State {
	case WorkReviewOpen:
		if review.Decision != nil || review.CanceledBy != nil || review.CancelReason != "" || review.InvalidatedAt != nil {
			return WorkReview{}, ErrInvalid
		}
	case WorkReviewApproved:
		if review.Decision == nil || review.Decision.Decision != ReviewApprove || review.CanceledBy != nil || review.CancelReason != "" || review.InvalidatedAt != nil {
			return WorkReview{}, ErrInvalid
		}
	case WorkReviewChangesRequested:
		if review.Decision == nil || review.Decision.Decision != ReviewRequestChanges || review.CanceledBy != nil || review.CancelReason != "" || review.InvalidatedAt != nil {
			return WorkReview{}, ErrInvalid
		}
	case WorkReviewCanceled:
		if review.Decision != nil || review.CanceledBy == nil || !review.CanceledBy.Valid() || !validReason(review.CancelReason) || review.InvalidatedAt != nil {
			return WorkReview{}, ErrInvalid
		}
	case WorkReviewInvalidated:
		if review.CanceledBy != nil || review.CancelReason != "" || review.InvalidatedAt == nil || review.InvalidatedAt.Before(review.CreatedAt) {
			return WorkReview{}, ErrInvalid
		}
		value := review.InvalidatedAt.UTC()
		review.InvalidatedAt = &value
	default:
		return WorkReview{}, ErrInvalid
	}
	return review, nil
}

type DecideWorkReviewCommand struct {
	Decision        WorkReviewDecision
	Reason          string
	Role            accounts.MembershipRole
	Actor           Actor
	ExpectedVersion uint64
	At              time.Time
}

func (review WorkReview) Decide(command DecideWorkReviewCommand) (WorkReview, error) {
	if command.ExpectedVersion != review.Version {
		return WorkReview{}, ErrConflict
	}
	if review.State != WorkReviewOpen || !command.Actor.Valid() || command.Actor.Kind != ActorUser || command.At.IsZero() ||
		(command.Decision != ReviewApprove && command.Decision != ReviewRequestChanges) {
		return WorkReview{}, ErrState
	}
	if !canParticipate(command.Role) {
		return WorkReview{}, ErrRole
	}
	if command.Actor.ID != string(review.ReviewerID) {
		return WorkReview{}, ErrReviewer
	}
	if !validReason(command.Reason) {
		return WorkReview{}, ErrReasonRequired
	}
	result := review
	if command.Decision == ReviewApprove {
		result.State = WorkReviewApproved
	} else {
		result.State = WorkReviewChangesRequested
	}
	result.Decision = &ReviewDecisionRecord{Decision: command.Decision, Reason: strings.TrimSpace(command.Reason), DecidedBy: command.Actor, DecidedAt: command.At.UTC()}
	result.Version++
	result.UpdatedAt = command.At.UTC()
	return RestoreWorkReview(result)
}

type ReconcileWorkReviewCommand struct {
	WorkVersion     uint64
	ProposalSHA256  [sha256.Size]byte
	ExpectedVersion uint64
	At              time.Time
}

func (review WorkReview) ReconcileProposal(command ReconcileWorkReviewCommand) (WorkReview, error) {
	if command.ExpectedVersion != review.Version {
		return WorkReview{}, ErrConflict
	}
	if command.WorkVersion == 0 || command.ProposalSHA256 == ([sha256.Size]byte{}) || command.At.IsZero() || review.State == WorkReviewCanceled || review.State == WorkReviewInvalidated {
		return WorkReview{}, ErrState
	}
	if command.WorkVersion == review.WorkVersion && command.ProposalSHA256 == review.ProposalSHA256 {
		return review, nil
	}
	result := review
	result.State = WorkReviewInvalidated
	value := command.At.UTC()
	result.InvalidatedAt = &value
	result.Version++
	result.UpdatedAt = value
	return RestoreWorkReview(result)
}

type CancelWorkReviewCommand struct {
	Role            accounts.MembershipRole
	Actor           Actor
	Reason          string
	ExpectedVersion uint64
	At              time.Time
}

func (review WorkReview) Cancel(command CancelWorkReviewCommand) (WorkReview, error) {
	if command.ExpectedVersion != review.Version {
		return WorkReview{}, ErrConflict
	}
	if review.State != WorkReviewOpen || !command.Actor.Valid() || command.At.IsZero() {
		return WorkReview{}, ErrState
	}
	if !canManage(command.Role) && !(canParticipate(command.Role) && sameActor(command.Actor, review.RequestedBy)) {
		return WorkReview{}, ErrRole
	}
	if !validReason(command.Reason) {
		return WorkReview{}, ErrReasonRequired
	}
	actor := command.Actor
	result := review
	result.State = WorkReviewCanceled
	result.CanceledBy = &actor
	result.CancelReason = strings.TrimSpace(command.Reason)
	result.Version++
	result.UpdatedAt = command.At.UTC()
	return RestoreWorkReview(result)
}

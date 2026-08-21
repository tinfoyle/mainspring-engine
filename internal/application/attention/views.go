package attention

import (
	"context"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/attention"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type InformationSummary struct {
	ID               ids.InformationRequestID
	ParentWorkItemID ids.WorkItemID
	Requirement      domain.FactRequirement
	Question         string
	RequestedBy      domain.Actor
	State            domain.InformationRequestState
	AnsweredAt       *time.Time
	Version          uint64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type InformationSummaryPage struct {
	Items      []InformationSummary
	NextCursor *InformationCursor
}

type WorkReviewSummary struct {
	ID          ids.WorkReviewID
	WorkItemID  ids.WorkItemID
	WorkVersion uint64
	Question    string
	RequestedBy domain.Actor
	ReviewerID  ids.UserID
	State       domain.WorkReviewState
	Decision    domain.WorkReviewDecision
	Version     uint64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type WorkReviewSummaryPage struct {
	Items      []WorkReviewSummary
	NextCursor *WorkReviewCursor
}

// ApprovalSummary is safe for queues: it deliberately excludes canonical
// payload bytes, evidence/input digests, and human decision reasons.
type ApprovalSummary struct {
	ID            ids.ConsequentialApprovalID
	WorkItemID    ids.WorkItemID
	OperationID   string
	InvocationID  ids.AgentInvocationID
	Capability    string
	Proposer      domain.Actor
	PolicyVersion uint64
	ExpiresAt     time.Time
	State         domain.ConsequentialApprovalState
	Decision      domain.ApprovalDecision
	Version       uint64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type ApprovalSummaryPage struct {
	Items      []ApprovalSummary
	NextCursor *ApprovalCursor
}

func (s *Service) GetInformation(ctx context.Context, actor access.Actor, accountID ids.AccountID, requestID ids.InformationRequestID) (domain.InformationRequest, error) {
	if !actor.Valid() || ids.Validate(string(accountID)) != nil || ids.Validate(string(requestID)) != nil {
		return domain.InformationRequest{}, ErrInvalidCommand
	}
	if _, err := s.authorize(ctx, actor, accountID, WorkPackage, false); err != nil {
		return domain.InformationRequest{}, err
	}
	return s.repository.GetInformation(ctx, accountID, requestID)
}

func (s *Service) ListInformation(ctx context.Context, actor access.Actor, accountID ids.AccountID, query InformationListQuery) (InformationSummaryPage, error) {
	if !actor.Valid() || ids.Validate(string(accountID)) != nil {
		return InformationSummaryPage{}, ErrInvalidCommand
	}
	if _, err := s.authorize(ctx, actor, accountID, WorkPackage, false); err != nil {
		return InformationSummaryPage{}, err
	}
	if query.Limit <= 0 {
		query.Limit = DefaultLimit
	}
	page, err := s.repository.ListInformation(ctx, accountID, query)
	if err != nil {
		return InformationSummaryPage{}, err
	}
	result := InformationSummaryPage{Items: make([]InformationSummary, 0, len(page.Items)), NextCursor: page.NextCursor}
	for _, item := range page.Items {
		var answeredAt *time.Time
		if item.AnswerRecord != nil {
			value := item.AnswerRecord.AnsweredAt
			answeredAt = &value
		}
		result.Items = append(result.Items, InformationSummary{
			ID: item.ID, ParentWorkItemID: item.ParentWorkItemID, Requirement: item.Requirement, Question: item.Question,
			RequestedBy: item.RequestedBy, State: item.State, AnsweredAt: answeredAt, Version: item.Version,
			CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		})
	}
	return result, nil
}

func (s *Service) GetWorkReview(ctx context.Context, actor access.Actor, accountID ids.AccountID, reviewID ids.WorkReviewID) (domain.WorkReview, error) {
	if !actor.Valid() || ids.Validate(string(accountID)) != nil || ids.Validate(string(reviewID)) != nil {
		return domain.WorkReview{}, ErrInvalidCommand
	}
	if _, err := s.authorize(ctx, actor, accountID, WorkPackage, false); err != nil {
		return domain.WorkReview{}, err
	}
	return s.repository.GetWorkReview(ctx, accountID, reviewID)
}

func (s *Service) ListWorkReviews(ctx context.Context, actor access.Actor, accountID ids.AccountID, query WorkReviewListQuery) (WorkReviewSummaryPage, error) {
	if !actor.Valid() || ids.Validate(string(accountID)) != nil {
		return WorkReviewSummaryPage{}, ErrInvalidCommand
	}
	if _, err := s.authorize(ctx, actor, accountID, WorkPackage, false); err != nil {
		return WorkReviewSummaryPage{}, err
	}
	if query.Limit <= 0 {
		query.Limit = DefaultLimit
	}
	page, err := s.repository.ListWorkReviews(ctx, accountID, query)
	if err != nil {
		return WorkReviewSummaryPage{}, err
	}
	result := WorkReviewSummaryPage{Items: make([]WorkReviewSummary, 0, len(page.Items)), NextCursor: page.NextCursor}
	for _, item := range page.Items {
		var decision domain.WorkReviewDecision
		if item.Decision != nil {
			decision = item.Decision.Decision
		}
		result.Items = append(result.Items, WorkReviewSummary{
			ID: item.ID, WorkItemID: item.WorkItemID, WorkVersion: item.WorkVersion, Question: item.Question,
			RequestedBy: item.RequestedBy, ReviewerID: item.ReviewerID, State: item.State, Decision: decision,
			Version: item.Version, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		})
	}
	return result, nil
}

func (s *Service) GetApproval(ctx context.Context, actor access.Actor, accountID ids.AccountID, approvalID ids.ConsequentialApprovalID) (domain.ConsequentialApproval, error) {
	if !validHuman(actor) || ids.Validate(string(accountID)) != nil || ids.Validate(string(approvalID)) != nil {
		return domain.ConsequentialApproval{}, ErrInvalidCommand
	}
	accountContext, err := s.authorize(ctx, actor, accountID, AgentsPackage, false)
	if err != nil {
		return domain.ConsequentialApproval{}, err
	}
	if !canApprove(accountContext.Role) {
		return domain.ConsequentialApproval{}, roleDenied(AgentsPackage)
	}
	return s.repository.GetApproval(ctx, accountID, approvalID)
}

func (s *Service) ListApprovals(ctx context.Context, actor access.Actor, accountID ids.AccountID, query ApprovalListQuery) (ApprovalSummaryPage, error) {
	if !validHuman(actor) || ids.Validate(string(accountID)) != nil {
		return ApprovalSummaryPage{}, ErrInvalidCommand
	}
	accountContext, err := s.authorize(ctx, actor, accountID, AgentsPackage, false)
	if err != nil {
		return ApprovalSummaryPage{}, err
	}
	if !canApprove(accountContext.Role) {
		return ApprovalSummaryPage{}, roleDenied(AgentsPackage)
	}
	if query.Limit <= 0 {
		query.Limit = DefaultLimit
	}
	page, err := s.repository.ListApprovals(ctx, accountID, query)
	if err != nil {
		return ApprovalSummaryPage{}, err
	}
	result := ApprovalSummaryPage{Items: make([]ApprovalSummary, 0, len(page.Items)), NextCursor: page.NextCursor}
	for _, item := range page.Items {
		var decision domain.ApprovalDecision
		if item.Decision != nil {
			decision = item.Decision.Decision
		}
		result.Items = append(result.Items, ApprovalSummary{
			ID: item.ID, WorkItemID: item.WorkItemID, OperationID: item.OperationID, InvocationID: item.InvocationID,
			Capability: item.Capability, Proposer: item.Proposer, PolicyVersion: item.PolicyVersion, ExpiresAt: item.ExpiresAt,
			State: item.State, Decision: decision, Version: item.Version, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		})
	}
	return result, nil
}

func canApprove(role accounts.MembershipRole) bool {
	return role == accounts.RoleOwner || role == accounts.RoleAdministrator
}

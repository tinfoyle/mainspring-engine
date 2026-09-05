// Package attention provides the transport-neutral, package-authorized
// command and query boundary for customer-visible Attention.
package attention

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"

	workapp "github.com/tinfoyle/spyglass-engine/internal/application/work"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/attention"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	workdomain "github.com/tinfoyle/spyglass-engine/internal/modules/work"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	WorkPackage   catalog.PackageCode = catalog.PackageWork
	AgentsPackage catalog.PackageCode = catalog.PackageAgents
	DefaultLimit                      = 50
)

type Authorizer interface {
	Authorize(context.Context, access.Actor, ids.AccountID, access.Requirement) (access.AccountContext, error)
}

type Clock interface{ Now() time.Time }

// ReviewerDirectory resolves only active Account Membership eligibility. It
// prevents a Work review from being assigned to an arbitrary system-wide User
// identifier that cannot ever decide it.
type ReviewerDirectory interface {
	ActiveRole(context.Context, ids.AccountID, ids.UserID) (accounts.MembershipRole, bool, error)
}

type WorkResumer interface {
	ResumeAttentionParents(context.Context, workapp.ResumeAttentionCommand) ([]workdomain.Item, error)
}

type Service struct {
	authorizer        Authorizer
	reviewerDirectory ReviewerDirectory
	repository        Repository
	clock             Clock
	workResumer       WorkResumer
}

type Option func(*Service)

func WithWorkResumer(resumer WorkResumer) Option {
	return func(service *Service) { service.workResumer = resumer }
}

func NewService(authorizer Authorizer, reviewerDirectory ReviewerDirectory, repository Repository, clock Clock, options ...Option) (*Service, error) {
	if authorizer == nil || reviewerDirectory == nil || repository == nil || clock == nil {
		return nil, errors.New("attention service dependencies are required")
	}
	service := &Service{authorizer: authorizer, reviewerDirectory: reviewerDirectory, repository: repository, clock: clock}
	for _, option := range options {
		option(service)
	}
	return service, nil
}

type CreateInformationCommand struct {
	Actor            access.Actor
	AccountID        ids.AccountID
	RequestID        ids.InformationRequestID
	ParentWorkItemID ids.WorkItemID
	Requirement      domain.FactRequirement
	Question         string
	CorrelationID    string
}

func (s *Service) CreateInformation(ctx context.Context, command CreateInformationCommand) (domain.InformationRequest, error) {
	if !validBase(command.Actor, command.AccountID, command.CorrelationID) || ids.Validate(string(command.RequestID)) != nil || ids.Validate(string(command.ParentWorkItemID)) != nil {
		return domain.InformationRequest{}, ErrInvalidCommand
	}
	accountContext, err := s.authorize(ctx, command.Actor, command.AccountID, WorkPackage, true)
	if err != nil {
		return domain.InformationRequest{}, err
	}
	if command.Actor.UserID != "" && !canParticipate(accountContext.Role) {
		return domain.InformationRequest{}, roleDenied(WorkPackage)
	}
	now := s.clock.Now().UTC()
	item, err := domain.NewInformationRequest(domain.InformationRequestDraft{
		ID: command.RequestID, AccountID: command.AccountID, ParentWorkItemID: command.ParentWorkItemID,
		Requirement: command.Requirement, Question: command.Question, RequestedBy: domainActor(command.Actor),
	}, now)
	if err != nil {
		return domain.InformationRequest{}, ErrInvalidCommand
	}
	return s.repository.CreateInformation(ctx, item, mutation(command.Actor, "", command.CorrelationID, now))
}

type AnswerInformationCommand struct {
	Actor           access.Actor
	AccountID       ids.AccountID
	RequestID       ids.InformationRequestID
	Fact            domain.FactReference
	ExpectedVersion uint64
	CorrelationID   string
}

func (s *Service) AnswerInformation(ctx context.Context, command AnswerInformationCommand) (InformationCompletion, error) {
	if !validHuman(command.Actor) || !validBase(command.Actor, command.AccountID, command.CorrelationID) || ids.Validate(string(command.RequestID)) != nil || !command.Fact.Valid() || command.ExpectedVersion == 0 {
		return InformationCompletion{}, ErrInvalidCommand
	}
	accountContext, err := s.authorize(ctx, command.Actor, command.AccountID, WorkPackage, true)
	if err != nil {
		return InformationCompletion{}, err
	}
	now := s.clock.Now().UTC()
	actor := domainActor(command.Actor)
	completion, err := s.repository.CompleteEligibleInformation(ctx, command.AccountID, CompleteInformationCommand{
		TargetID: command.RequestID, Fact: command.Fact, Role: accountContext.Role, Actor: actor, ExpectedVersion: command.ExpectedVersion,
		Mutation: Mutation{Actor: actor, ReasonCode: "information_supplied", CorrelationID: cleanCorrelation(command.CorrelationID), At: now},
	})
	if err != nil || len(completion.ResumableParents) == 0 || s.workResumer == nil {
		return completion, err
	}
	_, err = s.workResumer.ResumeAttentionParents(ctx, workapp.ResumeAttentionCommand{
		Actor: command.Actor, AccountID: command.AccountID, ParentIDs: completion.ResumableParents,
		CorrelationID: cleanCorrelation(command.CorrelationID),
	})
	return completion, err
}

type CancelInformationCommand struct {
	Actor           access.Actor
	AccountID       ids.AccountID
	RequestID       ids.InformationRequestID
	ExpectedVersion uint64
	Reason          string
	CorrelationID   string
}

func (s *Service) CancelInformation(ctx context.Context, command CancelInformationCommand) (domain.InformationRequest, error) {
	if !validHuman(command.Actor) || !validBase(command.Actor, command.AccountID, command.CorrelationID) || ids.Validate(string(command.RequestID)) != nil || command.ExpectedVersion == 0 {
		return domain.InformationRequest{}, ErrInvalidCommand
	}
	accountContext, err := s.authorize(ctx, command.Actor, command.AccountID, WorkPackage, true)
	if err != nil {
		return domain.InformationRequest{}, err
	}
	item, err := s.repository.GetInformation(ctx, command.AccountID, command.RequestID)
	if err != nil {
		return domain.InformationRequest{}, err
	}
	now := s.clock.Now().UTC()
	actor := domainActor(command.Actor)
	updated, err := item.Cancel(domain.CancelInformationCommand{Role: accountContext.Role, Actor: actor, Reason: command.Reason, ExpectedVersion: command.ExpectedVersion, At: now})
	if err != nil {
		return domain.InformationRequest{}, err
	}
	return s.repository.UpdateInformation(ctx, updated, item.Version, Mutation{Actor: actor, ReasonCode: "information_canceled", CorrelationID: cleanCorrelation(command.CorrelationID), At: now})
}

type CreateWorkReviewCommand struct {
	Actor          access.Actor
	AccountID      ids.AccountID
	ReviewID       ids.WorkReviewID
	WorkItemID     ids.WorkItemID
	WorkVersion    uint64
	ProposalSHA256 [sha256.Size]byte
	Question       string
	ReviewerID     ids.UserID
	CorrelationID  string
}

func (s *Service) CreateWorkReview(ctx context.Context, command CreateWorkReviewCommand) (domain.WorkReview, error) {
	if !validBase(command.Actor, command.AccountID, command.CorrelationID) || ids.Validate(string(command.ReviewID)) != nil || ids.Validate(string(command.WorkItemID)) != nil || ids.Validate(string(command.ReviewerID)) != nil {
		return domain.WorkReview{}, ErrInvalidCommand
	}
	accountContext, err := s.authorize(ctx, command.Actor, command.AccountID, WorkPackage, true)
	if err != nil {
		return domain.WorkReview{}, err
	}
	if command.Actor.UserID != "" && !canParticipate(accountContext.Role) {
		return domain.WorkReview{}, roleDenied(WorkPackage)
	}
	reviewerRole, found, err := s.reviewerDirectory.ActiveRole(ctx, command.AccountID, command.ReviewerID)
	if err != nil {
		return domain.WorkReview{}, err
	}
	if !found || !canParticipate(reviewerRole) {
		return domain.WorkReview{}, roleDenied(WorkPackage)
	}
	now := s.clock.Now().UTC()
	item, err := domain.NewWorkReview(domain.WorkReviewDraft{
		ID: command.ReviewID, AccountID: command.AccountID, WorkItemID: command.WorkItemID, WorkVersion: command.WorkVersion,
		ProposalSHA256: command.ProposalSHA256, Question: command.Question, RequestedBy: domainActor(command.Actor), ReviewerID: command.ReviewerID,
	}, now)
	if err != nil {
		return domain.WorkReview{}, ErrInvalidCommand
	}
	return s.repository.CreateWorkReview(ctx, item, mutation(command.Actor, "", command.CorrelationID, now))
}

type DecideWorkReviewCommand struct {
	Actor           access.Actor
	AccountID       ids.AccountID
	ReviewID        ids.WorkReviewID
	Decision        domain.WorkReviewDecision
	ExpectedVersion uint64
	Reason          string
	CorrelationID   string
}

func (s *Service) DecideWorkReview(ctx context.Context, command DecideWorkReviewCommand) (domain.WorkReview, error) {
	if !validHuman(command.Actor) || !validBase(command.Actor, command.AccountID, command.CorrelationID) || ids.Validate(string(command.ReviewID)) != nil || command.ExpectedVersion == 0 {
		return domain.WorkReview{}, ErrInvalidCommand
	}
	accountContext, err := s.authorize(ctx, command.Actor, command.AccountID, WorkPackage, true)
	if err != nil {
		return domain.WorkReview{}, err
	}
	item, err := s.repository.GetWorkReview(ctx, command.AccountID, command.ReviewID)
	if err != nil {
		return domain.WorkReview{}, err
	}
	now := s.clock.Now().UTC()
	actor := domainActor(command.Actor)
	updated, err := item.Decide(domain.DecideWorkReviewCommand{Decision: command.Decision, Reason: command.Reason, Role: accountContext.Role, Actor: actor, ExpectedVersion: command.ExpectedVersion, At: now})
	if err != nil {
		return domain.WorkReview{}, err
	}
	return s.repository.UpdateWorkReview(ctx, updated, item.Version, Mutation{Actor: actor, ReasonCode: "review_completed", CorrelationID: cleanCorrelation(command.CorrelationID), At: now})
}

type CancelWorkReviewCommand struct {
	Actor           access.Actor
	AccountID       ids.AccountID
	ReviewID        ids.WorkReviewID
	ExpectedVersion uint64
	Reason          string
	CorrelationID   string
}

func (s *Service) CancelWorkReview(ctx context.Context, command CancelWorkReviewCommand) (domain.WorkReview, error) {
	if !validHuman(command.Actor) || !validBase(command.Actor, command.AccountID, command.CorrelationID) || ids.Validate(string(command.ReviewID)) != nil || command.ExpectedVersion == 0 {
		return domain.WorkReview{}, ErrInvalidCommand
	}
	accountContext, err := s.authorize(ctx, command.Actor, command.AccountID, WorkPackage, true)
	if err != nil {
		return domain.WorkReview{}, err
	}
	item, err := s.repository.GetWorkReview(ctx, command.AccountID, command.ReviewID)
	if err != nil {
		return domain.WorkReview{}, err
	}
	now := s.clock.Now().UTC()
	actor := domainActor(command.Actor)
	updated, err := item.Cancel(domain.CancelWorkReviewCommand{Role: accountContext.Role, Actor: actor, Reason: command.Reason, ExpectedVersion: command.ExpectedVersion, At: now})
	if err != nil {
		return domain.WorkReview{}, err
	}
	return s.repository.UpdateWorkReview(ctx, updated, item.Version, Mutation{Actor: actor, ReasonCode: "review_canceled", CorrelationID: cleanCorrelation(command.CorrelationID), At: now})
}

type ReconcileWorkReviewCommand struct {
	Actor           access.Actor
	AccountID       ids.AccountID
	ReviewID        ids.WorkReviewID
	WorkVersion     uint64
	ProposalSHA256  [sha256.Size]byte
	ExpectedVersion uint64
	CorrelationID   string
}

func (s *Service) ReconcileWorkReview(ctx context.Context, command ReconcileWorkReviewCommand) (domain.WorkReview, error) {
	if !validBase(command.Actor, command.AccountID, command.CorrelationID) || ids.Validate(string(command.ReviewID)) != nil || command.ExpectedVersion == 0 {
		return domain.WorkReview{}, ErrInvalidCommand
	}
	accountContext, err := s.authorize(ctx, command.Actor, command.AccountID, WorkPackage, true)
	if err != nil {
		return domain.WorkReview{}, err
	}
	if command.Actor.UserID != "" && !canParticipate(accountContext.Role) {
		return domain.WorkReview{}, roleDenied(WorkPackage)
	}
	item, err := s.repository.GetWorkReview(ctx, command.AccountID, command.ReviewID)
	if err != nil {
		return domain.WorkReview{}, err
	}
	now := s.clock.Now().UTC()
	updated, err := item.ReconcileProposal(domain.ReconcileWorkReviewCommand{WorkVersion: command.WorkVersion, ProposalSHA256: command.ProposalSHA256, ExpectedVersion: command.ExpectedVersion, At: now})
	if err != nil || updated.Version == item.Version {
		return updated, err
	}
	return s.repository.UpdateWorkReview(ctx, updated, item.Version, mutation(command.Actor, "proposal_changed", command.CorrelationID, now))
}

type CreateApprovalCommand struct {
	Actor                    access.Actor
	AccountID                ids.AccountID
	ApprovalID               ids.ConsequentialApprovalID
	OperationID              string
	InvocationID             ids.AgentInvocationID
	WorkItemID               ids.WorkItemID
	Capability               string
	CanonicalPayload         json.RawMessage
	EvidenceSHA256           [sha256.Size]byte
	PolicyVersion            uint64
	RequireIndependentReview bool
	ExpiresAt                time.Time
	CorrelationID            string
}

func (s *Service) CreateApproval(ctx context.Context, command CreateApprovalCommand) (domain.ConsequentialApproval, error) {
	if !validBase(command.Actor, command.AccountID, command.CorrelationID) || ids.Validate(string(command.ApprovalID)) != nil || ids.Validate(string(command.InvocationID)) != nil {
		return domain.ConsequentialApproval{}, ErrInvalidCommand
	}
	accountContext, err := s.authorize(ctx, command.Actor, command.AccountID, AgentsPackage, true)
	if err != nil {
		return domain.ConsequentialApproval{}, err
	}
	if command.Actor.UserID != "" && !canParticipate(accountContext.Role) {
		return domain.ConsequentialApproval{}, roleDenied(AgentsPackage)
	}
	now := s.clock.Now().UTC()
	item, err := domain.NewConsequentialApproval(domain.ConsequentialApprovalDraft{
		ID: command.ApprovalID, AccountID: command.AccountID, OperationID: command.OperationID, InvocationID: command.InvocationID,
		WorkItemID: command.WorkItemID, Capability: command.Capability, CanonicalPayload: command.CanonicalPayload,
		EvidenceSHA256: command.EvidenceSHA256, Proposer: domainActor(command.Actor), PolicyVersion: command.PolicyVersion,
		RequireIndependentReview: command.RequireIndependentReview, ExpiresAt: command.ExpiresAt,
	}, now)
	if err != nil {
		return domain.ConsequentialApproval{}, ErrInvalidCommand
	}
	return s.repository.CreateApproval(ctx, item, mutation(command.Actor, "", command.CorrelationID, now))
}

type DecideApprovalCommand struct {
	Actor           access.Actor
	AccountID       ids.AccountID
	ApprovalID      ids.ConsequentialApprovalID
	Decision        domain.ApprovalDecision
	ExpectedVersion uint64
	Reason          string
	CorrelationID   string
}

func (s *Service) DecideApproval(ctx context.Context, command DecideApprovalCommand) (domain.ConsequentialApproval, error) {
	if !validHuman(command.Actor) || !validBase(command.Actor, command.AccountID, command.CorrelationID) || ids.Validate(string(command.ApprovalID)) != nil || command.ExpectedVersion == 0 {
		return domain.ConsequentialApproval{}, ErrInvalidCommand
	}
	accountContext, err := s.authorize(ctx, command.Actor, command.AccountID, AgentsPackage, true)
	if err != nil {
		return domain.ConsequentialApproval{}, err
	}
	item, err := s.repository.GetApproval(ctx, command.AccountID, command.ApprovalID)
	if err != nil {
		return domain.ConsequentialApproval{}, err
	}
	if item.InvocationID == "" {
		if _, err := s.authorize(ctx, command.Actor, command.AccountID, "marketing", true); err != nil {
			return domain.ConsequentialApproval{}, err
		}
	}
	now := s.clock.Now().UTC()
	actor := domainActor(command.Actor)
	updated, err := item.Decide(domain.DecideApprovalCommand{Decision: command.Decision, Reason: command.Reason, Role: accountContext.Role, Actor: actor, ExpectedVersion: command.ExpectedVersion, At: now})
	if err != nil {
		return domain.ConsequentialApproval{}, err
	}
	return s.repository.UpdateApproval(ctx, updated, item.Version, Mutation{Actor: actor, ReasonCode: "approval_decided", CorrelationID: cleanCorrelation(command.CorrelationID), At: now})
}

type CancelApprovalCommand struct {
	Actor           access.Actor
	AccountID       ids.AccountID
	ApprovalID      ids.ConsequentialApprovalID
	ExpectedVersion uint64
	Reason          string
	CorrelationID   string
}

func (s *Service) CancelApproval(ctx context.Context, command CancelApprovalCommand) (domain.ConsequentialApproval, error) {
	if !validHuman(command.Actor) || !validBase(command.Actor, command.AccountID, command.CorrelationID) || ids.Validate(string(command.ApprovalID)) != nil || command.ExpectedVersion == 0 {
		return domain.ConsequentialApproval{}, ErrInvalidCommand
	}
	accountContext, err := s.authorize(ctx, command.Actor, command.AccountID, AgentsPackage, true)
	if err != nil {
		return domain.ConsequentialApproval{}, err
	}
	item, err := s.repository.GetApproval(ctx, command.AccountID, command.ApprovalID)
	if err != nil {
		return domain.ConsequentialApproval{}, err
	}
	now := s.clock.Now().UTC()
	actor := domainActor(command.Actor)
	updated, err := item.Cancel(domain.CancelApprovalCommand{Role: accountContext.Role, Actor: actor, Reason: command.Reason, ExpectedVersion: command.ExpectedVersion, At: now})
	if err != nil {
		return domain.ConsequentialApproval{}, err
	}
	return s.repository.UpdateApproval(ctx, updated, item.Version, Mutation{Actor: actor, ReasonCode: "approval_canceled", CorrelationID: cleanCorrelation(command.CorrelationID), At: now})
}

type ReconcileApprovalCommand struct {
	Actor            access.Actor
	AccountID        ids.AccountID
	ApprovalID       ids.ConsequentialApprovalID
	CanonicalPayload json.RawMessage
	EvidenceSHA256   [sha256.Size]byte
	PolicyVersion    uint64
	ExpectedVersion  uint64
	CorrelationID    string
}

func (s *Service) ReconcileApproval(ctx context.Context, command ReconcileApprovalCommand) (domain.ConsequentialApproval, error) {
	if !validBase(command.Actor, command.AccountID, command.CorrelationID) || ids.Validate(string(command.ApprovalID)) != nil || command.ExpectedVersion == 0 {
		return domain.ConsequentialApproval{}, ErrInvalidCommand
	}
	accountContext, err := s.authorize(ctx, command.Actor, command.AccountID, AgentsPackage, true)
	if err != nil {
		return domain.ConsequentialApproval{}, err
	}
	if command.Actor.UserID != "" && !canApprove(accountContext.Role) {
		return domain.ConsequentialApproval{}, roleDenied(AgentsPackage)
	}
	item, err := s.repository.GetApproval(ctx, command.AccountID, command.ApprovalID)
	if err != nil {
		return domain.ConsequentialApproval{}, err
	}
	now := s.clock.Now().UTC()
	updated, err := item.ReconcileProposal(domain.ReconcileApprovalCommand{
		CanonicalPayload: command.CanonicalPayload, EvidenceSHA256: command.EvidenceSHA256, PolicyVersion: command.PolicyVersion,
		ExpectedVersion: command.ExpectedVersion, At: now,
	})
	if err != nil || updated.Version == item.Version {
		return updated, err
	}
	return s.repository.UpdateApproval(ctx, updated, item.Version, mutation(command.Actor, "proposal_changed", command.CorrelationID, now))
}

type ExpireApprovalCommand struct {
	Actor           access.Actor
	AccountID       ids.AccountID
	ApprovalID      ids.ConsequentialApprovalID
	ExpectedVersion uint64
	CorrelationID   string
}

func (s *Service) ExpireApproval(ctx context.Context, command ExpireApprovalCommand) (domain.ConsequentialApproval, error) {
	if !validBase(command.Actor, command.AccountID, command.CorrelationID) || ids.Validate(string(command.ApprovalID)) != nil || command.ExpectedVersion == 0 {
		return domain.ConsequentialApproval{}, ErrInvalidCommand
	}
	accountContext, err := s.authorize(ctx, command.Actor, command.AccountID, AgentsPackage, true)
	if err != nil {
		return domain.ConsequentialApproval{}, err
	}
	if command.Actor.UserID != "" && !canApprove(accountContext.Role) {
		return domain.ConsequentialApproval{}, roleDenied(AgentsPackage)
	}
	item, err := s.repository.GetApproval(ctx, command.AccountID, command.ApprovalID)
	if err != nil {
		return domain.ConsequentialApproval{}, err
	}
	now := s.clock.Now().UTC()
	updated, err := item.Expire(now, command.ExpectedVersion)
	if err != nil {
		return domain.ConsequentialApproval{}, err
	}
	return s.repository.UpdateApproval(ctx, updated, item.Version, mutation(command.Actor, "approval_expired", command.CorrelationID, now))
}

func (s *Service) authorize(ctx context.Context, actor access.Actor, accountID ids.AccountID, packageCode catalog.PackageCode, write bool) (access.AccountContext, error) {
	return s.authorizer.Authorize(ctx, actor, accountID, access.Requirement{Package: packageCode, Mutation: write})
}

func validBase(actor access.Actor, accountID ids.AccountID, correlationID string) bool {
	if !actor.Valid() || ids.Validate(string(accountID)) != nil || cleanCorrelation(correlationID) == "" || len(cleanCorrelation(correlationID)) > 200 {
		return false
	}
	if actor.UserID != "" {
		return ids.Validate(string(actor.UserID)) == nil
	}
	return strings.TrimSpace(actor.WorkloadID) == actor.WorkloadID && len(actor.WorkloadID) <= 200
}

func validHuman(actor access.Actor) bool {
	return actor.UserID != "" && ids.Validate(string(actor.UserID)) == nil && actor.WorkloadID == ""
}

func cleanCorrelation(value string) string { return strings.TrimSpace(value) }

func mutation(actor access.Actor, reasonCode, correlationID string, at time.Time) Mutation {
	return Mutation{Actor: domainActor(actor), ReasonCode: reasonCode, CorrelationID: cleanCorrelation(correlationID), At: at}
}

func domainActor(actor access.Actor) domain.Actor {
	if actor.UserID != "" {
		return domain.Actor{Kind: domain.ActorUser, ID: string(actor.UserID)}
	}
	return domain.Actor{Kind: domain.ActorWorkload, ID: actor.WorkloadID}
}

func canParticipate(role accounts.MembershipRole) bool {
	return role == accounts.RoleOwner || role == accounts.RoleAdministrator || role == accounts.RoleMember
}

func roleDenied(packageCode catalog.PackageCode) error {
	return &access.DeniedError{Code: access.DenialRole, Package: packageCode}
}

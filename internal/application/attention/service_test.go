package attention

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	workapp "github.com/tinfoyle/spyglass-engine/internal/application/work"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/attention"
	workdomain "github.com/tinfoyle/spyglass-engine/internal/modules/work"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	serviceAccount    = "10000000-0000-4000-8000-000000000001"
	serviceUser       = "20000000-0000-4000-8000-000000000002"
	serviceReviewer   = "30000000-0000-4000-8000-000000000003"
	serviceWork       = "40000000-0000-4000-8000-000000000004"
	serviceObject     = "50000000-0000-4000-8000-000000000005"
	serviceFact       = "60000000-0000-4000-8000-000000000006"
	serviceInvocation = "70000000-0000-4000-8000-000000000007"
	serviceOperation  = "80000000-0000-4000-8000-000000000008"
)

var serviceNow = time.Date(2026, 8, 21, 20, 0, 0, 0, time.UTC)

func TestServiceRejectsMissingDependencies(t *testing.T) {
	if _, err := NewService(nil, attentionReviewerStub{}, &attentionRepositoryStub{}, attentionClock{serviceNow}); err == nil {
		t.Fatal("missing authorizer was accepted")
	}
}

func TestInformationCommandsAuthorizeWorkAndPreserveExactCompletionPlan(t *testing.T) {
	authorizer := &attentionAuthorizerStub{role: accounts.RoleMember}
	repository := &attentionRepositoryStub{}
	resumer := &attentionWorkResumerStub{}
	service, err := NewService(authorizer, attentionReviewerStub{role: accounts.RoleMember, found: true}, repository, attentionClock{serviceNow}, WithWorkResumer(resumer))
	if err != nil {
		t.Fatal(err)
	}
	actor := access.Actor{UserID: ids.UserID(serviceUser)}
	created, err := service.CreateInformation(context.Background(), CreateInformationCommand{
		Actor: actor, AccountID: ids.AccountID(serviceAccount), RequestID: ids.InformationRequestID(serviceObject),
		ParentWorkItemID: ids.WorkItemID(serviceWork), Requirement: domain.FactRequirement{Key: "company.legal_name", Scope: domain.InformationScopeAccount},
		Question: "What is the registered company name?", CorrelationID: " information-create ",
	})
	if err != nil || created.RequestedBy != (domain.Actor{Kind: domain.ActorUser, ID: serviceUser}) || repository.lastMutation.CorrelationID != "information-create" {
		t.Fatalf("created=%+v mutation=%+v err=%v", created, repository.lastMutation, err)
	}
	if authorizer.last.Package != WorkPackage || !authorizer.last.Mutation {
		t.Fatalf("create requirement=%+v", authorizer.last)
	}
	repository.completion = InformationCompletion{Answered: []domain.InformationRequest{created}, ResumableParents: []ids.WorkItemID{created.ParentWorkItemID}}
	completion, err := service.AnswerInformation(context.Background(), AnswerInformationCommand{
		Actor: actor, AccountID: created.AccountID, RequestID: created.ID,
		Fact: domain.FactReference{ID: serviceFact, Version: 2, Requirement: created.Requirement}, ExpectedVersion: created.Version,
		CorrelationID: "information-answer",
	})
	if err != nil || len(completion.Answered) != 1 || len(completion.ResumableParents) != 1 || repository.completeCommand.Role != accounts.RoleMember || repository.completeCommand.Mutation.ReasonCode != "information_supplied" {
		t.Fatalf("completion=%+v command=%+v err=%v", completion, repository.completeCommand, err)
	}
	if repository.completeCommand.Actor.ID != serviceUser || !repository.completeCommand.Mutation.At.Equal(serviceNow) {
		t.Fatalf("completion actor/time=%+v", repository.completeCommand)
	}
	if len(resumer.command.ParentIDs) != 1 || resumer.command.ParentIDs[0] != created.ParentWorkItemID || resumer.command.Actor != actor || resumer.command.CorrelationID != "information-answer" {
		t.Fatalf("resumption command=%+v", resumer.command)
	}
}

func TestInformationCreationRejectsReadOnlyRoleBeforePersistence(t *testing.T) {
	repository := &attentionRepositoryStub{}
	service, _ := NewService(&attentionAuthorizerStub{role: accounts.RoleViewer}, attentionReviewerStub{role: accounts.RoleMember, found: true}, repository, attentionClock{serviceNow})
	_, err := service.CreateInformation(context.Background(), CreateInformationCommand{
		Actor: access.Actor{UserID: ids.UserID(serviceUser)}, AccountID: ids.AccountID(serviceAccount), RequestID: ids.InformationRequestID(serviceObject),
		ParentWorkItemID: ids.WorkItemID(serviceWork), Requirement: domain.FactRequirement{Key: "company.name", Scope: domain.InformationScopeAccount},
		Question: "What is the company name?", CorrelationID: "viewer-create",
	})
	if !access.IsDenied(err, access.DenialRole) || repository.information.ID != "" {
		t.Fatalf("error=%v repository=%+v", err, repository.information)
	}
}

func TestReviewDecisionRequiresAssignedReviewerAndPersistsTypedEvent(t *testing.T) {
	authorizer := &attentionAuthorizerStub{role: accounts.RoleMember}
	repository := &attentionRepositoryStub{}
	service, _ := NewService(authorizer, attentionReviewerStub{role: accounts.RoleMember, found: true}, repository, attentionClock{serviceNow})
	requester := access.Actor{UserID: ids.UserID(serviceUser)}
	review, err := service.CreateWorkReview(context.Background(), CreateWorkReviewCommand{
		Actor: requester, AccountID: ids.AccountID(serviceAccount), ReviewID: ids.WorkReviewID(serviceObject), WorkItemID: ids.WorkItemID(serviceWork),
		WorkVersion: 3, ProposalSHA256: sha256.Sum256([]byte("review proposal")), Question: "Is this result ready?",
		ReviewerID: ids.UserID(serviceReviewer), CorrelationID: "review-create",
	})
	if err != nil {
		t.Fatal(err)
	}
	repository.review = review
	service.clock = attentionClock{serviceNow.Add(time.Minute)}
	decided, err := service.DecideWorkReview(context.Background(), DecideWorkReviewCommand{
		Actor: access.Actor{UserID: ids.UserID(serviceReviewer)}, AccountID: review.AccountID, ReviewID: review.ID,
		Decision: domain.ReviewApprove, ExpectedVersion: review.Version, Reason: "the evidence is complete", CorrelationID: "review-decide",
	})
	if err != nil || decided.State != domain.WorkReviewApproved || repository.lastMutation.ReasonCode != "review_completed" || repository.lastExpected != review.Version {
		t.Fatalf("decided=%+v mutation=%+v expected=%d err=%v", decided, repository.lastMutation, repository.lastExpected, err)
	}
	repository.review = review
	_, err = service.DecideWorkReview(context.Background(), DecideWorkReviewCommand{
		Actor: requester, AccountID: review.AccountID, ReviewID: review.ID, Decision: domain.ReviewApprove,
		ExpectedVersion: review.Version, Reason: "attempt by requester", CorrelationID: "wrong-reviewer",
	})
	if !errors.Is(err, domain.ErrReviewer) {
		t.Fatalf("wrong reviewer error=%v", err)
	}
}

func TestReviewCreationRejectsAnIneligibleReviewer(t *testing.T) {
	repository := &attentionRepositoryStub{}
	service, _ := NewService(&attentionAuthorizerStub{role: accounts.RoleMember}, attentionReviewerStub{found: false}, repository, attentionClock{serviceNow})
	_, err := service.CreateWorkReview(context.Background(), CreateWorkReviewCommand{
		Actor: access.Actor{UserID: ids.UserID(serviceUser)}, AccountID: ids.AccountID(serviceAccount), ReviewID: ids.WorkReviewID(serviceObject),
		WorkItemID: ids.WorkItemID(serviceWork), WorkVersion: 1, ProposalSHA256: sha256.Sum256([]byte("proposal")),
		Question: "Is this ready?", ReviewerID: ids.UserID(serviceReviewer), CorrelationID: "review-ineligible",
	})
	if !access.IsDenied(err, access.DenialRole) || repository.review.ID != "" {
		t.Fatalf("error=%v review=%+v", err, repository.review)
	}
}

func TestApprovalCommandsUseAgentsPackageAndQueueViewExcludesPayload(t *testing.T) {
	authorizer := &attentionAuthorizerStub{role: accounts.RoleOwner}
	repository := &attentionRepositoryStub{}
	service, _ := NewService(authorizer, attentionReviewerStub{role: accounts.RoleMember, found: true}, repository, attentionClock{serviceNow})
	approval, err := service.CreateApproval(context.Background(), CreateApprovalCommand{
		Actor: access.Actor{WorkloadID: "runner-proposer"}, AccountID: ids.AccountID(serviceAccount), ApprovalID: ids.ConsequentialApprovalID(serviceObject),
		OperationID: serviceOperation, InvocationID: ids.AgentInvocationID(serviceInvocation), WorkItemID: ids.WorkItemID(serviceWork),
		Capability: "email.send", CanonicalPayload: json.RawMessage(`{"to":"secret@example.com","body":"private body"}`),
		EvidenceSHA256: sha256.Sum256([]byte("evidence")), PolicyVersion: 4, RequireIndependentReview: true,
		ExpiresAt: serviceNow.Add(30 * time.Minute), CorrelationID: "approval-create",
	})
	if err != nil || authorizer.last.Package != AgentsPackage || !authorizer.last.Mutation {
		t.Fatalf("approval=%+v requirement=%+v err=%v", approval, authorizer.last, err)
	}
	repository.approval = approval
	service.clock = attentionClock{serviceNow.Add(time.Minute)}
	approved, err := service.DecideApproval(context.Background(), DecideApprovalCommand{
		Actor: access.Actor{UserID: ids.UserID(serviceReviewer)}, AccountID: approval.AccountID, ApprovalID: approval.ID,
		Decision: domain.DecisionApprove, ExpectedVersion: approval.Version, Reason: "the exact action is authorized", CorrelationID: "approval-decide",
	})
	if err != nil || approved.State != domain.ConsequentialApprovalApproved || repository.lastMutation.ReasonCode != "approval_decided" {
		t.Fatalf("approved=%+v mutation=%+v err=%v", approved, repository.lastMutation, err)
	}
	repository.approvalPage = ApprovalPage{Items: []domain.ConsequentialApproval{approved}}
	page, err := service.ListApprovals(context.Background(), access.Actor{UserID: ids.UserID(serviceReviewer)}, approval.AccountID, ApprovalListQuery{})
	if err != nil || len(page.Items) != 1 || repository.lastApprovalQuery.Limit != DefaultLimit {
		t.Fatalf("page=%+v query=%+v err=%v", page, repository.lastApprovalQuery, err)
	}
	raw, _ := json.Marshal(page)
	for _, forbidden := range []string{"secret@example.com", "private body", "the exact action is authorized", "CanonicalPayload", "EvidenceSHA256", "InputSHA256"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("approval summary leaked %q: %s", forbidden, raw)
		}
	}
}

func TestApprovalDetailAndQueueRequireDecisionRole(t *testing.T) {
	repository := &attentionRepositoryStub{}
	service, _ := NewService(&attentionAuthorizerStub{role: accounts.RoleMember}, attentionReviewerStub{role: accounts.RoleMember, found: true}, repository, attentionClock{serviceNow})
	actor := access.Actor{UserID: ids.UserID(serviceUser)}
	if _, err := service.GetApproval(context.Background(), actor, ids.AccountID(serviceAccount), ids.ConsequentialApprovalID(serviceObject)); !access.IsDenied(err, access.DenialRole) {
		t.Fatalf("detail error=%v", err)
	}
	if _, err := service.ListApprovals(context.Background(), actor, ids.AccountID(serviceAccount), ApprovalListQuery{}); !access.IsDenied(err, access.DenialRole) {
		t.Fatalf("list error=%v", err)
	}
}

func TestInformationAndReviewQueuesExposePresentationWithoutEvidenceBindings(t *testing.T) {
	repository := &attentionRepositoryStub{}
	service, _ := NewService(&attentionAuthorizerStub{role: accounts.RoleMember}, attentionReviewerStub{role: accounts.RoleMember, found: true}, repository, attentionClock{serviceNow})
	request, _ := domain.NewInformationRequest(domain.InformationRequestDraft{
		ID: ids.InformationRequestID(serviceObject), AccountID: ids.AccountID(serviceAccount), ParentWorkItemID: ids.WorkItemID(serviceWork),
		Requirement: domain.FactRequirement{Key: "company.legal_name", Scope: domain.InformationScopeAccount},
		Question:    "What is the registered company name?", RequestedBy: domain.Actor{Kind: domain.ActorUser, ID: serviceUser},
	}, serviceNow)
	request, _ = request.Answer(domain.AnswerInformationCommand{
		Fact: domain.FactReference{ID: serviceFact, Version: 9, Requirement: request.Requirement}, Role: accounts.RoleMember,
		Actor: domain.Actor{Kind: domain.ActorUser, ID: serviceReviewer}, ExpectedVersion: request.Version, At: serviceNow.Add(time.Minute),
	})
	review, _ := domain.NewWorkReview(domain.WorkReviewDraft{
		ID: ids.WorkReviewID(serviceObject), AccountID: ids.AccountID(serviceAccount), WorkItemID: ids.WorkItemID(serviceWork), WorkVersion: 4,
		ProposalSHA256: sha256.Sum256([]byte("private proposal digest input")), Question: "Is this ready?",
		RequestedBy: domain.Actor{Kind: domain.ActorUser, ID: serviceUser}, ReviewerID: ids.UserID(serviceReviewer),
	}, serviceNow)
	review, _ = review.Decide(domain.DecideWorkReviewCommand{
		Decision: domain.ReviewRequestChanges, Reason: "private reviewer rationale", Role: accounts.RoleMember,
		Actor: domain.Actor{Kind: domain.ActorUser, ID: serviceReviewer}, ExpectedVersion: review.Version, At: serviceNow.Add(time.Minute),
	})
	repository.informationPage = InformationPage{Items: []domain.InformationRequest{request}}
	repository.reviewPage = WorkReviewPage{Items: []domain.WorkReview{review}}
	actor := access.Actor{UserID: ids.UserID(serviceUser)}
	informationPage, err := service.ListInformation(context.Background(), actor, ids.AccountID(serviceAccount), InformationListQuery{})
	if err != nil || len(informationPage.Items) != 1 || repository.lastInformationQuery.Limit != DefaultLimit {
		t.Fatalf("information page=%+v query=%+v err=%v", informationPage, repository.lastInformationQuery, err)
	}
	reviewPage, err := service.ListWorkReviews(context.Background(), actor, ids.AccountID(serviceAccount), WorkReviewListQuery{})
	if err != nil || len(reviewPage.Items) != 1 || repository.lastReviewQuery.Limit != DefaultLimit {
		t.Fatalf("review page=%+v query=%+v err=%v", reviewPage, repository.lastReviewQuery, err)
	}
	raw, _ := json.Marshal(struct {
		Information InformationSummaryPage
		Reviews     WorkReviewSummaryPage
	}{informationPage, reviewPage})
	for _, forbidden := range []string{serviceFact, "private reviewer rationale", "ProposalSHA256", "AnswerRecord"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("queue summary leaked %q: %s", forbidden, raw)
		}
	}
}

func TestInvalidCommandDoesNotAuthorizeOrPersist(t *testing.T) {
	authorizer := &attentionAuthorizerStub{role: accounts.RoleOwner}
	repository := &attentionRepositoryStub{}
	service, _ := NewService(authorizer, attentionReviewerStub{role: accounts.RoleMember, found: true}, repository, attentionClock{serviceNow})
	_, err := service.AnswerInformation(context.Background(), AnswerInformationCommand{Actor: access.Actor{WorkloadID: "runner"}, AccountID: ids.AccountID(serviceAccount), RequestID: ids.InformationRequestID(serviceObject), ExpectedVersion: 1, CorrelationID: "answer"})
	if !errors.Is(err, ErrInvalidCommand) || authorizer.calls != 0 || repository.completeCalls != 0 {
		t.Fatalf("error=%v authorizations=%d completions=%d", err, authorizer.calls, repository.completeCalls)
	}
}

type attentionClock struct{ now time.Time }

func (clock attentionClock) Now() time.Time { return clock.now }

type attentionAuthorizerStub struct {
	role  accounts.MembershipRole
	err   error
	last  access.Requirement
	calls int
}

type attentionReviewerStub struct {
	role  accounts.MembershipRole
	found bool
	err   error
}

type attentionWorkResumerStub struct {
	command workapp.ResumeAttentionCommand
	err     error
}

func (stub *attentionWorkResumerStub) ResumeAttentionParents(_ context.Context, command workapp.ResumeAttentionCommand) ([]workdomain.Item, error) {
	stub.command = command
	return nil, stub.err
}

func (stub attentionReviewerStub) ActiveRole(context.Context, ids.AccountID, ids.UserID) (accounts.MembershipRole, bool, error) {
	return stub.role, stub.found, stub.err
}

func (stub *attentionAuthorizerStub) Authorize(_ context.Context, _ access.Actor, accountID ids.AccountID, requirement access.Requirement) (access.AccountContext, error) {
	stub.calls++
	stub.last = requirement
	return access.AccountContext{AccountID: accountID, Role: stub.role}, stub.err
}

type attentionRepositoryStub struct {
	information          domain.InformationRequest
	review               domain.WorkReview
	approval             domain.ConsequentialApproval
	completion           InformationCompletion
	informationPage      InformationPage
	reviewPage           WorkReviewPage
	approvalPage         ApprovalPage
	completeCommand      CompleteInformationCommand
	lastMutation         Mutation
	lastExpected         uint64
	lastInformationQuery InformationListQuery
	lastReviewQuery      WorkReviewListQuery
	lastApprovalQuery    ApprovalListQuery
	completeCalls        int
	err                  error
}

func (stub *attentionRepositoryStub) CreateInformation(_ context.Context, item domain.InformationRequest, mutation Mutation) (domain.InformationRequest, error) {
	stub.information, stub.lastMutation = item, mutation
	return item, stub.err
}
func (stub *attentionRepositoryStub) GetInformation(context.Context, ids.AccountID, ids.InformationRequestID) (domain.InformationRequest, error) {
	return stub.information, stub.err
}
func (stub *attentionRepositoryStub) UpdateInformation(_ context.Context, item domain.InformationRequest, expected uint64, mutation Mutation) (domain.InformationRequest, error) {
	stub.information, stub.lastExpected, stub.lastMutation = item, expected, mutation
	return item, stub.err
}
func (stub *attentionRepositoryStub) CompleteEligibleInformation(_ context.Context, _ ids.AccountID, command CompleteInformationCommand) (InformationCompletion, error) {
	stub.completeCalls++
	stub.completeCommand = command
	return stub.completion, stub.err
}

func (stub *attentionRepositoryStub) ListInformation(_ context.Context, _ ids.AccountID, query InformationListQuery) (InformationPage, error) {
	stub.lastInformationQuery = query
	return stub.informationPage, stub.err
}
func (stub *attentionRepositoryStub) CreateWorkReview(_ context.Context, item domain.WorkReview, mutation Mutation) (domain.WorkReview, error) {
	stub.review, stub.lastMutation = item, mutation
	return item, stub.err
}
func (stub *attentionRepositoryStub) GetWorkReview(context.Context, ids.AccountID, ids.WorkReviewID) (domain.WorkReview, error) {
	return stub.review, stub.err
}
func (stub *attentionRepositoryStub) UpdateWorkReview(_ context.Context, item domain.WorkReview, expected uint64, mutation Mutation) (domain.WorkReview, error) {
	stub.review, stub.lastExpected, stub.lastMutation = item, expected, mutation
	return item, stub.err
}

func (stub *attentionRepositoryStub) ListWorkReviews(_ context.Context, _ ids.AccountID, query WorkReviewListQuery) (WorkReviewPage, error) {
	stub.lastReviewQuery = query
	return stub.reviewPage, stub.err
}
func (stub *attentionRepositoryStub) CreateApproval(_ context.Context, item domain.ConsequentialApproval, mutation Mutation) (domain.ConsequentialApproval, error) {
	stub.approval, stub.lastMutation = item, mutation
	return item, stub.err
}
func (stub *attentionRepositoryStub) GetApproval(context.Context, ids.AccountID, ids.ConsequentialApprovalID) (domain.ConsequentialApproval, error) {
	return stub.approval, stub.err
}
func (stub *attentionRepositoryStub) UpdateApproval(_ context.Context, item domain.ConsequentialApproval, expected uint64, mutation Mutation) (domain.ConsequentialApproval, error) {
	stub.approval, stub.lastExpected, stub.lastMutation = item, expected, mutation
	return item, stub.err
}
func (stub *attentionRepositoryStub) ListApprovals(_ context.Context, _ ids.AccountID, query ApprovalListQuery) (ApprovalPage, error) {
	stub.lastApprovalQuery = query
	return stub.approvalPage, stub.err
}

var _ Repository = (*attentionRepositoryStub)(nil)
var _ Authorizer = (*attentionAuthorizerStub)(nil)
var _ ReviewerDirectory = attentionReviewerStub{}
var _ WorkResumer = (*attentionWorkResumerStub)(nil)

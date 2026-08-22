package baseline

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	workapp "github.com/tinfoyle/spyglass-engine/internal/application/work"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/baseline"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	workdomain "github.com/tinfoyle/spyglass-engine/internal/modules/work"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Service struct {
	authorizer Authorizer
	repository Repository
	clock      Clock
	work       WorkCreator
}

type Option func(*Service) error

func WithWorkCreator(creator WorkCreator) Option {
	return func(service *Service) error {
		if creator == nil {
			return errors.New("Baseline Work creator is required")
		}
		service.work = creator
		return nil
	}
}

func New(authorizer Authorizer, repository Repository, clock Clock, options ...Option) (*Service, error) {
	if authorizer == nil || repository == nil || clock == nil {
		return nil, errors.New("Baseline dependencies are required")
	}
	service := &Service{authorizer: authorizer, repository: repository, clock: clock}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("Baseline option is required")
		}
		if err := option(service); err != nil {
			return nil, err
		}
	}
	return service, nil
}

type StartCommand struct {
	Actor              access.Actor
	AccountID          ids.AccountID
	AssessmentID       ids.BaselineAssessmentID
	CatalogVersion     string
	ScopePolicyVersion string
	CorrelationID      string
}

func (s *Service) Start(ctx context.Context, command StartCommand) (domain.Assessment, error) {
	accountContext, actor, err := s.authorize(ctx, command.Actor, command.AccountID, command.AssessmentID, command.CorrelationID, true)
	if err != nil {
		return domain.Assessment{}, err
	}
	if !canManage(accountContext.Role) {
		return domain.Assessment{}, roleDenied()
	}
	now := s.clock.Now().UTC()
	assessment, err := domain.NewAssessment(domain.AssessmentDraft{ID: command.AssessmentID, AccountID: command.AccountID, CatalogVersion: command.CatalogVersion, ScopePolicyVersion: command.ScopePolicyVersion, CreatedBy: actor}, now)
	if err != nil {
		return domain.Assessment{}, ErrInvalid
	}
	return s.repository.Create(ctx, assessment, mutation(actor, "assessment_started", command.CorrelationID, now))
}

func (s *Service) Get(ctx context.Context, actor access.Actor, accountID ids.AccountID, assessmentID ids.BaselineAssessmentID) (domain.Assessment, error) {
	if actor.UserID == "" || actor.WorkloadID != "" || ids.Validate(string(actor.UserID)) != nil || ids.Validate(string(accountID)) != nil || ids.Validate(string(assessmentID)) != nil {
		return domain.Assessment{}, ErrInvalid
	}
	if _, err := s.authorizer.Authorize(ctx, actor, accountID, access.Requirement{Package: catalog.PackageKnowledge}); err != nil {
		return domain.Assessment{}, err
	}
	return s.repository.Get(ctx, accountID, assessmentID)
}

type AnswerCommand struct {
	Actor           access.Actor
	AccountID       ids.AccountID
	AssessmentID    ids.BaselineAssessmentID
	QuestionKey     string
	Kind            domain.AnswerKind
	Fact            *domain.FactReference
	Reason          string
	ExpectedVersion uint64
	CorrelationID   string
}

func (s *Service) Answer(ctx context.Context, command AnswerCommand) (domain.Assessment, error) {
	return s.change(ctx, command.Actor, command.AccountID, command.AssessmentID, command.ExpectedVersion, command.CorrelationID, "interview_answered", func(current domain.Assessment, actor domain.Actor, role accounts.MembershipRole, now time.Time) (domain.Assessment, error) {
		return current.AnswerInterview(domain.AnswerInterviewCommand{Answer: domain.InterviewAnswer{QuestionKey: command.QuestionKey, Kind: command.Kind, Fact: command.Fact, Reason: command.Reason, AnsweredBy: actor, AnsweredAt: now}, Role: role, ExpectedVersion: command.ExpectedVersion})
	}, Transition{EventType: "interview_answered"})
}

type AdvanceCommand struct {
	Actor           access.Actor
	AccountID       ids.AccountID
	AssessmentID    ids.BaselineAssessmentID
	ExpectedVersion uint64
	CorrelationID   string
}

func (s *Service) BeginInventory(ctx context.Context, command AdvanceCommand) (domain.Assessment, error) {
	return s.change(ctx, command.Actor, command.AccountID, command.AssessmentID, command.ExpectedVersion, command.CorrelationID, "inventory_started", func(current domain.Assessment, actor domain.Actor, role accounts.MembershipRole, now time.Time) (domain.Assessment, error) {
		return current.BeginInventory(domain.BeginInventoryCommand{Actor: actor, Role: role, ExpectedVersion: command.ExpectedVersion, At: now})
	}, Transition{EventType: "inventory_started"})
}

type CompleteInventoryCommand struct {
	AdvanceCommand
	Requirements []domain.RequirementDraft
}

func (s *Service) CompleteInventory(ctx context.Context, command CompleteInventoryCommand) (domain.Assessment, error) {
	return s.change(ctx, command.Actor, command.AccountID, command.AssessmentID, command.ExpectedVersion, command.CorrelationID, "inventory_completed", func(current domain.Assessment, actor domain.Actor, role accounts.MembershipRole, now time.Time) (domain.Assessment, error) {
		requirements := append([]domain.RequirementDraft(nil), command.Requirements...)
		for index := range requirements {
			requirements[index].CatalogVersion = current.CatalogVersion
			requirements[index].ScopePolicyVersion = current.ScopePolicyVersion
		}
		return current.CompleteInventory(domain.CompleteInventoryCommand{Requirements: requirements, Actor: actor, Role: role, ExpectedVersion: command.ExpectedVersion, At: now})
	}, Transition{EventType: "inventory_completed"})
}

type DecideEvidenceCommand struct {
	AdvanceCommand
	RequirementID ids.BaselineRequirementID
	EvidenceID    ids.KnowledgeEvidenceID
	Decision      domain.EvidenceDecisionKind
	Reason        string
}

func (s *Service) DecideEvidence(ctx context.Context, command DecideEvidenceCommand) (domain.Assessment, error) {
	transition := Transition{EventType: "evidence_decided", RequirementID: command.RequirementID}
	return s.change(ctx, command.Actor, command.AccountID, command.AssessmentID, command.ExpectedVersion, command.CorrelationID, "evidence_decided", func(current domain.Assessment, actor domain.Actor, role accounts.MembershipRole, now time.Time) (domain.Assessment, error) {
		return current.DecideEvidence(domain.DecideEvidenceCommand{RequirementID: command.RequirementID, Decision: domain.EvidenceDecision{EvidenceID: command.EvidenceID, Decision: command.Decision, Reason: command.Reason, DecidedBy: actor, DecidedAt: now}, Role: role, ExpectedVersion: command.ExpectedVersion})
	}, transition)
}

type DispositionCommand struct {
	AdvanceCommand
	RequirementID ids.BaselineRequirementID
	Disposition   domain.RequirementDisposition
	Reason        string
}

func (s *Service) Disposition(ctx context.Context, command DispositionCommand) (domain.Assessment, error) {
	transition := Transition{EventType: "requirement_dispositioned", RequirementID: command.RequirementID}
	return s.change(ctx, command.Actor, command.AccountID, command.AssessmentID, command.ExpectedVersion, command.CorrelationID, "requirement_dispositioned", func(current domain.Assessment, actor domain.Actor, role accounts.MembershipRole, now time.Time) (domain.Assessment, error) {
		return current.DispositionRequirement(domain.DispositionRequirementCommand{RequirementID: command.RequirementID, Disposition: command.Disposition, Reason: command.Reason, Actor: actor, Role: role, ExpectedVersion: command.ExpectedVersion, At: now})
	}, transition)
}

type SubmitPlanCommand struct {
	AdvanceCommand
	PlanID ids.BaselinePlanID
}

func (s *Service) SubmitPlan(ctx context.Context, command SubmitPlanCommand) (domain.Assessment, error) {
	transition := Transition{EventType: "plan_submitted", PlanID: command.PlanID}
	return s.change(ctx, command.Actor, command.AccountID, command.AssessmentID, command.ExpectedVersion, command.CorrelationID, "plan_submitted", func(current domain.Assessment, actor domain.Actor, role accounts.MembershipRole, now time.Time) (domain.Assessment, error) {
		return current.SubmitPlan(domain.SubmitPlanCommand{PlanID: command.PlanID, Actor: actor, Role: role, ExpectedVersion: command.ExpectedVersion, At: now})
	}, transition)
}

type ApprovePlanCommand struct {
	AdvanceCommand
	PlanID            ids.BaselinePlanID
	ContentSHA256     [sha256.Size]byte
	AssessmentVersion uint64
}

func (s *Service) ApprovePlan(ctx context.Context, command ApprovePlanCommand) (domain.Assessment, error) {
	transition := Transition{EventType: "plan_approved", PlanID: command.PlanID}
	return s.change(ctx, command.Actor, command.AccountID, command.AssessmentID, command.ExpectedVersion, command.CorrelationID, "plan_approved", func(current domain.Assessment, actor domain.Actor, role accounts.MembershipRole, now time.Time) (domain.Assessment, error) {
		return current.ApprovePlan(domain.ApprovePlanCommand{PlanID: command.PlanID, PlanSHA256: command.ContentSHA256, AssessmentVersion: command.AssessmentVersion, Actor: actor, Role: role, ExpectedVersion: command.ExpectedVersion, At: now})
	}, transition)
}

func (s *Service) MarkReady(ctx context.Context, command AdvanceCommand) (domain.Assessment, error) {
	return s.change(ctx, command.Actor, command.AccountID, command.AssessmentID, command.ExpectedVersion, command.CorrelationID, "assessment_ready", func(current domain.Assessment, actor domain.Actor, role accounts.MembershipRole, now time.Time) (domain.Assessment, error) {
		return current.MarkReady(domain.MarkReadyCommand{Actor: actor, Role: role, ExpectedVersion: command.ExpectedVersion, At: now})
	}, Transition{EventType: "assessment_ready"})
}

type MaterializePlanCommand struct {
	AdvanceCommand
	PlanID            ids.BaselinePlanID
	ContentSHA256     [sha256.Size]byte
	AssessmentVersion uint64
}

// MaterializePlan creates the exact approved gap plan through the Work-owned
// command boundary. Every Work and correlation ID is derived from immutable
// plan inputs, so a partial or unknown outcome is safely completed by retrying
// this exact command.
func (s *Service) MaterializePlan(ctx context.Context, command MaterializePlanCommand) ([]workdomain.Item, error) {
	accountContext, actor, err := s.authorize(ctx, command.Actor, command.AccountID, command.AssessmentID, command.CorrelationID, true)
	if err != nil {
		return nil, err
	}
	if !canManage(accountContext.Role) {
		return nil, roleDenied()
	}
	if s.work == nil || command.ExpectedVersion == 0 {
		return nil, ErrRepository
	}
	assessment, err := s.repository.Get(ctx, command.AccountID, command.AssessmentID)
	if err != nil {
		return nil, err
	}
	if assessment.Version != command.ExpectedVersion {
		return nil, ErrConflict
	}
	if (assessment.State != domain.StateActive && assessment.State != domain.StateReady) || assessment.Plan == nil || assessment.Plan.ApprovedAt == nil ||
		assessment.Plan.ID != command.PlanID || assessment.Plan.ContentSHA256 != command.ContentSHA256 || assessment.Plan.AssessmentVersion != command.AssessmentVersion {
		return nil, ErrConstraint
	}
	planned := assessment.PlannedWork()
	if len(planned) != int(assessment.Plan.ProposedWorkCount) {
		return nil, ErrRepository
	}
	result := make([]workdomain.Item, 0, len(planned))
	for _, proposal := range planned {
		workID, deriveErr := ids.Derive(string(assessment.Plan.ID), "baseline-work:"+string(proposal.RequirementID))
		if deriveErr != nil {
			return nil, ErrRepository
		}
		correlationID, deriveErr := ids.Derive(command.CorrelationID, "baseline-work:"+string(proposal.RequirementID))
		if deriveErr != nil {
			return nil, ErrInvalid
		}
		item, createErr := s.work.Create(ctx, workapp.CreateCommand{Actor: command.Actor, AccountID: command.AccountID, RequestID: workID, Kind: workdomain.KindTodo, Title: proposal.Title, Description: proposal.Description, Priority: workdomain.PriorityNormal, Assignment: workAssignment(proposal.Responsibility), Provenance: workdomain.Provenance{Source: workdomain.SourceBaseline, CreatedBy: workdomain.Actor{Kind: workdomain.ActorUser, ID: string(actor.UserID)}, BaselineRequirementID: string(proposal.RequirementID)}, Reason: "Approved Baseline gap plan", CorrelationID: correlationID})
		if createErr != nil {
			if errors.Is(createErr, workapp.ErrConflict) {
				return nil, ErrConflict
			}
			if errors.Is(createErr, workapp.ErrInvalidCommand) || errors.Is(createErr, workapp.ErrConstraint) {
				return nil, ErrConstraint
			}
			return nil, createErr
		}
		result = append(result, item)
	}
	return result, nil
}

func workAssignment(value domain.Responsibility) workdomain.Assignment {
	switch value.Kind {
	case domain.ResponsibilityUser:
		return workdomain.Assignment{Responsibility: workdomain.ResponsibilityUser, UserID: ids.UserID(value.ID)}
	case domain.ResponsibilityPersona:
		return workdomain.Assignment{Responsibility: workdomain.ResponsibilityPersona, PersonaID: value.ID}
	default:
		return workdomain.Assignment{Responsibility: workdomain.ResponsibilityShared}
	}
}

type ReassessCommand struct {
	AdvanceCommand
	NewAssessmentID    ids.BaselineAssessmentID
	CatalogVersion     string
	ScopePolicyVersion string
}

func (s *Service) Reassess(ctx context.Context, command ReassessCommand) (domain.Assessment, domain.Assessment, error) {
	accountContext, actor, err := s.authorize(ctx, command.Actor, command.AccountID, command.AssessmentID, command.CorrelationID, true)
	if err != nil {
		return domain.Assessment{}, domain.Assessment{}, err
	}
	current, err := s.repository.Get(ctx, command.AccountID, command.AssessmentID)
	if err != nil {
		return domain.Assessment{}, domain.Assessment{}, err
	}
	archived, next, err := current.StartReassessment(domain.StartReassessmentCommand{NewAssessmentID: command.NewAssessmentID, CatalogVersion: command.CatalogVersion, ScopePolicyVersion: command.ScopePolicyVersion, Actor: actor, Role: accountContext.Role, ExpectedVersion: command.ExpectedVersion, At: s.clock.Now().UTC()})
	if err != nil {
		return domain.Assessment{}, domain.Assessment{}, classifyDomain(err)
	}
	return s.repository.Reassess(ctx, archived, next, command.ExpectedVersion, mutation(actor, "assessment_reassessed", command.CorrelationID, archived.UpdatedAt))
}

type changeFunction func(domain.Assessment, domain.Actor, accounts.MembershipRole, time.Time) (domain.Assessment, error)

func (s *Service) change(ctx context.Context, accessActor access.Actor, accountID ids.AccountID, assessmentID ids.BaselineAssessmentID, expected uint64, correlationID, reasonCode string, apply changeFunction, transition Transition) (domain.Assessment, error) {
	accountContext, actor, err := s.authorize(ctx, accessActor, accountID, assessmentID, correlationID, true)
	if err != nil {
		return domain.Assessment{}, err
	}
	current, err := s.repository.Get(ctx, accountID, assessmentID)
	if err != nil {
		return domain.Assessment{}, err
	}
	updated, err := apply(current, actor, accountContext.Role, s.clock.Now().UTC())
	if err != nil {
		return domain.Assessment{}, classifyDomain(err)
	}
	return s.repository.Update(ctx, updated, expected, transition, mutation(actor, reasonCode, correlationID, updated.UpdatedAt))
}

func (s *Service) authorize(ctx context.Context, accessActor access.Actor, accountID ids.AccountID, assessmentID ids.BaselineAssessmentID, correlationID string, write bool) (access.AccountContext, domain.Actor, error) {
	if !validBase(accessActor, accountID, assessmentID, correlationID) {
		return access.AccountContext{}, domain.Actor{}, ErrInvalid
	}
	accountContext, err := s.authorizer.Authorize(ctx, accessActor, accountID, access.Requirement{Package: catalog.PackageKnowledge, Mutation: write})
	if err != nil {
		return access.AccountContext{}, domain.Actor{}, err
	}
	return accountContext, domain.Actor{UserID: accessActor.UserID}, nil
}

func mutation(actor domain.Actor, reasonCode, correlationID string, at time.Time) Mutation {
	return Mutation{Actor: actor, ReasonCode: reasonCode, CorrelationID: correlationID, At: at}
}

func classifyDomain(err error) error {
	switch {
	case errors.Is(err, domain.ErrConflict):
		return ErrConflict
	case errors.Is(err, domain.ErrInvalid), errors.Is(err, domain.ErrState), errors.Is(err, domain.ErrPlan):
		return ErrConstraint
	case errors.Is(err, domain.ErrRole):
		return roleDenied()
	default:
		return ErrRepository
	}
}

func canManage(role accounts.MembershipRole) bool {
	return role == accounts.RoleOwner || role == accounts.RoleAdministrator
}

func roleDenied() error {
	return &access.DeniedError{Code: access.DenialRole, Package: catalog.PackageKnowledge}
}

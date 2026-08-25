package baseline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"

	workapp "github.com/tinfoyle/spyglass-engine/internal/application/work"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/baseline"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	workdomain "github.com/tinfoyle/spyglass-engine/internal/modules/work"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Service struct {
	authorizer        Authorizer
	repository        Repository
	clock             Clock
	work              WorkCreator
	facts             FactResolver
	evidence          EvidenceResolver
	workItems         WorkResolver
	sources           SourceGrantRepository
	sourceConnections SourceConnectionResolver
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

func WithFactResolver(resolver FactResolver) Option {
	return func(service *Service) error {
		if resolver == nil {
			return errors.New("Baseline Fact resolver is required")
		}
		service.facts = resolver
		return nil
	}
}

func WithEvidenceResolver(resolver EvidenceResolver) Option {
	return func(service *Service) error {
		if resolver == nil {
			return errors.New("Baseline Evidence resolver is required")
		}
		service.evidence = resolver
		return nil
	}
}

func WithWorkResolver(resolver WorkResolver) Option {
	return func(service *Service) error {
		if resolver == nil {
			return errors.New("Baseline Work resolver is required")
		}
		service.workItems = resolver
		return nil
	}
}

func WithSourceGrantRepository(repository SourceGrantRepository) Option {
	return func(service *Service) error {
		if repository == nil {
			return errors.New("Baseline Source Grant repository is required")
		}
		service.sources = repository
		return nil
	}
}

func WithSourceConnectionResolver(resolver SourceConnectionResolver) Option {
	return func(service *Service) error {
		if resolver == nil {
			return errors.New("Baseline source connection resolver is required")
		}
		service.sourceConnections = resolver
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
	Actor         access.Actor
	AccountID     ids.AccountID
	AssessmentID  ids.BaselineAssessmentID
	CorrelationID string
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
	assessment, err := domain.NewAssessment(domain.AssessmentDraft{ID: command.AssessmentID, AccountID: command.AccountID, CatalogVersion: domain.EvidenceCatalogVersion, ScopePolicyVersion: domain.ScopePolicyVersion, CreatedBy: actor}, now)
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

func (s *Service) Current(ctx context.Context, actor access.Actor, accountID ids.AccountID) (domain.Assessment, error) {
	if actor.UserID == "" || actor.WorkloadID != "" || ids.Validate(string(actor.UserID)) != nil || ids.Validate(string(accountID)) != nil {
		return domain.Assessment{}, ErrInvalid
	}
	if _, err := s.authorizer.Authorize(ctx, actor, accountID, access.Requirement{Package: catalog.PackageKnowledge}); err != nil {
		return domain.Assessment{}, err
	}
	return s.repository.Current(ctx, accountID)
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
	if _, exists := domain.BaselineQuestion(command.QuestionKey); !exists {
		return domain.Assessment{}, ErrInvalid
	}
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
		answered := make(map[string]struct{}, len(current.Answers))
		for _, answer := range current.Answers {
			answered[answer.QuestionKey] = struct{}{}
		}
		if len(domain.MissingRequiredQuestions(answered)) != 0 {
			return domain.Assessment{}, ErrConstraint
		}
		return current.BeginInventory(domain.BeginInventoryCommand{Actor: actor, Role: role, ExpectedVersion: command.ExpectedVersion, At: now})
	}, Transition{EventType: "inventory_started"})
}

type CompleteInventoryCommand struct {
	AdvanceCommand
}

func (s *Service) CompleteInventory(ctx context.Context, command CompleteInventoryCommand) (domain.Assessment, error) {
	return s.change(ctx, command.Actor, command.AccountID, command.AssessmentID, command.ExpectedVersion, command.CorrelationID, "inventory_completed", func(current domain.Assessment, actor domain.Actor, role accounts.MembershipRole, now time.Time) (domain.Assessment, error) {
		if s.facts == nil || current.CatalogVersion != domain.EvidenceCatalogVersion || current.ScopePolicyVersion != domain.ScopePolicyVersion {
			return domain.Assessment{}, ErrRepository
		}
		scopeFacts, err := s.resolveScopeFacts(ctx, current)
		if err != nil {
			return domain.Assessment{}, err
		}
		selection := domain.SelectScope(scopeFacts)
		requirements := make([]domain.RequirementDraft, 0, len(selection.Requirements))
		for _, definition := range selection.Requirements {
			requirementID, deriveErr := ids.Derive(string(current.ID), "baseline-requirement:"+definition.Code)
			if deriveErr != nil {
				return domain.Assessment{}, ErrRepository
			}
			responsibility := domain.Responsibility{Kind: domain.ResponsibilityAccount}
			if definition.Responsibility == domain.CatalogOwner {
				responsibility = domain.Responsibility{Kind: domain.ResponsibilityUser, ID: string(actor.UserID)}
			}
			requirements = append(requirements, domain.RequirementDraft{ID: ids.BaselineRequirementID(requirementID), Code: definition.Code, Title: definition.Title, Responsibility: responsibility, RenewAfterDays: definition.RenewAfterDays, CatalogVersion: selection.CatalogVersion, ScopePolicyVersion: selection.ScopePolicyVersion})
		}
		return current.CompleteInventory(domain.CompleteInventoryCommand{Requirements: requirements, Actor: actor, Role: role, ExpectedVersion: command.ExpectedVersion, At: now})
	}, Transition{EventType: "inventory_completed"})
}

func (s *Service) resolveScopeFacts(ctx context.Context, assessment domain.Assessment) (domain.ScopeFacts, error) {
	references := make([]domain.FactReference, 0, len(assessment.Answers))
	answerKeys := make(map[domain.FactReference]string)
	for _, answer := range assessment.Answers {
		if answer.Fact != nil {
			if _, exists := answerKeys[*answer.Fact]; exists {
				return domain.ScopeFacts{}, ErrConstraint
			}
			references = append(references, *answer.Fact)
			answerKeys[*answer.Fact] = answer.QuestionKey
		}
	}
	resolved, err := s.facts.Resolve(ctx, assessment.AccountID, references)
	if err != nil {
		return domain.ScopeFacts{}, err
	}
	values := make(map[string]string, len(resolved))
	for _, fact := range resolved {
		questionKey, exists := answerKeys[fact.Reference]
		if !exists || fact.Key != questionKey {
			return domain.ScopeFacts{}, ErrConstraint
		}
		if !json.Valid(fact.CanonicalValue) {
			return domain.ScopeFacts{}, ErrRepository
		}
		decoder := json.NewDecoder(bytes.NewReader(fact.CanonicalValue))
		decoder.UseNumber()
		var value any
		if decoder.Decode(&value) != nil {
			return domain.ScopeFacts{}, ErrRepository
		}
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) == "" {
				return domain.ScopeFacts{}, ErrConstraint
			}
			values[questionKey] = typed
		case json.Number:
			if questionKey != "organization.team_size" {
				return domain.ScopeFacts{}, ErrConstraint
			}
			values[questionKey] = typed.String()
		default:
			return domain.ScopeFacts{}, ErrConstraint
		}
	}
	if len(resolved) != len(references) {
		return domain.ScopeFacts{}, ErrConstraint
	}
	return domain.ScopeFacts{Industry: values["organization.industry"], Services: values["organization.services"], TeamSize: values["organization.team_size"], ImmediateConcern: values["baseline.immediate_concern"]}, nil
}

type DecideEvidenceCommand struct {
	AdvanceCommand
	RequirementID ids.BaselineRequirementID
	EvidenceID    ids.KnowledgeEvidenceID
	Decision      domain.EvidenceDecisionKind
	Reason        string
}

func (s *Service) DecideEvidence(ctx context.Context, command DecideEvidenceCommand) (domain.Assessment, error) {
	if s.evidence == nil {
		return domain.Assessment{}, ErrRepository
	}
	evidence, err := s.evidence.ResolveEvidence(ctx, command.AccountID, command.EvidenceID)
	if err != nil {
		return domain.Assessment{}, err
	}
	if evidence.ID != command.EvidenceID || !evidence.Kind.Valid() || (command.Decision == domain.EvidenceAccepted && (evidence.Kind == knowledge.SourceAgentDerivation || evidence.Kind == knowledge.SourceIntegrationRecord)) {
		return domain.Assessment{}, ErrConstraint
	}
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

type ConfirmWorkEvidenceCommand struct {
	AdvanceCommand
	RequirementID ids.BaselineRequirementID
	WorkItemID    ids.WorkItemID
	EvidenceID    ids.KnowledgeEvidenceID
	Reason        string
}

func (s *Service) ConfirmWorkEvidence(ctx context.Context, command ConfirmWorkEvidenceCommand) (domain.Assessment, error) {
	if s.evidence == nil || s.workItems == nil {
		return domain.Assessment{}, ErrRepository
	}
	evidence, err := s.evidence.ResolveEvidence(ctx, command.AccountID, command.EvidenceID)
	if err != nil {
		return domain.Assessment{}, err
	}
	if evidence.ID != command.EvidenceID || !evidence.Kind.Valid() || evidence.Kind == knowledge.SourceAgentDerivation || evidence.Kind == knowledge.SourceIntegrationRecord {
		return domain.Assessment{}, ErrConstraint
	}
	work, err := s.workItems.ResolveWork(ctx, command.AccountID, command.WorkItemID)
	if err != nil {
		return domain.Assessment{}, err
	}
	if work.ID != command.WorkItemID || work.State != workdomain.StateDone || work.CompletedAt == nil || work.BaselineRequirementID != command.RequirementID {
		return domain.Assessment{}, ErrConstraint
	}
	transition := Transition{EventType: "work_evidence_confirmed", RequirementID: command.RequirementID}
	return s.change(ctx, command.Actor, command.AccountID, command.AssessmentID, command.ExpectedVersion, command.CorrelationID, "work_evidence_confirmed", func(current domain.Assessment, actor domain.Actor, role accounts.MembershipRole, now time.Time) (domain.Assessment, error) {
		return current.ConfirmLinkedWork(domain.ConfirmLinkedWorkCommand{RequirementID: command.RequirementID, WorkItemID: command.WorkItemID, WorkCompletedAt: *work.CompletedAt, Decision: domain.EvidenceDecision{EvidenceID: command.EvidenceID, Decision: domain.EvidenceAccepted, Reason: command.Reason, DecidedBy: actor, DecidedAt: now}, Role: role, ExpectedVersion: command.ExpectedVersion})
	}, transition)
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
	planned := append([]domain.PlannedWork(nil), assessment.Plan.Work...)
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

type MaterializeMaintenanceCommand struct {
	AdvanceCommand
}

const MaintenanceWorkloadID = "baseline-maintenance-worker"

type MaterializeMaintenanceWorkloadCommand struct {
	Actor           access.Actor
	AccountID       ids.AccountID
	AssessmentID    ids.BaselineAssessmentID
	ExpectedVersion uint64
	CorrelationID   string
}

// MaterializeMaintenance creates only obligations inside the governed 30-day
// window. IDs include the immutable due instant, so retries and later renewal
// cycles cannot duplicate one another.
func (s *Service) MaterializeMaintenance(ctx context.Context, command MaterializeMaintenanceCommand) ([]workdomain.Item, error) {
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
	return s.materializeMaintenance(ctx, command.Actor, command.AccountID, command.CorrelationID, assessment, workdomain.Actor{Kind: workdomain.ActorUser, ID: string(actor.UserID)})
}

// MaterializeMaintenanceWorkload is the only unattended Baseline mutation
// boundary. It creates deterministic Work but cannot change an assessment,
// decide evidence, mark readiness, or impersonate the assessment owner.
func (s *Service) MaterializeMaintenanceWorkload(ctx context.Context, command MaterializeMaintenanceWorkloadCommand) ([]workdomain.Item, error) {
	if command.Actor.UserID != "" || command.Actor.WorkloadID != MaintenanceWorkloadID || ids.Validate(string(command.AccountID)) != nil || ids.Validate(string(command.AssessmentID)) != nil || ids.Validate(command.CorrelationID) != nil || command.ExpectedVersion == 0 || s.work == nil {
		return nil, ErrInvalid
	}
	if _, err := s.authorizer.Authorize(ctx, command.Actor, command.AccountID, access.Requirement{Package: catalog.PackageKnowledge, Mutation: true}); err != nil {
		return nil, err
	}
	assessment, err := s.repository.Get(ctx, command.AccountID, command.AssessmentID)
	if err != nil {
		return nil, err
	}
	if assessment.Version != command.ExpectedVersion {
		return nil, ErrConflict
	}
	return s.materializeMaintenance(ctx, command.Actor, command.AccountID, command.CorrelationID, assessment, workdomain.Actor{Kind: workdomain.ActorWorkload, ID: MaintenanceWorkloadID})
}

func (s *Service) materializeMaintenance(ctx context.Context, accessActor access.Actor, accountID ids.AccountID, correlationRoot string, assessment domain.Assessment, provenanceActor workdomain.Actor) ([]workdomain.Item, error) {
	now := s.clock.Now().UTC()
	planned := assessment.MaintenanceWork(now)
	result := make([]workdomain.Item, 0, len(planned))
	for _, proposal := range planned {
		key := string(proposal.Kind) + ":" + proposal.DueAt.UTC().Format(time.RFC3339Nano)
		if proposal.RequirementID != "" {
			key += ":" + string(proposal.RequirementID)
		}
		workID, deriveErr := ids.Derive(string(assessment.ID), "baseline-maintenance:"+key)
		if deriveErr != nil {
			return nil, ErrRepository
		}
		correlationID, deriveErr := ids.Derive(correlationRoot, "baseline-maintenance:"+key)
		if deriveErr != nil {
			return nil, ErrInvalid
		}
		priority := workdomain.PriorityHigh
		if !now.Before(proposal.DueAt) {
			priority = workdomain.PriorityUrgent
		}
		item, createErr := s.work.Create(ctx, workapp.CreateCommand{Actor: accessActor, AccountID: accountID, RequestID: workID, Kind: workdomain.KindTodo, Title: proposal.Title, Description: proposal.Description, Priority: priority, Assignment: workAssignment(proposal.Responsibility), Provenance: workdomain.Provenance{Source: workdomain.SourceBaseline, CreatedBy: provenanceActor, BaselineRequirementID: string(proposal.RequirementID)}, DueAt: &proposal.DueAt, Reason: "Scheduled Baseline maintenance", CorrelationID: correlationID})
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
	NewAssessmentID ids.BaselineAssessmentID
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
	archived, next, err := current.StartReassessment(domain.StartReassessmentCommand{NewAssessmentID: command.NewAssessmentID, CatalogVersion: domain.EvidenceCatalogVersion, ScopePolicyVersion: domain.ScopePolicyVersion, Actor: actor, Role: accountContext.Role, ExpectedVersion: command.ExpectedVersion, At: s.clock.Now().UTC()})
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
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrNotFound), errors.Is(err, ErrConflict), errors.Is(err, ErrConstraint), errors.Is(err, ErrRepository):
		return err
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

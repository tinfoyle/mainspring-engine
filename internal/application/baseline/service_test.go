package baseline

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	workapp "github.com/tinfoyle/spyglass-engine/internal/application/work"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/baseline"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	workdomain "github.com/tinfoyle/spyglass-engine/internal/modules/work"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestServiceRunsAuthorizedVersionBoundLifecycle(t *testing.T) {
	ctx := context.Background()
	clock := &baselineClock{now: time.Date(2026, 8, 22, 18, 0, 0, 0, time.UTC)}
	repository := &baselineRepository{items: map[ids.BaselineAssessmentID]domain.Assessment{}}
	authorizer := &baselineAuthorizer{role: accounts.RoleOwner}
	actor := access.Actor{UserID: "a1000000-0000-4000-8000-000000000001"}
	accountID := ids.AccountID("a2000000-0000-4000-8000-000000000002")
	assessmentID := ids.BaselineAssessmentID("a3000000-0000-4000-8000-000000000003")
	operation := func(label string) string {
		value, deriveErr := ids.Derive(string(assessmentID), label)
		if deriveErr != nil {
			t.Fatal(deriveErr)
		}
		return value
	}
	fact := &domain.FactReference{FactID: "a5000000-0000-4000-8000-000000000005", Revision: 2}
	resolver := &baselineFactResolver{values: map[domain.FactReference]ResolvedFact{
		*fact: {Reference: *fact, Key: "organization.industry", CanonicalValue: []byte(`"professional services"`)},
	}}
	service, err := New(authorizer, repository, clock, WithFactResolver(resolver))
	if err != nil {
		t.Fatal(err)
	}
	assessment, err := service.Start(ctx, StartCommand{Actor: actor, AccountID: accountID, AssessmentID: assessmentID, CorrelationID: operation("start")})
	if err != nil || assessment.State != domain.StateInterview {
		t.Fatalf("start=%+v err=%v", assessment, err)
	}
	if _, err := service.Answer(ctx, AnswerCommand{Actor: actor, AccountID: accountID, AssessmentID: assessmentID, QuestionKey: "caller.selected.question", Kind: domain.AnswerUnknown, Reason: "This is not a governed question", ExpectedVersion: assessment.Version, CorrelationID: operation("unknown-question")}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown question error=%v", err)
	}
	if _, err := service.BeginInventory(ctx, AdvanceCommand{Actor: actor, AccountID: accountID, AssessmentID: assessmentID, ExpectedVersion: assessment.Version, CorrelationID: operation("premature-inventory")}); !errors.Is(err, ErrConstraint) {
		t.Fatalf("premature inventory error=%v", err)
	}
	for _, question := range domain.BaselineQuestions() {
		if question.Optional {
			continue
		}
		clock.advance()
		command := AnswerCommand{Actor: actor, AccountID: accountID, AssessmentID: assessmentID, QuestionKey: question.Key, Kind: domain.AnswerUnknown, Reason: "Confirmed information is not yet available", ExpectedVersion: assessment.Version, CorrelationID: operation("answer:" + question.Key)}
		if question.Key == "organization.industry" {
			command.Kind, command.Fact, command.Reason = domain.AnswerFact, fact, ""
		}
		assessment, err = service.Answer(ctx, command)
		if err != nil {
			t.Fatal(err)
		}
	}
	clock.advance()
	assessment, err = service.BeginInventory(ctx, AdvanceCommand{Actor: actor, AccountID: accountID, AssessmentID: assessmentID, ExpectedVersion: assessment.Version, CorrelationID: operation("begin-inventory")})
	if err != nil {
		t.Fatal(err)
	}
	clock.advance()
	resolvedIndustry := resolver.values[*fact]
	wrongKey := resolvedIndustry
	wrongKey.Key = "organization.services"
	resolver.values[*fact] = wrongKey
	if _, err := service.CompleteInventory(ctx, CompleteInventoryCommand{AdvanceCommand: AdvanceCommand{Actor: actor, AccountID: accountID, AssessmentID: assessmentID, ExpectedVersion: assessment.Version, CorrelationID: operation("mismatched-fact")}}); !errors.Is(err, ErrConstraint) {
		t.Fatalf("mismatched fact error=%v", err)
	}
	resolver.values[*fact] = resolvedIndustry
	assessment, err = service.CompleteInventory(ctx, CompleteInventoryCommand{AdvanceCommand: AdvanceCommand{Actor: actor, AccountID: accountID, AssessmentID: assessmentID, ExpectedVersion: assessment.Version, CorrelationID: operation("complete-inventory")}})
	if err != nil || len(assessment.Requirements) != 13 || assessment.Requirements[0].CatalogVersion != domain.EvidenceCatalogVersion || assessment.Requirements[0].ScopePolicyVersion != domain.ScopePolicyVersion {
		t.Fatalf("inventory=%+v err=%v", assessment, err)
	}
	requirementID := assessment.Requirements[0].ID
	clock.advance()
	assessment, err = service.DecideEvidence(ctx, DecideEvidenceCommand{AdvanceCommand: AdvanceCommand{Actor: actor, AccountID: accountID, AssessmentID: assessmentID, ExpectedVersion: assessment.Version, CorrelationID: operation("evidence")}, RequirementID: requirementID, EvidenceID: "a7000000-0000-4000-8000-000000000007", Decision: domain.EvidenceAccepted, Reason: "Current verified formation record"})
	if err != nil {
		t.Fatal(err)
	}
	for _, requirement := range assessment.Requirements[1:] {
		clock.advance()
		assessment, err = service.Disposition(ctx, DispositionCommand{AdvanceCommand: AdvanceCommand{Actor: actor, AccountID: accountID, AssessmentID: assessmentID, ExpectedVersion: assessment.Version, CorrelationID: operation("disposition:" + requirement.Code)}, RequirementID: requirement.ID, Disposition: domain.DispositionNotApplicable, Reason: "Explicitly reviewed for lifecycle coverage"})
		if err != nil {
			t.Fatal(err)
		}
	}
	clock.advance()
	planID := ids.BaselinePlanID("a8000000-0000-4000-8000-000000000008")
	assessment, err = service.SubmitPlan(ctx, SubmitPlanCommand{AdvanceCommand: AdvanceCommand{Actor: actor, AccountID: accountID, AssessmentID: assessmentID, ExpectedVersion: assessment.Version, CorrelationID: operation("submit-plan")}, PlanID: planID})
	if err != nil {
		t.Fatal(err)
	}
	digest := assessment.Plan.ContentSHA256
	clock.advance()
	authorizer.role = accounts.RoleMember
	_, err = service.ApprovePlan(ctx, ApprovePlanCommand{AdvanceCommand: AdvanceCommand{Actor: actor, AccountID: accountID, AssessmentID: assessmentID, ExpectedVersion: assessment.Version, CorrelationID: "b4000000-0000-4000-8000-000000000001"}, PlanID: planID, ContentSHA256: digest, AssessmentVersion: assessment.Plan.AssessmentVersion})
	if !access.IsDenied(err, access.DenialRole) {
		t.Fatalf("member approved plan: %v", err)
	}
	authorizer.role = accounts.RoleOwner
	wrong := sha256.Sum256([]byte("changed"))
	_, err = service.ApprovePlan(ctx, ApprovePlanCommand{AdvanceCommand: AdvanceCommand{Actor: actor, AccountID: accountID, AssessmentID: assessmentID, ExpectedVersion: assessment.Version, CorrelationID: "b4000000-0000-4000-8000-000000000002"}, PlanID: planID, ContentSHA256: wrong, AssessmentVersion: assessment.Plan.AssessmentVersion})
	if !errors.Is(err, ErrConstraint) {
		t.Fatalf("changed plan approved: %v", err)
	}
	assessment, err = service.ApprovePlan(ctx, ApprovePlanCommand{AdvanceCommand: AdvanceCommand{Actor: actor, AccountID: accountID, AssessmentID: assessmentID, ExpectedVersion: assessment.Version, CorrelationID: "b4000000-0000-4000-8000-000000000003"}, PlanID: planID, ContentSHA256: digest, AssessmentVersion: assessment.Plan.AssessmentVersion})
	if err != nil || assessment.State != domain.StateActive {
		t.Fatalf("approve=%+v err=%v", assessment, err)
	}
	clock.advance()
	assessment, err = service.MarkReady(ctx, AdvanceCommand{Actor: actor, AccountID: accountID, AssessmentID: assessmentID, ExpectedVersion: assessment.Version, CorrelationID: "b4000000-0000-4000-8000-000000000004"})
	if err != nil {
		t.Fatal(err)
	}
	clock.advance()
	archived, next, err := service.Reassess(ctx, ReassessCommand{AdvanceCommand: AdvanceCommand{Actor: actor, AccountID: accountID, AssessmentID: assessmentID, ExpectedVersion: assessment.Version, CorrelationID: "b4000000-0000-4000-8000-000000000005"}, NewAssessmentID: "a9000000-0000-4000-8000-000000000009"})
	if err != nil || archived.State != domain.StateArchived || next.State != domain.StateInterview || next.CatalogVersion != domain.EvidenceCatalogVersion || next.ScopePolicyVersion != domain.ScopePolicyVersion || repository.events[len(repository.events)-1] != "reassessed" {
		t.Fatalf("reassess archived=%+v next=%+v events=%v err=%v", archived, next, repository.events, err)
	}
}

func TestServiceMaterializesApprovedGapPlanThroughDeterministicWorkCommands(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 22, 21, 0, 0, 0, time.UTC)
	actor := domain.Actor{UserID: "c1000000-0000-4000-8000-000000000001"}
	accessActor := access.Actor{UserID: actor.UserID}
	accountID := ids.AccountID("c2000000-0000-4000-8000-000000000002")
	assessmentID := ids.BaselineAssessmentID("c3000000-0000-4000-8000-000000000003")
	requirementID := ids.BaselineRequirementID("c4000000-0000-4000-8000-000000000004")
	planID := ids.BaselinePlanID("c5000000-0000-4000-8000-000000000005")
	assessment, err := domain.NewAssessment(domain.AssessmentDraft{ID: assessmentID, AccountID: accountID, CatalogVersion: "catalog-v1", ScopePolicyVersion: "scope-v1", CreatedBy: actor}, now)
	if err != nil {
		t.Fatal(err)
	}
	assessment, err = assessment.AnswerInterview(domain.AnswerInterviewCommand{Answer: domain.InterviewAnswer{QuestionKey: "organization.formation", Kind: domain.AnswerUnknown, Reason: "Formation record is missing", AnsweredBy: actor, AnsweredAt: now.Add(time.Minute)}, Role: accounts.RoleOwner, ExpectedVersion: assessment.Version})
	if err == nil {
		assessment, err = assessment.BeginInventory(domain.BeginInventoryCommand{Actor: actor, Role: accounts.RoleOwner, ExpectedVersion: assessment.Version, At: now.Add(2 * time.Minute)})
	}
	if err == nil {
		assessment, err = assessment.CompleteInventory(domain.CompleteInventoryCommand{Requirements: []domain.RequirementDraft{{ID: requirementID, Code: "legal.formation", Title: "Obtain formation record", Responsibility: domain.Responsibility{Kind: domain.ResponsibilityAccount}, CatalogVersion: "catalog-v1", ScopePolicyVersion: "scope-v1"}}, Actor: actor, Role: accounts.RoleOwner, ExpectedVersion: assessment.Version, At: now.Add(3 * time.Minute)})
	}
	if err == nil {
		assessment, err = assessment.DispositionRequirement(domain.DispositionRequirementCommand{RequirementID: requirementID, Disposition: domain.DispositionGap, Reason: "Formation record is not available", Actor: actor, Role: accounts.RoleOwner, ExpectedVersion: assessment.Version, At: now.Add(4 * time.Minute)})
	}
	if err == nil {
		assessment, err = assessment.SubmitPlan(domain.SubmitPlanCommand{PlanID: planID, Actor: actor, Role: accounts.RoleOwner, ExpectedVersion: assessment.Version, At: now.Add(5 * time.Minute)})
	}
	if err == nil {
		assessment, err = assessment.ApprovePlan(domain.ApprovePlanCommand{PlanID: planID, PlanSHA256: assessment.Plan.ContentSHA256, AssessmentVersion: assessment.Plan.AssessmentVersion, Actor: actor, Role: accounts.RoleOwner, ExpectedVersion: assessment.Version, At: now.Add(6 * time.Minute)})
	}
	if err != nil {
		t.Fatal(err)
	}
	repository := &baselineRepository{items: map[ids.BaselineAssessmentID]domain.Assessment{assessmentID: assessment}}
	creator := &baselineWorkCreator{now: now.Add(7 * time.Minute)}
	service, err := New(&baselineAuthorizer{role: accounts.RoleOwner}, repository, &baselineClock{now: creator.now}, WithWorkCreator(creator))
	if err != nil {
		t.Fatal(err)
	}
	command := MaterializePlanCommand{AdvanceCommand: AdvanceCommand{Actor: accessActor, AccountID: accountID, AssessmentID: assessmentID, ExpectedVersion: assessment.Version, CorrelationID: "c6000000-0000-4000-8000-000000000006"}, PlanID: planID, ContentSHA256: assessment.Plan.ContentSHA256, AssessmentVersion: assessment.Plan.AssessmentVersion}
	items, err := service.MaterializePlan(ctx, command)
	if err != nil || len(items) != 1 || len(creator.commands) != 1 {
		t.Fatalf("items=%+v commands=%+v err=%v", items, creator.commands, err)
	}
	first := creator.commands[0]
	if first.Provenance.Source != workdomain.SourceBaseline || first.Provenance.BaselineRequirementID != string(requirementID) || first.Assignment.Responsibility != workdomain.ResponsibilityShared || first.Title != "Obtain formation record" || first.Description != "Formation record is not available" {
		t.Fatalf("command=%+v", first)
	}
	if _, err := service.MaterializePlan(ctx, command); err != nil || creator.commands[1].RequestID != first.RequestID || creator.commands[1].CorrelationID != first.CorrelationID {
		t.Fatalf("replay commands=%+v err=%v", creator.commands, err)
	}
}

type baselineClock struct{ now time.Time }

type baselineFactResolver struct {
	values map[domain.FactReference]ResolvedFact
}

func (resolver *baselineFactResolver) Resolve(_ context.Context, _ ids.AccountID, references []domain.FactReference) ([]ResolvedFact, error) {
	result := make([]ResolvedFact, 0, len(references))
	for _, reference := range references {
		value, exists := resolver.values[reference]
		if !exists {
			return nil, ErrNotFound
		}
		result = append(result, value)
	}
	return result, nil
}

func (clock *baselineClock) Now() time.Time { return clock.now }
func (clock *baselineClock) advance()       { clock.now = clock.now.Add(time.Minute) }

type baselineAuthorizer struct{ role accounts.MembershipRole }

func (authorizer *baselineAuthorizer) Authorize(_ context.Context, actor access.Actor, accountID ids.AccountID, requirement access.Requirement) (access.AccountContext, error) {
	if !actor.Valid() || accountID == "" || requirement.Package != catalog.PackageKnowledge {
		return access.AccountContext{}, &access.DeniedError{Code: access.DenialUnauthenticated}
	}
	return access.AccountContext{AccountID: accountID, Role: authorizer.role}, nil
}

type baselineRepository struct {
	items  map[ids.BaselineAssessmentID]domain.Assessment
	events []string
}

type baselineWorkCreator struct {
	now      time.Time
	commands []workapp.CreateCommand
}

func (creator *baselineWorkCreator) Create(_ context.Context, command workapp.CreateCommand) (workdomain.Item, error) {
	creator.commands = append(creator.commands, command)
	draft, err := workdomain.NewDraft(workdomain.Draft{ID: ids.WorkItemID(command.RequestID), AccountID: command.AccountID, Kind: command.Kind, Title: command.Title, Description: command.Description, Priority: command.Priority, Assignment: command.Assignment, Provenance: command.Provenance, CapacityReservationID: command.RequestID})
	if err != nil {
		return workdomain.Item{}, err
	}
	return workdomain.Materialize(draft, 1, 0, creator.now)
}

func (repository *baselineRepository) Create(_ context.Context, value domain.Assessment, _ Mutation) (domain.Assessment, error) {
	if _, exists := repository.items[value.ID]; exists {
		return domain.Assessment{}, ErrConflict
	}
	repository.items[value.ID] = value
	repository.events = append(repository.events, "created")
	return value, nil
}

func (repository *baselineRepository) Get(_ context.Context, accountID ids.AccountID, assessmentID ids.BaselineAssessmentID) (domain.Assessment, error) {
	value, exists := repository.items[assessmentID]
	if !exists || value.AccountID != accountID {
		return domain.Assessment{}, ErrNotFound
	}
	return value, nil
}

func (repository *baselineRepository) Update(_ context.Context, value domain.Assessment, expected uint64, transition Transition, _ Mutation) (domain.Assessment, error) {
	current, exists := repository.items[value.ID]
	if !exists {
		return domain.Assessment{}, ErrNotFound
	}
	if current.Version != expected {
		return domain.Assessment{}, ErrConflict
	}
	repository.items[value.ID] = value
	repository.events = append(repository.events, transition.EventType)
	return value, nil
}

func (repository *baselineRepository) Reassess(_ context.Context, archived, next domain.Assessment, expected uint64, _ Mutation) (domain.Assessment, domain.Assessment, error) {
	if repository.items[archived.ID].Version != expected {
		return domain.Assessment{}, domain.Assessment{}, ErrConflict
	}
	repository.items[archived.ID], repository.items[next.ID] = archived, next
	repository.events = append(repository.events, "reassessed")
	return archived, next, nil
}

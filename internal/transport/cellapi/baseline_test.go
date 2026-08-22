package cellapi

import (
	"context"
	"crypto/sha256"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	baselineapp "github.com/tinfoyle/spyglass-engine/internal/application/baseline"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	baselinedomain "github.com/tinfoyle/spyglass-engine/internal/modules/baseline"
	workdomain "github.com/tinfoyle/spyglass-engine/internal/modules/work"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type baselineServiceStub struct {
	start       func(context.Context, baselineapp.StartCommand) (baselinedomain.Assessment, error)
	get         func(context.Context, access.Actor, ids.AccountID, ids.BaselineAssessmentID) (baselinedomain.Assessment, error)
	answer      func(context.Context, baselineapp.AnswerCommand) (baselinedomain.Assessment, error)
	materialize func(context.Context, baselineapp.MaterializePlanCommand) ([]workdomain.Item, error)
}

func (stub baselineServiceStub) Start(ctx context.Context, command baselineapp.StartCommand) (baselinedomain.Assessment, error) {
	return stub.start(ctx, command)
}
func (stub baselineServiceStub) Get(ctx context.Context, actor access.Actor, accountID ids.AccountID, assessmentID ids.BaselineAssessmentID) (baselinedomain.Assessment, error) {
	return stub.get(ctx, actor, accountID, assessmentID)
}
func (stub baselineServiceStub) Answer(ctx context.Context, command baselineapp.AnswerCommand) (baselinedomain.Assessment, error) {
	return stub.answer(ctx, command)
}
func (stub baselineServiceStub) BeginInventory(context.Context, baselineapp.AdvanceCommand) (baselinedomain.Assessment, error) {
	panic("unexpected BeginInventory")
}
func (stub baselineServiceStub) CompleteInventory(context.Context, baselineapp.CompleteInventoryCommand) (baselinedomain.Assessment, error) {
	panic("unexpected CompleteInventory")
}
func (stub baselineServiceStub) DecideEvidence(context.Context, baselineapp.DecideEvidenceCommand) (baselinedomain.Assessment, error) {
	panic("unexpected DecideEvidence")
}
func (stub baselineServiceStub) Disposition(context.Context, baselineapp.DispositionCommand) (baselinedomain.Assessment, error) {
	panic("unexpected Disposition")
}
func (stub baselineServiceStub) SubmitPlan(context.Context, baselineapp.SubmitPlanCommand) (baselinedomain.Assessment, error) {
	panic("unexpected SubmitPlan")
}
func (stub baselineServiceStub) ApprovePlan(context.Context, baselineapp.ApprovePlanCommand) (baselinedomain.Assessment, error) {
	panic("unexpected ApprovePlan")
}
func (stub baselineServiceStub) MaterializePlan(ctx context.Context, command baselineapp.MaterializePlanCommand) ([]workdomain.Item, error) {
	return stub.materialize(ctx, command)
}
func (stub baselineServiceStub) MarkReady(context.Context, baselineapp.AdvanceCommand) (baselinedomain.Assessment, error) {
	panic("unexpected MarkReady")
}
func (stub baselineServiceStub) Reassess(context.Context, baselineapp.ReassessCommand) (baselinedomain.Assessment, baselinedomain.Assessment, error) {
	panic("unexpected Reassess")
}

func newBaselineServer(t *testing.T, service BaselineService) *Server {
	t.Helper()
	server, err := New(claimAcceptor{claims: attentionClaims("knowledge")}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithBaseline(service))
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func TestBaselineStartBindsAssessmentToRoutedOperation(t *testing.T) {
	now := time.Date(2026, 8, 22, 20, 0, 0, 0, time.UTC)
	service := baselineServiceStub{start: func(ctx context.Context, command baselineapp.StartCommand) (baselinedomain.Assessment, error) {
		claims, ok := routecontext.FromContext(ctx)
		if !ok || claims.Authority.AccountID != attentionAccount || command.Actor.UserID != attentionUser || command.AccountID != attentionAccount || string(command.AssessmentID) != attentionOperation || command.CorrelationID != attentionOperation || command.CatalogVersion != "catalog-v1" || command.ScopePolicyVersion != "scope-v1" {
			t.Fatalf("claims=%+v command=%+v", claims, command)
		}
		return baselinedomain.NewAssessment(baselinedomain.AssessmentDraft{ID: command.AssessmentID, AccountID: command.AccountID, CatalogVersion: command.CatalogVersion, ScopePolicyVersion: command.ScopePolicyVersion, CreatedBy: baselinedomain.Actor{UserID: command.Actor.UserID}}, now)
	}}
	response := attentionMutation(t, newBaselineServer(t, service).Handler(), http.MethodPost, "/api/v1/accounts/"+attentionAccount+"/baseline-assessments", `{"catalog_version":"catalog-v1","scope_policy_version":"scope-v1"}`, attentionOperation, "")
	if response.Code != http.StatusCreated || response.Header().Get("ETag") != `W/"1"` || !strings.HasSuffix(response.Header().Get("Location"), "/"+attentionOperation) || !strings.Contains(response.Body.String(), `"state":"interview"`) {
		t.Fatalf("response=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
}

func TestBaselineAnswerRequiresVersionAndPreservesExactFactReference(t *testing.T) {
	now := time.Date(2026, 8, 22, 20, 0, 0, 0, time.UTC)
	assessmentID := ids.BaselineAssessmentID(attentionFact)
	factID := ids.KnowledgeFactID(attentionWork)
	service := baselineServiceStub{answer: func(_ context.Context, command baselineapp.AnswerCommand) (baselinedomain.Assessment, error) {
		if command.AssessmentID != assessmentID || command.ExpectedVersion != 1 || command.QuestionKey != "organization.legal_name" || command.Kind != baselinedomain.AnswerFact || command.Fact == nil || command.Fact.FactID != factID || command.Fact.Revision != 3 {
			t.Fatalf("command=%+v", command)
		}
		assessment, err := baselinedomain.NewAssessment(baselinedomain.AssessmentDraft{ID: assessmentID, AccountID: command.AccountID, CatalogVersion: "catalog-v1", ScopePolicyVersion: "scope-v1", CreatedBy: baselinedomain.Actor{UserID: command.Actor.UserID}}, now)
		if err != nil {
			return baselinedomain.Assessment{}, err
		}
		return assessment.AnswerInterview(baselinedomain.AnswerInterviewCommand{Answer: baselinedomain.InterviewAnswer{QuestionKey: command.QuestionKey, Kind: command.Kind, Fact: command.Fact, AnsweredBy: baselinedomain.Actor{UserID: command.Actor.UserID}, AnsweredAt: now.Add(time.Minute)}, Role: "owner", ExpectedVersion: 1})
	}}
	path := "/api/v1/accounts/" + attentionAccount + "/baseline-assessments/" + string(assessmentID) + "/answers"
	body := `{"question_key":"organization.legal_name","kind":"fact","fact":{"fact_id":"` + string(factID) + `","revision":3}}`
	missingVersion := attentionMutation(t, newBaselineServer(t, service).Handler(), http.MethodPost, path, body, attentionOperation, "")
	if missingVersion.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing version=%d %s", missingVersion.Code, missingVersion.Body.String())
	}
	response := attentionMutation(t, newBaselineServer(t, service).Handler(), http.MethodPost, path, body, attentionOperation, `W/"1"`)
	if response.Code != http.StatusOK || response.Header().Get("ETag") != `W/"2"` || !strings.Contains(response.Body.String(), `"revision":3`) {
		t.Fatalf("response=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
}

func TestBaselineMaterializationBindsApprovedPlanAndReturnsWork(t *testing.T) {
	now := time.Date(2026, 8, 22, 22, 0, 0, 0, time.UTC)
	assessmentID := ids.BaselineAssessmentID(attentionFact)
	planID := ids.BaselinePlanID(attentionInvocation)
	requirementID := ids.BaselineRequirementID(attentionWork)
	digest := sha256.Sum256([]byte("derived gap plan"))
	service := baselineServiceStub{materialize: func(_ context.Context, command baselineapp.MaterializePlanCommand) ([]workdomain.Item, error) {
		if command.AssessmentID != assessmentID || command.ExpectedVersion != 8 || command.PlanID != planID || command.ContentSHA256 != digest || command.AssessmentVersion != 6 || command.CorrelationID != attentionOperation {
			t.Fatalf("command=%+v", command)
		}
		draft, err := workdomain.NewDraft(workdomain.Draft{ID: ids.WorkItemID(attentionOperation), AccountID: command.AccountID, Kind: workdomain.KindTodo, Title: "Obtain formation record", Description: "Formation record is unavailable", Priority: workdomain.PriorityNormal, Assignment: workdomain.Assignment{Responsibility: workdomain.ResponsibilityShared}, Provenance: workdomain.Provenance{Source: workdomain.SourceBaseline, CreatedBy: workdomain.Actor{Kind: workdomain.ActorUser, ID: string(attentionUser)}, BaselineRequirementID: string(requirementID)}, CapacityReservationID: attentionOperation})
		if err != nil {
			return nil, err
		}
		item, err := workdomain.Materialize(draft, 42, 0, now)
		return []workdomain.Item{item}, err
	}}
	body := `{"plan_id":"` + string(planID) + `","content_sha256":"` + fmtDigest(digest) + `","assessment_version":6}`
	path := "/api/v1/accounts/" + attentionAccount + "/baseline-assessments/" + string(assessmentID) + "/work-materializations"
	response := attentionMutation(t, newBaselineServer(t, service).Handler(), http.MethodPost, path, body, attentionOperation, `W/"8"`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"number":42`) || !strings.Contains(response.Body.String(), `"source":"baseline"`) {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
}

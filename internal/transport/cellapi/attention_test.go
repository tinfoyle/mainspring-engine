package cellapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	attentionapp "github.com/tinfoyle/spyglass-engine/internal/application/attention"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	attentiondomain "github.com/tinfoyle/spyglass-engine/internal/modules/attention"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

const (
	attentionAccount    = "10000000-0000-4000-8000-000000000001"
	attentionUser       = "20000000-0000-4000-8000-000000000002"
	attentionOperation  = "30000000-0000-4000-8000-000000000003"
	attentionWork       = "40000000-0000-4000-8000-000000000004"
	attentionInvocation = "50000000-0000-4000-8000-000000000005"
	attentionFact       = "60000000-0000-4000-8000-000000000006"
)

func TestAttentionCreateUsesRoutedAuthorityAndOperation(t *testing.T) {
	now := time.Date(2026, 8, 21, 17, 0, 0, 0, time.UTC)
	service := &attentionServiceStub{}
	service.createInformation = func(ctx context.Context, command attentionapp.CreateInformationCommand) (attentiondomain.InformationRequest, error) {
		claims, ok := routecontext.FromContext(ctx)
		if !ok || claims.Authority.OperationID != attentionOperation || claims.Authority.ActorID != attentionUser {
			t.Fatalf("claims=%+v present=%t", claims, ok)
		}
		if command.AccountID != attentionAccount || command.RequestID != attentionOperation || command.Actor.UserID != attentionUser || command.CorrelationID != attentionOperation {
			t.Fatalf("command=%+v", command)
		}
		return testInformation(t, now), nil
	}
	server := newAttentionServer(t, attentionClaims("work"), service)
	body := `{"parent_work_item_id":"` + attentionWork + `","requirement":{"key":"customer.name","scope":"account"},"question":"What is the customer name?"}`
	response := attentionMutation(t, server.Handler(), http.MethodPost, "/api/v1/accounts/"+attentionAccount+"/attention/information-requests", body, attentionOperation, "")
	if response.Code != http.StatusCreated || response.Header().Get("ETag") != `W/"1"` || !strings.HasSuffix(response.Header().Get("Location"), "/"+attentionOperation) {
		t.Fatalf("response=%d etag=%q location=%q %s", response.Code, response.Header().Get("ETag"), response.Header().Get("Location"), response.Body.String())
	}
}

func TestAttentionQueueRedactsApprovalBindingsWhileDetailExposesDecisionView(t *testing.T) {
	now := time.Date(2026, 8, 21, 17, 0, 0, 0, time.UTC)
	approval := testApproval(t, now)
	service := &attentionServiceStub{
		listApprovals: func(context.Context, access.Actor, ids.AccountID, attentionapp.ApprovalListQuery) (attentionapp.ApprovalSummaryPage, error) {
			return attentionapp.ApprovalSummaryPage{Items: []attentionapp.ApprovalSummary{{
				ID: approval.ID, WorkItemID: approval.WorkItemID, OperationID: approval.OperationID, InvocationID: approval.InvocationID,
				Capability: approval.Capability, Proposer: approval.Proposer, PolicyVersion: approval.PolicyVersion, ExpiresAt: approval.ExpiresAt,
				State: approval.State, Version: approval.Version, CreatedAt: approval.CreatedAt, UpdatedAt: approval.UpdatedAt,
			}}}, nil
		},
		getApproval: func(context.Context, access.Actor, ids.AccountID, ids.ConsequentialApprovalID) (attentiondomain.ConsequentialApproval, error) {
			return approval, nil
		},
	}
	server := newAttentionServer(t, attentionClaims("agents"), service)
	queue := attentionRead(t, server.Handler(), "/api/v1/accounts/"+attentionAccount+"/attention/approvals")
	if queue.Code != http.StatusOK || strings.Contains(queue.Body.String(), "payload") || strings.Contains(queue.Body.String(), "sha256") || strings.Contains(queue.Body.String(), "secret-recipient") {
		t.Fatalf("unsafe queue=%d %s", queue.Code, queue.Body.String())
	}
	detail := attentionRead(t, server.Handler(), "/api/v1/accounts/"+attentionAccount+"/attention/approvals/"+attentionOperation)
	if detail.Code != http.StatusOK || detail.Header().Get("ETag") != `W/"1"` || !strings.Contains(detail.Body.String(), `"payload":{"recipient":"secret-recipient"}`) || !strings.Contains(detail.Body.String(), `"evidence_sha256":"`) {
		t.Fatalf("detail=%d etag=%q %s", detail.Code, detail.Header().Get("ETag"), detail.Body.String())
	}
}

func TestAttentionMutationsRequireValidVersionAndMapConflicts(t *testing.T) {
	service := &attentionServiceStub{cancelInformation: func(context.Context, attentionapp.CancelInformationCommand) (attentiondomain.InformationRequest, error) {
		return attentiondomain.InformationRequest{}, attentionapp.ErrConflict
	}}
	server := newAttentionServer(t, attentionClaims("work"), service)
	target := "/api/v1/accounts/" + attentionAccount + "/attention/information-requests/" + attentionOperation + "/cancellations"
	body := `{"reason":"No longer required"}`

	missing := attentionMutation(t, server.Handler(), http.MethodPost, target, body, attentionOperation, "")
	if missing.Code != http.StatusPreconditionRequired || !strings.Contains(missing.Body.String(), "attention_version_required") {
		t.Fatalf("missing=%d %s", missing.Code, missing.Body.String())
	}
	malformed := attentionMutation(t, server.Handler(), http.MethodPost, target, body, attentionOperation, `"1"`)
	if malformed.Code != http.StatusBadRequest || !strings.Contains(malformed.Body.String(), "invalid_attention_version") {
		t.Fatalf("malformed=%d %s", malformed.Code, malformed.Body.String())
	}
	conflict := attentionMutation(t, server.Handler(), http.MethodPost, target, body, attentionOperation, `W/"1"`)
	if conflict.Code != http.StatusPreconditionFailed || !strings.Contains(conflict.Body.String(), "attention_version_conflict") {
		t.Fatalf("conflict=%d %s", conflict.Code, conflict.Body.String())
	}
}

func TestAttentionRejectsUnknownQueryAndMismatchedIdempotencyKey(t *testing.T) {
	server := newAttentionServer(t, attentionClaims("work"), &attentionServiceStub{})
	query := attentionRead(t, server.Handler(), "/api/v1/accounts/"+attentionAccount+"/attention/information-requests?include=facts")
	if query.Code != http.StatusBadRequest || !strings.Contains(query.Body.String(), "invalid_attention_command") {
		t.Fatalf("query=%d %s", query.Code, query.Body.String())
	}
	body := `{"parent_work_item_id":"` + attentionWork + `","requirement":{"key":"customer.name","scope":"account"},"question":"What is the customer name?"}`
	mutation := attentionMutation(t, server.Handler(), http.MethodPost, "/api/v1/accounts/"+attentionAccount+"/attention/information-requests", body, attentionFact, "")
	if mutation.Code != http.StatusBadRequest || !strings.Contains(mutation.Body.String(), "invalid_idempotency_key") {
		t.Fatalf("mutation=%d %s", mutation.Code, mutation.Body.String())
	}
}

func newAttentionServer(t *testing.T, claims routecontext.Claims, service AttentionService) *Server {
	t.Helper()
	server, err := New(claimAcceptor{claims: claims}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithAttention(service))
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func attentionClaims(packageCode string) routecontext.Claims {
	return routecontext.Claims{Authority: routecontext.Authority{
		OperationID: attentionOperation, AccountID: attentionAccount, ActorKind: "user", ActorID: attentionUser, Role: "owner",
		CellID: "cell-us-east-01", PlacementGeneration: 1, EntitlementVersion: 3,
		PackageAccess: &routecontext.PackageAccess{Code: packageCode, Version: 1, Mode: "enabled"},
	}}
}

func attentionRead(t *testing.T, handler http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set(RouteContextHeader, "accepted-by-test-boundary")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func attentionMutation(t *testing.T, handler http.Handler, method, target, body, operationID, version string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set(RouteContextHeader, "accepted-by-test-boundary")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", operationID)
	if version != "" {
		request.Header.Set("If-Match", version)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func testInformation(t *testing.T, now time.Time) attentiondomain.InformationRequest {
	t.Helper()
	item, err := attentiondomain.NewInformationRequest(attentiondomain.InformationRequestDraft{
		ID: attentionOperation, AccountID: attentionAccount, ParentWorkItemID: attentionWork,
		Requirement: attentiondomain.FactRequirement{Key: "customer.name", Scope: attentiondomain.InformationScopeAccount},
		Question:    "What is the customer name?", RequestedBy: attentiondomain.Actor{Kind: attentiondomain.ActorUser, ID: attentionUser},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func testApproval(t *testing.T, now time.Time) attentiondomain.ConsequentialApproval {
	t.Helper()
	item, err := attentiondomain.NewConsequentialApproval(attentiondomain.ConsequentialApprovalDraft{
		ID: attentionOperation, AccountID: attentionAccount, OperationID: attentionFact, InvocationID: attentionInvocation,
		WorkItemID: attentionWork, Capability: "email.send", CanonicalPayload: json.RawMessage(`{"recipient":"secret-recipient"}`),
		EvidenceSHA256: sha256.Sum256([]byte("evidence")), Proposer: attentiondomain.Actor{Kind: attentiondomain.ActorWorkload, ID: "runner:test"},
		PolicyVersion: 1, RequireIndependentReview: true, ExpiresAt: now.Add(time.Hour),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	return item
}

type attentionServiceStub struct {
	createInformation func(context.Context, attentionapp.CreateInformationCommand) (attentiondomain.InformationRequest, error)
	cancelInformation func(context.Context, attentionapp.CancelInformationCommand) (attentiondomain.InformationRequest, error)
	listApprovals     func(context.Context, access.Actor, ids.AccountID, attentionapp.ApprovalListQuery) (attentionapp.ApprovalSummaryPage, error)
	getApproval       func(context.Context, access.Actor, ids.AccountID, ids.ConsequentialApprovalID) (attentiondomain.ConsequentialApproval, error)
}

func (s *attentionServiceStub) CreateInformation(ctx context.Context, command attentionapp.CreateInformationCommand) (attentiondomain.InformationRequest, error) {
	if s.createInformation != nil {
		return s.createInformation(ctx, command)
	}
	return attentiondomain.InformationRequest{}, nil
}
func (*attentionServiceStub) AnswerInformation(context.Context, attentionapp.AnswerInformationCommand) (attentionapp.InformationCompletion, error) {
	return attentionapp.InformationCompletion{}, nil
}
func (s *attentionServiceStub) CancelInformation(ctx context.Context, command attentionapp.CancelInformationCommand) (attentiondomain.InformationRequest, error) {
	if s.cancelInformation != nil {
		return s.cancelInformation(ctx, command)
	}
	return attentiondomain.InformationRequest{}, nil
}
func (*attentionServiceStub) GetInformation(context.Context, access.Actor, ids.AccountID, ids.InformationRequestID) (attentiondomain.InformationRequest, error) {
	return attentiondomain.InformationRequest{}, nil
}
func (*attentionServiceStub) ListInformation(context.Context, access.Actor, ids.AccountID, attentionapp.InformationListQuery) (attentionapp.InformationSummaryPage, error) {
	return attentionapp.InformationSummaryPage{}, nil
}
func (*attentionServiceStub) CreateWorkReview(context.Context, attentionapp.CreateWorkReviewCommand) (attentiondomain.WorkReview, error) {
	return attentiondomain.WorkReview{}, nil
}
func (*attentionServiceStub) DecideWorkReview(context.Context, attentionapp.DecideWorkReviewCommand) (attentiondomain.WorkReview, error) {
	return attentiondomain.WorkReview{}, nil
}
func (*attentionServiceStub) CancelWorkReview(context.Context, attentionapp.CancelWorkReviewCommand) (attentiondomain.WorkReview, error) {
	return attentiondomain.WorkReview{}, nil
}
func (*attentionServiceStub) GetWorkReview(context.Context, access.Actor, ids.AccountID, ids.WorkReviewID) (attentiondomain.WorkReview, error) {
	return attentiondomain.WorkReview{}, nil
}
func (*attentionServiceStub) ListWorkReviews(context.Context, access.Actor, ids.AccountID, attentionapp.WorkReviewListQuery) (attentionapp.WorkReviewSummaryPage, error) {
	return attentionapp.WorkReviewSummaryPage{}, nil
}
func (*attentionServiceStub) CreateApproval(context.Context, attentionapp.CreateApprovalCommand) (attentiondomain.ConsequentialApproval, error) {
	return attentiondomain.ConsequentialApproval{}, nil
}
func (*attentionServiceStub) DecideApproval(context.Context, attentionapp.DecideApprovalCommand) (attentiondomain.ConsequentialApproval, error) {
	return attentiondomain.ConsequentialApproval{}, nil
}
func (*attentionServiceStub) CancelApproval(context.Context, attentionapp.CancelApprovalCommand) (attentiondomain.ConsequentialApproval, error) {
	return attentiondomain.ConsequentialApproval{}, nil
}
func (s *attentionServiceStub) GetApproval(ctx context.Context, actor access.Actor, accountID ids.AccountID, approvalID ids.ConsequentialApprovalID) (attentiondomain.ConsequentialApproval, error) {
	if s.getApproval != nil {
		return s.getApproval(ctx, actor, accountID, approvalID)
	}
	return attentiondomain.ConsequentialApproval{}, nil
}
func (s *attentionServiceStub) ListApprovals(ctx context.Context, actor access.Actor, accountID ids.AccountID, query attentionapp.ApprovalListQuery) (attentionapp.ApprovalSummaryPage, error) {
	if s.listApprovals != nil {
		return s.listApprovals(ctx, actor, accountID, query)
	}
	return attentionapp.ApprovalSummaryPage{}, nil
}

var _ AttentionService = (*attentionServiceStub)(nil)

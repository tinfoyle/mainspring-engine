// Package cellapi exposes account-owned use cases only after accepting a
// signed, request-bound route context from the global app router.
package cellapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/tinfoyle/spyglass-engine/internal/application/actionrecovery"
	baselineapp "github.com/tinfoyle/spyglass-engine/internal/application/baseline"
	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	baselinedomain "github.com/tinfoyle/spyglass-engine/internal/modules/baseline"
	"github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	workdomain "github.com/tinfoyle/spyglass-engine/internal/modules/work"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/requestbody"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

const (
	RouteContextHeader = routecontext.HeaderName
	DefaultMaxBody     = int64(1 << 20)
)

type Acceptor interface {
	Accept(context.Context, string, routecontext.Binding) (routecontext.Claims, error)
}

type Server struct {
	acceptor  Acceptor
	logger    *slog.Logger
	maxBody   int64
	work      WorkQueries
	commands  WorkCommands
	agents    AgentService
	attention AttentionService
	actions   ActionRecoveryService
	knowledge KnowledgeService
	documents KnowledgeDocumentService
	baseline  BaselineService
	counters  routeCounters
}

type RouteStats struct {
	Accepted                uint64 `json:"accepted"`
	MissingContext          uint64 `json:"missing_context"`
	ReplayDenied            uint64 `json:"replay_denied"`
	StalePlacementDenied    uint64 `json:"stale_placement_denied"`
	AccountUnavailable      uint64 `json:"account_unavailable"`
	VerificationDenied      uint64 `json:"verification_denied"`
	ReceiptStoreUnavailable uint64 `json:"receipt_store_unavailable"`
}

type routeCounters struct {
	accepted, missing, replay, stale, unavailable, invalid, receiptStore atomic.Uint64
}

type Option func(*Server)

func WithWorkQueries(queries WorkQueries) Option {
	return func(server *Server) { server.work = queries }
}

func WithWorkCommands(commands WorkCommands) Option {
	return func(server *Server) { server.commands = commands }
}

func WithAgents(service AgentService) Option {
	return func(server *Server) { server.agents = service }
}

func WithAttention(service AttentionService) Option {
	return func(server *Server) { server.attention = service }
}

type ActionRecoveryService interface {
	List(context.Context, access.Actor, ids.AccountID, actionrecovery.ListQuery) (actionrecovery.Page, error)
	Get(context.Context, access.Actor, ids.AccountID, string) (actionrecovery.Detail, error)
	Request(context.Context, actionrecovery.RequestCommand) (actionrecovery.Detail, error)
	Confirm(context.Context, actionrecovery.ConfirmCommand) (actionrecovery.Detail, error)
}

func WithActionRecovery(service ActionRecoveryService) Option {
	return func(server *Server) { server.actions = service }
}

type KnowledgeService interface {
	RegisterEvidence(context.Context, knowledgeapp.RegisterEvidenceCommand) (knowledge.Evidence, error)
	ProposeClaim(context.Context, knowledgeapp.ProposeClaimCommand) (knowledge.Claim, error)
	DecideClaim(context.Context, knowledgeapp.DecideClaimCommand) (knowledge.Claim, *knowledge.Fact, error)
	GetClaim(context.Context, access.Actor, ids.AccountID, ids.KnowledgeClaimID) (knowledge.Claim, error)
	ListClaims(context.Context, access.Actor, ids.AccountID, knowledgeapp.ClaimListQuery) (knowledgeapp.ClaimPage, error)
	ListFacts(context.Context, access.Actor, ids.AccountID, knowledgeapp.FactListQuery) (knowledgeapp.FactPage, error)
}

type KnowledgeDocumentService interface {
	Upload(context.Context, knowledgeapp.UploadDocumentCommand) (knowledge.Document, knowledge.DocumentRevision, error)
	List(context.Context, access.Actor, ids.AccountID, knowledgeapp.DocumentListQuery) (knowledgeapp.DocumentPage, error)
	GetDetail(context.Context, access.Actor, ids.AccountID, ids.KnowledgeDocumentID) (knowledgeapp.DocumentDetail, error)
	Publish(context.Context, knowledgeapp.PublishDocumentCommand) (knowledge.Document, error)
	Delete(context.Context, knowledgeapp.DeleteDocumentCommand) (knowledge.Document, error)
	Retrieve(context.Context, access.Actor, ids.AccountID, knowledgeapp.DocumentRetrievalQuery) ([]knowledgeapp.DocumentCitation, error)
	GetCitation(context.Context, access.Actor, ids.AccountID, ids.KnowledgeDocumentID, ids.KnowledgeDocumentRevisionID, ids.KnowledgeDocumentChunkID) (knowledgeapp.DocumentCitation, error)
}

func WithKnowledge(service KnowledgeService) Option {
	return func(server *Server) { server.knowledge = service }
}

func WithKnowledgeDocuments(service KnowledgeDocumentService) Option {
	return func(server *Server) { server.documents = service }
}

type BaselineService interface {
	Start(context.Context, baselineapp.StartCommand) (baselinedomain.Assessment, error)
	Get(context.Context, access.Actor, ids.AccountID, ids.BaselineAssessmentID) (baselinedomain.Assessment, error)
	Answer(context.Context, baselineapp.AnswerCommand) (baselinedomain.Assessment, error)
	BeginInventory(context.Context, baselineapp.AdvanceCommand) (baselinedomain.Assessment, error)
	CompleteInventory(context.Context, baselineapp.CompleteInventoryCommand) (baselinedomain.Assessment, error)
	DecideEvidence(context.Context, baselineapp.DecideEvidenceCommand) (baselinedomain.Assessment, error)
	Disposition(context.Context, baselineapp.DispositionCommand) (baselinedomain.Assessment, error)
	SubmitPlan(context.Context, baselineapp.SubmitPlanCommand) (baselinedomain.Assessment, error)
	ApprovePlan(context.Context, baselineapp.ApprovePlanCommand) (baselinedomain.Assessment, error)
	MaterializePlan(context.Context, baselineapp.MaterializePlanCommand) ([]workdomain.Item, error)
	ConfirmWorkEvidence(context.Context, baselineapp.ConfirmWorkEvidenceCommand) (baselinedomain.Assessment, error)
	MaterializeMaintenance(context.Context, baselineapp.MaterializeMaintenanceCommand) ([]workdomain.Item, error)
	MarkReady(context.Context, baselineapp.AdvanceCommand) (baselinedomain.Assessment, error)
	Reassess(context.Context, baselineapp.ReassessCommand) (baselinedomain.Assessment, baselinedomain.Assessment, error)
	GrantSource(context.Context, baselineapp.GrantSourceCommand) (baselinedomain.SourceGrant, error)
	ListSourceGrants(context.Context, baselineapp.ListSourceGrantsQuery) (baselineapp.SourceGrantPage, error)
	RevokeSource(context.Context, baselineapp.RevokeSourceCommand) (baselinedomain.SourceGrant, error)
}

func WithBaseline(service BaselineService) Option {
	return func(server *Server) { server.baseline = service }
}

func New(acceptor Acceptor, logger *slog.Logger, maxBody int64, options ...Option) (*Server, error) {
	if acceptor == nil || logger == nil || maxBody <= 0 || maxBody > 16<<20 {
		return nil, errors.New("cell API dependencies and bounded body size are required")
	}
	server := &Server{acceptor: acceptor, logger: logger, maxBody: maxBody}
	for _, option := range options {
		option(server)
	}
	return server, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/context", s.accountContext)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/work-items", s.workList)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/work-items", s.workCreate)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/work-items/summary", s.workSummary)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/work-items/{itemID}", s.workItem)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/work-items/{itemID}/children", s.workChildren)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/work-items/{itemID}/transitions", s.workTransition)
	mux.HandleFunc("PATCH /api/v1/accounts/{accountID}/work-items/{itemID}/assignment", s.workAssign)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/work-items/{itemID}/provenance-links", s.workAttachProvenance)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/work-items/{itemID}/conversation-links", s.workLinkConversation)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/agent-boardrooms", s.agentBoardrooms)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/agent-boardrooms", s.agentBoardroomCreate)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/agent-boardrooms/{boardroomID}/personas", s.agentPersonas)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/agent-boardrooms/{boardroomID}/personas", s.agentPersonaPublish)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/agent-boardrooms/{boardroomID}/conversations", s.agentConversations)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/agent-boardrooms/{boardroomID}/runs", s.agentRunStart)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/agent-conversations/{conversationID}", s.agentConversation)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/agent-conversations/{conversationID}/messages", s.agentMessages)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/agent-runs/{runID}", s.agentRun)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/agent-runs/{runID}/resolutions", s.agentRunResolve)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/attention/information-requests", s.attentionInformationList)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/attention/information-requests", s.attentionInformationCreate)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/attention/information-requests/{requestID}", s.attentionInformationGet)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/attention/information-requests/{requestID}/answers", s.attentionInformationAnswer)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/attention/information-requests/{requestID}/cancellations", s.attentionInformationCancel)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/attention/work-reviews", s.attentionReviewList)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/attention/work-reviews", s.attentionReviewCreate)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/attention/work-reviews/{reviewID}", s.attentionReviewGet)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/attention/work-reviews/{reviewID}/decisions", s.attentionReviewDecide)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/attention/work-reviews/{reviewID}/cancellations", s.attentionReviewCancel)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/attention/approvals", s.attentionApprovalList)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/attention/approvals", s.attentionApprovalCreate)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/attention/approvals/{approvalID}", s.attentionApprovalGet)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/attention/approvals/{approvalID}/decisions", s.attentionApprovalDecide)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/attention/approvals/{approvalID}/cancellations", s.attentionApprovalCancel)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/attention/actions", s.actionRecoveryList)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/attention/actions/{operationID}", s.actionRecoveryGet)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/attention/actions/{operationID}/resolution-requests", s.actionRecoveryRequest)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/attention/actions/{operationID}/resolutions/{resolutionID}/confirmations", s.actionRecoveryConfirm)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/knowledge/evidence", s.knowledgeEvidenceRegister)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/knowledge/documents", s.knowledgeDocumentList)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/knowledge/documents", s.knowledgeDocumentUpload)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/knowledge/retrieval", s.knowledgeDocumentRetrieve)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/knowledge/documents/{documentID}", s.knowledgeDocumentGet)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/knowledge/documents/{documentID}/revisions/{revisionID}/chunks/{chunkID}", s.knowledgeDocumentCitationGet)
	mux.HandleFunc("DELETE /api/v1/accounts/{accountID}/knowledge/documents/{documentID}", s.knowledgeDocumentDelete)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/knowledge/documents/{documentID}/publications", s.knowledgeDocumentPublish)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/knowledge/facts", s.knowledgeFactList)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/knowledge/claims", s.knowledgeClaimPropose)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/knowledge/claims", s.knowledgeClaimList)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/knowledge/claims/{claimID}", s.knowledgeClaimGet)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/knowledge/claims/{claimID}/decisions", s.knowledgeClaimDecide)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/baseline-assessments", s.baselineStart)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/baseline-assessments/{assessmentID}", s.baselineGet)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/baseline-assessments/{assessmentID}/answers", s.baselineAnswer)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/baseline-assessments/{assessmentID}/inventory-starts", s.baselineBeginInventory)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/baseline-assessments/{assessmentID}/inventories", s.baselineCompleteInventory)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/baseline-assessments/{assessmentID}/evidence-decisions", s.baselineDecideEvidence)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/baseline-assessments/{assessmentID}/dispositions", s.baselineDisposition)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/baseline-assessments/{assessmentID}/plans", s.baselineSubmitPlan)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/baseline-assessments/{assessmentID}/plan-approvals", s.baselineApprovePlan)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/baseline-assessments/{assessmentID}/work-materializations", s.baselineMaterializePlan)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/baseline-assessments/{assessmentID}/work-evidence-confirmations", s.baselineConfirmWorkEvidence)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/baseline-assessments/{assessmentID}/maintenance-work-materializations", s.baselineMaterializeMaintenance)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/baseline-assessments/{assessmentID}/readiness", s.baselineMarkReady)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/baseline-assessments/{assessmentID}/reassessments", s.baselineReassess)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/baseline-assessments/{assessmentID}/source-grants", s.baselineSourceGrantList)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/baseline-assessments/{assessmentID}/source-grants", s.baselineSourceGrantCreate)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/baseline-assessments/{assessmentID}/source-grants/{grantID}/revocations", s.baselineSourceGrantRevoke)
	return s.recover(s.securityHeaders(mux))
}

func (s *Server) Stats() RouteStats {
	return RouteStats{Accepted: s.counters.accepted.Load(), MissingContext: s.counters.missing.Load(), ReplayDenied: s.counters.replay.Load(), StalePlacementDenied: s.counters.stale.Load(), AccountUnavailable: s.counters.unavailable.Load(), VerificationDenied: s.counters.invalid.Load(), ReceiptStoreUnavailable: s.counters.receiptStore.Load()}
}

func (s *Server) accountContext(w http.ResponseWriter, r *http.Request) {
	claims, ok := s.accept(w, r)
	if !ok {
		return
	}
	if ids.AccountID(r.PathValue("accountID")) != claims.Authority.AccountID {
		writeProblem(w, http.StatusNotFound, "resource_not_found", "the requested Account resource was not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"account_id": claims.Authority.AccountID, "actor_kind": claims.Authority.ActorKind,
		"role": claims.Authority.Role, "cell_id": claims.Authority.CellID,
		"placement_generation": claims.Authority.PlacementGeneration,
		"entitlement_version":  claims.Authority.EntitlementVersion,
		"package_access":       claims.Authority.PackageAccess,
	})
}

func (s *Server) accept(w http.ResponseWriter, r *http.Request) (routecontext.Claims, bool) {
	token := strings.TrimSpace(r.Header.Get(RouteContextHeader))
	if token == "" {
		s.counters.missing.Add(1)
		writeProblem(w, http.StatusUnauthorized, "route_context_required", "trusted route context is required")
		return routecontext.Claims{}, false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.maxBody))
	if err != nil {
		writeProblem(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds the cell limit")
		return routecontext.Claims{}, false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	binding, err := routecontext.BindRequest(r, body)
	if err != nil {
		s.counters.invalid.Add(1)
		writeProblem(w, http.StatusBadRequest, "invalid_request", "request target is invalid")
		return routecontext.Claims{}, false
	}
	return s.acceptBinding(w, r, token, binding)
}

func (s *Server) acceptCaptured(w http.ResponseWriter, r *http.Request, maximum int64) (routecontext.Claims, *requestbody.Capture, bool) {
	token := strings.TrimSpace(r.Header.Get(RouteContextHeader))
	if token == "" {
		s.counters.missing.Add(1)
		writeProblem(w, http.StatusUnauthorized, "route_context_required", "trusted route context is required")
		return routecontext.Claims{}, nil, false
	}
	body, err := requestbody.Read(r.Body, maximum)
	if err != nil {
		if errors.Is(err, requestbody.ErrTooLarge) {
			writeProblem(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds the document upload limit")
		} else {
			s.logger.Error("capture routed document body", "error", err)
			writeProblem(w, http.StatusServiceUnavailable, "route_boundary_unavailable", "the routed document body could not be secured")
		}
		return routecontext.Claims{}, nil, false
	}
	binding, err := routecontext.BindRequestDigest(r, body.SHA256())
	if err != nil {
		_ = body.Close()
		s.counters.invalid.Add(1)
		writeProblem(w, http.StatusBadRequest, "invalid_request", "request target is invalid")
		return routecontext.Claims{}, nil, false
	}
	claims, ok := s.acceptBinding(w, r, token, binding)
	if !ok {
		_ = body.Close()
		return routecontext.Claims{}, nil, false
	}
	return claims, body, true
}

func (s *Server) acceptBinding(w http.ResponseWriter, r *http.Request, token string, binding routecontext.Binding) (routecontext.Claims, bool) {
	claims, err := s.acceptor.Accept(r.Context(), token, binding)
	if err != nil {
		switch {
		case errors.Is(err, routecontext.ErrReplay):
			s.counters.replay.Add(1)
			writeProblem(w, http.StatusConflict, "route_replay", "this routed request was already consumed")
		case errors.Is(err, routecontext.ErrPlacement):
			s.counters.stale.Add(1)
			writeProblem(w, http.StatusConflict, "stale_route", "Account placement changed; refresh routing")
		case errors.Is(err, routecontext.ErrUnavailable):
			s.counters.unavailable.Add(1)
			writeProblem(w, http.StatusServiceUnavailable, "account_unavailable", "the Account is not currently available in this cell")
		case errors.Is(err, routecontext.ErrReceiptStore):
			s.counters.receiptStore.Add(1)
			s.logger.Error("route receipt store unavailable", "error", err)
			writeProblem(w, http.StatusServiceUnavailable, "route_boundary_unavailable", "the trusted route boundary is temporarily unavailable")
		default:
			s.counters.invalid.Add(1)
			writeProblem(w, http.StatusUnauthorized, "invalid_route_context", "trusted route context could not be verified")
		}
		return routecontext.Claims{}, false
	}
	s.counters.accepted.Add(1)
	*r = *r.WithContext(routecontext.WithProof(r.Context(), routecontext.Proof{Token: token, Binding: binding}))
	return claims, true
}

func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if value := recover(); value != nil {
				s.logger.Error("cell API panic", "value", value)
				writeProblem(w, http.StatusInternalServerError, "internal_error", "the request could not be completed")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func writeProblem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	writeJSON(w, status, map[string]any{"type": "https://infiniteocean.net/problems/" + code, "title": http.StatusText(status), "status": status, "code": code, "detail": detail})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

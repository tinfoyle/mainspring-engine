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
	financeapp "github.com/tinfoyle/spyglass-engine/internal/application/finance"
	integrationsapp "github.com/tinfoyle/spyglass-engine/internal/application/integrations"
	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	marketingapp "github.com/tinfoyle/spyglass-engine/internal/application/marketing"
	schedulingapp "github.com/tinfoyle/spyglass-engine/internal/application/scheduling"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	baselinedomain "github.com/tinfoyle/spyglass-engine/internal/modules/baseline"
	financedomain "github.com/tinfoyle/spyglass-engine/internal/modules/finance"
	integrationsdomain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	marketingdomain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
	schedulingdomain "github.com/tinfoyle/spyglass-engine/internal/modules/scheduling"
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
	acceptor               Acceptor
	logger                 *slog.Logger
	maxBody                int64
	work                   WorkQueries
	commands               WorkCommands
	agents                 AgentService
	attention              AttentionService
	actions                ActionRecoveryService
	knowledge              KnowledgeService
	documents              KnowledgeDocumentService
	baseline               BaselineService
	finance                FinanceQueryService
	financeCommands        FinanceCommandService
	marketing              MarketingQueryService
	marketingCommands      MarketingCommandService
	integrations           IntegrationsQueryService
	integrationCommands    IntegrationsCommandService
	scheduling             SchedulingService
	scheduleExecution      ScheduleExecutionService
	scheduleWorkerIdentity string
	counters               routeCounters
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

type FinanceQueryService interface {
	GetLedger(context.Context, access.Actor, ids.AccountID, ids.FinanceLedgerID) (financedomain.Ledger, error)
	ListLedgers(context.Context, access.Actor, ids.AccountID, financeapp.LedgerListQuery) (financeapp.LedgerPage, error)
	GetPostingAccount(context.Context, access.Actor, ids.AccountID, ids.FinanceAccountID) (financedomain.PostingAccount, error)
	ListPostingAccounts(context.Context, access.Actor, ids.AccountID, financeapp.PostingAccountListQuery) (financeapp.PostingAccountPage, error)
	GetEntry(context.Context, access.Actor, ids.AccountID, ids.FinanceEntryID) (financedomain.JournalEntry, error)
	ListEntries(context.Context, access.Actor, ids.AccountID, financeapp.EntryListQuery) (financeapp.EntryPage, error)
	GetReconciliation(context.Context, access.Actor, ids.AccountID, ids.FinanceReconciliationID) (financedomain.Reconciliation, error)
	ListReconciliations(context.Context, access.Actor, ids.AccountID, financeapp.ReconciliationListQuery) (financeapp.ReconciliationPage, error)
}

type FinanceCommandService interface {
	CreateLedger(context.Context, financeapp.CreateLedgerCommand) (financedomain.Ledger, bool, error)
	ReviseLedger(context.Context, financeapp.ReviseLedgerCommand) (financedomain.Ledger, error)
	ClosePeriod(context.Context, financeapp.ClosePeriodCommand) (financedomain.Ledger, error)
	ArchiveLedger(context.Context, financeapp.LedgerTransitionCommand) (financedomain.Ledger, error)
	CreatePostingAccount(context.Context, financeapp.CreateAccountCommand) (financedomain.PostingAccount, bool, error)
	RevisePostingAccount(context.Context, financeapp.RevisePostingAccountCommand) (financedomain.PostingAccount, error)
	ArchivePostingAccount(context.Context, financeapp.PostingAccountTransitionCommand) (financedomain.PostingAccount, error)
	CreateEntry(context.Context, financeapp.CreateEntryCommand) (financedomain.JournalEntry, bool, error)
	ReviseEntry(context.Context, financeapp.ReviseEntryCommand) (financedomain.JournalEntry, error)
	PostEntry(context.Context, financeapp.EntryTransitionCommand) (financedomain.JournalEntry, error)
	ReverseEntry(context.Context, financeapp.ReverseEntryCommand) (financedomain.JournalEntry, financedomain.JournalEntry, error)
	Reconcile(context.Context, financeapp.ReconcileCommand) (financedomain.Reconciliation, bool, error)
	ConfirmReconciliation(context.Context, financeapp.ConfirmReconciliationCommand) (financedomain.Reconciliation, error)
}

func WithFinance(service FinanceQueryService) Option {
	return func(server *Server) { server.finance = service }
}

func WithFinanceCommands(service FinanceCommandService) Option {
	return func(server *Server) { server.financeCommands = service }
}

type MarketingQueryService interface {
	GetCampaign(context.Context, access.Actor, ids.AccountID, ids.MarketingCampaignID) (marketingdomain.Campaign, error)
	ListCampaigns(context.Context, access.Actor, ids.AccountID, marketingapp.CampaignListQuery) (marketingapp.CampaignPage, error)
	ListAssetRevisions(context.Context, access.Actor, ids.AccountID, marketingapp.AssetRevisionListQuery) (marketingapp.AssetRevisionPage, error)
	GetRelease(context.Context, access.Actor, ids.AccountID, ids.MarketingReleaseID) (marketingdomain.ReleasePlan, error)
	ListReleases(context.Context, access.Actor, ids.AccountID, marketingapp.ReleaseListQuery) (marketingapp.ReleasePage, error)
}

func WithMarketing(service MarketingQueryService) Option {
	return func(server *Server) { server.marketing = service }
}

type MarketingCommandService interface {
	CreateCampaign(context.Context, marketingapp.CreateCampaignCommand) (marketingdomain.Campaign, bool, error)
	ReviseCampaign(context.Context, marketingapp.ReviseCampaignCommand) (marketingdomain.Campaign, error)
	CreateAssetRevision(context.Context, marketingapp.CreateAssetRevisionCommand) (marketingdomain.AssetRevision, bool, error)
	CreateRelease(context.Context, marketingapp.CreateReleaseCommand) (marketingdomain.ReleasePlan, bool, error)
	SubmitRelease(context.Context, marketingapp.ReleaseTransitionCommand) (marketingdomain.ReleasePlan, error)
	ApproveRelease(context.Context, marketingapp.ReleaseTransitionCommand) (marketingdomain.ReleasePlan, error)
	CancelRelease(context.Context, marketingapp.ReleaseTransitionCommand) (marketingdomain.ReleasePlan, error)
	ActivateCampaign(context.Context, marketingapp.CampaignTransitionCommand) (marketingdomain.Campaign, error)
	PauseCampaign(context.Context, marketingapp.CampaignTransitionCommand) (marketingdomain.Campaign, error)
	CompleteCampaign(context.Context, marketingapp.CampaignTransitionCommand) (marketingdomain.Campaign, error)
	ArchiveCampaign(context.Context, marketingapp.CampaignTransitionCommand) (marketingdomain.Campaign, error)
}

func WithMarketingCommands(service MarketingCommandService) Option {
	return func(server *Server) { server.marketingCommands = service }
}

type IntegrationsQueryService interface {
	GetConnectionDetail(context.Context, access.Actor, ids.AccountID, ids.IntegrationConnectionID) (integrationsapp.ConnectionDetail, error)
	ListConnections(context.Context, access.Actor, ids.AccountID, integrationsapp.ConnectionListQuery) (integrationsapp.ConnectionPage, error)
	ListHealth(context.Context, access.Actor, ids.AccountID, integrationsapp.HealthListQuery) (integrationsapp.HealthPage, error)
	GetExecution(context.Context, access.Actor, ids.AccountID, ids.IntegrationExecutionID) (integrationsapp.ExecutionDetail, error)
	ListExecutions(context.Context, access.Actor, ids.AccountID, integrationsapp.ExecutionListQuery) (integrationsapp.ExecutionPage, error)
}

func WithIntegrations(service IntegrationsQueryService) Option {
	return func(server *Server) { server.integrations = service }
}

type IntegrationsCommandService interface {
	CreateConnection(context.Context, integrationsapp.CreateConnectionCommand) (integrationsdomain.Connection, bool, error)
	ReviseConnection(context.Context, integrationsapp.ReviseConnectionCommand) (integrationsdomain.Connection, error)
	ActivateConnection(context.Context, integrationsapp.CredentialCommand) (integrationsdomain.Connection, error)
	RotateCredential(context.Context, integrationsapp.CredentialCommand) (integrationsdomain.Connection, error)
	DisableConnection(context.Context, integrationsapp.TransitionCommand) (integrationsdomain.Connection, error)
	EnableConnection(context.Context, integrationsapp.TransitionCommand) (integrationsdomain.Connection, error)
	RevokeConnection(context.Context, integrationsapp.TransitionCommand) (integrationsdomain.Connection, error)
	PrepareExecution(context.Context, integrationsapp.PrepareExecutionCommand) (integrationsdomain.Execution, bool, error)
}

func WithIntegrationCommands(service IntegrationsCommandService) Option {
	return func(server *Server) { server.integrationCommands = service }
}

type SchedulingService interface {
	Create(context.Context, schedulingapp.CreateCommand) (schedulingdomain.Schedule, bool, error)
	Get(context.Context, schedulingapp.GetQuery) (schedulingdomain.Schedule, error)
	List(context.Context, schedulingapp.ListCommand) (schedulingapp.Page, error)
	Revise(context.Context, schedulingapp.ReviseCommand) (schedulingdomain.Schedule, error)
	Pause(context.Context, schedulingapp.TransitionCommand) (schedulingdomain.Schedule, error)
	Resume(context.Context, schedulingapp.TransitionCommand) (schedulingdomain.Schedule, error)
	Delete(context.Context, schedulingapp.TransitionCommand) (schedulingdomain.Schedule, error)
	TriggerNow(context.Context, schedulingapp.TransitionCommand) (schedulingapp.Trigger, bool, error)
}

func WithScheduling(service SchedulingService) Option {
	return func(server *Server) { server.scheduling = service }
}

type ScheduleExecutionService interface {
	Load(context.Context, schedulingapp.ExecutionClaim) (schedulingapp.ExecutionSnapshot, error)
	Dispatch(context.Context, schedulingapp.OccurrenceCommand) (bool, error)
	Skip(context.Context, schedulingapp.OccurrenceCommand) (bool, error)
}

func WithScheduleExecution(service ScheduleExecutionService, cellID ids.CellID) Option {
	return func(server *Server) {
		server.scheduleExecution = service
		if routecontext.ValidCellID(cellID) {
			server.scheduleWorkerIdentity = "spiffe://infiniteocean.net/spyglass/cells/" + string(cellID) + "/schedule-execution-worker"
		}
	}
}

func New(acceptor Acceptor, logger *slog.Logger, maxBody int64, options ...Option) (*Server, error) {
	if acceptor == nil || logger == nil || maxBody <= 0 || maxBody > 16<<20 {
		return nil, errors.New("cell API dependencies and bounded body size are required")
	}
	server := &Server{acceptor: acceptor, logger: logger, maxBody: maxBody}
	for _, option := range options {
		option(server)
	}
	if server.scheduleExecution != nil && server.scheduleWorkerIdentity == "" {
		return nil, errors.New("Schedule execution requires a valid cell workload identity")
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
	mux.HandleFunc("PUT /api/v1/accounts/{accountID}/agent-boardrooms/{boardroomID}/manager", s.agentBoardroomManagerConfigure)
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
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/finance/ledgers", s.financeLedgerList)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/finance/ledgers", s.financeLedgerCreate)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/finance/ledgers/{ledgerID}", s.financeLedgerGet)
	mux.HandleFunc("PUT /api/v1/accounts/{accountID}/finance/ledgers/{ledgerID}", s.financeLedgerRevise)
	mux.HandleFunc("DELETE /api/v1/accounts/{accountID}/finance/ledgers/{ledgerID}", s.financeLedgerArchive)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/finance/ledgers/{ledgerID}/period-closes", s.financePeriodClose)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/finance/ledgers/{ledgerID}/accounts", s.financeAccountList)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/finance/ledgers/{ledgerID}/accounts", s.financeAccountCreate)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/finance/accounts/{postingAccountID}", s.financeAccountGet)
	mux.HandleFunc("PUT /api/v1/accounts/{accountID}/finance/accounts/{postingAccountID}", s.financeAccountRevise)
	mux.HandleFunc("DELETE /api/v1/accounts/{accountID}/finance/accounts/{postingAccountID}", s.financeAccountArchive)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/finance/ledgers/{ledgerID}/entries", s.financeEntryList)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/finance/ledgers/{ledgerID}/entries", s.financeEntryCreate)
	mux.HandleFunc("POST /internal/v1/accounts/{accountID}/finance/ledgers/{ledgerID}/entries:draft", s.financeAgentEntryDraft)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/finance/entries/{entryID}", s.financeEntryGet)
	mux.HandleFunc("PUT /api/v1/accounts/{accountID}/finance/entries/{entryID}", s.financeEntryRevise)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/finance/entries/{entryID}/postings", s.financeEntryPost)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/finance/entries/{entryID}/reversals", s.financeEntryReverse)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/finance/ledgers/{ledgerID}/reconciliations", s.financeReconciliationList)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/finance/ledgers/{ledgerID}/reconciliations", s.financeReconciliationCreate)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/finance/reconciliations/{reconciliationID}", s.financeReconciliationGet)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/finance/reconciliations/{reconciliationID}/confirmations", s.financeReconciliationConfirm)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/marketing/campaigns", s.marketingCampaignList)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/marketing/campaigns", s.marketingCampaignCreate)
	mux.HandleFunc("POST /internal/v1/accounts/{accountID}/marketing/campaigns:draft", s.marketingAgentCampaignDraft)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/marketing/campaigns/{campaignID}", s.marketingCampaignGet)
	mux.HandleFunc("PUT /api/v1/accounts/{accountID}/marketing/campaigns/{campaignID}", s.marketingCampaignRevise)
	mux.HandleFunc("DELETE /api/v1/accounts/{accountID}/marketing/campaigns/{campaignID}", s.marketingCampaignArchive)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/marketing/campaigns/{campaignID}/asset-revisions", s.marketingAssetRevisionList)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/marketing/campaigns/{campaignID}/asset-revisions", s.marketingAssetRevisionCreate)
	mux.HandleFunc("POST /internal/v1/accounts/{accountID}/marketing/campaigns/{campaignID}/asset-revisions:draft", s.marketingAgentAssetRevisionDraft)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/marketing/campaigns/{campaignID}/releases", s.marketingReleaseList)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/marketing/campaigns/{campaignID}/releases", s.marketingReleaseCreate)
	mux.HandleFunc("POST /internal/v1/accounts/{accountID}/marketing/campaigns/{campaignID}/releases:draft", s.marketingAgentReleaseDraft)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/marketing/releases/{releaseID}", s.marketingReleaseGet)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/marketing/releases/{releaseID}/submissions", s.marketingReleaseSubmit)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/marketing/releases/{releaseID}/approvals", s.marketingReleaseApprove)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/marketing/releases/{releaseID}/cancellations", s.marketingReleaseCancel)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/marketing/campaigns/{campaignID}/activations", s.marketingCampaignActivate)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/marketing/campaigns/{campaignID}/pauses", s.marketingCampaignPause)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/marketing/campaigns/{campaignID}/completions", s.marketingCampaignComplete)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/integrations/connections", s.integrationsConnectionList)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/integrations/connections", s.integrationsConnectionCreate)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/integrations/connections/{connectionID}", s.integrationsConnectionGet)
	mux.HandleFunc("PUT /api/v1/accounts/{accountID}/integrations/connections/{connectionID}", s.integrationsConnectionRevise)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/integrations/connections/{connectionID}/health", s.integrationsHealthList)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/integrations/connections/{connectionID}/credential-bindings", s.integrationsCredentialActivate)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/integrations/connections/{connectionID}/credential-rotations", s.integrationsCredentialRotate)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/integrations/connections/{connectionID}/disables", s.integrationsConnectionDisable)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/integrations/connections/{connectionID}/enables", s.integrationsConnectionEnable)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/integrations/connections/{connectionID}/revocations", s.integrationsConnectionRevoke)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/integrations/executions", s.integrationsExecutionList)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/integrations/executions", s.integrationsExecutionPrepare)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/integrations/executions/{executionID}", s.integrationsExecutionGet)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/schedules", s.scheduleList)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/schedules", s.scheduleCreate)
	mux.HandleFunc("GET /api/v1/accounts/{accountID}/schedules/{scheduleID}", s.scheduleGet)
	mux.HandleFunc("PUT /api/v1/accounts/{accountID}/schedules/{scheduleID}", s.scheduleRevise)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/schedules/{scheduleID}/pauses", s.schedulePause)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/schedules/{scheduleID}/resumptions", s.scheduleResume)
	mux.HandleFunc("POST /api/v1/accounts/{accountID}/schedules/{scheduleID}/triggers", s.scheduleTrigger)
	mux.HandleFunc("DELETE /api/v1/accounts/{accountID}/schedules/{scheduleID}", s.scheduleDelete)
	mux.HandleFunc("POST /internal/v1/schedules/executions:load", s.scheduleExecutionLoad)
	mux.HandleFunc("POST /internal/v1/schedules/executions:dispatch", s.scheduleExecutionDispatch)
	mux.HandleFunc("POST /internal/v1/schedules/executions:skip", s.scheduleExecutionSkip)
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

package tenant

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/tinfoyle/mainspring-engine/internal/auth"
	"github.com/tinfoyle/mainspring-engine/internal/boardroom"
	"github.com/tinfoyle/mainspring-engine/internal/documentextract"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
	mailbox "github.com/tinfoyle/mainspring-engine/internal/email"
	"github.com/tinfoyle/mainspring-engine/internal/finance"
	"github.com/tinfoyle/mainspring-engine/internal/gdrive"
	"github.com/tinfoyle/mainspring-engine/internal/httpx"
	"github.com/tinfoyle/mainspring-engine/internal/rag"
	"github.com/tinfoyle/mainspring-engine/internal/scheduling"
	toolbroker "github.com/tinfoyle/mainspring-engine/internal/tools"
	"github.com/tinfoyle/mainspring-engine/internal/webresearch"
	"github.com/tinfoyle/mainspring-engine/web/assets"
	"github.com/tinfoyle/mainspring-engine/web/components"
)

const (
	sessionCookieName = "mainspring_session"
	sessionLifetime   = 7 * 24 * time.Hour
)

type contextKey string

const authContextKey contextKey = "tenant_auth"

type authenticatedSession struct {
	User     User
	RawToken string
}

type RunDispatcher interface {
	Dispatch(context.Context, domain.RunID) error
	ApprovalDecision(context.Context, domain.RunID, string) error
}

type DocumentService interface {
	ListDocuments(context.Context) ([]rag.Document, error)
	GetDocument(context.Context, string) (rag.DocumentDetail, error)
	IngestText(context.Context, string, string, string, string) (rag.Document, error)
	Search(context.Context, string, int, []string) ([]rag.SearchResult, error)
}

type ServerConfig struct {
	TenantID          domain.TenantID
	TenantSlug        string
	TenantName        string
	BusinessTemplate  BusinessTemplate
	BaseDomain        string
	SessionSecret     []byte
	SetupToken        string
	CookieSecure      bool
	Development       bool
	ControlAdminToken string
	MCPToken          string
	MCPUserEmail      string
}

type Server struct {
	logger     *slog.Logger
	config     ServerConfig
	store      *Store
	boardrooms *boardroom.Store
	dispatcher RunDispatcher
	schedules  *scheduling.Service
	documents  DocumentService
	email      *mailbox.Service
	drive      *gdrive.Service
	research   webresearch.Provider
	approvals  *toolbroker.ApprovalService
	usage      *boardroom.UsageService
	toolIssuer *toolbroker.TokenIssuer
	toolBroker *toolbroker.Broker
	finance    *finance.Service
}

func (s *Server) SetToolBroker(issuer *toolbroker.TokenIssuer, broker *toolbroker.Broker) {
	s.toolIssuer = issuer
	s.toolBroker = broker
}

func NewServer(logger *slog.Logger, config ServerConfig, store *Store, boardrooms *boardroom.Store, dispatcher RunDispatcher, schedules *scheduling.Service, documents DocumentService, emailService *mailbox.Service, driveService *gdrive.Service, researchProvider webresearch.Provider, approvals *toolbroker.ApprovalService, usage *boardroom.UsageService) (*Server, error) {
	if len(config.SessionSecret) < 32 {
		return nil, errors.New("MAINSPRING_SESSION_SECRET must contain at least 32 bytes")
	}
	if strings.TrimSpace(config.SetupToken) == "" {
		return nil, errors.New("MAINSPRING_SETUP_TOKEN is required")
	}
	if config.MCPToken != "" {
		if len(config.MCPToken) < 32 {
			return nil, errors.New("MAINSPRING_MCP_TOKEN must contain at least 32 bytes when MCP is enabled")
		}
		if strings.TrimSpace(config.MCPUserEmail) == "" {
			return nil, errors.New("MAINSPRING_MCP_USER_EMAIL is required when MCP is enabled")
		}
	}
	if documents == nil {
		return nil, errors.New("tenant document service is required")
	}
	if emailService == nil {
		return nil, errors.New("tenant email service is required")
	}
	if approvals == nil {
		return nil, errors.New("tenant approval service is required")
	}
	if usage == nil {
		return nil, errors.New("tenant usage service is required")
	}
	return &Server{
		logger:     logger,
		config:     config,
		store:      store,
		boardrooms: boardrooms,
		dispatcher: dispatcher,
		schedules:  schedules,
		documents:  documents,
		email:      emailService,
		drive:      driveService,
		research:   researchProvider,
		approvals:  approvals,
		usage:      usage,
		finance:    finance.NewService(store.pool),
	}, nil
}

func (s *Server) Handler() http.Handler {
	router := chi.NewRouter()
	router.Get("/healthz", s.health)
	router.Handle("/assets/*", http.StripPrefix("/assets/", http.FileServerFS(assets.Files)))
	router.Post("/internal/platform/ownership", s.requirePlatformControl(s.transferOwnership))
	router.Post("/internal/platform/announcements", s.requirePlatformControl(s.deliverAnnouncement))
	router.With(s.tenantBoundary).Handle("/mcp", s.mcpHTTPHandler())

	router.Group(func(router chi.Router) {
		router.Use(s.tenantBoundary)
		router.Use(s.rejectCrossSitePosts)
		router.Get("/setup", s.setupPage)
		router.Post("/setup", s.setup)
		router.Get("/login", s.loginPage)
		router.Post("/login", s.login)
		router.Get("/invitations/accept", s.acceptInvitationPage)
		router.Post("/invitations/accept", s.acceptInvitation)

		router.Group(func(router chi.Router) {
			router.Use(s.requireAuthentication)
			router.Get("/onboarding", s.onboardingPage)
			router.Post("/onboarding/template", s.requireCSRF(s.saveOnboardingTemplate))
			router.Post("/onboarding/stage", s.requireCSRF(s.saveOnboardingStage))
			router.Post("/onboarding/business", s.requireCSRF(s.saveOnboardingBusiness))
			router.Post("/onboarding/operations", s.requireCSRF(s.saveOnboardingOperations))
			router.Post("/onboarding/priorities", s.requireCSRF(s.saveOnboardingPriorities))
			router.Post("/onboarding/team", s.requireCSRF(s.saveOnboardingTeam))
			router.Post("/onboarding/permissions", s.requireCSRF(s.saveOnboardingPermissions))
			router.Post("/onboarding/launch", s.requireCSRF(s.launchOnboarding))
			router.Get("/baseline", s.baselinePage)
			router.Post("/baseline/interview", s.requireCSRF(s.answerBaselineInterview))
			router.Post("/baseline/facts", s.requireCSRF(s.saveBaselineFact))
			router.Post("/baseline/sources", s.requireCSRF(s.configureBaselineSource))
			router.Post("/baseline/sources/email/sync", s.requireCSRF(s.syncBaselineEmail))
			router.Get("/baseline/google-drive/connect", s.connectGoogleDrive)
			router.Get("/baseline/google-drive/callback", s.googleDriveCallback)
			router.Get("/baseline/google-drive/folders", s.googleDriveFolders)
			router.Post("/baseline/google-drive/sync", s.requireCSRF(s.syncGoogleDrive))
			router.Post("/baseline/google-drive/disconnect", s.requireCSRF(s.disconnectGoogleDrive))
			router.Post("/baseline/advance", s.requireCSRF(s.advanceBaseline))
			router.Post("/baseline/evidence/{requirementID}", s.requireCSRF(s.resolveBaselineEvidence))
			router.Post("/baseline/evidence/{requirementID}/interview", s.requireCSRF(s.answerBaselineEvidenceInterview))
			router.Post("/baseline/evidence/{requirementID}/documents", s.requireCSRF(s.linkBaselineDocument))
			router.Post("/baseline/evidence/{requirementID}/public", s.requireCSRF(s.addBaselinePublicEvidence))
			router.Post("/baseline/evidence/{requirementID}/research", s.requireCSRF(s.researchBaselineEvidence))
			router.Post("/baseline/evidence/{requirementID}/upload", s.uploadBaselineDocument)
			router.Post("/baseline/plan", s.requireCSRF(s.createBaselinePlan))
			router.Post("/baseline/reassess", s.requireCSRF(s.reassessBaseline))
			router.Get("/documents", s.documentsPage)
			router.Post("/documents", s.uploadDocument)
			router.Get("/documents/{documentID}", s.documentPage)
			router.Get("/api/v2/documents", s.v2DocumentsAPI)
			router.Post("/api/v2/documents", s.uploadDocument)
			router.Get("/api/v2/documents/{documentID}", s.v2DocumentAPI)
			router.Get("/api/v2/home", s.v2HomeAPI)
			router.Get("/api/v2/baseline", s.v2BaselineAPI)
			router.Post("/api/v2/baseline/interview", s.requireCSRF(s.v2AnswerBaselineInterview))
			router.Post("/api/v2/baseline/evidence/{requirementID}/interview", s.requireCSRF(s.v2AnswerBaselineEvidence))
			router.Post("/api/v2/baseline/evidence/{requirementID}/documents", s.requireCSRF(s.v2LinkBaselineDocument))
			router.Post("/api/v2/baseline/evidence/{requirementID}", s.requireCSRF(s.v2ResolveBaselineEvidence))
			router.Post("/api/v2/baseline/advance", s.requireCSRF(s.v2AdvanceBaseline))
			router.Post("/api/v2/baseline/plan", s.requireCSRF(s.v2CreateBaselinePlan))
			router.Post("/api/v2/baseline/reassess", s.requireCSRF(s.v2ReassessBaseline))
			router.Get("/api/v2/inbox", s.v2InboxAPI)
			router.Post("/api/v2/inbox/{announcementID}/read", s.requireCSRF(s.v2ReadAnnouncement))
			router.Get("/email", s.emailPage)
			router.Post("/email/settings", s.requireCSRF(s.saveEmailSettings))
			router.Get("/email/messages/{uid}", s.emailMessagePage)
			router.Get("/development", s.developmentPage)
			router.Post("/development/reset-onboarding", s.requireCSRF(s.resetOnboarding))
			router.Post("/logout", s.requireCSRF(s.logout))
			router.Get("/team", s.membersPage)
			router.Post("/team/invitations", s.requireCSRF(s.createMemberInvitation))
			router.Post("/team/members/{userID}/remove", s.requireCSRF(s.removeMember))
			router.Get("/inbox", s.announcementsPage)
			router.Post("/inbox/{announcementID}/read", s.requireCSRF(s.readAnnouncement))
			router.Get("/api/messenger/{channel}", s.listMessengerMessages)
			router.Post("/api/messenger/{channel}", s.requireCSRF(s.createMessengerMessage))

			router.Group(func(router chi.Router) {
				router.Use(s.requireOnboarding)
				router.Get("/", s.dashboard)
				router.Get("/agents", s.agentsPage)
				router.Get("/agents/new", s.newAgentPage)
				router.Post("/agents", s.requireCSRF(s.createAgent))
				router.Get("/agents/{personaID}", s.agentPage)
				router.Post("/agents/{personaID}", s.requireCSRF(s.updateAgent))
				router.Post("/agents/{personaID}/duplicate", s.requireCSRF(s.duplicateAgent))
				router.Get("/boardrooms/{boardroomID}", s.boardroomPage)
				router.Post("/boardrooms/{boardroomID}/conversations", s.requireCSRF(s.createRun))
				router.Post("/boardrooms/{boardroomID}/runs", s.requireCSRF(s.createRun))
				router.Get("/conversations/{conversationID}", s.conversationPage)
				router.Post("/conversations/{conversationID}/runs", s.requireCSRF(s.createFollowUp))
				router.Get("/runs/{runID}", s.runPage)
				router.Get("/runs/{runID}/events", s.runEvents)
				router.Get("/schedules", s.schedulesPage)
				router.Post("/schedules", s.requireCSRF(s.createSchedule))
				router.Post("/schedules/{scheduleID}/pause", s.requireCSRF(s.pauseSchedule))
				router.Post("/schedules/{scheduleID}/trigger", s.requireCSRF(s.triggerSchedule))
				router.Post("/schedules/{scheduleID}/delete", s.requireCSRF(s.deleteSchedule))
				router.Get("/work", s.workQueuePage)
				router.Post("/work", s.requireCSRF(s.createWorkItem))
				router.Get("/work/{workItemID}", s.workItemPage)
				router.Post("/work/{workItemID}/messages", s.createWorkItemMessage)
				router.Post("/work/{workItemID}/status", s.requireCSRF(s.updateWorkItemStatus))
				router.Get("/api/v2/work", s.v2WorkQueueAPI)
				router.Post("/api/v2/work", s.requireCSRF(s.createWorkItem))
				router.Get("/api/v2/work/{workItemID}", s.v2WorkItemAPI)
				router.Get("/api/v2/work/{workItemID}/events", s.v2WorkItemEvents)
				router.Post("/api/v2/work/{workItemID}/messages", s.createWorkItemMessage)
				router.Post("/api/v2/work/{workItemID}/status", s.requireCSRF(s.updateWorkItemStatus))
				router.Get("/api/v2/your-turn", s.v2YourTurnAPI)
				router.Get("/api/v2/your-turn/events", s.v2YourTurnEvents)
				router.Get("/api/v2/boardrooms/{boardroomID}", s.v2BoardroomAPI)
				router.Get("/api/v2/agents", s.v2AgentsAPI)
				router.Get("/api/v2/finance", s.v2FinanceAPI)
				router.Post("/api/v2/finance/ledgers", s.requireCSRF(s.v2CreateFinanceLedger))
				router.Post("/api/v2/finance/ledgers/{ledgerID}/accounts", s.requireCSRF(s.v2CreateFinanceAccount))
				router.Post("/api/v2/finance/ledgers/{ledgerID}/entries", s.requireCSRF(s.v2CreateFinanceEntry))
				router.Get("/api/v2/finance/entries/{entryID}", s.v2FinanceEntryAPI)
				router.Post("/api/v2/finance/entries/{entryID}/post", s.requireCSRF(s.v2PostFinanceEntry))
				router.Post("/api/v2/finance/entries/{entryID}/void", s.requireCSRF(s.v2VoidFinanceEntry))
				router.Post("/api/v2/boardrooms/{boardroomID}/conversations", s.requireCSRF(s.createRun))
				router.Get("/api/v2/conversations/{conversationID}", s.v2ConversationAPI)
				router.Get("/api/v2/conversations/{conversationID}/events", s.v2ConversationEvents)
				router.Post("/api/v2/conversations/{conversationID}/runs", s.requireCSRF(s.createFollowUp))
				router.Post("/api/v2/your-turn/coordinator/answer", s.answerInputCoordinator)
				router.Post("/api/v2/your-turn/approvals/{approvalID}/approve", s.requireCSRF(s.approveAction))
				router.Post("/api/v2/your-turn/approvals/{approvalID}/reject", s.requireCSRF(s.rejectAction))
				router.Post("/email/send", s.requireCSRF(s.sendEmail))
				router.Get("/your-turn", s.yourTurnPage)
				router.Get("/your-turn/coordinator/state", s.inputCoordinatorState)
				router.Post("/your-turn/coordinator/answer", s.answerInputCoordinator)
				router.Post("/your-turn/input/{requestID}/answer", s.answerHumanInput)
				router.Get("/approvals", s.approvalsPage)
				router.Post("/approvals/{approvalID}/approve", s.requireCSRF(s.approveAction))
				router.Post("/approvals/{approvalID}/reject", s.requireCSRF(s.rejectAction))
				router.Get("/operations", s.operationsPage)
				router.Get("/finance", s.financePage)
				router.Post("/finance/ledgers", s.requireCSRF(s.createFinanceLedger))
				router.Post("/finance/ledgers/{ledgerID}", s.requireCSRF(s.updateFinanceLedger))
				router.Post("/finance/ledgers/{ledgerID}/accounts", s.requireCSRF(s.createFinanceAccount))
				router.Post("/finance/accounts/{accountID}", s.requireCSRF(s.updateFinanceAccount))
				router.Post("/finance/ledgers/{ledgerID}/entries", s.requireCSRF(s.createFinanceEntry))
				router.Get("/finance/entries/{entryID}", s.financeEntryPage)
				router.Post("/finance/entries/{entryID}", s.requireCSRF(s.updateFinanceEntry))
				router.Post("/finance/entries/{entryID}/post", s.requireCSRF(s.postFinanceEntry))
				router.Post("/finance/entries/{entryID}/void", s.requireCSRF(s.voidFinanceEntry))
			})
		})
	})

	return httpx.Chain(router,
		httpx.RequestID,
		httpx.SecurityHeaders,
		httpx.Recover(s.logger),
		httpx.AccessLog(s.logger),
	)
}

const documentUploadLimit = documentextract.MaxUploadBytes

func (s *Server) emailPage(w http.ResponseWriter, r *http.Request) {
	s.renderEmailPage(w, r, http.StatusOK, "")
}

func (s *Server) renderEmailPage(w http.ResponseWriter, r *http.Request, status int, formError string) {
	session, _ := sessionFromContext(r.Context())
	baselineSetup := r.URL.Query().Get("return_to") == "baseline" || r.FormValue("return_to") == "baseline"
	integration, err := s.email.Integration(r.Context())
	if errors.Is(err, mailbox.ErrNotConfigured) {
		s.render(w, status, components.EmailPage(s.tenantName(r.Context()), s.userView(session.User), nil, nil,
			s.csrfToken(session), "", formError, uuid.NewString(), s.config.Development, baselineSetup))
		return
	}
	if err != nil {
		s.logger.Error("load email integration", "error", err)
		s.renderError(w, http.StatusServiceUnavailable, "Email settings are temporarily unavailable.")
		return
	}
	messages, inboxErr := s.email.Inbox(r.Context(), 25)
	if inboxErr != nil && formError == "" {
		formError = "The mailbox is configured, but the inbox could not be refreshed: " + inboxErr.Error()
	}
	view := emailIntegrationView(integration)
	notice := ""
	switch r.URL.Query().Get("status") {
	case "configured":
		notice = "Mailbox verified and connected."
	case "sent":
		notice = "Email sent and recorded in the outbound audit ledger."
	}
	s.render(w, status, components.EmailPage(s.tenantName(r.Context()), s.userView(session.User), &view,
		emailInboxViews(messages), s.csrfToken(session), notice, formError, uuid.NewString(), s.config.Development, baselineSetup))
}

func (s *Server) saveEmailSettings(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if err := r.ParseForm(); err != nil {
		s.renderEmailPage(w, r, http.StatusBadRequest, "The mailbox settings were invalid.")
		return
	}
	imapPort, err := strconv.Atoi(r.FormValue("imap_port"))
	if err != nil {
		s.renderEmailPage(w, r, http.StatusBadRequest, "Enter a valid IMAP port.")
		return
	}
	smtpPort, err := strconv.Atoi(r.FormValue("smtp_port"))
	if err != nil {
		s.renderEmailPage(w, r, http.StatusBadRequest, "Enter a valid SMTP port.")
		return
	}
	_, err = s.email.Configure(r.Context(), mailbox.SettingsInput{
		Name: r.FormValue("name"), EmailAddress: r.FormValue("email_address"), DisplayName: r.FormValue("display_name"),
		IMAPHost: r.FormValue("imap_host"), IMAPPort: imapPort, IMAPSecurity: r.FormValue("imap_security"),
		IMAPUsername: r.FormValue("imap_username"), IMAPPassword: r.FormValue("imap_password"),
		SMTPHost: r.FormValue("smtp_host"), SMTPPort: smtpPort, SMTPSecurity: r.FormValue("smtp_security"),
		SMTPUsername: r.FormValue("smtp_username"), SMTPPassword: r.FormValue("smtp_password"),
	}, session.User.ID)
	if err != nil {
		s.logger.Warn("verify email integration", "error", err)
		s.renderEmailPage(w, r, http.StatusBadRequest, "The mailbox could not be verified: "+err.Error())
		return
	}
	if r.FormValue("return_to") == "baseline" {
		http.Redirect(w, r, "/baseline?email=configured", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/email?status=configured", http.StatusSeeOther)
}

func (s *Server) sendEmail(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if err := r.ParseForm(); err != nil {
		s.renderEmailPage(w, r, http.StatusBadRequest, "The email form was invalid.")
		return
	}
	if r.FormValue("confirm_send") != "send" {
		s.renderEmailPage(w, r, http.StatusBadRequest, "Confirm that this should be sent now.")
		return
	}
	key := strings.TrimSpace(r.FormValue("idempotency_key"))
	if _, err := uuid.Parse(key); err != nil {
		s.renderEmailPage(w, r, http.StatusBadRequest, "The send request expired. Refresh and try again.")
		return
	}
	to, err := mailbox.ParseRecipientList(r.FormValue("to"))
	if err != nil {
		s.renderEmailPage(w, r, http.StatusBadRequest, err.Error())
		return
	}
	cc, err := mailbox.ParseRecipientList(r.FormValue("cc"))
	if err != nil {
		s.renderEmailPage(w, r, http.StatusBadRequest, err.Error())
		return
	}
	_, err = s.email.Send(r.Context(), key, mailbox.OutgoingMessage{To: to, CC: cc, Subject: strings.TrimSpace(r.FormValue("subject")), Body: r.FormValue("body")}, "user", session.User.ID, nil)
	if err != nil {
		s.logger.Error("send tenant email", "error", err)
		s.renderEmailPage(w, r, http.StatusBadGateway, "The email was not sent: "+err.Error())
		return
	}
	http.Redirect(w, r, "/email?status=sent", http.StatusSeeOther)
}

func (s *Server) emailMessagePage(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	uidValue, err := strconv.ParseUint(chi.URLParam(r, "uid"), 10, 32)
	if err != nil || uidValue == 0 {
		s.renderError(w, http.StatusNotFound, "The email message was not found.")
		return
	}
	message, err := s.email.Message(r.Context(), uint32(uidValue))
	if errors.Is(err, mailbox.ErrMessageNotFound) {
		s.renderError(w, http.StatusNotFound, "The email message was not found.")
		return
	}
	if err != nil {
		s.logger.Error("load email message", "error", err)
		s.renderError(w, http.StatusServiceUnavailable, "The email message could not be loaded.")
		return
	}
	s.render(w, http.StatusOK, components.EmailMessagePage(s.tenantName(r.Context()), s.userView(session.User), emailMessageView(message), s.csrfToken(session)))
}

func (s *Server) approvalsPage(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/your-turn?tab=approvals", http.StatusSeeOther)
}

func (s *Server) renderApprovalsPage(w http.ResponseWriter, r *http.Request, status int, pageError string) {
	tab := strings.TrimSpace(r.FormValue("return_tab"))
	if tab != "reviews" {
		tab = "approvals"
	}
	s.renderYourTurnPage(w, r, status, pageError, tab)
}

func (s *Server) yourTurnPage(w http.ResponseWriter, r *http.Request) {
	if requestWantsV2Page(r) {
		s.renderV2App(w, r, "Your turn")
		return
	}
	tab := strings.TrimSpace(r.URL.Query().Get("tab"))
	if tab != "reviews" && tab != "approvals" {
		tab = "input"
	}
	s.renderYourTurnPage(w, r, http.StatusOK, "", tab)
}

func (s *Server) inputCoordinatorState(w http.ResponseWriter, r *http.Request) {
	state, err := s.approvals.InputCoordinatorState(r.Context())
	if err != nil {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "coordinator_unavailable", "Mia could not load the latest owner questions.")
		return
	}
	inputs, reviews, approvals, err := s.approvals.YourTurnCounts(r.Context())
	if err != nil {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "coordinator_unavailable", "Mia could not load the latest owner attention counts.")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"state":  state,
		"counts": map[string]int{"inputs": inputs, "reviews": reviews, "approvals": approvals},
	})
}

func (s *Server) renderYourTurnPage(w http.ResponseWriter, r *http.Request, status int, pageError, tab string) {
	session, _ := sessionFromContext(r.Context())
	includeHistory := r.URL.Query().Get("history") == "1"
	var coordinator toolbroker.InputCoordinatorState
	var err error
	if tab == "input" {
		coordinator, err = s.approvals.InputCoordinatorState(r.Context())
		if err != nil {
			s.logger.Error("coordinate your turn inputs", "error", err)
			s.renderError(w, http.StatusServiceUnavailable, "Your turn is temporarily unavailable.")
			return
		}
	}
	inputsCount, reviewsCount, approvalsCount, err := s.approvals.YourTurnCounts(r.Context())
	if err != nil {
		s.logger.Error("count your turn items", "error", err)
		s.renderError(w, http.StatusServiceUnavailable, "Your turn is temporarily unavailable.")
		return
	}
	var inputs []toolbroker.HumanInputRequest
	var approvals []toolbroker.Approval
	var documents []components.DocumentOptionView
	if tab == "input" {
		inputs, err = s.approvals.ListHumanInputs(r.Context(), includeHistory)
		if err == nil {
			documents, err = s.documentOptions(r.Context(), nil)
		}
	} else {
		var all []toolbroker.Approval
		all, err = s.approvals.List(r.Context(), includeHistory)
		for _, item := range all {
			isReview := item.ActionType == toolbroker.WorkReviewAction
			if (tab == "reviews" && isReview) || (tab == "approvals" && !isReview) {
				approvals = append(approvals, item)
			}
		}
	}
	if err != nil {
		s.logger.Error("list your turn items", "tab", tab, "error", err)
		s.renderError(w, http.StatusServiceUnavailable, "Your turn is temporarily unavailable.")
		return
	}
	notice := ""
	switch r.URL.Query().Get("status") {
	case "approved":
		notice = "Your decision was recorded and the workflow continued."
	case "rejected":
		notice = "The proposed action was declined."
	case "answered":
		notice = "Your answers were added to the ticket. The assigned agent will resume automatically as soon as any other required decisions are resolved."
	case "coordinated":
		notice = "Mia saved that answer to the business fact registry and applied it to every matching open request."
	}
	s.render(w, status, components.YourTurnPage(
		s.tenantName(r.Context()), s.userView(session.User), tab,
		components.YourTurnCountsView{Inputs: inputsCount, Reviews: reviewsCount, Approvals: approvalsCount},
		humanInputRequestViews(inputs), inputCoordinatorView(coordinator), approvalViews(approvals), documents, s.csrfToken(session), notice, pageError, includeHistory,
	))
}

func (s *Server) answerInputCoordinator(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	wantsJSON := strings.Contains(r.Header.Get("Accept"), "application/json")
	respondError := func(status int, code, detail string) {
		if wantsJSON {
			httpx.WriteProblem(w, status, code, detail)
			return
		}
		s.renderYourTurnPage(w, r, status, detail, "input")
	}
	r.Body = http.MaxBytesReader(w, r.Body, documentUploadLimit+(512<<10))
	if err := r.ParseMultipartForm(documentUploadLimit); err != nil {
		respondError(http.StatusBadRequest, "invalid_coordinator_answer", "The answer or uploaded document is too large.")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	if !auth.CheckCSRF(session.RawToken, r.FormValue("csrf_token"), s.config.SessionSecret) {
		httpx.WriteProblem(w, http.StatusForbidden, "invalid_csrf", "The form expired or could not be verified.")
		return
	}
	answer := strings.TrimSpace(r.FormValue("answer"))
	switch r.FormValue("response_mode") {
	case "unknown":
		answer = "I do not know this yet. Treat it as unknown, use only conservative reversible assumptions, and identify any evidence needed to confirm it."
	case "not_applicable":
		answer = "This does not apply to the business based on its current operations. Revisit it if the business scope changes."
	}
	attachments, err := s.documentAttachmentsFromRequest(r)
	if err != nil {
		respondError(http.StatusBadRequest, "invalid_document_attachment", err.Error())
		return
	}
	if uploaded, ok, uploadErr := s.ticketUploadedDocument(r, session.User.ID); uploadErr != nil {
		respondError(http.StatusBadRequest, "document_upload_failed", uploadErr.Error())
		return
	} else if ok {
		attachments = append(attachments, uploaded)
	}
	documentIDs := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		documentIDs = append(documentIDs, attachment.ID)
	}
	if _, err := s.approvals.AnswerInputCoordinator(r.Context(), r.FormValue("fact_key"), session.User.ID, answer, documentIDs); err != nil {
		respondError(http.StatusConflict, "coordinator_answer_failed", "Mia could not apply that answer: "+err.Error())
		return
	}
	if wantsJSON {
		s.inputCoordinatorState(w, r)
		return
	}
	http.Redirect(w, r, "/your-turn?tab=input&status=coordinated", http.StatusSeeOther)
}

func (s *Server) answerHumanInput(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, documentUploadLimit+(512<<10))
	if err := r.ParseMultipartForm(documentUploadLimit); err != nil {
		s.renderYourTurnPage(w, r, http.StatusBadRequest, "The answers or uploaded document are too large.", "input")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	if !auth.CheckCSRF(session.RawToken, r.FormValue("csrf_token"), s.config.SessionSecret) {
		httpx.WriteProblem(w, http.StatusForbidden, "invalid_csrf", "The form expired or could not be verified.")
		return
	}
	request, err := s.approvals.GetHumanInput(r.Context(), chi.URLParam(r, "requestID"))
	if errors.Is(err, pgx.ErrNoRows) {
		s.renderError(w, http.StatusNotFound, "The information request was not found.")
		return
	}
	if err != nil || request.Status != "pending" {
		s.renderYourTurnPage(w, r, http.StatusConflict, "This information request is no longer available.", "input")
		return
	}
	values := r.Form["answer"]
	if len(values) != len(request.Questions) {
		s.renderYourTurnPage(w, r, http.StatusBadRequest, "Answer every requested item before resuming the agent.", "input")
		return
	}
	answers := make([]toolbroker.HumanInputAnswer, 0, len(values))
	for index, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len([]rune(value)) > 12000 {
			s.renderYourTurnPage(w, r, http.StatusBadRequest, fmt.Sprintf("Answer %d must contain between 1 and 12,000 characters.", index+1), "input")
			return
		}
		answer := toolbroker.HumanInputAnswer{Question: request.Questions[index], Answer: value}
		answers = append(answers, answer)
	}
	attachments, err := s.documentAttachmentsFromRequest(r)
	if err != nil {
		s.renderYourTurnPage(w, r, http.StatusBadRequest, err.Error(), "input")
		return
	}
	if uploaded, ok, uploadErr := s.ticketUploadedDocument(r, session.User.ID); uploadErr != nil {
		s.renderYourTurnPage(w, r, http.StatusBadRequest, uploadErr.Error(), "input")
		return
	} else if ok {
		attachments = append(attachments, uploaded)
	}
	documentIDs := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		documentIDs = append(documentIDs, attachment.ID)
	}
	if _, err := s.approvals.AnswerHumanInput(r.Context(), request.ID, session.User.ID, answers, documentIDs); err != nil {
		s.renderYourTurnPage(w, r, http.StatusConflict, "The answers could not be recorded: "+err.Error(), "input")
		return
	}
	http.Redirect(w, r, "/your-turn?tab=input&status=answered", http.StatusSeeOther)
}

func (s *Server) approveAction(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if session.User.Role != "owner" {
		s.renderApprovalsPage(w, r, http.StatusForbidden, "Only an owner or administrator can approve external actions.")
		return
	}
	action, err := s.approvals.Decide(r.Context(), chi.URLParam(r, "approvalID"), session.User.ID, true)
	if err != nil {
		s.renderApprovalsPage(w, r, http.StatusConflict, "The action could not be approved: "+err.Error())
		return
	}
	switch action.ActionType {
	case "email.send":
		var message mailbox.OutgoingMessage
		if err := json.Unmarshal(action.RequestPayload, &message); err != nil {
			s.renderApprovalsPage(w, r, http.StatusUnprocessableEntity, "The approved email payload was invalid and was not executed.")
			return
		}
		if _, err := s.email.Send(r.Context(), action.IdempotencyKey, message, "user", session.User.ID, action.RunID); err != nil {
			s.logger.Error("execute approved email", "action_id", action.ID.String(), "error", err)
			s.renderApprovalsPage(w, r, http.StatusBadGateway, "The action was approved, but execution needs attention: "+err.Error())
			return
		}
	case toolbroker.TicketCreateAction:
		if err := s.executeApprovedTicket(r.Context(), action, session.User.ID); err != nil {
			s.logger.Error("execute approved ticket", "action_id", action.ID.String(), "error", err)
			s.renderApprovalsPage(w, r, http.StatusUnprocessableEntity, "The work item was approved, but creation needs attention: "+err.Error())
			return
		}
	case toolbroker.WorkReviewAction:
		if err := s.executeApprovedWorkReview(r.Context(), action); err != nil {
			s.logger.Error("complete approved agent work", "action_id", action.ID.String(), "error", err)
			s.renderApprovalsPage(w, r, http.StatusUnprocessableEntity, "The review was approved, but the ticket could not be completed: "+err.Error())
			return
		}
	default:
		s.renderApprovalsPage(w, r, http.StatusUnprocessableEntity, "The approved action type does not have an executor.")
		return
	}
	s.resumeRunAfterDecision(r.Context(), action.RunID, chi.URLParam(r, "approvalID"))
	tab := "approvals"
	if action.ActionType == toolbroker.WorkReviewAction {
		tab = "reviews"
	}
	if requestWantsJSON(r) {
		payload, err := s.loadV2YourTurn(r.Context(), tab, false)
		if err != nil {
			httpx.WriteProblem(w, http.StatusServiceUnavailable, "your_turn_unavailable", "The decision was recorded, but Your turn could not be refreshed.")
			return
		}
		httpx.WriteJSON(w, http.StatusOK, payload)
		return
	}
	http.Redirect(w, r, "/your-turn?tab="+tab+"&history=1&status=approved", http.StatusSeeOther)
}

func (s *Server) executeApprovedWorkReview(ctx context.Context, proposed toolbroker.ExternalAction) error {
	action, execute, err := s.approvals.BeginExecution(ctx, proposed.ID)
	if err != nil {
		return err
	}
	if !execute {
		if action.Status == "succeeded" {
			return nil
		}
		return fmt.Errorf("work review is already %s", action.Status)
	}
	payload, err := toolbroker.DecodeWorkReviewPayload(action.RequestPayload)
	if err != nil {
		_ = s.approvals.MarkFailed(ctx, action.ID, err)
		return err
	}
	if err := s.store.UpdateWorkItemStatus(ctx, payload.WorkItemID, "done"); err != nil {
		_ = s.approvals.MarkFailed(ctx, action.ID, err)
		return err
	}
	return s.approvals.MarkSucceeded(ctx, action.ID, map[string]any{"work_item_id": payload.WorkItemID, "status": "done"}, payload.WorkItemID)
}

func (s *Server) executeApprovedTicket(ctx context.Context, proposed toolbroker.ExternalAction, userID string) error {
	action, execute, err := s.approvals.BeginExecution(ctx, proposed.ID)
	if err != nil {
		return err
	}
	if !execute {
		if action.Status == "succeeded" {
			return nil
		}
		return fmt.Errorf("work-item action is already %s", action.Status)
	}
	payload, err := toolbroker.DecodeTicketCreatePayload(action.RequestPayload)
	if err != nil {
		_ = s.approvals.MarkFailed(ctx, action.ID, err)
		return err
	}
	if action.RunID == nil {
		err = errors.New("approved work-item action is not linked to a boardroom run")
		_ = s.approvals.MarkFailed(ctx, action.ID, err)
		return err
	}
	run, err := s.boardrooms.GetRun(ctx, *action.RunID)
	if err != nil {
		_ = s.approvals.MarkFailed(ctx, action.ID, err)
		return err
	}
	item, err := s.store.CreateWorkItem(ctx, CreateWorkItemInput{
		Kind: "ticket", Title: payload.Title, Description: payload.Description, Priority: payload.Priority,
		Source: "persona", CreatedByUserID: userID, BoardroomID: run.BoardroomID.String(),
		ConversationID: run.ConversationID.String(), RunID: run.ID.String(),
		ParentID: payload.ParentWorkItemID,
	})
	if err != nil {
		_ = s.approvals.MarkFailed(ctx, action.ID, err)
		return err
	}
	response := map[string]any{"work_item_id": item.ID, "work_item_number": item.Number, "title": item.Title}
	return s.approvals.MarkSucceeded(ctx, action.ID, response, item.ID)
}

func (s *Server) rejectAction(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if session.User.Role != "owner" {
		s.renderApprovalsPage(w, r, http.StatusForbidden, "Only an owner or administrator can reject external actions.")
		return
	}
	action, err := s.approvals.Decide(r.Context(), chi.URLParam(r, "approvalID"), session.User.ID, false)
	if err != nil {
		s.renderApprovalsPage(w, r, http.StatusConflict, "The action could not be rejected: "+err.Error())
		return
	}
	if action.ActionType == toolbroker.WorkReviewAction {
		if payload, decodeErr := toolbroker.DecodeWorkReviewPayload(action.RequestPayload); decodeErr == nil {
			_ = s.store.UpdateWorkItemStatus(r.Context(), payload.WorkItemID, "waiting")
		}
	}
	s.resumeRunAfterDecision(r.Context(), action.RunID, chi.URLParam(r, "approvalID"))
	tab := "approvals"
	if action.ActionType == toolbroker.WorkReviewAction {
		tab = "reviews"
	}
	if requestWantsJSON(r) {
		payload, err := s.loadV2YourTurn(r.Context(), tab, false)
		if err != nil {
			httpx.WriteProblem(w, http.StatusServiceUnavailable, "your_turn_unavailable", "The decision was recorded, but Your turn could not be refreshed.")
			return
		}
		httpx.WriteJSON(w, http.StatusOK, payload)
		return
	}
	http.Redirect(w, r, "/your-turn?tab="+tab+"&history=1&status=rejected", http.StatusSeeOther)
}

func (s *Server) resumeRunAfterDecision(ctx context.Context, runID *domain.RunID, approvalID string) {
	if runID == nil {
		return
	}
	if err := s.dispatcher.ApprovalDecision(ctx, *runID, approvalID); err != nil {
		s.logger.Error("resume run after approval decision", "run_id", runID.String(), "approval_id", approvalID, "error", err)
	}
}

func approvalViews(items []toolbroker.Approval) []components.ApprovalView {
	views := make([]components.ApprovalView, 0, len(items))
	for _, item := range items {
		payload := string(item.RequestPayload)
		var pretty bytes.Buffer
		if json.Indent(&pretty, item.RequestPayload, "", "  ") == nil {
			payload = pretty.String()
		}
		view := components.ApprovalView{
			ID: item.ID, RunID: item.RunID.String(), PersonaName: item.PersonaName, PersonaRole: item.PersonaRole,
			ActionType: item.ActionType, Reason: item.Reason, Evidence: item.Evidence, RequestPayload: payload,
			ActionStatus: item.ActionStatus, Status: item.Status, RequestedAt: item.RequestedAt, DecidedAt: item.DecidedAt,
			WorkItemID: item.WorkItemID, WorkItemNumber: item.WorkItemNumber, WorkItemTitle: item.WorkItemTitle,
		}
		if item.ActionType == toolbroker.WorkReviewAction {
			if review, err := toolbroker.DecodeWorkReviewPayload(item.RequestPayload); err == nil {
				view.ReviewSummary = review.Summary
				view.Recommendations = review.Recommendations
			}
		}
		if item.ActionType == toolbroker.TicketCreateAction && view.WorkItemTitle == "" {
			if ticket, err := toolbroker.DecodeTicketCreatePayload(item.RequestPayload); err == nil {
				view.WorkItemTitle = ticket.Title
			}
		}
		views = append(views, view)
	}
	return views
}

func humanInputRequestViews(items []toolbroker.HumanInputRequest) []components.HumanInputRequestView {
	views := make([]components.HumanInputRequestView, 0, len(items))
	for _, item := range items {
		answers := make([]components.HumanInputAnswerView, 0, len(item.Answers))
		for _, answer := range item.Answers {
			answers = append(answers, components.HumanInputAnswerView{Question: answer.Question, Answer: answer.Answer})
		}
		views = append(views, components.HumanInputRequestView{
			ID: item.ID, ParentWorkItemID: item.ParentWorkItemID, ParentNumber: item.ParentNumber,
			ParentTitle: item.ParentTitle, WorkItemID: item.WorkItemID, WorkItemNumber: item.WorkItemNumber,
			PersonaName: item.PersonaName, PersonaRole: item.PersonaRole, Questions: item.Questions,
			Answers: answers, DocumentIDs: item.DocumentIDs, Status: item.Status,
			RequestedAt: item.RequestedAt, AnsweredAt: item.AnsweredAt,
		})
	}
	return views
}

func inputCoordinatorView(state toolbroker.InputCoordinatorState) components.InputCoordinatorView {
	view := components.InputCoordinatorView{
		PendingQuestions: state.PendingQuestions, PendingRequests: state.PendingRequests,
		RemainingTopics: state.RemainingTopics, KnownFacts: state.KnownFacts,
	}
	for _, message := range state.Messages {
		view.Messages = append(view.Messages, components.InputCoordinatorMessageView{
			Role: message.Role, MessageKind: message.MessageKind, Body: message.Body, CreatedAt: message.CreatedAt,
		})
	}
	for _, fact := range state.RecentFacts {
		view.RecentFacts = append(view.RecentFacts, components.BusinessKnowledgeFactView{
			Key: fact.Key, Label: fact.Label, Value: fact.Value, SourceType: fact.SourceType, UpdatedAt: fact.UpdatedAt,
		})
	}
	if state.Current != nil {
		current := components.InputCoordinatorQuestionView{
			FactKey: state.Current.FactKey, Label: state.Current.Label, Prompt: state.Current.Prompt,
			TicketCount: state.Current.TicketCount, QuestionCount: state.Current.QuestionCount,
		}
		for _, ticket := range state.Current.Tickets {
			current.Tickets = append(current.Tickets, components.InputCoordinatorTicketView{ID: ticket.ID, Number: ticket.Number, Title: ticket.Title})
		}
		view.Current = &current
	}
	return view
}

func (s *Server) operationsPage(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if session.User.Role != "owner" {
		s.renderError(w, http.StatusForbidden, "Only an owner or administrator can view agent operations.")
		return
	}
	snapshot, err := s.usage.Snapshot(r.Context())
	if err != nil {
		s.logger.Error("load operations snapshot", "error", err)
		s.renderError(w, http.StatusServiceUnavailable, "Agent operations are temporarily unavailable.")
		return
	}
	view := components.OperationsView{
		QueuedRuns: snapshot.QueuedRuns, RunningRuns: snapshot.RunningRuns, AwaitingApproval: snapshot.AwaitingApproval,
		FailedRuns: snapshot.FailedRuns, ActiveInvocations: snapshot.ActiveInvocations,
		CompletedInvocations: snapshot.CompletedInvocations, FailedInvocations: snapshot.FailedInvocations,
		MonthlyTokens: snapshot.MonthlyTokens, MonthlyCost: fmt.Sprintf("$%.2f", float64(snapshot.MonthlyCostMicros)/1_000_000),
		AverageLatency: snapshot.AverageLatency.Round(time.Millisecond).String(), ToolDenials: snapshot.ToolDenials,
	}
	for _, circuit := range snapshot.ProviderCircuits {
		view.ProviderCircuits = append(view.ProviderCircuits, components.ProviderCircuitView{
			Provider: circuit.Provider, ConsecutiveFailures: circuit.ConsecutiveFailures,
			OpenUntil: circuit.OpenUntil, LastErrorCategory: circuit.LastErrorCategory,
		})
	}
	s.render(w, http.StatusOK, components.OperationsPage(s.tenantName(r.Context()), s.userView(session.User), view, s.csrfToken(session)))
}

func (s *Server) agentsPage(w http.ResponseWriter, r *http.Request) {
	if requestWantsV2Page(r) {
		if _, ok := s.requireAgentAdministrator(w, r); !ok {
			return
		}
		s.renderV2App(w, r, "Agents")
		return
	}
	session, ok := s.requireAgentAdministrator(w, r)
	if !ok {
		return
	}
	agents, err := s.boardrooms.ListAgents(r.Context())
	if err != nil {
		s.logger.Error("list agents", "error", err)
		s.renderError(w, http.StatusServiceUnavailable, "Agents are temporarily unavailable.")
		return
	}
	rooms, err := s.boardrooms.List(r.Context())
	if err != nil {
		s.renderError(w, http.StatusServiceUnavailable, "Boardrooms are temporarily unavailable.")
		return
	}
	roomNames := make(map[domain.BoardroomID]boardroom.Summary, len(rooms))
	for _, room := range rooms {
		roomNames[room.ID] = room
	}
	views := make([]components.AgentCardView, 0, len(agents))
	for _, item := range agents {
		room := roomNames[item.BoardroomID]
		views = append(views, components.AgentCardView{ID: item.ID.String(), Name: item.Name, Role: item.Role,
			Description: item.Description, BoardroomName: room.Name, Provider: item.Settings.Provider, Model: item.Settings.Model,
			ReasoningEffort: item.Settings.ReasoningEffort, Position: item.Position, MaxTurns: room.MaxTurns,
			ToolCount: len(item.Grants), Enabled: item.Enabled})
	}
	notice := map[string]string{"created": "Agent created.", "updated": "Agent settings saved for future runs.", "duplicated": "Agent duplicated as inactive."}[r.URL.Query().Get("status")]
	s.render(w, http.StatusOK, components.AgentsPage(s.tenantName(r.Context()), s.userView(session.User), views, s.csrfToken(session), notice))
}

func (s *Server) newAgentPage(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireAgentAdministrator(w, r)
	if !ok {
		return
	}
	rooms, err := s.boardrooms.List(r.Context())
	if err != nil || len(rooms) == 0 {
		s.renderError(w, http.StatusServiceUnavailable, "Create a boardroom before adding an agent.")
		return
	}
	settings := boardroom.DefaultAgentSettings()
	input := boardroom.AgentInput{BoardroomID: rooms[0].ID, Position: 1, Enabled: true, Settings: settings}
	s.renderAgentForm(w, r, session, input, domain.PersonaID{}, nil, http.StatusOK, "", "")
}

func (s *Server) agentPage(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireAgentAdministrator(w, r)
	if !ok {
		return
	}
	id, err := domain.ParsePersonaID(chi.URLParam(r, "personaID"))
	if err != nil {
		s.renderError(w, http.StatusNotFound, "Agent was not found.")
		return
	}
	item, err := s.boardrooms.GetAgent(r.Context(), id)
	if errors.Is(err, boardroom.ErrPersonaNotFound) {
		s.renderError(w, http.StatusNotFound, "Agent was not found.")
		return
	}
	if err != nil {
		s.renderError(w, http.StatusServiceUnavailable, "Agent settings are temporarily unavailable.")
		return
	}
	versions, err := s.boardrooms.AgentVersions(r.Context(), id)
	if err != nil {
		s.renderError(w, http.StatusServiceUnavailable, "Agent version history is temporarily unavailable.")
		return
	}
	input := boardroom.AgentInput{BoardroomID: item.BoardroomID, Name: item.Name, Role: item.Role, Description: item.Description,
		SystemInstructions: item.SystemInstructions, Position: item.Position, Enabled: item.Enabled, Grants: item.Grants, Settings: item.Settings}
	s.renderAgentForm(w, r, session, input, id, versions, http.StatusOK, "", map[string]string{
		"created": "Agent created.",
		"updated": "Agent settings saved for future runs.",
	}[r.URL.Query().Get("status")])
}

func (s *Server) createAgent(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireAgentAdministrator(w, r)
	if !ok {
		return
	}
	input, err := agentInputFromRequest(r)
	if err != nil {
		s.renderAgentForm(w, r, session, input, domain.PersonaID{}, nil, http.StatusUnprocessableEntity, err.Error(), "")
		return
	}
	if err := s.validateAgentDocumentGrant(r.Context(), input.Grants); err != nil {
		s.renderAgentForm(w, r, session, input, domain.PersonaID{}, nil, http.StatusUnprocessableEntity, err.Error(), "")
		return
	}
	persona, err := s.boardrooms.CreateAgent(r.Context(), input)
	if err != nil {
		s.renderAgentForm(w, r, session, input, domain.PersonaID{}, nil, http.StatusUnprocessableEntity, err.Error(), "")
		return
	}
	http.Redirect(w, r, "/agents/"+persona.ID.String()+"?status=created", http.StatusSeeOther)
}

func (s *Server) updateAgent(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireAgentAdministrator(w, r)
	if !ok {
		return
	}
	id, err := domain.ParsePersonaID(chi.URLParam(r, "personaID"))
	if err != nil {
		s.renderError(w, http.StatusNotFound, "Agent was not found.")
		return
	}
	input, parseErr := agentInputFromRequest(r)
	versions, _ := s.boardrooms.AgentVersions(r.Context(), id)
	if parseErr != nil {
		s.renderAgentForm(w, r, session, input, id, versions, http.StatusUnprocessableEntity, parseErr.Error(), "")
		return
	}
	if err := s.validateAgentDocumentGrant(r.Context(), input.Grants); err != nil {
		s.renderAgentForm(w, r, session, input, id, versions, http.StatusUnprocessableEntity, err.Error(), "")
		return
	}
	if _, err := s.boardrooms.UpdateAgent(r.Context(), id, input); err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, boardroom.ErrPersonaNotFound) {
			status = http.StatusNotFound
		}
		s.renderAgentForm(w, r, session, input, id, versions, status, err.Error(), "")
		return
	}
	http.Redirect(w, r, "/agents/"+id.String()+"?status=updated", http.StatusSeeOther)
}

func (s *Server) duplicateAgent(w http.ResponseWriter, r *http.Request) {
	_, ok := s.requireAgentAdministrator(w, r)
	if !ok {
		return
	}
	id, err := domain.ParsePersonaID(chi.URLParam(r, "personaID"))
	if err != nil {
		s.renderError(w, http.StatusNotFound, "Agent was not found.")
		return
	}
	if _, err := s.boardrooms.DuplicateAgent(r.Context(), id); err != nil {
		s.renderError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	http.Redirect(w, r, "/agents?status=duplicated", http.StatusSeeOther)
}

func (s *Server) requireAgentAdministrator(w http.ResponseWriter, r *http.Request) (authenticatedSession, bool) {
	session, _ := sessionFromContext(r.Context())
	if session.User.Role != "owner" {
		s.renderError(w, http.StatusForbidden, "Only an owner or administrator can customize agents.")
		return authenticatedSession{}, false
	}
	return session, true
}

func agentInputFromRequest(r *http.Request) (boardroom.AgentInput, error) {
	settings := boardroom.DefaultAgentSettings()
	input := boardroom.AgentInput{Name: r.FormValue("name"), Role: r.FormValue("role"), Description: r.FormValue("description"),
		SystemInstructions: r.FormValue("system_instructions"), Enabled: r.FormValue("enabled") == "true", Settings: settings}
	var err error
	input.BoardroomID, err = domain.ParseBoardroomID(r.FormValue("boardroom_id"))
	if err != nil {
		return input, errors.New("select a valid boardroom")
	}
	if input.Position, err = strconv.Atoi(r.FormValue("position")); err != nil {
		return input, errors.New("turn position must be a number")
	}
	input.Settings.Provider = r.FormValue("provider")
	input.Settings.Model = r.FormValue("model")
	input.Settings.ReasoningEffort = r.FormValue("reasoning_effort")
	if input.Settings.Temperature, err = optionalFloat(r.FormValue("temperature")); err != nil {
		return input, errors.New("temperature must be a number")
	}
	if input.Settings.TopP, err = optionalFloat(r.FormValue("top_p")); err != nil {
		return input, errors.New("top-p must be a number")
	}
	if input.Settings.ContextTokenLimit, err = strconv.ParseInt(r.FormValue("context_token_limit"), 10, 64); err != nil {
		return input, errors.New("context token limit must be a number")
	}
	if input.Settings.MaxOutputTokens, err = strconv.ParseInt(r.FormValue("max_output_tokens"), 10, 64); err != nil {
		return input, errors.New("output token limit must be a number")
	}
	if input.Settings.TimeoutSeconds, err = strconv.Atoi(r.FormValue("timeout_seconds")); err != nil {
		return input, errors.New("timeout must be a number")
	}
	if input.Settings.MaxToolCalls, err = strconv.Atoi(r.FormValue("max_tool_calls")); err != nil {
		return input, errors.New("tool-call limit must be a number")
	}
	if input.Settings.MaxCostMicros, err = strconv.ParseInt(r.FormValue("max_cost_micros"), 10, 64); err != nil {
		return input, errors.New("cost reservation must be a number")
	}
	input.Settings.ResponseStyle = r.FormValue("response_style")
	input.Settings.CitationPolicy = r.FormValue("citation_policy")
	input.Settings.ActionPolicy = r.FormValue("action_policy")
	knowledgeMode := r.FormValue("knowledge_mode")
	if knowledgeMode == "" && r.FormValue("tool_"+string(domain.CapabilityDocumentsRead)) == "true" {
		conditions := map[string]string{}
		value := strings.TrimSpace(r.FormValue("conditions_" + string(domain.CapabilityDocumentsRead)))
		if value != "" && value != "{}" {
			if err := json.Unmarshal([]byte(value), &conditions); err != nil {
				return input, errors.New("legacy document conditions must be a JSON object containing string values")
			}
		}
		input.Grants = append(input.Grants, domain.ToolGrant{Capability: domain.CapabilityDocumentsRead, Conditions: conditions})
	} else if knowledgeMode == "all" || knowledgeMode == "selected" {
		maximumResults := 5
		if value := strings.TrimSpace(r.FormValue("knowledge_max_results")); value != "" {
			maximumResults, err = strconv.Atoi(value)
			if err != nil || maximumResults < 1 || maximumResults > 10 {
				return input, errors.New("document results must be between 1 and 10")
			}
		}
		conditions := map[string]string{"max_results": strconv.Itoa(maximumResults)}
		if knowledgeMode == "selected" {
			if err := r.ParseForm(); err != nil {
				return input, errors.New("document selection was invalid")
			}
			seen := make(map[string]bool)
			for _, documentID := range r.Form["knowledge_document_id"] {
				documentID = strings.TrimSpace(documentID)
				if _, parseErr := uuid.Parse(documentID); parseErr != nil {
					return input, errors.New("select valid agent documents")
				}
				seen[documentID] = true
			}
			if len(seen) == 0 {
				return input, errors.New("select at least one document or choose the entire library")
			}
			if len(seen) > 20 {
				return input, errors.New("an agent can select at most 20 documents")
			}
			documentIDs := make([]string, 0, len(seen))
			for documentID := range seen {
				documentIDs = append(documentIDs, documentID)
			}
			slices.Sort(documentIDs)
			conditions["document_ids"] = strings.Join(documentIDs, ",")
		}
		input.Grants = append(input.Grants, domain.ToolGrant{Capability: domain.CapabilityDocumentsRead, Conditions: conditions})
	} else if knowledgeMode != "none" && knowledgeMode != "" {
		return input, errors.New("document access mode is invalid")
	}
	for _, capability := range boardroom.AllCapabilities() {
		if capability == domain.CapabilityDocumentsRead {
			continue
		}
		if r.FormValue("tool_"+string(capability)) != "true" {
			continue
		}
		conditions := map[string]string{}
		value := strings.TrimSpace(r.FormValue("conditions_" + string(capability)))
		if value != "" && value != "{}" {
			if err := json.Unmarshal([]byte(value), &conditions); err != nil {
				return input, fmt.Errorf("conditions for %s must be a JSON object containing string values", capability)
			}
		}
		input.Grants = append(input.Grants, domain.ToolGrant{Capability: capability, Conditions: conditions})
	}
	return input, input.NormalizeAndValidate()
}

func (s *Server) validateAgentDocumentGrant(ctx context.Context, grants []domain.ToolGrant) error {
	wanted := make(map[string]bool)
	for _, grant := range grants {
		if grant.Capability != domain.CapabilityDocumentsRead {
			continue
		}
		for _, id := range strings.Split(grant.Conditions["document_ids"], ",") {
			if id = strings.TrimSpace(id); id != "" {
				wanted[id] = true
			}
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	documents, err := s.documents.ListDocuments(ctx)
	if err != nil {
		return errors.New("documents are temporarily unavailable")
	}
	for _, document := range documents {
		if document.Status == "ready" {
			delete(wanted, document.ID)
		}
	}
	if len(wanted) != 0 {
		return errors.New("one or more selected agent documents are unavailable")
	}
	return nil
}

func optionalFloat(value string) (*float64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func (s *Server) renderAgentForm(w http.ResponseWriter, r *http.Request, session authenticatedSession, input boardroom.AgentInput, id domain.PersonaID, versions []boardroom.AgentVersion, status int, formError, notice string) {
	rooms, err := s.boardrooms.List(r.Context())
	if err != nil {
		s.renderError(w, http.StatusServiceUnavailable, "Boardrooms are temporarily unavailable.")
		return
	}
	view := components.AgentFormView{ID: id.String(), Name: input.Name, Role: input.Role, Description: input.Description,
		SystemInstructions: input.SystemInstructions, BoardroomID: input.BoardroomID.String(), Position: input.Position, Enabled: input.Enabled,
		Provider: input.Settings.Provider, Model: input.Settings.Model, ReasoningEffort: input.Settings.ReasoningEffort,
		Temperature: formatOptionalFloat(input.Settings.Temperature), TopP: formatOptionalFloat(input.Settings.TopP),
		ContextTokenLimit: input.Settings.ContextTokenLimit, MaxOutputTokens: input.Settings.MaxOutputTokens,
		TimeoutSeconds: input.Settings.TimeoutSeconds, MaxToolCalls: input.Settings.MaxToolCalls, MaxCostMicros: input.Settings.MaxCostMicros,
		ResponseStyle: input.Settings.ResponseStyle, CitationPolicy: input.Settings.CitationPolicy, ActionPolicy: input.Settings.ActionPolicy,
		IsNew: id.String() == "00000000-0000-0000-0000-000000000000", KnowledgeMode: "none", KnowledgeMaxResults: 5}
	for _, room := range rooms {
		view.Boardrooms = append(view.Boardrooms, components.AgentBoardroomOptionView{ID: room.ID.String(), Name: room.Name, Selected: room.ID == input.BoardroomID})
		if room.ID == input.BoardroomID {
			view.BoardroomName = room.Name
		}
	}
	selected := make(map[domain.Capability]domain.ToolGrant, len(input.Grants))
	for _, grant := range input.Grants {
		selected[grant.Capability] = grant
	}
	selectedDocuments := make(map[string]bool)
	if grant, enabled := selected[domain.CapabilityDocumentsRead]; enabled {
		view.KnowledgeMode = "all"
		if value := strings.TrimSpace(grant.Conditions["max_results"]); value != "" {
			_, _ = fmt.Sscan(value, &view.KnowledgeMaxResults)
		}
		if value := strings.TrimSpace(grant.Conditions["document_ids"]); value != "" {
			view.KnowledgeMode = "selected"
			for _, documentID := range strings.Split(value, ",") {
				selectedDocuments[strings.TrimSpace(documentID)] = true
			}
		}
	}
	view.Documents, err = s.documentOptions(r.Context(), selectedDocuments)
	if err != nil {
		s.renderError(w, http.StatusServiceUnavailable, "Documents could not be loaded for agent settings.")
		return
	}
	for _, definition := range agentToolDefinitions() {
		grant, enabled := selected[definition.Capability]
		conditions := "{}"
		if enabled && len(grant.Conditions) > 0 {
			body, _ := json.Marshal(grant.Conditions)
			conditions = string(body)
		}
		view.Tools = append(view.Tools, components.AgentToolView{Capability: string(definition.Capability), Label: definition.Label,
			Description: definition.Description, Selected: enabled, Conditions: conditions})
	}
	for _, version := range versions {
		view.Versions = append(view.Versions, components.AgentVersionView{Version: version.Version, Name: version.Name, Role: version.Role, CreatedAt: version.CreatedAt})
	}
	s.render(w, status, components.AgentFormPage(s.tenantName(r.Context()), s.userView(session.User), view, s.csrfToken(session), formError, notice))
}

func formatOptionalFloat(value *float64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatFloat(*value, 'f', -1, 64)
}

type agentToolDefinition struct {
	Capability  domain.Capability
	Label       string
	Description string
}

func agentToolDefinitions() []agentToolDefinition {
	return []agentToolDefinition{
		{domain.CapabilityDocumentsRead, "Search documents", "Retrieve bounded, citable passages from the tenant document library."},
		{domain.CapabilityDocumentsWrite, "Create and update documents", "Publish durable, versioned internal knowledge for future agent work."},
		{domain.CapabilityDocumentsComment, "Comment on documents", "Propose comments without replacing source documents."},
		{domain.CapabilityWebSearch, "Search the web", "Search public sources when the web connector is enabled."},
		{domain.CapabilityWebRead, "Read websites", "Open public webpages for analysis and recommendations."},
		{domain.CapabilityEmailRead, "Read inbox", "Read messages from the connected tenant mailbox."},
		{domain.CapabilityEmailDraft, "Draft email", "Prepare customer and vendor messages."},
		{domain.CapabilityEmailSend, "Propose email sends", "Propose SMTP delivery; every send still requires owner approval."},
		{domain.CapabilityTicketRead, "Read work queue", "Review tenant to-dos and tickets."},
		{domain.CapabilityTicketCreate, "Create tickets", "Create work-queue tickets with provenance."},
		{domain.CapabilityScheduleRead, "Read schedules", "Review schedules and recurring boardroom jobs."},
		{domain.CapabilitySchedulePropose, "Propose schedule changes", "Suggest scheduling changes for review."},
		{domain.CapabilityScheduleModify, "Modify schedules", "Apply approved schedule changes."},
		{domain.CapabilityInvoicePrepare, "Prepare invoices", "Draft invoice records and line items."},
		{domain.CapabilityInvoiceIssue, "Issue invoices", "Propose customer invoice issuance."},
		{domain.CapabilityPaymentPropose, "Propose payments", "Prepare a payment request for owner review."},
		{domain.CapabilityPaymentExecute, "Execute payments", "Execute separately approved payment actions when supported."},
	}
}

func emailIntegrationView(value mailbox.Integration) components.EmailIntegrationView {
	return components.EmailIntegrationView{Name: value.Name, EmailAddress: value.EmailAddress, DisplayName: value.DisplayName,
		IMAPServer: net.JoinHostPort(value.IMAPHost, strconv.Itoa(value.IMAPPort)), SMTPServer: net.JoinHostPort(value.SMTPHost, strconv.Itoa(value.SMTPPort)),
		Status: value.Status, LastVerifiedAt: value.LastVerifiedAt, LastError: value.LastError}
}

func emailInboxViews(values []mailbox.InboxMessage) []components.EmailInboxMessageView {
	result := make([]components.EmailInboxMessageView, 0, len(values))
	for _, value := range values {
		result = append(result, components.EmailInboxMessageView{UID: value.UID, From: value.From, Subject: value.Subject, Date: value.Date, Unread: value.Unread})
	}
	return result
}

func emailMessageView(value mailbox.Message) components.EmailMessageView {
	return components.EmailMessageView{EmailInboxMessageView: components.EmailInboxMessageView{UID: value.UID, From: value.From, Subject: value.Subject, Date: value.Date, Unread: value.Unread}, To: strings.Join(value.To, ", "), Body: value.Body}
}

func (s *Server) documentsPage(w http.ResponseWriter, r *http.Request) {
	if requestWantsV2Page(r) {
		s.renderV2App(w, r, "Documents")
		return
	}
	s.renderDocumentsPage(w, r, http.StatusOK, "")
}

func (s *Server) documentUploadError(w http.ResponseWriter, r *http.Request, status int, detail string) {
	if requestWantsJSON(r) {
		httpx.WriteProblem(w, status, "document_upload_failed", detail)
		return
	}
	s.renderDocumentsPage(w, r, status, detail)
}

func (s *Server) renderDocumentsPage(w http.ResponseWriter, r *http.Request, status int, formError string) {
	session, _ := sessionFromContext(r.Context())
	documents, err := s.documents.ListDocuments(r.Context())
	if err != nil {
		s.logger.Error("load tenant documents", "error", err)
		s.renderError(w, http.StatusServiceUnavailable, "The document library is temporarily unavailable.")
		return
	}
	s.render(w, status, components.DocumentsPage(
		s.tenantName(r.Context()), s.userView(session.User), documentViews(documents), s.csrfToken(session), formError,
	))
}

func (s *Server) uploadDocument(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, documentUploadLimit+(256<<10))
	if err := r.ParseMultipartForm(documentUploadLimit); err != nil {
		s.documentUploadError(w, r, http.StatusBadRequest, "Choose a supported document no larger than 15 MB.")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	if !auth.CheckCSRF(session.RawToken, r.FormValue("csrf_token"), s.config.SessionSecret) {
		httpx.WriteProblem(w, http.StatusForbidden, "invalid_csrf", "The form expired or could not be verified.")
		return
	}
	file, header, err := r.FormFile("document")
	if err != nil {
		s.documentUploadError(w, r, http.StatusBadRequest, "Choose a document to upload.")
		return
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, documentUploadLimit+1))
	if err != nil || len(content) == 0 || len(content) > documentUploadLimit {
		s.documentUploadError(w, r, http.StatusBadRequest, "The document must be no larger than 15 MB.")
		return
	}
	extracted, mediaType, err := documentextract.Extract(header.Filename, content)
	if err != nil {
		s.documentUploadError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = filepath.Base(header.Filename)
	}
	if name == "" || len(name) > 255 {
		s.documentUploadError(w, r, http.StatusBadRequest, "Give the document a name of 255 characters or fewer.")
		return
	}
	document, err := s.documents.IngestText(r.Context(), name, mediaType, extracted, session.User.ID)
	if err != nil {
		s.logger.Error("upload tenant document", "error", err, "name", name)
		s.documentUploadError(w, r, http.StatusBadRequest, "The document could not be indexed. Check the file and try again.")
		return
	}
	if requestWantsJSON(r) {
		detail, detailErr := s.documents.GetDocument(r.Context(), document.ID)
		if detailErr != nil {
			httpx.WriteJSON(w, http.StatusCreated, v2DocumentPayload{Document: components.DocumentDetailView{DocumentView: documentView(document)}})
			return
		}
		httpx.WriteJSON(w, http.StatusCreated, v2DocumentPayload{Document: documentDetailView(detail)})
		return
	}
	http.Redirect(w, r, "/documents/"+document.ID+"?uploaded=1", http.StatusSeeOther)
}

func (s *Server) documentPage(w http.ResponseWriter, r *http.Request) {
	if requestWantsV2Page(r) {
		s.renderV2App(w, r, "Document")
		return
	}
	session, _ := sessionFromContext(r.Context())
	documentID := chi.URLParam(r, "documentID")
	if _, err := uuid.Parse(documentID); err != nil {
		s.renderError(w, http.StatusNotFound, "The document was not found.")
		return
	}
	document, err := s.documents.GetDocument(r.Context(), documentID)
	if errors.Is(err, rag.ErrDocumentNotFound) {
		s.renderError(w, http.StatusNotFound, "The document was not found.")
		return
	}
	if err != nil {
		s.logger.Error("load tenant document", "error", err, "document_id", documentID)
		s.renderError(w, http.StatusServiceUnavailable, "The document is temporarily unavailable.")
		return
	}
	s.render(w, http.StatusOK, components.DocumentPage(
		s.tenantName(r.Context()), s.userView(session.User), documentDetailView(document), s.csrfToken(session), r.URL.Query().Get("uploaded") == "1",
	))
}

func (s *Server) documentOptions(ctx context.Context, selected map[string]bool) ([]components.DocumentOptionView, error) {
	documents, err := s.documents.ListDocuments(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]components.DocumentOptionView, 0, len(documents))
	for _, document := range documents {
		if document.Status != "ready" {
			continue
		}
		result = append(result, components.DocumentOptionView{
			ID: document.ID, Name: document.Name, MediaType: document.MediaType, Selected: selected[document.ID],
		})
	}
	return result, nil
}

func (s *Server) documentAttachmentsFromRequest(r *http.Request) ([]boardroom.DocumentAttachment, error) {
	if err := r.ParseForm(); err != nil {
		return nil, errors.New("the document selection was invalid")
	}
	requested := r.Form["document_id"]
	if len(requested) == 0 {
		return nil, nil
	}
	if len(requested) > 20 {
		return nil, boardroom.ErrTooManyDocuments
	}
	wanted := make(map[string]bool, len(requested))
	for _, id := range requested {
		id = strings.TrimSpace(id)
		if _, err := uuid.Parse(id); err != nil {
			return nil, errors.New("select valid documents from the tenant library")
		}
		wanted[id] = true
	}
	if len(wanted) > 20 {
		return nil, boardroom.ErrTooManyDocuments
	}
	documents, err := s.documents.ListDocuments(r.Context())
	if err != nil {
		return nil, errors.New("documents are temporarily unavailable")
	}
	result := make([]boardroom.DocumentAttachment, 0, len(wanted))
	for _, document := range documents {
		if wanted[document.ID] && document.Status == "ready" {
			result = append(result, boardroom.DocumentAttachment{ID: document.ID, Name: document.Name})
			delete(wanted, document.ID)
		}
	}
	if len(wanted) != 0 {
		return nil, errors.New("one or more selected documents are unavailable")
	}
	return result, nil
}

func documentMediaType(filename string) (string, bool) {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".txt", ".log":
		return "text/plain", true
	case ".md", ".markdown":
		return "text/markdown", true
	case ".csv":
		return "text/csv", true
	case ".tsv":
		return "text/tab-separated-values", true
	case ".json":
		return "application/json", true
	case ".xml":
		return "application/xml", true
	case ".html", ".htm":
		return "text/html", true
	case ".yaml", ".yml":
		return "text/yaml", true
	case ".pdf":
		return "application/pdf", true
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document", true
	default:
		return "", false
	}
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"status": "ok", "service": "tenant", "tenant_id": s.config.TenantID.String(),
	})
}

func (s *Server) tenantBoundary(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := normalizedHost(r.Host)
		expected := strings.ToLower(s.config.TenantSlug + "." + strings.Trim(s.config.BaseDomain, "."))
		if host != expected {
			httpx.WriteProblem(w, http.StatusMisdirectedRequest, "tenant_host_mismatch", "This request was sent to the wrong boardroom.")
			return
		}
		if forwardedTenant := r.Header.Get("X-Mainspring-Tenant-ID"); forwardedTenant != "" && forwardedTenant != s.config.TenantID.String() {
			httpx.WriteProblem(w, http.StatusMisdirectedRequest, "tenant_identity_mismatch", "The routed tenant identity does not match this boardroom.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) rejectCrossSitePosts(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch || r.Method == http.MethodDelete {
			if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
				httpx.WriteProblem(w, http.StatusForbidden, "cross_site_request", "Cross-site form submissions are not allowed.")
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				parsed, err := url.Parse(origin)
				if err != nil || !strings.EqualFold(parsed.Host, r.Host) {
					httpx.WriteProblem(w, http.StatusForbidden, "origin_mismatch", "Request origin does not match this boardroom.")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireAuthentication(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || strings.TrimSpace(cookie.Value) == "" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		user, err := s.store.UserBySession(r.Context(), cookie.Value)
		if errors.Is(err, ErrSessionNotFound) {
			s.clearSessionCookie(w)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if err != nil {
			s.logger.Error("authenticate tenant session", "error", err)
			httpx.WriteProblem(w, http.StatusInternalServerError, "session_error", "The session could not be verified.")
			return
		}
		ctx := context.WithValue(r.Context(), authContextKey, authenticatedSession{User: user, RawToken: cookie.Value})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) requireOnboarding(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state, err := s.store.GetOnboarding(r.Context(), s.config.TenantID)
		if err != nil {
			s.logger.Error("load onboarding boundary", "error", err)
			httpx.WriteProblem(w, http.StatusInternalServerError, "onboarding_error", "The onboarding state could not be verified.")
			return
		}
		if state.Status != OnboardingCompleted {
			http.Redirect(w, r, "/onboarding", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireCSRF(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := sessionFromContext(r.Context())
		if !ok {
			httpx.WriteProblem(w, http.StatusUnauthorized, "authentication_required", "Sign in is required.")
			return
		}
		if err := r.ParseForm(); err != nil || !auth.CheckCSRF(session.RawToken, r.FormValue("csrf_token"), s.config.SessionSecret) {
			httpx.WriteProblem(w, http.StatusForbidden, "invalid_csrf", "The form expired or could not be verified.")
			return
		}
		next(w, r)
	}
}

// requirePlatformControl guards narrow maintenance endpoints used only by the
// control plane. It deliberately does not grant a platform credential a tenant
// browser session or normal end-user access.
func (s *Server) requirePlatformControl(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		provided := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		expected := sha256.Sum256([]byte(s.config.ControlAdminToken))
		actual := sha256.Sum256([]byte(provided))
		if s.config.ControlAdminToken == "" || provided == "" || subtle.ConstantTimeCompare(expected[:], actual[:]) != 1 {
			httpx.WriteProblem(w, http.StatusUnauthorized, "platform_authentication_required", "A platform administrator credential is required.")
			return
		}
		next(w, r)
	}
}

func (s *Server) membersPage(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if session.User.Role != "owner" {
		s.renderError(w, http.StatusForbidden, "Only the organization owner can manage members.")
		return
	}
	members, err := s.store.ListMembers(r.Context())
	if err != nil {
		s.renderError(w, http.StatusServiceUnavailable, "Members are temporarily unavailable.")
		return
	}
	views := make([]components.MemberView, 0, len(members))
	for _, member := range members {
		views = append(views, components.MemberView{ID: member.ID, Email: member.Email, DisplayName: member.DisplayName, Role: member.Role, State: member.State})
	}
	notice := map[string]string{"invited": "Invitation created.", "removed": "Member removed."}[r.URL.Query().Get("status")]
	s.render(w, http.StatusOK, components.MembersPage(s.tenantName(r.Context()), s.userView(session.User), views, s.csrfToken(session), notice, "", ""))
}

func (s *Server) createMemberInvitation(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if session.User.Role != "owner" {
		s.renderError(w, http.StatusForbidden, "Only the organization owner can invite members.")
		return
	}
	invitation, raw, err := s.store.CreateMemberInvitation(r.Context(), r.FormValue("email"), r.FormValue("display_name"))
	if err != nil {
		s.renderMembersError(w, r, err.Error(), "")
		return
	}
	inviteURL := "https://" + s.config.TenantSlug + "." + s.config.BaseDomain + "/invitations/accept?token=" + url.QueryEscape(raw)
	if s.config.Development {
		s.renderMembersError(w, r, "", inviteURL)
		return
	}
	_, err = s.email.Send(r.Context(), "member-invite-"+invitation.ID, mailbox.OutgoingMessage{To: []string{invitation.Email}, Subject: "You’re invited to " + s.tenantName(r.Context()), Body: "You have been invited to join " + s.tenantName(r.Context()) + ". Set your password and activate your account: " + inviteURL}, "system", session.User.ID, nil)
	if err != nil {
		s.renderMembersError(w, r, "Invitation was created, but email could not be sent. Configure the organization mailbox and invite again: "+err.Error(), "")
		return
	}
	http.Redirect(w, r, "/team?status=invited", http.StatusSeeOther)
}

func (s *Server) renderMembersError(w http.ResponseWriter, r *http.Request, formError, inviteURL string) {
	session, _ := sessionFromContext(r.Context())
	members, err := s.store.ListMembers(r.Context())
	if err != nil {
		s.renderError(w, http.StatusServiceUnavailable, "Members are temporarily unavailable.")
		return
	}
	views := make([]components.MemberView, 0, len(members))
	for _, member := range members {
		views = append(views, components.MemberView{ID: member.ID, Email: member.Email, DisplayName: member.DisplayName, Role: member.Role, State: member.State})
	}
	s.render(w, http.StatusBadRequest, components.MembersPage(s.tenantName(r.Context()), s.userView(session.User), views, s.csrfToken(session), "", formError, inviteURL))
}

func (s *Server) removeMember(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if session.User.Role != "owner" {
		s.renderError(w, http.StatusForbidden, "Only the organization owner can remove members.")
		return
	}
	if err := s.store.DisableMember(r.Context(), chi.URLParam(r, "userID")); err != nil {
		s.renderMembersError(w, r, "That member could not be removed.", "")
		return
	}
	http.Redirect(w, r, "/team?status=removed", http.StatusSeeOther)
}

func (s *Server) acceptInvitationPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, http.StatusOK, components.AcceptInvitationPage(s.tenantName(r.Context()), r.URL.Query().Get("token"), ""))
}
func (s *Server) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.render(w, http.StatusBadRequest, components.AcceptInvitationPage(s.tenantName(r.Context()), "", "The invitation form could not be read."))
		return
	}
	user, err := s.store.AcceptMemberInvitation(r.Context(), r.FormValue("token"), r.FormValue("password"))
	if err != nil {
		s.render(w, http.StatusBadRequest, components.AcceptInvitationPage(s.tenantName(r.Context()), r.FormValue("token"), "This invitation is invalid, expired, or could not be activated."))
		return
	}
	if err := s.startSession(w, r, user); err != nil {
		s.renderError(w, http.StatusInternalServerError, "Your account was activated, but sign-in could not be completed.")
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) announcementsPage(w http.ResponseWriter, r *http.Request) {
	if requestWantsV2Page(r) {
		s.renderV2App(w, r, "Inbox")
		return
	}
	session, _ := sessionFromContext(r.Context())
	items, err := s.store.ListAnnouncements(r.Context(), session.User.ID)
	if err != nil {
		s.renderError(w, http.StatusServiceUnavailable, "Service announcements are temporarily unavailable.")
		return
	}
	views := make([]components.AnnouncementView, 0, len(items))
	for _, item := range items {
		views = append(views, components.AnnouncementView{ID: item.ID, Title: item.Title, Body: item.Body, Category: strings.ReplaceAll(item.Category, "_", " "), PublishedAt: item.PublishedAt.Local().Format("Jan 2, 2006 3:04 PM"), Read: item.ReadAt != nil})
	}
	s.render(w, http.StatusOK, components.AnnouncementsPage(s.tenantName(r.Context()), s.userView(session.User), views, s.csrfToken(session)))
}
func (s *Server) readAnnouncement(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if err := s.store.MarkAnnouncementRead(r.Context(), session.User.ID, chi.URLParam(r, "announcementID")); err != nil {
		s.renderError(w, http.StatusInternalServerError, "The announcement could not be marked read.")
		return
	}
	http.Redirect(w, r, "/inbox", http.StatusSeeOther)
}

func (s *Server) listMessengerMessages(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListMessengerMessages(r.Context(), chi.URLParam(r, "channel"))
	if err != nil {
		httpx.WriteProblem(w, http.StatusBadRequest, "invalid_messenger_channel", "The requested chat channel is not available.")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"messages": items})
}

func (s *Server) createMessengerMessage(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	item, err := s.store.CreateMessengerMessage(r.Context(), chi.URLParam(r, "channel"), session.User, r.FormValue("body"))
	if err != nil {
		httpx.WriteProblem(w, http.StatusBadRequest, "invalid_messenger_message", err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, item)
}

func (s *Server) transferOwnership(w http.ResponseWriter, r *http.Request) {
	var input struct {
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil || input.UserID == "" {
		httpx.WriteProblem(w, http.StatusBadRequest, "invalid_owner", "A member user ID is required.")
		return
	}
	if err := s.store.TransferOwnership(r.Context(), input.UserID); err != nil {
		httpx.WriteProblem(w, http.StatusBadRequest, "ownership_transfer_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) deliverAnnouncement(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Body     string `json:"body"`
		Category string `json:"category"`
		Target   string `json:"target"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
		httpx.WriteProblem(w, http.StatusBadRequest, "invalid_announcement", "Announcement payload is invalid.")
		return
	}
	if err := s.store.DeliverAnnouncement(r.Context(), input.ID, input.Title, input.Body, input.Category, input.Target); err != nil {
		httpx.WriteProblem(w, http.StatusBadRequest, "announcement_delivery_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) setupPage(w http.ResponseWriter, r *http.Request) {
	hasUsers, err := s.store.HasUsers(r.Context())
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "The boardroom setup state could not be loaded.")
		return
	}
	if hasUsers {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.render(w, http.StatusOK, components.SetupPage(s.tenantName(r.Context()), ""))
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.render(w, http.StatusBadRequest, components.SetupPage(s.tenantName(r.Context()), "The setup form could not be read."))
		return
	}
	expected := sha256.Sum256([]byte(s.config.SetupToken))
	actual := sha256.Sum256([]byte(r.FormValue("setup_token")))
	if subtle.ConstantTimeCompare(expected[:], actual[:]) != 1 {
		s.render(w, http.StatusForbidden, components.SetupPage(s.tenantName(r.Context()), "The setup token is not valid."))
		return
	}
	user, err := s.store.CreateOwner(r.Context(), r.FormValue("email"), r.FormValue("display_name"), r.FormValue("password"))
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrSetupComplete) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		s.render(w, status, components.SetupPage(s.tenantName(r.Context()), err.Error()))
		return
	}
	if err := s.startSession(w, r, user); err != nil {
		s.renderError(w, http.StatusInternalServerError, "The owner was created, but the session could not be started.")
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	hasUsers, err := s.store.HasUsers(r.Context())
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "The boardroom login state could not be loaded.")
		return
	}
	if !hasUsers {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	s.render(w, http.StatusOK, components.LoginPage(s.tenantName(r.Context()), ""))
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.render(w, http.StatusBadRequest, components.LoginPage(s.tenantName(r.Context()), "The login form could not be read."))
		return
	}
	user, err := s.store.Authenticate(r.Context(), r.FormValue("email"), r.FormValue("password"))
	if errors.Is(err, ErrAuthenticationFailed) {
		s.render(w, http.StatusUnauthorized, components.LoginPage(s.tenantName(r.Context()), "The email or password is incorrect."))
		return
	}
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Sign in could not be completed.")
		return
	}
	if err := s.startSession(w, r, user); err != nil {
		s.renderError(w, http.StatusInternalServerError, "Sign in could not be completed.")
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if err := s.store.DeleteSession(r.Context(), session.RawToken); err != nil {
		s.logger.Error("delete tenant session", "error", err)
	}
	s.clearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) onboardingPage(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	state, err := s.store.GetOnboarding(r.Context(), s.config.TenantID)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Onboarding could not be loaded.")
		return
	}
	if state.Status == OnboardingCompleted {
		rooms, err := s.boardrooms.List(r.Context())
		if err != nil || len(rooms) == 0 {
			s.renderError(w, http.StatusInternalServerError, "Your completed boardroom could not be loaded.")
			return
		}
		template := s.selectedBusinessTemplate(state)
		s.render(w, http.StatusOK, components.OnboardingCompletePage(
			s.tenantName(r.Context()), s.userView(session.User), boardroomView(rooms[0]), s.csrfToken(session), template.String(), state.Business.Stage,
		))
		return
	}
	if state.Business.BusinessName == "" {
		state.Business.BusinessName = s.tenantName(r.Context())
		state.Business.TimeZone = "America/New_York"
		state.Business.CustomerMix = "mixed"
		state.Business.TeamSize = 1
	}
	if state.Business.Template != "" && r.URL.Query().Get("legacy") != "1" {
		http.Redirect(w, r, "/baseline", http.StatusSeeOther)
		return
	}
	if r.URL.Query().Get("choose_template") == "true" {
		state.Business.Template = ""
	}
	step := state.CurrentStep
	if requested, parseErr := strconv.Atoi(r.URL.Query().Get("step")); parseErr == nil && requested >= 1 && requested <= state.CurrentStep {
		step = requested
	}
	s.renderOnboarding(r.Context(), w, http.StatusOK, session, state, step, "")
}

func (s *Server) saveOnboardingTemplate(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	state, err := s.editableOnboarding(r.Context())
	if err != nil {
		http.Redirect(w, r, "/onboarding", http.StatusSeeOther)
		return
	}
	selection := strings.ToLower(strings.TrimSpace(r.FormValue("template_selection")))
	if selection == "startup" {
		state.Business = BusinessProfile{Stage: BusinessStageStarting}
		state.Operations = OperatingPlaybook{}
		state.Priorities = nil
		state.Blueprint = BoardroomBlueprint{}
		state.Permissions = PermissionPlan{}
		state.CurrentStep = 1
		if err := s.store.SaveOnboarding(r.Context(), s.config.TenantID, state); err != nil {
			s.renderOnboarding(r.Context(), w, http.StatusInternalServerError, session, state, 1, "The startup path could not be saved.")
			return
		}
		http.Redirect(w, r, "/onboarding?step=1", http.StatusSeeOther)
		return
	}

	template, variant, suggestedModel, ok := onboardingTemplateSelection(selection)
	if !ok {
		s.renderOnboarding(r.Context(), w, http.StatusBadRequest, session, state, 1, "Choose trades, SaaS, MSP, or the start-from-scratch path.")
		return
	}
	stage := state.Business.Stage
	if stage == "" {
		stage = BusinessStageOperating
	}
	changed := state.Business.Template != "" && (state.Business.Template != template.String() || state.Business.Variant != variant)
	if changed || state.Business.Template == "" {
		state.Business = BusinessProfile{Template: template.String(), Variant: variant, Stage: stage, Trade: suggestedModel}
		state.Operations = OperatingPlaybook{}
		state.Priorities = nil
		state.Blueprint = BoardroomBlueprint{}
		state.Permissions = PermissionPlan{}
		state.CurrentStep = 1
	} else {
		state.Business.Template = template.String()
		state.Business.Variant = variant
	}
	if err := s.store.SaveOnboarding(r.Context(), s.config.TenantID, state); err != nil {
		s.renderOnboarding(r.Context(), w, http.StatusInternalServerError, session, state, 1, "The business template could not be saved.")
		return
	}
	http.Redirect(w, r, "/onboarding?step=1", http.StatusSeeOther)
}

func onboardingTemplateSelection(selection string) (BusinessTemplate, string, string, bool) {
	switch selection {
	case "trades":
		return TemplateTrades, "trades", "", true
	case "saas":
		return TemplateSoftware, "saas", "B2B SaaS", true
	case "msp":
		return TemplateSoftware, "msp", "Managed service provider (MSP)", true
	default:
		return "", "", "", false
	}
}

func (s *Server) saveOnboardingStage(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	state, err := s.editableOnboarding(r.Context())
	if err != nil {
		http.Redirect(w, r, "/onboarding", http.StatusSeeOther)
		return
	}
	requested := strings.ToLower(strings.TrimSpace(r.FormValue("business_stage")))
	if requested != BusinessStageOperating && requested != BusinessStageStarting {
		s.renderOnboarding(r.Context(), w, http.StatusBadRequest, session, state, 1, "Choose whether this business is already operating or is starting from scratch.")
		return
	}
	previous := state.Business.Stage
	state.Business.Stage = requested
	if previous != "" && NormalizeBusinessStage(previous) != requested {
		state.Operations = OperatingPlaybook{}
		state.Priorities = nil
		state.Blueprint = BoardroomBlueprint{}
		state.Permissions = PermissionPlan{}
		state.CurrentStep = 1
	}
	if err := s.store.SaveOnboarding(r.Context(), s.config.TenantID, state); err != nil {
		s.renderOnboarding(r.Context(), w, http.StatusInternalServerError, session, state, 1, "The onboarding path could not be saved.")
		return
	}
	http.Redirect(w, r, "/onboarding?step=1", http.StatusSeeOther)
}

func (s *Server) saveOnboardingBusiness(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	state, err := s.editableOnboarding(r.Context())
	if err != nil {
		http.Redirect(w, r, "/onboarding", http.StatusSeeOther)
		return
	}
	teamSize, parseErr := strconv.Atoi(r.FormValue("team_size"))
	state.Business = BusinessProfile{
		Template:     state.Business.Template,
		Variant:      state.Business.Variant,
		Stage:        NormalizeBusinessStage(state.Business.Stage),
		BusinessName: strings.TrimSpace(r.FormValue("business_name")),
		WebsiteURL:   strings.TrimSpace(r.FormValue("website_url")),
		Trade:        strings.TrimSpace(r.FormValue("trade")), Services: strings.TrimSpace(r.FormValue("services")),
		ServiceArea: strings.TrimSpace(r.FormValue("service_area")), TimeZone: strings.TrimSpace(r.FormValue("time_zone")),
		TeamSize: teamSize, CustomerMix: strings.TrimSpace(r.FormValue("customer_mix")),
		WorkingHours: strings.TrimSpace(r.FormValue("working_hours")), EmergencyService: r.FormValue("emergency_service") == "true",
		CurrentSystems: cleanFormValues(r.Form["current_systems"], 8, 80),
	}
	if state.Business.BusinessName == "" || state.Business.Trade == "" || state.Business.ServiceArea == "" || parseErr != nil || teamSize < 1 || teamSize > 10000 {
		message := "Enter the business name, primary trade, service area, and a valid team size."
		if s.selectedBusinessTemplate(state).IsSoftware() {
			message = "Enter the company name, business model, target market, and a valid team size."
		}
		s.renderOnboarding(r.Context(), w, http.StatusBadRequest, session, state, 1, message)
		return
	}
	if state.Business.WebsiteURL != "" {
		website, err := url.Parse(state.Business.WebsiteURL)
		if err != nil || (website.Scheme != "http" && website.Scheme != "https") || website.Hostname() == "" || website.User != nil {
			s.renderOnboarding(r.Context(), w, http.StatusBadRequest, session, state, 1, "Enter a complete public website URL beginning with https:// or leave it blank.")
			return
		}
		website.Fragment = ""
		state.Business.WebsiteURL = website.String()
	}
	if _, err := time.LoadLocation(state.Business.TimeZone); err != nil {
		s.renderOnboarding(r.Context(), w, http.StatusBadRequest, session, state, 1, "Use a valid IANA time zone such as America/New_York.")
		return
	}
	state.Status = OnboardingInProgress
	state.CurrentStep = maxInt(state.CurrentStep, 2)
	if err := s.store.SaveOnboarding(r.Context(), s.config.TenantID, state); err != nil {
		s.renderOnboarding(r.Context(), w, http.StatusInternalServerError, session, state, 1, "The business profile could not be saved.")
		return
	}
	http.Redirect(w, r, "/onboarding?step=2", http.StatusSeeOther)
}

func (s *Server) saveOnboardingOperations(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	state, err := s.editableOnboarding(r.Context())
	if err != nil {
		http.Redirect(w, r, "/onboarding", http.StatusSeeOther)
		return
	}
	if state.CurrentStep < 2 || state.Business.BusinessName == "" {
		http.Redirect(w, r, "/onboarding", http.StatusSeeOther)
		return
	}
	state.Operations = OperatingPlaybook{
		LeadIntake: strings.TrimSpace(r.FormValue("lead_intake")), Scheduling: strings.TrimSpace(r.FormValue("scheduling")),
		EstimateToJob: strings.TrimSpace(r.FormValue("estimate_to_job")), JobToInvoice: strings.TrimSpace(r.FormValue("job_to_invoice")),
		Payments: strings.TrimSpace(r.FormValue("payments")), VendorBills: strings.TrimSpace(r.FormValue("vendor_bills")),
		BiggestBottleneck: strings.TrimSpace(r.FormValue("biggest_bottleneck")), ImportantExceptions: strings.TrimSpace(r.FormValue("important_exceptions")),
	}
	if state.Operations.LeadIntake == "" || state.Operations.Scheduling == "" || state.Operations.JobToInvoice == "" || state.Operations.BiggestBottleneck == "" {
		message := "Tell Mia how leads arrive, how jobs are scheduled, how work becomes an invoice, and where the biggest bottleneck is."
		if s.selectedBusinessTemplate(state).IsSoftware() {
			message = "Tell Mia how demand arrives, how work is prioritized, how releases reach customers and billing, and where the biggest bottleneck is."
		}
		s.renderOnboarding(r.Context(), w, http.StatusBadRequest, session, state, 2, message)
		return
	}
	state.Status = OnboardingInProgress
	state.CurrentStep = maxInt(state.CurrentStep, 3)
	if err := s.store.SaveOnboarding(r.Context(), s.config.TenantID, state); err != nil {
		s.renderOnboarding(r.Context(), w, http.StatusInternalServerError, session, state, 2, "The operating playbook could not be saved.")
		return
	}
	http.Redirect(w, r, "/onboarding?step=3", http.StatusSeeOther)
}

func (s *Server) saveOnboardingPriorities(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	state, err := s.editableOnboarding(r.Context())
	if err != nil {
		http.Redirect(w, r, "/onboarding", http.StatusSeeOther)
		return
	}
	if state.CurrentStep < 3 || state.Operations.LeadIntake == "" {
		http.Redirect(w, r, "/onboarding", http.StatusSeeOther)
		return
	}
	allowed := make(map[string]bool)
	template := s.selectedBusinessTemplate(state)
	for _, value := range components.PriorityOptionsForOnboarding(template.String(), state.Business.Stage) {
		allowed[value] = true
	}
	state.Priorities = nil
	for _, value := range cleanFormValues(r.Form["priorities"], 3, 120) {
		if allowed[value] {
			state.Priorities = append(state.Priorities, value)
		}
	}
	if len(state.Priorities) == 0 || len(r.Form["priorities"]) > 3 {
		state.Priorities = nil
		s.renderOnboarding(r.Context(), w, http.StatusBadRequest, session, state, 3, "Choose between one and three priorities.")
		return
	}
	state.Blueprint = GenerateBlueprintForTemplate(state, template)
	state.Permissions = DefaultPermissionPlan()
	state.Status = OnboardingInProgress
	state.CurrentStep = maxInt(state.CurrentStep, 4)
	if err := s.store.SaveOnboarding(r.Context(), s.config.TenantID, state); err != nil {
		s.renderOnboarding(r.Context(), w, http.StatusInternalServerError, session, state, 3, "The priorities could not be saved.")
		return
	}
	http.Redirect(w, r, "/onboarding?step=4", http.StatusSeeOther)
}

func (s *Server) saveOnboardingTeam(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	state, err := s.editableOnboarding(r.Context())
	if err != nil {
		http.Redirect(w, r, "/onboarding", http.StatusSeeOther)
		return
	}
	if state.CurrentStep < 4 || len(state.Priorities) == 0 {
		http.Redirect(w, r, "/onboarding", http.StatusSeeOther)
		return
	}
	if len(state.Blueprint.Personas) == 0 {
		state.Blueprint = GenerateBlueprintForTemplate(state, s.selectedBusinessTemplate(state))
	}
	enabled := 0
	for index := range state.Blueprint.Personas {
		persona := &state.Blueprint.Personas[index]
		persona.Enabled = r.FormValue("enabled_"+persona.Key) == "true"
		name := strings.TrimSpace(r.FormValue("name_" + persona.Key))
		if len(name) > 80 || (persona.Enabled && name == "") {
			s.renderOnboarding(r.Context(), w, http.StatusBadRequest, session, state, 4, "Each proposed team member needs a name of 80 characters or fewer.")
			return
		}
		if name != "" {
			persona.Name = name
		}
		if persona.Enabled {
			enabled++
		}
	}
	if enabled == 0 {
		s.renderOnboarding(r.Context(), w, http.StatusBadRequest, session, state, 4, "Include at least one person in the boardroom.")
		return
	}
	state.CurrentStep = maxInt(state.CurrentStep, 5)
	if err := s.store.SaveOnboarding(r.Context(), s.config.TenantID, state); err != nil {
		s.renderOnboarding(r.Context(), w, http.StatusInternalServerError, session, state, 4, "The proposed team could not be saved.")
		return
	}
	http.Redirect(w, r, "/onboarding?step=5", http.StatusSeeOther)
}

func (s *Server) saveOnboardingPermissions(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	state, err := s.editableOnboarding(r.Context())
	if err != nil {
		http.Redirect(w, r, "/onboarding", http.StatusSeeOther)
		return
	}
	if state.CurrentStep < 5 || len(state.Blueprint.Personas) == 0 {
		http.Redirect(w, r, "/onboarding", http.StatusSeeOther)
		return
	}
	readBusinessRecords := r.FormValue("read_business_records") == "true"
	state.Permissions = PermissionPlan{
		ReadBusinessRecords:  readBusinessRecords,
		ResearchPublicWeb:    true,
		CommentOnDocuments:   readBusinessRecords && r.FormValue("comment_on_documents") == "true",
		PrepareInvoiceDrafts: r.FormValue("prepare_invoice_drafts") == "true",
		DraftCustomerEmail:   r.FormValue("draft_customer_email") == "true",
		ReadEmailInbox:       r.FormValue("read_email_inbox") == "true",
		SendEmail:            r.FormValue("send_email") == "true",
		ProposeScheduleEdits: r.FormValue("propose_schedule_edits") == "true",
		ProposePayments:      r.FormValue("propose_payments") == "true",
	}
	state.CurrentStep = maxInt(state.CurrentStep, 6)
	if err := s.store.SaveOnboarding(r.Context(), s.config.TenantID, state); err != nil {
		s.renderOnboarding(r.Context(), w, http.StatusInternalServerError, session, state, 5, "The authority plan could not be saved.")
		return
	}
	http.Redirect(w, r, "/onboarding?step=6", http.StatusSeeOther)
}

func (s *Server) launchOnboarding(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	state, err := s.editableOnboarding(r.Context())
	if err != nil {
		http.Redirect(w, r, "/onboarding", http.StatusSeeOther)
		return
	}
	if state.CurrentStep < 6 || state.Business.BusinessName == "" || len(state.Blueprint.Personas) == 0 {
		s.renderOnboarding(r.Context(), w, http.StatusBadRequest, session, state, 6, "Finish the earlier onboarding steps before launching the boardroom.")
		return
	}
	if err := s.store.CompleteOnboarding(r.Context(), s.config.TenantID, state); err != nil {
		s.logger.Error("complete tenant onboarding", "error", err)
		s.renderOnboarding(r.Context(), w, http.StatusInternalServerError, session, state, 6, "The boardroom could not be launched. Your draft is still safe.")
		return
	}
	http.Redirect(w, r, "/onboarding", http.StatusSeeOther)
}

func (s *Server) developmentPage(w http.ResponseWriter, r *http.Request) {
	if !s.config.Development {
		http.NotFound(w, r)
		return
	}
	session, _ := sessionFromContext(r.Context())
	if session.User.Role != "owner" {
		httpx.WriteProblem(w, http.StatusForbidden, "permission_denied", "Only an owner or administrator can use demo tools.")
		return
	}
	rooms, err := s.boardrooms.List(r.Context())
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Demo tools could not load the boardroom.")
		return
	}
	s.render(w, http.StatusOK, components.DevelopmentPage(s.tenantName(r.Context()), s.userView(session.User), boardroomViews(rooms), s.csrfToken(session)))
}

func (s *Server) resetOnboarding(w http.ResponseWriter, r *http.Request) {
	if !s.config.Development {
		http.NotFound(w, r)
		return
	}
	session, _ := sessionFromContext(r.Context())
	if session.User.Role != "owner" || r.FormValue("confirm") != "reset" {
		httpx.WriteProblem(w, http.StatusForbidden, "permission_denied", "The onboarding reset was not authorized.")
		return
	}
	if err := s.store.ResetOnboarding(r.Context(), s.config.TenantID, s.config.TenantName); err != nil {
		s.logger.Error("reset tenant onboarding", "error", err)
		httpx.WriteProblem(w, http.StatusInternalServerError, "onboarding_reset_failed", "Onboarding could not be reset.")
		return
	}
	http.Redirect(w, r, "/onboarding", http.StatusSeeOther)
}

func (s *Server) editableOnboarding(ctx context.Context) (Onboarding, error) {
	state, err := s.store.GetOnboarding(ctx, s.config.TenantID)
	if err != nil {
		return Onboarding{}, err
	}
	if state.Status == OnboardingCompleted {
		return Onboarding{}, errors.New("onboarding is already complete")
	}
	return state, nil
}

func (s *Server) renderOnboarding(ctx context.Context, w http.ResponseWriter, status int, session authenticatedSession, state Onboarding, step int, formError string) {
	template := s.selectedBusinessTemplate(state)
	s.render(w, status, components.OnboardingPage(
		s.tenantName(ctx), s.userView(session.User), onboardingView(state, template.String()), step, s.csrfToken(session), formError,
	))
}

func (s *Server) selectedBusinessTemplate(state Onboarding) BusinessTemplate {
	if strings.TrimSpace(state.Business.Template) != "" {
		return ParseBusinessTemplate(state.Business.Template)
	}
	return s.config.BusinessTemplate
}

func (s *Server) tenantBusinessTemplate(ctx context.Context) BusinessTemplate {
	state, err := s.store.GetOnboarding(ctx, s.config.TenantID)
	if err != nil {
		return s.config.BusinessTemplate
	}
	return s.selectedBusinessTemplate(state)
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	if requestWantsV2Page(r) {
		s.renderV2App(w, r, "Home")
		return
	}
	session, _ := sessionFromContext(r.Context())
	rooms, err := s.boardrooms.List(r.Context())
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Boardrooms could not be loaded.")
		return
	}
	views := make([]components.BoardroomCardView, 0, len(rooms))
	for _, room := range rooms {
		views = append(views, boardroomView(room))
	}
	welcome := r.URL.Query().Get("welcome") == "1"
	s.render(w, http.StatusOK, components.DashboardPage(s.tenantName(r.Context()), s.userView(session.User), views, s.csrfToken(session), welcome))
}

func (s *Server) boardroomPage(w http.ResponseWriter, r *http.Request) {
	if requestWantsV2Page(r) {
		s.renderV2App(w, r, "Boardroom")
		return
	}
	session, _ := sessionFromContext(r.Context())
	roomID, err := domain.ParseBoardroomID(chi.URLParam(r, "boardroomID"))
	if err != nil {
		s.renderError(w, http.StatusNotFound, "Boardroom was not found.")
		return
	}
	room, err := s.boardrooms.Get(r.Context(), roomID)
	if errors.Is(err, boardroom.ErrBoardroomNotFound) {
		s.renderError(w, http.StatusNotFound, "Boardroom was not found.")
		return
	}
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Boardroom could not be loaded.")
		return
	}
	personas, err := s.boardrooms.Personas(r.Context(), roomID)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Boardroom personas could not be loaded.")
		return
	}
	conversations, err := s.boardrooms.ListConversations(r.Context(), roomID, 50)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Boardroom conversations could not be loaded.")
		return
	}
	documents, err := s.documentOptions(r.Context(), nil)
	if err != nil {
		s.logger.Warn("load boardroom document options", "boardroom_id", roomID.String(), "error", err)
		documents = nil
	}
	s.render(w, http.StatusOK, components.BoardroomPage(
		s.tenantName(r.Context()), s.userView(session.User), boardroomView(room), personaViews(personas), conversationViews(conversations), documents, s.csrfToken(session), s.tenantBusinessTemplate(r.Context()).String(),
	))
}

func (s *Server) createRun(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	roomID, err := domain.ParseBoardroomID(chi.URLParam(r, "boardroomID"))
	if err != nil {
		if requestWantsJSON(r) {
			httpx.WriteProblem(w, http.StatusNotFound, "boardroom_not_found", "Boardroom was not found.")
			return
		}
		s.renderError(w, http.StatusNotFound, "Boardroom was not found.")
		return
	}
	prompt := strings.TrimSpace(r.FormValue("prompt"))
	if prompt == "" || len(prompt) > 12000 {
		if requestWantsJSON(r) {
			httpx.WriteProblem(w, http.StatusBadRequest, "invalid_prompt", "The boardroom request must contain between 1 and 12,000 characters.")
			return
		}
		s.renderError(w, http.StatusBadRequest, "The boardroom request must contain between 1 and 12,000 characters.")
		return
	}
	attachments, err := s.documentAttachmentsFromRequest(r)
	if err != nil {
		if requestWantsJSON(r) {
			httpx.WriteProblem(w, http.StatusBadRequest, "invalid_documents", err.Error())
			return
		}
		s.renderError(w, http.StatusBadRequest, err.Error())
		return
	}
	run, err := s.boardrooms.CreateRunWithDocuments(r.Context(), roomID, session.User.ID, prompt, attachments)
	if err != nil {
		if requestWantsJSON(r) {
			httpx.WriteProblem(w, http.StatusBadRequest, "run_not_created", err.Error())
			return
		}
		s.renderError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.dispatcher.Dispatch(r.Context(), run.ID); err != nil {
		_ = s.boardrooms.SetRunStatus(r.Context(), run.ID, domain.RunFailed, "The durable workflow could not be started.")
		s.logger.Error("dispatch boardroom run", "run_id", run.ID.String(), "error", err)
		if requestWantsJSON(r) {
			httpx.WriteProblem(w, http.StatusServiceUnavailable, "dispatch_failed", "The boardroom could not be started. Please try again.")
			return
		}
		s.renderError(w, http.StatusServiceUnavailable, "The boardroom could not be started. Please try again.")
		return
	}
	if requestWantsJSON(r) {
		payload, payloadErr := s.loadV2Conversation(r.Context(), run.ConversationID.String())
		if payloadErr != nil {
			httpx.WriteProblem(w, http.StatusServiceUnavailable, "conversation_unavailable", "The conversation was created but could not be loaded.")
			return
		}
		httpx.WriteJSON(w, http.StatusAccepted, payload)
		return
	}
	http.Redirect(w, r, "/conversations/"+run.ConversationID.String(), http.StatusSeeOther)
}

func (s *Server) conversationPage(w http.ResponseWriter, r *http.Request) {
	if requestWantsV2Page(r) {
		s.renderV2App(w, r, "Conversation")
		return
	}
	session, _ := sessionFromContext(r.Context())
	conversationID, err := domain.ParseConversationID(chi.URLParam(r, "conversationID"))
	if err != nil {
		s.renderError(w, http.StatusNotFound, "Conversation was not found.")
		return
	}
	conversation, err := s.boardrooms.GetConversation(r.Context(), conversationID)
	if errors.Is(err, boardroom.ErrConversationNotFound) {
		s.renderError(w, http.StatusNotFound, "Conversation was not found.")
		return
	}
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Conversation could not be loaded.")
		return
	}
	run, err := s.boardrooms.GetRun(r.Context(), conversation.LatestRunID)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Conversation activity could not be loaded.")
		return
	}
	room, err := s.boardrooms.Get(r.Context(), conversation.BoardroomID)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Boardroom could not be loaded.")
		return
	}
	personas, err := s.boardrooms.Personas(r.Context(), conversation.BoardroomID)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Boardroom personas could not be loaded.")
		return
	}
	messages, cursor, err := s.boardrooms.MessagesSnapshot(r.Context(), conversation.ID, run.ID)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Conversation messages could not be loaded.")
		return
	}
	attachments, err := s.boardrooms.ConversationDocuments(r.Context(), conversation.ID)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Conversation documents could not be loaded.")
		return
	}
	selected := make(map[string]bool, len(attachments))
	for _, attachment := range attachments {
		selected[attachment.ID] = true
	}
	documents, err := s.documentOptions(r.Context(), selected)
	if err != nil {
		s.logger.Warn("load conversation document options", "conversation_id", conversation.ID.String(), "error", err)
		documents = nil
	}
	pendingApprovals, err := s.approvals.PendingApprovalsForRun(r.Context(), run.ID)
	if err != nil {
		s.logger.Warn("load conversation approvals", "run_id", run.ID.String(), "error", err)
		pendingApprovals = nil
	}
	runView := componentRun(run, cursor)
	s.render(w, http.StatusOK, components.ConversationPage(
		s.tenantName(r.Context()), s.userView(session.User), boardroomView(room), personaViews(personas),
		conversationView(conversation), runView, messageViews(messages), documentAttachmentViews(attachments), documents, approvalViews(pendingApprovals), s.csrfToken(session),
	))
}

func (s *Server) createFollowUp(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	conversationID, err := domain.ParseConversationID(chi.URLParam(r, "conversationID"))
	if err != nil {
		if requestWantsJSON(r) {
			httpx.WriteProblem(w, http.StatusNotFound, "conversation_not_found", "Conversation was not found.")
			return
		}
		s.renderError(w, http.StatusNotFound, "Conversation was not found.")
		return
	}
	prompt := strings.TrimSpace(r.FormValue("prompt"))
	if prompt == "" || len(prompt) > 12000 {
		if requestWantsJSON(r) {
			httpx.WriteProblem(w, http.StatusBadRequest, "invalid_prompt", "The follow-up must contain between 1 and 12,000 characters.")
			return
		}
		s.renderError(w, http.StatusBadRequest, "The follow-up must contain between 1 and 12,000 characters.")
		return
	}
	attachments, err := s.documentAttachmentsFromRequest(r)
	if err != nil {
		if requestWantsJSON(r) {
			httpx.WriteProblem(w, http.StatusBadRequest, "invalid_documents", err.Error())
			return
		}
		s.renderError(w, http.StatusBadRequest, err.Error())
		return
	}
	run, err := s.boardrooms.CreateFollowUpRunWithDocuments(r.Context(), conversationID, session.User.ID, prompt, attachments)
	if errors.Is(err, boardroom.ErrConversationBusy) {
		if requestWantsJSON(r) {
			httpx.WriteProblem(w, http.StatusConflict, "conversation_busy", "The boardroom is still working on the previous message.")
			return
		}
		http.Redirect(w, r, "/conversations/"+conversationID.String(), http.StatusSeeOther)
		return
	}
	if errors.Is(err, boardroom.ErrConversationNotFound) {
		if requestWantsJSON(r) {
			httpx.WriteProblem(w, http.StatusNotFound, "conversation_not_found", "Conversation was not found.")
			return
		}
		s.renderError(w, http.StatusNotFound, "Conversation was not found.")
		return
	}
	if err != nil {
		if requestWantsJSON(r) {
			httpx.WriteProblem(w, http.StatusBadRequest, "follow_up_failed", "The follow-up could not be added.")
			return
		}
		s.renderError(w, http.StatusBadRequest, "The follow-up could not be added.")
		return
	}
	if err := s.dispatcher.Dispatch(r.Context(), run.ID); err != nil {
		_ = s.boardrooms.SetRunStatus(r.Context(), run.ID, domain.RunFailed, "The durable workflow could not be started.")
		s.logger.Error("dispatch conversation follow-up", "run_id", run.ID.String(), "error", err)
		if requestWantsJSON(r) {
			httpx.WriteProblem(w, http.StatusServiceUnavailable, "dispatch_failed", "The boardroom could not be started. Please try again.")
			return
		}
		s.renderError(w, http.StatusServiceUnavailable, "The boardroom could not be started. Please try again.")
		return
	}
	if requestWantsJSON(r) {
		payload, payloadErr := s.loadV2Conversation(r.Context(), conversationID.String())
		if payloadErr != nil {
			httpx.WriteProblem(w, http.StatusServiceUnavailable, "conversation_unavailable", "The follow-up was added but the conversation could not be loaded.")
			return
		}
		httpx.WriteJSON(w, http.StatusAccepted, payload)
		return
	}
	http.Redirect(w, r, "/conversations/"+conversationID.String(), http.StatusSeeOther)
}

func (s *Server) schedulesPage(w http.ResponseWriter, r *http.Request) {
	s.renderSchedules(w, r, http.StatusOK, "")
}

func (s *Server) workQueuePage(w http.ResponseWriter, r *http.Request) {
	if !requestWantsV2Page(r) {
		s.renderWorkQueue(w, r, http.StatusOK, "")
		return
	}
	s.renderV2App(w, r, "Work")
}

func (s *Server) createWorkItem(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	var dueAt *time.Time
	if value := strings.TrimSpace(r.FormValue("due_date")); value != "" {
		parsed, err := time.Parse("2006-01-02", value)
		if err != nil {
			s.renderWorkQueue(w, r, http.StatusBadRequest, "Enter a valid due date.")
			return
		}
		endOfDay := parsed.Add(24*time.Hour - time.Nanosecond)
		dueAt = &endOfDay
	}
	assignedUserID := ""
	if r.FormValue("assign_to_me") == "true" {
		assignedUserID = session.User.ID
	}
	item, err := s.store.CreateWorkItem(r.Context(), CreateWorkItemInput{
		Kind: r.FormValue("kind"), Title: r.FormValue("title"), Description: r.FormValue("description"),
		Priority: r.FormValue("priority"), Source: "user", CreatedByUserID: session.User.ID,
		AssignedUserID: assignedUserID, DueAt: dueAt,
	})
	if err != nil {
		if requestWantsJSON(r) {
			httpx.WriteProblem(w, http.StatusBadRequest, "invalid_work_item", err.Error())
			return
		}
		s.renderWorkQueue(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if requestWantsJSON(r) {
		httpx.WriteJSON(w, http.StatusCreated, map[string]any{"item": workItemViews([]WorkItem{item})[0]})
		return
	}
	http.Redirect(w, r, "/work?status=active&created=true", http.StatusSeeOther)
}

func (s *Server) updateWorkItemStatus(w http.ResponseWriter, r *http.Request) {
	if err := s.store.UpdateWorkItemStatus(r.Context(), chi.URLParam(r, "workItemID"), r.FormValue("status")); err != nil {
		if errors.Is(err, ErrWorkItemNotFound) {
			httpx.WriteProblem(w, http.StatusNotFound, "work_item_not_found", "The work item was not found.")
			return
		}
		s.renderWorkQueue(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if requestWantsJSON(r) {
		payload, err := s.loadV2Ticket(r.Context(), chi.URLParam(r, "workItemID"))
		if err != nil {
			httpx.WriteProblem(w, http.StatusServiceUnavailable, "work_item_unavailable", "The updated ticket could not be loaded.")
			return
		}
		httpx.WriteJSON(w, http.StatusOK, payload)
		return
	}
	if r.FormValue("return_to") == "detail" {
		http.Redirect(w, r, "/work/"+chi.URLParam(r, "workItemID")+"?updated=true", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, workQueueReturnURL(r, true), http.StatusSeeOther)
}

func (s *Server) workItemPage(w http.ResponseWriter, r *http.Request) {
	if !requestWantsV2Page(r) {
		s.renderWorkItemPage(w, r, http.StatusOK, "")
		return
	}
	s.renderV2App(w, r, "Ticket")
}

func (s *Server) renderWorkItemPage(w http.ResponseWriter, r *http.Request, status int, formError string) {
	session, _ := sessionFromContext(r.Context())
	item, err := s.store.GetWorkItem(r.Context(), chi.URLParam(r, "workItemID"))
	if errors.Is(err, ErrWorkItemNotFound) {
		s.renderError(w, http.StatusNotFound, "The work item was not found.")
		return
	}
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "The work item could not be loaded.")
		return
	}
	subtasks, err := s.store.ListSubtasks(r.Context(), item.ID)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Ticket subtasks could not be loaded.")
		return
	}
	room, err := s.workItemBoardroom(r.Context(), item)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "The ticket's agent workspace could not be loaded.")
		return
	}
	personas, err := s.boardrooms.Personas(r.Context(), room.ID)
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Ticket agents could not be loaded.")
		return
	}
	var run boardroom.Run
	var messages []boardroom.Message
	var attachments []boardroom.DocumentAttachment
	var approvals []toolbroker.Approval
	selectedDocuments := map[string]bool{}
	if item.ConversationID != "" {
		conversationID, parseErr := domain.ParseConversationID(item.ConversationID)
		if parseErr == nil {
			conversation, conversationErr := s.boardrooms.GetConversation(r.Context(), conversationID)
			if conversationErr == nil {
				run, _ = s.boardrooms.GetRun(r.Context(), conversation.LatestRunID)
				messages, _, _ = s.boardrooms.MessagesSnapshot(r.Context(), conversationID, conversation.LatestRunID)
				attachments, _ = s.boardrooms.ConversationDocuments(r.Context(), conversationID)
				for _, attachment := range attachments {
					selectedDocuments[attachment.ID] = true
				}
				if run.ID.String() != "" {
					approvals, _ = s.approvals.PendingApprovalsForRun(r.Context(), run.ID)
				}
			}
		}
	}
	documents, err := s.documentOptions(r.Context(), selectedDocuments)
	if err != nil {
		documents = nil
	}
	notice := ""
	if r.URL.Query().Get("sent") == "true" {
		notice = "The selected agents are working on the ticket."
	} else if r.URL.Query().Get("updated") == "true" {
		notice = "Ticket status updated."
	}
	s.render(w, status, components.TicketDetailsPage(
		s.tenantName(r.Context()), s.userView(session.User), workItemViews([]WorkItem{item})[0], workItemViews(subtasks), personaViews(personas),
		messageViews(messages), documentAttachmentViews(attachments), documents, componentRun(run, 0), approvalViews(approvals), s.csrfToken(session), notice, formError,
	))
}

func (s *Server) workItemBoardroom(ctx context.Context, item WorkItem) (boardroom.Summary, error) {
	if item.BoardroomID != "" {
		if id, err := domain.ParseBoardroomID(item.BoardroomID); err == nil {
			return s.boardrooms.Get(ctx, id)
		}
	}
	rooms, err := s.boardrooms.List(ctx)
	if err != nil || len(rooms) == 0 {
		return boardroom.Summary{}, errors.New("no active boardroom")
	}
	return rooms[0], nil
}

func (s *Server) createWorkItemMessage(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, documentUploadLimit+(512<<10))
	if err := r.ParseMultipartForm(documentUploadLimit); err != nil {
		s.renderWorkItemPage(w, r, http.StatusBadRequest, "The message or uploaded document is too large.")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	if !auth.CheckCSRF(session.RawToken, r.FormValue("csrf_token"), s.config.SessionSecret) {
		httpx.WriteProblem(w, http.StatusForbidden, "invalid_csrf", "The form expired or could not be verified.")
		return
	}
	item, err := s.store.GetWorkItem(r.Context(), chi.URLParam(r, "workItemID"))
	if err != nil {
		s.renderError(w, http.StatusNotFound, "The work item was not found.")
		return
	}
	prompt := strings.TrimSpace(r.FormValue("prompt"))
	if prompt == "" || len(prompt) > 12000 {
		s.renderWorkItemPage(w, r, http.StatusBadRequest, "The message must contain between 1 and 12,000 characters.")
		return
	}
	room, err := s.workItemBoardroom(r.Context(), item)
	if err != nil {
		s.renderWorkItemPage(w, r, http.StatusBadRequest, "Choose a ticket with an active boardroom.")
		return
	}
	personas, err := s.boardrooms.Personas(r.Context(), room.ID)
	if err != nil {
		s.renderWorkItemPage(w, r, http.StatusInternalServerError, "Ticket agents could not be loaded.")
		return
	}
	selectedIDs, err := selectedWorkItemPersonas(r.Form["persona_id"], personas, room.MaxTurns)
	if err != nil {
		s.renderWorkItemPage(w, r, http.StatusBadRequest, err.Error())
		return
	}
	attachments, err := s.documentAttachmentsFromRequest(r)
	if err != nil {
		s.renderWorkItemPage(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if uploaded, ok, uploadErr := s.ticketUploadedDocument(r, session.User.ID); uploadErr != nil {
		s.renderWorkItemPage(w, r, http.StatusBadRequest, uploadErr.Error())
		return
	} else if ok {
		attachments = append(attachments, uploaded)
	}
	var run boardroom.Run
	if item.ConversationID == "" {
		run, err = s.boardrooms.CreateTargetedRunWithDocuments(r.Context(), room.ID, session.User.ID, fmt.Sprintf("Ticket #%04d: %s", item.Number, item.Title), prompt, item.ID, selectedIDs, attachments)
	} else {
		conversationID, parseErr := domain.ParseConversationID(item.ConversationID)
		if parseErr != nil {
			err = parseErr
		} else {
			run, err = s.boardrooms.CreateTargetedFollowUpRunWithDocuments(r.Context(), conversationID, session.User.ID, prompt, item.ID, selectedIDs, attachments)
		}
	}
	if errors.Is(err, boardroom.ErrConversationBusy) {
		s.renderWorkItemPage(w, r, http.StatusConflict, "Agents are already working in this ticket. Wait for the current run or resolve its approval.")
		return
	}
	if err != nil {
		s.renderWorkItemPage(w, r, http.StatusBadRequest, "The agent conversation could not be started: "+err.Error())
		return
	}
	if err := s.store.LinkWorkItemConversation(r.Context(), item.ID, room.ID.String(), run.ConversationID.String(), run.ID.String()); err != nil {
		_ = s.boardrooms.SetRunStatus(r.Context(), run.ID, domain.RunFailed, "The ticket conversation could not be linked.")
		s.renderWorkItemPage(w, r, http.StatusInternalServerError, "The ticket conversation could not be linked.")
		return
	}
	if err := s.dispatcher.Dispatch(r.Context(), run.ID); err != nil {
		_ = s.boardrooms.SetRunStatus(r.Context(), run.ID, domain.RunFailed, "The durable workflow could not be started.")
		s.renderWorkItemPage(w, r, http.StatusServiceUnavailable, "The selected agents could not be started.")
		return
	}
	if requestWantsJSON(r) {
		payload, err := s.loadV2Ticket(r.Context(), item.ID)
		if err != nil {
			httpx.WriteProblem(w, http.StatusServiceUnavailable, "work_item_unavailable", "The ticket was started, but its live state could not be loaded.")
			return
		}
		httpx.WriteJSON(w, http.StatusAccepted, payload)
		return
	}
	http.Redirect(w, r, "/work/"+item.ID+"?sent=true", http.StatusSeeOther)
}

func selectedWorkItemPersonas(requested []string, personas []boardroom.Persona, maximum int) ([]string, error) {
	if len(requested) == 0 {
		return nil, errors.New("Choose at least one agent.")
	}
	available := make(map[string]bool, len(personas))
	for _, persona := range personas {
		available[persona.ID.String()] = true
	}
	seen := map[string]bool{}
	result := make([]string, 0, len(requested))
	for _, id := range requested {
		id = strings.TrimSpace(id)
		if !available[id] {
			return nil, errors.New("Choose enabled agents from this boardroom.")
		}
		if !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	if len(result) > maximum {
		return nil, fmt.Errorf("Choose no more than %d agents for one message.", maximum)
	}
	return result, nil
}

func (s *Server) ticketUploadedDocument(r *http.Request, userID string) (boardroom.DocumentAttachment, bool, error) {
	file, header, err := r.FormFile("document")
	if errors.Is(err, http.ErrMissingFile) {
		return boardroom.DocumentAttachment{}, false, nil
	}
	if err != nil {
		return boardroom.DocumentAttachment{}, false, errors.New("The uploaded document could not be read.")
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, documentUploadLimit+1))
	if err != nil || len(content) == 0 || len(content) > documentUploadLimit {
		return boardroom.DocumentAttachment{}, false, errors.New("Upload a supported document no larger than 15 MB.")
	}
	extracted, mediaType, err := documentextract.Extract(header.Filename, content)
	if err != nil {
		return boardroom.DocumentAttachment{}, false, err
	}
	name := filepath.Base(header.Filename)
	document, err := s.documents.IngestText(r.Context(), name, mediaType, extracted, userID)
	if err != nil {
		return boardroom.DocumentAttachment{}, false, errors.New("The uploaded document could not be indexed.")
	}
	return boardroom.DocumentAttachment{ID: document.ID, Name: document.Name}, true, nil
}

func (s *Server) renderWorkQueue(w http.ResponseWriter, r *http.Request, status int, formError string) {
	session, _ := sessionFromContext(r.Context())
	filter := NormalizeWorkFilter(WorkFilter{
		Status: r.URL.Query().Get("status"), Kind: r.URL.Query().Get("kind"), Query: r.URL.Query().Get("q"),
	})
	items, summary, err := s.store.ListWorkItems(r.Context(), filter)
	if err != nil {
		s.logger.Error("load work queue", "error", err)
		s.renderError(w, http.StatusInternalServerError, "The work queue could not be loaded.")
		return
	}
	notice := ""
	if r.URL.Query().Get("created") == "true" {
		notice = "Work item added to the queue."
	} else if r.URL.Query().Get("updated") == "true" {
		notice = "Work item status updated."
	}
	s.render(w, status, components.WorkQueuePage(
		s.tenantName(r.Context()), s.userView(session.User), workItemViews(items), workSummaryView(summary),
		components.WorkFilterView{Status: filter.Status, Kind: filter.Kind, Query: filter.Query},
		s.csrfToken(session), notice, formError,
	))
}

func workQueueReturnURL(r *http.Request, updated bool) string {
	query := url.Values{}
	for formName, queryName := range map[string]string{
		"return_status": "status", "return_kind": "kind", "return_q": "q",
	} {
		if value := strings.TrimSpace(r.FormValue(formName)); value != "" {
			query.Set(queryName, value)
		}
	}
	if query.Get("status") == "" {
		query.Set("status", "active")
	}
	if updated {
		query.Set("updated", "true")
	}
	return "/work?" + query.Encode()
}

func (s *Server) createSchedule(w http.ResponseWriter, r *http.Request) {
	if !scheduleAdmin(r.Context()) {
		httpx.WriteProblem(w, http.StatusForbidden, "permission_denied", "Only a boardroom owner or administrator can create schedules.")
		return
	}
	if s.schedules == nil {
		s.renderSchedules(w, r, http.StatusServiceUnavailable, "Durable scheduling requires Temporal orchestration.")
		return
	}
	boardroomID, err := domain.ParseBoardroomID(r.FormValue("boardroom_id"))
	if err != nil {
		s.renderSchedules(w, r, http.StatusBadRequest, "Choose a valid boardroom.")
		return
	}
	var every time.Duration
	cronExpression := strings.TrimSpace(r.FormValue("cron_expression"))
	if cronExpression == "" {
		count, err := strconv.Atoi(r.FormValue("every_number"))
		if err != nil || count <= 0 {
			s.renderSchedules(w, r, http.StatusBadRequest, "Enter a valid schedule interval.")
			return
		}
		unit := time.Hour
		switch r.FormValue("every_unit") {
		case "hours":
		case "days":
			unit = 24 * time.Hour
		case "weeks":
			unit = 7 * 24 * time.Hour
		default:
			s.renderSchedules(w, r, http.StatusBadRequest, "Choose hours, days, or weeks for the interval.")
			return
		}
		every = time.Duration(count) * unit
	}
	_, err = s.schedules.Create(r.Context(), scheduling.CreateInput{
		BoardroomID: boardroomID, Name: r.FormValue("name"), Prompt: r.FormValue("prompt"),
		Every: every, CronExpression: cronExpression, TimeZone: r.FormValue("time_zone"),
	})
	if err != nil {
		if errors.Is(err, scheduling.ErrInvalidInput) {
			s.renderSchedules(w, r, http.StatusBadRequest, strings.TrimPrefix(err.Error(), scheduling.ErrInvalidInput.Error()+": "))
			return
		}
		s.logger.Error("create boardroom schedule", "error", err)
		s.renderSchedules(w, r, http.StatusServiceUnavailable, "The durable schedule could not be created. Please try again.")
		return
	}
	http.Redirect(w, r, "/schedules", http.StatusSeeOther)
}

func (s *Server) pauseSchedule(w http.ResponseWriter, r *http.Request) {
	if !scheduleAdmin(r.Context()) {
		httpx.WriteProblem(w, http.StatusForbidden, "permission_denied", "Only a boardroom owner or administrator can change schedules.")
		return
	}
	if s.schedules == nil {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "scheduling_unavailable", "Durable scheduling is not available.")
		return
	}
	id := chi.URLParam(r, "scheduleID")
	if _, err := uuid.Parse(id); err != nil {
		httpx.WriteProblem(w, http.StatusNotFound, "schedule_not_found", "Schedule was not found.")
		return
	}
	paused := r.FormValue("paused") == "true"
	if err := s.schedules.SetPaused(r.Context(), id, paused); err != nil {
		if errors.Is(err, scheduling.ErrNotFound) {
			httpx.WriteProblem(w, http.StatusNotFound, "schedule_not_found", "Schedule was not found.")
			return
		}
		s.logger.Error("pause boardroom schedule", "schedule_id", id, "error", err)
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "schedule_update_failed", "Schedule could not be updated.")
		return
	}
	http.Redirect(w, r, "/schedules", http.StatusSeeOther)
}

func (s *Server) triggerSchedule(w http.ResponseWriter, r *http.Request) {
	if !scheduleAdmin(r.Context()) {
		httpx.WriteProblem(w, http.StatusForbidden, "permission_denied", "Only a boardroom owner or administrator can run schedules.")
		return
	}
	id := chi.URLParam(r, "scheduleID")
	if s.schedules == nil {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "scheduling_unavailable", "Durable scheduling is not available.")
		return
	}
	if _, err := uuid.Parse(id); err != nil {
		httpx.WriteProblem(w, http.StatusNotFound, "schedule_not_found", "Schedule was not found.")
		return
	}
	if err := s.schedules.Trigger(r.Context(), id); err != nil {
		if errors.Is(err, scheduling.ErrNotFound) {
			httpx.WriteProblem(w, http.StatusNotFound, "schedule_not_found", "Schedule was not found.")
			return
		}
		s.logger.Error("trigger boardroom schedule", "schedule_id", id, "error", err)
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "schedule_trigger_failed", "Schedule could not be started.")
		return
	}
	http.Redirect(w, r, "/schedules", http.StatusSeeOther)
}

func (s *Server) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	if !scheduleAdmin(r.Context()) {
		httpx.WriteProblem(w, http.StatusForbidden, "permission_denied", "Only a boardroom owner or administrator can delete schedules.")
		return
	}
	id := chi.URLParam(r, "scheduleID")
	if s.schedules == nil {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "scheduling_unavailable", "Durable scheduling is not available.")
		return
	}
	if _, err := uuid.Parse(id); err != nil {
		httpx.WriteProblem(w, http.StatusNotFound, "schedule_not_found", "Schedule was not found.")
		return
	}
	if err := s.schedules.Delete(r.Context(), id); err != nil {
		if errors.Is(err, scheduling.ErrNotFound) {
			httpx.WriteProblem(w, http.StatusNotFound, "schedule_not_found", "Schedule was not found.")
			return
		}
		s.logger.Error("delete boardroom schedule", "schedule_id", id, "error", err)
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "schedule_delete_failed", "Schedule could not be deleted.")
		return
	}
	http.Redirect(w, r, "/schedules", http.StatusSeeOther)
}

func (s *Server) renderSchedules(w http.ResponseWriter, r *http.Request, status int, formError string) {
	session, _ := sessionFromContext(r.Context())
	rooms, err := s.boardrooms.List(r.Context())
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Boardrooms could not be loaded.")
		return
	}
	var items []scheduling.Schedule
	if s.schedules != nil {
		items, err = s.schedules.List(r.Context())
		if err != nil {
			s.renderError(w, http.StatusInternalServerError, "Schedules could not be loaded.")
			return
		}
	} else if formError == "" {
		formError = "Durable scheduling requires Temporal orchestration."
	}
	s.render(w, status, components.SchedulesPage(
		s.tenantName(r.Context()), s.userView(session.User), boardroomViews(rooms), scheduleViews(items), s.csrfToken(session), formError, s.tenantBusinessTemplate(r.Context()).String(),
	))
}

func scheduleAdmin(ctx context.Context) bool {
	session, ok := sessionFromContext(ctx)
	return ok && session.User.Role == "owner"
}

func (s *Server) runPage(w http.ResponseWriter, r *http.Request) {
	runID, err := domain.ParseRunID(chi.URLParam(r, "runID"))
	if err != nil {
		s.renderError(w, http.StatusNotFound, "Boardroom run was not found.")
		return
	}
	run, err := s.boardrooms.GetRun(r.Context(), runID)
	if errors.Is(err, boardroom.ErrRunNotFound) {
		s.renderError(w, http.StatusNotFound, "Boardroom run was not found.")
		return
	}
	if err != nil {
		s.renderError(w, http.StatusInternalServerError, "Boardroom run could not be loaded.")
		return
	}
	http.Redirect(w, r, "/conversations/"+run.ConversationID.String(), http.StatusSeeOther)
}

func (s *Server) runEvents(w http.ResponseWriter, r *http.Request) {
	runID, err := domain.ParseRunID(chi.URLParam(r, "runID"))
	if err != nil {
		httpx.WriteProblem(w, http.StatusNotFound, "run_not_found", "Boardroom run was not found.")
		return
	}
	if _, err := s.boardrooms.GetRun(r.Context(), runID); err != nil {
		httpx.WriteProblem(w, http.StatusNotFound, "run_not_found", "Boardroom run was not found.")
		return
	}
	after := parseEventCursor(r)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Connection", "keep-alive")
	_, _ = fmt.Fprint(w, "retry: 2000\n\n")
	controller := http.NewResponseController(w)
	if err := controller.Flush(); err != nil {
		return
	}

	poll := time.NewTicker(700 * time.Millisecond)
	heartbeat := time.NewTicker(15 * time.Second)
	defer poll.Stop()
	defer heartbeat.Stop()

	for {
		terminal, err := s.writePendingEvents(r.Context(), w, runID, &after)
		if err != nil {
			s.logger.Error("stream boardroom events", "run_id", runID.String(), "error", err)
			return
		}
		if terminal {
			run, runErr := s.boardrooms.GetRun(r.Context(), runID)
			session, hasSession := sessionFromContext(r.Context())
			if runErr == nil && hasSession {
				if run.Status == domain.RunAwaitingApproval {
					items, approvalErr := s.approvals.PendingApprovalsForRun(r.Context(), run.ID)
					if approvalErr != nil {
						s.logger.Warn("load streamed conversation approvals", "run_id", run.ID.String(), "error", approvalErr)
					}
					fragment, renderErr := renderString(components.ApprovalOffers(approvalViews(items), s.csrfToken(session)))
					if renderErr == nil {
						writeSSE(w, after, "finished", fragment)
					} else {
						writeSSE(w, after, "finished", "")
					}
					_ = controller.Flush()
					return
				}
				attachments, attachmentErr := s.boardrooms.ConversationDocuments(r.Context(), run.ConversationID)
				selected := make(map[string]bool, len(attachments))
				for _, attachment := range attachments {
					selected[attachment.ID] = true
				}
				documents, documentErr := s.documentOptions(r.Context(), selected)
				if attachmentErr != nil {
					s.logger.Warn("load follow-up attachments", "conversation_id", run.ConversationID.String(), "error", attachmentErr)
				}
				if documentErr != nil {
					s.logger.Warn("load follow-up document options", "conversation_id", run.ConversationID.String(), "error", documentErr)
					documents = nil
				}
				fragment, renderErr := renderString(components.FollowUpForm(run.ConversationID.String(), documents, s.csrfToken(session), string(run.Status)))
				if renderErr == nil {
					writeSSE(w, after, "finished", fragment)
				} else {
					writeSSE(w, after, "finished", "")
				}
			} else {
				writeSSE(w, after, "finished", "")
			}
			_ = controller.Flush()
			return
		}
		if err := controller.Flush(); err != nil {
			return
		}

		select {
		case <-r.Context().Done():
			return
		case <-poll.C:
		case <-heartbeat.C:
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
			if err := controller.Flush(); err != nil {
				return
			}
		}
	}
}

func (s *Server) writePendingEvents(ctx context.Context, w http.ResponseWriter, runID domain.RunID, after *int64) (bool, error) {
	events, err := s.boardrooms.EventsAfter(ctx, runID, *after, 100)
	if err != nil {
		return false, err
	}
	terminal := false
	for _, event := range events {
		*after = event.ID
		switch event.Type {
		case "message.completed":
			var payload struct {
				MessageID   string    `json:"message_id"`
				Sequence    int64     `json:"sequence"`
				PersonaName string    `json:"persona_name"`
				PersonaRole string    `json:"persona_role"`
				Role        string    `json:"role"`
				Body        string    `json:"body"`
				CreatedAt   time.Time `json:"created_at"`
			}
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return false, err
			}
			research, err := s.boardrooms.MessageResearch(ctx, runID, payload.MessageID)
			if err != nil {
				s.logger.Warn("load completed message research", "run_id", runID.String(), "message_id", payload.MessageID, "error", err)
				research = nil
			}
			fragment, err := renderString(components.MessageBubble(components.MessageView{
				ID: payload.MessageID, Sequence: payload.Sequence, PersonaName: payload.PersonaName,
				PersonaRole: payload.PersonaRole, Role: payload.Role, Body: payload.Body, CreatedAt: payload.CreatedAt,
				Research: researchActivityViews(research),
			}))
			if err != nil {
				return false, err
			}
			writeSSE(w, event.ID, "message", fragment)
		case "run.status":
			run, err := s.boardrooms.GetRun(ctx, runID)
			if err != nil {
				return false, err
			}
			fragment, err := renderString(components.StatusFragment(componentRun(run, event.ID)))
			if err != nil {
				return false, err
			}
			writeSSE(w, event.ID, "status", fragment)
			terminal = run.Status == domain.RunCompleted || run.Status == domain.RunFailed || run.Status == domain.RunCanceled || run.Status == domain.RunAwaitingApproval
		}
	}
	if !terminal {
		run, err := s.boardrooms.GetRun(ctx, runID)
		if err != nil {
			return false, err
		}
		terminal = run.Status == domain.RunCompleted || run.Status == domain.RunFailed || run.Status == domain.RunCanceled || run.Status == domain.RunAwaitingApproval
	}
	return terminal, nil
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, user User) error {
	raw, expiresAt, err := s.store.CreateSession(r.Context(), user.ID, r.UserAgent(), clientIP(r), sessionLifetime)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: raw, Path: "/", Expires: expiresAt,
		MaxAge: int(time.Until(expiresAt).Seconds()), HttpOnly: true, Secure: s.config.CookieSecure, SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1, Expires: time.Unix(1, 0),
		HttpOnly: true, Secure: s.config.CookieSecure, SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) csrfToken(session authenticatedSession) string {
	return auth.CSRFToken(session.RawToken, s.config.SessionSecret)
}

func (s *Server) render(w http.ResponseWriter, status int, component templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := component.Render(context.Background(), w); err != nil {
		s.logger.Error("render tenant page", "error", err)
	}
}

func (s *Server) renderError(w http.ResponseWriter, status int, message string) {
	httpx.WriteProblem(w, status, "request_failed", message)
}

func sessionFromContext(ctx context.Context) (authenticatedSession, bool) {
	session, ok := ctx.Value(authContextKey).(authenticatedSession)
	return session, ok
}

func normalizedHost(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if host, _, err := net.SplitHostPort(value); err == nil {
		return host
	}
	return value
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return ""
}

func parseEventCursor(r *http.Request) int64 {
	value := r.Header.Get("Last-Event-ID")
	if value == "" {
		value = r.URL.Query().Get("after")
	}
	parsed, _ := strconv.ParseInt(value, 10, 64)
	if parsed < 0 {
		return 0
	}
	return parsed
}

func writeSSE(w http.ResponseWriter, id int64, eventName, data string) {
	_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\n", id, eventName)
	for _, line := range strings.Split(strings.ReplaceAll(data, "\r\n", "\n"), "\n") {
		_, _ = fmt.Fprintf(w, "data: %s\n", line)
	}
	_, _ = fmt.Fprint(w, "\n")
}

func renderString(component templ.Component) (string, error) {
	var buffer bytes.Buffer
	if err := component.Render(context.Background(), &buffer); err != nil {
		return "", err
	}
	return buffer.String(), nil
}

func (s *Server) userView(user User) components.UserView {
	return components.UserView{DisplayName: user.DisplayName, Email: user.Email, Role: user.Role, Development: s.config.Development}
}

func (s *Server) tenantName(ctx context.Context) string {
	name, err := s.store.TenantDisplayName(ctx, s.config.TenantID)
	if err != nil || strings.TrimSpace(name) == "" {
		return s.config.TenantName
	}
	return name
}

func onboardingView(state Onboarding, template string) components.OnboardingView {
	result := components.OnboardingView{
		Template: template,
		Status:   state.Status, CurrentStep: state.CurrentStep, Priorities: state.Priorities,
		Business: components.BusinessProfileView{
			Template: state.Business.Template, Variant: state.Business.Variant, Stage: state.Business.Stage,
			BusinessName: state.Business.BusinessName, WebsiteURL: state.Business.WebsiteURL, Trade: state.Business.Trade, Services: state.Business.Services,
			ServiceArea: state.Business.ServiceArea, TimeZone: state.Business.TimeZone, TeamSize: state.Business.TeamSize,
			CustomerMix: state.Business.CustomerMix, WorkingHours: state.Business.WorkingHours,
			EmergencyService: state.Business.EmergencyService, CurrentSystems: state.Business.CurrentSystems,
		},
		Operations: components.OperatingPlaybookView{
			LeadIntake: state.Operations.LeadIntake, Scheduling: state.Operations.Scheduling,
			EstimateToJob: state.Operations.EstimateToJob, JobToInvoice: state.Operations.JobToInvoice,
			Payments: state.Operations.Payments, VendorBills: state.Operations.VendorBills,
			BiggestBottleneck: state.Operations.BiggestBottleneck, ImportantExceptions: state.Operations.ImportantExceptions,
		},
		BoardroomName: state.Blueprint.Name, BoardroomDescription: state.Blueprint.Description,
		Permissions: components.PermissionPlanView{
			ReadBusinessRecords: state.Permissions.ReadBusinessRecords, ResearchPublicWeb: state.Permissions.ResearchPublicWeb,
			CommentOnDocuments: state.Permissions.CommentOnDocuments, PrepareInvoiceDrafts: state.Permissions.PrepareInvoiceDrafts,
			DraftCustomerEmail: state.Permissions.DraftCustomerEmail, ProposeScheduleEdits: state.Permissions.ProposeScheduleEdits,
			ReadEmailInbox: state.Permissions.ReadEmailInbox, SendEmail: state.Permissions.SendEmail,
			ProposePayments: state.Permissions.ProposePayments,
		},
	}
	for _, persona := range state.Blueprint.Personas {
		result.Personas = append(result.Personas, components.PersonaBlueprintView{
			Key: persona.Key, Group: persona.Group, Name: persona.Name, Role: persona.Role, Mission: persona.Mission,
			Enabled: persona.Enabled, Capabilities: persona.Capabilities,
		})
	}
	return result
}

func documentViews(documents []rag.Document) []components.DocumentView {
	result := make([]components.DocumentView, 0, len(documents))
	for _, document := range documents {
		result = append(result, documentView(document))
	}
	return result
}

func documentView(document rag.Document) components.DocumentView {
	return components.DocumentView{
		ID: document.ID, Name: document.Name, MediaType: document.MediaType, Status: document.Status,
		ChunkCount: document.ChunkCount, CharacterCount: document.CharacterCount,
		UploadedBy: document.UploadedBy, Revision: document.Revision, CreatedAt: document.CreatedAt, UpdatedAt: document.UpdatedAt,
	}
}

func documentDetailView(document rag.DocumentDetail) components.DocumentDetailView {
	return components.DocumentDetailView{DocumentView: documentView(document.Document), Content: document.Content}
}

func cleanFormValues(values []string, maximum, maxLength int) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > maxLength || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
		if len(result) == maximum {
			break
		}
	}
	return result
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func boardroomView(room boardroom.Summary) components.BoardroomCardView {
	return components.BoardroomCardView{
		ID: room.ID.String(), Name: room.Name, Description: room.Description,
		PersonaCount: room.PersonaCount, Status: room.Status,
	}
}

func boardroomViews(rooms []boardroom.Summary) []components.BoardroomCardView {
	result := make([]components.BoardroomCardView, 0, len(rooms))
	for _, room := range rooms {
		result = append(result, boardroomView(room))
	}
	return result
}

func conversationView(item boardroom.Conversation) components.ConversationView {
	return components.ConversationView{
		ID: item.ID.String(), Title: item.Title, Source: item.Source, LatestStatus: string(item.LatestStatus),
		LatestPrompt: item.LatestPrompt, MessageCount: item.MessageCount, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
	}
}

func conversationViews(items []boardroom.Conversation) []components.ConversationView {
	result := make([]components.ConversationView, 0, len(items))
	for _, item := range items {
		result = append(result, conversationView(item))
	}
	return result
}

func workItemViews(items []WorkItem) []components.WorkItemView {
	result := make([]components.WorkItemView, 0, len(items))
	now := time.Now().UTC()
	for _, item := range items {
		dueLabel := ""
		overdue := false
		if item.DueAt != nil {
			dueLabel = item.DueAt.Format("Jan 2, 2006")
			overdue = item.Status != "done" && item.Status != "canceled" && item.DueAt.Before(now)
		}
		result = append(result, components.WorkItemView{
			ID: item.ID, Number: item.Number, Kind: item.Kind, Title: item.Title, Description: item.Description,
			Status: item.Status, Priority: item.Priority, Source: item.Source,
			CreatedByName: item.CreatedByName, AssignedToName: item.AssignedToName, AssignedToType: item.AssignedToType,
			Responsibility: item.Responsibility,
			ParentID:       item.ParentID, ParentNumber: item.ParentNumber,
			DueLabel: dueLabel, IsOverdue: overdue, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		})
	}
	return result
}

func workSummaryView(summary WorkSummary) components.WorkSummaryView {
	return components.WorkSummaryView{
		Active: summary.Active, InProgress: summary.InProgress, Waiting: summary.Waiting, Urgent: summary.Urgent, Done: summary.Done,
	}
}

func scheduleViews(items []scheduling.Schedule) []components.ScheduleView {
	result := make([]components.ScheduleView, 0, len(items))
	for _, item := range items {
		timing := "Cron: " + item.Spec.CronExpression
		if item.Spec.Kind == "interval" {
			duration := time.Duration(item.Spec.EverySeconds) * time.Second
			switch {
			case duration%(7*24*time.Hour) == 0:
				timing = fmt.Sprintf("Every %d weeks", duration/(7*24*time.Hour))
			case duration%(24*time.Hour) == 0:
				timing = fmt.Sprintf("Every %d days", duration/(24*time.Hour))
			default:
				timing = fmt.Sprintf("Every %d hours", duration/time.Hour)
			}
		}
		result = append(result, components.ScheduleView{
			ID: item.ID, Name: item.Name, BoardroomName: item.BoardroomName, Prompt: item.Prompt,
			Timing: timing, TimeZone: item.Spec.TimeZone, Paused: item.Paused, State: item.State, LastError: item.LastError,
		})
	}
	return result
}

func personaViews(personas []boardroom.Persona) []components.PersonaView {
	result := make([]components.PersonaView, 0, len(personas))
	for _, persona := range personas {
		view := components.PersonaView{ID: persona.ID.String(), Name: persona.Name, Role: persona.Role}
		for _, grant := range persona.Grants {
			view.Tools = append(view.Tools, string(grant.Capability))
			if grant.Capability == domain.CapabilityTicketCreate {
				view.CanCreateSubtasks = true
			}
		}
		result = append(result, view)
	}
	return result
}

func componentRun(run boardroom.Run, eventCursor int64) components.RunView {
	return components.RunView{
		ID: run.ID.String(), Status: string(run.Status), Prompt: run.Prompt, TurnCount: run.TurnCount,
		EventCursor: eventCursor, CreatedAt: run.CreatedAt, Error: run.Error,
	}
}

func messageViews(messages []boardroom.Message) []components.MessageView {
	result := make([]components.MessageView, 0, len(messages))
	for _, message := range messages {
		result = append(result, components.MessageView{
			ID: message.ID, PersonaName: message.PersonaName, PersonaRole: message.PersonaRole,
			Role: string(message.Role), Body: message.Body, Sequence: message.Sequence, CreatedAt: message.CreatedAt,
			Research: researchActivityViews(message.Research),
		})
	}
	return result
}

func researchActivityViews(activities []boardroom.ResearchActivity) []components.ResearchActivityView {
	result := make([]components.ResearchActivityView, 0, len(activities))
	for _, activity := range activities {
		view := components.ResearchActivityView{
			Tool: activity.Tool, Query: activity.Query, URL: activity.URL, Status: activity.Status,
			Results: make([]components.ResearchResultView, 0, len(activity.Results)),
		}
		for _, item := range activity.Results {
			view.Results = append(view.Results, components.ResearchResultView{Title: item.Title, URL: item.URL, Excerpt: item.Excerpt})
		}
		result = append(result, view)
	}
	return result
}

func documentAttachmentViews(documents []boardroom.DocumentAttachment) []components.DocumentOptionView {
	result := make([]components.DocumentOptionView, 0, len(documents))
	for _, document := range documents {
		result = append(result, components.DocumentOptionView{ID: document.ID, Name: document.Name, Selected: true})
	}
	return result
}

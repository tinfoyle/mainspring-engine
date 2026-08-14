package tenant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tinfoyle/mainspring-engine/internal/boardroom"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
	"github.com/tinfoyle/mainspring-engine/internal/finance"
	"github.com/tinfoyle/mainspring-engine/internal/httpx"
	"github.com/tinfoyle/mainspring-engine/internal/rag"
	toolbroker "github.com/tinfoyle/mainspring-engine/internal/tools"
	"github.com/tinfoyle/mainspring-engine/web/components"
)

type v2WorkQueuePayload struct {
	Items   []components.WorkItemView  `json:"items"`
	Summary components.WorkSummaryView `json:"summary"`
	Filter  components.WorkFilterView  `json:"filter"`
}

type v2TicketPayload struct {
	Item        components.WorkItemView         `json:"item"`
	Subtasks    []components.WorkItemView       `json:"subtasks"`
	Personas    []components.PersonaView        `json:"personas"`
	Messages    []components.MessageView        `json:"messages"`
	Attachments []components.DocumentOptionView `json:"attachments"`
	Documents   []components.DocumentOptionView `json:"documents"`
	Run         components.RunView              `json:"run"`
	Approvals   []components.ApprovalView       `json:"approvals"`
}

type v2YourTurnPayload struct {
	Tab         string                             `json:"tab"`
	Counts      components.YourTurnCountsView      `json:"counts"`
	Coordinator components.InputCoordinatorView    `json:"coordinator"`
	Inputs      []components.HumanInputRequestView `json:"inputs"`
	Approvals   []components.ApprovalView          `json:"approvals"`
	Documents   []components.DocumentOptionView    `json:"documents"`
}

type v2DocumentsPayload struct {
	Documents []components.DocumentView `json:"documents"`
}

type v2DocumentPayload struct {
	Document components.DocumentDetailView `json:"document"`
}

type v2BoardroomPayload struct {
	Boardroom     components.BoardroomCardView    `json:"boardroom"`
	Personas      []components.PersonaView        `json:"personas"`
	Conversations []components.ConversationView   `json:"conversations"`
	Documents     []components.DocumentOptionView `json:"documents"`
	Template      string                          `json:"template"`
}

type v2ConversationPayload struct {
	Boardroom    components.BoardroomCardView    `json:"boardroom"`
	Personas     []components.PersonaView        `json:"personas"`
	Conversation components.ConversationView     `json:"conversation"`
	Run          components.RunView              `json:"run"`
	Messages     []components.MessageView        `json:"messages"`
	Attachments  []components.DocumentOptionView `json:"attachments"`
	Documents    []components.DocumentOptionView `json:"documents"`
	Approvals    []components.ApprovalView       `json:"approvals"`
}

type v2HomePayload struct {
	Boardrooms []components.BoardroomCardView `json:"boardrooms"`
	Work       components.WorkSummaryView     `json:"work"`
	YourTurn   components.YourTurnCountsView  `json:"yourTurn"`
	Documents  int                            `json:"documents"`
}

type v2AgentsPayload struct {
	Agents []components.AgentCardView `json:"agents"`
}

type v2FinancePayload struct {
	Ledgers  []components.FinanceLedgerView  `json:"ledgers"`
	Selected *components.FinanceLedgerView   `json:"selected"`
	Accounts []components.FinanceAccountView `json:"accounts"`
	Entries  []components.FinanceEntryView   `json:"entries"`
	Today    string                          `json:"today"`
}

type v2FinanceEntryPayload struct {
	Ledger   components.FinanceLedgerView    `json:"ledger"`
	Entry    components.FinanceEntryView     `json:"entry"`
	Accounts []components.FinanceAccountView `json:"accounts"`
}

type v2BaselinePayload struct {
	Baseline components.BaselineView `json:"baseline"`
}

type v2InboxPayload struct {
	Items  []components.AnnouncementView `json:"items"`
	Unread int                           `json:"unread"`
}

func (s *Server) renderV2App(w http.ResponseWriter, r *http.Request, title string) {
	session, _ := sessionFromContext(r.Context())
	s.render(w, http.StatusOK, components.V2AppPage(title, s.tenantName(r.Context()), s.userView(session.User), s.csrfToken(session)))
}

func (s *Server) v2WorkQueueAPI(w http.ResponseWriter, r *http.Request) {
	filter := NormalizeWorkFilter(WorkFilter{
		Status: r.URL.Query().Get("status"), Kind: r.URL.Query().Get("kind"), Query: r.URL.Query().Get("q"),
	})
	items, summary, err := s.store.ListWorkItems(r.Context(), filter)
	if err != nil {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "work_queue_unavailable", "The work queue could not be loaded.")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, v2WorkQueuePayload{
		Items: workItemViews(items), Summary: workSummaryView(summary),
		Filter: components.WorkFilterView{Status: filter.Status, Kind: filter.Kind, Query: filter.Query},
	})
}

func (s *Server) v2WorkItemAPI(w http.ResponseWriter, r *http.Request) {
	payload, err := s.loadV2Ticket(r.Context(), chi.URLParam(r, "workItemID"))
	if errors.Is(err, ErrWorkItemNotFound) {
		httpx.WriteProblem(w, http.StatusNotFound, "work_item_not_found", "The work item was not found.")
		return
	}
	if err != nil {
		s.logger.Error("load v2 ticket", "error", err)
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "work_item_unavailable", "The ticket workspace could not be loaded.")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, payload)
}

func (s *Server) v2YourTurnAPI(w http.ResponseWriter, r *http.Request) {
	payload, err := s.loadV2YourTurn(r.Context(), r.URL.Query().Get("tab"), r.URL.Query().Get("history") == "1")
	if err != nil {
		s.logger.Error("load v2 your turn", "error", err)
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "your_turn_unavailable", "Your turn could not be loaded.")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, payload)
}

func (s *Server) v2DocumentsAPI(w http.ResponseWriter, r *http.Request) {
	documents, err := s.documents.ListDocuments(r.Context())
	if err != nil {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "documents_unavailable", "The document library could not be loaded.")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, v2DocumentsPayload{Documents: documentViews(documents)})
}

func (s *Server) v2HomeAPI(w http.ResponseWriter, r *http.Request) {
	rooms, err := s.boardrooms.List(r.Context())
	if err != nil {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "home_unavailable", "The workspace could not be loaded.")
		return
	}
	_, summary, err := s.store.ListWorkItems(r.Context(), NormalizeWorkFilter(WorkFilter{Status: "all"}))
	if err != nil {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "home_unavailable", "The workspace could not be loaded.")
		return
	}
	inputs, reviews, approvals, err := s.approvals.YourTurnCounts(r.Context())
	if err != nil {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "home_unavailable", "The workspace could not be loaded.")
		return
	}
	documents, err := s.documents.ListDocuments(r.Context())
	if err != nil {
		documents = nil
	}
	httpx.WriteJSON(w, http.StatusOK, v2HomePayload{
		Boardrooms: boardroomViews(rooms), Work: workSummaryView(summary),
		YourTurn: components.YourTurnCountsView{Inputs: inputs, Reviews: reviews, Approvals: approvals}, Documents: len(documents),
	})
}

func (s *Server) v2AgentsAPI(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAgentAdministrator(w, r); !ok {
		return
	}
	agents, err := s.boardrooms.ListAgents(r.Context())
	if err != nil {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "agents_unavailable", "Agents are temporarily unavailable.")
		return
	}
	rooms, err := s.boardrooms.List(r.Context())
	if err != nil {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "agents_unavailable", "Boardrooms are temporarily unavailable.")
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
	httpx.WriteJSON(w, http.StatusOK, v2AgentsPayload{Agents: views})
}

func (s *Server) loadV2Finance(ctx context.Context, selectedID string) (v2FinancePayload, error) {
	ledgers, err := s.finance.ListLedgers(ctx)
	if err != nil {
		return v2FinancePayload{}, err
	}
	payload := v2FinancePayload{Ledgers: []components.FinanceLedgerView{}, Accounts: []components.FinanceAccountView{}, Entries: []components.FinanceEntryView{}, Today: s.financeToday(ctx)}
	for _, ledger := range ledgers {
		payload.Ledgers = append(payload.Ledgers, financeLedgerView(ledger))
	}
	if selectedID == "" && len(ledgers) > 0 {
		selectedID = ledgers[0].ID
	}
	if selectedID == "" {
		return payload, nil
	}
	ledger, err := s.finance.GetLedger(ctx, selectedID)
	if err != nil {
		return v2FinancePayload{}, err
	}
	selected := financeLedgerView(ledger)
	for _, summary := range ledgers {
		if summary.ID == ledger.ID {
			selected = financeLedgerView(summary)
			break
		}
	}
	payload.Selected = &selected
	accounts, err := s.finance.Accounts(ctx, ledger.ID)
	if err != nil {
		return v2FinancePayload{}, err
	}
	payload.Accounts = financeAccountViews(accounts, ledger.Currency)
	entries, err := s.finance.ListEntries(ctx, ledger.ID, 50)
	if err != nil {
		return v2FinancePayload{}, err
	}
	for _, entry := range entries {
		payload.Entries = append(payload.Entries, financeEntryView(entry, ledger.Currency))
	}
	return payload, nil
}

func (s *Server) v2FinanceAPI(w http.ResponseWriter, r *http.Request) {
	payload, err := s.loadV2Finance(r.Context(), r.URL.Query().Get("ledger"))
	if err != nil {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "finance_unavailable", "Financial ledgers could not be loaded.")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, payload)
}

func (s *Server) v2CreateFinanceLedger(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	ledger, err := s.finance.CreateLedger(r.Context(), finance.CreateLedgerInput{Name: r.FormValue("name"), Code: r.FormValue("code"), Description: r.FormValue("description"), Currency: r.FormValue("currency"), CreateStandardAccounts: r.FormValue("standard_accounts") == "yes"}, financeUserActor(session.User))
	if err != nil {
		httpx.WriteProblem(w, http.StatusBadRequest, "ledger_not_created", err.Error())
		return
	}
	payload, err := s.loadV2Finance(r.Context(), ledger.ID)
	if err != nil {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "finance_unavailable", "The ledger was created but could not be loaded.")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, payload)
}

func (s *Server) v2CreateFinanceAccount(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	ledgerID := chi.URLParam(r, "ledgerID")
	_, err := s.finance.CreateAccount(r.Context(), finance.CreateAccountInput{LedgerID: ledgerID, ParentAccountID: r.FormValue("parent_account_id"), Code: r.FormValue("code"), Name: r.FormValue("name"), Description: r.FormValue("description"), Type: r.FormValue("account_type"), AllowPosting: r.FormValue("allow_posting") == "yes"}, financeUserActor(session.User))
	if err != nil {
		httpx.WriteProblem(w, http.StatusBadRequest, "account_not_created", err.Error())
		return
	}
	payload, err := s.loadV2Finance(r.Context(), ledgerID)
	if err != nil {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "finance_unavailable", "The account was created but the ledger could not be loaded.")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, payload)
}

func (s *Server) v2CreateFinanceEntry(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	ledgerID := chi.URLParam(r, "ledgerID")
	lines, err := parseFinanceLines(r)
	if err != nil {
		httpx.WriteProblem(w, http.StatusBadRequest, "invalid_entry", err.Error())
		return
	}
	date, err := time.Parse("2006-01-02", r.FormValue("entry_date"))
	if err != nil {
		httpx.WriteProblem(w, http.StatusBadRequest, "invalid_entry_date", "Enter a valid entry date.")
		return
	}
	entry, err := s.finance.CreateEntry(r.Context(), finance.CreateEntryInput{LedgerID: ledgerID, Description: r.FormValue("description"), Reference: r.FormValue("reference"), Source: "manual", EntryDate: date, WorkItemID: r.FormValue("work_item_id"), Lines: lines}, financeUserActor(session.User))
	if err != nil {
		httpx.WriteProblem(w, http.StatusBadRequest, "entry_not_created", err.Error())
		return
	}
	payload, err := s.loadV2FinanceEntry(r.Context(), entry.ID)
	if err != nil {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "entry_unavailable", "The draft was created but could not be loaded.")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, payload)
}

func (s *Server) loadV2FinanceEntry(ctx context.Context, id string) (v2FinanceEntryPayload, error) {
	entry, err := s.finance.GetEntry(ctx, id)
	if err != nil {
		return v2FinanceEntryPayload{}, err
	}
	ledger, err := s.finance.GetLedger(ctx, entry.LedgerID)
	if err != nil {
		return v2FinanceEntryPayload{}, err
	}
	accounts, err := s.finance.Accounts(ctx, entry.LedgerID)
	if err != nil {
		return v2FinanceEntryPayload{}, err
	}
	return v2FinanceEntryPayload{Ledger: financeLedgerView(ledger), Entry: financeEntryView(entry, ledger.Currency), Accounts: financeAccountViews(accounts, ledger.Currency)}, nil
}

func (s *Server) v2FinanceEntryAPI(w http.ResponseWriter, r *http.Request) {
	payload, err := s.loadV2FinanceEntry(r.Context(), chi.URLParam(r, "entryID"))
	if err != nil {
		httpx.WriteProblem(w, http.StatusNotFound, "entry_not_found", "The journal entry was not found.")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, payload)
}

func (s *Server) v2PostFinanceEntry(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	entry, err := s.finance.PostEntry(r.Context(), chi.URLParam(r, "entryID"), financeUserActor(session.User))
	if err != nil {
		httpx.WriteProblem(w, http.StatusBadRequest, "entry_not_posted", err.Error())
		return
	}
	payload, err := s.loadV2Finance(r.Context(), entry.LedgerID)
	if err != nil {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "finance_unavailable", "The entry was posted but the ledger could not be loaded.")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, payload)
}

func (s *Server) v2VoidFinanceEntry(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	entry, err := s.finance.VoidEntry(r.Context(), chi.URLParam(r, "entryID"), financeUserActor(session.User))
	if err != nil {
		httpx.WriteProblem(w, http.StatusBadRequest, "entry_not_voided", err.Error())
		return
	}
	payload, err := s.loadV2Finance(r.Context(), entry.LedgerID)
	if err != nil {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "finance_unavailable", "The entry was reversed but the ledger could not be loaded.")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, payload)
}

func (s *Server) loadV2Baseline(ctx context.Context) (v2BaselinePayload, error) {
	assessment, err := s.store.EnsureBaseline(ctx, s.config.TenantID)
	if err != nil {
		return v2BaselinePayload{}, err
	}
	if assessment.Phase == BaselinePhaseSourceAccess {
		if err := s.store.AdvanceBaseline(ctx, s.config.TenantID, BaselinePhaseInventory); err != nil {
			return v2BaselinePayload{}, err
		}
		s.inventoryBaselineSources(ctx)
		assessment, err = s.store.GetBaseline(ctx, s.config.TenantID)
		if err != nil {
			return v2BaselinePayload{}, err
		}
	}
	if assessment.Phase == BaselinePhaseInventory {
		rescoped, scopeErr := s.store.ReconcileBaselineEvidenceScope(ctx, s.config.TenantID)
		if scopeErr != nil {
			return v2BaselinePayload{}, scopeErr
		}
		if rescoped {
			s.inventoryBaselineSources(ctx)
			assessment, err = s.store.GetBaseline(ctx, s.config.TenantID)
			if err != nil {
				return v2BaselinePayload{}, err
			}
		}
	}
	documents, err := s.documentOptions(ctx, nil)
	if err != nil {
		documents = []components.DocumentOptionView{}
	}
	return v2BaselinePayload{Baseline: baselineView(assessment, documents)}, nil
}

func (s *Server) writeV2Baseline(w http.ResponseWriter, r *http.Request) {
	payload, err := s.loadV2Baseline(r.Context())
	if err != nil {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "baseline_unavailable", "The business baseline could not be loaded.")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, payload)
}

func (s *Server) v2BaselineAPI(w http.ResponseWriter, r *http.Request) { s.writeV2Baseline(w, r) }

func (s *Server) v2AnswerBaselineInterview(w http.ResponseWriter, r *http.Request) {
	if err := s.store.AnswerBaselineQuestion(r.Context(), s.config.TenantID, r.FormValue("answer")); err != nil {
		httpx.WriteProblem(w, http.StatusBadRequest, "invalid_answer", err.Error())
		return
	}
	assessment, err := s.store.GetBaseline(r.Context(), s.config.TenantID)
	if err == nil && assessment.Phase == BaselinePhaseInventory {
		s.inventoryBaselineSources(r.Context())
	}
	s.writeV2Baseline(w, r)
}

func (s *Server) v2AnswerBaselineEvidence(w http.ResponseWriter, r *http.Request) {
	if err := s.store.AnswerEvidenceInterview(r.Context(), s.config.TenantID, chi.URLParam(r, "requirementID"), r.FormValue("answer"), r.FormValue("choice")); err != nil {
		httpx.WriteProblem(w, http.StatusBadRequest, "invalid_evidence_answer", err.Error())
		return
	}
	s.writeV2Baseline(w, r)
}

func (s *Server) v2LinkBaselineDocument(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if err := s.store.LinkDocumentEvidence(r.Context(), s.config.TenantID, chi.URLParam(r, "requirementID"), r.FormValue("document_id"), session.User.ID); err != nil {
		httpx.WriteProblem(w, http.StatusBadRequest, "document_not_linked", err.Error())
		return
	}
	s.writeV2Baseline(w, r)
}

func (s *Server) v2ResolveBaselineEvidence(w http.ResponseWriter, r *http.Request) {
	var renewal *time.Time
	if raw := strings.TrimSpace(r.FormValue("renewal_due")); raw != "" {
		parsed, err := time.Parse("2006-01-02", raw)
		if err != nil {
			httpx.WriteProblem(w, http.StatusBadRequest, "invalid_renewal", "Renewal date must use YYYY-MM-DD.")
			return
		}
		renewal = &parsed
	}
	if err := s.store.SetEvidenceDisposition(r.Context(), s.config.TenantID, chi.URLParam(r, "requirementID"), r.FormValue("disposition"), r.FormValue("responsibility"), renewal); err != nil {
		httpx.WriteProblem(w, http.StatusBadRequest, "evidence_not_updated", err.Error())
		return
	}
	s.writeV2Baseline(w, r)
}

func (s *Server) v2AdvanceBaseline(w http.ResponseWriter, r *http.Request) {
	target := strings.TrimSpace(r.FormValue("phase"))
	if err := s.store.AdvanceBaseline(r.Context(), s.config.TenantID, target); err != nil {
		httpx.WriteProblem(w, http.StatusConflict, "baseline_not_advanced", err.Error())
		return
	}
	if target == BaselinePhaseInventory {
		s.inventoryBaselineSources(r.Context())
	}
	s.writeV2Baseline(w, r)
}

func (s *Server) v2CreateBaselinePlan(w http.ResponseWriter, r *http.Request) {
	if r.FormValue("approve") != "true" {
		httpx.WriteProblem(w, http.StatusBadRequest, "approval_required", "Confirm approval before creating the baseline plan.")
		return
	}
	session, _ := sessionFromContext(r.Context())
	state, err := s.store.GetOnboarding(r.Context(), s.config.TenantID)
	if err != nil {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "onboarding_unavailable", "The onboarding state could not be loaded.")
		return
	}
	if state.Status != OnboardingCompleted {
		state, err = s.store.ApplyBaselineFactsToOnboarding(r.Context(), s.config.TenantID, state)
		if err == nil {
			err = s.store.CompleteOnboarding(r.Context(), s.config.TenantID, state)
		}
		if err != nil {
			httpx.WriteProblem(w, http.StatusServiceUnavailable, "onboarding_not_completed", "The boardroom could not be prepared for the baseline plan.")
			return
		}
	}
	if _, _, err := s.store.CreateBaselinePlan(r.Context(), s.config.TenantID, session.User.ID); err != nil {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "plan_not_created", "The approved baseline plan could not be created.")
		return
	}
	s.writeV2Baseline(w, r)
}

func (s *Server) v2ReassessBaseline(w http.ResponseWriter, r *http.Request) {
	if err := s.store.StartBaselineReassessment(r.Context(), s.config.TenantID); err != nil {
		httpx.WriteProblem(w, http.StatusConflict, "reassessment_not_started", err.Error())
		return
	}
	s.writeV2Baseline(w, r)
}

func (s *Server) loadV2Inbox(ctx context.Context, userID string) (v2InboxPayload, error) {
	items, err := s.store.ListAnnouncements(ctx, userID)
	if err != nil {
		return v2InboxPayload{}, err
	}
	payload := v2InboxPayload{Items: []components.AnnouncementView{}}
	for _, item := range items {
		view := components.AnnouncementView{ID: item.ID, Title: item.Title, Body: item.Body, Category: strings.ReplaceAll(item.Category, "_", " "), PublishedAt: item.PublishedAt.Local().Format("Jan 2, 2006 3:04 PM"), Read: item.ReadAt != nil}
		if !view.Read {
			payload.Unread++
		}
		payload.Items = append(payload.Items, view)
	}
	return payload, nil
}

func (s *Server) v2InboxAPI(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	payload, err := s.loadV2Inbox(r.Context(), session.User.ID)
	if err != nil {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "inbox_unavailable", "The service inbox could not be loaded.")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, payload)
}

func (s *Server) v2ReadAnnouncement(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if err := s.store.MarkAnnouncementRead(r.Context(), session.User.ID, chi.URLParam(r, "announcementID")); err != nil {
		httpx.WriteProblem(w, http.StatusBadRequest, "announcement_not_read", "The announcement could not be marked read.")
		return
	}
	payload, err := s.loadV2Inbox(r.Context(), session.User.ID)
	if err != nil {
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "inbox_unavailable", "The service inbox could not be loaded.")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, payload)
}

func (s *Server) v2DocumentAPI(w http.ResponseWriter, r *http.Request) {
	document, err := s.documents.GetDocument(r.Context(), chi.URLParam(r, "documentID"))
	if err != nil {
		if errors.Is(err, rag.ErrDocumentNotFound) {
			httpx.WriteProblem(w, http.StatusNotFound, "document_not_found", "The document was not found.")
			return
		}
		httpx.WriteProblem(w, http.StatusServiceUnavailable, "document_unavailable", "The document could not be loaded.")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, v2DocumentPayload{Document: documentDetailView(document)})
}

func (s *Server) v2BoardroomAPI(w http.ResponseWriter, r *http.Request) {
	payload, err := s.loadV2Boardroom(r.Context(), chi.URLParam(r, "boardroomID"))
	if err != nil {
		httpx.WriteProblem(w, http.StatusNotFound, "boardroom_unavailable", "The boardroom could not be loaded.")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, payload)
}

func (s *Server) loadV2Boardroom(ctx context.Context, rawID string) (v2BoardroomPayload, error) {
	roomID, err := domain.ParseBoardroomID(rawID)
	if err != nil {
		return v2BoardroomPayload{}, err
	}
	room, err := s.boardrooms.Get(ctx, roomID)
	if err != nil {
		return v2BoardroomPayload{}, err
	}
	personas, err := s.boardrooms.Personas(ctx, roomID)
	if err != nil {
		return v2BoardroomPayload{}, err
	}
	conversations, err := s.boardrooms.ListConversations(ctx, roomID, 50)
	if err != nil {
		return v2BoardroomPayload{}, err
	}
	documents, err := s.documentOptions(ctx, nil)
	if err != nil {
		documents = []components.DocumentOptionView{}
	}
	return v2BoardroomPayload{
		Boardroom: boardroomView(room), Personas: personaViews(personas), Conversations: conversationViews(conversations),
		Documents: documents, Template: s.tenantBusinessTemplate(ctx).String(),
	}, nil
}

func (s *Server) v2ConversationAPI(w http.ResponseWriter, r *http.Request) {
	payload, err := s.loadV2Conversation(r.Context(), chi.URLParam(r, "conversationID"))
	if err != nil {
		httpx.WriteProblem(w, http.StatusNotFound, "conversation_unavailable", "The conversation could not be loaded.")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, payload)
}

func (s *Server) loadV2Conversation(ctx context.Context, rawID string) (v2ConversationPayload, error) {
	conversationID, err := domain.ParseConversationID(rawID)
	if err != nil {
		return v2ConversationPayload{}, err
	}
	conversation, err := s.boardrooms.GetConversation(ctx, conversationID)
	if err != nil {
		return v2ConversationPayload{}, err
	}
	run, err := s.boardrooms.GetRun(ctx, conversation.LatestRunID)
	if err != nil {
		return v2ConversationPayload{}, err
	}
	room, err := s.boardrooms.Get(ctx, conversation.BoardroomID)
	if err != nil {
		return v2ConversationPayload{}, err
	}
	personas, err := s.boardrooms.Personas(ctx, conversation.BoardroomID)
	if err != nil {
		return v2ConversationPayload{}, err
	}
	messages, cursor, err := s.boardrooms.MessagesSnapshot(ctx, conversation.ID, run.ID)
	if err != nil {
		return v2ConversationPayload{}, err
	}
	attachments, err := s.boardrooms.ConversationDocuments(ctx, conversation.ID)
	if err != nil {
		return v2ConversationPayload{}, err
	}
	selected := make(map[string]bool, len(attachments))
	for _, attachment := range attachments {
		selected[attachment.ID] = true
	}
	documents, err := s.documentOptions(ctx, selected)
	if err != nil {
		documents = []components.DocumentOptionView{}
	}
	approvals, err := s.approvals.PendingApprovalsForRun(ctx, run.ID)
	if err != nil {
		approvals = []toolbroker.Approval{}
	}
	return v2ConversationPayload{
		Boardroom: boardroomView(room), Personas: personaViews(personas), Conversation: conversationView(conversation),
		Run: componentRun(run, cursor), Messages: messageViews(messages), Attachments: documentAttachmentViews(attachments),
		Documents: documents, Approvals: approvalViews(approvals),
	}, nil
}

func (s *Server) v2ConversationEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	_, _ = fmt.Fprint(w, "retry: 2000\n\n")
	controller := http.NewResponseController(w)
	if err := controller.Flush(); err != nil {
		return
	}
	conversationID := chi.URLParam(r, "conversationID")
	ticker := time.NewTicker(time.Second)
	keepalive := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	defer keepalive.Stop()
	lastDigest := ""
	sequence := parseEventCursor(r)
	emit := func() bool {
		payload, err := s.loadV2Conversation(r.Context(), conversationID)
		if err != nil {
			return !errors.Is(err, context.Canceled)
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return true
		}
		sum := sha256.Sum256(encoded)
		digest := hex.EncodeToString(sum[:])
		if digest == lastDigest {
			return true
		}
		lastDigest = digest
		sequence++
		writeSSE(w, sequence, "snapshot", string(encoded))
		_ = controller.Flush()
		return true
	}
	if !emit() {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if !emit() {
				return
			}
		case <-keepalive.C:
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
			_ = controller.Flush()
		}
	}
}

func (s *Server) loadV2YourTurn(ctx context.Context, tab string, includeHistory bool) (v2YourTurnPayload, error) {
	if tab != "reviews" && tab != "approvals" {
		tab = "input"
	}
	inputsCount, reviewsCount, approvalsCount, err := s.approvals.YourTurnCounts(ctx)
	if err != nil {
		return v2YourTurnPayload{}, err
	}
	payload := v2YourTurnPayload{
		Tab:    tab,
		Counts: components.YourTurnCountsView{Inputs: inputsCount, Reviews: reviewsCount, Approvals: approvalsCount},
		Inputs: []components.HumanInputRequestView{}, Approvals: []components.ApprovalView{}, Documents: []components.DocumentOptionView{},
	}
	if tab == "input" {
		state, err := s.approvals.InputCoordinatorState(ctx)
		if err != nil {
			return v2YourTurnPayload{}, err
		}
		inputs, err := s.approvals.ListHumanInputs(ctx, includeHistory)
		if err != nil {
			return v2YourTurnPayload{}, err
		}
		documents, err := s.documentOptions(ctx, nil)
		if err != nil {
			return v2YourTurnPayload{}, err
		}
		payload.Coordinator = inputCoordinatorView(state)
		payload.Inputs = humanInputRequestViews(inputs)
		payload.Documents = documents
		return payload, nil
	}
	items, err := s.approvals.List(ctx, includeHistory)
	if err != nil {
		return v2YourTurnPayload{}, err
	}
	for _, item := range items {
		isReview := item.ActionType == toolbroker.WorkReviewAction
		if (tab == "reviews" && isReview) || (tab == "approvals" && !isReview) {
			payload.Approvals = append(payload.Approvals, approvalViews([]toolbroker.Approval{item})[0])
		}
	}
	return payload, nil
}

func (s *Server) v2YourTurnEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	_, _ = fmt.Fprint(w, "retry: 2500\n\n")
	controller := http.NewResponseController(w)
	if err := controller.Flush(); err != nil {
		return
	}
	tab := r.URL.Query().Get("tab")
	includeHistory := r.URL.Query().Get("history") == "1"
	ticker := time.NewTicker(2 * time.Second)
	keepalive := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	defer keepalive.Stop()
	lastDigest := ""
	sequence := parseEventCursor(r)
	emit := func() bool {
		payload, err := s.loadV2YourTurn(r.Context(), tab, includeHistory)
		if err != nil {
			return !errors.Is(err, context.Canceled)
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return true
		}
		sum := sha256.Sum256(encoded)
		digest := hex.EncodeToString(sum[:])
		if digest == lastDigest {
			return true
		}
		lastDigest = digest
		sequence++
		writeSSE(w, sequence, "snapshot", string(encoded))
		_ = controller.Flush()
		return true
	}
	if !emit() {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if !emit() {
				return
			}
		case <-keepalive.C:
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
			_ = controller.Flush()
		}
	}
}

func (s *Server) loadV2Ticket(ctx context.Context, workItemID string) (v2TicketPayload, error) {
	item, err := s.store.GetWorkItem(ctx, workItemID)
	if err != nil {
		return v2TicketPayload{}, err
	}
	subtasks, err := s.store.ListSubtasks(ctx, item.ID)
	if err != nil {
		return v2TicketPayload{}, err
	}
	room, err := s.workItemBoardroom(ctx, item)
	if err != nil {
		return v2TicketPayload{}, err
	}
	personas, err := s.boardrooms.Personas(ctx, room.ID)
	if err != nil {
		return v2TicketPayload{}, err
	}
	var run boardroom.Run
	var messages []boardroom.Message
	var attachments []boardroom.DocumentAttachment
	var approvals []toolbroker.Approval
	selectedDocuments := map[string]bool{}
	if item.ConversationID != "" {
		conversationID, parseErr := domain.ParseConversationID(item.ConversationID)
		if parseErr == nil {
			conversation, conversationErr := s.boardrooms.GetConversation(ctx, conversationID)
			if conversationErr == nil {
				run, _ = s.boardrooms.GetRun(ctx, conversation.LatestRunID)
				messages, _, _ = s.boardrooms.MessagesSnapshot(ctx, conversationID, conversation.LatestRunID)
				attachments, _ = s.boardrooms.ConversationDocuments(ctx, conversationID)
				for _, attachment := range attachments {
					selectedDocuments[attachment.ID] = true
				}
				if run.ID.String() != "00000000-0000-0000-0000-000000000000" {
					approvals, _ = s.approvals.PendingApprovalsForRun(ctx, run.ID)
				}
			}
		}
	}
	documents, err := s.documentOptions(ctx, selectedDocuments)
	if err != nil {
		documents = []components.DocumentOptionView{}
	}
	return v2TicketPayload{
		Item: workItemViews([]WorkItem{item})[0], Subtasks: workItemViews(subtasks), Personas: personaViews(personas),
		Messages: messageViews(messages), Attachments: documentAttachmentViews(attachments), Documents: documents,
		Run: componentRun(run, 0), Approvals: approvalViews(approvals),
	}, nil
}

// v2WorkItemEvents gives the client a durable, reconnectable live surface now.
// It emits snapshots only when meaningful ticket state changes. The backing
// implementation can later move from this inexpensive change detector to the
// tenant event log without changing the browser contract.
func (s *Server) v2WorkItemEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	_, _ = fmt.Fprint(w, "retry: 2000\n\n")
	controller := http.NewResponseController(w)
	if err := controller.Flush(); err != nil {
		return
	}

	workItemID := chi.URLParam(r, "workItemID")
	ticker := time.NewTicker(time.Second)
	keepalive := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	defer keepalive.Stop()
	lastDigest := ""
	sequence := parseEventCursor(r)

	emit := func() bool {
		payload, err := s.loadV2Ticket(r.Context(), workItemID)
		if err != nil {
			return !errors.Is(err, context.Canceled)
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return true
		}
		sum := sha256.Sum256(encoded)
		digest := hex.EncodeToString(sum[:])
		if digest == lastDigest {
			return true
		}
		lastDigest = digest
		sequence++
		writeSSE(w, sequence, "snapshot", string(encoded))
		_ = controller.Flush()
		return true
	}
	if !emit() {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if !emit() {
				return
			}
		case <-keepalive.C:
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
			_ = controller.Flush()
		}
	}
}

func requestWantsJSON(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "application/json")
}

// requestWantsV2Page keeps the browser experience modern while preserving the
// server-rendered HTML contract used by health checks and shell smoke tests.
func requestWantsV2Page(r *http.Request) bool {
	if r.URL.Query().Get("legacy") == "1" {
		return false
	}
	if r.URL.Query().Get("v2") == "1" {
		return true
	}
	if strings.EqualFold(r.Header.Get("Sec-Fetch-Dest"), "document") {
		return true
	}
	return strings.Contains(strings.ToLower(r.Header.Get("User-Agent")), "mozilla/")
}

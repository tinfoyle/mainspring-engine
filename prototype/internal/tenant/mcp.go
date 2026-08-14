package tenant

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tinfoyle/mainspring-engine/internal/boardroom"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
	mailbox "github.com/tinfoyle/mainspring-engine/internal/email"
	"github.com/tinfoyle/mainspring-engine/internal/finance"
	"github.com/tinfoyle/mainspring-engine/internal/rag"
	toolbroker "github.com/tinfoyle/mainspring-engine/internal/tools"
)

const mcpServerVersion = "0.1.0"

type mcpListAgentsInput struct {
	BoardroomID string `json:"boardroom_id,omitempty" jsonschema:"optional boardroom UUID used to limit the agent list"`
}

type mcpSearchDocumentsInput struct {
	Query       string   `json:"query" jsonschema:"search phrase; use * to inspect the document catalog"`
	Limit       int      `json:"limit,omitempty" jsonschema:"maximum number of excerpts to return, from 1 to 20"`
	DocumentIDs []string `json:"document_ids,omitempty" jsonschema:"optional document UUIDs that bound the search"`
}

type mcpUploadDocumentInput struct {
	Name      string `json:"name" jsonschema:"document filename or descriptive name"`
	MediaType string `json:"media_type,omitempty" jsonschema:"text media type such as text/plain or text/markdown"`
	Content   string `json:"content" jsonschema:"UTF-8 document content, no larger than 2 MB"`
}

type mcpSearchWebInput struct {
	Query          string   `json:"query" jsonschema:"public web search phrase"`
	Limit          int      `json:"limit,omitempty" jsonschema:"maximum results from 1 to 10"`
	IncludeDomains []string `json:"include_domains,omitempty" jsonschema:"optional public hostnames to include"`
	ExcludeDomains []string `json:"exclude_domains,omitempty" jsonschema:"optional public hostnames to exclude"`
	RecencyDays    int      `json:"recency_days,omitempty" jsonschema:"optional age limit from 1 to 3650 days"`
}

type mcpReadWebInput struct {
	URL           string `json:"url" jsonschema:"absolute public HTTP or HTTPS URL"`
	MaxCharacters int    `json:"max_characters,omitempty" jsonschema:"bounded page content from 500 to 12000 characters"`
}

type mcpListWorkItemsInput struct {
	Status string `json:"status,omitempty" jsonschema:"active, all, open, in_progress, waiting, or done"`
	Kind   string `json:"kind,omitempty" jsonschema:"all, ticket, or todo"`
	Query  string `json:"query,omitempty" jsonschema:"optional title and description search"`
}

type mcpWorkItemIDInput struct {
	WorkItemID string `json:"work_item_id" jsonschema:"work item UUID"`
}

type mcpCreateWorkItemInput struct {
	Kind             string `json:"kind,omitempty" jsonschema:"ticket or todo; defaults to ticket"`
	Title            string `json:"title" jsonschema:"short work item title"`
	Description      string `json:"description,omitempty" jsonschema:"work context, plan, or acceptance criteria"`
	Priority         string `json:"priority,omitempty" jsonschema:"low, normal, high, or urgent; defaults to normal"`
	ParentWorkItemID string `json:"parent_work_item_id,omitempty" jsonschema:"optional parent ticket UUID for a subtask"`
	DueAt            string `json:"due_at,omitempty" jsonschema:"optional RFC3339 timestamp or YYYY-MM-DD date"`
	AssignToActor    bool   `json:"assign_to_actor,omitempty" jsonschema:"assign the item to the MCP identity"`
	Responsibility   string `json:"responsibility,omitempty" jsonschema:"owner, agent, shared, or external; defaults from assignment"`
}

type mcpUpdateWorkItemStatusInput struct {
	WorkItemID string `json:"work_item_id" jsonschema:"work item UUID"`
	Status     string `json:"status" jsonschema:"open, in_progress, waiting, done, or canceled"`
}

type mcpMessageTicketInput struct {
	WorkItemID  string   `json:"work_item_id" jsonschema:"parent ticket UUID"`
	Prompt      string   `json:"prompt" jsonschema:"message for the selected agents"`
	PersonaIDs  []string `json:"persona_ids" jsonschema:"one or more enabled agent UUIDs from mainspring_list_agents"`
	DocumentIDs []string `json:"document_ids,omitempty" jsonschema:"optional uploaded document UUIDs to attach to the ticket conversation"`
}

type mcpRunIDInput struct {
	RunID string `json:"run_id" jsonschema:"boardroom run UUID returned by mainspring_message_ticket"`
}

type mcpListApprovalsInput struct {
	IncludeDecided bool `json:"include_decided,omitempty" jsonschema:"include approval history instead of only pending requests"`
}

type mcpDecideApprovalInput struct {
	ApprovalID string `json:"approval_id" jsonschema:"approval request UUID"`
	Decision   string `json:"decision" jsonschema:"approve or reject"`
}

type mcpFinanceLineInput struct {
	AccountID   string `json:"account_id" jsonschema:"posting account UUID"`
	Memo        string `json:"memo,omitempty" jsonschema:"optional line memo"`
	DebitMinor  int64  `json:"debit_minor,omitempty" jsonschema:"debit in integer minor currency units"`
	CreditMinor int64  `json:"credit_minor,omitempty" jsonschema:"credit in integer minor currency units"`
}

type mcpFinanceQueryInput struct {
	Action   string `json:"action" jsonschema:"list_ledgers, get_ledger, list_entries, or get_entry"`
	LedgerID string `json:"ledger_id,omitempty" jsonschema:"ledger UUID required for ledger and entry lists"`
	EntryID  string `json:"entry_id,omitempty" jsonschema:"journal entry UUID required for get_entry"`
	Limit    int    `json:"limit,omitempty" jsonschema:"maximum entries from 1 to 200"`
}

type mcpFinanceManageInput struct {
	Action                 string                `json:"action" jsonschema:"create_ledger, update_ledger, create_account, update_account, create_entry, update_entry, post_entry, or void_entry"`
	LedgerID               string                `json:"ledger_id,omitempty" jsonschema:"ledger UUID"`
	AccountID              string                `json:"account_id,omitempty" jsonschema:"account UUID"`
	EntryID                string                `json:"entry_id,omitempty" jsonschema:"journal entry UUID"`
	Name                   string                `json:"name,omitempty" jsonschema:"ledger or account name"`
	Code                   string                `json:"code,omitempty" jsonschema:"ledger or account code"`
	Description            string                `json:"description,omitempty" jsonschema:"ledger, account, or entry description"`
	Currency               string                `json:"currency,omitempty" jsonschema:"three-letter currency code"`
	Status                 string                `json:"status,omitempty" jsonschema:"active or archived"`
	ParentAccountID        string                `json:"parent_account_id,omitempty" jsonschema:"optional parent account UUID for a sub-account"`
	AccountType            string                `json:"account_type,omitempty" jsonschema:"asset, liability, equity, income, or expense"`
	AllowPosting           bool                  `json:"allow_posting,omitempty" jsonschema:"whether journal lines may post to the account"`
	CreateStandardAccounts bool                  `json:"create_standard_accounts,omitempty" jsonschema:"create the standard six-account chart with a new ledger"`
	EntryDate              string                `json:"entry_date,omitempty" jsonschema:"journal date in YYYY-MM-DD format"`
	Reference              string                `json:"reference,omitempty" jsonschema:"receipt, invoice, or source reference"`
	WorkItemID             string                `json:"work_item_id,omitempty" jsonschema:"optional related work item UUID"`
	Lines                  []mcpFinanceLineInput `json:"lines,omitempty" jsonschema:"two or more balanced journal lines"`
}

type mcpWorkItem struct {
	ID             string     `json:"id"`
	Number         int64      `json:"number"`
	Kind           string     `json:"kind"`
	Title          string     `json:"title"`
	Description    string     `json:"description"`
	Status         string     `json:"status"`
	Priority       string     `json:"priority"`
	Source         string     `json:"source"`
	AssignedToName string     `json:"assigned_to_name,omitempty"`
	AssignedToType string     `json:"assigned_to_type,omitempty"`
	Responsibility string     `json:"responsibility"`
	BoardroomID    string     `json:"boardroom_id,omitempty"`
	ConversationID string     `json:"conversation_id,omitempty"`
	RunID          string     `json:"run_id,omitempty"`
	ParentID       string     `json:"parent_id,omitempty"`
	ParentNumber   int64      `json:"parent_number,omitempty"`
	DueAt          *time.Time `json:"due_at,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type mcpMessage struct {
	ID          string    `json:"id"`
	RunID       string    `json:"run_id"`
	PersonaID   string    `json:"persona_id,omitempty"`
	PersonaName string    `json:"persona_name,omitempty"`
	PersonaRole string    `json:"persona_role,omitempty"`
	Role        string    `json:"role"`
	Body        string    `json:"body"`
	Sequence    int64     `json:"sequence"`
	CreatedAt   time.Time `json:"created_at"`
}

type mcpApproval struct {
	ID             string          `json:"id"`
	RunID          string          `json:"run_id"`
	PersonaName    string          `json:"persona_name,omitempty"`
	PersonaRole    string          `json:"persona_role,omitempty"`
	ActionType     string          `json:"action_type"`
	Reason         string          `json:"reason"`
	Evidence       []string        `json:"evidence"`
	RequestPayload json.RawMessage `json:"request_payload"`
	ActionStatus   string          `json:"action_status"`
	Status         string          `json:"status"`
	RequestedAt    time.Time       `json:"requested_at"`
	DecidedAt      *time.Time      `json:"decided_at,omitempty"`
}

func (s *Server) mcpHTTPHandler() http.Handler {
	if strings.TrimSpace(s.config.MCPToken) == "" {
		return http.NotFoundHandler()
	}
	server := s.newMCPServer()
	streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless:                    true,
		JSONResponse:                 true,
		Logger:                       s.logger.With("transport", "mcp"),
		MaxRequestBodyBytes:          4 << 20,
		PropagateRequestCancellation: true,
	})
	var handler http.Handler = streamable
	handler = http.NewCrossOriginProtection().Handler(handler)
	return s.requireMCPToken(handler)
}

func (s *Server) requireMCPToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provided := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		expected := s.config.MCPToken
		if len(provided) != len(expected) || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="Mainspring MCP"`)
			http.Error(w, "MCP authentication required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) mcpActor(ctx context.Context) (User, error) {
	actor, err := s.store.UserByEmail(ctx, s.config.MCPUserEmail)
	if err != nil {
		return User{}, errors.New("the configured MCP identity is unavailable")
	}
	return actor, nil
}

func (s *Server) newMCPServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "mainspring",
		Version: mcpServerVersion,
		Title:   "Mainspring tenant workspace",
	}, nil)

	mcp.AddTool(server, mcpTool("mainspring_list_agents", "List agents", "List enabled agents available for ticket conversations, including their IDs, boardrooms, roles, and granted capabilities.", true, false, false), s.mcpListAgents)
	mcp.AddTool(server, mcpTool("mainspring_list_documents", "List documents", "List the tenant's indexed documents for attachment or recall.", true, false, false), s.mcpListDocuments)
	mcp.AddTool(server, mcpTool("mainspring_search_documents", "Search documents", "Search authorized tenant documentation and return bounded excerpts suitable for evidence-backed answers.", true, false, false), s.mcpSearchDocuments)
	mcp.AddTool(server, mcpTool("mainspring_upload_document", "Upload document", "Add and index a UTF-8 text document in the tenant document library.", false, false, true), s.mcpUploadDocument)
	if s.mcpHasTool(toolbroker.WebSearchTool, domain.CapabilityWebSearch) {
		mcp.AddTool(server, mcpTool("mainspring_search_web", "Search public web", "Search public sources through Mainspring's governed web-research provider and return bounded, citable metadata.", true, false, false), s.mcpSearchWeb)
	}
	if s.mcpHasTool(toolbroker.WebReadTool, domain.CapabilityWebRead) {
		mcp.AddTool(server, mcpTool("mainspring_read_web_page", "Read public web page", "Read bounded main content from one public page through Mainspring's governed web-research provider.", true, false, false), s.mcpReadWebPage)
	}
	mcp.AddTool(server, mcpTool("mainspring_list_work_items", "List work items", "List and filter tickets and todos in the tenant work queue.", true, false, false), s.mcpListWorkItems)
	mcp.AddTool(server, mcpTool("mainspring_get_work_item", "Get work item", "Load a ticket with its subtasks, linked agent conversation, documents, run status, and pending approvals.", true, false, false), s.mcpGetWorkItem)
	mcp.AddTool(server, mcpTool("mainspring_create_work_item", "Create work item", "Create a ticket, todo, or parent-linked subtask as the configured MCP identity.", false, false, false), s.mcpCreateWorkItem)
	mcp.AddTool(server, mcpTool("mainspring_update_work_item_status", "Update work item status", "Move a work item through open, in-progress, waiting, done, or canceled states.", false, true, true), s.mcpUpdateWorkItemStatus)
	mcp.AddTool(server, mcpTool("mainspring_message_ticket", "Message ticket agents", "Start or continue a durable ticket conversation with exactly the selected agents and attached documents. Returns a run ID for polling.", false, false, false), s.mcpMessageTicket)
	mcp.AddTool(server, mcpTool("mainspring_get_run", "Get agent run", "Poll a durable agent run and return conversation messages, attachments, and pending approvals.", true, false, false), s.mcpGetRun)
	mcp.AddTool(server, mcpTool("mainspring_list_approvals", "List approvals", "List pending approval-gated actions or their decision history.", true, false, false), s.mcpListApprovals)
	mcp.AddTool(server, mcpTool("mainspring_decide_approval", "Decide approval", "Approve or reject an agent-proposed external action. Only an owner MCP identity may call this tool.", false, true, true), s.mcpDecideApproval)
	mcp.AddTool(server, mcpTool("mainspring_query_finance", "Query financial ledgers", "Inspect ledgers, hierarchical accounts, balances, and journal entries. Monetary amounts are integer minor units.", true, false, false), s.mcpQueryFinance)
	mcp.AddTool(server, mcpTool("mainspring_manage_finance", "Manage financial ledgers", "Create and update ledgers and sub-accounts, create or edit balanced drafts, post entries, and void posted entries through audited reversals.", false, false, false), s.mcpManageFinance)
	return server
}

func (s *Server) mcpQueryFinance(ctx context.Context, _ *mcp.CallToolRequest, input mcpFinanceQueryInput) (*mcp.CallToolResult, any, error) {
	if _, err := s.mcpActor(ctx); err != nil {
		return nil, nil, err
	}
	var value any
	var err error
	switch input.Action {
	case "list_ledgers":
		value, err = s.finance.ListLedgers(ctx)
	case "get_ledger":
		var ledger finance.Ledger
		ledger, err = s.finance.GetLedger(ctx, input.LedgerID)
		if err == nil {
			var accounts []finance.Account
			accounts, err = s.finance.Accounts(ctx, input.LedgerID)
			value = map[string]any{"ledger": ledger, "accounts": accounts}
		}
	case "list_entries":
		value, err = s.finance.ListEntries(ctx, input.LedgerID, input.Limit)
	case "get_entry":
		value, err = s.finance.GetEntry(ctx, input.EntryID)
	default:
		return nil, nil, errors.New("finance action is invalid")
	}
	if err != nil {
		return nil, nil, err
	}
	return nil, map[string]any{"result": value}, nil
}

func (s *Server) mcpManageFinance(ctx context.Context, _ *mcp.CallToolRequest, input mcpFinanceManageInput) (*mcp.CallToolResult, any, error) {
	user, err := s.mcpActor(ctx)
	if err != nil {
		return nil, nil, err
	}
	actor := finance.Actor{Type: "mcp", ID: user.ID}
	lines := make([]finance.EntryLineInput, 0, len(input.Lines))
	for _, line := range input.Lines {
		lines = append(lines, finance.EntryLineInput{AccountID: line.AccountID, Memo: line.Memo, DebitMinor: line.DebitMinor, CreditMinor: line.CreditMinor})
	}
	date := time.Now()
	if input.EntryDate != "" {
		date, err = time.Parse("2006-01-02", input.EntryDate)
		if err != nil {
			return nil, nil, errors.New("entry_date must use YYYY-MM-DD")
		}
	}
	var value any
	switch input.Action {
	case "create_ledger":
		value, err = s.finance.CreateLedger(ctx, finance.CreateLedgerInput{Name: input.Name, Code: input.Code, Description: input.Description, Currency: input.Currency, CreateStandardAccounts: input.CreateStandardAccounts}, actor)
	case "update_ledger":
		value, err = s.finance.UpdateLedger(ctx, input.LedgerID, finance.UpdateLedgerInput{Name: input.Name, Code: input.Code, Description: input.Description, Currency: input.Currency, Status: input.Status}, actor)
	case "create_account":
		value, err = s.finance.CreateAccount(ctx, finance.CreateAccountInput{LedgerID: input.LedgerID, ParentAccountID: input.ParentAccountID, Code: input.Code, Name: input.Name, Description: input.Description, Type: input.AccountType, AllowPosting: input.AllowPosting}, actor)
	case "update_account":
		value, err = s.finance.UpdateAccount(ctx, input.AccountID, finance.UpdateAccountInput{ParentAccountID: input.ParentAccountID, Code: input.Code, Name: input.Name, Description: input.Description, Status: input.Status, AllowPosting: input.AllowPosting}, actor)
	case "create_entry":
		value, err = s.finance.CreateEntry(ctx, finance.CreateEntryInput{LedgerID: input.LedgerID, Description: input.Description, Reference: input.Reference, Source: "mcp", EntryDate: date, WorkItemID: input.WorkItemID, Lines: lines}, actor)
	case "update_entry":
		value, err = s.finance.UpdateEntry(ctx, input.EntryID, finance.UpdateEntryInput{Description: input.Description, Reference: input.Reference, EntryDate: date, Lines: lines}, actor)
	case "post_entry":
		value, err = s.finance.PostEntry(ctx, input.EntryID, actor)
	case "void_entry":
		value, err = s.finance.VoidEntry(ctx, input.EntryID, actor)
	default:
		return nil, nil, errors.New("finance action is invalid")
	}
	if err != nil {
		return nil, nil, err
	}
	return nil, map[string]any{"result": value}, nil
}

func (s *Server) mcpHasTool(name string, capability domain.Capability) bool {
	if s.toolBroker == nil {
		return false
	}
	definitions := s.toolBroker.Definitions([]domain.ToolGrant{{Capability: capability}})
	return len(definitions) == 1 && definitions[0].Name == name
}

func (s *Server) mcpSearchWeb(ctx context.Context, _ *mcp.CallToolRequest, input mcpSearchWebInput) (*mcp.CallToolResult, any, error) {
	actor, err := s.mcpActor(ctx)
	if err != nil {
		return nil, nil, err
	}
	return s.mcpInvokeBroker(ctx, actor, domain.CapabilityWebSearch, toolbroker.WebSearchTool, input)
}

func (s *Server) mcpReadWebPage(ctx context.Context, _ *mcp.CallToolRequest, input mcpReadWebInput) (*mcp.CallToolResult, any, error) {
	actor, err := s.mcpActor(ctx)
	if err != nil {
		return nil, nil, err
	}
	return s.mcpInvokeBroker(ctx, actor, domain.CapabilityWebRead, toolbroker.WebReadTool, input)
}

func (s *Server) mcpInvokeBroker(ctx context.Context, actor User, capability domain.Capability, name string, input any) (*mcp.CallToolResult, any, error) {
	if s.toolIssuer == nil || s.toolBroker == nil {
		return nil, nil, errors.New("web research is not configured")
	}
	actorID, err := domain.ParsePersonaID(actor.ID)
	if err != nil {
		return nil, nil, errors.New("the configured MCP identity has an invalid ID")
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, nil, err
	}
	token, err := s.toolIssuer.Mint(domain.InvocationContext{
		TenantID: s.config.TenantID, BoardroomID: domain.NewBoardroomID(), RunID: domain.NewRunID(),
		PersonaID: actorID, InvocationID: domain.NewInvocationID(), ActorType: "user", ActorID: actor.ID,
		Grants: []domain.ToolGrant{{Capability: capability}}, ExpiresAt: time.Now().Add(2 * time.Minute),
	})
	if err != nil {
		return nil, nil, err
	}
	output, err := s.toolBroker.InvokeNamed(ctx, token, s.config.TenantID, name, payload)
	if err != nil {
		return nil, nil, err
	}
	var result any
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, nil, err
	}
	return nil, result, nil
}

func mcpTool(name, title, description string, readOnly, destructive, idempotent bool) *mcp.Tool {
	closedWorld := false
	return &mcp.Tool{
		Name: name, Title: title, Description: description,
		Annotations: &mcp.ToolAnnotations{
			Title: title, ReadOnlyHint: readOnly, DestructiveHint: &destructive,
			IdempotentHint: idempotent, OpenWorldHint: &closedWorld,
		},
	}
}

func (s *Server) mcpListAgents(ctx context.Context, _ *mcp.CallToolRequest, input mcpListAgentsInput) (*mcp.CallToolResult, any, error) {
	if _, err := s.mcpActor(ctx); err != nil {
		return nil, nil, err
	}
	if input.BoardroomID != "" {
		id, err := domain.ParseBoardroomID(input.BoardroomID)
		if err != nil {
			return nil, nil, errors.New("boardroom_id must be a UUID")
		}
		input.BoardroomID = id.String()
	}
	agents, err := s.boardrooms.ListAgents(ctx)
	if err != nil {
		return nil, nil, err
	}
	result := make([]map[string]any, 0, len(agents))
	for _, persona := range agents {
		if !persona.Enabled || (input.BoardroomID != "" && persona.BoardroomID.String() != input.BoardroomID) {
			continue
		}
		capabilities := make([]string, 0, len(persona.Grants))
		for _, grant := range persona.Grants {
			capabilities = append(capabilities, string(grant.Capability))
		}
		result = append(result, map[string]any{
			"id": persona.ID.String(), "boardroom_id": persona.BoardroomID.String(), "name": persona.Name,
			"role": persona.Role, "description": persona.Description, "capabilities": capabilities,
		})
	}
	return nil, map[string]any{"agents": result}, nil
}

func (s *Server) mcpListDocuments(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	if _, err := s.mcpActor(ctx); err != nil {
		return nil, nil, err
	}
	documents, err := s.documents.ListDocuments(ctx)
	if err != nil {
		return nil, nil, err
	}
	return nil, map[string]any{"documents": documents}, nil
}

func (s *Server) mcpSearchDocuments(ctx context.Context, _ *mcp.CallToolRequest, input mcpSearchDocumentsInput) (*mcp.CallToolResult, any, error) {
	if _, err := s.mcpActor(ctx); err != nil {
		return nil, nil, err
	}
	input.Query = strings.TrimSpace(input.Query)
	if input.Query == "" || len(input.Query) > 500 {
		return nil, nil, errors.New("query must contain between 1 and 500 characters")
	}
	if input.Limit == 0 {
		input.Limit = 5
	}
	if input.Limit < 1 || input.Limit > 20 {
		return nil, nil, errors.New("limit must be between 1 and 20")
	}
	results, err := s.documents.Search(ctx, input.Query, input.Limit, input.DocumentIDs)
	if err != nil {
		return nil, nil, err
	}
	return nil, map[string]any{"query": input.Query, "results": results}, nil
}

func (s *Server) mcpUploadDocument(ctx context.Context, _ *mcp.CallToolRequest, input mcpUploadDocumentInput) (*mcp.CallToolResult, any, error) {
	actor, err := s.mcpActor(ctx)
	if err != nil {
		return nil, nil, err
	}
	if input.MediaType == "" {
		input.MediaType = "text/plain"
	}
	document, err := s.documents.IngestText(ctx, input.Name, input.MediaType, input.Content, actor.ID)
	if err != nil {
		return nil, nil, err
	}
	return nil, map[string]any{"document": document}, nil
}

func (s *Server) mcpListWorkItems(ctx context.Context, _ *mcp.CallToolRequest, input mcpListWorkItemsInput) (*mcp.CallToolResult, any, error) {
	if _, err := s.mcpActor(ctx); err != nil {
		return nil, nil, err
	}
	items, summary, err := s.store.ListWorkItems(ctx, WorkFilter{Status: input.Status, Kind: input.Kind, Query: input.Query})
	if err != nil {
		return nil, nil, err
	}
	return nil, map[string]any{
		"work_items": mcpWorkItems(items),
		"summary": map[string]int{
			"active": summary.Active, "in_progress": summary.InProgress, "waiting": summary.Waiting,
			"urgent": summary.Urgent, "done": summary.Done,
		},
	}, nil
}

func (s *Server) mcpGetWorkItem(ctx context.Context, _ *mcp.CallToolRequest, input mcpWorkItemIDInput) (*mcp.CallToolResult, any, error) {
	if _, err := s.mcpActor(ctx); err != nil {
		return nil, nil, err
	}
	item, err := s.store.GetWorkItem(ctx, input.WorkItemID)
	if err != nil {
		return nil, nil, err
	}
	subtasks, err := s.store.ListSubtasks(ctx, item.ID)
	if err != nil {
		return nil, nil, err
	}
	result := map[string]any{"work_item": mcpWorkItemFrom(item), "subtasks": mcpWorkItems(subtasks)}
	if item.ConversationID == "" {
		return nil, result, nil
	}
	conversationID, err := domain.ParseConversationID(item.ConversationID)
	if err != nil {
		return nil, nil, err
	}
	conversation, err := s.boardrooms.GetConversation(ctx, conversationID)
	if err != nil {
		return nil, nil, err
	}
	run, err := s.boardrooms.GetRun(ctx, conversation.LatestRunID)
	if err != nil {
		return nil, nil, err
	}
	messages, _, err := s.boardrooms.MessagesSnapshot(ctx, conversationID, run.ID)
	if err != nil {
		return nil, nil, err
	}
	attachments, err := s.boardrooms.ConversationDocuments(ctx, conversationID)
	if err != nil {
		return nil, nil, err
	}
	approvals, err := s.approvals.PendingApprovalsForRun(ctx, run.ID)
	if err != nil {
		return nil, nil, err
	}
	result["run"] = mcpRun(run)
	result["messages"] = mcpMessages(messages)
	result["documents"] = attachments
	result["pending_approvals"] = mcpApprovals(approvals)
	return nil, result, nil
}

func (s *Server) mcpCreateWorkItem(ctx context.Context, _ *mcp.CallToolRequest, input mcpCreateWorkItemInput) (*mcp.CallToolResult, any, error) {
	actor, err := s.mcpActor(ctx)
	if err != nil {
		return nil, nil, err
	}
	if input.Kind == "" {
		input.Kind = "ticket"
	}
	if input.Priority == "" {
		input.Priority = "normal"
	}
	var dueAt *time.Time
	if strings.TrimSpace(input.DueAt) != "" {
		parsed, parseErr := parseMCPDueAt(input.DueAt)
		if parseErr != nil {
			return nil, nil, parseErr
		}
		dueAt = &parsed
	}
	if input.ParentWorkItemID != "" {
		if _, err := s.store.GetWorkItem(ctx, input.ParentWorkItemID); err != nil {
			return nil, nil, errors.New("parent work item was not found")
		}
	}
	assignedUserID := ""
	if input.AssignToActor {
		assignedUserID = actor.ID
	}
	item, err := s.store.CreateWorkItem(ctx, CreateWorkItemInput{
		Kind: input.Kind, Title: input.Title, Description: input.Description, Priority: input.Priority,
		Source: "user", CreatedByUserID: actor.ID, AssignedUserID: assignedUserID,
		ParentID: input.ParentWorkItemID, Responsibility: input.Responsibility, DueAt: dueAt,
	})
	if err != nil {
		return nil, nil, err
	}
	return nil, map[string]any{"work_item": mcpWorkItemFrom(item)}, nil
}

func (s *Server) mcpUpdateWorkItemStatus(ctx context.Context, _ *mcp.CallToolRequest, input mcpUpdateWorkItemStatusInput) (*mcp.CallToolResult, any, error) {
	if _, err := s.mcpActor(ctx); err != nil {
		return nil, nil, err
	}
	if err := s.store.UpdateWorkItemStatus(ctx, input.WorkItemID, input.Status); err != nil {
		return nil, nil, err
	}
	item, err := s.store.GetWorkItem(ctx, input.WorkItemID)
	if err != nil {
		return nil, nil, err
	}
	return nil, map[string]any{"work_item": mcpWorkItemFrom(item)}, nil
}

func (s *Server) mcpMessageTicket(ctx context.Context, _ *mcp.CallToolRequest, input mcpMessageTicketInput) (*mcp.CallToolResult, any, error) {
	actor, err := s.mcpActor(ctx)
	if err != nil {
		return nil, nil, err
	}
	item, err := s.store.GetWorkItem(ctx, input.WorkItemID)
	if err != nil {
		return nil, nil, err
	}
	prompt := strings.TrimSpace(input.Prompt)
	if prompt == "" || len(prompt) > 12000 {
		return nil, nil, errors.New("prompt must contain between 1 and 12,000 characters")
	}
	room, err := s.workItemBoardroom(ctx, item)
	if err != nil {
		return nil, nil, err
	}
	personas, err := s.boardrooms.Personas(ctx, room.ID)
	if err != nil {
		return nil, nil, err
	}
	selectedIDs, err := selectedWorkItemPersonas(input.PersonaIDs, personas, room.MaxTurns)
	if err != nil {
		return nil, nil, err
	}
	attachments, err := s.mcpDocumentAttachments(ctx, input.DocumentIDs)
	if err != nil {
		return nil, nil, err
	}
	var run boardroom.Run
	if item.ConversationID == "" {
		run, err = s.boardrooms.CreateTargetedRunWithDocuments(ctx, room.ID, actor.ID, fmt.Sprintf("Ticket #%04d: %s", item.Number, item.Title), prompt, item.ID, selectedIDs, attachments)
	} else {
		conversationID, parseErr := domain.ParseConversationID(item.ConversationID)
		if parseErr != nil {
			return nil, nil, parseErr
		}
		run, err = s.boardrooms.CreateTargetedFollowUpRunWithDocuments(ctx, conversationID, actor.ID, prompt, item.ID, selectedIDs, attachments)
	}
	if err != nil {
		return nil, nil, err
	}
	if err := s.store.LinkWorkItemConversation(ctx, item.ID, room.ID.String(), run.ConversationID.String(), run.ID.String()); err != nil {
		_ = s.boardrooms.SetRunStatus(ctx, run.ID, domain.RunFailed, "The ticket conversation could not be linked.")
		return nil, nil, err
	}
	if err := s.dispatcher.Dispatch(ctx, run.ID); err != nil {
		_ = s.boardrooms.SetRunStatus(ctx, run.ID, domain.RunFailed, "The durable workflow could not be started.")
		return nil, nil, err
	}
	return nil, map[string]any{
		"work_item_id": item.ID, "conversation_id": run.ConversationID.String(), "run_id": run.ID.String(),
		"status": string(run.Status), "selected_persona_ids": selectedIDs,
		"next": "Poll mainspring_get_run with run_id until completed, failed, or awaiting_approval.",
	}, nil
}

func (s *Server) mcpGetRun(ctx context.Context, _ *mcp.CallToolRequest, input mcpRunIDInput) (*mcp.CallToolResult, any, error) {
	if _, err := s.mcpActor(ctx); err != nil {
		return nil, nil, err
	}
	runID, err := domain.ParseRunID(input.RunID)
	if err != nil {
		return nil, nil, errors.New("run_id must be a UUID")
	}
	run, err := s.boardrooms.GetRun(ctx, runID)
	if err != nil {
		return nil, nil, err
	}
	messages, _, err := s.boardrooms.MessagesSnapshot(ctx, run.ConversationID, run.ID)
	if err != nil {
		return nil, nil, err
	}
	attachments, err := s.boardrooms.ConversationDocuments(ctx, run.ConversationID)
	if err != nil {
		return nil, nil, err
	}
	approvals, err := s.approvals.PendingApprovalsForRun(ctx, run.ID)
	if err != nil {
		return nil, nil, err
	}
	return nil, map[string]any{
		"run": mcpRun(run), "messages": mcpMessages(messages), "documents": attachments,
		"pending_approvals": mcpApprovals(approvals),
	}, nil
}

func (s *Server) mcpListApprovals(ctx context.Context, _ *mcp.CallToolRequest, input mcpListApprovalsInput) (*mcp.CallToolResult, any, error) {
	if _, err := s.mcpActor(ctx); err != nil {
		return nil, nil, err
	}
	approvals, err := s.approvals.List(ctx, input.IncludeDecided)
	if err != nil {
		return nil, nil, err
	}
	return nil, map[string]any{"approvals": mcpApprovals(approvals)}, nil
}

func (s *Server) mcpDecideApproval(ctx context.Context, _ *mcp.CallToolRequest, input mcpDecideApprovalInput) (*mcp.CallToolResult, any, error) {
	actor, err := s.mcpActor(ctx)
	if err != nil {
		return nil, nil, err
	}
	if actor.Role != "owner" {
		return nil, nil, errors.New("only an owner MCP identity may decide approvals")
	}
	decision := strings.ToLower(strings.TrimSpace(input.Decision))
	if decision != "approve" && decision != "reject" {
		return nil, nil, errors.New("decision must be approve or reject")
	}
	action, err := s.approvals.Decide(ctx, input.ApprovalID, actor.ID, decision == "approve")
	if err != nil {
		return nil, nil, err
	}
	if decision == "approve" {
		switch action.ActionType {
		case "email.send":
			var message mailbox.OutgoingMessage
			if err := json.Unmarshal(action.RequestPayload, &message); err != nil {
				return nil, nil, errors.New("approved email payload was invalid")
			}
			if _, err := s.email.Send(ctx, action.IdempotencyKey, message, "user", actor.ID, action.RunID); err != nil {
				return nil, nil, err
			}
		case toolbroker.TicketCreateAction:
			if err := s.executeApprovedTicket(ctx, action, actor.ID); err != nil {
				return nil, nil, err
			}
		case toolbroker.WorkReviewAction:
			if err := s.executeApprovedWorkReview(ctx, action); err != nil {
				return nil, nil, err
			}
		default:
			return nil, nil, errors.New("approved action type does not have an executor")
		}
	} else if action.ActionType == toolbroker.WorkReviewAction {
		if payload, decodeErr := toolbroker.DecodeWorkReviewPayload(action.RequestPayload); decodeErr == nil {
			_ = s.store.UpdateWorkItemStatus(ctx, payload.WorkItemID, "waiting")
		}
	}
	s.resumeRunAfterDecision(ctx, action.RunID, input.ApprovalID)
	return nil, map[string]any{
		"approval_id": input.ApprovalID, "decision": decision, "action_id": action.ID.String(),
		"action_type": action.ActionType, "run_id": optionalRunID(action.RunID),
	}, nil
}

func (s *Server) mcpDocumentAttachments(ctx context.Context, requested []string) ([]boardroom.DocumentAttachment, error) {
	if len(requested) > 20 {
		return nil, boardroom.ErrTooManyDocuments
	}
	documents, err := s.documents.ListDocuments(ctx)
	if err != nil {
		return nil, err
	}
	available := make(map[string]rag.Document, len(documents))
	for _, document := range documents {
		available[document.ID] = document
	}
	seen := map[string]bool{}
	attachments := make([]boardroom.DocumentAttachment, 0, len(requested))
	for _, id := range requested {
		id = strings.TrimSpace(id)
		document, ok := available[id]
		if !ok {
			return nil, fmt.Errorf("document %q was not found", id)
		}
		if !seen[id] {
			seen[id] = true
			attachments = append(attachments, boardroom.DocumentAttachment{ID: id, Name: document.Name})
		}
	}
	return attachments, nil
}

func parseMCPDueAt(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, nil
	}
	if parsed, err := time.Parse("2006-01-02", value); err == nil {
		return parsed.Add(24*time.Hour - time.Nanosecond), nil
	}
	return time.Time{}, errors.New("due_at must be an RFC3339 timestamp or YYYY-MM-DD date")
}

func mcpWorkItemFrom(item WorkItem) mcpWorkItem {
	return mcpWorkItem{
		ID: item.ID, Number: item.Number, Kind: item.Kind, Title: item.Title, Description: item.Description,
		Status: item.Status, Priority: item.Priority, Source: item.Source, AssignedToName: item.AssignedToName,
		AssignedToType: item.AssignedToType, BoardroomID: item.BoardroomID, ConversationID: item.ConversationID,
		Responsibility: item.Responsibility,
		RunID:          item.RunID, ParentID: item.ParentID, ParentNumber: item.ParentNumber, DueAt: item.DueAt,
		CompletedAt: item.CompletedAt, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
	}
}

func mcpWorkItems(items []WorkItem) []mcpWorkItem {
	result := make([]mcpWorkItem, 0, len(items))
	for _, item := range items {
		result = append(result, mcpWorkItemFrom(item))
	}
	return result
}

func mcpMessages(messages []boardroom.Message) []mcpMessage {
	result := make([]mcpMessage, 0, len(messages))
	for _, message := range messages {
		personaID := ""
		if message.PersonaID != nil {
			personaID = message.PersonaID.String()
		}
		result = append(result, mcpMessage{
			ID: message.ID, RunID: message.RunID.String(), PersonaID: personaID, PersonaName: message.PersonaName,
			PersonaRole: message.PersonaRole, Role: string(message.Role), Body: message.Body,
			Sequence: message.Sequence, CreatedAt: message.CreatedAt,
		})
	}
	return result
}

func mcpApprovals(approvals []toolbroker.Approval) []mcpApproval {
	result := make([]mcpApproval, 0, len(approvals))
	for _, approval := range approvals {
		result = append(result, mcpApproval{
			ID: approval.ID, RunID: approval.RunID.String(), PersonaName: approval.PersonaName,
			PersonaRole: approval.PersonaRole, ActionType: approval.ActionType, Reason: approval.Reason,
			Evidence: approval.Evidence, RequestPayload: approval.RequestPayload, ActionStatus: approval.ActionStatus,
			Status: approval.Status, RequestedAt: approval.RequestedAt, DecidedAt: approval.DecidedAt,
		})
	}
	return result
}

func mcpRun(run boardroom.Run) map[string]any {
	return map[string]any{
		"id": run.ID.String(), "boardroom_id": run.BoardroomID.String(), "conversation_id": run.ConversationID.String(),
		"status": string(run.Status), "prompt": run.Prompt, "turn_count": run.TurnCount, "max_turns": run.MaxTurns,
		"error": run.Error, "orchestration": run.Orchestration, "work_item_id": run.WorkItemID,
		"created_at": run.CreatedAt, "started_at": run.StartedAt, "completed_at": run.CompletedAt,
	}
}

func optionalRunID(runID *domain.RunID) string {
	if runID == nil {
		return ""
	}
	return runID.String()
}

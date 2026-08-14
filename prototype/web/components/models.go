package components

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

func Initial(value string) string {
	for _, character := range value {
		return string(character)
	}
	return "?"
}

func PauseValue(paused bool) string {
	if paused {
		return "false"
	}
	return "true"
}

func PauseLabel(paused bool) string {
	if paused {
		return "Resume"
	}
	return "Pause"
}

type UserView struct {
	DisplayName string
	Email       string
	Role        string
	Development bool
}

type FinanceLedgerView struct {
	ID, Name, Code, Description, Currency, Status string
	AccountCount, DraftCount                      int
	Income, Expenses, Net                         string
}

type FinanceAccountView struct {
	ID, ParentAccountID, Code, Name, Description, Type, Status, Balance string
	AllowPosting                                                        bool
}

type FinanceLineView struct {
	AccountID, AccountCode, AccountName, Memo, Debit, Credit string
}

type FinanceEntryView struct {
	ID, LedgerID, Number, Date, Description, Reference, Status, Source, Total string
	ReversalOfID                                                              string
	Lines                                                                     []FinanceLineView
}

type FinancePageView struct {
	Ledgers  []FinanceLedgerView
	Selected *FinanceLedgerView
	Accounts []FinanceAccountView
	Entries  []FinanceEntryView
	Notice   string
	Error    string
	Today    string
}

func FinanceSubAccountLabel(parentID string) string {
	if parentID != "" {
		return " · sub-account"
	}
	return ""
}

func FinanceReferenceLabel(reference string) string {
	if reference != "" {
		return " · " + reference
	}
	return ""
}

type ApprovalView struct {
	ID              string
	RunID           string
	PersonaName     string
	PersonaRole     string
	ActionType      string
	Reason          string
	Evidence        []string
	RequestPayload  string
	ActionStatus    string
	Status          string
	RequestedAt     time.Time
	DecidedAt       *time.Time
	WorkItemID      string
	WorkItemNumber  int64
	WorkItemTitle   string
	ReviewSummary   string
	Recommendations []string
}

type HumanInputAnswerView struct {
	Question string
	Answer   string
}

type HumanInputRequestView struct {
	ID               string
	ParentWorkItemID string
	ParentNumber     int64
	ParentTitle      string
	WorkItemID       string
	WorkItemNumber   int64
	PersonaName      string
	PersonaRole      string
	Questions        []string
	Answers          []HumanInputAnswerView
	DocumentIDs      []string
	Status           string
	RequestedAt      time.Time
	AnsweredAt       *time.Time
}

type YourTurnCountsView struct {
	Inputs    int
	Reviews   int
	Approvals int
}

type InputCoordinatorMessageView struct {
	Role        string
	MessageKind string
	Body        string
	CreatedAt   time.Time
}

type InputCoordinatorTicketView struct {
	ID     string
	Number int64
	Title  string
}

type InputCoordinatorQuestionView struct {
	FactKey       string
	Label         string
	Prompt        string
	TicketCount   int
	QuestionCount int
	Tickets       []InputCoordinatorTicketView
}

type BusinessKnowledgeFactView struct {
	Key        string
	Label      string
	Value      string
	SourceType string
	UpdatedAt  time.Time
}

type InputCoordinatorView struct {
	Messages         []InputCoordinatorMessageView
	Current          *InputCoordinatorQuestionView
	PendingQuestions int
	PendingRequests  int
	RemainingTopics  int
	KnownFacts       int
	RecentFacts      []BusinessKnowledgeFactView
}

type ProviderCircuitView struct {
	Provider            string
	ConsecutiveFailures int
	OpenUntil           *time.Time
	LastErrorCategory   string
}

type OperationsView struct {
	QueuedRuns           int
	RunningRuns          int
	AwaitingApproval     int
	FailedRuns           int
	ActiveInvocations    int
	CompletedInvocations int
	FailedInvocations    int
	MonthlyTokens        int64
	MonthlyCost          string
	AverageLatency       string
	ToolDenials          int
	ProviderCircuits     []ProviderCircuitView
}

func ApprovalStatusLabel(status string) string {
	switch status {
	case "pending":
		return "Needs review"
	case "approved":
		return "Approved"
	case "rejected":
		return "Rejected"
	default:
		return status
	}
}

func ActionTypeLabel(actionType string) string {
	switch actionType {
	case "email.send":
		return "Send email"
	case "tickets.create":
		return "Create work item"
	case "work.review":
		return "Review agent work"
	}
	return actionType
}

func ApprovalButtonLabel(actionType string) string {
	if actionType == "tickets.create" {
		return "Approve and create work item"
	}
	if actionType == "work.review" {
		return "Accept and complete ticket"
	}
	return "Approve and execute"
}

func ApprovalRejectLabel(actionType string) string {
	if actionType == "work.review" {
		return "Needs more work"
	}
	return "Reject"
}

type WorkItemView struct {
	ID             string
	Number         int64
	Kind           string
	Title          string
	Description    string
	Status         string
	Priority       string
	Source         string
	CreatedByName  string
	AssignedToName string
	AssignedToType string
	Responsibility string
	ParentID       string
	ParentNumber   int64
	DueLabel       string
	IsOverdue      bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type WorkSummaryView struct {
	Active     int
	InProgress int
	Waiting    int
	Urgent     int
	Done       int
}

type WorkFilterView struct {
	Status string
	Kind   string
	Query  string
}

type WorkStatusAction struct {
	Status string
	Label  string
	Class  string
}

func WorkStatusLabel(status string) string {
	switch status {
	case "in_progress":
		return "In progress"
	case "waiting":
		return "Waiting"
	case "done":
		return "Done"
	case "canceled":
		return "Canceled"
	default:
		return "Open"
	}
}

func WorkResponsibilityLabel(value string) string {
	switch value {
	case "agent":
		return "Agent work"
	case "shared":
		return "Shared"
	case "external":
		return "External"
	default:
		return "Owner work"
	}
}

func WorkKindLabel(kind string) string {
	if kind == "ticket" {
		return "Ticket"
	}
	return "To-do"
}

func WorkPriorityLabel(priority string) string {
	if priority == "normal" {
		return "Normal"
	}
	if priority == "" {
		return "Normal"
	}
	return strings.ToUpper(priority[:1]) + priority[1:]
}

func WorkSourceLabel(source string) string {
	switch source {
	case "persona":
		return "Agent"
	case "schedule":
		return "Schedule"
	case "system":
		return "System"
	default:
		return "Person"
	}
}

func WorkStatusActions(status string) []WorkStatusAction {
	switch status {
	case "open":
		return []WorkStatusAction{{"in_progress", "Start", "button-quiet"}, {"done", "Complete", "button-primary"}}
	case "in_progress":
		return []WorkStatusAction{{"waiting", "Mark waiting", "button-quiet"}, {"done", "Complete", "button-primary"}}
	case "waiting":
		return []WorkStatusAction{{"in_progress", "Resume", "button-quiet"}, {"done", "Complete", "button-primary"}}
	case "done":
		return []WorkStatusAction{{"open", "Reopen", "button-quiet"}}
	default:
		return nil
	}
}

func WorkQueueURL(filter WorkFilterView, status string) string {
	values := url.Values{}
	values.Set("status", status)
	if filter.Kind != "" && filter.Kind != "all" {
		values.Set("kind", filter.Kind)
	}
	if filter.Query != "" {
		values.Set("q", filter.Query)
	}
	return "/work?" + values.Encode()
}

type DocumentView struct {
	ID             string
	Name           string
	MediaType      string
	Status         string
	ChunkCount     int
	CharacterCount int
	UploadedBy     string
	Revision       int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type DocumentDetailView struct {
	DocumentView
	Content string
}

type DocumentOptionView struct {
	ID, Name, MediaType string
	Selected            bool
}

type EmailIntegrationView struct {
	Name           string
	EmailAddress   string
	DisplayName    string
	IMAPServer     string
	SMTPServer     string
	Status         string
	LastVerifiedAt *time.Time
	LastError      string
}

type EmailSettingsView struct {
	Name         string
	EmailAddress string
	DisplayName  string
	IMAPHost     string
	IMAPPort     int
	IMAPSecurity string
	IMAPUsername string
	SMTPHost     string
	SMTPPort     int
	SMTPSecurity string
	SMTPUsername string
}

func BlankEmailSettings() EmailSettingsView {
	return EmailSettingsView{Name: "Company inbox", IMAPPort: 993, IMAPSecurity: "tls", SMTPPort: 465, SMTPSecurity: "tls"}
}

type EmailInboxMessageView struct {
	UID     uint32
	From    string
	Subject string
	Date    time.Time
	Unread  bool
}

type EmailMessageView struct {
	EmailInboxMessageView
	To   string
	Body string
}

func EmailDate(value time.Time) string {
	if value.IsZero() {
		return "Unknown time"
	}
	return value.Local().Format("Jan 2, 2006 at 3:04 PM")
}

func DocumentTypeLabel(mediaType string) string {
	switch mediaType {
	case "text/markdown":
		return "Markdown"
	case "text/csv":
		return "CSV"
	case "text/tab-separated-values":
		return "TSV"
	case "application/json":
		return "JSON"
	case "application/xml":
		return "XML"
	case "text/html":
		return "HTML"
	case "text/yaml":
		return "YAML"
	default:
		return "Text"
	}
}

func DocumentCountLabel(count int) string {
	if count == 1 {
		return "1 document"
	}
	return fmt.Sprintf("%d documents", count)
}

func CharacterCountLabel(count int) string {
	if count == 1 {
		return "1 character"
	}
	return fmt.Sprintf("%d characters", count)
}

type BusinessProfileView struct {
	Template         string
	Variant          string
	Stage            string
	BusinessName     string
	WebsiteURL       string
	Trade            string
	Services         string
	ServiceArea      string
	TimeZone         string
	TeamSize         int
	CustomerMix      string
	WorkingHours     string
	EmergencyService bool
	CurrentSystems   []string
}

type OperatingPlaybookView struct {
	LeadIntake          string
	Scheduling          string
	EstimateToJob       string
	JobToInvoice        string
	Payments            string
	VendorBills         string
	BiggestBottleneck   string
	ImportantExceptions string
}

type PersonaBlueprintView struct {
	Key          string
	Group        string
	Name         string
	Role         string
	Mission      string
	Enabled      bool
	Capabilities []string
}

type PermissionPlanView struct {
	ReadBusinessRecords  bool
	ResearchPublicWeb    bool
	CommentOnDocuments   bool
	PrepareInvoiceDrafts bool
	DraftCustomerEmail   bool
	ReadEmailInbox       bool
	SendEmail            bool
	ProposeScheduleEdits bool
	ProposePayments      bool
}

type PersonaGroupView struct {
	Name        string
	Description string
	Personas    []PersonaBlueprintView
}

func PersonaGroups(personas []PersonaBlueprintView) []PersonaGroupView {
	return PersonaGroupsForTemplate(personas, "trades")
}

func PersonaGroupsForTemplate(personas []PersonaBlueprintView, template string) []PersonaGroupView {
	groups := []PersonaGroupView{
		{Name: "Core operations", Description: "The everyday office team. These three are selected as the practical starting point."},
		{Name: "Product and engineering", Description: "Specialists for product decisions, software delivery, technical operations, and reliability."},
		{Name: "Managed services", Description: "Specialists for service queues, SLAs, client technology plans, cloud operations, and recurring delivery."},
		{Name: "Growth and customers", Description: "Specialists for demand, lead development, and customer follow-through."},
		{Name: "Risk and people", Description: "Advisors who surface legal, compliance, employment, and safety questions for human review."},
		{Name: "Risk and reliability", Description: "Advisors who surface security, privacy, compliance, vendor, and operational risks for human review."},
		{Name: "Financial and supply", Description: "Specialists for job-cost visibility, purchasing, vendors, and payment preparation."},
	}
	if IsSoftwareTemplate(template) {
		groups[0].Description = "The cross-functional operating team. These three are selected as the practical starting point."
		groups[3].Description = "Specialists for acquisition, sales, customer research, positioning, and conversion."
	}
	for _, persona := range personas {
		matched := false
		for index := range groups {
			if groups[index].Name == persona.Group {
				groups[index].Personas = append(groups[index].Personas, persona)
				matched = true
				break
			}
		}
		if !matched {
			groups = append(groups, PersonaGroupView{Name: "Specialists", Personas: []PersonaBlueprintView{persona}})
		}
	}
	result := groups[:0]
	for _, group := range groups {
		if len(group.Personas) > 0 {
			result = append(result, group)
		}
	}
	return result
}

func PersonaGroupsForOnboarding(personas []PersonaBlueprintView, template, stage string) []PersonaGroupView {
	groups := PersonaGroupsForTemplate(personas, template)
	if !IsStartingBusiness(stage) {
		return groups
	}
	for index := range groups {
		switch groups[index].Name {
		case "Core operations":
			groups[index].Description = "Launch operators for sequencing setup, modeling the money, and designing the first repeatable routines."
		case "Product and engineering":
			groups[index].Description = "Specialists for validating the problem, scoping the first release, and choosing a credible delivery path."
		case "Growth and customers":
			groups[index].Description = "Specialists for market evidence, first-customer development, positioning, and launch demand."
		}
	}
	return groups
}

type OnboardingView struct {
	Template             string
	Status               string
	CurrentStep          int
	Business             BusinessProfileView
	Operations           OperatingPlaybookView
	Priorities           []string
	BoardroomName        string
	BoardroomDescription string
	Personas             []PersonaBlueprintView
	Permissions          PermissionPlanView
}

type OnboardingStepView struct {
	Number int
	Label  string
}

type BaselineView struct {
	ID                   string
	Status               string
	Phase                string
	CurrentQuestion      int
	QuestionCount        int
	CurrentPrompt        string
	CurrentExplanation   string
	Messages             []BaselineMessageView
	Facts                []BusinessFactView
	Sources              []BaselineSourceView
	Requirements         []EvidenceRequirementView
	Documents            []DocumentOptionView
	EmailFolders         []string
	InventoryCurrent     *EvidenceRequirementView
	InventoryAnswered    int
	InventoryTotal       int
	EvidenceScopeTitle   string
	EvidenceScopeSummary string
	PlanParentWorkItemID string
	NextReassessment     string
}

type BaselineMessageView struct {
	Role string
	Body string
}

type BusinessFactView struct {
	Key         string
	Label       string
	Value       string
	SourceLabel string
}

type BaselineSourceView struct {
	Type        string
	DisplayName string
	Status      string
	ScopeLabel  string
}

type GoogleDriveFolderView struct {
	ID   string
	Name string
}

type EvidenceRequirementView struct {
	ID             string
	Domain         string
	Label          string
	Rationale      string
	Status         string
	Disposition    string
	Responsibility string
	RenewalDue     string
	OwnerAnswer    string
	Interviewed    bool
	Evidence       []EvidenceLinkView
	Research       []BaselineResearchView
}

type BaselineResearchView struct {
	Query     string
	Status    string
	LastError string
	Results   []BaselineResearchResultView
}

type BaselineResearchResultView struct {
	Title       string
	URL         string
	Description string
	CitationID  string
}

type EvidenceLinkView struct {
	Label string
	URL   string
	Type  string
}

func OnboardingSteps() []OnboardingStepView {
	return []OnboardingStepView{
		{1, "Your business"},
		{2, "How work flows"},
		{3, "Priorities"},
		{4, "Your team"},
		{5, "Authority"},
		{6, "Review"},
	}
}

func PriorityOptions() []string {
	return PriorityOptionsForTemplate("trades")
}

func PriorityOptionsForTemplate(template string) []string {
	return PriorityOptionsForOnboarding(template, "operating")
}

func PriorityOptionsForOnboarding(template, stage string) []string {
	if IsStartingBusiness(stage) {
		if IsSoftwareTemplate(template) {
			return []string{
				"Validate the problem and ideal customer",
				"Define the offer, pricing, and revenue model",
				"Build the MVP and launch roadmap",
				"Design sales and first-customer onboarding",
				"Set up legal, security, privacy, and compliance",
				"Create the runway, budget, and vendor plan",
				"Build the website and go-to-market plan",
			}
		}
		return []string{
			"Validate demand and the ideal customer",
			"Define offers, pricing, and target margins",
			"Build a simple sales and follow-up process",
			"Set up licensing, insurance, legal, and compliance",
			"Create the launch budget and cash plan",
			"Choose the first operating systems",
			"Plan the website and launch marketing",
		}
	}
	if IsSoftwareTemplate(template) {
		return []string{
			"Improve customer onboarding and activation",
			"Reduce churn, contract, and renewal risk",
			"Ship the roadmap more predictably",
			"Improve SLA delivery and ticket flow",
			"Improve website and trial conversion",
			"Monitor reliability, security, and incident follow-up",
			"Track recurring revenue, contracts, and failed payments",
		}
	}
	return []string{
		"Get completed work invoiced faster",
		"Reduce scheduling mistakes",
		"Follow up on unpaid invoices",
		"Keep customers updated",
		"Track bills and upcoming payments",
		"Prepare paperwork for the accountant",
		"Catch jobs falling through the cracks",
	}
}

func IsStartingBusiness(stage string) bool {
	return stage == "starting"
}

func ChooseText(condition bool, whenTrue, whenFalse string) string {
	if condition {
		return whenTrue
	}
	return whenFalse
}

type ChoiceOptionView struct {
	Value string
	Label string
}

func CustomerMixOptions(template string) []ChoiceOptionView {
	if IsSoftwareTemplate(template) {
		return []ChoiceOptionView{{"b2b", "Mostly B2B"}, {"b2c", "Mostly B2C"}, {"channel", "Channel or partner-led"}, {"mixed", "A mix of these"}}
	}
	return []ChoiceOptionView{{"residential", "Mostly residential"}, {"commercial", "Mostly commercial"}, {"mixed", "A mix of both"}}
}

func ExistingSystemOptions(template string) []string {
	if IsSoftwareTemplate(template) {
		return []string{"Email", "Calendar", "Billing or PSA platform", "GitHub or GitLab", "Linear, Jira, or issue tracker", "CRM or help desk", "RMM or monitoring", "Product analytics", "Spreadsheets"}
	}
	return []string{"Email", "Calendar", "QuickBooks", "Job management software", "Paper or spreadsheets"}
}

func SuggestedFirstConversation(template, stage string) string {
	if IsStartingBusiness(stage) {
		if IsSoftwareTemplate(template) {
			return "Challenge my target customer, problem, offer, and launch assumptions. Separate facts from guesses, identify the three riskiest unknowns, and give me the smallest validation plan for the next two weeks."
		}
		return "Challenge my customer, service, pricing, startup-cost, and launch assumptions. Separate facts from guesses, identify the three riskiest unknowns, and give me the smallest validation plan for the next two weeks."
	}
	if IsSoftwareTemplate(template) {
		return "Review how customer requests become product or service-delivery work in my company. Find where priority, ownership, SLA, or follow-up can fall through the cracks and give me the first three actions to take."
	}
	return "Review how completed jobs become invoices in my business. Find the points where work could fall through the cracks and give me the first three actions to take."
}

func IsSoftwareTemplate(template string) bool {
	return template == "software" || template == "saas"
}

func Contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func PermissionSummary(plan PermissionPlanView) []string {
	var result []string
	if plan.ReadBusinessRecords {
		result = append(result, "Read connected business records")
	}
	if plan.ResearchPublicWeb {
		result = append(result, "Research the public web")
	}
	if plan.CommentOnDocuments {
		result = append(result, "Add review comments to documents")
	}
	if plan.PrepareInvoiceDrafts {
		result = append(result, "Prepare invoice drafts")
	}
	if plan.DraftCustomerEmail {
		result = append(result, "Draft customer messages")
	}
	if plan.ReadEmailInbox {
		result = append(result, "Read the connected email inbox")
	}
	if plan.SendEmail {
		result = append(result, "Send email through the connected SMTP account")
	}
	if plan.ProposeScheduleEdits {
		result = append(result, "Propose schedule changes")
	}
	if plan.ProposePayments {
		result = append(result, "Propose payments for approval")
	}
	return result
}

type BoardroomCardView struct {
	ID           string
	Name         string
	Description  string
	PersonaCount int
	Status       string
}

type PersonaView struct {
	ID                string
	Name              string
	Role              string
	Tools             []string
	CanCreateSubtasks bool
}

type AgentCardView struct {
	ID, Name, Role, Description, BoardroomName, Provider, Model, ReasoningEffort string
	Position, MaxTurns, ToolCount                                                int
	Enabled                                                                      bool
}

type AgentToolView struct {
	Capability, Label, Description, Conditions string
	Selected                                   bool
}

type AgentBoardroomOptionView struct {
	ID, Name string
	Selected bool
}

type AgentVersionView struct {
	Version   int
	Name      string
	Role      string
	CreatedAt time.Time
}

type AgentFormView struct {
	ID, Name, Role, Description, SystemInstructions string
	BoardroomID, BoardroomName                      string
	Position                                        int
	Enabled                                         bool
	Provider, Model, ReasoningEffort                string
	Temperature, TopP                               string
	ContextTokenLimit, MaxOutputTokens              int64
	TimeoutSeconds, MaxToolCalls                    int
	MaxCostMicros                                   int64
	ResponseStyle, CitationPolicy, ActionPolicy     string
	Tools                                           []AgentToolView
	Documents                                       []DocumentOptionView
	KnowledgeMode                                   string
	KnowledgeMaxResults                             int
	Boardrooms                                      []AgentBoardroomOptionView
	Versions                                        []AgentVersionView
	IsNew                                           bool
}

type RunView struct {
	ID          string
	Status      string
	Prompt      string
	TurnCount   int
	EventCursor int64
	CreatedAt   time.Time
	Error       string
}

type ConversationView struct {
	ID           string
	Title        string
	Source       string
	LatestStatus string
	LatestPrompt string
	MessageCount int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func RunIsTerminal(status string) bool {
	return status == "completed" || status == "failed" || status == "canceled"
}

func RunIsActive(status string) bool {
	return status == "pending" || status == "queued" || status == "preparing" || status == "running" || status == "awaiting_approval"
}

func RunStatusLabel(status string) string {
	switch status {
	case "completed":
		return "Ready"
	case "pending", "preparing", "running":
		return "Working"
	case "awaiting_approval":
		return "Needs approval"
	case "failed":
		return "Needs attention"
	case "canceled":
		return "Canceled"
	default:
		return status
	}
}

func ConversationSourceLabel(source string) string {
	if source == "schedule" {
		return "Scheduled"
	}
	return "Owner"
}

type MessageView struct {
	ID          string
	PersonaName string
	PersonaRole string
	Role        string
	Body        string
	Sequence    int64
	CreatedAt   time.Time
	Research    []ResearchActivityView
}

type ResearchActivityView struct {
	Tool    string
	Query   string
	URL     string
	Status  string
	Results []ResearchResultView
}

type ResearchResultView struct {
	Title   string
	URL     string
	Excerpt string
}

func ResearchActivityLabel(tool string) string {
	switch tool {
	case "documents.search":
		return "Searched documents"
	case "web.search":
		return "Searched the web"
	case "web.read":
		return "Read a source"
	default:
		return "Research"
	}
}

func ResearchURL(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return ""
	}
	return parsed.String()
}

type ScheduleView struct {
	ID            string
	Name          string
	BoardroomName string
	Prompt        string
	Timing        string
	TimeZone      string
	Paused        bool
	State         string
	LastError     string
}

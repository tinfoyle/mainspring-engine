package components

import (
	"fmt"
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

type DocumentView struct {
	ID             string
	Name           string
	MediaType      string
	Status         string
	ChunkCount     int
	CharacterCount int
	UploadedBy     string
	CreatedAt      time.Time
}

type DocumentDetailView struct {
	DocumentView
	Content string
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
		{Name: "Growth and customers", Description: "Specialists for demand, lead development, and customer follow-through."},
		{Name: "Risk and people", Description: "Advisors who surface legal, compliance, employment, and safety questions for human review."},
		{Name: "Risk and reliability", Description: "Advisors who surface security, privacy, compliance, vendor, and operational risks for human review."},
		{Name: "Financial and supply", Description: "Specialists for job-cost visibility, purchasing, vendors, and payment preparation."},
	}
	if template == "saas" {
		groups[0].Description = "The cross-functional operating team. These three are selected as the practical starting point."
		groups[2].Description = "Specialists for acquisition, sales, customer research, positioning, and conversion."
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
	if template == "saas" {
		return []string{
			"Improve onboarding and product activation",
			"Reduce churn and renewal risk",
			"Ship the roadmap more predictably",
			"Triage support and customer follow-up",
			"Improve website and trial conversion",
			"Monitor reliability and incident follow-up",
			"Track recurring revenue and failed payments",
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

type ChoiceOptionView struct {
	Value string
	Label string
}

func CustomerMixOptions(template string) []ChoiceOptionView {
	if template == "saas" {
		return []ChoiceOptionView{{"b2b", "Mostly B2B"}, {"b2c", "Mostly B2C"}, {"mixed", "A mix of both"}}
	}
	return []ChoiceOptionView{{"residential", "Mostly residential"}, {"commercial", "Mostly commercial"}, {"mixed", "A mix of both"}}
}

func ExistingSystemOptions(template string) []string {
	if template == "saas" {
		return []string{"Email", "Calendar", "Stripe or billing platform", "GitHub or GitLab", "Linear, Jira, or issue tracker", "CRM or help desk", "Product analytics", "Spreadsheets"}
	}
	return []string{"Email", "Calendar", "QuickBooks", "Job management software", "Paper or spreadsheets"}
}

func SuggestedFirstConversation(template string) string {
	if template == "saas" {
		return "Review how customer feedback becomes product work in my company. Find where priorities, ownership, or follow-up can fall through the cracks and give me the first three actions to take."
	}
	return "Review how completed jobs become invoices in my business. Find the points where work could fall through the cracks and give me the first three actions to take."
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
	Name  string
	Role  string
	Tools []string
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

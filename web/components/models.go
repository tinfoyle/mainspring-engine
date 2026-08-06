package components

import "time"

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

type BusinessProfileView struct {
	BusinessName     string
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
	Name         string
	Role         string
	Mission      string
	Enabled      bool
	Capabilities []string
}

type PermissionPlanView struct {
	ReadBusinessRecords  bool
	PrepareInvoiceDrafts bool
	DraftCustomerEmail   bool
	ProposeScheduleEdits bool
	ProposePayments      bool
}

type OnboardingView struct {
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
	if plan.PrepareInvoiceDrafts {
		result = append(result, "Prepare invoice drafts")
	}
	if plan.DraftCustomerEmail {
		result = append(result, "Draft customer messages")
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

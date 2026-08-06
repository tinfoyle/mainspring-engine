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

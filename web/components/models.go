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

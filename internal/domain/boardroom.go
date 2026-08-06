package domain

import "time"

type RunStatus string

const (
	RunPending          RunStatus = "pending"
	RunPreparing        RunStatus = "preparing"
	RunRunning          RunStatus = "running"
	RunAwaitingApproval RunStatus = "awaiting_approval"
	RunCompleted        RunStatus = "completed"
	RunFailed           RunStatus = "failed"
	RunCanceled         RunStatus = "canceled"
)

type MessageRole string

const (
	MessageUser   MessageRole = "user"
	MessageAgent  MessageRole = "agent"
	MessageSystem MessageRole = "system"
)

type Boardroom struct {
	ID          BoardroomID
	Name        string
	Description string
	MaxTurns    int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Persona struct {
	ID                 PersonaID
	BoardroomID        BoardroomID
	Name               string
	Role               string
	SystemInstructions string
	Position           int
	Enabled            bool
}

type BoardroomRun struct {
	ID          RunID
	BoardroomID BoardroomID
	Status      RunStatus
	Prompt      string
	TurnCount   int
	StartedAt   *time.Time
	CompletedAt *time.Time
	CreatedAt   time.Time
}

type Message struct {
	ID        uuidString
	RunID     RunID
	PersonaID *PersonaID
	Role      MessageRole
	Body      string
	Sequence  int64
	CreatedAt time.Time
}

// uuidString avoids leaking a provider-specific identifier into message semantics.
type uuidString string

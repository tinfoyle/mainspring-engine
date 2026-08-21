// Package work owns the authoritative, customer-visible lifecycle of work.
// Workflow engines may coordinate a WorkItem, but they never infer or replace
// its state.
package work

import (
	"errors"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const MaxDepth uint8 = 3

type Kind string
type State string
type Priority string
type Responsibility string
type Source string
type ActorKind string
type ProvenanceLinkKind string

const (
	KindTodo   Kind = "todo"
	KindTicket Kind = "ticket"

	StateOpen       State = "open"
	StateInProgress State = "in_progress"
	StateWaiting    State = "waiting"
	StateDone       State = "done"
	StateCanceled   State = "canceled"

	PriorityLow    Priority = "low"
	PriorityNormal Priority = "normal"
	PriorityHigh   Priority = "high"
	PriorityUrgent Priority = "urgent"

	ResponsibilityUser     Responsibility = "user"
	ResponsibilityPersona  Responsibility = "persona"
	ResponsibilityShared   Responsibility = "shared"
	ResponsibilityExternal Responsibility = "external"

	SourceManual       Source = "manual"
	SourceBaseline     Source = "baseline"
	SourceSchedule     Source = "schedule"
	SourceConversation Source = "conversation"
	SourceRun          Source = "run"
	SourceSystem       Source = "system"

	ActorUser     ActorKind = "user"
	ActorWorkload ActorKind = "workload"

	ProvenanceBaselineRequirement ProvenanceLinkKind = "baseline_requirement"
	ProvenanceSchedule            ProvenanceLinkKind = "schedule"
	ProvenanceRun                 ProvenanceLinkKind = "run"
)

var (
	ErrInvalid        = errors.New("work item is invalid")
	ErrTransition     = errors.New("work item transition is not allowed")
	ErrRole           = errors.New("role cannot perform the work transition")
	ErrReasonRequired = errors.New("work transition reason is required")
	ErrLinkExists     = errors.New("work provenance link already exists")
)

type Actor struct {
	Kind ActorKind
	ID   string
}

func (a Actor) Valid() bool {
	return (a.Kind == ActorUser || a.Kind == ActorWorkload) && strings.TrimSpace(a.ID) != ""
}

type Assignment struct {
	Responsibility Responsibility
	UserID         ids.UserID
	PersonaID      string
	ExternalRef    string
}

func (a Assignment) Valid() bool {
	switch a.Responsibility {
	case ResponsibilityUser:
		return a.UserID != "" && a.PersonaID == "" && strings.TrimSpace(a.ExternalRef) == ""
	case ResponsibilityPersona:
		return a.UserID == "" && validOptionalID(a.PersonaID) && strings.TrimSpace(a.ExternalRef) == ""
	case ResponsibilityShared:
		return a.UserID == "" && a.PersonaID == "" && strings.TrimSpace(a.ExternalRef) == ""
	case ResponsibilityExternal:
		return a.UserID == "" && a.PersonaID == "" && len(strings.TrimSpace(a.ExternalRef)) >= 2 && len(strings.TrimSpace(a.ExternalRef)) <= 200
	default:
		return false
	}
}

type Provenance struct {
	Source                Source
	CreatedBy             Actor
	BaselineRequirementID string
	ScheduleID            string
	ConversationID        string
	RunID                 string
}

func (p Provenance) Valid() bool {
	if !p.CreatedBy.Valid() {
		return false
	}
	values := []string{p.BaselineRequirementID, p.ScheduleID, p.ConversationID, p.RunID}
	for _, value := range values {
		if value != "" && !validOptionalID(value) {
			return false
		}
	}
	switch p.Source {
	case SourceManual, SourceSystem:
		return true
	case SourceBaseline:
		return p.BaselineRequirementID != ""
	case SourceSchedule:
		return p.ScheduleID != ""
	case SourceConversation:
		return p.ConversationID != ""
	case SourceRun:
		return p.RunID != ""
	default:
		return false
	}
}

type Draft struct {
	ID                    ids.WorkItemID
	AccountID             ids.AccountID
	ParentID              ids.WorkItemID
	Kind                  Kind
	Title                 string
	Description           string
	Priority              Priority
	Assignment            Assignment
	Provenance            Provenance
	DueAt                 *time.Time
	CapacityReservationID string
}

func NewDraft(draft Draft) (Draft, error) {
	draft.Title = strings.TrimSpace(draft.Title)
	draft.Description = strings.TrimSpace(draft.Description)
	draft.Assignment.ExternalRef = strings.TrimSpace(draft.Assignment.ExternalRef)
	if draft.ID == "" || ids.Validate(string(draft.ID)) != nil || draft.AccountID == "" || ids.Validate(string(draft.AccountID)) != nil ||
		(draft.ParentID != "" && ids.Validate(string(draft.ParentID)) != nil) || draft.ParentID == draft.ID ||
		(draft.Kind != KindTodo && draft.Kind != KindTicket) || len(draft.Title) < 2 || len(draft.Title) > 240 || len(draft.Description) > 20_000 ||
		!draft.Priority.Valid() || !draft.Assignment.Valid() || !draft.Provenance.Valid() ||
		(draft.CapacityReservationID != "" && ids.Validate(draft.CapacityReservationID) != nil) {
		return Draft{}, ErrInvalid
	}
	if draft.DueAt != nil {
		value := draft.DueAt.UTC()
		draft.DueAt = &value
	}
	return draft, nil
}

type Item struct {
	ID                    ids.WorkItemID
	AccountID             ids.AccountID
	Number                uint64
	ParentID              ids.WorkItemID
	Depth                 uint8
	Kind                  Kind
	Title                 string
	Description           string
	State                 State
	Priority              Priority
	Assignment            Assignment
	Provenance            Provenance
	DueAt                 *time.Time
	CompletedAt           *time.Time
	CapacityReservationID string
	CapacityReleasedAt    *time.Time
	Version               uint64
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

func Materialize(draft Draft, number uint64, depth uint8, now time.Time) (Item, error) {
	valid, err := NewDraft(draft)
	if err != nil || number == 0 || depth > MaxDepth || now.IsZero() {
		return Item{}, ErrInvalid
	}
	now = now.UTC()
	return Item{ID: valid.ID, AccountID: valid.AccountID, Number: number, ParentID: valid.ParentID, Depth: depth, Kind: valid.Kind, Title: valid.Title, Description: valid.Description, State: StateOpen, Priority: valid.Priority, Assignment: valid.Assignment, Provenance: valid.Provenance, DueAt: valid.DueAt, CapacityReservationID: valid.CapacityReservationID, Version: 1, CreatedAt: now, UpdatedAt: now}, nil
}

// Restore is the sole persistence reconstitution boundary. It rejects corrupt
// rows rather than letting invalid state leak above an adapter.
func Restore(item Item) (Item, error) {
	draft, err := NewDraft(Draft{ID: item.ID, AccountID: item.AccountID, ParentID: item.ParentID, Kind: item.Kind, Title: item.Title, Description: item.Description, Priority: item.Priority, Assignment: item.Assignment, Provenance: item.Provenance, DueAt: item.DueAt, CapacityReservationID: item.CapacityReservationID})
	if err != nil || item.Number == 0 || item.Depth > MaxDepth || !item.State.Valid() || item.Version == 0 || item.CreatedAt.IsZero() || item.UpdatedAt.IsZero() || item.UpdatedAt.Before(item.CreatedAt) {
		return Item{}, ErrInvalid
	}
	if item.State == StateDone {
		if item.CompletedAt == nil {
			return Item{}, ErrInvalid
		}
	} else if item.CompletedAt != nil {
		return Item{}, ErrInvalid
	}
	item.Title, item.Description, item.Assignment, item.DueAt = draft.Title, draft.Description, draft.Assignment, draft.DueAt
	item.CreatedAt, item.UpdatedAt = item.CreatedAt.UTC(), item.UpdatedAt.UTC()
	if item.CompletedAt != nil {
		value := item.CompletedAt.UTC()
		item.CompletedAt = &value
	}
	if item.CapacityReleasedAt != nil {
		value := item.CapacityReleasedAt.UTC()
		item.CapacityReleasedAt = &value
	}
	return item, nil
}

type TransitionCommand struct {
	To              State
	Role            accounts.MembershipRole
	Actor           Actor
	Reason          string
	ExpectedVersion uint64
	At              time.Time
}

func (i Item) Transition(command TransitionCommand) (Item, error) {
	if !command.Actor.Valid() || command.ExpectedVersion != i.Version || command.At.IsZero() || !allowedTransition(i.State, command.To) {
		return Item{}, ErrTransition
	}
	if !roleCanTransition(command.Role, i.State, command.To) {
		return Item{}, ErrRole
	}
	reason := strings.TrimSpace(command.Reason)
	if (command.To == StateWaiting || command.To == StateCanceled || i.State == StateDone) && (len(reason) < 3 || len(reason) > 1000) {
		return Item{}, ErrReasonRequired
	}
	result := i
	result.State = command.To
	result.Version++
	result.UpdatedAt = command.At.UTC()
	result.CompletedAt = nil
	if command.To == StateDone {
		completed := command.At.UTC()
		result.CompletedAt = &completed
	}
	return result, nil
}

type AssignmentCommand struct {
	Assignment      Assignment
	Role            accounts.MembershipRole
	Actor           Actor
	ExpectedVersion uint64
	At              time.Time
}

func (i Item) Assign(command AssignmentCommand) (Item, error) {
	if !command.Actor.Valid() || !command.Assignment.Valid() || command.ExpectedVersion != i.Version || command.At.IsZero() || i.State.Terminal() {
		return Item{}, ErrInvalid
	}
	if !roleCanEdit(command.Role) {
		return Item{}, ErrRole
	}
	result := i
	result.Assignment = command.Assignment
	result.Version++
	result.UpdatedAt = command.At.UTC()
	return result, nil
}

type ProvenanceLinkCommand struct {
	Kind            ProvenanceLinkKind
	ReferenceID     string
	Role            accounts.MembershipRole
	Actor           Actor
	ExpectedVersion uint64
	At              time.Time
}

// AttachProvenance adds one immutable reference without changing the Work
// item's historical creation source or actor.
func (i Item) AttachProvenance(command ProvenanceLinkCommand) (Item, error) {
	if !command.Actor.Valid() || ids.Validate(command.ReferenceID) != nil || command.ExpectedVersion != i.Version || command.At.IsZero() {
		return Item{}, ErrInvalid
	}
	if !roleCanEdit(command.Role) {
		return Item{}, ErrRole
	}
	result := i
	switch command.Kind {
	case ProvenanceBaselineRequirement:
		if result.Provenance.BaselineRequirementID != "" {
			return Item{}, ErrLinkExists
		}
		result.Provenance.BaselineRequirementID = command.ReferenceID
	case ProvenanceSchedule:
		if result.Provenance.ScheduleID != "" {
			return Item{}, ErrLinkExists
		}
		result.Provenance.ScheduleID = command.ReferenceID
	case ProvenanceRun:
		if result.Provenance.RunID != "" {
			return Item{}, ErrLinkExists
		}
		result.Provenance.RunID = command.ReferenceID
	default:
		return Item{}, ErrInvalid
	}
	result.Version++
	result.UpdatedAt = command.At.UTC()
	return result, nil
}

type ConversationLinkCommand struct {
	ConversationID  string
	Role            accounts.MembershipRole
	Actor           Actor
	ExpectedVersion uint64
	At              time.Time
}

// LinkConversation adds the sole Account-owned Conversation reference. A
// different Conversation cannot silently replace the historical link.
func (i Item) LinkConversation(command ConversationLinkCommand) (Item, error) {
	if !command.Actor.Valid() || ids.Validate(command.ConversationID) != nil || command.ExpectedVersion != i.Version || command.At.IsZero() {
		return Item{}, ErrInvalid
	}
	if !roleCanEdit(command.Role) {
		return Item{}, ErrRole
	}
	if i.Provenance.ConversationID != "" {
		return Item{}, ErrLinkExists
	}
	result := i
	result.Provenance.ConversationID = command.ConversationID
	result.Version++
	result.UpdatedAt = command.At.UTC()
	return result, nil
}

func roleCanEdit(role accounts.MembershipRole) bool {
	return role == accounts.RoleOwner || role == accounts.RoleAdministrator || role == accounts.RoleMember
}

// WithReopenedCapacity binds a new admission reservation before a completed
// item is persisted as open again.
func (i Item) WithReopenedCapacity(requestID string) (Item, error) {
	if i.State != StateOpen || i.CompletedAt != nil || ids.Validate(requestID) != nil {
		return Item{}, ErrInvalid
	}
	i.CapacityReservationID = requestID
	i.CapacityReleasedAt = nil
	return i, nil
}

func (p Priority) Valid() bool {
	return p == PriorityLow || p == PriorityNormal || p == PriorityHigh || p == PriorityUrgent
}
func (k Kind) Valid() bool { return k == KindTodo || k == KindTicket }
func (s State) Valid() bool {
	return s == StateOpen || s == StateInProgress || s == StateWaiting || s == StateDone || s == StateCanceled
}
func (s State) Terminal() bool { return s == StateDone || s == StateCanceled }

func allowedTransition(from, to State) bool {
	switch from {
	case StateOpen:
		return to == StateInProgress || to == StateCanceled
	case StateInProgress:
		return to == StateWaiting || to == StateDone || to == StateCanceled
	case StateWaiting:
		return to == StateInProgress || to == StateCanceled
	case StateDone:
		return to == StateOpen
	default:
		return false
	}
}

func roleCanTransition(role accounts.MembershipRole, from, to State) bool {
	if role == accounts.RoleOwner || role == accounts.RoleAdministrator {
		return true
	}
	if role != accounts.RoleMember {
		return false
	}
	return to != StateCanceled && from != StateDone
}

func validOptionalID(value string) bool { return ids.Validate(value) == nil }

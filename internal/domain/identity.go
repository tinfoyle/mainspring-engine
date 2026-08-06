package domain

import (
	"fmt"

	"github.com/google/uuid"
)

type TenantID uuid.UUID
type BoardroomID uuid.UUID
type PersonaID uuid.UUID
type RunID uuid.UUID
type InvocationID uuid.UUID
type ActionID uuid.UUID

func NewTenantID() TenantID         { return TenantID(uuid.New()) }
func NewBoardroomID() BoardroomID   { return BoardroomID(uuid.New()) }
func NewPersonaID() PersonaID       { return PersonaID(uuid.New()) }
func NewRunID() RunID               { return RunID(uuid.New()) }
func NewInvocationID() InvocationID { return InvocationID(uuid.New()) }
func NewActionID() ActionID         { return ActionID(uuid.New()) }

func ParseTenantID(value string) (TenantID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return TenantID{}, fmt.Errorf("parse tenant id: %w", err)
	}
	return TenantID(id), nil
}

func ParseBoardroomID(value string) (BoardroomID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return BoardroomID{}, fmt.Errorf("parse boardroom id: %w", err)
	}
	return BoardroomID(id), nil
}

func ParsePersonaID(value string) (PersonaID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return PersonaID{}, fmt.Errorf("parse persona id: %w", err)
	}
	return PersonaID(id), nil
}

func ParseRunID(value string) (RunID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return RunID{}, fmt.Errorf("parse run id: %w", err)
	}
	return RunID(id), nil
}

func ParseInvocationID(value string) (InvocationID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return InvocationID{}, fmt.Errorf("parse invocation id: %w", err)
	}
	return InvocationID(id), nil
}

func ParseActionID(value string) (ActionID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return ActionID{}, fmt.Errorf("parse action id: %w", err)
	}
	return ActionID(id), nil
}

func (id TenantID) String() string     { return uuid.UUID(id).String() }
func (id BoardroomID) String() string  { return uuid.UUID(id).String() }
func (id PersonaID) String() string    { return uuid.UUID(id).String() }
func (id RunID) String() string        { return uuid.UUID(id).String() }
func (id InvocationID) String() string { return uuid.UUID(id).String() }
func (id ActionID) String() string     { return uuid.UUID(id).String() }

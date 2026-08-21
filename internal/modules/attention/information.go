package attention

import (
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type InformationScope string
type InformationRequestState string

const (
	InformationScopeAccount      InformationScope = "account"
	InformationScopeWorkItem     InformationScope = "work_item"
	InformationScopeConversation InformationScope = "conversation"

	InformationRequestOpen     InformationRequestState = "open"
	InformationRequestAnswered InformationRequestState = "answered"
	InformationRequestCanceled InformationRequestState = "canceled"
)

func (state InformationRequestState) Valid() bool {
	return state == InformationRequestOpen || state == InformationRequestAnswered || state == InformationRequestCanceled
}

type FactRequirement struct {
	Key     string
	Scope   InformationScope
	ScopeID string
}

func (requirement FactRequirement) Valid() bool {
	if !validCode(requirement.Key) {
		return false
	}
	switch requirement.Scope {
	case InformationScopeAccount:
		return requirement.ScopeID == ""
	case InformationScopeWorkItem, InformationScopeConversation:
		return validID(requirement.ScopeID)
	default:
		return false
	}
}

type FactReference struct {
	ID          string
	Version     uint64
	Requirement FactRequirement
}

func (reference FactReference) Valid() bool {
	return validID(reference.ID) && reference.Version > 0 && reference.Requirement.Valid()
}

type InformationAnswer struct {
	Fact       FactReference
	AnsweredBy Actor
	AnsweredAt time.Time
}

type InformationRequestDraft struct {
	ID               ids.InformationRequestID
	AccountID        ids.AccountID
	ParentWorkItemID ids.WorkItemID
	Requirement      FactRequirement
	Question         string
	RequestedBy      Actor
}

type InformationRequest struct {
	InformationRequestDraft
	State        InformationRequestState
	AnswerRecord *InformationAnswer
	CanceledBy   *Actor
	Reason       string
	Version      uint64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func NewInformationRequest(draft InformationRequestDraft, now time.Time) (InformationRequest, error) {
	draft.Question = strings.TrimSpace(draft.Question)
	request := InformationRequest{InformationRequestDraft: draft, State: InformationRequestOpen, Version: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
	return RestoreInformationRequest(request)
}

func RestoreInformationRequest(request InformationRequest) (InformationRequest, error) {
	request.Question = strings.TrimSpace(request.Question)
	request.Reason = strings.TrimSpace(request.Reason)
	request.CreatedAt, request.UpdatedAt = request.CreatedAt.UTC(), request.UpdatedAt.UTC()
	if !validID(string(request.ID)) || !validID(string(request.AccountID)) || !validID(string(request.ParentWorkItemID)) ||
		!request.Requirement.Valid() || len(request.Question) < 3 || len(request.Question) > 4000 || !request.RequestedBy.Valid() ||
		request.Version == 0 || request.CreatedAt.IsZero() || request.UpdatedAt.Before(request.CreatedAt) {
		return InformationRequest{}, ErrInvalid
	}
	switch request.State {
	case InformationRequestOpen:
		if request.AnswerRecord != nil || request.CanceledBy != nil || request.Reason != "" {
			return InformationRequest{}, ErrInvalid
		}
	case InformationRequestAnswered:
		if request.AnswerRecord == nil || !request.AnswerRecord.Fact.Valid() || !request.AnswerRecord.AnsweredBy.Valid() || request.AnswerRecord.AnsweredAt.IsZero() || request.AnswerRecord.AnsweredAt.Before(request.CreatedAt) ||
			request.AnswerRecord.Fact.Requirement != request.Requirement || request.CanceledBy != nil || request.Reason != "" {
			return InformationRequest{}, ErrInvalid
		}
		answer := *request.AnswerRecord
		answer.AnsweredAt = answer.AnsweredAt.UTC()
		request.AnswerRecord = &answer
	case InformationRequestCanceled:
		if request.AnswerRecord != nil || request.CanceledBy == nil || !request.CanceledBy.Valid() || !validReason(request.Reason) {
			return InformationRequest{}, ErrInvalid
		}
	default:
		return InformationRequest{}, ErrInvalid
	}
	return request, nil
}

func (request InformationRequest) EligibleFor(fact FactReference) bool {
	return request.State == InformationRequestOpen && fact.Valid() && fact.Requirement == request.Requirement
}

type AnswerInformationCommand struct {
	Fact            FactReference
	Role            accounts.MembershipRole
	Actor           Actor
	ExpectedVersion uint64
	At              time.Time
}

func (request InformationRequest) Answer(command AnswerInformationCommand) (InformationRequest, error) {
	if command.ExpectedVersion != request.Version {
		return InformationRequest{}, ErrConflict
	}
	if request.State != InformationRequestOpen || !command.Actor.Valid() || !command.Fact.Valid() || command.At.IsZero() {
		return InformationRequest{}, ErrState
	}
	if !canParticipate(command.Role) {
		return InformationRequest{}, ErrRole
	}
	if command.Fact.Requirement != request.Requirement {
		return InformationRequest{}, ErrRequirementMismatch
	}
	result := request
	result.State = InformationRequestAnswered
	result.AnswerRecord = &InformationAnswer{Fact: command.Fact, AnsweredBy: command.Actor, AnsweredAt: command.At.UTC()}
	result.Version++
	result.UpdatedAt = command.At.UTC()
	return RestoreInformationRequest(result)
}

type CancelInformationCommand struct {
	Role            accounts.MembershipRole
	Actor           Actor
	Reason          string
	ExpectedVersion uint64
	At              time.Time
}

func (request InformationRequest) Cancel(command CancelInformationCommand) (InformationRequest, error) {
	if command.ExpectedVersion != request.Version {
		return InformationRequest{}, ErrConflict
	}
	if request.State != InformationRequestOpen || !command.Actor.Valid() || command.At.IsZero() {
		return InformationRequest{}, ErrState
	}
	if !canManage(command.Role) && !(canParticipate(command.Role) && sameActor(command.Actor, request.RequestedBy)) {
		return InformationRequest{}, ErrRole
	}
	if !validReason(command.Reason) {
		return InformationRequest{}, ErrReasonRequired
	}
	actor := command.Actor
	result := request
	result.State = InformationRequestCanceled
	result.CanceledBy = &actor
	result.Reason = strings.TrimSpace(command.Reason)
	result.Version++
	result.UpdatedAt = command.At.UTC()
	return RestoreInformationRequest(result)
}

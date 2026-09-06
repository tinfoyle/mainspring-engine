package scheduling

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/scheduling"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const PackageCode catalog.PackageCode = "agents"

type Authorizer interface {
	Authorize(context.Context, access.Actor, ids.AccountID, access.Requirement) (access.AccountContext, error)
}

type Clock interface{ Now() time.Time }

type Service struct {
	authorizer Authorizer
	store      Store
	clock      Clock
}

func New(authorizer Authorizer, store Store, clock Clock) (*Service, error) {
	if authorizer == nil || store == nil || clock == nil {
		return nil, errors.New("Scheduling dependencies are required")
	}
	return &Service{authorizer: authorizer, store: store, clock: clock}, nil
}

type CreateCommand struct {
	Actor           access.Actor
	AccountID       ids.AccountID
	RequestID       string
	Name            string
	Timezone        string
	Recurrence      domain.Recurrence
	MissedRunPolicy domain.MissedRunPolicy
	Template        domain.AgentRunTemplate
	Reason          string
}

func (service *Service) Create(ctx context.Context, command CreateCommand) (domain.Schedule, bool, error) {
	if err := service.authorize(ctx, command.Actor, command.AccountID, true); err != nil {
		return domain.Schedule{}, false, err
	}
	if ids.Validate(command.RequestID) != nil {
		return domain.Schedule{}, false, ErrInvalid
	}
	now := service.clock.Now().UTC()
	value, err := domain.New(domain.Draft{ID: ids.ScheduleID(command.RequestID), AccountID: command.AccountID, Name: command.Name,
		Timezone: command.Timezone, Recurrence: command.Recurrence, MissedRunPolicy: command.MissedRunPolicy,
		Template: command.Template, CreatedBy: command.Actor.UserID, CreatedAt: now})
	if err != nil {
		return domain.Schedule{}, false, ErrInvalid
	}
	return service.store.Create(ctx, value, mutation(command.RequestID, "created", command.Actor.UserID, command.Reason, now))
}

type GetQuery struct {
	Actor      access.Actor
	AccountID  ids.AccountID
	ScheduleID ids.ScheduleID
}

func (service *Service) Get(ctx context.Context, query GetQuery) (domain.Schedule, error) {
	if err := service.authorize(ctx, query.Actor, query.AccountID, false); err != nil || ids.Validate(string(query.ScheduleID)) != nil {
		if err != nil {
			return domain.Schedule{}, err
		}
		return domain.Schedule{}, ErrInvalid
	}
	value, err := service.store.Get(ctx, query.AccountID, query.ScheduleID)
	if err == nil && value.State == domain.StateDeleted {
		return domain.Schedule{}, ErrNotFound
	}
	return value, err
}

type ListCommand struct {
	Actor     access.Actor
	AccountID ids.AccountID
	Query     ListQuery
}

func (service *Service) List(ctx context.Context, command ListCommand) (Page, error) {
	if err := service.authorize(ctx, command.Actor, command.AccountID, false); err != nil {
		return Page{}, err
	}
	return service.store.List(ctx, command.AccountID, command.Query)
}

type ReviseCommand struct {
	Actor           access.Actor
	AccountID       ids.AccountID
	ScheduleID      ids.ScheduleID
	RequestID       string
	ExpectedVersion uint64
	Name            string
	Timezone        string
	Recurrence      domain.Recurrence
	MissedRunPolicy domain.MissedRunPolicy
	Template        domain.AgentRunTemplate
	Reason          string
}

func (service *Service) Revise(ctx context.Context, command ReviseCommand) (domain.Schedule, error) {
	if err := service.authorize(ctx, command.Actor, command.AccountID, true); err != nil {
		return domain.Schedule{}, err
	}
	current, err := service.loadMutation(ctx, command.AccountID, command.ScheduleID, command.RequestID, command.ExpectedVersion)
	if err != nil {
		return domain.Schedule{}, err
	}
	now := service.clock.Now().UTC()
	if command.Template.EmailSelf && current.CreatedBy != command.Actor.UserID {
		return domain.Schedule{}, ErrInvalid
	}
	if current.State == domain.StateDeleted {
		return domain.Schedule{}, ErrNotFound
	}
	if current.Version == command.ExpectedVersion+1 {
		if !matchesRevision(current, command) {
			return domain.Schedule{}, ErrConflict
		}
		replay := current
		replay.UpdatedAt = now
		return service.store.Update(ctx, replay, command.ExpectedVersion, mutation(command.RequestID, "updated", command.Actor.UserID, command.Reason, now))
	}
	if current.Version != command.ExpectedVersion {
		return domain.Schedule{}, ErrConflict
	}
	value, err := current.Revise(command.ExpectedVersion, domain.Revision{Name: command.Name, Timezone: command.Timezone,
		Recurrence: command.Recurrence, MissedRunPolicy: command.MissedRunPolicy, Template: command.Template}, now)
	if err != nil {
		return domain.Schedule{}, mapDomainError(err)
	}
	return service.store.Update(ctx, value, command.ExpectedVersion, mutation(command.RequestID, "updated", command.Actor.UserID, command.Reason, now))
}

type TransitionCommand struct {
	Actor           access.Actor
	AccountID       ids.AccountID
	ScheduleID      ids.ScheduleID
	RequestID       string
	ExpectedVersion uint64
	Reason          string
}

func (service *Service) Pause(ctx context.Context, command TransitionCommand) (domain.Schedule, error) {
	return service.transition(ctx, command, "paused")
}

func (service *Service) Resume(ctx context.Context, command TransitionCommand) (domain.Schedule, error) {
	return service.transition(ctx, command, "resumed")
}

func (service *Service) Delete(ctx context.Context, command TransitionCommand) (domain.Schedule, error) {
	return service.transition(ctx, command, "deleted")
}

func (service *Service) TriggerNow(ctx context.Context, command TransitionCommand) (Trigger, bool, error) {
	if err := service.authorize(ctx, command.Actor, command.AccountID, true); err != nil {
		return Trigger{}, false, err
	}
	if ids.Validate(string(command.ScheduleID)) != nil || ids.Validate(command.RequestID) != nil || command.ExpectedVersion == 0 {
		return Trigger{}, false, ErrInvalid
	}
	current, err := service.store.Get(ctx, command.AccountID, command.ScheduleID)
	if err != nil {
		return Trigger{}, false, err
	}
	if current.Template.EmailSelf && current.CreatedBy != command.Actor.UserID {
		return Trigger{}, false, ErrInvalid
	}
	now := service.clock.Now().UTC()
	request := TriggerRequest{ID: command.RequestID, AccountID: command.AccountID, ScheduleID: command.ScheduleID,
		ScheduleVersion: command.ExpectedVersion, RequestedBy: command.Actor.UserID, RequestedAt: now}
	return service.store.EnqueueTrigger(ctx, request, mutation(command.RequestID, "trigger_requested", command.Actor.UserID, command.Reason, now))
}

func (service *Service) transition(ctx context.Context, command TransitionCommand, kind string) (domain.Schedule, error) {
	if err := service.authorize(ctx, command.Actor, command.AccountID, true); err != nil {
		return domain.Schedule{}, err
	}
	current, err := service.loadMutation(ctx, command.AccountID, command.ScheduleID, command.RequestID, command.ExpectedVersion)
	if err != nil {
		return domain.Schedule{}, err
	}
	now := service.clock.Now().UTC()
	if kind == "resumed" && current.Template.EmailSelf && current.CreatedBy != command.Actor.UserID {
		return domain.Schedule{}, ErrInvalid
	}
	desiredState := map[string]domain.State{"paused": domain.StatePaused, "resumed": domain.StateActive, "deleted": domain.StateDeleted}[kind]
	if current.Version == command.ExpectedVersion+1 {
		if desiredState == "" || current.State != desiredState {
			return domain.Schedule{}, ErrConflict
		}
		replay := current
		replay.UpdatedAt = now
		return service.store.Update(ctx, replay, command.ExpectedVersion, mutation(command.RequestID, kind, command.Actor.UserID, command.Reason, now))
	}
	if current.Version != command.ExpectedVersion {
		return domain.Schedule{}, ErrConflict
	}
	if current.State == domain.StateDeleted {
		return domain.Schedule{}, ErrNotFound
	}
	if (kind == "paused" && current.State == domain.StatePaused) || (kind == "resumed" && current.State == domain.StateActive) {
		return current, nil
	}
	var value domain.Schedule
	switch kind {
	case "paused":
		value, err = current.Pause(command.ExpectedVersion, now)
	case "resumed":
		value, err = current.Resume(command.ExpectedVersion, now)
	case "deleted":
		value, err = current.Delete(command.ExpectedVersion, now)
	default:
		err = domain.ErrInvalidSchedule
	}
	if err != nil {
		return domain.Schedule{}, mapDomainError(err)
	}
	return service.store.Update(ctx, value, command.ExpectedVersion, mutation(command.RequestID, kind, command.Actor.UserID, command.Reason, now))
}

func (service *Service) loadMutation(ctx context.Context, accountID ids.AccountID, scheduleID ids.ScheduleID, requestID string, expected uint64) (domain.Schedule, error) {
	if ids.Validate(string(scheduleID)) != nil || ids.Validate(requestID) != nil || expected == 0 {
		return domain.Schedule{}, ErrInvalid
	}
	value, err := service.store.Get(ctx, accountID, scheduleID)
	if err != nil {
		return domain.Schedule{}, err
	}
	return value, nil
}

func matchesRevision(value domain.Schedule, command ReviseCommand) bool {
	recurrence, err := domain.NewRecurrence(command.Recurrence)
	if err != nil {
		return false
	}
	return value.Name == strings.TrimSpace(command.Name) && value.Timezone == command.Timezone &&
		value.MissedRunPolicy == command.MissedRunPolicy && value.Recurrence.Frequency == recurrence.Frequency &&
		value.Recurrence.LocalHour == recurrence.LocalHour && value.Recurrence.LocalMinute == recurrence.LocalMinute &&
		value.Recurrence.GapPolicy == recurrence.GapPolicy && value.Recurrence.OverlapPolicy == recurrence.OverlapPolicy &&
		sameWeekdays(value.Recurrence.Weekdays, recurrence.Weekdays) && sameTemplate(value.Template, command.Template)
}

func sameWeekdays(left, right []time.Weekday) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func sameTemplate(left, right domain.AgentRunTemplate) bool {
	normalized, err := domain.NewAgentRunTemplate(right)
	if err != nil {
		return false
	}
	return left.BoardroomID == normalized.BoardroomID && left.Mode == normalized.Mode && left.Subject == normalized.Subject && left.Prompt == normalized.Prompt &&
		sameIDs(left.PersonaIDs, normalized.PersonaIDs) && sameIDs(left.WorkItemIDs, normalized.WorkItemIDs) &&
		sameIDs(left.KnowledgeFactIDs, normalized.KnowledgeFactIDs) && sameIDs(left.KnowledgeDocumentIDs, normalized.KnowledgeDocumentIDs) &&
		sameIDs(left.BaselineAssessmentIDs, normalized.BaselineAssessmentIDs) && left.EmailSelf == normalized.EmailSelf && sameIDs(left.SourceURLs, normalized.SourceURLs)
}

func sameIDs[T ~string](left, right []T) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (service *Service) authorize(ctx context.Context, actor access.Actor, accountID ids.AccountID, mutation bool) error {
	if !actor.Valid() || (mutation && actor.UserID == "") || ids.Validate(string(accountID)) != nil {
		return ErrInvalid
	}
	requirement := access.Requirement{Package: PackageCode, Mutation: mutation}
	if mutation {
		requirement.Roles = []accounts.MembershipRole{accounts.RoleOwner, accounts.RoleAdministrator, accounts.RoleMember}
	}
	_, err := service.authorizer.Authorize(ctx, actor, accountID, requirement)
	return err
}

func mutation(requestID, kind string, actor ids.UserID, reason string, at time.Time) Mutation {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "Schedule " + kind
	}
	return Mutation{EventID: requestID, Kind: kind, ActorUserID: actor, Reason: reason, CorrelationID: requestID, At: at}
}

func mapDomainError(err error) error {
	if errors.Is(err, domain.ErrVersionConflict) {
		return ErrConflict
	}
	return ErrInvalid
}

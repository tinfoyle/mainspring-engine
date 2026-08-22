// Package scheduling owns customer-defined recurrence and immutable execution
// templates. It intentionally has no worker, database, or workflow dependency.
package scheduling

import (
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	MaximumPersonas     = 32
	MaximumContextItems = 32
	MaximumPromptBytes  = 65536
	MaximumScheduleName = 160
	MaximumSearchDays   = 370
)

var (
	ErrInvalidRecurrence = errors.New("schedule recurrence is invalid")
	ErrInvalidTemplate   = errors.New("schedule execution template is invalid")
	ErrInvalidSchedule   = errors.New("schedule is invalid")
	ErrVersionConflict   = errors.New("schedule version conflicts")
)

type Frequency string

const (
	FrequencyDaily  Frequency = "daily"
	FrequencyWeekly Frequency = "weekly"
)

type GapPolicy string

const (
	GapSkip      GapPolicy = "skip"
	GapNextValid GapPolicy = "next_valid"
)

type OverlapPolicy string

const (
	OverlapFirst  OverlapPolicy = "first"
	OverlapSecond OverlapPolicy = "second"
)

type MissedRunPolicy string

const (
	MissedSkip       MissedRunPolicy = "skip"
	MissedCatchUpOne MissedRunPolicy = "catch_up_one"
)

type Recurrence struct {
	Frequency     Frequency      `json:"frequency"`
	LocalHour     int            `json:"local_hour"`
	LocalMinute   int            `json:"local_minute"`
	Weekdays      []time.Weekday `json:"weekdays"`
	GapPolicy     GapPolicy      `json:"gap_policy"`
	OverlapPolicy OverlapPolicy  `json:"overlap_policy"`
}

func NewRecurrence(value Recurrence) (Recurrence, error) {
	value.Weekdays = append([]time.Weekday(nil), value.Weekdays...)
	slices.Sort(value.Weekdays)
	if (value.Frequency != FrequencyDaily && value.Frequency != FrequencyWeekly) || value.LocalHour < 0 || value.LocalHour > 23 ||
		value.LocalMinute < 0 || value.LocalMinute > 59 || (value.GapPolicy != GapSkip && value.GapPolicy != GapNextValid) ||
		(value.OverlapPolicy != OverlapFirst && value.OverlapPolicy != OverlapSecond) {
		return Recurrence{}, ErrInvalidRecurrence
	}
	for index, weekday := range value.Weekdays {
		if weekday < time.Sunday || weekday > time.Saturday || slices.Contains(value.Weekdays[:index], weekday) {
			return Recurrence{}, ErrInvalidRecurrence
		}
	}
	if (value.Frequency == FrequencyDaily && len(value.Weekdays) != 0) || (value.Frequency == FrequencyWeekly && len(value.Weekdays) == 0) {
		return Recurrence{}, ErrInvalidRecurrence
	}
	return value, nil
}

// Next returns the first selected wall-clock occurrence strictly after the
// supplied instant. It searches in UTC around each local calendar date so DST
// overlaps yield two real candidates and gaps yield none.
func (r Recurrence) Next(after time.Time, timezone string) (time.Time, error) {
	recurrence, err := NewRecurrence(r)
	if err != nil {
		return time.Time{}, err
	}
	if strings.TrimSpace(timezone) != timezone || timezone == "" || timezone == "Local" {
		return time.Time{}, ErrInvalidRecurrence
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, ErrInvalidRecurrence
	}
	after = after.UTC()
	local := after.In(location)
	date := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	for day := 0; day < MaximumSearchDays; day++ {
		candidateDate := date.AddDate(0, 0, day)
		weekday := time.Date(candidateDate.Year(), candidateDate.Month(), candidateDate.Day(), 12, 0, 0, 0, location).Weekday()
		if recurrence.Frequency == FrequencyWeekly && !slices.Contains(recurrence.Weekdays, weekday) {
			continue
		}
		candidates, nextValid := wallClockCandidates(candidateDate, recurrence.LocalHour, recurrence.LocalMinute, location)
		if len(candidates) == 0 && recurrence.GapPolicy == GapNextValid && !nextValid.IsZero() {
			candidates = []time.Time{nextValid}
		}
		if len(candidates) == 2 && recurrence.OverlapPolicy == OverlapSecond {
			candidates = candidates[1:]
		} else if len(candidates) > 0 {
			candidates = candidates[:1]
		}
		for _, candidate := range candidates {
			if candidate.After(after) {
				return candidate.UTC(), nil
			}
		}
	}
	return time.Time{}, ErrInvalidRecurrence
}

func wallClockCandidates(date time.Time, hour, minute int, location *time.Location) ([]time.Time, time.Time) {
	// Every civil timezone is within 14 hours of UTC. A 52-hour UTC window
	// around the requested local date covers both sides of every offset change.
	start := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC).Add(-14 * time.Hour)
	end := start.Add(52 * time.Hour)
	result := make([]time.Time, 0, 2)
	var nextValid time.Time
	targetMinute := hour*60 + minute
	for instant := start; instant.Before(end); instant = instant.Add(time.Minute) {
		local := instant.In(location)
		if local.Year() != date.Year() || local.Month() != date.Month() || local.Day() != date.Day() {
			continue
		}
		wallMinute := local.Hour()*60 + local.Minute()
		if wallMinute == targetMinute {
			result = append(result, instant.UTC())
		}
		if wallMinute > targetMinute && (nextValid.IsZero() || instant.Before(nextValid)) {
			nextValid = instant.UTC()
		}
	}
	return result, nextValid
}

type AgentRunTemplate struct {
	BoardroomID           ids.BoardroomID            `json:"boardroom_id"`
	Mode                  string                     `json:"mode"`
	PersonaIDs            []ids.PersonaID            `json:"persona_ids"`
	Subject               string                     `json:"subject"`
	Prompt                string                     `json:"prompt"`
	WorkItemIDs           []ids.WorkItemID           `json:"work_item_ids"`
	KnowledgeFactIDs      []ids.KnowledgeFactID      `json:"knowledge_fact_ids"`
	KnowledgeDocumentIDs  []ids.KnowledgeDocumentID  `json:"knowledge_document_ids"`
	BaselineAssessmentIDs []ids.BaselineAssessmentID `json:"baseline_assessment_ids"`
}

func NewAgentRunTemplate(value AgentRunTemplate) (AgentRunTemplate, error) {
	value.Subject, value.Prompt = strings.TrimSpace(value.Subject), strings.TrimSpace(value.Prompt)
	value.PersonaIDs = append([]ids.PersonaID(nil), value.PersonaIDs...)
	value.WorkItemIDs = append([]ids.WorkItemID(nil), value.WorkItemIDs...)
	value.KnowledgeFactIDs = append([]ids.KnowledgeFactID(nil), value.KnowledgeFactIDs...)
	value.KnowledgeDocumentIDs = append([]ids.KnowledgeDocumentID(nil), value.KnowledgeDocumentIDs...)
	value.BaselineAssessmentIDs = append([]ids.BaselineAssessmentID(nil), value.BaselineAssessmentIDs...)
	maximumPersonas := MaximumPersonas
	if value.Mode == "manager_led" {
		maximumPersonas--
	}
	if ids.Validate(string(value.BoardroomID)) != nil || (value.Mode != "selected" && value.Mode != "manager_led") || len(value.PersonaIDs) == 0 || len(value.PersonaIDs) > maximumPersonas ||
		utf8.RuneCountInString(value.Subject) < 2 || utf8.RuneCountInString(value.Subject) > 240 || value.Prompt == "" || len(value.Prompt) > MaximumPromptBytes ||
		len(value.WorkItemIDs)+len(value.KnowledgeFactIDs)+len(value.KnowledgeDocumentIDs)+len(value.BaselineAssessmentIDs) > MaximumContextItems {
		return AgentRunTemplate{}, ErrInvalidTemplate
	}
	if !validUniqueIDs(value.PersonaIDs) || !validUniqueIDs(value.WorkItemIDs) || !validUniqueIDs(value.KnowledgeFactIDs) ||
		!validUniqueIDs(value.KnowledgeDocumentIDs) || !validUniqueIDs(value.BaselineAssessmentIDs) {
		return AgentRunTemplate{}, ErrInvalidTemplate
	}
	return value, nil
}

func validUniqueIDs[T ~string](values []T) bool {
	for index, value := range values {
		if ids.Validate(string(value)) != nil || slices.Contains(values[:index], value) {
			return false
		}
	}
	return true
}

type State string

const (
	StateActive State = "active"
	StatePaused State = "paused"
)

type Schedule struct {
	ID              ids.ScheduleID
	AccountID       ids.AccountID
	Name            string
	Timezone        string
	Recurrence      Recurrence
	MissedRunPolicy MissedRunPolicy
	Template        AgentRunTemplate
	State           State
	Version         uint64
	NextRunAt       *time.Time
	CreatedBy       ids.UserID
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type Draft struct {
	ID              ids.ScheduleID
	AccountID       ids.AccountID
	Name            string
	Timezone        string
	Recurrence      Recurrence
	MissedRunPolicy MissedRunPolicy
	Template        AgentRunTemplate
	CreatedBy       ids.UserID
	CreatedAt       time.Time
}

func New(draft Draft) (Schedule, error) {
	recurrence, err := NewRecurrence(draft.Recurrence)
	if err != nil {
		return Schedule{}, err
	}
	template, err := NewAgentRunTemplate(draft.Template)
	if err != nil {
		return Schedule{}, err
	}
	next, err := recurrence.Next(draft.CreatedAt, draft.Timezone)
	if err != nil {
		return Schedule{}, err
	}
	value := Schedule{ID: draft.ID, AccountID: draft.AccountID, Name: strings.TrimSpace(draft.Name), Timezone: draft.Timezone,
		Recurrence: recurrence, MissedRunPolicy: draft.MissedRunPolicy, Template: template, State: StateActive, Version: 1,
		NextRunAt: &next, CreatedBy: draft.CreatedBy, CreatedAt: draft.CreatedAt.UTC(), UpdatedAt: draft.CreatedAt.UTC()}
	return Restore(value)
}

func Restore(value Schedule) (Schedule, error) {
	value.Name = strings.TrimSpace(value.Name)
	recurrence, recurrenceErr := NewRecurrence(value.Recurrence)
	template, templateErr := NewAgentRunTemplate(value.Template)
	if recurrenceErr != nil || templateErr != nil || ids.Validate(string(value.ID)) != nil || ids.Validate(string(value.AccountID)) != nil || ids.Validate(string(value.CreatedBy)) != nil ||
		utf8.RuneCountInString(value.Name) < 2 || utf8.RuneCountInString(value.Name) > MaximumScheduleName || strings.TrimSpace(value.Timezone) != value.Timezone ||
		(value.MissedRunPolicy != MissedSkip && value.MissedRunPolicy != MissedCatchUpOne) || (value.State != StateActive && value.State != StatePaused) ||
		value.Version == 0 || value.CreatedAt.IsZero() || value.UpdatedAt.Before(value.CreatedAt) ||
		(value.State == StateActive) != (value.NextRunAt != nil) {
		return Schedule{}, ErrInvalidSchedule
	}
	if value.Timezone == "Local" {
		return Schedule{}, ErrInvalidSchedule
	}
	if _, err := time.LoadLocation(value.Timezone); err != nil {
		return Schedule{}, ErrInvalidSchedule
	}
	if value.NextRunAt != nil && !value.NextRunAt.After(value.CreatedAt) {
		return Schedule{}, ErrInvalidSchedule
	}
	value.Recurrence, value.Template = recurrence, template
	value.CreatedAt, value.UpdatedAt = value.CreatedAt.UTC(), value.UpdatedAt.UTC()
	if value.NextRunAt != nil {
		next := value.NextRunAt.UTC()
		value.NextRunAt = &next
	}
	return value, nil
}

func (s Schedule) Pause(expectedVersion uint64, at time.Time) (Schedule, error) {
	if expectedVersion != s.Version {
		return Schedule{}, ErrVersionConflict
	}
	if s.State == StatePaused {
		return s, nil
	}
	s.State, s.Version, s.NextRunAt, s.UpdatedAt = StatePaused, s.Version+1, nil, at.UTC()
	return Restore(s)
}

func (s Schedule) Resume(expectedVersion uint64, at time.Time) (Schedule, error) {
	if expectedVersion != s.Version {
		return Schedule{}, ErrVersionConflict
	}
	if s.State == StateActive {
		return s, nil
	}
	next, err := s.Recurrence.Next(at, s.Timezone)
	if err != nil {
		return Schedule{}, err
	}
	s.State, s.Version, s.NextRunAt, s.UpdatedAt = StateActive, s.Version+1, &next, at.UTC()
	return Restore(s)
}

// AdvanceOccurrence records the next selected instant after one scheduled
// occurrence was either dispatched or deliberately skipped. Occurrence/run
// persistence remains an application transaction concern.
func (s Schedule) AdvanceOccurrence(expectedVersion uint64, next, at time.Time) (Schedule, error) {
	if expectedVersion != s.Version {
		return Schedule{}, ErrVersionConflict
	}
	if s.State != StateActive || s.NextRunAt == nil || !next.After(at) || !next.After(*s.NextRunAt) {
		return Schedule{}, ErrInvalidSchedule
	}
	next = next.UTC()
	s.Version, s.NextRunAt, s.UpdatedAt = s.Version+1, &next, at.UTC()
	return Restore(s)
}

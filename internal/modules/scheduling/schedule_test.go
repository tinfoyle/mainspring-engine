package scheduling

import (
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestRecurrenceHandlesDSTGapAndOverlapExplicitly(t *testing.T) {
	gapAfter := time.Date(2026, 3, 8, 4, 0, 0, 0, time.UTC)
	gap := Recurrence{Frequency: FrequencyDaily, LocalHour: 2, LocalMinute: 30, GapPolicy: GapSkip, OverlapPolicy: OverlapFirst}
	next, err := gap.Next(gapAfter, "America/New_York")
	if err != nil || !next.Equal(time.Date(2026, 3, 9, 6, 30, 0, 0, time.UTC)) {
		t.Fatalf("gap skip next=%s err=%v", next, err)
	}
	gap.GapPolicy = GapNextValid
	next, err = gap.Next(gapAfter, "America/New_York")
	if err != nil || !next.Equal(time.Date(2026, 3, 8, 7, 0, 0, 0, time.UTC)) {
		t.Fatalf("gap next-valid=%s err=%v", next, err)
	}

	overlapAfter := time.Date(2026, 11, 1, 4, 0, 0, 0, time.UTC)
	overlap := Recurrence{Frequency: FrequencyDaily, LocalHour: 1, LocalMinute: 30, GapPolicy: GapSkip, OverlapPolicy: OverlapFirst}
	first, err := overlap.Next(overlapAfter, "America/New_York")
	if err != nil || !first.Equal(time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC)) {
		t.Fatalf("overlap first=%s err=%v", first, err)
	}
	overlap.OverlapPolicy = OverlapSecond
	second, err := overlap.Next(overlapAfter, "America/New_York")
	if err != nil || !second.Equal(time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC)) {
		t.Fatalf("overlap second=%s err=%v", second, err)
	}
}

func TestWeeklyRecurrenceSelectsCanonicalWeekdays(t *testing.T) {
	recurrence, err := NewRecurrence(Recurrence{Frequency: FrequencyWeekly, LocalHour: 9, LocalMinute: 15,
		Weekdays: []time.Weekday{time.Friday, time.Monday}, GapPolicy: GapSkip, OverlapPolicy: OverlapFirst})
	if err != nil {
		t.Fatal(err)
	}
	next, err := recurrence.Next(time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC), "America/New_York")
	if err != nil || next.In(time.FixedZone("EDT", -4*60*60)).Weekday() != time.Monday || next.Hour() != 13 || next.Minute() != 15 {
		t.Fatalf("weekly next=%s err=%v", next, err)
	}
}

func TestScheduleFreezesTemplateAndPauseResumeIsVersioned(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	schedule, err := New(Draft{ID: "10000000-0000-4000-8000-000000000001", AccountID: "20000000-0000-4000-8000-000000000002",
		Name: "Weekly operating review", Timezone: "America/New_York",
		Recurrence:      Recurrence{Frequency: FrequencyWeekly, LocalHour: 9, LocalMinute: 0, Weekdays: []time.Weekday{time.Monday}, GapPolicy: GapSkip, OverlapPolicy: OverlapFirst},
		MissedRunPolicy: MissedCatchUpOne, Template: validTemplate(), CreatedBy: "30000000-0000-4000-8000-000000000003", CreatedAt: now})
	if err != nil || schedule.NextRunAt == nil || schedule.State != StateActive {
		t.Fatalf("schedule=%+v err=%v", schedule, err)
	}
	paused, err := schedule.Pause(schedule.Version, now.Add(time.Hour))
	if err != nil || paused.State != StatePaused || paused.NextRunAt != nil || paused.Version != 2 {
		t.Fatalf("paused=%+v err=%v", paused, err)
	}
	if _, err := paused.Resume(1, now.Add(2*time.Hour)); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale resume=%v", err)
	}
	resumed, err := paused.Resume(paused.Version, now.Add(2*time.Hour))
	if err != nil || resumed.State != StateActive || resumed.NextRunAt == nil || resumed.Version != 3 {
		t.Fatalf("resumed=%+v err=%v", resumed, err)
	}
}

func TestScheduleRejectsAmbiguousOrUnboundedInput(t *testing.T) {
	template := validTemplate()
	template.PersonaIDs = append(template.PersonaIDs, template.PersonaIDs[0])
	if _, err := NewAgentRunTemplate(template); !errors.Is(err, ErrInvalidTemplate) {
		t.Fatalf("duplicate Persona error=%v", err)
	}
	if _, err := (Recurrence{Frequency: FrequencyWeekly, LocalHour: 9, GapPolicy: GapSkip, OverlapPolicy: OverlapFirst}).Next(time.Now(), "America/New_York"); !errors.Is(err, ErrInvalidRecurrence) {
		t.Fatalf("empty weekdays error=%v", err)
	}
	if _, err := (Recurrence{Frequency: FrequencyDaily, LocalHour: 9, GapPolicy: GapSkip, OverlapPolicy: OverlapFirst}).Next(time.Now(), "Local"); !errors.Is(err, ErrInvalidRecurrence) {
		t.Fatalf("environment-local timezone error=%v", err)
	}
}

func TestScheduleRevisionRecomputesFutureDueAndDeleteIsTerminal(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	schedule, err := New(Draft{ID: "10000000-0000-4000-8000-000000000001", AccountID: "20000000-0000-4000-8000-000000000002",
		Name: "Daily review", Timezone: "America/New_York", Recurrence: Recurrence{Frequency: FrequencyDaily, LocalHour: 9, GapPolicy: GapSkip, OverlapPolicy: OverlapFirst},
		MissedRunPolicy: MissedSkip, Template: validTemplate(), CreatedBy: "30000000-0000-4000-8000-000000000003", CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	revisedAt := now.Add(time.Hour)
	revised, err := schedule.Revise(1, Revision{Name: "Evening review", Timezone: "America/New_York",
		Recurrence:      Recurrence{Frequency: FrequencyDaily, LocalHour: 17, GapPolicy: GapSkip, OverlapPolicy: OverlapFirst},
		MissedRunPolicy: MissedCatchUpOne, Template: validTemplate()}, revisedAt)
	if err != nil || revised.Version != 2 || revised.NextRunAt == nil || !revised.NextRunAt.After(revisedAt) {
		t.Fatalf("revised=%+v err=%v", revised, err)
	}
	deleted, err := revised.Delete(2, revisedAt.Add(time.Hour))
	if err != nil || deleted.State != StateDeleted || deleted.NextRunAt != nil || deleted.Version != 3 {
		t.Fatalf("deleted=%+v err=%v", deleted, err)
	}
	if _, err := deleted.Resume(3, revisedAt.Add(2*time.Hour)); !errors.Is(err, ErrInvalidSchedule) {
		t.Fatalf("deleted resume err=%v", err)
	}
}

func validTemplate() AgentRunTemplate {
	return AgentRunTemplate{BoardroomID: ids.BoardroomID("40000000-0000-4000-8000-000000000004"), Mode: "manager_led",
		PersonaIDs: []ids.PersonaID{"50000000-0000-4000-8000-000000000005"}, Subject: "Scheduled operating review", Prompt: "Review current operating priorities."}
}

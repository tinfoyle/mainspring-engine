package scheduling

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"

	"github.com/google/uuid"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
	"github.com/tinfoyle/mainspring-engine/internal/orchestration"
)

var ErrInvalidInput = errors.New("invalid schedule")

type CreateInput struct {
	BoardroomID    domain.BoardroomID
	Name           string
	Prompt         string
	Every          time.Duration
	CronExpression string
	TimeZone       string
}

type Service struct {
	store     *Store
	client    client.Client
	tenantID  domain.TenantID
	taskQueue string
}

func NewService(store *Store, temporalClient client.Client, tenantID domain.TenantID, taskQueue string) *Service {
	return &Service{store: store, client: temporalClient, tenantID: tenantID, taskQueue: taskQueue}
}

func (s *Service) List(ctx context.Context) ([]Schedule, error) {
	return s.store.List(ctx)
}

func (s *Service) Create(ctx context.Context, input CreateInput) (Schedule, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Prompt = strings.TrimSpace(input.Prompt)
	input.CronExpression = strings.TrimSpace(input.CronExpression)
	input.TimeZone = strings.TrimSpace(input.TimeZone)
	if input.Name == "" || len(input.Name) > 120 {
		return Schedule{}, fmt.Errorf("%w: schedule name must contain between 1 and 120 characters", ErrInvalidInput)
	}
	if input.Prompt == "" || len(input.Prompt) > 12000 {
		return Schedule{}, fmt.Errorf("%w: schedule request must contain between 1 and 12,000 characters", ErrInvalidInput)
	}
	if input.TimeZone == "" {
		input.TimeZone = "UTC"
	}
	if _, err := time.LoadLocation(input.TimeZone); err != nil {
		return Schedule{}, fmt.Errorf("%w: time zone is not valid", ErrInvalidInput)
	}

	item := Schedule{
		ID: uuid.NewString(), BoardroomID: input.BoardroomID, Name: input.Name, Prompt: input.Prompt,
		Spec: Spec{TimeZone: input.TimeZone},
	}
	item.TemporalScheduleID = fmt.Sprintf("tenant:%s:schedule:%s", s.tenantID.String(), item.ID)
	var temporalSpec client.ScheduleSpec
	if input.CronExpression != "" {
		if len(input.CronExpression) > 200 {
			return Schedule{}, fmt.Errorf("%w: cron expression is too long", ErrInvalidInput)
		}
		item.Spec.Kind = "cron"
		item.Spec.CronExpression = input.CronExpression
		temporalSpec = client.ScheduleSpec{CronExpressions: []string{input.CronExpression}, TimeZoneName: input.TimeZone}
	} else {
		if input.Every < time.Hour || input.Every > 365*24*time.Hour {
			return Schedule{}, fmt.Errorf("%w: interval must be between one hour and one year", ErrInvalidInput)
		}
		item.Spec.Kind = "interval"
		item.Spec.EverySeconds = int64(input.Every / time.Second)
		temporalSpec = client.ScheduleSpec{Intervals: []client.ScheduleIntervalSpec{{Every: input.Every}}, TimeZoneName: input.TimeZone}
	}

	item, err := s.store.Create(ctx, item)
	if err != nil {
		return Schedule{}, err
	}
	_, err = s.client.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID:   item.TemporalScheduleID,
		Spec: temporalSpec,
		Action: &client.ScheduleWorkflowAction{
			ID:        item.TemporalScheduleID + ":run",
			Workflow:  orchestration.BoardroomWorkflowName,
			Args:      []interface{}{orchestration.BoardroomWorkflowInput{TenantID: s.tenantID.String(), BoardroomID: input.BoardroomID.String(), Prompt: input.Prompt}},
			TaskQueue: s.taskQueue,
		},
		Overlap:        enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
		CatchupWindow:  time.Hour,
		PauseOnFailure: true,
	})
	var alreadyExists *serviceerror.AlreadyExists
	if err != nil && !errors.As(err, &alreadyExists) {
		_ = s.store.MarkFailed(ctx, item.ID, err)
		return Schedule{}, fmt.Errorf("create Temporal schedule: %w", err)
	}
	if err := s.store.MarkActive(ctx, item.ID); err != nil {
		return Schedule{}, fmt.Errorf("activate schedule record: %w", err)
	}
	item.State = "active"
	return item, nil
}

func (s *Service) SetPaused(ctx context.Context, id string, paused bool) error {
	item, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	handle := s.client.ScheduleClient().GetHandle(ctx, item.TemporalScheduleID)
	if paused {
		err = handle.Pause(ctx, client.SchedulePauseOptions{Note: "Paused by a Mainspring boardroom owner"})
	} else {
		err = handle.Unpause(ctx, client.ScheduleUnpauseOptions{Note: "Resumed by a Mainspring boardroom owner"})
	}
	if err != nil {
		return fmt.Errorf("update Temporal schedule: %w", err)
	}
	return s.store.SetPaused(ctx, id, paused)
}

func (s *Service) Trigger(ctx context.Context, id string) error {
	item, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	return s.client.ScheduleClient().GetHandle(ctx, item.TemporalScheduleID).Trigger(ctx, client.ScheduleTriggerOptions{
		Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
	})
}

func (s *Service) Delete(ctx context.Context, id string) error {
	item, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := s.client.ScheduleClient().GetHandle(ctx, item.TemporalScheduleID).Delete(ctx); err != nil {
		var notFound *serviceerror.NotFound
		if !errors.As(err, &notFound) {
			return fmt.Errorf("delete Temporal schedule: %w", err)
		}
	}
	return s.store.Delete(ctx, id)
}

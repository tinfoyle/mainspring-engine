package orchestration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	"github.com/tinfoyle/mainspring-engine/internal/boardroom"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

const (
	BoardroomWorkflowName       = "mainspring.boardroom.run.v1"
	CreateScheduledRunActivity  = "mainspring.boardroom.create-scheduled-run.v1"
	ExecuteBoardroomActivity    = "mainspring.boardroom.execute.v1"
	defaultActivityStartToClose = 30 * time.Minute
)

type BoardroomWorkflowInput struct {
	TenantID    string
	RunID       string
	BoardroomID string
	Prompt      string
}

// BoardroomWorkflow deliberately contains only deterministic coordination.
// Database access and model invocation are performed by the Activity.
func BoardroomWorkflow(ctx workflow.Context, input BoardroomWorkflowInput) error {
	options := workflow.ActivityOptions{
		StartToCloseTimeout: defaultActivityStartToClose,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, options)
	if input.RunID == "" {
		if err := workflow.ExecuteActivity(ctx, CreateScheduledRunActivity, input).Get(ctx, &input.RunID); err != nil {
			return err
		}
	}
	return workflow.ExecuteActivity(ctx, ExecuteBoardroomActivity, input).Get(ctx, nil)
}

type Activities struct {
	tenantID domain.TenantID
	service  *boardroom.Service
}

func NewActivities(tenantID domain.TenantID, service *boardroom.Service) *Activities {
	return &Activities{tenantID: tenantID, service: service}
}

func (a *Activities) ExecuteBoardroomRun(ctx context.Context, input BoardroomWorkflowInput) error {
	if input.TenantID != a.tenantID.String() {
		return temporal.NewNonRetryableApplicationError("workflow tenant does not match worker tenant", "tenant_mismatch", nil)
	}
	runID, err := domain.ParseRunID(input.RunID)
	if err != nil {
		return temporal.NewNonRetryableApplicationError("workflow run ID is invalid", "invalid_run_id", err)
	}
	activity.RecordHeartbeat(ctx, "starting", runID.String())
	return a.service.ExecuteRun(ctx, runID)
}

func (a *Activities) CreateScheduledRun(ctx context.Context, input BoardroomWorkflowInput) (string, error) {
	if input.TenantID != a.tenantID.String() {
		return "", temporal.NewNonRetryableApplicationError("workflow tenant does not match worker tenant", "tenant_mismatch", nil)
	}
	boardroomID, err := domain.ParseBoardroomID(input.BoardroomID)
	if err != nil {
		return "", temporal.NewNonRetryableApplicationError("scheduled boardroom ID is invalid", "invalid_boardroom_id", err)
	}
	if input.Prompt == "" {
		return "", temporal.NewNonRetryableApplicationError("scheduled prompt is empty", "invalid_prompt", nil)
	}
	workflowID := activity.GetInfo(ctx).WorkflowExecution.ID
	run, err := a.service.CreateScheduledRun(ctx, boardroomID, workflowID, input.Prompt)
	if err != nil {
		return "", err
	}
	return run.ID.String(), nil
}

type TemporalDispatcher struct {
	client    client.Client
	tenantID  domain.TenantID
	taskQueue string
}

func NewTemporalDispatcher(temporalClient client.Client, tenantID domain.TenantID, taskQueue string) *TemporalDispatcher {
	return &TemporalDispatcher{client: temporalClient, tenantID: tenantID, taskQueue: taskQueue}
}

func (d *TemporalDispatcher) Dispatch(ctx context.Context, runID domain.RunID) error {
	workflowID := fmt.Sprintf("tenant:%s:boardroom-run:%s", d.tenantID.String(), runID.String())
	_, err := d.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: d.taskQueue,
	}, BoardroomWorkflowName, BoardroomWorkflowInput{TenantID: d.tenantID.String(), RunID: runID.String()})
	var alreadyStarted *serviceerror.WorkflowExecutionAlreadyStarted
	if errors.As(err, &alreadyStarted) {
		return nil
	}
	return err
}

func DialTemporal(cfgAddress, namespace string) (client.Client, error) {
	return client.Dial(client.Options{HostPort: cfgAddress, Namespace: namespace})
}

func RunWorker(ctx context.Context, logger *slog.Logger, temporalClient client.Client, taskQueue string, activities *Activities) error {
	w := worker.New(temporalClient, taskQueue, worker.Options{})
	w.RegisterWorkflowWithOptions(BoardroomWorkflow, workflow.RegisterOptions{Name: BoardroomWorkflowName})
	w.RegisterActivityWithOptions(activities.CreateScheduledRun, activity.RegisterOptions{Name: CreateScheduledRunActivity})
	w.RegisterActivityWithOptions(activities.ExecuteBoardroomRun, activity.RegisterOptions{Name: ExecuteBoardroomActivity})
	if err := w.Start(); err != nil {
		return fmt.Errorf("start Temporal worker: %w", err)
	}
	logger.Info("Temporal worker started", "task_queue", taskQueue)
	defer w.Stop()
	<-ctx.Done()
	return nil
}

type LocalDispatcher struct {
	rootContext context.Context
	logger      *slog.Logger
	service     *boardroom.Service
	slots       chan struct{}
}

func NewLocalDispatcher(rootContext context.Context, logger *slog.Logger, service *boardroom.Service, concurrency int) *LocalDispatcher {
	if concurrency <= 0 {
		concurrency = 1
	}
	return &LocalDispatcher{rootContext: rootContext, logger: logger, service: service, slots: make(chan struct{}, concurrency)}
}

func (d *LocalDispatcher) Dispatch(_ context.Context, runID domain.RunID) error {
	if d.rootContext.Err() != nil {
		return d.rootContext.Err()
	}
	go func() {
		select {
		case d.slots <- struct{}{}:
			defer func() { <-d.slots }()
		case <-d.rootContext.Done():
			return
		}
		if err := d.service.ExecuteRun(d.rootContext, runID); err != nil {
			d.logger.Error("local boardroom run failed", "run_id", runID.String(), "error", err)
		}
	}()
	return nil
}

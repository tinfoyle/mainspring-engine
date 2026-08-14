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

	"github.com/tinfoyle/mainspring-engine/internal/agent"
	"github.com/tinfoyle/mainspring-engine/internal/boardroom"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

const (
	BoardroomWorkflowName        = "mainspring.boardroom.run.v1"
	CreateScheduledRunActivity   = "mainspring.boardroom.create-scheduled-run.v1"
	PrepareBoardroomRunActivity  = "mainspring.boardroom.prepare.v1"
	ExecutePersonaTurnActivity   = "mainspring.boardroom.execute-turn.v1"
	PlanDelegationsActivity      = "mainspring.boardroom.plan-delegations.v1"
	CompleteBoardroomRunActivity = "mainspring.boardroom.complete.v1"
	FailBoardroomRunActivity     = "mainspring.boardroom.fail.v1"
	ApprovalDecisionSignal       = "mainspring.approval.decided.v1"
	defaultActivityStartToClose  = 30 * time.Minute
)

type BoardroomWorkflowInput struct {
	TenantID          string
	RunID             string
	BoardroomID       string
	Prompt            string
	ConversationTitle string
	ScheduleID        string
	ScheduleTimeZone  string
}

type PersonaTurnInput struct {
	TenantID   string
	RunID      string
	TurnNumber int
}

type FailRunInput struct {
	TenantID string
	RunID    string
	Error    string
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
			MaximumAttempts:    100,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, options)
	if input.RunID == "" {
		if err := workflow.ExecuteActivity(ctx, CreateScheduledRunActivity, input).Get(ctx, &input.RunID); err != nil {
			return err
		}
	}
	var turnCount int
	if err := workflow.ExecuteActivity(ctx, PrepareBoardroomRunActivity, input).Get(ctx, &turnCount); err != nil {
		markRunFailed(ctx, input, err)
		return err
	}
	if turnCount > 0 {
		turnInput := PersonaTurnInput{TenantID: input.TenantID, RunID: input.RunID, TurnNumber: 1}
		if err := workflow.ExecuteActivity(ctx, ExecutePersonaTurnActivity, turnInput).Get(ctx, nil); err != nil {
			markRunFailed(ctx, input, err)
			return err
		}
	}
	if err := workflow.ExecuteActivity(ctx, PlanDelegationsActivity, input).Get(ctx, &turnCount); err != nil {
		markRunFailed(ctx, input, err)
		return err
	}
	for turnNumber := 2; turnNumber <= turnCount; turnNumber++ {
		turnInput := PersonaTurnInput{TenantID: input.TenantID, RunID: input.RunID, TurnNumber: turnNumber}
		if err := workflow.ExecuteActivity(ctx, ExecutePersonaTurnActivity, turnInput).Get(ctx, nil); err != nil {
			markRunFailed(ctx, input, err)
			return err
		}
	}
	var awaitingApproval bool
	if err := workflow.ExecuteActivity(ctx, CompleteBoardroomRunActivity, input).Get(ctx, &awaitingApproval); err != nil {
		markRunFailed(ctx, input, err)
		return err
	}
	for awaitingApproval {
		var decision map[string]string
		workflow.GetSignalChannel(ctx, ApprovalDecisionSignal).Receive(ctx, &decision)
		if err := workflow.ExecuteActivity(ctx, CompleteBoardroomRunActivity, input).Get(ctx, &awaitingApproval); err != nil {
			markRunFailed(ctx, input, err)
			return err
		}
	}
	return nil
}

func markRunFailed(ctx workflow.Context, input BoardroomWorkflowInput, runErr error) {
	disconnected, _ := workflow.NewDisconnectedContext(ctx)
	_ = workflow.ExecuteActivity(disconnected, FailBoardroomRunActivity, FailRunInput{
		TenantID: input.TenantID, RunID: input.RunID, Error: runErr.Error(),
	}).Get(disconnected, nil)
}

type Activities struct {
	tenantID domain.TenantID
	service  *boardroom.Service
}

func NewActivities(tenantID domain.TenantID, service *boardroom.Service) *Activities {
	return &Activities{tenantID: tenantID, service: service}
}

func (a *Activities) PrepareBoardroomRun(ctx context.Context, input BoardroomWorkflowInput) (int, error) {
	if input.TenantID != a.tenantID.String() {
		return 0, temporal.NewNonRetryableApplicationError("workflow tenant does not match worker tenant", "tenant_mismatch", nil)
	}
	runID, err := domain.ParseRunID(input.RunID)
	if err != nil {
		return 0, temporal.NewNonRetryableApplicationError("workflow run ID is invalid", "invalid_run_id", err)
	}
	plan, err := a.service.PrepareRun(ctx, runID)
	if err != nil {
		return 0, err
	}
	return len(plan.Personas), nil
}

func (a *Activities) ExecutePersonaTurn(ctx context.Context, input PersonaTurnInput) error {
	if input.TenantID != a.tenantID.String() {
		return temporal.NewNonRetryableApplicationError("workflow tenant does not match worker tenant", "tenant_mismatch", nil)
	}
	runID, err := domain.ParseRunID(input.RunID)
	if err != nil {
		return temporal.NewNonRetryableApplicationError("workflow run ID is invalid", "invalid_run_id", err)
	}
	activity.RecordHeartbeat(ctx, "invoking", runID.String(), input.TurnNumber)
	if err := a.service.ExecuteTurn(ctx, runID, input.TurnNumber); err != nil {
		category, retryable := agent.Failure(err)
		if !retryable {
			return temporal.NewNonRetryableApplicationError(err.Error(), string(category), err)
		}
		return err
	}
	return nil
}

func (a *Activities) PlanDelegations(ctx context.Context, input BoardroomWorkflowInput) (int, error) {
	if input.TenantID != a.tenantID.String() {
		return 0, temporal.NewNonRetryableApplicationError("workflow tenant does not match worker tenant", "tenant_mismatch", nil)
	}
	runID, err := domain.ParseRunID(input.RunID)
	if err != nil {
		return 0, temporal.NewNonRetryableApplicationError("workflow run ID is invalid", "invalid_run_id", err)
	}
	turnCount, err := a.service.ScheduleDelegations(ctx, runID)
	if errors.Is(err, boardroom.ErrInvalidDelegation) {
		return 0, temporal.NewNonRetryableApplicationError(err.Error(), "invalid_delegation", err)
	}
	return turnCount, err
}

func (a *Activities) CompleteBoardroomRun(ctx context.Context, input BoardroomWorkflowInput) (bool, error) {
	if input.TenantID != a.tenantID.String() {
		return false, temporal.NewNonRetryableApplicationError("workflow tenant does not match worker tenant", "tenant_mismatch", nil)
	}
	runID, err := domain.ParseRunID(input.RunID)
	if err != nil {
		return false, temporal.NewNonRetryableApplicationError("workflow run ID is invalid", "invalid_run_id", err)
	}
	return a.service.FinalizeRun(ctx, runID)
}

func (a *Activities) FailBoardroomRun(ctx context.Context, input FailRunInput) error {
	if input.TenantID != a.tenantID.String() {
		return temporal.NewNonRetryableApplicationError("workflow tenant does not match worker tenant", "tenant_mismatch", nil)
	}
	runID, err := domain.ParseRunID(input.RunID)
	if err != nil {
		return temporal.NewNonRetryableApplicationError("workflow run ID is invalid", "invalid_run_id", err)
	}
	return a.service.FailRun(ctx, runID, input.Error)
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
	title := input.ConversationTitle
	location, locationErr := time.LoadLocation(input.ScheduleTimeZone)
	if locationErr != nil {
		location = time.UTC
	}
	if title != "" {
		title += " — " + time.Now().In(location).Format("January 2, 2006")
	}
	run, err := a.service.CreateScheduledRun(ctx, boardroomID, workflowID, title, input.Prompt, input.ScheduleID)
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

func (d *TemporalDispatcher) ApprovalDecision(ctx context.Context, runID domain.RunID, approvalID string) error {
	workflowID := fmt.Sprintf("tenant:%s:boardroom-run:%s", d.tenantID.String(), runID.String())
	return d.client.SignalWorkflow(ctx, workflowID, "", ApprovalDecisionSignal, map[string]string{"approval_id": approvalID})
}

func DialTemporal(cfgAddress, namespace string) (client.Client, error) {
	return client.Dial(client.Options{HostPort: cfgAddress, Namespace: namespace})
}

func RunWorker(ctx context.Context, logger *slog.Logger, temporalClient client.Client, taskQueue string, activities *Activities) error {
	w := worker.New(temporalClient, taskQueue, worker.Options{})
	w.RegisterWorkflowWithOptions(BoardroomWorkflow, workflow.RegisterOptions{Name: BoardroomWorkflowName})
	w.RegisterActivityWithOptions(activities.CreateScheduledRun, activity.RegisterOptions{Name: CreateScheduledRunActivity})
	w.RegisterActivityWithOptions(activities.PrepareBoardroomRun, activity.RegisterOptions{Name: PrepareBoardroomRunActivity})
	w.RegisterActivityWithOptions(activities.ExecutePersonaTurn, activity.RegisterOptions{Name: ExecutePersonaTurnActivity})
	w.RegisterActivityWithOptions(activities.PlanDelegations, activity.RegisterOptions{Name: PlanDelegationsActivity})
	w.RegisterActivityWithOptions(activities.CompleteBoardroomRun, activity.RegisterOptions{Name: CompleteBoardroomRunActivity})
	w.RegisterActivityWithOptions(activities.FailBoardroomRun, activity.RegisterOptions{Name: FailBoardroomRunActivity})
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

func (d *LocalDispatcher) ApprovalDecision(ctx context.Context, runID domain.RunID, _ string) error {
	return d.service.CompleteRun(ctx, runID)
}

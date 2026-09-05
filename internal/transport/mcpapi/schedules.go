package mcpapi

import (
	"context"
	"errors"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	app "github.com/tinfoyle/spyglass-engine/internal/application/scheduling"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/scheduling"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type scheduleTargetInput struct {
	AccountID  ids.AccountID  `json:"account_id"`
	ScheduleID ids.ScheduleID `json:"schedule_id"`
}
type scheduleListInput struct {
	AccountID ids.AccountID `json:"account_id"`
	Limit     int           `json:"limit,omitempty"`
	Cursor    *app.Cursor   `json:"cursor,omitempty"`
}
type scheduleWriteInput struct {
	AccountID       ids.AccountID           `json:"account_id"`
	OperationID     string                  `json:"operation_id"`
	ScheduleID      ids.ScheduleID          `json:"schedule_id,omitempty"`
	ExpectedVersion uint64                  `json:"expected_version,omitempty"`
	Name            string                  `json:"name"`
	Timezone        string                  `json:"timezone"`
	Recurrence      domain.Recurrence       `json:"recurrence"`
	MissedRunPolicy domain.MissedRunPolicy  `json:"missed_run_policy"`
	Template        domain.AgentRunTemplate `json:"template"`
	Reason          string                  `json:"reason"`
}
type scheduleTransitionInput struct {
	AccountID       ids.AccountID  `json:"account_id"`
	ScheduleID      ids.ScheduleID `json:"schedule_id"`
	OperationID     string         `json:"operation_id"`
	ExpectedVersion uint64         `json:"expected_version"`
	Reason          string         `json:"reason"`
}

func (s *Server) registerSchedules(server *mcp.Server, actor access.Actor) {
	read := access.Requirement{Package: catalog.PackageAgents}
	write := access.Requirement{Package: catalog.PackageAgents, Mutation: true}
	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_schedule_list", Description: "List saved recurring agent tasks. Use the returned cursor to continue.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, in scheduleListInput) (*mcp.CallToolResult, app.Page, error) {
		ctx, err := s.toolContext(ctx, actor, in.AccountID, read)
		if err != nil {
			return nil, app.Page{}, err
		}
		if in.Limit == 0 {
			in.Limit = 25
		}
		q := app.ListQuery{Limit: in.Limit}
		if in.Cursor != nil {
			q.AfterUpdatedAt = &in.Cursor.UpdatedAt
			q.AfterID = in.Cursor.ID
		}
		v, err := s.scheduling.List(ctx, app.ListCommand{Actor: actor, AccountID: in.AccountID, Query: q})
		return nil, v, scheduleToolError(err)
	})
	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_schedule_get", Description: "Read a saved schedule and its current version before changing or triggering it.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, in scheduleTargetInput) (*mcp.CallToolResult, domain.Schedule, error) {
		ctx, err := s.toolContext(ctx, actor, in.AccountID, read)
		if err != nil {
			return nil, domain.Schedule{}, err
		}
		v, err := s.scheduling.Get(ctx, app.GetQuery{Actor: actor, AccountID: in.AccountID, ScheduleID: in.ScheduleID})
		return nil, v, scheduleToolError(err)
	})

	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_schedule_history", Description: "Read the last 50 occurrences, agent run states and email outcomes. Sent means the mail server accepted delivery; unknown means delivery could not be confirmed and will not be automatically retried.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, in scheduleTargetInput) (*mcp.CallToolResult, app.HistoryPage, error) {
		ctx, err := s.toolContext(ctx, actor, in.AccountID, read)
		if err != nil {
			return nil, app.HistoryPage{}, err
		}
		v, err := s.scheduling.History(ctx, app.GetQuery{Actor: actor, AccountID: in.AccountID, ScheduleID: in.ScheduleID})
		return nil, v, scheduleToolError(err)
	})
	for _, action := range []string{"create", "revise"} {
		mcp.AddTool(server, &mcp.Tool{Name: "spyglass_schedule_" + action, Description: "Save a recurring agent task requested by the user. Establish instructions, local time, timezone and selected agents first. Preserve the operation ID for an exact retry. Email delivery, when enabled, authorizes recurring reports only to the requesting user's verified address.", Annotations: toolAnnotations(false, true)}, func(ctx context.Context, _ *mcp.CallToolRequest, in scheduleWriteInput) (*mcp.CallToolResult, domain.Schedule, error) {
			ctx, err := s.toolContext(ctx, actor, in.AccountID, write)
			if err != nil {
				return nil, domain.Schedule{}, err
			}
			op, err := operationID(in.OperationID)
			if err != nil {
				return nil, domain.Schedule{}, err
			}
			if action == "create" {
				if in.ScheduleID != "" || in.ExpectedVersion != 0 {
					return nil, domain.Schedule{}, safeError("invalid_schedule_request")
				}
				v, _, err := s.scheduling.Create(ctx, app.CreateCommand{Actor: actor, AccountID: in.AccountID, RequestID: op, Name: in.Name, Timezone: in.Timezone, Recurrence: in.Recurrence, MissedRunPolicy: in.MissedRunPolicy, Template: in.Template, Reason: in.Reason})
				return nil, v, scheduleToolError(err)
			}
			v, err := s.scheduling.Revise(ctx, app.ReviseCommand{Actor: actor, AccountID: in.AccountID, RequestID: op, ScheduleID: in.ScheduleID, ExpectedVersion: in.ExpectedVersion, Name: in.Name, Timezone: in.Timezone, Recurrence: in.Recurrence, MissedRunPolicy: in.MissedRunPolicy, Template: in.Template, Reason: in.Reason})
			return nil, v, scheduleToolError(err)
		})
	}
	for _, action := range []string{"pause", "resume", "delete"} {
		mcp.AddTool(server, &mcp.Tool{Name: "spyglass_schedule_" + action, Description: "Change a recurring task using its current version. Pausing stops future occurrences; deletion retains governed history.", Annotations: toolAnnotations(false, true)}, func(ctx context.Context, _ *mcp.CallToolRequest, in scheduleTransitionInput) (*mcp.CallToolResult, domain.Schedule, error) {
			ctx, err := s.toolContext(ctx, actor, in.AccountID, write)
			if err != nil {
				return nil, domain.Schedule{}, err
			}
			op, err := operationID(in.OperationID)
			if err != nil {
				return nil, domain.Schedule{}, err
			}
			cmd := app.TransitionCommand{Actor: actor, AccountID: in.AccountID, ScheduleID: in.ScheduleID, RequestID: op, ExpectedVersion: in.ExpectedVersion, Reason: in.Reason}
			var v domain.Schedule
			switch action {
			case "pause":
				v, err = s.scheduling.Pause(ctx, cmd)
			case "resume":
				v, err = s.scheduling.Resume(ctx, cmd)
			case "delete":
				v, err = s.scheduling.Delete(ctx, cmd)
			}
			return nil, v, scheduleToolError(err)
		})
	}
	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_schedule_trigger", Description: "Run one active schedule now without changing its next recurring time. Reuse the operation ID for exact retries to avoid duplicate runs.", Annotations: toolAnnotations(false, true)}, func(ctx context.Context, _ *mcp.CallToolRequest, in scheduleTransitionInput) (*mcp.CallToolResult, app.Trigger, error) {
		ctx, err := s.toolContext(ctx, actor, in.AccountID, write)
		if err != nil {
			return nil, app.Trigger{}, err
		}
		op, err := operationID(in.OperationID)
		if err != nil {
			return nil, app.Trigger{}, err
		}
		v, _, err := s.scheduling.TriggerNow(ctx, app.TransitionCommand{Actor: actor, AccountID: in.AccountID, ScheduleID: in.ScheduleID, RequestID: op, ExpectedVersion: in.ExpectedVersion, Reason: in.Reason})
		return nil, v, scheduleToolError(err)
	})
}
func scheduleToolError(err error) error {
	var denied *access.DeniedError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &denied):
		return safeError(string(denied.Code))
	case errors.Is(err, app.ErrInvalid):
		return safeError("invalid_schedule_request")
	case errors.Is(err, app.ErrConflict):
		return safeError("schedule_conflict")
	case errors.Is(err, app.ErrNotFound):
		return safeError("schedule_not_found")
	default:
		return safeError("schedule_unavailable")
	}
}

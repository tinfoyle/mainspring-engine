package mcpapi

import (
	"context"
	"errors"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	app "github.com/tinfoyle/spyglass-engine/internal/application/agents"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type agentTeamInput struct {
	AccountID   ids.AccountID   `json:"account_id"`
	BoardroomID ids.BoardroomID `json:"boardroom_id,omitempty"`
	Limit       int             `json:"limit,omitempty"`
}
type agentTeamView struct {
	ID               ids.BoardroomID `json:"id"`
	Name             string          `json:"name"`
	Purpose          string          `json:"purpose"`
	State            string          `json:"state"`
	ManagerPersonaID ids.PersonaID   `json:"manager_persona_id,omitempty"`
}
type agentTeamPage struct {
	Items []agentTeamView `json:"items"`
}
type agentView struct {
	ID      ids.PersonaID `json:"id"`
	Name    string        `json:"name"`
	Role    string        `json:"role"`
	State   string        `json:"state"`
	Version uint64        `json:"version"`
}
type agentPage struct {
	Items []agentView `json:"items"`
}
type agentRunInput struct {
	AccountID      ids.AccountID      `json:"account_id"`
	OperationID    string             `json:"operation_id"`
	BoardroomID    ids.BoardroomID    `json:"boardroom_id"`
	ConversationID ids.ConversationID `json:"conversation_id,omitempty"`
	Subject        string             `json:"subject,omitempty"`
	Prompt         string             `json:"prompt"`
	Mode           app.RunMode        `json:"mode"`
	PersonaIDs     []ids.PersonaID    `json:"persona_ids"`
}
type agentRunTarget struct {
	AccountID ids.AccountID `json:"account_id"`
	RunID     ids.RunID     `json:"run_id"`
}
type agentRunView struct {
	ID             ids.RunID          `json:"id"`
	ConversationID ids.ConversationID `json:"conversation_id"`
	State          string             `json:"state"`
	Subject        string             `json:"subject"`
}
type agentMessageInput struct {
	AccountID      ids.AccountID      `json:"account_id"`
	ConversationID ids.ConversationID `json:"conversation_id"`
	Limit          int                `json:"limit,omitempty"`
	AfterSequence  uint64             `json:"after_sequence,omitempty"`
}
type agentMessageView struct {
	Sequence uint64    `json:"sequence"`
	Role     string    `json:"role"`
	Body     string    `json:"body"`
	RunID    ids.RunID `json:"run_id,omitempty"`
}
type agentMessagePage struct {
	Items             []agentMessageView `json:"items"`
	NextAfterSequence *uint64            `json:"next_after_sequence,omitempty"`
}

func runToolView(v app.Run) agentRunView {
	return agentRunView{ID: v.Plan.RunID, ConversationID: v.Plan.ConversationID, State: v.State, Subject: v.Subject}
}
func (s *Server) registerAgents(server *mcp.Server, actor access.Actor) {
	read := access.Requirement{Package: catalog.PackageAgents}
	write := access.Requirement{Package: catalog.PackageAgents, Mutation: true}
	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_agent_team_list", Description: "List the existing agent teams available to this account before selecting agents for a run or schedule.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, in agentTeamInput) (*mcp.CallToolResult, agentTeamPage, error) {
		ctx, err := s.toolContext(ctx, actor, in.AccountID, read)
		if err != nil {
			return nil, agentTeamPage{}, err
		}
		if in.Limit == 0 {
			in.Limit = 100
		}
		rows, err := s.agents.ListBoardrooms(ctx, actor, in.AccountID, in.Limit)
		out := agentTeamPage{Items: []agentTeamView{}}
		for _, v := range rows {
			out.Items = append(out.Items, agentTeamView{ID: v.ID, Name: v.Name, Purpose: v.Purpose, State: string(v.State), ManagerPersonaID: v.ManagerPersonaID})
		}
		return nil, out, agentToolError(err)
	})
	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_agent_list", Description: "List published agents in one agent team. Select active agents by their returned IDs.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, in agentTeamInput) (*mcp.CallToolResult, agentPage, error) {
		ctx, err := s.toolContext(ctx, actor, in.AccountID, read)
		if err != nil {
			return nil, agentPage{}, err
		}
		if in.Limit == 0 {
			in.Limit = 100
		}
		rows, err := s.agents.ListPersonas(ctx, actor, in.AccountID, in.BoardroomID, in.Limit)
		out := agentPage{Items: []agentView{}}
		for _, v := range rows {
			out.Items = append(out.Items, agentView{ID: v.ID, Name: v.Published.Name, Role: v.Published.Role, State: v.State, Version: v.LatestVersion})
		}
		return nil, out, agentToolError(err)
	})
	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_agent_run_start", Description: "Start one bounded agent run. Supply a subject for a new conversation or an existing conversation ID. This consumes AI credits; preserve operation_id on exact retries. Starting a run does not create a recurring schedule.", Annotations: toolAnnotations(false, true)}, func(ctx context.Context, _ *mcp.CallToolRequest, in agentRunInput) (*mcp.CallToolResult, agentRunView, error) {
		ctx, err := s.toolContext(ctx, actor, in.AccountID, write)
		if err != nil {
			return nil, agentRunView{}, err
		}
		op, err := operationID(in.OperationID)
		if err != nil {
			return nil, agentRunView{}, err
		}
		v, _, err := s.agents.StartRun(ctx, app.StartRunCommand{Actor: actor, AccountID: in.AccountID, RequestID: op, BoardroomID: in.BoardroomID, ConversationID: in.ConversationID, Subject: in.Subject, Prompt: in.Prompt, Mode: in.Mode, PersonaIDs: in.PersonaIDs})
		return nil, runToolView(v), agentToolError(err)
	})
	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_agent_run_get", Description: "Read an agent run's current status and conversation ID. Poll at a bounded interval; read conversation messages for results.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, in agentRunTarget) (*mcp.CallToolResult, agentRunView, error) {
		ctx, err := s.toolContext(ctx, actor, in.AccountID, read)
		if err != nil {
			return nil, agentRunView{}, err
		}
		v, err := s.agents.GetRun(ctx, actor, in.AccountID, in.RunID)
		return nil, runToolView(v), agentToolError(err)
	})
	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_agent_messages", Description: "Read the saved results and questions from an agent conversation. Continue after the returned sequence for additional messages.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, in agentMessageInput) (*mcp.CallToolResult, agentMessagePage, error) {
		ctx, err := s.toolContext(ctx, actor, in.AccountID, read)
		if err != nil {
			return nil, agentMessagePage{}, err
		}
		if in.Limit == 0 {
			in.Limit = 25
		}
		rows, err := s.agents.ListMessages(ctx, actor, in.AccountID, in.ConversationID, app.MessageListQuery{Limit: in.Limit, AfterSequence: in.AfterSequence})
		out := agentMessagePage{Items: []agentMessageView{}, NextAfterSequence: rows.NextAfterSequence}
		for _, v := range rows.Items {
			out.Items = append(out.Items, agentMessageView{Sequence: v.Sequence, Role: string(v.Role), Body: v.Body, RunID: v.RunID})
		}
		return nil, out, agentToolError(err)
	})
}
func agentToolError(err error) error {
	var denied *access.DeniedError
	var capacity *app.ConcurrentRunLimitError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &denied):
		return safeError(string(denied.Code))
	case errors.As(err, &capacity):
		return safeError("agent_capacity_unavailable")
	case errors.Is(err, app.ErrInvalidCommand):
		return safeError("invalid_agent_request")
	case errors.Is(err, app.ErrConflict):
		return safeError("agent_conflict")
	case errors.Is(err, app.ErrNotFound):
		return safeError("agent_not_found")
	default:
		return safeError("agent_unavailable")
	}
}

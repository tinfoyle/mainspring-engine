package mcpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	integrationsapp "github.com/tinfoyle/spyglass-engine/internal/application/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	integrationsdomain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type integrationConnectionTargetInput struct {
	AccountID    ids.AccountID               `json:"account_id"`
	ConnectionID ids.IntegrationConnectionID `json:"connection_id"`
}
type integrationConnectionListInput struct {
	AccountID ids.AccountID                        `json:"account_id"`
	States    []integrationsdomain.ConnectionState `json:"states,omitempty"`
	Kinds     []integrationsdomain.ConnectorKind   `json:"kinds,omitempty"`
	Cursor    string                               `json:"cursor,omitempty"`
	Limit     int                                  `json:"limit,omitempty"`
}
type integrationConnectionCreateInput struct {
	AccountID    ids.AccountID                      `json:"account_id"`
	OperationID  string                             `json:"operation_id"`
	Name         string                             `json:"name"`
	Kind         integrationsdomain.ConnectorKind   `json:"kind"`
	Capabilities []integrationsdomain.Capability    `json:"capabilities"`
	Scope        integrationsdomain.ConnectionScope `json:"scope"`
}
type integrationConnectionReviseInput struct {
	AccountID       ids.AccountID                      `json:"account_id"`
	OperationID     string                             `json:"operation_id"`
	ConnectionID    ids.IntegrationConnectionID        `json:"connection_id"`
	ExpectedVersion uint64                             `json:"expected_version"`
	Name            string                             `json:"name"`
	Capabilities    []integrationsdomain.Capability    `json:"capabilities"`
	Scope           integrationsdomain.ConnectionScope `json:"scope"`
}
type integrationCredentialInput struct {
	AccountID          ids.AccountID               `json:"account_id"`
	OperationID        string                      `json:"operation_id"`
	ConnectionID       ids.IntegrationConnectionID `json:"connection_id"`
	ExpectedVersion    uint64                      `json:"expected_version"`
	ExpectedGeneration uint64                      `json:"expected_generation,omitempty"`
	Provider           string                      `json:"provider"`
	ReferenceSHA256    string                      `json:"reference_sha256"`
	ExpiresAt          *time.Time                  `json:"expires_at,omitempty"`
}
type integrationTransitionInput struct {
	AccountID       ids.AccountID               `json:"account_id"`
	OperationID     string                      `json:"operation_id"`
	ConnectionID    ids.IntegrationConnectionID `json:"connection_id"`
	ExpectedVersion uint64                      `json:"expected_version"`
}
type integrationHealthListInput struct {
	AccountID    ids.AccountID               `json:"account_id"`
	ConnectionID ids.IntegrationConnectionID `json:"connection_id"`
	Cursor       string                      `json:"cursor,omitempty"`
	Limit        int                         `json:"limit,omitempty"`
}
type integrationExecutionTargetInput struct {
	AccountID   ids.AccountID              `json:"account_id"`
	ExecutionID ids.IntegrationExecutionID `json:"execution_id"`
}
type integrationExecutionListInput struct {
	AccountID    ids.AccountID                       `json:"account_id"`
	ConnectionID ids.IntegrationConnectionID         `json:"connection_id,omitempty"`
	States       []integrationsdomain.ExecutionState `json:"states,omitempty"`
	Capabilities []integrationsdomain.Capability     `json:"capabilities,omitempty"`
	Cursor       string                              `json:"cursor,omitempty"`
	Limit        int                                 `json:"limit,omitempty"`
}
type integrationExecutionPrepareInput struct {
	AccountID      ids.AccountID                 `json:"account_id"`
	OperationID    string                        `json:"operation_id"`
	ReleaseID      ids.MarketingReleaseID        `json:"release_id"`
	ReleaseVersion uint64                        `json:"release_version"`
	Capability     integrationsdomain.Capability `json:"capability"`
	ConnectionID   ids.IntegrationConnectionID   `json:"connection_id"`
}

type integrationConnectionPageOutput struct {
	Items      []integrationsdomain.Connection `json:"items"`
	NextCursor string                          `json:"next_cursor,omitempty"`
}
type integrationConnectionDetailOutput struct {
	Connection   integrationsdomain.Connection         `json:"connection"`
	Revision     integrationsdomain.ConnectionRevision `json:"revision"`
	LatestHealth *integrationsdomain.HealthObservation `json:"latest_health,omitempty"`
}
type integrationHealthPageOutput struct {
	Items      []integrationsdomain.HealthObservation `json:"items"`
	NextCursor string                                 `json:"next_cursor,omitempty"`
}
type integrationExecutionOutput struct {
	ID                   ids.IntegrationExecutionID          `json:"id"`
	AccountID            ids.AccountID                       `json:"account_id"`
	ReleaseID            ids.MarketingReleaseID              `json:"release_id"`
	ReleaseVersion       uint64                              `json:"release_version"`
	ApprovalID           ids.ConsequentialApprovalID         `json:"approval_id"`
	Capability           integrationsdomain.Capability       `json:"capability"`
	ConnectionID         ids.IntegrationConnectionID         `json:"connection_id"`
	ConnectionRevisionID ids.IntegrationConnectionRevisionID `json:"connection_revision_id"`
	ConnectionRevision   uint64                              `json:"connection_revision"`
	CredentialID         ids.IntegrationCredentialID         `json:"credential_id"`
	CredentialGeneration uint64                              `json:"credential_generation"`
	PayloadSHA256        string                              `json:"payload_sha256"`
	State                integrationsdomain.ExecutionState   `json:"state"`
	AttemptCount         uint16                              `json:"attempt_count"`
	CurrentAttemptID     ids.IntegrationAttemptID            `json:"current_attempt_id,omitempty"`
	LastErrorCode        string                              `json:"last_error_code,omitempty"`
	LeaseExpiresAt       *time.Time                          `json:"lease_expires_at,omitempty"`
	NextAttemptAt        *time.Time                          `json:"next_attempt_at,omitempty"`
	CreatedAt            time.Time                           `json:"created_at"`
	UpdatedAt            time.Time                           `json:"updated_at"`
	CompletedAt          *time.Time                          `json:"completed_at,omitempty"`
}
type integrationExecutionPageOutput struct {
	Items      []integrationExecutionOutput `json:"items"`
	NextCursor string                       `json:"next_cursor,omitempty"`
}
type integrationExecutionDetailOutput struct {
	Execution integrationExecutionOutput   `json:"execution"`
	Attempts  []integrationsdomain.Attempt `json:"attempts"`
}

func (s *Server) registerIntegrations(server *mcp.Server, actor access.Actor) {
	read, mutation := access.Requirement{Package: catalog.PackageIntegrations}, access.Requirement{Package: catalog.PackageIntegrations, Mutation: true}
	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_integrations_connection_list", Title: "List Integration connections", Description: "List bounded Account-owned connector identities without provider secrets.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input integrationConnectionListInput) (*mcp.CallToolResult, integrationConnectionPageOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, read)
		if err != nil {
			return nil, integrationConnectionPageOutput{}, err
		}
		limit, err := integrationLimit(input.Limit)
		if err != nil {
			return nil, integrationConnectionPageOutput{}, err
		}
		query := integrationsapp.ConnectionListQuery{States: input.States, Kinds: input.Kinds, Limit: limit}
		if input.Cursor != "" {
			cursor, err := decodeIntegrationMCPCursor(input.Cursor, "connection")
			if err != nil {
				return nil, integrationConnectionPageOutput{}, err
			}
			query.After = &integrationsapp.ConnectionCursor{UpdatedAt: cursor.Date, ID: ids.IntegrationConnectionID(cursor.ID)}
		}
		page, err := s.integrations.ListConnections(ctx, actor, input.AccountID, query)
		if err != nil {
			return nil, integrationConnectionPageOutput{}, integrationError(err)
		}
		output := integrationConnectionPageOutput{Items: page.Items}
		if page.NextCursor != nil {
			output.NextCursor = encodeIntegrationMCPCursor("connection", page.NextCursor.UpdatedAt, string(page.NextCursor.ID))
		}
		return nil, output, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_integrations_connection_get", Title: "Get Integration connection", Description: "Get current non-secret scope and latest content-free health.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input integrationConnectionTargetInput) (*mcp.CallToolResult, integrationConnectionDetailOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, read)
		if err != nil {
			return nil, integrationConnectionDetailOutput{}, err
		}
		value, err := s.integrations.GetConnectionDetail(ctx, actor, input.AccountID, input.ConnectionID)
		return nil, integrationConnectionDetailOutput{Connection: value.Connection, Revision: value.Revision, LatestHealth: value.LatestHealth}, integrationError(err)
	})
	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_integrations_connection_create", Title: "Create Integration connection", Description: "Create immutable email or web-publication connector scope. Human manager only.", Annotations: toolAnnotations(false, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input integrationConnectionCreateInput) (*mcp.CallToolResult, integrationsdomain.Connection, error) {
		ctx, op, err := s.integrationMutationContext(ctx, actor, input.AccountID, input.OperationID, mutation, 0, true)
		if err != nil {
			return nil, integrationsdomain.Connection{}, err
		}
		value, _, err := s.integrations.CreateConnection(ctx, integrationsapp.CreateConnectionCommand{Actor: actor, AccountID: input.AccountID, RequestID: op, Name: input.Name, Kind: input.Kind, Capabilities: input.Capabilities, Scope: input.Scope})
		return nil, value, integrationError(err)
	})
	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_integrations_connection_revise", Title: "Revise Integration connection", Description: "Append a new immutable non-secret scope revision. Human manager only.", Annotations: toolAnnotations(false, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input integrationConnectionReviseInput) (*mcp.CallToolResult, integrationsdomain.Connection, error) {
		ctx, op, err := s.integrationMutationContext(ctx, actor, input.AccountID, input.OperationID, mutation, input.ExpectedVersion, false)
		if err != nil {
			return nil, integrationsdomain.Connection{}, err
		}
		value, err := s.integrations.ReviseConnection(ctx, integrationsapp.ReviseConnectionCommand{Actor: actor, AccountID: input.AccountID, RequestID: op, ConnectionID: input.ConnectionID, ExpectedVersion: input.ExpectedVersion, Name: input.Name, Capabilities: input.Capabilities, Scope: input.Scope})
		return nil, value, integrationError(err)
	})
	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_integrations_credential_activate", Title: "Activate Integration credential binding", Description: "Bind only a reviewed secret-broker reference attestation. No credential material is accepted.", Annotations: toolAnnotations(false, true)}, func(ctx context.Context, _ *mcp.CallToolRequest, input integrationCredentialInput) (*mcp.CallToolResult, integrationsdomain.Connection, error) {
		return s.integrationCredential(ctx, actor, input, mutation, false)
	})
	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_integrations_credential_rotate", Title: "Rotate Integration credential binding", Description: "Monotonically rotate to a reviewed secret-broker reference attestation.", Annotations: toolAnnotations(false, true)}, func(ctx context.Context, _ *mcp.CallToolRequest, input integrationCredentialInput) (*mcp.CallToolResult, integrationsdomain.Connection, error) {
		return s.integrationCredential(ctx, actor, input, mutation, true)
	})
	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_integrations_connection_disable", Title: "Disable Integration connection", Description: "Disable new effects while preserving evidence.", Annotations: toolAnnotations(false, true)}, func(ctx context.Context, _ *mcp.CallToolRequest, input integrationTransitionInput) (*mcp.CallToolResult, integrationsdomain.Connection, error) {
		return s.integrationTransition(ctx, actor, input, mutation, "disable")
	})
	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_integrations_connection_enable", Title: "Enable Integration connection", Description: "Re-enable a disabled connection with its current binding.", Annotations: toolAnnotations(false, true)}, func(ctx context.Context, _ *mcp.CallToolRequest, input integrationTransitionInput) (*mcp.CallToolResult, integrationsdomain.Connection, error) {
		return s.integrationTransition(ctx, actor, input, mutation, "enable")
	})
	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_integrations_connection_revoke", Title: "Revoke Integration connection", Description: "Irreversibly revoke the connection and current credential binding.", Annotations: toolAnnotations(false, true)}, func(ctx context.Context, _ *mcp.CallToolRequest, input integrationTransitionInput) (*mcp.CallToolResult, integrationsdomain.Connection, error) {
		return s.integrationTransition(ctx, actor, input, mutation, "revoke")
	})
	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_integrations_health_list", Title: "List Integration health", Description: "List content-free connector health observations.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input integrationHealthListInput) (*mcp.CallToolResult, integrationHealthPageOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, read)
		if err != nil {
			return nil, integrationHealthPageOutput{}, err
		}
		limit, err := integrationLimit(input.Limit)
		if err != nil {
			return nil, integrationHealthPageOutput{}, err
		}
		query := integrationsapp.HealthListQuery{ConnectionID: input.ConnectionID, Limit: limit}
		if input.Cursor != "" {
			cursor, err := decodeIntegrationMCPCursor(input.Cursor, "health")
			if err != nil {
				return nil, integrationHealthPageOutput{}, err
			}
			query.After = &integrationsapp.HealthCursor{CheckedAt: cursor.Date, ID: ids.IntegrationHealthObservationID(cursor.ID)}
		}
		page, err := s.integrations.ListHealth(ctx, actor, input.AccountID, query)
		if err != nil {
			return nil, integrationHealthPageOutput{}, integrationError(err)
		}
		output := integrationHealthPageOutput{Items: page.Items}
		if page.NextCursor != nil {
			output.NextCursor = encodeIntegrationMCPCursor("health", page.NextCursor.CheckedAt, string(page.NextCursor.ID))
		}
		return nil, output, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_integrations_execution_list", Title: "List Integration executions", Description: "List content-free external-effect evidence.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input integrationExecutionListInput) (*mcp.CallToolResult, integrationExecutionPageOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, read)
		if err != nil {
			return nil, integrationExecutionPageOutput{}, err
		}
		limit, err := integrationLimit(input.Limit)
		if err != nil {
			return nil, integrationExecutionPageOutput{}, err
		}
		query := integrationsapp.ExecutionListQuery{ConnectionID: input.ConnectionID, States: input.States, Capabilities: input.Capabilities, Limit: limit}
		if input.Cursor != "" {
			cursor, err := decodeIntegrationMCPCursor(input.Cursor, "execution")
			if err != nil {
				return nil, integrationExecutionPageOutput{}, err
			}
			query.After = &integrationsapp.ExecutionCursor{UpdatedAt: cursor.Date, ID: ids.IntegrationExecutionID(cursor.ID)}
		}
		page, err := s.integrations.ListExecutions(ctx, actor, input.AccountID, query)
		if err != nil {
			return nil, integrationExecutionPageOutput{}, integrationError(err)
		}
		output := integrationExecutionPageOutput{Items: make([]integrationExecutionOutput, len(page.Items))}
		for index, item := range page.Items {
			output.Items[index] = integrationExecutionMCPOutput(item)
		}
		if page.NextCursor != nil {
			output.NextCursor = encodeIntegrationMCPCursor("execution", page.NextCursor.UpdatedAt, string(page.NextCursor.ID))
		}
		return nil, output, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_integrations_execution_get", Title: "Get Integration execution", Description: "Get frozen authority and ordered content-free attempts.", Annotations: toolAnnotations(true, false)}, func(ctx context.Context, _ *mcp.CallToolRequest, input integrationExecutionTargetInput) (*mcp.CallToolResult, integrationExecutionDetailOutput, error) {
		ctx, err := s.toolContext(ctx, actor, input.AccountID, read)
		if err != nil {
			return nil, integrationExecutionDetailOutput{}, err
		}
		value, err := s.integrations.GetExecution(ctx, actor, input.AccountID, input.ExecutionID)
		return nil, integrationExecutionDetailOutput{Execution: integrationExecutionMCPOutput(value.Execution), Attempts: value.Attempts}, integrationError(err)
	})
	mcp.AddTool(server, &mcp.Tool{Name: "spyglass_integrations_execution_prepare", Title: "Prepare Integration execution", Description: "Freeze one approved Marketing delivery against current connector authority. Human manager only.", Annotations: toolAnnotations(false, true)}, func(ctx context.Context, _ *mcp.CallToolRequest, input integrationExecutionPrepareInput) (*mcp.CallToolResult, integrationExecutionOutput, error) {
		ctx, op, err := s.integrationMutationContext(ctx, actor, input.AccountID, input.OperationID, mutation, 0, true)
		if err != nil {
			return nil, integrationExecutionOutput{}, err
		}
		value, _, err := s.integrations.PrepareExecution(ctx, integrationsapp.PrepareExecutionCommand{Actor: actor, AccountID: input.AccountID, RequestID: op, ReleaseID: input.ReleaseID, ReleaseVersion: input.ReleaseVersion, Capability: input.Capability, ConnectionID: input.ConnectionID})
		return nil, integrationExecutionMCPOutput(value), integrationError(err)
	})
}

func (s *Server) integrationCredential(ctx context.Context, actor access.Actor, input integrationCredentialInput, requirement access.Requirement, rotate bool) (*mcp.CallToolResult, integrationsdomain.Connection, error) {
	ctx, op, err := s.integrationMutationContext(ctx, actor, input.AccountID, input.OperationID, requirement, input.ExpectedVersion, false)
	if err != nil {
		return nil, integrationsdomain.Connection{}, err
	}
	digest, err := integrationDigest(input.ReferenceSHA256)
	if err != nil {
		return nil, integrationsdomain.Connection{}, err
	}
	command := integrationsapp.CredentialCommand{Actor: actor, AccountID: input.AccountID, RequestID: op, ConnectionID: input.ConnectionID, ExpectedVersion: input.ExpectedVersion, ExpectedGeneration: input.ExpectedGeneration, Provider: input.Provider, ReferenceSHA256: digest, CredentialExpiresAt: input.ExpiresAt}
	var value integrationsdomain.Connection
	if rotate {
		value, err = s.integrations.RotateCredential(ctx, command)
	} else {
		value, err = s.integrations.ActivateConnection(ctx, command)
	}
	return nil, value, integrationError(err)
}
func (s *Server) integrationTransition(ctx context.Context, actor access.Actor, input integrationTransitionInput, requirement access.Requirement, kind string) (*mcp.CallToolResult, integrationsdomain.Connection, error) {
	ctx, op, err := s.integrationMutationContext(ctx, actor, input.AccountID, input.OperationID, requirement, input.ExpectedVersion, false)
	if err != nil {
		return nil, integrationsdomain.Connection{}, err
	}
	command := integrationsapp.TransitionCommand{Actor: actor, AccountID: input.AccountID, RequestID: op, ConnectionID: input.ConnectionID, ExpectedVersion: input.ExpectedVersion}
	var value integrationsdomain.Connection
	if kind == "disable" {
		value, err = s.integrations.DisableConnection(ctx, command)
	} else if kind == "enable" {
		value, err = s.integrations.EnableConnection(ctx, command)
	} else {
		value, err = s.integrations.RevokeConnection(ctx, command)
	}
	return nil, value, integrationError(err)
}
func (s *Server) integrationMutationContext(ctx context.Context, actor access.Actor, accountID ids.AccountID, operation string, requirement access.Requirement, version uint64, allowZero bool) (context.Context, string, error) {
	ctx, err := s.toolContext(ctx, actor, accountID, requirement)
	if err != nil {
		return nil, "", err
	}
	op, err := operationID(operation)
	if err != nil {
		return nil, "", err
	}
	if !allowZero && version == 0 {
		return nil, "", safeError("integration_version_required")
	}
	return ctx, op, nil
}
func integrationLimit(value int) (int, error) {
	if value == 0 {
		return integrationsapp.DefaultConnectionPageSize, nil
	}
	if value < 1 || value > integrationsapp.MaximumConnectionPageSize {
		return 0, safeError("invalid_integration_limit")
	}
	return value, nil
}
func integrationDigest(raw string) ([32]byte, error) {
	decoded, err := hex.DecodeString(raw)
	if err != nil || len(decoded) != 32 || raw != strings.ToLower(raw) {
		return [32]byte{}, safeError("invalid_integration_digest")
	}
	var value [32]byte
	copy(value[:], decoded)
	return value, nil
}
func integrationExecutionMCPOutput(value integrationsdomain.Execution) integrationExecutionOutput {
	return integrationExecutionOutput{ID: value.ID, AccountID: value.AccountID, ReleaseID: value.ReleaseID, ReleaseVersion: value.ReleaseVersion, ApprovalID: value.ApprovalID, Capability: value.Capability, ConnectionID: value.ConnectionID, ConnectionRevisionID: value.ConnectionRevisionID, ConnectionRevision: value.ConnectionRevision, CredentialID: value.CredentialID, CredentialGeneration: value.CredentialGeneration, PayloadSHA256: hex.EncodeToString(value.PayloadSHA256[:]), State: value.State, AttemptCount: value.AttemptCount, CurrentAttemptID: value.CurrentAttemptID, LastErrorCode: value.LastErrorCode, LeaseExpiresAt: value.LeaseExpiresAt, NextAttemptAt: value.NextAttemptAt, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, CompletedAt: value.CompletedAt}
}

type integrationMCPCursor struct {
	Version int       `json:"v"`
	Kind    string    `json:"kind"`
	Date    time.Time `json:"date"`
	ID      string    `json:"id"`
}

func encodeIntegrationMCPCursor(kind string, date time.Time, id string) string {
	raw, _ := json.Marshal(integrationMCPCursor{Version: 1, Kind: kind, Date: date.UTC(), ID: id})
	return base64.RawURLEncoding.EncodeToString(raw)
}
func decodeIntegrationMCPCursor(raw, kind string) (integrationMCPCursor, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) > 1024 {
		return integrationMCPCursor{}, safeError("invalid_integration_cursor")
	}
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	var value integrationMCPCursor
	if decoder.Decode(&value) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) || value.Version != 1 || value.Kind != kind || value.Date.IsZero() || ids.Validate(value.ID) != nil {
		return integrationMCPCursor{}, safeError("invalid_integration_cursor")
	}
	return value, nil
}
func integrationError(err error) error {
	var denied *access.DeniedError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, integrationsapp.ErrInvalid):
		return safeError("invalid_integration_command")
	case errors.Is(err, integrationsapp.ErrNotFound):
		return safeError("integration_record_not_found")
	case errors.Is(err, integrationsapp.ErrConflict):
		return safeError("integration_version_conflict")
	case errors.As(err, &denied):
		return safeError(string(denied.Code))
	default:
		return safeError("integrations_unavailable")
	}
}

var _ IntegrationsService = (*integrationsapp.Service)(nil)

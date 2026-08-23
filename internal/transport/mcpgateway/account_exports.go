package mcpgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountexport"
	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/requestbody"
)

const (
	accountExportListTool     = "spyglass_account_export_list"
	accountExportGetTool      = "spyglass_account_export_get"
	accountExportRequestTool  = "spyglass_account_export_request"
	accountExportCancelTool   = "spyglass_account_export_cancel"
	accountExportDownloadTool = "spyglass_account_export_download_capability_create"
)

type AccountExportService interface {
	Create(context.Context, accountexport.CreateCommand) (accountexport.Status, error)
	Get(context.Context, ids.AccountID, ids.UserID, string) (accountexport.Status, error)
	List(context.Context, ids.AccountID, ids.UserID, uint64) ([]accountexport.Status, error)
	Cancel(context.Context, accountexport.CancelCommand) (accountexport.Status, error)
}

type ExportCapabilityIssuer interface {
	Issue(context.Context, accountexport.CapabilityCommand) (accountexport.Capability, error)
}

type accountExportTools struct {
	service      AccountExportService
	capabilities ExportCapabilityIssuer
	appOrigin    string
	definitions  []map[string]any
	logger       *slog.Logger
}

func newAccountExportTools(service AccountExportService, capabilities ExportCapabilityIssuer, appOrigin string, logger *slog.Logger) (*accountExportTools, error) {
	configured := service != nil || capabilities != nil || strings.TrimSpace(appOrigin) != ""
	if !configured {
		return nil, nil
	}
	parsed, err := url.Parse(strings.TrimSpace(appOrigin))
	if service == nil || capabilities == nil || logger == nil || err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("MCP Account-export tools require canonical app origin and complete dependencies")
	}
	tools := &accountExportTools{service: service, capabilities: capabilities, appOrigin: parsed.String(), logger: logger}
	tools.definitions = tools.toolDefinitions()
	return tools, nil
}

func (tools *accountExportTools) handles(name string) bool {
	_, ok := tools.requirement(name)
	return ok
}

func (tools *accountExportTools) requirement(name string) (access.Requirement, bool) {
	mutation := false
	switch name {
	case accountExportListTool, accountExportGetTool:
	case accountExportRequestTool, accountExportCancelTool, accountExportDownloadTool:
		mutation = true
	default:
		return access.Requirement{}, false
	}
	return access.Requirement{Roles: []accounts.MembershipRole{accounts.RoleOwner}, Mutation: mutation}, true
}

func (tools *accountExportTools) call(w http.ResponseWriter, r *http.Request, body *requestbody.Capture, principal Principal, account access.AccountContext, requestID string) {
	envelope, call, err := decodeGlobalCall(body)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_mcp_request")
		return
	}
	session := sessions.Session{UserID: principal.Actor.UserID}
	if principal.StrongAuthenticatedAt != nil {
		session.ReauthenticatedAt = principal.StrongAuthenticatedAt.UTC()
		session.ReauthenticationMethod = sessions.AuthenticationMethodPasskey
	}
	var output any
	switch call.Name {
	case accountExportListTool:
		var input struct {
			AccountID ids.AccountID `json:"account_id"`
			Limit     uint64        `json:"limit,omitempty"`
		}
		if decodeToolArguments(call.Arguments, &input) != nil {
			err = accountexport.ErrInvalid
			break
		}
		if input.Limit == 0 {
			input.Limit = 25
		}
		var values []accountexport.Status
		values, err = tools.service.List(r.Context(), account.AccountID, principal.Actor.UserID, input.Limit)
		output = map[string]any{"exports": values}
	case accountExportGetTool:
		var input exportTargetInput
		if decodeToolArguments(call.Arguments, &input) != nil {
			err = accountexport.ErrInvalid
			break
		}
		output, err = tools.service.Get(r.Context(), account.AccountID, principal.Actor.UserID, input.ExportID)
	case accountExportRequestTool:
		var input accountOnlyInput
		if decodeToolArguments(call.Arguments, &input) != nil {
			err = accountexport.ErrInvalid
			break
		}
		output, err = tools.service.Create(r.Context(), accountexport.CreateCommand{AccountID: account.AccountID, Actor: principal.Actor.UserID, Session: session})
	case accountExportCancelTool:
		var input struct {
			AccountID       ids.AccountID `json:"account_id"`
			ExportID        string        `json:"export_id"`
			ExpectedVersion uint64        `json:"expected_version"`
		}
		if decodeToolArguments(call.Arguments, &input) != nil {
			err = accountexport.ErrInvalid
			break
		}
		output, err = tools.service.Cancel(r.Context(), accountexport.CancelCommand{AccountID: account.AccountID, Actor: principal.Actor.UserID, ID: input.ExportID, ExpectedVersion: input.ExpectedVersion, Session: session})
	case accountExportDownloadTool:
		var input exportTargetInput
		if decodeToolArguments(call.Arguments, &input) != nil {
			err = accountexport.ErrInvalid
			break
		}
		var capability accountexport.Capability
		capability, err = tools.capabilities.Issue(r.Context(), accountexport.CapabilityCommand{AccountID: account.AccountID, Actor: principal.Actor.UserID, ExportID: input.ExportID, Session: session})
		if err == nil {
			output = map[string]any{"export_id": input.ExportID, "artifact_url": fmt.Sprintf("%s/api/v1/account-exports/%s/artifact", tools.appOrigin, input.ExportID), "authorization_scheme": "SPYGLASS-ACCOUNT-EXPORT", "token": capability.Token, "expires_at": capability.ExpiresAt}
		}
	default:
		err = accountexport.ErrInvalid
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Request-ID", requestID)
	if err != nil {
		tools.logger.Info("MCP Account-export tool denied", "request_id", requestID, "account_id", account.AccountID, "tool", call.Name, "code", accountExportError(err))
		writeToolResult(w, envelope.ID, nil, accountExportError(err), true, false)
		return
	}
	tools.logger.Info("MCP Account-export tool completed", "request_id", requestID, "account_id", account.AccountID, "tool", call.Name)
	writeToolResult(w, envelope.ID, output, "", false, call.Name == accountExportDownloadTool)
}

type accountOnlyInput struct {
	AccountID ids.AccountID `json:"account_id"`
}

type exportTargetInput struct {
	AccountID ids.AccountID `json:"account_id"`
	ExportID  string        `json:"export_id"`
}

func decodeGlobalCall(body *requestbody.Capture) (rpcEnvelope, callParams, error) {
	reader, err := body.Open()
	if err != nil {
		return rpcEnvelope{}, callParams{}, err
	}
	defer reader.Close()
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var envelope rpcEnvelope
	if decoder.Decode(&envelope) != nil || envelope.JSONRPC != "2.0" || envelope.Method != "tools/call" || len(envelope.ID) == 0 || string(envelope.ID) == "null" {
		return rpcEnvelope{}, callParams{}, errors.New("invalid global tool call")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return rpcEnvelope{}, callParams{}, errors.New("multiple JSON values")
	}
	var call callParams
	if json.Unmarshal(envelope.Params, &call) != nil || call.Name == "" || len(call.Arguments) == 0 {
		return rpcEnvelope{}, callParams{}, errors.New("invalid tool parameters")
	}
	return envelope, call, nil
}

func decodeToolArguments(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return ensureJSONEOF(decoder)
}

func ensureJSONEOF(decoder *json.Decoder) error {
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values")
	}
	return nil
}

func writeToolResult(w http.ResponseWriter, id json.RawMessage, output any, safeCode string, isError, capability bool) {
	result := map[string]any{"isError": isError}
	if isError {
		result["content"] = []map[string]string{{"type": "text", "text": safeCode}}
	} else {
		result["structuredContent"] = output
		text := "Account export download capability issued. Send it only in the Authorization header to artifact_url."
		if !capability {
			raw, _ := json.Marshal(output)
			text = string(raw)
		}
		result["content"] = []map[string]string{{"type": "text", "text": text}}
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func accountExportError(err error) string {
	var denied *access.DeniedError
	switch {
	case errors.Is(err, strongauth.ErrRequired):
		return "strong_authentication_required"
	case errors.Is(err, accountexport.ErrNotFound):
		return "account_export_not_found"
	case errors.Is(err, accountexport.ErrStateConflict):
		return "account_export_state_conflict"
	case errors.Is(err, accountexport.ErrArtifactUnavailable):
		return "account_export_unavailable"
	case errors.Is(err, accountexport.ErrInvalid):
		return "invalid_account_export"
	case errors.As(err, &denied):
		return string(denied.Code)
	default:
		return "account_export_failed"
	}
}

func (tools *accountExportTools) mergeList(raw []byte) ([]byte, error) {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil {
		return nil, errors.New("invalid JSON-RPC response")
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(envelope["result"], &result) != nil {
		return nil, errors.New("missing tools result")
	}
	var existing []json.RawMessage
	if json.Unmarshal(result["tools"], &existing) != nil {
		return nil, errors.New("missing tool list")
	}
	for _, definition := range tools.definitions {
		encoded, _ := json.Marshal(definition)
		existing = append(existing, encoded)
	}
	slices.SortFunc(existing, func(left, right json.RawMessage) int {
		return strings.Compare(toolName(left), toolName(right))
	})
	encodedTools, _ := json.Marshal(existing)
	result["tools"] = encodedTools
	encodedResult, _ := json.Marshal(result)
	envelope["result"] = encodedResult
	return json.Marshal(envelope)
}

func toolName(raw json.RawMessage) string {
	var value struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(raw, &value)
	return value.Name
}

func (tools *accountExportTools) toolDefinitions() []map[string]any {
	stringProperty := map[string]any{"type": "string"}
	account := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"account_id"}, "properties": map[string]any{"account_id": stringProperty}}
	target := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"account_id", "export_id"}, "properties": map[string]any{"account_id": stringProperty, "export_id": stringProperty}}
	cancel := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"account_id", "export_id", "expected_version"}, "properties": map[string]any{"account_id": stringProperty, "export_id": stringProperty, "expected_version": map[string]any{"type": "integer", "minimum": 1}}}
	list := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"account_id"}, "properties": map[string]any{"account_id": stringProperty, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}}}
	status := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"id", "account_id", "requested_by", "state", "cell_id", "placement_generation", "account_version", "attempt_count", "version", "requested_at", "expires_at"},
		"properties": map[string]any{
			"id":                   stringProperty,
			"account_id":           stringProperty,
			"requested_by":         stringProperty,
			"state":                map[string]any{"type": "string", "enum": []string{"queued", "building", "available", "failed", "deleting", "deleted", "canceled"}},
			"cell_id":              stringProperty,
			"placement_generation": map[string]any{"type": "integer", "minimum": 1},
			"account_version":      map[string]any{"type": "integer", "minimum": 1},
			"attempt_count":        map[string]any{"type": "integer", "minimum": 0},
			"error_code":           stringProperty,
			"artifact_bytes":       map[string]any{"type": "integer", "minimum": 1},
			"version":              map[string]any{"type": "integer", "minimum": 1},
			"requested_at":         map[string]any{"type": "string", "format": "date-time"},
			"expires_at":           map[string]any{"type": "string", "format": "date-time"},
			"available_at":         map[string]any{"type": "string", "format": "date-time"},
			"deleted_at":           map[string]any{"type": "string", "format": "date-time"},
		},
	}
	page := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"exports"}, "properties": map[string]any{"exports": map[string]any{"type": "array", "items": status}}}
	capability := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"export_id", "artifact_url", "authorization_scheme", "token", "expires_at"}, "properties": map[string]any{"export_id": stringProperty, "artifact_url": stringProperty, "authorization_scheme": stringProperty, "token": stringProperty, "expires_at": map[string]any{"type": "string", "format": "date-time"}}}
	definition := func(name, title, description string, input, output map[string]any, readOnly, destructive, idempotent bool) map[string]any {
		return map[string]any{"name": name, "title": title, "description": description, "inputSchema": input, "outputSchema": output, "annotations": map[string]any{"readOnlyHint": readOnly, "destructiveHint": destructive, "idempotentHint": idempotent, "openWorldHint": false}}
	}
	return []map[string]any{
		definition(accountExportCancelTool, "Cancel Account export", "Cancel a queued Account portability export using its exact version. Requires recent passkey authorization captured at OAuth consent.", cancel, status, false, true, false),
		definition(accountExportDownloadTool, "Create Account export download capability", "Create a short-lived header-only capability for an available Account export. Requires recent passkey authorization captured at OAuth consent.", target, capability, false, false, false),
		definition(accountExportGetTool, "Get Account export", "Get one redacted Account portability export status without artifact internals.", target, status, true, false, true),
		definition(accountExportListTool, "List Account exports", "List a bounded Account portability export history without artifact internals.", list, page, true, false, true),
		definition(accountExportRequestTool, "Request Account export", "Request a complete Account portability export. Requires recent passkey authorization captured at OAuth consent.", account, status, false, false, false),
	}
}

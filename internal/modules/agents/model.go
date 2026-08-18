// Package agents owns the durable customer-visible concepts used to plan and
// record Spyglass agent work. Provider adapters and workflow engines consume
// immutable snapshots from this package; they do not define agent policy.
package agents

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	MaximumPersonasPerRun  = 32
	MaximumToolsPerPersona = 32
	MaximumInstructions    = 32 << 10
	MaximumResultBytes     = 256 << 10
	MaximumListItems       = 100
	MaximumToolSteps       = 5
)

var (
	ErrInvalidBoardroom = errors.New("agent boardroom is invalid")
	ErrInvalidPersona   = errors.New("agent persona version is invalid")
	ErrInvalidRunPlan   = errors.New("agent run plan is invalid")
	ErrInvalidResult    = errors.New("agent result is invalid")
	ErrImmutableVersion = errors.New("agent persona version content is immutable")
	validCode           = regexp.MustCompile(`^[a-z][a-z0-9._:-]{0,127}$`)
	validToolName       = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,63}$`)
)

type BoardroomState string

const (
	BoardroomActive   BoardroomState = "active"
	BoardroomArchived BoardroomState = "archived"
)

type Boardroom struct {
	ID        ids.BoardroomID
	AccountID ids.AccountID
	Name      string
	Purpose   string
	State     BoardroomState
	Version   uint64
	CreatedAt time.Time
	UpdatedAt time.Time
}

func NewBoardroom(id ids.BoardroomID, accountID ids.AccountID, name, purpose string, now time.Time) (Boardroom, error) {
	boardroom := Boardroom{ID: id, AccountID: accountID, Name: strings.TrimSpace(name), Purpose: strings.TrimSpace(purpose), State: BoardroomActive, Version: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
	return RestoreBoardroom(boardroom)
}

func RestoreBoardroom(boardroom Boardroom) (Boardroom, error) {
	boardroom.Name, boardroom.Purpose = strings.TrimSpace(boardroom.Name), strings.TrimSpace(boardroom.Purpose)
	boardroom.CreatedAt, boardroom.UpdatedAt = boardroom.CreatedAt.UTC(), boardroom.UpdatedAt.UTC()
	if ids.Validate(string(boardroom.ID)) != nil || ids.Validate(string(boardroom.AccountID)) != nil || len(boardroom.Name) < 2 || len(boardroom.Name) > 160 || len(boardroom.Purpose) > 2000 ||
		(boardroom.State != BoardroomActive && boardroom.State != BoardroomArchived) || boardroom.Version == 0 || boardroom.CreatedAt.IsZero() || boardroom.UpdatedAt.Before(boardroom.CreatedAt) {
		return Boardroom{}, ErrInvalidBoardroom
	}
	return boardroom, nil
}

type ToolGrant struct {
	Name        string          `json:"name"`
	Capability  string          `json:"capability"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type PersonaPolicy struct {
	Provider            string          `json:"provider"`
	Model               string          `json:"model"`
	ReasoningEffort     string          `json:"reasoning_effort,omitempty"`
	MaximumInputTokens  int64           `json:"maximum_input_tokens"`
	MaximumOutputTokens int64           `json:"maximum_output_tokens"`
	MaximumCostMicros   int64           `json:"maximum_cost_micros"`
	MaximumToolSteps    int             `json:"maximum_tool_steps"`
	CitationPolicy      string          `json:"citation_policy"`
	ActionPolicy        string          `json:"action_policy"`
	Tools               []ToolGrant     `json:"tools"`
	OutputSchema        json.RawMessage `json:"output_schema"`
}

type PersonaVersionDraft struct {
	ID                 ids.PersonaVersionID
	PersonaID          ids.PersonaID
	AccountID          ids.AccountID
	Version            uint64
	Name               string
	Role               string
	Description        string
	SystemInstructions string
	Policy             PersonaPolicy
	CreatedBy          ids.UserID
	CreatedAt          time.Time
}

type PersonaVersion struct {
	PersonaVersionDraft
	ContentDigest [sha256.Size]byte
}

func NewPersonaVersion(draft PersonaVersionDraft) (PersonaVersion, error) {
	canonical, err := canonicalPersonaDraft(draft)
	if err != nil {
		return PersonaVersion{}, err
	}
	digest, err := personaDigest(canonical)
	if err != nil {
		return PersonaVersion{}, err
	}
	return PersonaVersion{PersonaVersionDraft: canonical, ContentDigest: digest}, nil
}

func RestorePersonaVersion(version PersonaVersion) (PersonaVersion, error) {
	canonical, err := NewPersonaVersion(version.PersonaVersionDraft)
	if err != nil {
		return PersonaVersion{}, err
	}
	if canonical.ContentDigest != version.ContentDigest {
		return PersonaVersion{}, ErrImmutableVersion
	}
	return canonical, nil
}

func canonicalPersonaDraft(draft PersonaVersionDraft) (PersonaVersionDraft, error) {
	draft.Policy.Tools = append([]ToolGrant(nil), draft.Policy.Tools...)
	draft.Name, draft.Role, draft.Description, draft.SystemInstructions = strings.TrimSpace(draft.Name), strings.TrimSpace(draft.Role), strings.TrimSpace(draft.Description), strings.TrimSpace(draft.SystemInstructions)
	draft.Policy.Provider, draft.Policy.Model, draft.Policy.ReasoningEffort = strings.TrimSpace(draft.Policy.Provider), strings.TrimSpace(draft.Policy.Model), strings.TrimSpace(draft.Policy.ReasoningEffort)
	draft.Policy.CitationPolicy, draft.Policy.ActionPolicy = strings.TrimSpace(draft.Policy.CitationPolicy), strings.TrimSpace(draft.Policy.ActionPolicy)
	draft.CreatedAt = draft.CreatedAt.UTC()
	if ids.Validate(string(draft.ID)) != nil || ids.Validate(string(draft.PersonaID)) != nil || ids.Validate(string(draft.AccountID)) != nil || ids.Validate(string(draft.CreatedBy)) != nil || draft.Version == 0 || draft.CreatedAt.IsZero() ||
		len(draft.Name) < 2 || len(draft.Name) > 120 || len(draft.Role) < 2 || len(draft.Role) > 160 || len(draft.Description) > 4000 || len(draft.SystemInstructions) < 20 || len(draft.SystemInstructions) > MaximumInstructions ||
		!validCode.MatchString(draft.Policy.Provider) || !validCode.MatchString(draft.Policy.Model) || (draft.Policy.ReasoningEffort != "" && !validCode.MatchString(draft.Policy.ReasoningEffort)) ||
		draft.Policy.MaximumInputTokens < 1 || draft.Policy.MaximumInputTokens > 2_000_000 || draft.Policy.MaximumOutputTokens < 1 || draft.Policy.MaximumOutputTokens > 32_768 || draft.Policy.MaximumCostMicros < 0 || draft.Policy.MaximumCostMicros > 1_000_000_000 ||
		draft.Policy.MaximumToolSteps < 0 || draft.Policy.MaximumToolSteps > MaximumToolSteps || !slices.Contains([]string{"none", "required", "best_effort"}, draft.Policy.CitationPolicy) || !slices.Contains([]string{"none", "propose"}, draft.Policy.ActionPolicy) || len(draft.Policy.Tools) > MaximumToolsPerPersona {
		return PersonaVersionDraft{}, ErrInvalidPersona
	}
	outputSchema, err := canonicalObject(draft.Policy.OutputSchema, 64<<10)
	if err != nil {
		return PersonaVersionDraft{}, ErrInvalidPersona
	}
	draft.Policy.OutputSchema = outputSchema
	toolNames, capabilities := map[string]struct{}{}, map[string]struct{}{}
	for index := range draft.Policy.Tools {
		tool := &draft.Policy.Tools[index]
		tool.Name, tool.Capability, tool.Description = strings.TrimSpace(tool.Name), strings.TrimSpace(tool.Capability), strings.TrimSpace(tool.Description)
		if !validToolName.MatchString(tool.Name) || !validCode.MatchString(tool.Capability) || len(tool.Description) < 2 || len(tool.Description) > 4000 {
			return PersonaVersionDraft{}, ErrInvalidPersona
		}
		if _, duplicate := toolNames[tool.Name]; duplicate {
			return PersonaVersionDraft{}, ErrInvalidPersona
		}
		if _, duplicate := capabilities[tool.Capability]; duplicate {
			return PersonaVersionDraft{}, ErrInvalidPersona
		}
		toolNames[tool.Name], capabilities[tool.Capability] = struct{}{}, struct{}{}
		tool.InputSchema, err = canonicalObject(tool.InputSchema, 64<<10)
		if err != nil {
			return PersonaVersionDraft{}, ErrInvalidPersona
		}
	}
	if draft.Policy.MaximumToolSteps == 0 && len(draft.Policy.Tools) != 0 {
		return PersonaVersionDraft{}, ErrInvalidPersona
	}
	if draft.Policy.MaximumToolSteps > 0 && len(draft.Policy.Tools) == 0 {
		return PersonaVersionDraft{}, ErrInvalidPersona
	}
	return draft, nil
}

func personaDigest(draft PersonaVersionDraft) ([sha256.Size]byte, error) {
	content := struct {
		ID                 ids.PersonaVersionID `json:"id"`
		PersonaID          ids.PersonaID        `json:"persona_id"`
		AccountID          ids.AccountID        `json:"account_id"`
		Version            uint64               `json:"version"`
		Name               string               `json:"name"`
		Role               string               `json:"role"`
		Description        string               `json:"description"`
		SystemInstructions string               `json:"system_instructions"`
		Policy             PersonaPolicy        `json:"policy"`
	}{draft.ID, draft.PersonaID, draft.AccountID, draft.Version, draft.Name, draft.Role, draft.Description, draft.SystemInstructions, draft.Policy}
	raw, err := json.Marshal(content)
	if err != nil {
		return [sha256.Size]byte{}, ErrInvalidPersona
	}
	return sha256.Sum256(raw), nil
}

type PlannedTurn struct {
	Turn             uint32
	PersonaID        ids.PersonaID
	PersonaVersionID ids.PersonaVersionID
	PersonaDigest    [sha256.Size]byte
}

type RunPlan struct {
	RunID              ids.RunID
	AccountID          ids.AccountID
	BoardroomID        ids.BoardroomID
	ConversationID     ids.ConversationID
	EntitlementVersion uint64
	PolicyVersion      uint64
	Turns              []PlannedTurn
	CreatedBy          ids.UserID
	CreatedAt          time.Time
	Digest             [sha256.Size]byte
}

func NewRunPlan(plan RunPlan) (RunPlan, error) {
	plan.CreatedAt = plan.CreatedAt.UTC()
	if ids.Validate(string(plan.RunID)) != nil || ids.Validate(string(plan.AccountID)) != nil || ids.Validate(string(plan.BoardroomID)) != nil || ids.Validate(string(plan.ConversationID)) != nil || ids.Validate(string(plan.CreatedBy)) != nil ||
		plan.EntitlementVersion == 0 || plan.PolicyVersion == 0 || plan.CreatedAt.IsZero() || len(plan.Turns) == 0 || len(plan.Turns) > MaximumPersonasPerRun {
		return RunPlan{}, ErrInvalidRunPlan
	}
	personas := make(map[ids.PersonaID]struct{}, len(plan.Turns))
	versions := make(map[ids.PersonaVersionID]struct{}, len(plan.Turns))
	for index, turn := range plan.Turns {
		if turn.Turn != uint32(index+1) || ids.Validate(string(turn.PersonaID)) != nil || ids.Validate(string(turn.PersonaVersionID)) != nil || turn.PersonaDigest == ([sha256.Size]byte{}) {
			return RunPlan{}, ErrInvalidRunPlan
		}
		if _, exists := personas[turn.PersonaID]; exists {
			return RunPlan{}, ErrInvalidRunPlan
		}
		if _, exists := versions[turn.PersonaVersionID]; exists {
			return RunPlan{}, ErrInvalidRunPlan
		}
		personas[turn.PersonaID], versions[turn.PersonaVersionID] = struct{}{}, struct{}{}
	}
	copyPlan := plan
	copyPlan.Turns = append([]PlannedTurn(nil), plan.Turns...)
	digestInput := struct {
		RunID              ids.RunID          `json:"run_id"`
		AccountID          ids.AccountID      `json:"account_id"`
		BoardroomID        ids.BoardroomID    `json:"boardroom_id"`
		ConversationID     ids.ConversationID `json:"conversation_id"`
		EntitlementVersion uint64             `json:"entitlement_version"`
		PolicyVersion      uint64             `json:"policy_version"`
		Turns              []PlannedTurn      `json:"turns"`
		CreatedBy          ids.UserID         `json:"created_by"`
		CreatedAt          time.Time          `json:"created_at"`
	}{copyPlan.RunID, copyPlan.AccountID, copyPlan.BoardroomID, copyPlan.ConversationID, copyPlan.EntitlementVersion, copyPlan.PolicyVersion, copyPlan.Turns, copyPlan.CreatedBy, copyPlan.CreatedAt}
	raw, err := json.Marshal(digestInput)
	if err != nil {
		return RunPlan{}, ErrInvalidRunPlan
	}
	copyPlan.Digest = sha256.Sum256(raw)
	return copyPlan, nil
}

func RestoreRunPlan(plan RunPlan) (RunPlan, error) {
	restored, err := NewRunPlan(plan)
	if err != nil {
		return RunPlan{}, err
	}
	if restored.Digest != plan.Digest {
		return RunPlan{}, ErrImmutableVersion
	}
	return restored, nil
}

type Citation struct {
	ID         string `json:"id"`
	DocumentID string `json:"document_id"`
	ChunkID    string `json:"chunk_id"`
	Label      string `json:"label"`
}
type ProposedAction struct {
	Kind     string          `json:"kind"`
	Reason   string          `json:"reason"`
	Payload  json.RawMessage `json:"payload"`
	Evidence []string        `json:"evidence"`
}
type Delegation struct {
	PersonaID ids.PersonaID `json:"persona_id"`
	Request   string        `json:"request"`
}
type Confidence string

const (
	ConfidenceLow    Confidence = "low"
	ConfidenceMedium Confidence = "medium"
	ConfidenceHigh   Confidence = "high"
)

type ResultEnvelope struct {
	Contribution    string           `json:"contribution"`
	Findings        []string         `json:"findings"`
	Recommendations []string         `json:"recommendations"`
	Questions       []string         `json:"questions"`
	Citations       []Citation       `json:"citations"`
	ProposedActions []ProposedAction `json:"proposed_actions"`
	Delegations     []Delegation     `json:"delegations"`
	Confidence      Confidence       `json:"confidence"`
}

func ValidateResult(result ResultEnvelope) (ResultEnvelope, error) {
	result.Findings = append([]string(nil), result.Findings...)
	result.Recommendations = append([]string(nil), result.Recommendations...)
	result.Questions = append([]string(nil), result.Questions...)
	result.Citations = append([]Citation(nil), result.Citations...)
	result.ProposedActions = append([]ProposedAction(nil), result.ProposedActions...)
	result.Delegations = append([]Delegation(nil), result.Delegations...)
	for index := range result.ProposedActions {
		result.ProposedActions[index].Evidence = append([]string(nil), result.ProposedActions[index].Evidence...)
	}
	result.Contribution = strings.TrimSpace(result.Contribution)
	if result.Contribution == "" || len(result.Contribution) > 64<<10 || !slices.Contains([]Confidence{ConfidenceLow, ConfidenceMedium, ConfidenceHigh}, result.Confidence) ||
		len(result.Findings) > MaximumListItems || len(result.Recommendations) > MaximumListItems || len(result.Questions) > MaximumListItems || len(result.Citations) > MaximumListItems || len(result.ProposedActions) > MaximumListItems || len(result.Delegations) > MaximumPersonasPerRun {
		return ResultEnvelope{}, ErrInvalidResult
	}
	if !validStringList(result.Findings, 4000) || !validStringList(result.Recommendations, 4000) || !validStringList(result.Questions, 4000) {
		return ResultEnvelope{}, ErrInvalidResult
	}
	for index := range result.Citations {
		item := &result.Citations[index]
		item.ID, item.DocumentID, item.ChunkID, item.Label = strings.TrimSpace(item.ID), strings.TrimSpace(item.DocumentID), strings.TrimSpace(item.ChunkID), strings.TrimSpace(item.Label)
		if item.ID == "" || len(item.ID) > 200 || len(item.DocumentID) > 200 || len(item.ChunkID) > 200 || len(item.Label) > 500 {
			return ResultEnvelope{}, ErrInvalidResult
		}
	}
	for index := range result.ProposedActions {
		action := &result.ProposedActions[index]
		action.Kind, action.Reason = strings.TrimSpace(action.Kind), strings.TrimSpace(action.Reason)
		if !validCode.MatchString(action.Kind) || len(action.Reason) < 3 || len(action.Reason) > 4000 || !validStringList(action.Evidence, 500) {
			return ResultEnvelope{}, ErrInvalidResult
		}
		payload, err := canonicalObject(action.Payload, 64<<10)
		if err != nil {
			return ResultEnvelope{}, ErrInvalidResult
		}
		action.Payload = payload
	}
	for index := range result.Delegations {
		delegation := &result.Delegations[index]
		delegation.Request = strings.TrimSpace(delegation.Request)
		if ids.Validate(string(delegation.PersonaID)) != nil || len(delegation.Request) < 3 || len(delegation.Request) > 4000 {
			return ResultEnvelope{}, ErrInvalidResult
		}
	}
	raw, err := json.Marshal(result)
	if err != nil || len(raw) > MaximumResultBytes {
		return ResultEnvelope{}, ErrInvalidResult
	}
	return result, nil
}

func validStringList(values []string, maximum int) bool {
	for index := range values {
		values[index] = strings.TrimSpace(values[index])
		if values[index] == "" || len(values[index]) > maximum {
			return false
		}
	}
	return true
}

func canonicalObject(raw json.RawMessage, maximum int) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) < 2 || len(trimmed) > maximum || trimmed[0] != '{' {
		return nil, ErrInvalidPersona
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil || value == nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return nil, ErrInvalidPersona
	}
	canonical, err := json.Marshal(value)
	if err != nil || len(canonical) > maximum {
		return nil, ErrInvalidPersona
	}
	return canonical, nil
}

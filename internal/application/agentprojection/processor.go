// Package agentprojection turns authenticated encrypted runner results into
// durable Agent messages and run state. It is the only layer that receives
// both the envelope cipher and projection authority.
package agentprojection

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/agentresultpolicy"
	"github.com/tinfoyle/spyglass-engine/internal/application/agentusage"
	"github.com/tinfoyle/spyglass-engine/internal/application/runneragents"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	agentdomain "github.com/tinfoyle/spyglass-engine/internal/modules/agents"
	"github.com/tinfoyle/spyglass-engine/internal/modules/aitokens"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

const (
	DefaultLease       = 30 * time.Second
	DefaultMaxAttempts = 12
	MaximumMaxAttempts = 100
	maximumRetryDelay  = 15 * time.Minute
)

var (
	ErrInvalidClaim   = errors.New("agent result projection claim is invalid")
	ErrInvalidPayload = errors.New("agent runner result payload is invalid")
	ErrLeaseLost      = errors.New("agent result projection lease was lost")
	validClaimModel   = regexp.MustCompile(`^[a-z][a-z0-9._:-]{0,127}$`)
)

type Claim struct {
	AccountID           ids.AccountID
	InvocationID        string
	LeaseID             string
	Attempt             int
	ExpectedProvider    string
	RequestedModel      string
	PermittedModels     []string
	ResultPolicyVersion uint32
	CitationPolicy      string
	ActionPolicy        string
	ActionCapabilities  []string
	CurrentPersonaID    ids.PersonaID
	WorkItemID          ids.WorkItemID
	DelegatePersonaIDs  []ids.PersonaID
	CitationBindings    []agentresultpolicy.CitationBinding
	Result              runnerbroker.StoredResult
}

func (c Claim) Valid() bool {
	if len(c.PermittedModels) < 1 || len(c.PermittedModels) > 3 || c.PermittedModels[0] != c.RequestedModel {
		return false
	}
	for index, model := range c.PermittedModels {
		if !validClaimModel.MatchString(model) || slices.Contains(c.PermittedModels[:index], model) {
			return false
		}
	}
	if (c.ResultPolicyVersion < agentresultpolicy.LegacyVersion || c.ResultPolicyVersion > agentresultpolicy.CurrentVersion) || ids.Validate(string(c.CurrentPersonaID)) != nil ||
		!slices.Contains([]string{"none", "required", "best_effort"}, c.CitationPolicy) || !slices.Contains([]string{"none", "propose"}, c.ActionPolicy) {
		return false
	}
	if c.WorkItemID != "" && ids.Validate(string(c.WorkItemID)) != nil {
		return false
	}
	for index, capability := range c.ActionCapabilities {
		if !validClaimModel.MatchString(capability) || slices.Contains(c.ActionCapabilities[:index], capability) {
			return false
		}
	}
	for index, personaID := range c.DelegatePersonaIDs {
		if ids.Validate(string(personaID)) != nil || personaID == c.CurrentPersonaID || slices.Contains(c.DelegatePersonaIDs[:index], personaID) {
			return false
		}
	}
	for index, binding := range c.CitationBindings {
		if ids.Validate(binding.DocumentID) != nil || ids.Validate(binding.ChunkID) != nil || slices.Contains(c.CitationBindings[:index], binding) {
			return false
		}
	}
	return ids.Validate(string(c.AccountID)) == nil && ids.Validate(c.InvocationID) == nil && ids.Validate(c.LeaseID) == nil &&
		c.Attempt > 0 && c.ExpectedProvider != "" && c.RequestedModel != "" && c.Result.InvocationID == c.InvocationID &&
		ids.Validate(c.Result.PodUID) == nil && !c.Result.SubmittedAt.IsZero()
}

type Success struct {
	Claim               Claim
	MessageID           string
	Provider            string
	SelectedModel       string
	ResponseModel       string
	ProviderResponseID  string
	RunnerDigest        [sha256.Size]byte
	ResultDigest        [sha256.Size]byte
	ResultPayload       json.RawMessage
	Body                string
	InputTokens         int64
	OutputTokens        int64
	TotalTokens         int64
	CostMicros          int64
	CompletedAt         time.Time
	ProjectedAt         time.Time
	Proposals           []ApprovalProposal
	InformationRequests []InformationRequest
	WorkEventID         string
}

type ApprovalProposal struct {
	ApprovalID       string
	OperationID      string
	EventID          string
	Capability       string
	CanonicalPayload json.RawMessage
	InputDigest      [sha256.Size]byte
	EvidenceDigest   [sha256.Size]byte
	ProposerID       string
	PolicyVersion    uint32
	ExpiresAt        time.Time
}

type InformationRequest struct {
	RequestID   string
	EventID     string
	FactKey     string
	Question    string
	RequesterID string
}

type Failure struct {
	Claim        Claim
	RunnerDigest [sha256.Size]byte
	FailureCode  string
	CompletedAt  time.Time
	ProjectedAt  time.Time
}

type Stats struct {
	Pending        uint64
	Ready          uint64
	Leased         uint64
	Retrying       uint64
	DeadLetter     uint64
	OldestReadyAge time.Duration
}

type Queue interface {
	Claim(context.Context, string, time.Time, time.Duration) (Claim, bool, error)
	ProjectSuccess(context.Context, Success) error
	ProjectFailure(context.Context, Failure) error
	Fail(context.Context, Claim, bool, time.Time, string, time.Time, int) (string, error)
	Stats(context.Context, time.Time) (Stats, error)
}

type Clock interface{ Now() time.Time }

type Result struct {
	Worked     bool
	Projected  bool
	DeadLetter bool
}

type Processor struct {
	queue       Queue
	cipher      *runnerbroker.Cipher
	clock       Clock
	ids         ids.Generator
	lease       time.Duration
	maxAttempts int
	cellID      ids.CellID
	tokens      agentusage.Broker
}

func New(queue Queue, cipher *runnerbroker.Cipher, tokens agentusage.Broker, cellID ids.CellID, clock Clock, generator ids.Generator, lease time.Duration, maxAttempts int) (*Processor, error) {
	if queue == nil || cipher == nil || tokens == nil || !routecontext.ValidCellID(cellID) || clock == nil || generator == nil || lease < time.Second || lease > 30*time.Minute || maxAttempts < 1 || maxAttempts > MaximumMaxAttempts {
		return nil, errors.New("agent projection dependencies or bounds are invalid")
	}
	return &Processor{queue: queue, cipher: cipher, tokens: tokens, cellID: cellID, clock: clock, ids: generator, lease: lease, maxAttempts: maxAttempts}, nil
}

func (p *Processor) ProcessOne(ctx context.Context) (Result, error) {
	now := p.clock.Now().UTC()
	claim, found, err := p.queue.Claim(ctx, p.ids.New(), now, p.lease)
	if err != nil || !found {
		return Result{}, err
	}
	if !claim.Valid() {
		return p.reject(ctx, claim, now, "claim_invalid", ErrInvalidClaim)
	}
	envelope, err := p.cipher.DecodeResult(claim.Result)
	if err != nil {
		return p.reject(ctx, claim, now, "result_envelope_invalid", fmt.Errorf("%w: authenticated envelope", ErrInvalidPayload))
	}
	if envelope.Outcome == "execution_failed" {
		if err := p.closeTokens(ctx, claim, agentusage.Usage{}); err != nil {
			if errors.Is(err, aitokens.ErrInvalidReservation) {
				return p.reject(ctx, claim, now, "token_settlement_invalid", err)
			}
			return p.retry(ctx, claim, now, "token_release_failed", err)
		}
		err = p.queue.ProjectFailure(ctx, Failure{Claim: claim, RunnerDigest: claim.Result.Digest, FailureCode: envelope.ErrorCode, CompletedAt: claim.Result.SubmittedAt.UTC(), ProjectedAt: now})
		if err != nil {
			return p.handleProjectionError(ctx, claim, now, "failure_projection_failed", err)
		}
		return Result{Worked: true, Projected: true}, nil
	}
	output, err := decodeExact[runneragents.TurnOutput](envelope.Output)
	if err != nil {
		return p.failOutput(ctx, claim, now, "turn_output_invalid")
	}
	output, err = runneragents.ValidateTurnOutput(output, claim.ExpectedProvider, claim.PermittedModels)
	if err != nil {
		return p.failOutput(ctx, claim, now, "turn_output_invalid")
	}
	if err := p.closeTokens(ctx, claim, agentusage.Usage{ProviderStarted: true, InputTokens: output.Usage.InputTokens,
		CachedInputTokens: output.Usage.CachedInputTokens, OutputTokens: output.Usage.OutputTokens, ToolInvocations: output.Usage.ToolInvocations}); err != nil {
		if errors.Is(err, aitokens.ErrInvalidReservation) {
			return p.reject(ctx, claim, now, "token_settlement_invalid", err)
		}
		return p.retry(ctx, claim, now, "token_settlement_failed", err)
	}
	if err := agentresultpolicy.Validate(agentresultpolicy.Policy{Version: claim.ResultPolicyVersion, CitationPolicy: claim.CitationPolicy,
		ActionPolicy: claim.ActionPolicy, CurrentPersonaID: claim.CurrentPersonaID, DelegatePersonaIDs: claim.DelegatePersonaIDs,
		ActionCapabilities: claim.ActionCapabilities, CitationBindings: claim.CitationBindings}, output.Result); err != nil {
		return p.failOutput(ctx, claim, now, "result_policy_denied")
	}
	payload, err := json.Marshal(output.Result)
	if err != nil {
		return p.failOutput(ctx, claim, now, "turn_output_invalid")
	}
	success := Success{
		Claim: claim, MessageID: p.ids.New(), Provider: output.Provider, SelectedModel: output.RequestedModel, ResponseModel: output.ResponseModel,
		ProviderResponseID: output.ResponseID, RunnerDigest: claim.Result.Digest, ResultDigest: sha256.Sum256(payload),
		ResultPayload: payload, Body: output.Result.Contribution, InputTokens: output.Usage.InputTokens,
		OutputTokens: output.Usage.OutputTokens, TotalTokens: output.Usage.TotalTokens,
		CostMicros:  output.Usage.CostMicros,
		CompletedAt: claim.Result.SubmittedAt.UTC(), ProjectedAt: now,
	}
	if claim.ResultPolicyVersion >= agentresultpolicy.ActionVersion {
		success.Proposals, err = approvalProposals(claim, output.Result.ProposedActions, success.ResultDigest, success.CompletedAt)
		if err != nil {
			return p.failOutput(ctx, claim, now, "result_policy_denied")
		}
	}
	if claim.ResultPolicyVersion >= agentresultpolicy.CurrentVersion && claim.WorkItemID != "" {
		success.InformationRequests, success.WorkEventID, err = informationRequests(claim, output.Result.Questions)
		if err != nil {
			return p.failOutput(ctx, claim, now, "result_policy_denied")
		}
	}
	if err := p.queue.ProjectSuccess(ctx, success); err != nil {
		return p.handleProjectionError(ctx, claim, now, "success_projection_failed", err)
	}
	return Result{Worked: true, Projected: true}, nil
}

func informationRequests(claim Claim, questions []string) ([]InformationRequest, string, error) {
	if claim.WorkItemID == "" {
		return []InformationRequest{}, "", nil
	}
	workEventID, err := ids.Derive(claim.InvocationID, "work-outcome/event")
	if err != nil {
		return nil, "", err
	}
	if len(questions) == 0 {
		return []InformationRequest{}, workEventID, nil
	}
	requests := make([]InformationRequest, len(questions))
	for index, question := range questions {
		requestID, err := ids.Derive(claim.InvocationID, fmt.Sprintf("question/%d/request", index+1))
		if err != nil {
			return nil, "", err
		}
		eventID, err := ids.Derive(claim.InvocationID, fmt.Sprintf("question/%d/event", index+1))
		if err != nil {
			return nil, "", err
		}
		digest := sha256.Sum256([]byte(question))
		requests[index] = InformationRequest{RequestID: requestID, EventID: eventID,
			FactKey: "agent.owner_question." + hex.EncodeToString(digest[:16]), Question: question,
			RequesterID: "agent:" + string(claim.CurrentPersonaID)}
	}
	return requests, workEventID, nil
}

func approvalProposals(claim Claim, actions []agentdomain.ProposedAction, resultDigest [sha256.Size]byte, now time.Time) ([]ApprovalProposal, error) {
	proposals := make([]ApprovalProposal, len(actions))
	for index, action := range actions {
		approvalID, err := ids.Derive(claim.InvocationID, fmt.Sprintf("action/%d/approval", index+1))
		if err != nil {
			return nil, err
		}
		operationID, err := ids.Derive(claim.InvocationID, fmt.Sprintf("action/%d/operation", index+1))
		if err != nil {
			return nil, err
		}
		eventID, err := ids.Derive(claim.InvocationID, fmt.Sprintf("action/%d/event", index+1))
		if err != nil {
			return nil, err
		}
		inputDigest := sha256.Sum256(action.Payload)
		hash := sha256.New()
		_, _ = hash.Write([]byte("spyglass/action-evidence/v1/"))
		_, _ = hash.Write(resultDigest[:])
		var ordinal [8]byte
		binary.BigEndian.PutUint64(ordinal[:], uint64(index+1))
		_, _ = hash.Write(ordinal[:])
		_, _ = hash.Write([]byte(action.Reason))
		for _, evidence := range action.Evidence {
			_, _ = hash.Write([]byte{0})
			_, _ = hash.Write([]byte(evidence))
		}
		var evidenceDigest [sha256.Size]byte
		copy(evidenceDigest[:], hash.Sum(nil))
		proposals[index] = ApprovalProposal{ApprovalID: approvalID, OperationID: operationID, EventID: eventID,
			Capability: action.Kind, CanonicalPayload: action.Payload, InputDigest: inputDigest, EvidenceDigest: evidenceDigest,
			ProposerID: "agent:" + string(claim.CurrentPersonaID), PolicyVersion: claim.ResultPolicyVersion, ExpiresAt: now.Add(24 * time.Hour)}
	}
	return proposals, nil
}

func (p *Processor) Stats(ctx context.Context) (Stats, error) {
	return p.queue.Stats(ctx, p.clock.Now().UTC())
}

// failOutput records an authenticated runner result that cannot be published as
// a terminal agent failure. No rejected content or proposed actions are exposed.
func (p *Processor) failOutput(ctx context.Context, claim Claim, now time.Time, code string) (Result, error) {
	if err := p.closeTokens(ctx, claim, agentusage.Usage{}); err != nil && !errors.Is(err, aitokens.ErrInvalidReservation) {
		return p.retry(ctx, claim, now, "token_release_failed", err)
	}
	err := p.queue.ProjectFailure(ctx, Failure{Claim: claim, RunnerDigest: claim.Result.Digest, FailureCode: code, CompletedAt: claim.Result.SubmittedAt.UTC(), ProjectedAt: now})
	if err != nil {
		return p.handleProjectionError(ctx, claim, now, "failure_projection_failed", err)
	}
	return Result{Worked: true, Projected: true}, nil
}

func (p *Processor) reject(ctx context.Context, claim Claim, now time.Time, code string, cause error) (Result, error) {
	state, failErr := p.queue.Fail(ctx, claim, false, now, code, now, p.maxAttempts)
	var closeErr error
	if state == "dead_letter" {
		closeErr = p.closeTokens(ctx, claim, agentusage.Usage{})
		if errors.Is(closeErr, aitokens.ErrInvalidReservation) {
			closeErr = nil
		}
	}
	return Result{Worked: true, DeadLetter: state == "dead_letter"}, errors.Join(cause, failErr, closeErr)
}

func (p *Processor) retry(ctx context.Context, claim Claim, now time.Time, code string, cause error) (Result, error) {
	next := now.Add(retryDelay(claim.Attempt))
	state, failErr := p.queue.Fail(ctx, claim, true, next, code, now, p.maxAttempts)
	var closeErr error
	if state == "dead_letter" {
		closeErr = p.closeTokens(ctx, claim, agentusage.Usage{})
		if errors.Is(closeErr, aitokens.ErrInvalidReservation) {
			closeErr = nil
		}
	}
	return Result{Worked: true, DeadLetter: state == "dead_letter"}, errors.Join(cause, failErr, closeErr)
}

func (p *Processor) closeTokens(ctx context.Context, claim Claim, usage agentusage.Usage) error {
	return p.tokens.CloseAgentTokens(ctx, agentusage.CloseCommand{CellID: p.cellID, AccountID: claim.AccountID, InvocationID: claim.InvocationID, Usage: usage})
}

func (p *Processor) handleProjectionError(ctx context.Context, claim Claim, now time.Time, code string, cause error) (Result, error) {
	if errors.Is(cause, ErrInvalidPayload) {
		return p.reject(ctx, claim, now, "projection_rejected", cause)
	}
	if errors.Is(cause, ErrLeaseLost) {
		return Result{Worked: true}, cause
	}
	return p.retry(ctx, claim, now, code, cause)
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Second
	for index := 1; index < attempt && delay < maximumRetryDelay; index++ {
		delay *= 2
	}
	if delay > maximumRetryDelay {
		return maximumRetryDelay
	}
	return delay
}

func decodeExact[T any](raw json.RawMessage) (T, error) {
	var result T
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if len(raw) == 0 || raw[0] != '{' || decoder.Decode(&result) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return result, ErrInvalidPayload
	}
	return result, nil
}

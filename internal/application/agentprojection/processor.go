// Package agentprojection turns authenticated encrypted runner results into
// durable Agent messages and run state. It is the only layer that receives
// both the envelope cipher and projection authority.
package agentprojection

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/runneragents"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
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
	AccountID        ids.AccountID
	InvocationID     string
	LeaseID          string
	Attempt          int
	ExpectedProvider string
	RequestedModel   string
	PermittedModels  []string
	Result           runnerbroker.StoredResult
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
	return ids.Validate(string(c.AccountID)) == nil && ids.Validate(c.InvocationID) == nil && ids.Validate(c.LeaseID) == nil &&
		c.Attempt > 0 && c.ExpectedProvider != "" && c.RequestedModel != "" && c.Result.InvocationID == c.InvocationID &&
		ids.Validate(c.Result.PodUID) == nil && !c.Result.SubmittedAt.IsZero()
}

type Success struct {
	Claim              Claim
	MessageID          string
	Provider           string
	SelectedModel      string
	ResponseModel      string
	ProviderResponseID string
	RunnerDigest       [sha256.Size]byte
	ResultDigest       [sha256.Size]byte
	ResultPayload      json.RawMessage
	Body               string
	InputTokens        int64
	OutputTokens       int64
	TotalTokens        int64
	CostMicros         int64
	CompletedAt        time.Time
	ProjectedAt        time.Time
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
}

func New(queue Queue, cipher *runnerbroker.Cipher, clock Clock, generator ids.Generator, lease time.Duration, maxAttempts int) (*Processor, error) {
	if queue == nil || cipher == nil || clock == nil || generator == nil || lease < time.Second || lease > 30*time.Minute || maxAttempts < 1 || maxAttempts > MaximumMaxAttempts {
		return nil, errors.New("agent projection dependencies or bounds are invalid")
	}
	return &Processor{queue: queue, cipher: cipher, clock: clock, ids: generator, lease: lease, maxAttempts: maxAttempts}, nil
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
		err = p.queue.ProjectFailure(ctx, Failure{Claim: claim, RunnerDigest: claim.Result.Digest, FailureCode: envelope.ErrorCode, CompletedAt: claim.Result.SubmittedAt.UTC(), ProjectedAt: now})
		if err != nil {
			return p.handleProjectionError(ctx, claim, now, "failure_projection_failed", err)
		}
		return Result{Worked: true, Projected: true}, nil
	}
	output, err := decodeExact[runneragents.TurnOutput](envelope.Output)
	if err != nil {
		return p.reject(ctx, claim, now, "turn_output_invalid", ErrInvalidPayload)
	}
	output, err = runneragents.ValidateTurnOutput(output, claim.ExpectedProvider, claim.PermittedModels)
	if err != nil {
		return p.reject(ctx, claim, now, "turn_output_invalid", ErrInvalidPayload)
	}
	payload, err := json.Marshal(output.Result)
	if err != nil {
		return p.reject(ctx, claim, now, "turn_output_invalid", ErrInvalidPayload)
	}
	success := Success{
		Claim: claim, MessageID: p.ids.New(), Provider: output.Provider, SelectedModel: output.RequestedModel, ResponseModel: output.ResponseModel,
		ProviderResponseID: output.ResponseID, RunnerDigest: claim.Result.Digest, ResultDigest: sha256.Sum256(payload),
		ResultPayload: payload, Body: output.Result.Contribution, InputTokens: output.Usage.InputTokens,
		OutputTokens: output.Usage.OutputTokens, TotalTokens: output.Usage.TotalTokens,
		CostMicros:  output.Usage.CostMicros,
		CompletedAt: claim.Result.SubmittedAt.UTC(), ProjectedAt: now,
	}
	if err := p.queue.ProjectSuccess(ctx, success); err != nil {
		return p.handleProjectionError(ctx, claim, now, "success_projection_failed", err)
	}
	return Result{Worked: true, Projected: true}, nil
}

func (p *Processor) Stats(ctx context.Context) (Stats, error) {
	return p.queue.Stats(ctx, p.clock.Now().UTC())
}

func (p *Processor) reject(ctx context.Context, claim Claim, now time.Time, code string, cause error) (Result, error) {
	state, failErr := p.queue.Fail(ctx, claim, false, now, code, now, p.maxAttempts)
	return Result{Worked: true, DeadLetter: state == "dead_letter"}, errors.Join(cause, failErr)
}

func (p *Processor) retry(ctx context.Context, claim Claim, now time.Time, code string, cause error) (Result, error) {
	next := now.Add(retryDelay(claim.Attempt))
	state, failErr := p.queue.Fail(ctx, claim, true, next, code, now, p.maxAttempts)
	return Result{Worked: true, DeadLetter: state == "dead_letter"}, errors.Join(cause, failErr)
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

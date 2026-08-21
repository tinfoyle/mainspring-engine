// Package workagentexecution owns the durable bridge from Persona-assigned
// Work to one Agent Run. Agent dispatch begins only after this boundary has
// atomically created and linked the Run; it never substitutes for this state.
package workagentexecution

import (
	"context"
	"errors"
	"strings"
	"time"

	agentdomain "github.com/tinfoyle/spyglass-engine/internal/modules/agents"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	DefaultLease       = 30 * time.Second
	DefaultMaxAttempts = 12
	MaximumMaxAttempts = 100
	maximumRetryDelay  = 15 * time.Minute
)

var (
	ErrInvalidClaim          = errors.New("Work Agent execution claim is invalid")
	ErrInvalidSnapshot       = errors.New("Work Agent execution snapshot is invalid")
	ErrAuthorizationDenied   = errors.New("Work Agent execution authorization is denied")
	ErrAuthorizationStale    = errors.New("Work Agent execution authorization is stale")
	ErrAuthorizationService  = errors.New("Work Agent execution authorization is unavailable")
	ErrWorkChanged           = errors.New("Work Agent execution input changed")
	ErrPersonaUnavailable    = errors.New("Work Agent execution Persona is unavailable")
	ErrRunCapacity           = errors.New("Work Agent execution Run capacity is unavailable")
	ErrLeaseLost             = errors.New("Work Agent execution lease was lost")
	ErrAtomicStartLinkFailed = errors.New("Work Agent execution atomic start/link failed")
)

type Claim struct {
	ExecutionID string
	AccountID   ids.AccountID
	WorkItemID  ids.WorkItemID
	LeaseID     string
	Attempt     int
}

func (claim Claim) Valid() bool {
	return ids.Validate(claim.ExecutionID) == nil && ids.Validate(string(claim.AccountID)) == nil &&
		ids.Validate(string(claim.WorkItemID)) == nil && ids.Validate(claim.LeaseID) == nil && claim.Attempt > 0
}

// Snapshot is immutable execution intent. UserID is the human who made the
// Persona assignment; the worker never presents itself as that User.
type Snapshot struct {
	ExecutionID      string
	AccountID        ids.AccountID
	WorkItemID       ids.WorkItemID
	WorkVersion      uint64
	UserID           ids.UserID
	PersonaID        ids.PersonaID
	BoardroomID      ids.BoardroomID
	BoardroomVersion uint64
	RunID            ids.RunID
	ConversationID   ids.ConversationID
	Title            string
	Description      string
	Persona          agentdomain.PersonaVersion
	QueuedAt         time.Time
}

func (snapshot Snapshot) Valid() bool {
	if ids.Validate(snapshot.ExecutionID) != nil || ids.Validate(string(snapshot.AccountID)) != nil ||
		ids.Validate(string(snapshot.WorkItemID)) != nil || snapshot.WorkVersion == 0 || ids.Validate(string(snapshot.UserID)) != nil ||
		ids.Validate(string(snapshot.PersonaID)) != nil || ids.Validate(string(snapshot.BoardroomID)) != nil ||
		snapshot.BoardroomVersion == 0 ||
		ids.Validate(string(snapshot.RunID)) != nil || ids.Validate(string(snapshot.ConversationID)) != nil ||
		len(strings.TrimSpace(snapshot.Title)) < 2 || len(strings.TrimSpace(snapshot.Title)) > 240 ||
		len(strings.TrimSpace(snapshot.Description)) > 20_000 || snapshot.Persona.AccountID != snapshot.AccountID ||
		snapshot.Persona.PersonaID != snapshot.PersonaID || snapshot.Persona.ID == "" || snapshot.QueuedAt.IsZero() {
		return false
	}
	_, err := agentdomain.RestorePersonaVersion(snapshot.Persona)
	return err == nil
}

// ValidForAuthorization exposes only the identifier subset required by the
// private global broker. Full snapshot validation remains inside ProcessOne.
func (snapshot Snapshot) ValidForAuthorization() bool {
	return ids.Validate(snapshot.ExecutionID) == nil && ids.Validate(string(snapshot.AccountID)) == nil && ids.Validate(string(snapshot.UserID)) == nil
}

type Authorization struct {
	EntitlementVersion   uint64
	MaximumConcurrentRun int64
}

func (authorization Authorization) Valid() bool {
	return authorization.EntitlementVersion > 0 && authorization.MaximumConcurrentRun > 0
}

type StartLinkCommand struct {
	Claim         Claim
	Snapshot      Snapshot
	Authorization Authorization
	At            time.Time
}

type StartLinkResult struct {
	CreatedRun bool
	LinkedRun  bool
	Reconciled bool
}

type Stats struct {
	Pending        uint64
	Ready          uint64
	Leased         uint64
	Retrying       uint64
	Linked         uint64
	DeadLetter     uint64
	OldestReadyAge time.Duration
}

// Store keeps claim, heartbeat, start, and Work linkage in the cell. StartLink
// must commit Agent Run creation and Work linkage atomically and treat the
// deterministic existing pair as a successful reconciliation.
type Store interface {
	Claim(context.Context, string, time.Time, time.Duration) (Claim, bool, error)
	Load(context.Context, Claim) (Snapshot, error)
	Heartbeat(context.Context, Claim, time.Time, time.Duration) error
	StartLink(context.Context, StartLinkCommand) (StartLinkResult, error)
	Fail(context.Context, Claim, bool, time.Time, string, time.Time, int) (string, error)
	Stats(context.Context, time.Time) (Stats, error)
}

// Authorizer performs current global Membership, placement, Agents package,
// and concurrent-run-limit resolution over a private workload boundary.
type Authorizer interface {
	Authorize(context.Context, Snapshot) (Authorization, error)
}

type Clock interface{ Now() time.Time }

type Result struct {
	Worked     bool
	Linked     bool
	Reconciled bool
	DeadLetter bool
}

type Processor struct {
	store       Store
	authorizer  Authorizer
	clock       Clock
	ids         ids.Generator
	lease       time.Duration
	maxAttempts int
}

func New(store Store, authorizer Authorizer, clock Clock, generator ids.Generator, lease time.Duration, maxAttempts int) (*Processor, error) {
	if store == nil || authorizer == nil || clock == nil || generator == nil || lease < time.Second || lease > 30*time.Minute || maxAttempts < 1 || maxAttempts > MaximumMaxAttempts {
		return nil, errors.New("Work Agent execution dependencies or bounds are invalid")
	}
	return &Processor{store: store, authorizer: authorizer, clock: clock, ids: generator, lease: lease, maxAttempts: maxAttempts}, nil
}

func (processor *Processor) ProcessOne(ctx context.Context) (Result, error) {
	now := processor.clock.Now().UTC()
	claim, found, err := processor.store.Claim(ctx, processor.ids.New(), now, processor.lease)
	if err != nil || !found {
		return Result{}, err
	}
	if !claim.Valid() {
		return processor.reject(ctx, claim, now, "claim_invalid", ErrInvalidClaim)
	}
	snapshot, err := processor.store.Load(ctx, claim)
	if err != nil {
		if errors.Is(err, ErrInvalidSnapshot) {
			return processor.reject(ctx, claim, now, "snapshot_invalid", err)
		}
		return processor.retry(ctx, claim, now, "snapshot_unavailable", err)
	}
	if !snapshot.Valid() || snapshot.ExecutionID != claim.ExecutionID || snapshot.AccountID != claim.AccountID || snapshot.WorkItemID != claim.WorkItemID {
		return processor.reject(ctx, claim, now, "snapshot_invalid", ErrInvalidSnapshot)
	}
	authorization, err := processor.authorizer.Authorize(ctx, snapshot)
	if err != nil {
		switch {
		case errors.Is(err, ErrAuthorizationDenied), errors.Is(err, ErrAuthorizationStale):
			return processor.reject(ctx, claim, now, "authorization_denied", err)
		default:
			return processor.retry(ctx, claim, now, "authorization_unavailable", errors.Join(ErrAuthorizationService, err))
		}
	}
	if !authorization.Valid() {
		return processor.reject(ctx, claim, now, "authorization_invalid", ErrAuthorizationDenied)
	}
	now = processor.clock.Now().UTC()
	if err := processor.store.Heartbeat(ctx, claim, now, processor.lease); err != nil {
		if errors.Is(err, ErrLeaseLost) {
			return Result{Worked: true}, err
		}
		return processor.retry(ctx, claim, now, "heartbeat_failed", err)
	}
	result, err := processor.store.StartLink(ctx, StartLinkCommand{Claim: claim, Snapshot: snapshot, Authorization: authorization, At: now})
	if err != nil {
		switch {
		case errors.Is(err, ErrLeaseLost):
			return Result{Worked: true}, err
		case errors.Is(err, ErrWorkChanged), errors.Is(err, ErrPersonaUnavailable), errors.Is(err, ErrAuthorizationStale):
			return processor.reject(ctx, claim, now, startFailureCode(err), err)
		case errors.Is(err, ErrRunCapacity):
			return processor.retry(ctx, claim, now, "run_capacity", err)
		default:
			return processor.retry(ctx, claim, now, "start_link_failed", errors.Join(ErrAtomicStartLinkFailed, err))
		}
	}
	if !result.LinkedRun {
		return processor.retry(ctx, claim, now, "start_link_incomplete", ErrAtomicStartLinkFailed)
	}
	return Result{Worked: true, Linked: true, Reconciled: result.Reconciled}, nil
}

func (processor *Processor) Stats(ctx context.Context) (Stats, error) {
	return processor.store.Stats(ctx, processor.clock.Now().UTC())
}

func (processor *Processor) reject(ctx context.Context, claim Claim, now time.Time, code string, cause error) (Result, error) {
	state, failErr := processor.store.Fail(ctx, claim, false, now, code, now, processor.maxAttempts)
	return Result{Worked: true, DeadLetter: state == "dead_letter"}, errors.Join(cause, failErr)
}

func (processor *Processor) retry(ctx context.Context, claim Claim, now time.Time, code string, cause error) (Result, error) {
	next := now.Add(retryDelay(claim.Attempt))
	state, failErr := processor.store.Fail(ctx, claim, true, next, code, now, processor.maxAttempts)
	return Result{Worked: true, DeadLetter: state == "dead_letter"}, errors.Join(cause, failErr)
}

func startFailureCode(err error) string {
	switch {
	case errors.Is(err, ErrWorkChanged):
		return "work_changed"
	case errors.Is(err, ErrPersonaUnavailable):
		return "persona_unavailable"
	default:
		return "authorization_stale"
	}
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

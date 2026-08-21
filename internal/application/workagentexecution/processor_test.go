package workagentexecution

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	agentdomain "github.com/tinfoyle/spyglass-engine/internal/modules/agents"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	executionID  = "10000000-0000-4000-8000-000000000001"
	accountID    = "20000000-0000-4000-8000-000000000002"
	workItemID   = "30000000-0000-4000-8000-000000000003"
	userID       = "40000000-0000-4000-8000-000000000004"
	personaID    = "50000000-0000-4000-8000-000000000005"
	boardroomID  = "60000000-0000-4000-8000-000000000006"
	runID        = "70000000-0000-4000-8000-000000000007"
	conversation = "80000000-0000-4000-8000-000000000008"
	leaseID      = "90000000-0000-4000-8000-000000000009"
)

type testClock struct{ now time.Time }

func (clock *testClock) Now() time.Time { return clock.now }

type testIDs struct{ value string }

func (generator testIDs) New() string { return generator.value }

type testAuthorizer struct {
	authorization Authorization
	err           error
	snapshot      Snapshot
}

func (authorizer *testAuthorizer) Authorize(_ context.Context, snapshot Snapshot) (Authorization, error) {
	authorizer.snapshot = snapshot
	return authorizer.authorization, authorizer.err
}

type testStore struct {
	claim        Claim
	snapshot     Snapshot
	startResult  StartLinkResult
	claimFound   bool
	loadErr      error
	heartbeatErr error
	startErr     error
	failState    string
	failRetry    bool
	failCode     string
	failNext     time.Time
	heartbeats   int
	startCommand StartLinkCommand
}

func (store *testStore) Claim(context.Context, string, time.Time, time.Duration) (Claim, bool, error) {
	return store.claim, store.claimFound, nil
}
func (store *testStore) Load(context.Context, Claim) (Snapshot, error) {
	return store.snapshot, store.loadErr
}
func (store *testStore) Heartbeat(context.Context, Claim, time.Time, time.Duration) error {
	store.heartbeats++
	return store.heartbeatErr
}
func (store *testStore) StartLink(_ context.Context, command StartLinkCommand) (StartLinkResult, error) {
	store.startCommand = command
	return store.startResult, store.startErr
}
func (store *testStore) Fail(_ context.Context, _ Claim, retry bool, next time.Time, code string, _ time.Time, _ int) (string, error) {
	store.failRetry, store.failNext, store.failCode = retry, next, code
	if store.failState == "" {
		store.failState = "dead_letter"
	}
	return store.failState, nil
}
func (*testStore) Stats(context.Context, time.Time) (Stats, error) { return Stats{}, nil }

func validSnapshot(now time.Time) Snapshot {
	persona, err := agentdomain.NewPersonaVersion(agentdomain.PersonaVersionDraft{ID: "51000000-0000-4000-8000-000000000005", PersonaID: personaID, AccountID: accountID, Version: 1,
		Name: "Operations analyst", Role: "Analyst", Description: "", SystemInstructions: "Analyze the assigned Work and return a clear result.",
		Policy: agentdomain.PersonaPolicy{Provider: "openai", Model: "gpt-5", MaximumInputTokens: 1000, MaximumOutputTokens: 500, MaximumToolSteps: 0,
			CitationPolicy: "none", ActionPolicy: "none", OutputSchema: json.RawMessage(`{"type":"object"}`)}, CreatedBy: ids.UserID(userID), CreatedAt: now})
	if err != nil {
		panic(err)
	}
	return Snapshot{ExecutionID: executionID, AccountID: accountID, WorkItemID: workItemID, WorkVersion: 3, UserID: userID,
		PersonaID: personaID, BoardroomID: boardroomID, BoardroomVersion: 2, RunID: runID, ConversationID: conversation, Title: "Analyze current operations", Persona: persona, QueuedAt: now}
}

func validStore(now time.Time) *testStore {
	return &testStore{claimFound: true, claim: Claim{ExecutionID: executionID, AccountID: accountID, WorkItemID: workItemID, LeaseID: leaseID, Attempt: 1},
		snapshot: validSnapshot(now), startResult: StartLinkResult{CreatedRun: true, LinkedRun: true}}
}

func TestProcessorAtomicallyStartsAndLinksAuthorizedIntent(t *testing.T) {
	now := time.Date(2026, 8, 21, 20, 0, 0, 0, time.UTC)
	store := validStore(now)
	authorizer := &testAuthorizer{authorization: Authorization{EntitlementVersion: 7, MaximumConcurrentRun: 3}}
	processor, err := New(store, authorizer, &testClock{now: now}, testIDs{leaseID}, DefaultLease, DefaultMaxAttempts)
	if err != nil {
		t.Fatal(err)
	}
	result, err := processor.ProcessOne(context.Background())
	if err != nil || !result.Worked || !result.Linked || result.Reconciled || store.heartbeats != 1 {
		t.Fatalf("result=%+v err=%v heartbeats=%d", result, err, store.heartbeats)
	}
	if authorizer.snapshot.ExecutionID != executionID || store.startCommand.Authorization.EntitlementVersion != 7 || store.startCommand.Snapshot.RunID != runID {
		t.Fatalf("authorization=%+v command=%+v", authorizer.snapshot, store.startCommand)
	}
}

func TestProcessorConvergesExistingDeterministicRunAndLink(t *testing.T) {
	now := time.Date(2026, 8, 21, 20, 0, 0, 0, time.UTC)
	store := validStore(now)
	store.startResult = StartLinkResult{LinkedRun: true, Reconciled: true}
	processor, _ := New(store, &testAuthorizer{authorization: Authorization{EntitlementVersion: 7, MaximumConcurrentRun: 3}}, &testClock{now}, testIDs{leaseID}, DefaultLease, DefaultMaxAttempts)
	result, err := processor.ProcessOne(context.Background())
	if err != nil || !result.Linked || !result.Reconciled || result.DeadLetter {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestProcessorRejectsPermanentAuthorityAndWorkDrift(t *testing.T) {
	now := time.Date(2026, 8, 21, 20, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name      string
		authorize error
		start     error
		code      string
	}{
		{"authorization", ErrAuthorizationDenied, nil, "authorization_denied"},
		{"Work changed", nil, ErrWorkChanged, "work_changed"},
		{"Persona retired", nil, ErrPersonaUnavailable, "persona_unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := validStore(now)
			store.startErr = test.start
			authorizer := &testAuthorizer{authorization: Authorization{EntitlementVersion: 7, MaximumConcurrentRun: 3}, err: test.authorize}
			processor, _ := New(store, authorizer, &testClock{now}, testIDs{leaseID}, DefaultLease, DefaultMaxAttempts)
			result, err := processor.ProcessOne(context.Background())
			if err == nil || !result.DeadLetter || store.failRetry || store.failCode != test.code {
				t.Fatalf("result=%+v err=%v retry=%v code=%s", result, err, store.failRetry, store.failCode)
			}
		})
	}
}

func TestProcessorRetriesTransientAuthorizationCapacityAndUnknownStart(t *testing.T) {
	now := time.Date(2026, 8, 21, 20, 0, 0, 0, time.UTC)
	private := errors.New("private backend detail")
	for _, test := range []struct {
		name             string
		authorize, start error
		code             string
	}{
		{"authorization unavailable", private, nil, "authorization_unavailable"},
		{"capacity", nil, ErrRunCapacity, "run_capacity"},
		{"unknown start", nil, private, "start_link_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := validStore(now)
			store.startErr = test.start
			store.failState = "retry"
			authorizer := &testAuthorizer{authorization: Authorization{EntitlementVersion: 7, MaximumConcurrentRun: 3}, err: test.authorize}
			processor, _ := New(store, authorizer, &testClock{now}, testIDs{leaseID}, DefaultLease, DefaultMaxAttempts)
			result, err := processor.ProcessOne(context.Background())
			if err == nil || result.DeadLetter || !store.failRetry || store.failCode != test.code || !store.failNext.Equal(now.Add(time.Second)) {
				t.Fatalf("result=%+v err=%v retry=%v code=%s next=%v", result, err, store.failRetry, store.failCode, store.failNext)
			}
		})
	}
}

func TestProcessorDoesNotContinueAfterLeaseLoss(t *testing.T) {
	now := time.Date(2026, 8, 21, 20, 0, 0, 0, time.UTC)
	store := validStore(now)
	store.heartbeatErr = ErrLeaseLost
	processor, _ := New(store, &testAuthorizer{authorization: Authorization{EntitlementVersion: 7, MaximumConcurrentRun: 3}}, &testClock{now}, testIDs{leaseID}, DefaultLease, DefaultMaxAttempts)
	result, err := processor.ProcessOne(context.Background())
	if !errors.Is(err, ErrLeaseLost) || !result.Worked || store.startCommand.Claim.ExecutionID != "" || store.failCode != "" {
		t.Fatalf("result=%+v err=%v start=%+v fail=%s", result, err, store.startCommand, store.failCode)
	}
}

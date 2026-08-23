package runnercapability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type capabilityClock struct{ now time.Time }

func (c capabilityClock) Now() time.Time { return c.now }

func TestExportedCapabilitiesFollowBrokerGrammar(t *testing.T) {
	capabilities := []string{
		WorkSummaryCapability,
		FinanceLedgersReadCapability,
		FinanceAccountsReadCapability,
		FinanceEntryDraftCapability,
		FinanceEntryPostCapability,
		MarketingCampaignsReadCapability,
		MarketingAssetsReadCapability,
		MarketingReleasesReadCapability,
		MarketingCampaignDraftCapability,
		MarketingAssetDraftCapability,
		MarketingReleaseDraftCapability,
		MarketingReleaseActivateCapability,
	}
	for _, capability := range capabilities {
		if !runnerbroker.ValidCapability(capability) {
			t.Errorf("capability %q violates the broker grammar", capability)
		}
	}
}

type capabilityAuthorizer struct {
	grant runnerbroker.CapabilityGrant
	err   error
	calls int
}

func (a *capabilityAuthorizer) AuthorizeCapability(_ context.Context, _, _, _ string) (runnerbroker.CapabilityGrant, error) {
	a.calls++
	return a.grant, a.err
}

type capabilityAuditor struct {
	records []AuditRecord
	err     error
}

func (a *capabilityAuditor) RecordCapability(_ context.Context, record AuditRecord) error {
	a.records = append(a.records, record)
	return a.err
}

type actionAuthorizer struct {
	request     ActionRequest
	lease       ActionLease
	beginErr    error
	completeErr error
	completions []ActionCompletion
	calls       int
}

func (a *actionAuthorizer) BeginAction(_ context.Context, request ActionRequest) (ActionLease, error) {
	a.calls++
	a.request = request
	if a.beginErr != nil {
		return ActionLease{}, a.beginErr
	}
	lease := a.lease
	if lease.AttemptID == "" {
		lease = ActionLease{AccountID: request.AccountID, InvocationID: request.InvocationID, OperationID: request.OperationID, AttemptID: "51000000-0000-4000-8000-000000000001", Capability: request.Capability, InputDigest: request.InputDigest, Mode: ActionExecute, IdempotencyKey: request.OperationID, LeaseExpiresAt: request.ExpiresAt}
	}
	return lease, nil
}

func (a *actionAuthorizer) CompleteAction(_ context.Context, completion ActionCompletion) error {
	a.completions = append(a.completions, completion)
	return a.completeErr
}

type codeError string

func (e codeError) Error() string { return "private provider detail" }
func (e codeError) Code() string  { return string(e) }

type definitiveCodeError string

func (e definitiveCodeError) Error() string    { return "private definitive provider detail" }
func (e definitiveCodeError) Code() string     { return string(e) }
func (e definitiveCodeError) Definitive() bool { return true }

type consequentialHandler struct {
	execute   func(context.Context, AuthorizedCall) (json.RawMessage, error)
	reconcile func(context.Context, AuthorizedCall) (json.RawMessage, ActionOutcome, error)
}

func (h consequentialHandler) Execute(ctx context.Context, call AuthorizedCall) (json.RawMessage, error) {
	return h.execute(ctx, call)
}

func (h consequentialHandler) Reconcile(ctx context.Context, call AuthorizedCall) (json.RawMessage, ActionOutcome, error) {
	return h.reconcile(ctx, call)
}

func capabilityFixture() (*capabilityAuthorizer, *capabilityAuditor, capabilityClock, Call) {
	now := time.Date(2026, 8, 18, 16, 0, 0, 0, time.UTC)
	grant := runnerbroker.CapabilityGrant{
		Identity:  runnerbroker.Identity{InvocationID: "11000000-0000-4000-8000-000000000001", PodUID: "21000000-0000-4000-8000-000000000001"},
		AccountID: ids.AccountID("31000000-0000-4000-8000-000000000001"), Kind: "agent.execute", Capability: "work:read", ExpiresAt: now.Add(time.Hour),
	}
	call := Call{SchemaVersion: 1, OperationID: "41000000-0000-4000-8000-000000000001", Capability: "work:read", Input: json.RawMessage(`{"z":{"b":1,"a":2},"a":2}`)}
	return &capabilityAuthorizer{grant: grant}, &capabilityAuditor{}, capabilityClock{now}, call
}

func TestReadOnlyCapabilityReauthorizesAuditsAndBoundsExecution(t *testing.T) {
	authorizer, auditor, clock, call := capabilityFixture()
	handlerCalls := 0
	service, err := New(authorizer, nil, auditor, clock, []Definition{{Capability: "work:read", Effect: EffectReadOnly, Timeout: time.Second, Handler: HandlerFunc(func(ctx context.Context, authorized AuthorizedCall) (json.RawMessage, error) {
		handlerCalls++
		if deadline, ok := ctx.Deadline(); !ok || !deadline.Equal(clock.now.Add(time.Second)) {
			t.Errorf("execution deadline=%v ok=%v", deadline, ok)
		}
		if string(authorized.Input) != `{"a":2,"z":{"a":2,"b":1}}` || authorized.InputDigest == [32]byte{} {
			t.Errorf("authorized call=%+v", authorized)
		}
		return json.RawMessage(`{"items":[1]}`), nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Invoke(context.Background(), "pod-token", authorizer.grant.Identity.InvocationID, call)
	if err != nil || result.SchemaVersion != 1 || !bytes.Contains(result.Output, []byte("items")) || authorizer.calls != 1 || handlerCalls != 1 {
		t.Fatalf("result=%+v auth=%d handler=%d err=%v", result, authorizer.calls, handlerCalls, err)
	}
	if len(auditor.records) != 2 || auditor.records[0].Decision != "authorized" || auditor.records[1].Decision != "succeeded" || auditor.records[0].AccountID != authorizer.grant.AccountID {
		t.Fatalf("audit=%+v", auditor.records)
	}
}

func TestAdditiveCapabilityExecutesWithoutConsequentialAuthorization(t *testing.T) {
	authorizer, auditor, clock, call := capabilityFixture()
	authorizer.grant.Capability = FinanceEntryDraftCapability
	call.Capability = FinanceEntryDraftCapability
	called := false
	service, err := New(authorizer, nil, auditor, clock, []Definition{{Capability: FinanceEntryDraftCapability, Effect: EffectAdditive, Timeout: time.Second, Handler: HandlerFunc(func(_ context.Context, authorized AuthorizedCall) (json.RawMessage, error) {
		called = authorized.Action == nil
		return json.RawMessage(`{"state":"draft"}`), nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Invoke(context.Background(), "runner-token", authorizer.grant.Identity.InvocationID, call)
	if err != nil || !called || !strings.Contains(string(result.Output), `"state":"draft"`) || len(auditor.records) != 2 || auditor.records[0].Effect != string(EffectAdditive) || auditor.records[0].Decision != "authorized" || auditor.records[1].Decision != "succeeded" {
		t.Fatalf("result=%s called=%v records=%+v err=%v", result.Output, called, auditor.records, err)
	}
}

func TestCancellationDenialPreventsHandlerAndAudit(t *testing.T) {
	authorizer, auditor, clock, call := capabilityFixture()
	authorizer.err = runnerbroker.ErrExchangeCanceled
	handlerCalls := 0
	service, _ := New(authorizer, nil, auditor, clock, []Definition{{Capability: "work:read", Effect: EffectReadOnly, Timeout: time.Second, Handler: HandlerFunc(func(context.Context, AuthorizedCall) (json.RawMessage, error) {
		handlerCalls++
		return json.RawMessage(`{}`), nil
	})}})
	if _, err := service.Invoke(context.Background(), "pod-token", authorizer.grant.Identity.InvocationID, call); !errors.Is(err, runnerbroker.ErrExchangeCanceled) || handlerCalls != 0 || len(auditor.records) != 0 {
		t.Fatalf("canceled err=%v handler=%d audit=%v", err, handlerCalls, auditor.records)
	}
}

func TestConsequentialCapabilityRequiresDurableActionAuthorization(t *testing.T) {
	authorizer, auditor, clock, call := capabilityFixture()
	authorizer.grant.Capability = "email:send"
	call.Capability = "email:send"
	actions := &actionAuthorizer{beginErr: codeError("approval_required")}
	handlerCalls := 0
	handler := consequentialHandler{
		execute: func(context.Context, AuthorizedCall) (json.RawMessage, error) {
			handlerCalls++
			return json.RawMessage(`{}`), nil
		},
		reconcile: func(context.Context, AuthorizedCall) (json.RawMessage, ActionOutcome, error) {
			handlerCalls++
			return json.RawMessage(`{}`), ActionSucceeded, nil
		},
	}
	definition := Definition{Capability: "email:send", Effect: EffectConsequential, Timeout: time.Second, Handler: handler}
	if _, err := New(authorizer, nil, auditor, clock, []Definition{definition}); !errors.Is(err, ErrInvalidCall) {
		t.Fatalf("consequential definition without action authorizer=%v", err)
	}
	if _, err := New(authorizer, actions, auditor, clock, []Definition{{Capability: "email:send", Effect: EffectConsequential, Timeout: time.Second, Handler: HandlerFunc(handler.execute)}}); !errors.Is(err, ErrInvalidCall) {
		t.Fatalf("consequential definition without reconcile handler=%v", err)
	}
	service, _ := New(authorizer, actions, auditor, clock, []Definition{definition})
	if _, err := service.Invoke(context.Background(), "pod-token", authorizer.grant.Identity.InvocationID, call); !errors.Is(err, ErrActionDenied) || actions.calls != 1 || handlerCalls != 0 {
		t.Fatalf("action denial err=%v actions=%d handler=%d", err, actions.calls, handlerCalls)
	}
	if actions.request.OperationID != call.OperationID || actions.request.InputDigest == [32]byte{} || len(auditor.records) != 1 || auditor.records[0].ErrorCode != "approval_required" {
		t.Fatalf("action=%+v audit=%+v", actions.request, auditor.records)
	}
}

func TestConsequentialTransientActionAdmissionIsUnavailable(t *testing.T) {
	for _, code := range []codeError{"action_in_progress", "action_repository_unavailable"} {
		t.Run(string(code), func(t *testing.T) {
			authorizer, auditor, clock, call := capabilityFixture()
			authorizer.grant.Capability, call.Capability = "email:send", "email:send"
			actions := &actionAuthorizer{beginErr: code}
			handler := consequentialHandler{
				execute: func(context.Context, AuthorizedCall) (json.RawMessage, error) {
					t.Fatal("transient admission failure reached handler")
					return nil, nil
				},
				reconcile: func(context.Context, AuthorizedCall) (json.RawMessage, ActionOutcome, error) {
					t.Fatal("transient admission failure reached reconciliation")
					return nil, ActionUnknown, nil
				},
			}
			service, err := New(authorizer, actions, auditor, clock, []Definition{{Capability: call.Capability, Effect: EffectConsequential, Timeout: time.Second, Handler: handler}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.Invoke(context.Background(), "pod-token", authorizer.grant.Identity.InvocationID, call); !errors.Is(err, ErrActionUnavailable) {
				t.Fatalf("transient admission error=%v", err)
			}
			if len(auditor.records) != 1 || auditor.records[0].ErrorCode != string(code) {
				t.Fatalf("audit=%+v", auditor.records)
			}
		})
	}
}

func TestConsequentialExecutionUsesStableLeaseAndSettlesSuccess(t *testing.T) {
	authorizer, auditor, clock, call := capabilityFixture()
	authorizer.grant.Capability, call.Capability = "email:send", "email:send"
	actions := &actionAuthorizer{}
	executeCalls, reconcileCalls := 0, 0
	handler := consequentialHandler{
		execute: func(_ context.Context, authorized AuthorizedCall) (json.RawMessage, error) {
			executeCalls++
			if authorized.Action == nil || authorized.Action.Mode != ActionExecute || authorized.Action.IdempotencyKey != call.OperationID {
				t.Fatalf("missing stable action lease: %#v", authorized.Action)
			}
			return json.RawMessage(`{"provider_id":"message-redacted"}`), nil
		},
		reconcile: func(context.Context, AuthorizedCall) (json.RawMessage, ActionOutcome, error) {
			reconcileCalls++
			return nil, ActionUnknown, nil
		},
	}
	service, err := New(authorizer, actions, auditor, clock, []Definition{{Capability: call.Capability, Effect: EffectConsequential, Timeout: time.Second, Handler: handler}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Invoke(context.Background(), "pod-token", authorizer.grant.Identity.InvocationID, call)
	if err != nil || executeCalls != 1 || reconcileCalls != 0 || !bytes.Contains(result.Output, []byte("provider_id")) {
		t.Fatalf("result=%s execute=%d reconcile=%d err=%v", result.Output, executeCalls, reconcileCalls, err)
	}
	if len(actions.completions) != 1 || actions.completions[0].Outcome != ActionSucceeded || actions.completions[0].ErrorCode != "" {
		t.Fatalf("completion=%+v", actions.completions)
	}
}

func TestConsequentialRetryCanOnlyReconcileUnknownOutcome(t *testing.T) {
	authorizer, auditor, clock, call := capabilityFixture()
	authorizer.grant.Capability, call.Capability = "email:send", "email:send"
	digestInput, digest, _ := validateCall(call)
	_ = digestInput
	actions := &actionAuthorizer{lease: ActionLease{
		AccountID: authorizer.grant.AccountID, InvocationID: authorizer.grant.Identity.InvocationID, OperationID: call.OperationID,
		AttemptID: "51000000-0000-4000-8000-000000000001", Capability: call.Capability, InputDigest: digest,
		Mode: ActionReconcile, IdempotencyKey: call.OperationID, LeaseExpiresAt: clock.now.Add(time.Minute),
	}}
	executeCalls, reconcileCalls := 0, 0
	handler := consequentialHandler{
		execute: func(context.Context, AuthorizedCall) (json.RawMessage, error) {
			executeCalls++
			return nil, errors.New("must not execute")
		},
		reconcile: func(_ context.Context, authorized AuthorizedCall) (json.RawMessage, ActionOutcome, error) {
			reconcileCalls++
			if authorized.Action == nil || authorized.Action.Mode != ActionReconcile {
				t.Fatal("reconciliation did not receive its lease")
			}
			return nil, ActionUnknown, nil
		},
	}
	service, _ := New(authorizer, actions, auditor, clock, []Definition{{Capability: call.Capability, Effect: EffectConsequential, Timeout: time.Second, Handler: handler}})
	if _, err := service.Invoke(context.Background(), "pod-token", authorizer.grant.Identity.InvocationID, call); !errors.Is(err, ErrExecutionFailed) || executeCalls != 0 || reconcileCalls != 1 {
		t.Fatalf("err=%v execute=%d reconcile=%d", err, executeCalls, reconcileCalls)
	}
	if len(actions.completions) != 1 || actions.completions[0].Outcome != ActionUnknown || actions.completions[0].ErrorCode != "action_unknown" {
		t.Fatalf("completion=%+v", actions.completions)
	}
}

func TestConsequentialErrorsSettleUnknownUnlessProvenDefinitive(t *testing.T) {
	for _, test := range []struct {
		name    string
		err     error
		outcome ActionOutcome
	}{
		{"timeout", codeError("provider_timeout"), ActionUnknown},
		{"rejected", definitiveCodeError("provider_rejected"), ActionFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			authorizer, auditor, clock, call := capabilityFixture()
			authorizer.grant.Capability, call.Capability = "email:send", "email:send"
			actions := &actionAuthorizer{}
			handler := consequentialHandler{
				execute: func(context.Context, AuthorizedCall) (json.RawMessage, error) { return nil, test.err },
				reconcile: func(context.Context, AuthorizedCall) (json.RawMessage, ActionOutcome, error) {
					return nil, ActionUnknown, nil
				},
			}
			service, _ := New(authorizer, actions, auditor, clock, []Definition{{Capability: call.Capability, Effect: EffectConsequential, Timeout: time.Second, Handler: handler}})
			if _, err := service.Invoke(context.Background(), "pod-token", authorizer.grant.Identity.InvocationID, call); !errors.Is(err, ErrExecutionFailed) {
				t.Fatalf("execution error=%v", err)
			}
			if len(actions.completions) != 1 || actions.completions[0].Outcome != test.outcome || actions.completions[0].ErrorCode != machineError(test.err, "execution_failed") {
				t.Fatalf("completion=%+v", actions.completions)
			}
		})
	}
}

func TestConsequentialSettlementFailureIsUnavailableAndDoesNotClaimSuccess(t *testing.T) {
	authorizer, auditor, clock, call := capabilityFixture()
	authorizer.grant.Capability, call.Capability = "email:send", "email:send"
	actions := &actionAuthorizer{completeErr: errors.New("action database unavailable")}
	handler := consequentialHandler{
		execute: func(context.Context, AuthorizedCall) (json.RawMessage, error) {
			return json.RawMessage(`{"accepted":true}`), nil
		},
		reconcile: func(context.Context, AuthorizedCall) (json.RawMessage, ActionOutcome, error) {
			return nil, ActionUnknown, nil
		},
	}
	service, _ := New(authorizer, actions, auditor, clock, []Definition{{Capability: call.Capability, Effect: EffectConsequential, Timeout: time.Second, Handler: handler}})
	if _, err := service.Invoke(context.Background(), "pod-token", authorizer.grant.Identity.InvocationID, call); !errors.Is(err, ErrActionUnavailable) {
		t.Fatalf("settlement failure=%v", err)
	}
	if len(auditor.records) != 1 || auditor.records[0].Decision != "authorized" {
		t.Fatalf("gateway claimed a terminal outcome: %+v", auditor.records)
	}
}

func TestAuditFailureAndInvalidOutputFailClosed(t *testing.T) {
	authorizer, auditor, clock, call := capabilityFixture()
	handlerCalls := 0
	handler := HandlerFunc(func(context.Context, AuthorizedCall) (json.RawMessage, error) {
		handlerCalls++
		return json.RawMessage(`[]`), nil
	})
	service, _ := New(authorizer, nil, auditor, clock, []Definition{{Capability: "work:read", Effect: EffectReadOnly, Timeout: time.Second, Handler: handler}})
	auditor.err = errors.New("audit database unavailable")
	if _, err := service.Invoke(context.Background(), "pod-token", authorizer.grant.Identity.InvocationID, call); !errors.Is(err, ErrAuditUnavailable) || handlerCalls != 0 {
		t.Fatalf("pre-execution audit err=%v handler=%d", err, handlerCalls)
	}
	auditor.err = nil
	if _, err := service.Invoke(context.Background(), "pod-token", authorizer.grant.Identity.InvocationID, call); !errors.Is(err, ErrExecutionFailed) || handlerCalls != 1 || auditor.records[len(auditor.records)-1].ErrorCode != "invalid_output" {
		t.Fatalf("invalid output err=%v handler=%d audit=%+v", err, handlerCalls, auditor.records)
	}
}

func TestHandlerErrorExposesOnlyMachineCodeToAudit(t *testing.T) {
	authorizer, auditor, clock, call := capabilityFixture()
	service, _ := New(authorizer, nil, auditor, clock, []Definition{{Capability: "work:read", Effect: EffectReadOnly, Timeout: time.Second, Handler: HandlerFunc(func(context.Context, AuthorizedCall) (json.RawMessage, error) {
		return nil, codeError("provider_timeout")
	})}})
	if _, err := service.Invoke(context.Background(), "pod-token", authorizer.grant.Identity.InvocationID, call); !errors.Is(err, ErrExecutionFailed) || len(auditor.records) != 2 || auditor.records[1].ErrorCode != "provider_timeout" || bytes.Contains([]byte(auditor.records[1].ErrorCode), []byte("private")) {
		t.Fatalf("handler error=%v audit=%+v", err, auditor.records)
	}
}

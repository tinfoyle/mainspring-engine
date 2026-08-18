package runnercapability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type capabilityClock struct{ now time.Time }

func (c capabilityClock) Now() time.Time { return c.now }

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
	request ActionRequest
	err     error
	calls   int
}

func (a *actionAuthorizer) AuthorizeAction(_ context.Context, request ActionRequest) error {
	a.calls++
	a.request = request
	return a.err
}

type codeError string

func (e codeError) Error() string { return "private provider detail" }
func (e codeError) Code() string  { return string(e) }

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
	actions := &actionAuthorizer{err: codeError("approval_required")}
	handlerCalls := 0
	definition := Definition{Capability: "email:send", Effect: EffectConsequential, Timeout: time.Second, Handler: HandlerFunc(func(context.Context, AuthorizedCall) (json.RawMessage, error) {
		handlerCalls++
		return json.RawMessage(`{}`), nil
	})}
	if _, err := New(authorizer, nil, auditor, clock, []Definition{definition}); !errors.Is(err, ErrInvalidCall) {
		t.Fatalf("consequential definition without action authorizer=%v", err)
	}
	service, _ := New(authorizer, actions, auditor, clock, []Definition{definition})
	if _, err := service.Invoke(context.Background(), "pod-token", authorizer.grant.Identity.InvocationID, call); !errors.Is(err, ErrActionDenied) || actions.calls != 1 || handlerCalls != 0 {
		t.Fatalf("action denial err=%v actions=%d handler=%d", err, actions.calls, handlerCalls)
	}
	if actions.request.OperationID != call.OperationID || actions.request.InputDigest == [32]byte{} || len(auditor.records) != 1 || auditor.records[0].ErrorCode != "approval_required" {
		t.Fatalf("action=%+v audit=%+v", actions.request, auditor.records)
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

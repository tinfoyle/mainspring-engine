package cellapi

import (
	"context"
	"crypto/sha256"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/actionrecovery"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type actionRecoveryStub struct {
	list    func(context.Context, access.Actor, ids.AccountID, actionrecovery.ListQuery) (actionrecovery.Page, error)
	get     func(context.Context, access.Actor, ids.AccountID, string) (actionrecovery.Detail, error)
	request func(context.Context, actionrecovery.RequestCommand) (actionrecovery.Detail, error)
	confirm func(context.Context, actionrecovery.ConfirmCommand) (actionrecovery.Detail, error)
}

func (s actionRecoveryStub) List(c context.Context, a access.Actor, id ids.AccountID, q actionrecovery.ListQuery) (actionrecovery.Page, error) {
	return s.list(c, a, id, q)
}
func (s actionRecoveryStub) Get(c context.Context, a access.Actor, id ids.AccountID, op string) (actionrecovery.Detail, error) {
	return s.get(c, a, id, op)
}
func (s actionRecoveryStub) Request(c context.Context, cmd actionrecovery.RequestCommand) (actionrecovery.Detail, error) {
	return s.request(c, cmd)
}
func (s actionRecoveryStub) Confirm(c context.Context, cmd actionrecovery.ConfirmCommand) (actionrecovery.Detail, error) {
	return s.confirm(c, cmd)
}

func actionFixture() actionrecovery.Detail {
	now := time.Date(2026, 8, 21, 23, 30, 0, 0, time.UTC)
	return actionrecovery.Detail{Summary: actionrecovery.Summary{OperationID: attentionWork, ApprovalID: attentionOperation, InvocationID: attentionInvocation, Capability: "stripe.customer.create", ExecutorID: "stripe.customer", ExecutorVersion: 1, PolicyVersion: 1, State: actionrecovery.StateUnknown, AttemptCount: 2, LastErrorCode: "stripe_transport_unknown", StartedAt: now.Add(-time.Minute), UpdatedAt: now}}
}
func newActionRecoveryServer(t *testing.T, service ActionRecoveryService) *Server {
	t.Helper()
	server, err := New(claimAcceptor{claims: attentionClaims("agents")}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithActionRecovery(service))
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func TestActionRecoveryQueueIsContentRedactedAndRouteAuthorized(t *testing.T) {
	fixture := actionFixture()
	service := actionRecoveryStub{list: func(ctx context.Context, actor access.Actor, account ids.AccountID, query actionrecovery.ListQuery) (actionrecovery.Page, error) {
		claims, ok := routecontext.FromContext(ctx)
		if !ok || claims.Authority.AccountID != attentionAccount || actor.UserID != attentionUser || account != attentionAccount || query.State != actionrecovery.StateUnknown {
			t.Fatalf("claims=%+v actor=%+v account=%s query=%+v", claims, actor, account, query)
		}
		return actionrecovery.Page{Items: []actionrecovery.Summary{fixture.Summary}}, nil
	}}
	response := attentionRead(t, newActionRecoveryServer(t, service).Handler(), "/api/v1/accounts/"+attentionAccount+"/attention/actions?state=unknown")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "stripe_transport_unknown") || strings.Contains(response.Body.String(), "reason_sha256") || strings.Contains(response.Body.String(), "owner@example.com") {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
}

func TestActionResolutionRequestUsesRoutedOperationAsResolutionIdentity(t *testing.T) {
	fixture := actionFixture()
	service := actionRecoveryStub{request: func(_ context.Context, command actionrecovery.RequestCommand) (actionrecovery.Detail, error) {
		if command.Actor.UserID != attentionUser || command.AccountID != attentionAccount || command.OperationID != attentionWork || command.ResolutionID != attentionOperation || command.Outcome != actionrecovery.StateSucceeded || command.Reason != "Provider evidence inspected" {
			t.Fatalf("command=%+v", command)
		}
		digest := sha256.Sum256([]byte(command.Reason))
		fixture.Resolution = &actionrecovery.Resolution{ID: command.ResolutionID, OperationID: command.OperationID, RequestedOutcome: command.Outcome, ReasonSHA256: digest, RequestedByUserID: command.Actor.UserID, State: "pending"}
		return fixture, nil
	}}
	response := attentionMutation(t, newActionRecoveryServer(t, service).Handler(), http.MethodPost, "/api/v1/accounts/"+attentionAccount+"/attention/actions/"+attentionWork+"/resolution-requests", `{"outcome":"succeeded","reason":"Provider evidence inspected"}`, attentionOperation, "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"state":"pending"`) || !strings.Contains(response.Body.String(), `"reason_sha256"`) {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
}

func TestActionResolutionConfirmationBindsExactTargetAndMapsSelfConfirmation(t *testing.T) {
	service := actionRecoveryStub{confirm: func(_ context.Context, command actionrecovery.ConfirmCommand) (actionrecovery.Detail, error) {
		if command.OperationID != attentionWork || command.ResolutionID != attentionFact || command.Actor.UserID != attentionUser {
			t.Fatalf("command=%+v", command)
		}
		return actionrecovery.Detail{}, actionrecovery.ErrConstraint
	}}
	response := attentionMutation(t, newActionRecoveryServer(t, service).Handler(), http.MethodPost, "/api/v1/accounts/"+attentionAccount+"/attention/actions/"+attentionWork+"/resolutions/"+attentionFact+"/confirmations", "", attentionOperation, "")
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "action_recovery_rejected") {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
}

func TestActionRecoveryRejectsUnknownQueriesAndMismatchedIdempotency(t *testing.T) {
	server := newActionRecoveryServer(t, actionRecoveryStub{list: func(context.Context, access.Actor, ids.AccountID, actionrecovery.ListQuery) (actionrecovery.Page, error) {
		return actionrecovery.Page{}, nil
	}, request: func(context.Context, actionrecovery.RequestCommand) (actionrecovery.Detail, error) {
		return actionrecovery.Detail{}, nil
	}})
	query := attentionRead(t, server.Handler(), "/api/v1/accounts/"+attentionAccount+"/attention/actions?payload=true")
	if query.Code != http.StatusBadRequest {
		t.Fatalf("query=%d %s", query.Code, query.Body.String())
	}
	mutation := attentionMutation(t, server.Handler(), http.MethodPost, "/api/v1/accounts/"+attentionAccount+"/attention/actions/"+attentionWork+"/resolution-requests", `{"outcome":"failed","reason":"Inspected provider"}`, attentionFact, "")
	if mutation.Code != http.StatusBadRequest || !strings.Contains(mutation.Body.String(), "invalid_idempotency_key") {
		t.Fatalf("mutation=%d %s", mutation.Code, mutation.Body.String())
	}
}

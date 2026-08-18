package cellapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	workapp "github.com/tinfoyle/spyglass-engine/internal/application/work"
	workdomain "github.com/tinfoyle/spyglass-engine/internal/modules/work"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

const workOperation = "50000000-0000-4000-8000-000000000005"

func TestWorkCommandContractsDeriveActorAndConcurrencyAuthority(t *testing.T) {
	now := time.Date(2026, 8, 18, 15, 0, 0, 0, time.UTC)
	commands := &commandService{item: testWorkItem(t, now)}
	server, err := New(claimAcceptor{claims: commandClaims()}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithWorkCommands(commands))
	if err != nil {
		t.Fatal(err)
	}

	created := commandRequest(t, server.Handler(), http.MethodPost, "/api/v1/accounts/"+workAccount+"/work-items", `{"kind":"ticket","title":"Close the monthly books","description":"Review the close checklist.","priority":"urgent","assignment":{"responsibility":"user","user_id":"`+workUser+`"}}`, "")
	if created.Code != http.StatusCreated || created.Header().Get("ETag") != `W/"1"` || created.Header().Get("Location") != "/api/v1/accounts/"+workAccount+"/work-items/"+workItemID {
		t.Fatalf("create=%d etag=%q location=%q body=%s", created.Code, created.Header().Get("ETag"), created.Header().Get("Location"), created.Body.String())
	}
	if commands.create.RequestID != workOperation || commands.create.Actor.UserID != workUser || commands.create.Provenance.CreatedBy.ID != workUser || commands.create.Assignment.UserID != workUser {
		t.Fatalf("create command=%+v", commands.create)
	}

	commands.item.Version = 2
	transitioned := commandRequest(t, server.Handler(), http.MethodPost, "/api/v1/accounts/"+workAccount+"/work-items/"+workItemID+"/transitions", `{"to":"in_progress"}`, `W/"1"`)
	if transitioned.Code != http.StatusOK || commands.transition.ExpectedVersion != 1 || commands.transition.RequestID != workOperation || commands.transition.CorrelationID != workOperation {
		t.Fatalf("transition=%d command=%+v body=%s", transitioned.Code, commands.transition, transitioned.Body.String())
	}
}

func TestWorkCommandsRejectSpoofedAssignmentMissingVersionAndConflict(t *testing.T) {
	commands := &commandService{item: testWorkItem(t, time.Now().UTC())}
	server, _ := New(claimAcceptor{claims: commandClaims()}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithWorkCommands(commands))
	spoofed := commandRequest(t, server.Handler(), http.MethodPost, "/api/v1/accounts/"+workAccount+"/work-items", `{"kind":"todo","title":"Spoof owner","priority":"normal","assignment":{"responsibility":"user","user_id":"60000000-0000-4000-8000-000000000006"}}`, "")
	if spoofed.Code != http.StatusUnprocessableEntity || commands.create.RequestID != "" {
		t.Fatalf("spoofed=%d command=%+v body=%s", spoofed.Code, commands.create, spoofed.Body.String())
	}
	missing := commandRequest(t, server.Handler(), http.MethodPost, "/api/v1/accounts/"+workAccount+"/work-items/"+workItemID+"/transitions", `{"to":"in_progress"}`, "")
	if missing.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing version=%d %s", missing.Code, missing.Body.String())
	}
	commands.err = workapp.ErrConflict
	conflict := commandRequest(t, server.Handler(), http.MethodPost, "/api/v1/accounts/"+workAccount+"/work-items/"+workItemID+"/transitions", `{"to":"in_progress"}`, `W/"1"`)
	if conflict.Code != http.StatusPreconditionFailed || !strings.Contains(conflict.Body.String(), "work_version_conflict") {
		t.Fatalf("conflict=%d %s", conflict.Code, conflict.Body.String())
	}
}

func commandRequest(t *testing.T, handler http.Handler, method, target, body, ifMatch string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set(RouteContextHeader, "accepted-by-test-boundary")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", workOperation)
	if ifMatch != "" {
		request.Header.Set("If-Match", ifMatch)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func commandClaims() routecontext.Claims {
	claims := workClaims()
	claims.Authority.Role = "member"
	claims.Authority.OperationID = workOperation
	return claims
}

type commandService struct {
	item       workdomain.Item
	err        error
	create     workapp.CreateCommand
	transition workapp.TransitionCommand
	assign     workapp.AssignCommand
}

func (s *commandService) Create(_ context.Context, command workapp.CreateCommand) (workdomain.Item, error) {
	s.create = command
	return s.item, s.err
}
func (s *commandService) Transition(_ context.Context, command workapp.TransitionCommand) (workdomain.Item, error) {
	s.transition = command
	return s.item, s.err
}
func (s *commandService) Assign(_ context.Context, command workapp.AssignCommand) (workdomain.Item, error) {
	s.assign = command
	return s.item, s.err
}

var _ WorkCommands = (*commandService)(nil)

package admissionhttp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	scheduleapp "github.com/tinfoyle/spyglass-engine/internal/application/scheduling"
	"github.com/tinfoyle/spyglass-engine/internal/application/usageadmission"
	workagent "github.com/tinfoyle/spyglass-engine/internal/application/workagentexecution"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	scheduledomain "github.com/tinfoyle/spyglass-engine/internal/modules/scheduling"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	"github.com/tinfoyle/spyglass-engine/internal/transport/admissionapi"
)

const (
	admissionAccount   = "10000000-0000-4000-8000-000000000001"
	admissionUser      = "20000000-0000-4000-8000-000000000002"
	admissionRoute     = "30000000-0000-4000-8000-000000000003"
	admissionOperation = "40000000-0000-4000-8000-000000000004"
)

func TestClientUsesSignedRouteProofForNarrowWorkCapacity(t *testing.T) {
	now := time.Date(2026, 8, 18, 14, 0, 0, 0, time.UTC)
	clock := admissionClock{now}
	key := []byte("0123456789abcdef0123456789abcdef")
	cellID := ids.CellID("cell-us-east-01")
	signer, _ := routecontext.NewSigner("router", "current", key, 20*time.Second, clock)
	verifier, _ := routecontext.NewVerifier("router", routecontext.Audience(cellID), map[string][]byte{"current": key}, routecontext.MaximumLifetime, 0, clock)
	usage := &admissionUsage{result: usageadmission.Reservation{RequestID: admissionOperation, State: usageadmission.ReservationActive, Current: 4, Maximum: 100}}
	reviewers := &admissionReviewers{role: accounts.RoleMember, active: true}
	server, err := admissionapi.New(usage, map[ids.CellID]admissionapi.Verifier{cellID: verifier}, slog.New(slog.NewTextHandler(io.Discard, nil)), admissionapi.DefaultMaxBody,
		admissionapi.WithReviewerDirectory(reviewers))
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	client, err := New(httpServer.URL, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	binding, _ := routecontext.Bind(http.MethodPost, "/api/v1/accounts/"+admissionAccount+"/work-items", []byte(`{"title":"Close books"}`))
	authority := routecontext.Authority{RequestID: admissionRoute, OperationID: admissionOperation, AccountID: ids.AccountID(admissionAccount), ActorKind: "user", ActorID: admissionUser, Role: "member", CellID: cellID, PlacementGeneration: 2, EntitlementVersion: 7, PackageAccess: &routecontext.PackageAccess{Code: "work", Version: 1, Mode: "enabled", Limits: map[string]int64{"active_items": 100}, LimitPolicies: map[string]routecontext.LimitPolicy{"active_items": {Kind: "capacity", Combine: "maximum"}}}}
	token, _ := signer.Issue(routecontext.Audience(cellID), authority, binding)
	claims, _ := verifier.Verify(token, binding)
	ctx := routecontext.WithClaims(context.Background(), claims)
	ctx = routecontext.WithProof(ctx, routecontext.Proof{Token: token, Binding: binding})
	reservation, err := client.Reserve(ctx, usageadmission.ReserveCommand{Actor: access.Actor{UserID: ids.UserID(admissionUser)}, AccountID: ids.AccountID(admissionAccount), PackageCode: catalog.PackageWork, LimitCode: "active_items", Amount: 1, RequestID: admissionOperation})
	if err != nil || reservation.State != usageadmission.ReservationActive || reservation.Current != 4 || usage.reserve.AccountID != admissionAccount || usage.reserve.Actor.UserID != admissionUser {
		t.Fatalf("reservation=%+v command=%+v err=%v", reservation, usage.reserve, err)
	}
	reviewerID := ids.UserID("60000000-0000-4000-8000-000000000006")
	reviewBinding, _ := routecontext.Bind(http.MethodPost, "/api/v1/accounts/"+admissionAccount+"/attention/work-reviews", []byte(`{"reviewer_id":"`+string(reviewerID)+`"}`))
	reviewToken, _ := signer.Issue(routecontext.Audience(cellID), authority, reviewBinding)
	reviewClaims, _ := verifier.Verify(reviewToken, reviewBinding)
	reviewContext := routecontext.WithClaims(context.Background(), reviewClaims)
	reviewContext = routecontext.WithProof(reviewContext, routecontext.Proof{Token: reviewToken, Binding: reviewBinding})
	role, active, err := client.ActiveRole(reviewContext, ids.AccountID(admissionAccount), reviewerID)
	if err != nil || !active || role != accounts.RoleMember || reviewers.accountID != admissionAccount || reviewers.userID != reviewerID {
		t.Fatalf("reviewer role=%q active=%t lookup=%+v err=%v", role, active, reviewers, err)
	}
}

func TestClientMapsAdmissionDenialsAndRequiresMatchingOperation(t *testing.T) {
	calls := 0
	client, _ := New("https://admission.test", false, roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusForbidden, Header: http.Header{"Content-Type": []string{"application/problem+json"}}, Body: io.NopCloser(strings.NewReader(`{"type":"https://infiniteocean.net/problems/limit_exceeded","title":"Forbidden","status":403,"code":"limit_exceeded","detail":"capacity reached","current":2,"maximum":2}`))}, nil
	}))
	claims := routecontext.Claims{Authority: routecontext.Authority{AccountID: ids.AccountID(admissionAccount), OperationID: admissionOperation, CellID: "cell-us-east-01"}}
	binding, _ := routecontext.Bind(http.MethodPost, "/api/v1/accounts/"+admissionAccount+"/work-items", nil)
	ctx := routecontext.WithClaims(context.Background(), claims)
	ctx = routecontext.WithProof(ctx, routecontext.Proof{Token: "proof", Binding: binding})
	_, err := client.Reserve(ctx, usageadmission.ReserveCommand{AccountID: ids.AccountID(admissionAccount), PackageCode: catalog.PackageWork, LimitCode: "active_items", Amount: 1, RequestID: admissionOperation})
	if !access.IsDenied(err, access.DenialLimitExceeded) || calls != 1 {
		t.Fatalf("mapped error=%v", err)
	}
	_, err = client.Release(ctx, usageadmission.ReleaseCommand{AccountID: ids.AccountID(admissionAccount), RequestID: "50000000-0000-4000-8000-000000000005"})
	if err != usageadmission.ErrInvalidRequest {
		t.Fatalf("mismatched operation error=%v", err)
	}
}

func TestClientAllowsSignedBaselineMaterializationToReserveChildWorkCapacity(t *testing.T) {
	now := time.Date(2026, 9, 3, 5, 0, 0, 0, time.UTC)
	clock := admissionClock{now}
	key := []byte("0123456789abcdef0123456789abcdef")
	cellID := ids.CellID("cell-us-east-01")
	assessmentID := "51000000-0000-4000-8000-000000000005"
	childWorkID := "52000000-0000-4000-8000-000000000005"
	signer, _ := routecontext.NewSigner("router", "current", key, 20*time.Second, clock)
	verifier, _ := routecontext.NewVerifier("router", routecontext.Audience(cellID), map[string][]byte{"current": key}, routecontext.MaximumLifetime, 0, clock)
	usage := &admissionUsage{result: usageadmission.Reservation{RequestID: childWorkID, State: usageadmission.ReservationActive, Current: 1, Maximum: 100}}
	server, err := admissionapi.New(usage, map[ids.CellID]admissionapi.Verifier{cellID: verifier}, slog.New(slog.NewTextHandler(io.Discard, nil)), admissionapi.DefaultMaxBody)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	client, _ := New(httpServer.URL, true, nil)
	target := "/api/v1/accounts/" + admissionAccount + "/baseline-assessments/" + assessmentID + "/work-materializations"
	binding, _ := routecontext.Bind(http.MethodPost, target, []byte(`{"plan_id":"approved"}`))
	authority := routecontext.Authority{
		RequestID: admissionRoute, OperationID: admissionOperation, AccountID: ids.AccountID(admissionAccount), ActorKind: "user", ActorID: admissionUser,
		Role: "owner", CellID: cellID, PlacementGeneration: 2, EntitlementVersion: 7,
		PackageAccess:   &routecontext.PackageAccess{Code: string(catalog.PackageKnowledge), Version: 1, Mode: string(catalog.ModeEnabled)},
		PackageAccesses: []routecontext.PackageAccess{{Code: string(catalog.PackageWork), Version: 1, Mode: string(catalog.ModeEnabled), Limits: map[string]int64{"active_items": 100}, LimitPolicies: map[string]routecontext.LimitPolicy{"active_items": {Kind: "capacity", Combine: "maximum"}}}},
	}
	token, _ := signer.Issue(routecontext.Audience(cellID), authority, binding)
	claims, _ := verifier.Verify(token, binding)
	ctx := routecontext.WithProof(routecontext.WithClaims(context.Background(), claims), routecontext.Proof{Token: token, Binding: binding})
	reservation, err := client.Reserve(ctx, usageadmission.ReserveCommand{Actor: access.Actor{UserID: ids.UserID(admissionUser)}, AccountID: ids.AccountID(admissionAccount), PackageCode: catalog.PackageWork, LimitCode: "active_items", Amount: 1, RequestID: childWorkID})
	if err != nil || reservation.RequestID != childWorkID || usage.reserve.RequestID != childWorkID {
		t.Fatalf("reservation=%+v command=%+v err=%v", reservation, usage.reserve, err)
	}
}

func TestClientRetriesIdempotentAdmissionTransportFailureOnce(t *testing.T) {
	var payloads []string
	client, _ := New("https://admission.test", false, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(request.Body)
		payloads = append(payloads, string(body))
		if len(payloads) == 1 {
			return nil, errors.New("connection reset after dispatch")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"request_id":"` + admissionOperation + `","state":"active","current":4,"maximum":100,"newly_created":false}`))}, nil
	}))
	claims := routecontext.Claims{Authority: routecontext.Authority{AccountID: ids.AccountID(admissionAccount), OperationID: admissionOperation, CellID: "cell-us-east-01"}}
	binding, _ := routecontext.Bind(http.MethodPost, "/api/v1/accounts/"+admissionAccount+"/work-items", nil)
	ctx := routecontext.WithClaims(context.Background(), claims)
	ctx = routecontext.WithProof(ctx, routecontext.Proof{Token: "proof", Binding: binding})
	reservation, err := client.Reserve(ctx, usageadmission.ReserveCommand{AccountID: ids.AccountID(admissionAccount), PackageCode: catalog.PackageWork, LimitCode: "active_items", Amount: 1, RequestID: admissionOperation})
	if err != nil || reservation.State != usageadmission.ReservationActive || len(payloads) != 2 || payloads[0] != payloads[1] {
		t.Fatalf("reservation=%+v payloads=%v err=%v", reservation, payloads, err)
	}
	if stats := client.TransportStats(); stats.RetryAttempts != 1 || stats.RetryRecovered != 1 || stats.RequestsFailed != 0 {
		t.Fatalf("transport stats=%+v", stats)
	}
}

func TestClientRejectsNonOriginAdmissionTargets(t *testing.T) {
	for _, target := range []string{"http://admission.test", "https://user@admission.test", "https://admission.test/path", "https://admission.test?", "https://admission.test?query=value", "https://admission.test#fragment"} {
		t.Run(target, func(t *testing.T) {
			if _, err := New(target, false, nil); err == nil {
				t.Fatal("expected unsafe admission target to fail closed")
			}
		})
	}
}

func TestWorkAgentAuthorizerUsesWorkloadRequestWithoutRouteProof(t *testing.T) {
	cellID := ids.CellID("cell-us-east-01")
	executionID := "70000000-0000-4000-8000-000000000007"
	calls := 0
	authorizer, err := NewWorkAgentAuthorizer("https://admission.test", cellID, false, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Path != "/internal/v1/agents/work-executions:authorize" || request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
			t.Fatalf("request path=%q authorization=%q cookie=%q", request.URL.Path, request.Header.Get("Authorization"), request.Header.Get("Cookie"))
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"cell_id":"cell-us-east-01","account_id":"` + admissionAccount + `","user_id":"` + admissionUser + `","execution_id":"` + executionID + `","entitlement_version":9,"maximum_concurrent_runs":3}`))}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := authorizer.Authorize(context.Background(), workagent.Snapshot{ExecutionID: executionID, AccountID: admissionAccount, UserID: admissionUser})
	if err != nil || result.EntitlementVersion != 9 || result.MaximumConcurrentRun != 3 || calls != 1 {
		t.Fatalf("authorization=%+v calls=%d err=%v", result, calls, err)
	}
}

func TestScheduleExecutionAuthorizerBindsSnapshotAndContextPackages(t *testing.T) {
	cellID := ids.CellID("cell-us-east-01")
	scheduleID := ids.ScheduleID("70000000-0000-4000-8000-000000000007")
	workID := ids.WorkItemID("71000000-0000-4000-8000-000000000007")
	next := time.Date(2026, 8, 24, 13, 0, 0, 0, time.UTC)
	schedule, err := scheduledomain.Restore(scheduledomain.Schedule{ID: scheduleID, AccountID: admissionAccount, Name: "Daily review", Timezone: "America/New_York",
		Recurrence:      scheduledomain.Recurrence{Frequency: scheduledomain.FrequencyDaily, LocalHour: 9, GapPolicy: scheduledomain.GapSkip, OverlapPolicy: scheduledomain.OverlapFirst},
		MissedRunPolicy: scheduledomain.MissedSkip, Template: scheduledomain.AgentRunTemplate{BoardroomID: "72000000-0000-4000-8000-000000000007", Mode: "selected", PersonaIDs: []ids.PersonaID{"73000000-0000-4000-8000-000000000007"}, Subject: "Daily review", Prompt: "Review priorities.", WorkItemIDs: []ids.WorkItemID{workID}},
		State: scheduledomain.StateActive, Version: 1, NextRunAt: &next, CreatedBy: admissionUser, CreatedAt: next.Add(-time.Hour), UpdatedAt: next.Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	authorizer, err := NewScheduleExecutionAuthorizer("https://admission.test", cellID, false, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(request.Body)
		if request.URL.Path != "/internal/v1/agents/schedule-executions:authorize" || request.Header.Get("Authorization") != "" ||
			!strings.Contains(string(body), `"requires_work":true`) || !strings.Contains(string(body), `"requires_knowledge":false`) {
			t.Fatalf("path=%q body=%s", request.URL.Path, body)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"cell_id":"cell-us-east-01","account_id":"` + admissionAccount + `","user_id":"` + admissionUser + `","schedule_id":"` + string(scheduleID) + `","entitlement_version":9,"maximum_concurrent_runs":3,"can_read_restricted":true}`))}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := authorizer.Authorize(context.Background(), scheduleapp.ExecutionSnapshot{Schedule: schedule})
	if err != nil || result.EntitlementVersion != 9 || result.MaximumConcurrentRun != 3 || !result.CanReadRestricted || calls != 1 {
		t.Fatalf("authorization=%+v calls=%d err=%v", result, calls, err)
	}
}

type admissionUsage struct {
	reserve usageadmission.ReserveCommand
	result  usageadmission.Reservation
}

type admissionReviewers struct {
	accountID ids.AccountID
	userID    ids.UserID
	role      accounts.MembershipRole
	active    bool
}

func (r *admissionReviewers) ActiveRole(_ context.Context, accountID ids.AccountID, userID ids.UserID) (accounts.MembershipRole, bool, error) {
	r.accountID, r.userID = accountID, userID
	return r.role, r.active, nil
}

func (u *admissionUsage) Reserve(_ context.Context, command usageadmission.ReserveCommand) (usageadmission.Reservation, error) {
	u.reserve = command
	return u.result, nil
}
func (u *admissionUsage) Release(context.Context, usageadmission.ReleaseCommand) (usageadmission.Reservation, error) {
	return u.result, nil
}

type admissionClock struct{ now time.Time }

func (c admissionClock) Now() time.Time { return c.now }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

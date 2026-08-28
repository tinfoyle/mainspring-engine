package operationsapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/analyticsreport"
	"github.com/tinfoyle/spyglass-engine/internal/application/operationsconsole"
	"github.com/tinfoyle/spyglass-engine/internal/application/passkeys"
	"github.com/tinfoyle/spyglass-engine/internal/modules/operations"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/transport/operationsapi"
)

const (
	staffID   = ids.UserID("63000000-0000-4000-8000-000000000001")
	targetID  = ids.UserID("63000000-0000-4000-8000-000000000002")
	accountID = ids.AccountID("63000000-0000-4000-8000-000000000003")
	sessionID = ids.SessionID("63000000-0000-4000-8000-000000000004")
	grantID   = ids.OperationsSupportGrantID("63000000-0000-4000-8000-000000000005")
)

var testNow = time.Date(2026, 8, 27, 22, 0, 0, 0, time.UTC)

type consoleFake struct {
	staff           operations.Staff
	staffErr        error
	authErr         error
	lookupQuery     operations.LookupQuery
	lookupActor     ids.UserID
	viewAudit       operations.AuditReason
	viewActor       ids.UserID
	viewGrant       ids.OperationsSupportGrantID
	logoutRecorded  bool
	createdCommand  operationsconsole.CreateGrantCommand
	revokedGrant    ids.OperationsSupportGrantID
	analyticsCalled bool
}

func (fake *consoleFake) Staff(context.Context, ids.UserID) (operations.Staff, error) {
	return fake.staff, fake.staffErr
}
func (fake *consoleFake) RecordAuthentication(context.Context, ids.UserID, ids.SessionID) (operations.Staff, error) {
	return fake.staff, fake.authErr
}
func (fake *consoleFake) RecordLogout(context.Context, ids.UserID, ids.SessionID) error {
	fake.logoutRecorded = true
	return nil
}
func (fake *consoleFake) Lookup(_ context.Context, actor ids.UserID, query operations.LookupQuery) ([]operations.LookupResult, error) {
	fake.lookupActor, fake.lookupQuery = actor, query
	return []operations.LookupResult{{UserID: targetID, AccountID: accountID}}, nil
}
func (fake *consoleFake) CreateGrant(_ context.Context, command operationsconsole.CreateGrantCommand) (operations.SupportGrant, error) {
	fake.createdCommand = command
	return operations.SupportGrant{ID: grantID, StaffUserID: staffID, TargetUserID: targetID, AccountID: accountID,
		State: operations.GrantActive, Ticket: command.Audit.Ticket, Reason: command.Audit.Reason, CreatedAt: testNow,
		ExpiresAt: testNow.Add(command.Lifetime), Version: 1}, nil
}
func (fake *consoleFake) ViewAccount(_ context.Context, actor ids.UserID, grant ids.OperationsSupportGrantID, audit operations.AuditReason) (operations.AccountView, error) {
	fake.viewActor, fake.viewGrant, fake.viewAudit = actor, grant, audit
	return operations.AccountView{Grant: operations.SupportGrant{ID: grant, StaffUserID: actor, TargetUserID: targetID, AccountID: accountID, State: operations.GrantActive}}, nil
}
func (fake *consoleFake) RevokeGrant(_ context.Context, _ ids.UserID, grant ids.OperationsSupportGrantID, _ uint64, _ operations.AuditReason) (operations.SupportGrant, error) {
	fake.revokedGrant = grant
	return operations.SupportGrant{ID: grant, State: operations.GrantRevoked, Version: 2}, nil
}
func (fake *consoleFake) Analytics(context.Context, ids.UserID, analyticsreport.Query, operations.AuditReason) (analyticsreport.Report, error) {
	fake.analyticsCalled = true
	return analyticsreport.Report{Rows: []analyticsreport.Row{}}, nil
}

type passkeyFake struct {
	issued sessions.Issued
	err    error
}

func (fake *passkeyFake) BeginLogin(context.Context, [32]byte) (passkeys.BeginResult, error) {
	return passkeys.BeginResult{CeremonyID: string(sessionID), PublicKey: json.RawMessage(`{"challenge":"fixture"}`), ExpiresAt: testNow.Add(time.Minute)}, fake.err
}
func (fake *passkeyFake) CompleteLogin(context.Context, passkeys.LoginCommand) (sessions.Issued, error) {
	return fake.issued, fake.err
}

type sessionsFake struct {
	authenticated sessions.Authenticated
	authErr       error
	revoked       bool
}

func (fake *sessionsFake) Authenticate(context.Context, string) (sessions.Authenticated, error) {
	return fake.authenticated, fake.authErr
}
func (fake *sessionsFake) RevokeOwned(context.Context, ids.UserID, ids.SessionID) (bool, error) {
	fake.revoked = true
	return true, nil
}

func fixture(t *testing.T) (*operationsapi.Server, *consoleFake, *passkeyFake, *sessionsFake) {
	t.Helper()
	staff := operations.Staff{UserID: staffID, DisplayName: "Support Person", State: operations.StaffActive, Roles: []operations.StaffRole{operations.RoleSupport, operations.RoleAnalytics}}
	console := &consoleFake{staff: staff}
	issued := sessions.Issued{Token: "staff-token", Session: sessions.Session{ID: sessionID, UserID: staffID, AuthenticationMethod: sessions.AuthenticationMethodPasskey, ReauthenticationMethod: sessions.AuthenticationMethodPasskey, ExpiresAt: testNow.Add(time.Hour)}}
	passkey := &passkeyFake{issued: issued}
	sessionService := &sessionsFake{authenticated: sessions.Authenticated{Session: issued.Session}}
	server, err := operationsapi.New(console, passkey, sessionService, operationsapi.Cookie{Name: "__Host-spyglass_operations", Secure: true}, "https://ops.infiniteocean.net", 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return server, console, passkey, sessionService
}

func request(t *testing.T, server *operationsapi.Server, method, path, body string, withCookie, withOrigin bool) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if withCookie {
		req.AddCookie(&http.Cookie{Name: "__Host-spyglass_operations", Value: "staff-token"})
	}
	if withOrigin {
		req.Header.Set("Origin", "https://ops.infiniteocean.net")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	server.Handler().ServeHTTP(recorder, req)
	return recorder
}

func TestOperationsBoundaryRejectsCrossSiteAndCustomerSession(t *testing.T) {
	server, _, _, sessionStore := fixture(t)
	crossSite := request(t, server, http.MethodPost, "/api/operations/v1/lookups", `{}`, true, false)
	if crossSite.Code != http.StatusForbidden {
		t.Fatalf("cross-site status=%d", crossSite.Code)
	}
	unauthenticated := request(t, server, http.MethodGet, "/api/operations/v1/session", "", false, false)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d", unauthenticated.Code)
	}
	sessionStore.authenticated.Session.AuthenticationMethod = sessions.AuthenticationMethodPassword
	password := request(t, server, http.MethodGet, "/api/operations/v1/session", "", true, false)
	if password.Code != http.StatusUnauthorized || !strings.Contains(password.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("password status=%d cookie=%q", password.Code, password.Header().Get("Set-Cookie"))
	}
}

func TestPasskeyLoginIssuesOnlyStrictOperationsCookieAndAuditsActor(t *testing.T) {
	server, _, _, _ := fixture(t)
	recorder := request(t, server, http.MethodPost, "/api/operations/v1/passkey-login/challenges/63000000-0000-4000-8000-000000000004/complete", `{"credential":{"id":"fixture"},"client_label":"Support browser"}`, false, true)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	cookie := recorder.Header().Get("Set-Cookie")
	if !strings.Contains(cookie, "__Host-spyglass_operations=staff-token") || !strings.Contains(cookie, "SameSite=Strict") || !strings.Contains(cookie, "HttpOnly") || !strings.Contains(cookie, "Secure") || strings.Contains(cookie, "spyglass_session") {
		t.Fatalf("unsafe operations cookie: %q", cookie)
	}
}

func TestFailedAuthenticationAuditRevokesIssuedSession(t *testing.T) {
	server, console, _, sessions := fixture(t)
	console.authErr = operations.ErrStaffUnauthorized
	recorder := request(t, server, http.MethodPost, "/api/operations/v1/passkey-login/challenges/63000000-0000-4000-8000-000000000004/complete", `{"credential":{"id":"fixture"}}`, false, true)
	if recorder.Code != http.StatusForbidden || !sessions.revoked || recorder.Header().Get("Set-Cookie") != "" {
		t.Fatalf("status=%d revoked=%v cookie=%q", recorder.Code, sessions.revoked, recorder.Header().Get("Set-Cookie"))
	}
}

func TestLookupAndReadOnlyViewPreserveStaffAuditScope(t *testing.T) {
	server, console, _, _ := fixture(t)
	lookup := request(t, server, http.MethodPost, "/api/operations/v1/lookups", `{"kind":"email","value":"customer@example.com","ticket":"SUP-630","reason":"Customer requested account help."}`, true, true)
	if lookup.Code != http.StatusOK || console.lookupActor != staffID || console.lookupQuery.Audit.Ticket != "SUP-630" {
		t.Fatalf("lookup status=%d actor=%s query=%+v body=%s", lookup.Code, console.lookupActor, console.lookupQuery, lookup.Body.String())
	}
	view := request(t, server, http.MethodPost, "/api/operations/v1/support-grants/63000000-0000-4000-8000-000000000005/views", `{"ticket":"SUP-630","reason":"Inspect the customer-safe account projection."}`, true, true)
	if view.Code != http.StatusOK || console.viewActor != staffID || console.viewGrant != grantID || !strings.Contains(view.Body.String(), `"mode":"read_only_support_view"`) {
		t.Fatalf("view status=%d actor=%s grant=%s body=%s", view.Code, console.viewActor, console.viewGrant, view.Body.String())
	}
}

func TestGrantAnalyticsAndLogoutRoutesAreExplicit(t *testing.T) {
	server, console, _, sessions := fixture(t)
	grant := request(t, server, http.MethodPost, "/api/operations/v1/support-grants", `{"target_user_id":"63000000-0000-4000-8000-000000000002","account_id":"63000000-0000-4000-8000-000000000003","lifetime_seconds":900,"ticket":"SUP-630","reason":"Customer requested account inspection."}`, true, true)
	if grant.Code != http.StatusCreated || console.createdCommand.ActorUserID != staffID || console.createdCommand.Lifetime != 15*time.Minute {
		t.Fatalf("grant status=%d command=%+v body=%s", grant.Code, console.createdCommand, grant.Body.String())
	}
	analytics := request(t, server, http.MethodPost, "/api/operations/v1/analytics/reports", `{"from":"2026-08-26T00:00:00Z","to":"2026-08-27T00:00:00Z","bucket":"day","dimension":"none","minimum_cohort":5,"ticket":"AN-630","reason":"Review the private aggregate launch funnel."}`, true, true)
	if analytics.Code != http.StatusOK || !console.analyticsCalled {
		t.Fatalf("analytics status=%d body=%s", analytics.Code, analytics.Body.String())
	}
	logout := request(t, server, http.MethodDelete, "/api/operations/v1/session", "", true, true)
	if logout.Code != http.StatusNoContent || !console.logoutRecorded || !sessions.revoked || !strings.Contains(logout.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("logout status=%d audit=%v revoked=%v cookie=%q", logout.Code, console.logoutRecorded, sessions.revoked, logout.Header().Get("Set-Cookie"))
	}
}

func TestOperationsErrorsDoNotLeakDatabaseDetails(t *testing.T) {
	server, console, _, _ := fixture(t)
	console.staffErr = errors.New("password=secret postgres unavailable")
	recorder := request(t, server, http.MethodGet, "/api/operations/v1/session", "", true, false)
	if recorder.Code != http.StatusForbidden || strings.Contains(recorder.Body.String(), "password=secret") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

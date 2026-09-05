package operationsapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/application/trafficreport"
	"github.com/tinfoyle/spyglass-engine/internal/modules/operations"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/transport/operationsapi"
)

type trafficFake struct {
	calls int
	err   error
}

func (f *trafficFake) Report(context.Context, ids.UserID, trafficreport.Query, operations.AuditReason) (trafficreport.Report, error) {
	f.calls++
	return trafficreport.Report{Requests: 7}, f.err
}

func TestTrafficRequiresAdministratorPasskeyAndSameOrigin(t *testing.T) {
	for _, test := range []struct {
		name                              string
		role                              operations.StaffRole
		cookie, origin, password, revoked bool
		status                            int
	}{
		{name: "administrator", role: operations.RoleAdministrator, cookie: true, origin: true, status: 200},
		{name: "analytics cannot read IPs", role: operations.RoleAnalytics, cookie: true, origin: true, status: 403},
		{name: "support cannot read IPs", role: operations.RoleSupport, cookie: true, origin: true, status: 403},
		{name: "missing session", role: operations.RoleAdministrator, origin: true, status: 401},
		{name: "missing origin", role: operations.RoleAdministrator, cookie: true, status: 403},
		{name: "password session", role: operations.RoleAdministrator, cookie: true, origin: true, password: true, status: 401},
		{name: "revoked staff", role: operations.RoleAdministrator, cookie: true, origin: true, revoked: true, status: 403},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, console, passkey, session := fixture(t)
			console.staff.Roles = []operations.StaffRole{test.role}
			if test.password {
				session.authenticated.Session.AuthenticationMethod = sessions.AuthenticationMethodPassword
			}
			if test.revoked {
				console.staffErr = operations.ErrStaffUnauthorized
			}
			traffic := &trafficFake{}
			server, err := operationsapi.New(console, passkey, session, operationsapi.OperatorServices{Billing: &billingOperatorFake{}, Privacy: &privacyOperatorFake{}, Affiliate: &affiliateOperatorFake{}, Traffic: traffic}, operationsapi.Cookie{Name: "__Host-spyglass_operations", Secure: true}, "https://ops.infiniteocean.net", "local", 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			response := request(t, server, http.MethodPost, "/api/operations/v1/traffic/reports", `{"from":"2026-09-05T00:00:00Z","to":"2026-09-05T01:00:00Z","ticket":"OPS-100","reason":"Review site traffic"}`, test.cookie, test.origin)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if (traffic.calls == 1) != (test.status == 200) {
				t.Fatalf("unexpected report read: %d", traffic.calls)
			}
		})
	}
}

func TestMissingTrafficCollectionIsNotAZeroReport(t *testing.T) {
	server, console, _, _ := fixture(t)
	console.staff.Roles = []operations.StaffRole{operations.RoleAdministrator}
	response := request(t, server, http.MethodPost, "/api/operations/v1/traffic/reports", `{}`, true, true)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

package operationsapi_test

import (
	"github.com/tinfoyle/spyglass-engine/internal/modules/operations"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"net/http"
	"testing"
)

func TestDirectoryAuthorizationAndAuditScope(t *testing.T) {
	for _, tc := range []struct {
		name                              string
		role                              operations.StaffRole
		cookie, origin, password, revoked bool
		status                            int
	}{
		{"administrator", operations.RoleAdministrator, true, true, false, false, 200},
		{"support", operations.RoleSupport, true, true, false, false, 403},
		{"analytics", operations.RoleAnalytics, true, true, false, false, 403},
		{"anonymous", operations.RoleAdministrator, false, true, false, false, 401},
		{"cross origin", operations.RoleAdministrator, true, false, false, false, 403},
		{"password only", operations.RoleAdministrator, true, true, true, false, 401},
		{"revoked staff", operations.RoleAdministrator, true, true, false, true, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, console, _, session := fixture(t)
			console.staff.Roles = []operations.StaffRole{tc.role}
			if tc.password {
				session.authenticated.Session.AuthenticationMethod = sessions.AuthenticationMethodPassword
			}
			if tc.revoked {
				console.staffErr = operations.ErrStaffUnauthorized
			}
			response := request(t, server, http.MethodPost, "/api/operations/v1/directory", `{"kind":"teams","page":2,"page_size":25,"ticket":"OPS-100","reason":"Review team registrations."}`, tc.cookie, tc.origin)
			if response.Code != tc.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if tc.status == 200 {
				if console.directoryActor != staffID || console.directoryQuery.Page != 2 || console.directoryQuery.Kind != "teams" || console.directoryQuery.Audit.Ticket != "OPS-100" {
					t.Fatal("directory scope was not preserved")
				}
			} else if console.directoryActor != "" {
				t.Fatal("unauthorized directory call")
			}
		})
	}
}

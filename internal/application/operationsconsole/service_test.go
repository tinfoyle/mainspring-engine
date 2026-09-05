package operationsconsole_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/analyticsreport"
	"github.com/tinfoyle/spyglass-engine/internal/application/operationsconsole"
	"github.com/tinfoyle/spyglass-engine/internal/modules/operations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	staffID   = ids.UserID("10000000-0000-4000-8000-000000000001")
	targetID  = ids.UserID("10000000-0000-4000-8000-000000000002")
	accountID = ids.AccountID("10000000-0000-4000-8000-000000000003")
	grantID   = ids.OperationsSupportGrantID("10000000-0000-4000-8000-000000000004")
	sessionID = ids.SessionID("10000000-0000-4000-8000-000000000005")
)

var now = time.Date(2026, 8, 27, 20, 0, 0, 0, time.UTC)

type clock struct{}

func (clock) Now() time.Time { return now }

type generator struct{ next []string }

func (g *generator) New() string {
	if len(g.next) == 0 {
		return "10000000-0000-4000-8000-000000000099"
	}
	value := g.next[0]
	g.next = g.next[1:]
	return value
}

type repository struct {
	staff     operations.Staff
	grant     operations.SupportGrant
	created   operations.SupportGrant
	viewed    bool
	directory *operations.DirectoryQuery
	analytics bool
}

func (r *repository) Staff(context.Context, ids.UserID) (operations.Staff, error) {
	return r.staff, nil
}
func (r *repository) Directory(_ context.Context, _ operations.Staff, q operations.DirectoryQuery, _ ids.OperationsAuditEventID, _ string) (operations.DirectoryPage, error) {
	r.directory = &q
	return operations.DirectoryPage{Kind: q.Kind, Page: q.Page, PageSize: q.PageSize, Users: []operations.DirectoryUser{}, Teams: []operations.DirectoryTeam{}}, nil
}
func (r *repository) Lookup(context.Context, operations.Staff, operations.LookupQuery, ids.OperationsAuditEventID, string, time.Time) ([]operations.LookupResult, error) {
	return []operations.LookupResult{{UserID: targetID, AccountID: accountID}}, nil
}
func (r *repository) CreateGrant(_ context.Context, grant operations.SupportGrant, _ ids.OperationsAuditEventID, _ string) (operations.SupportGrant, error) {
	r.created = grant
	r.grant = grant
	return grant, nil
}
func (r *repository) Grant(context.Context, ids.OperationsSupportGrantID, ids.UserID) (operations.SupportGrant, error) {
	return r.grant, nil
}
func (r *repository) ViewAccount(_ context.Context, _ operations.Staff, grant operations.SupportGrant, _ operations.AuditReason, _ ids.OperationsAuditEventID, _ string, _ time.Time) (operations.AccountView, error) {
	r.viewed = true
	return operations.AccountView{Grant: grant}, nil
}
func (r *repository) RevokeGrant(_ context.Context, _ operations.Staff, id ids.OperationsSupportGrantID, _ uint64, _ operations.AuditReason, _ ids.OperationsAuditEventID, _ string, at time.Time) (operations.SupportGrant, error) {
	r.grant.ID, r.grant.State, r.grant.RevokedAt, r.grant.Version = id, operations.GrantRevoked, &at, r.grant.Version+1
	return r.grant, nil
}
func (r *repository) CustomerHistory(context.Context, ids.UserID, ids.AccountID, int) ([]operations.AccessEvent, error) {
	return nil, nil
}
func (r *repository) Analytics(_ context.Context, _ operations.Staff, query analyticsreport.Query, _ operations.AuditReason, _ ids.OperationsAuditEventID, _ string, _ time.Time) (analyticsreport.Report, error) {
	r.analytics = true
	return analyticsreport.Report{From: query.From, To: query.To, Bucket: query.Bucket, Dimension: query.Dimension, MinimumCohort: query.MinimumCohort, Rows: []analyticsreport.Row{}}, nil
}
func (r *repository) RecordAuthentication(context.Context, operations.Staff, ids.SessionID, ids.OperationsAuditEventID, string, time.Time) error {
	return nil
}
func (r *repository) RecordLogout(context.Context, operations.Staff, ids.SessionID, ids.OperationsAuditEventID, string, time.Time) error {
	return nil
}

func service(t *testing.T, roles ...operations.StaffRole) (*operationsconsole.Service, *repository) {
	t.Helper()
	repo := &repository{staff: operations.Staff{UserID: staffID, DisplayName: "Support Operator", State: operations.StaffActive, Roles: roles}}
	ids := &generator{next: []string{string(grantID), "10000000-0000-4000-8000-000000000006", "10000000-0000-4000-8000-000000000007"}}
	service, err := operationsconsole.New(repo, ids, clock{}, "local")
	if err != nil {
		t.Fatal(err)
	}
	return service, repo
}

func TestSupportGrantIsExactShortLivedAndReadOnly(t *testing.T) {
	service, repo := service(t, operations.RoleSupport)
	grant, err := service.CreateGrant(context.Background(), operationsconsole.CreateGrantCommand{
		ActorUserID: staffID, TargetUserID: targetID, AccountID: accountID,
		Audit: operations.AuditReason{Ticket: "SUP-100", Reason: "Customer requested help reviewing account state."},
	})
	if err != nil {
		t.Fatal(err)
	}
	if grant.ID != grantID || grant.StaffUserID != staffID || grant.TargetUserID != targetID || grant.AccountID != accountID || grant.ExpiresAt.Sub(grant.CreatedAt) != operations.DefaultGrantLifetime {
		t.Fatalf("unexpected grant: %+v", grant)
	}
	view, err := service.ViewAccount(context.Background(), staffID, grant.ID, operations.AuditReason{Ticket: "SUP-100", Reason: "Inspect the exact customer support view."})
	if err != nil || !repo.viewed || view.Grant.ID != grant.ID {
		t.Fatalf("view=%+v err=%v", view, err)
	}
	revoked, err := service.RevokeGrant(context.Background(), staffID, grant.ID, 1, operations.AuditReason{Ticket: "SUP-100", Reason: "Support inspection is complete."})
	if err != nil || revoked.State != operations.GrantRevoked {
		t.Fatalf("revoked=%+v err=%v", revoked, err)
	}
	if _, err := service.ViewAccount(context.Background(), staffID, grant.ID, operations.AuditReason{Ticket: "SUP-100", Reason: "Attempt view after the grant was revoked."}); !errors.Is(err, operations.ErrGrantDenied) {
		t.Fatalf("expected revoked grant denial, got %v", err)
	}
}

func TestSupportGrantRejectsSelfTargetAndExcessLifetime(t *testing.T) {
	service, _ := service(t, operations.RoleSupport)
	base := operationsconsole.CreateGrantCommand{ActorUserID: staffID, TargetUserID: staffID, AccountID: accountID, Audit: operations.AuditReason{Ticket: "SUP-100", Reason: "Customer requested account assistance."}}
	if _, err := service.CreateGrant(context.Background(), base); !errors.Is(err, operations.ErrStaffUnauthorized) {
		t.Fatalf("self target: %v", err)
	}
	base.TargetUserID, base.Lifetime = targetID, operations.MaximumGrantLifetime+time.Second
	if _, err := service.CreateGrant(context.Background(), base); !errors.Is(err, operations.ErrInvalidInput) {
		t.Fatalf("lifetime: %v", err)
	}
}

func TestRolesSeparateAnalyticsFromSupportInspection(t *testing.T) {
	analyticsService, analyticsRepo := service(t, operations.RoleAnalytics)
	query := analyticsreport.Query{From: now.Add(-24 * time.Hour), To: now, Bucket: analyticsreport.BucketDay, Dimension: "none", MinimumCohort: 5}
	if _, err := analyticsService.Analytics(context.Background(), staffID, query, operations.AuditReason{Ticket: "AN-100", Reason: "Review the daily launch funnel."}); err != nil || !analyticsRepo.analytics {
		t.Fatalf("analytics err=%v", err)
	}
	if _, err := analyticsService.Lookup(context.Background(), staffID, operations.LookupQuery{Kind: operations.LookupEmail, Value: "customer@example.com", Audit: operations.AuditReason{Ticket: "SUP-100", Reason: "Look up an exact support target."}}); !errors.Is(err, operations.ErrStaffUnauthorized) {
		t.Fatalf("lookup err=%v", err)
	}

	supportService, _ := service(t, operations.RoleSupport)
	if _, err := supportService.Analytics(context.Background(), staffID, query, operations.AuditReason{Ticket: "AN-100", Reason: "Try analytics without the role."}); !errors.Is(err, operations.ErrStaffUnauthorized) {
		t.Fatalf("analytics role err=%v", err)
	}
}

func TestSuspendedStaffCannotAuthenticate(t *testing.T) {
	service, repo := service(t, operations.RoleAdministrator)
	repo.staff.State = operations.StaffSuspended
	if _, err := service.RecordAuthentication(context.Background(), staffID, sessionID); !errors.Is(err, operations.ErrStaffUnauthorized) {
		t.Fatalf("authentication err=%v", err)
	}
}

func TestCustomerHistoryAlwaysReturnsAnArray(t *testing.T) {
	service, _ := service(t, operations.RoleSupport)
	history, err := service.CustomerHistory(context.Background(), targetID, accountID, 50)
	if err != nil || history == nil || len(history) != 0 {
		t.Fatalf("history=%v err=%v", history, err)
	}
}

func TestDirectoryRequiresAdministratorAndBoundsEveryPage(t *testing.T) {
	q := operations.DirectoryQuery{Kind: "users", Audit: operations.AuditReason{Ticket: "OPS-100", Reason: "Review users and teams."}}
	for _, role := range []operations.StaffRole{operations.RoleSupport, operations.RoleAnalytics, operations.RoleBilling, operations.RolePrivacy, operations.RoleAffiliate} {
		s, r := service(t, role)
		if _, err := s.Directory(context.Background(), staffID, q); !errors.Is(err, operations.ErrStaffUnauthorized) || r.directory != nil {
			t.Fatalf("role %s allowed: %v", role, err)
		}
	}
	s, r := service(t, operations.RoleAdministrator)
	page, err := s.Directory(context.Background(), staffID, q)
	if err != nil || page.Page != 1 || page.PageSize != 25 {
		t.Fatalf("default page: %+v %v", page, err)
	}
	for _, bad := range []operations.DirectoryQuery{
		{Kind: "unknown", Page: 1, PageSize: 25, Audit: q.Audit},
		{Kind: "users", Page: -1, PageSize: 25, Audit: q.Audit},
		{Kind: "users", Page: 1000001, PageSize: 25, Audit: q.Audit},
		{Kind: "users", Page: 1, PageSize: 101, Audit: q.Audit},
		{Kind: "users", Page: 1, PageSize: 25},
	} {
		r.directory = nil
		if _, err := s.Directory(context.Background(), staffID, bad); !errors.Is(err, operations.ErrInvalidInput) || r.directory != nil {
			t.Fatalf("invalid query reached repository: %+v %v", bad, err)
		}
	}
}

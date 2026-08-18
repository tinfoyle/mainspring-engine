package cellapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/routeaccess"
	workapp "github.com/tinfoyle/spyglass-engine/internal/application/work"
	workdomain "github.com/tinfoyle/spyglass-engine/internal/modules/work"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

const (
	workAccount = "10000000-0000-4000-8000-000000000001"
	workUser    = "20000000-0000-4000-8000-000000000002"
	workItemID  = "30000000-0000-4000-8000-000000000003"
)

func TestWorkReadContractsUseSignedAuthorityAndExplicitViews(t *testing.T) {
	now := time.Date(2026, 8, 18, 5, 0, 0, 0, time.UTC)
	item := testWorkItem(t, now)
	repository := &queryRepository{item: item}
	queries, err := workapp.NewQueryService(routeaccess.NewAuthorizer(), repository)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(claimAcceptor{claims: workClaims()}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithWorkQueries(queries))
	if err != nil {
		t.Fatal(err)
	}

	list := routedRequest(t, server.Handler(), "/api/v1/accounts/"+workAccount+"/work-items?state=open&kind=ticket&q=close&limit=1")
	if list.Code != http.StatusOK {
		t.Fatalf("list=%d %s", list.Code, list.Body.String())
	}
	if repository.listQuery.Limit != 1 || repository.listQuery.Search != "close" || len(repository.listQuery.States) != 1 || len(repository.listQuery.Kinds) != 1 {
		t.Fatalf("query=%+v", repository.listQuery)
	}
	if strings.Contains(list.Body.String(), "capacity_reservation") || !strings.Contains(list.Body.String(), `"created_by":{"kind":"user","id":"`+workUser+`"}`) {
		t.Fatalf("unsafe or malformed item view: %s", list.Body.String())
	}
	var page struct {
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &page); err != nil || page.NextCursor == "" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	routedRequest(t, server.Handler(), "/api/v1/accounts/"+workAccount+"/work-items?cursor="+page.NextCursor+"&limit=1")
	if repository.listQuery.AfterUpdatedAt == nil || repository.listQuery.AfterID != item.ID {
		t.Fatalf("decoded cursor query=%+v", repository.listQuery)
	}

	detail := routedRequest(t, server.Handler(), "/api/v1/accounts/"+workAccount+"/work-items/"+workItemID)
	if detail.Code != http.StatusOK || detail.Header().Get("ETag") != `W/"1"` || !strings.Contains(detail.Body.String(), `"number":42`) {
		t.Fatalf("detail=%d etag=%q %s", detail.Code, detail.Header().Get("ETag"), detail.Body.String())
	}
	summary := routedRequest(t, server.Handler(), "/api/v1/accounts/"+workAccount+"/work-items/summary")
	if summary.Code != http.StatusOK || !strings.Contains(summary.Body.String(), `"in_progress":2`) || strings.Contains(summary.Body.String(), `"InProgress"`) {
		t.Fatalf("summary=%d %s", summary.Code, summary.Body.String())
	}
}

func TestWorkReadContractsRejectUnknownQueryAndCrossAccountPath(t *testing.T) {
	repository := &queryRepository{item: testWorkItem(t, time.Now())}
	queries, _ := workapp.NewQueryService(routeaccess.NewAuthorizer(), repository)
	server, _ := New(claimAcceptor{claims: workClaims()}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithWorkQueries(queries))
	invalid := routedRequest(t, server.Handler(), "/api/v1/accounts/"+workAccount+"/work-items?sort=secret")
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "invalid_work_query") {
		t.Fatalf("invalid=%d %s", invalid.Code, invalid.Body.String())
	}
	other := routedRequest(t, server.Handler(), "/api/v1/accounts/40000000-0000-4000-8000-000000000004/work-items")
	if other.Code != http.StatusNotFound {
		t.Fatalf("other=%d %s", other.Code, other.Body.String())
	}
}

func TestWorkDetailRejectsQueryAndChildrenRejectDuplicateLimit(t *testing.T) {
	repository := &queryRepository{item: testWorkItem(t, time.Now())}
	queries, _ := workapp.NewQueryService(routeaccess.NewAuthorizer(), repository)
	server, _ := New(claimAcceptor{claims: workClaims()}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithWorkQueries(queries))

	detail := routedRequest(t, server.Handler(), "/api/v1/accounts/"+workAccount+"/work-items/"+workItemID+"?limit=1")
	if detail.Code != http.StatusBadRequest || !strings.Contains(detail.Body.String(), "invalid_work_query") {
		t.Fatalf("detail query=%d %s", detail.Code, detail.Body.String())
	}
	children := routedRequest(t, server.Handler(), "/api/v1/accounts/"+workAccount+"/work-items/"+workItemID+"/children?limit=1&limit=2")
	if children.Code != http.StatusBadRequest || !strings.Contains(children.Body.String(), "invalid_work_query") {
		t.Fatalf("children query=%d %s", children.Code, children.Body.String())
	}
}

func routedRequest(t *testing.T, handler http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set(RouteContextHeader, "accepted-by-test-boundary")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func workClaims() routecontext.Claims {
	return routecontext.Claims{Authority: routecontext.Authority{AccountID: ids.AccountID(workAccount), ActorKind: "user", ActorID: workUser, Role: "viewer", CellID: "cell-us-east-01", PlacementGeneration: 1, EntitlementVersion: 3, PackageAccess: &routecontext.PackageAccess{Code: "work", Version: 1, Mode: "enabled"}}}
}

func testWorkItem(t *testing.T, now time.Time) workdomain.Item {
	t.Helper()
	draft, err := workdomain.NewDraft(workdomain.Draft{ID: ids.WorkItemID(workItemID), AccountID: ids.AccountID(workAccount), Kind: workdomain.KindTicket, Title: "Close the monthly books", Description: "Review the close checklist.", Priority: workdomain.PriorityUrgent, Assignment: workdomain.Assignment{Responsibility: workdomain.ResponsibilityShared}, Provenance: workdomain.Provenance{Source: workdomain.SourceManual, CreatedBy: workdomain.Actor{Kind: workdomain.ActorUser, ID: workUser}}, CapacityReservationID: workItemID})
	if err != nil {
		t.Fatal(err)
	}
	item, err := workdomain.Materialize(draft, 42, 0, now.UTC())
	if err != nil {
		t.Fatal(err)
	}
	return item
}

type claimAcceptor struct{ claims routecontext.Claims }

func (a claimAcceptor) Accept(context.Context, string, routecontext.Binding) (routecontext.Claims, error) {
	return a.claims, nil
}

type queryRepository struct {
	item      workdomain.Item
	listQuery workapp.ListQuery
}

func (r *queryRepository) Create(context.Context, workdomain.Draft, workapp.Mutation) (workdomain.Item, error) {
	return workdomain.Item{}, nil
}
func (r *queryRepository) Get(context.Context, ids.AccountID, ids.WorkItemID) (workdomain.Item, error) {
	return r.item, nil
}
func (r *queryRepository) Update(context.Context, workdomain.Item, uint64, workapp.Mutation) (workdomain.Item, error) {
	return workdomain.Item{}, nil
}
func (r *queryRepository) MarkCapacityReleased(context.Context, ids.AccountID, ids.WorkItemID, string, time.Time) error {
	return nil
}
func (r *queryRepository) List(_ context.Context, _ ids.AccountID, query workapp.ListQuery) (workapp.Page, error) {
	r.listQuery = query
	return workapp.Page{Items: []workdomain.Item{r.item}, NextCursor: &workapp.Cursor{UpdatedAt: r.item.UpdatedAt, ID: r.item.ID}}, nil
}
func (r *queryRepository) Children(context.Context, ids.AccountID, ids.WorkItemID, int) ([]workdomain.Item, error) {
	return []workdomain.Item{r.item}, nil
}
func (r *queryRepository) Summary(context.Context, ids.AccountID) (workapp.Summary, error) {
	return workapp.Summary{Active: 5, InProgress: 2, Waiting: 1, Urgent: 1, Done: 9}, nil
}

var _ workapp.Repository = (*queryRepository)(nil)

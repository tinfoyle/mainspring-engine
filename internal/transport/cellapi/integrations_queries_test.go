package cellapi

import (
	"context"
	"crypto/sha256"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	integrationsapp "github.com/tinfoyle/spyglass-engine/internal/application/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	integrationsdomain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	"github.com/tinfoyle/spyglass-engine/internal/testsupport/openapifixture"
)

const (
	integrationsAccountID    = "b1000000-0000-4000-8000-000000000001"
	integrationsUserID       = "b2000000-0000-4000-8000-000000000002"
	integrationsConnectionID = "b3000000-0000-4000-8000-000000000003"
	integrationsRevisionID   = "b4000000-0000-4000-8000-000000000004"
	integrationsCredentialID = "b5000000-0000-4000-8000-000000000005"
	integrationsHealthID     = "b6000000-0000-4000-8000-000000000006"
	integrationsExecutionID  = "b7000000-0000-4000-8000-000000000007"
	integrationsReleaseID    = "b8000000-0000-4000-8000-000000000008"
	integrationsApprovalID   = "b9000000-0000-4000-8000-000000000009"
	integrationsAttemptID    = "ba000000-0000-4000-8000-00000000000a"
)

type integrationsQueryTransportService struct {
	now             time.Time
	empty           bool
	connectionQuery integrationsapp.ConnectionListQuery
	healthQuery     integrationsapp.HealthListQuery
	executionQuery  integrationsapp.ExecutionListQuery
}

func (service *integrationsQueryTransportService) connection() integrationsdomain.Connection {
	return integrationsdomain.Connection{ID: integrationsConnectionID, AccountID: integrationsAccountID, Name: "Campaign email", Kind: integrationsdomain.ConnectorEmail,
		State: integrationsdomain.ConnectionActive, CurrentRevisionID: integrationsRevisionID, CurrentRevision: 2, CredentialID: integrationsCredentialID,
		CredentialGeneration: 3, Version: 4, CreatedBy: integrationsdomain.Actor{UserID: integrationsUserID}, CreatedAt: service.now.Add(-time.Hour), UpdatedAt: service.now}
}

func (service *integrationsQueryTransportService) revision() integrationsdomain.ConnectionRevision {
	return integrationsdomain.ConnectionRevision{ID: integrationsRevisionID, AccountID: integrationsAccountID, ConnectionID: integrationsConnectionID,
		Revision: 2, Capabilities: []integrationsdomain.Capability{integrationsdomain.CapabilityEmailSend},
		Scope:     integrationsdomain.ConnectionScope{EmailAddress: "launch@example.com", AudienceReference: "audience:customers-v1"},
		CreatedBy: integrationsdomain.Actor{UserID: integrationsUserID}, CreatedAt: service.now.Add(-30 * time.Minute)}
}

func (service *integrationsQueryTransportService) health() integrationsdomain.HealthObservation {
	return integrationsdomain.HealthObservation{ID: integrationsHealthID, AccountID: integrationsAccountID, ConnectionID: integrationsConnectionID,
		ConnectionRevisionID: integrationsRevisionID, CredentialID: integrationsCredentialID, State: integrationsdomain.HealthDegraded,
		ErrorCode: "provider_rate_limited", LatencyMilliseconds: 850, CheckedAt: service.now}
}

func (service *integrationsQueryTransportService) execution() integrationsdomain.Execution {
	return integrationsdomain.Execution{ID: integrationsExecutionID, AccountID: integrationsAccountID, ReleaseID: integrationsReleaseID, ReleaseVersion: 2,
		ApprovalID: integrationsApprovalID, Capability: integrationsdomain.CapabilityEmailSend, ConnectionID: integrationsConnectionID,
		ConnectionRevisionID: integrationsRevisionID, ConnectionRevision: 2, CredentialID: integrationsCredentialID, CredentialGeneration: 3,
		PayloadSHA256: sha256.Sum256([]byte("canonical integration payload")), State: integrationsdomain.ExecutionSucceeded, AttemptCount: 1,
		CreatedAt: service.now.Add(-time.Minute), UpdatedAt: service.now, CompletedAt: timePointer(service.now)}
}

func (service *integrationsQueryTransportService) GetConnectionDetail(context.Context, access.Actor, ids.AccountID, ids.IntegrationConnectionID) (integrationsapp.ConnectionDetail, error) {
	health := service.health()
	return integrationsapp.ConnectionDetail{Connection: service.connection(), Revision: service.revision(), LatestHealth: &health}, nil
}

func (service *integrationsQueryTransportService) ListConnections(_ context.Context, _ access.Actor, _ ids.AccountID, query integrationsapp.ConnectionListQuery) (integrationsapp.ConnectionPage, error) {
	service.connectionQuery = query
	if service.empty {
		return integrationsapp.ConnectionPage{}, nil
	}
	value := service.connection()
	return integrationsapp.ConnectionPage{Items: []integrationsdomain.Connection{value}, NextCursor: &integrationsapp.ConnectionCursor{UpdatedAt: value.UpdatedAt, ID: value.ID}}, nil
}

func (service *integrationsQueryTransportService) ListHealth(_ context.Context, _ access.Actor, _ ids.AccountID, query integrationsapp.HealthListQuery) (integrationsapp.HealthPage, error) {
	service.healthQuery = query
	if service.empty {
		return integrationsapp.HealthPage{}, nil
	}
	value := service.health()
	return integrationsapp.HealthPage{Items: []integrationsdomain.HealthObservation{value}, NextCursor: &integrationsapp.HealthCursor{CheckedAt: value.CheckedAt, ID: value.ID}}, nil
}

func (service *integrationsQueryTransportService) GetExecution(context.Context, access.Actor, ids.AccountID, ids.IntegrationExecutionID) (integrationsapp.ExecutionDetail, error) {
	value := service.execution()
	attempt := integrationsdomain.Attempt{ID: integrationsAttemptID, AccountID: integrationsAccountID, ExecutionID: value.ID, Number: 1,
		Mode: integrationsdomain.AttemptExecute, Outcome: integrationsdomain.AttemptSucceeded, StartedAt: service.now.Add(-time.Minute),
		LeaseExpiresAt: service.now.Add(time.Minute), CompletedAt: timePointer(service.now)}
	return integrationsapp.ExecutionDetail{Execution: value, Attempts: []integrationsdomain.Attempt{attempt}}, nil
}

func (service *integrationsQueryTransportService) ListExecutions(_ context.Context, _ access.Actor, _ ids.AccountID, query integrationsapp.ExecutionListQuery) (integrationsapp.ExecutionPage, error) {
	service.executionQuery = query
	if service.empty {
		return integrationsapp.ExecutionPage{}, nil
	}
	value := service.execution()
	return integrationsapp.ExecutionPage{Items: []integrationsdomain.Execution{value}, NextCursor: &integrationsapp.ExecutionCursor{UpdatedAt: value.UpdatedAt, ID: value.ID}}, nil
}

func TestIntegrationsEmptyPagesEncodeCollectionsAsArrays(t *testing.T) {
	service := &integrationsQueryTransportService{now: time.Date(2026, 8, 23, 22, 0, 0, 0, time.UTC), empty: true}
	server, err := New(claimAcceptor{claims: integrationsQueryClaims()}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithIntegrations(service))
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/accounts/" + integrationsAccountID + "/integrations"
	for _, target := range []string{base + "/connections", base + "/connections/" + integrationsConnectionID + "/health", base + "/executions"} {
		response := integrationsQueryRequest(server.Handler(), target)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"items":[]`) || strings.Contains(response.Body.String(), `"items":null`) {
			t.Fatalf("%s status=%d body=%s", target, response.Code, response.Body.String())
		}
	}
}

func TestIntegrationsQueryRoutesExposeOnlyTypedContentFreeEvidence(t *testing.T) {
	now := time.Date(2026, 8, 23, 22, 0, 0, 0, time.UTC)
	service := &integrationsQueryTransportService{now: now}
	server, err := New(claimAcceptor{claims: integrationsQueryClaims()}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithIntegrations(service))
	if err != nil {
		t.Fatal(err)
	}
	contract, err := openapifixture.Load(filepath.Join("..", "..", "..", "api", "spyglass.openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/accounts/" + integrationsAccountID + "/integrations"
	tests := []struct {
		name, target string
		check        func(*httptest.ResponseRecorder)
	}{
		{name: "connection page", target: base + "/connections?state=active&kind=email&limit=25", check: func(response *httptest.ResponseRecorder) {
			if service.connectionQuery.Limit != 25 || len(service.connectionQuery.States) != 1 || len(service.connectionQuery.Kinds) != 1 || !strings.Contains(response.Body.String(), `"next_cursor":"`) {
				t.Fatalf("query=%+v body=%s", service.connectionQuery, response.Body.String())
			}
		}},
		{name: "connection detail", target: base + "/connections/" + integrationsConnectionID, check: func(response *httptest.ResponseRecorder) {
			if response.Header().Get("ETag") != `W/"4"` || strings.Contains(response.Body.String(), "reference_sha256") || strings.Contains(response.Body.String(), "provider_payload") {
				t.Fatalf("headers=%v body=%s", response.Header(), response.Body.String())
			}
		}},
		{name: "health page", target: base + "/connections/" + integrationsConnectionID + "/health?limit=10", check: func(response *httptest.ResponseRecorder) {
			if service.healthQuery.ConnectionID != integrationsConnectionID || service.healthQuery.Limit != 10 || !strings.Contains(response.Body.String(), `"error_code":"provider_rate_limited"`) {
				t.Fatalf("query=%+v body=%s", service.healthQuery, response.Body.String())
			}
		}},
		{name: "execution page", target: base + "/executions?connection_id=" + integrationsConnectionID + "&state=succeeded&capability=email.send&limit=5", check: func(response *httptest.ResponseRecorder) {
			if service.executionQuery.ConnectionID != integrationsConnectionID || service.executionQuery.Limit != 5 || !strings.Contains(response.Body.String(), `"payload_sha256":"`) {
				t.Fatalf("query=%+v body=%s", service.executionQuery, response.Body.String())
			}
		}},
		{name: "execution detail", target: base + "/executions/" + integrationsExecutionID, check: func(response *httptest.ResponseRecorder) {
			if !strings.Contains(response.Body.String(), `"mode":"execute"`) || strings.Contains(response.Body.String(), "credential_reference") {
				t.Fatalf("body=%s", response.Body.String())
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := integrationsQueryRequest(server.Handler(), test.target)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			test.check(response)
			if err := contract.ValidateResponse(http.MethodGet, strings.Split(test.target, "?")[0], response.Code, response.Header(), response.Body.Bytes()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestIntegrationsQueryRoutesRejectCrossAccountAndCursorConfusion(t *testing.T) {
	service := &integrationsQueryTransportService{now: time.Date(2026, 8, 23, 22, 0, 0, 0, time.UTC)}
	server, _ := New(claimAcceptor{claims: integrationsQueryClaims()}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithIntegrations(service))
	wrong := integrationsQueryRequest(server.Handler(), "/api/v1/accounts/bb000000-0000-4000-8000-00000000000b/integrations/connections")
	if wrong.Code != http.StatusNotFound {
		t.Fatalf("cross Account=%d body=%s", wrong.Code, wrong.Body.String())
	}
	healthCursor := encodeIntegrationsCursor("health", service.now, integrationsHealthID)
	confused := integrationsQueryRequest(server.Handler(), "/api/v1/accounts/"+integrationsAccountID+"/integrations/connections?cursor="+healthCursor)
	if confused.Code != http.StatusBadRequest || !strings.Contains(confused.Body.String(), "invalid_integrations_query") {
		t.Fatalf("cursor=%d body=%s", confused.Code, confused.Body.String())
	}
}

func integrationsQueryRequest(handler http.Handler, target string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set(RouteContextHeader, "accepted-by-test-boundary")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func integrationsQueryClaims() routecontext.Claims {
	return routecontext.Claims{Authority: routecontext.Authority{AccountID: integrationsAccountID, ActorKind: "user", ActorID: integrationsUserID,
		Role: "viewer", CellID: "cell-us-east-01", PlacementGeneration: 1, EntitlementVersion: 3,
		PackageAccess: &routecontext.PackageAccess{Code: "integrations", Version: 1, Mode: "enabled"}}}
}

func timePointer(value time.Time) *time.Time { return &value }

var _ IntegrationsQueryService = (*integrationsQueryTransportService)(nil)

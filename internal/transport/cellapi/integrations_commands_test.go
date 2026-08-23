package cellapi

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	integrationsapp "github.com/tinfoyle/spyglass-engine/internal/application/integrations"
	integrationsdomain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
	"github.com/tinfoyle/spyglass-engine/internal/testsupport/openapifixture"
)

type integrationsCommandTransportService struct {
	create     integrationsapp.CreateConnectionCommand
	revise     integrationsapp.ReviseConnectionCommand
	credential integrationsapp.CredentialCommand
	transition integrationsapp.TransitionCommand
	prepare    integrationsapp.PrepareExecutionCommand
}

func integrationCommandConnection(version uint64) integrationsdomain.Connection {
	if version == 0 {
		version = 1
	}
	return integrationsdomain.Connection{ID: integrationsConnectionID, AccountID: integrationsAccountID, Name: "Campaign email", Kind: integrationsdomain.ConnectorEmail,
		State: integrationsdomain.ConnectionActive, CurrentRevisionID: integrationsRevisionID, CurrentRevision: 1, CredentialID: integrationsCredentialID,
		CredentialGeneration: 1, Version: version, CreatedBy: integrationsdomain.Actor{UserID: integrationsUserID},
		CreatedAt: time.Date(2026, 8, 23, 22, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 8, 23, 22, 1, 0, 0, time.UTC)}
}

func (service *integrationsCommandTransportService) CreateConnection(_ context.Context, command integrationsapp.CreateConnectionCommand) (integrationsdomain.Connection, bool, error) {
	service.create = command
	value := integrationCommandConnection(1)
	value.State, value.CredentialID, value.CredentialGeneration = integrationsdomain.ConnectionPending, "", 0
	return value, true, nil
}
func (service *integrationsCommandTransportService) ReviseConnection(_ context.Context, command integrationsapp.ReviseConnectionCommand) (integrationsdomain.Connection, error) {
	service.revise = command
	return integrationCommandConnection(command.ExpectedVersion + 1), nil
}
func (service *integrationsCommandTransportService) ActivateConnection(_ context.Context, command integrationsapp.CredentialCommand) (integrationsdomain.Connection, error) {
	service.credential = command
	return integrationCommandConnection(command.ExpectedVersion + 1), nil
}
func (service *integrationsCommandTransportService) RotateCredential(_ context.Context, command integrationsapp.CredentialCommand) (integrationsdomain.Connection, error) {
	service.credential = command
	value := integrationCommandConnection(command.ExpectedVersion + 1)
	value.CredentialGeneration = command.ExpectedGeneration + 1
	return value, nil
}
func (service *integrationsCommandTransportService) DisableConnection(_ context.Context, command integrationsapp.TransitionCommand) (integrationsdomain.Connection, error) {
	service.transition = command
	value := integrationCommandConnection(command.ExpectedVersion + 1)
	value.State = integrationsdomain.ConnectionDisabled
	return value, nil
}
func (service *integrationsCommandTransportService) EnableConnection(_ context.Context, command integrationsapp.TransitionCommand) (integrationsdomain.Connection, error) {
	service.transition = command
	return integrationCommandConnection(command.ExpectedVersion + 1), nil
}
func (service *integrationsCommandTransportService) RevokeConnection(_ context.Context, command integrationsapp.TransitionCommand) (integrationsdomain.Connection, error) {
	service.transition = command
	value := integrationCommandConnection(command.ExpectedVersion + 1)
	value.State = integrationsdomain.ConnectionRevoked
	actor := integrationsdomain.Actor{UserID: integrationsUserID}
	at := value.UpdatedAt
	value.RevokedBy, value.RevokedAt = &actor, &at
	return value, nil
}
func (service *integrationsCommandTransportService) PrepareExecution(_ context.Context, command integrationsapp.PrepareExecutionCommand) (integrationsdomain.Execution, bool, error) {
	service.prepare = command
	return integrationsdomain.Execution{ID: ids.IntegrationExecutionID(command.RequestID), AccountID: command.AccountID, ReleaseID: command.ReleaseID,
		ReleaseVersion: command.ReleaseVersion, ApprovalID: integrationsApprovalID, Capability: command.Capability, ConnectionID: command.ConnectionID,
		ConnectionRevisionID: integrationsRevisionID, ConnectionRevision: 1, CredentialID: integrationsCredentialID, CredentialGeneration: 1,
		PayloadSHA256: sha256.Sum256([]byte("prepared manifest")), State: integrationsdomain.ExecutionPrepared,
		CreatedAt: time.Date(2026, 8, 23, 22, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 8, 23, 22, 0, 0, 0, time.UTC)}, true, nil
}

func TestIntegrationsMutationRoutesBindHumanAuthorityAndTypedContracts(t *testing.T) {
	service := &integrationsCommandTransportService{}
	contract, err := openapifixture.Load(filepath.Join("..", "..", "..", "api", "spyglass.openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/accounts/" + integrationsAccountID + "/integrations"
	digest := strings.Repeat("11", 32)
	tests := []struct {
		name, method, target, body, version string
		check                               func()
	}{
		{name: "create", method: http.MethodPost, target: base + "/connections", body: `{"name":"Campaign email","kind":"email","capabilities":["email.send"],"scope":{"email_address":"launch@example.com","audience_reference":"audience:customers-v1"}}`, check: func() {
			if service.create.Kind != integrationsdomain.ConnectorEmail {
				t.Fatalf("create=%+v", service.create)
			}
		}},
		{name: "revise", method: http.MethodPut, target: base + "/connections/" + integrationsConnectionID, version: `W/"4"`, body: `{"name":"Campaign email v2","capabilities":["email.send"],"scope":{"email_address":"launch@example.com","audience_reference":"audience:customers-v2"}}`, check: func() {
			if service.revise.ExpectedVersion != 4 {
				t.Fatalf("revise=%+v", service.revise)
			}
		}},
		{name: "activate credential", method: http.MethodPost, target: base + "/connections/" + integrationsConnectionID + "/credential-bindings", version: `W/"1"`, body: `{"provider":"smtp_primary","reference_sha256":"` + digest + `"}`, check: func() {
			if service.credential.ExpectedGeneration != 0 || service.credential.ReferenceSHA256 == ([32]byte{}) {
				t.Fatalf("credential=%+v", service.credential)
			}
		}},
		{name: "rotate credential", method: http.MethodPost, target: base + "/connections/" + integrationsConnectionID + "/credential-rotations", version: `W/"2"`, body: `{"expected_generation":1,"provider":"smtp_primary","reference_sha256":"` + digest + `"}`, check: func() {
			if service.credential.ExpectedGeneration != 1 {
				t.Fatalf("credential=%+v", service.credential)
			}
		}},
		{name: "disable", method: http.MethodPost, target: base + "/connections/" + integrationsConnectionID + "/disables", version: `W/"3"`, check: func() {
			if service.transition.ExpectedVersion != 3 {
				t.Fatalf("transition=%+v", service.transition)
			}
		}},
		{name: "enable", method: http.MethodPost, target: base + "/connections/" + integrationsConnectionID + "/enables", version: `W/"4"`, check: func() {
			if service.transition.ExpectedVersion != 4 {
				t.Fatalf("transition=%+v", service.transition)
			}
		}},
		{name: "revoke", method: http.MethodPost, target: base + "/connections/" + integrationsConnectionID + "/revocations", version: `W/"5"`, check: func() {
			if service.transition.ExpectedVersion != 5 {
				t.Fatalf("transition=%+v", service.transition)
			}
		}},
		{name: "prepare", method: http.MethodPost, target: base + "/executions", body: `{"release_id":"` + integrationsReleaseID + `","release_version":2,"capability":"email.send","connection_id":"` + integrationsConnectionID + `"}`, check: func() {
			if service.prepare.ReleaseVersion != 2 || service.prepare.Capability != integrationsdomain.CapabilityEmailSend {
				t.Fatalf("prepare=%+v", service.prepare)
			}
		}},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requestID := fmt.Sprintf("bc%06d-0000-4000-8000-00000000000c", index)
			claims := integrationsMutationClaims(requestID)
			server, err := New(claimAcceptor{claims: claims}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithIntegrationCommands(service))
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(test.method, test.target, strings.NewReader(test.body))
			request.Header.Set(RouteContextHeader, "accepted-by-test-boundary")
			request.Header.Set("Idempotency-Key", requestID)
			if test.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			if test.version != "" {
				request.Header.Set("If-Match", test.version)
			}
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusOK && response.Code != http.StatusCreated {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			test.check()
			if err := contract.ValidateResponse(test.method, test.target, response.Code, response.Header(), response.Body.Bytes()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestIntegrationsMutationRoutesRejectMismatchedIdempotencyAndMissingVersion(t *testing.T) {
	service := &integrationsCommandTransportService{}
	requestID := "bd000000-0000-4000-8000-00000000000d"
	server, _ := New(claimAcceptor{claims: integrationsMutationClaims(requestID)}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultMaxBody, WithIntegrationCommands(service))
	request := httptest.NewRequest(http.MethodPut, "/api/v1/accounts/"+integrationsAccountID+"/integrations/connections/"+integrationsConnectionID, strings.NewReader(`{}`))
	request.Header.Set(RouteContextHeader, "accepted-by-test-boundary")
	request.Header.Set("Idempotency-Key", requestID)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusPreconditionRequired {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+integrationsAccountID+"/integrations/connections", strings.NewReader(`{}`))
	request.Header.Set(RouteContextHeader, "accepted-by-test-boundary")
	request.Header.Set("Idempotency-Key", "be000000-0000-4000-8000-00000000000e")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func integrationsMutationClaims(operationID string) routecontext.Claims {
	return routecontext.Claims{Authority: routecontext.Authority{AccountID: integrationsAccountID, ActorKind: "user", ActorID: integrationsUserID,
		Role: "owner", CellID: "cell-us-east-01", PlacementGeneration: 1, EntitlementVersion: 3, OperationID: operationID,
		PackageAccess: &routecontext.PackageAccess{Code: "integrations", Version: 1, Mode: "enabled"}}}
}

var _ IntegrationsCommandService = (*integrationsCommandTransportService)(nil)

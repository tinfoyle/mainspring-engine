package integrationexecution_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/mockconnector"
	"github.com/tinfoyle/spyglass-engine/internal/application/integrationexecution"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type fixedGenerator string

func (value fixedGenerator) New() string { return string(value) }

type fixedClock struct{ at time.Time }

func (clock *fixedClock) Now() time.Time { return clock.at }

type executionRepository struct {
	claim      integrationexecution.Claim
	found      bool
	completion *integrationexecution.Completion
}

func (repository *executionRepository) Claim(_ context.Context, attemptID ids.IntegrationAttemptID, _ time.Time, lease time.Time) (integrationexecution.Claim, bool, error) {
	claim := repository.claim
	claim.AttemptID, claim.LeaseExpiresAt = attemptID, lease
	return claim, repository.found, nil
}

func (repository *executionRepository) Complete(_ context.Context, completion integrationexecution.Completion) error {
	repository.completion = &completion
	return nil
}

type executionAuthority struct{ err error }

func (authority executionAuthority) Authorize(context.Context, integrationexecution.Claim) error {
	return authority.err
}

type executionPayloadSource struct {
	payload integrationexecution.Payload
	err     error
}

type executionCredentialLease struct {
	material []byte
	closed   bool
}

func (lease *executionCredentialLease) Material() []byte { return lease.material }
func (lease *executionCredentialLease) Close() error {
	lease.closed = true
	return nil
}

type executionCredentialBroker struct {
	lease   *executionCredentialLease
	err     error
	request integrationexecution.CredentialRequest
}

type credentialInspectingConnector struct{ material []byte }

func (connector *credentialInspectingConnector) Execute(_ context.Context, call integrationexecution.ConnectorCall) integrationexecution.ConnectorResult {
	connector.material = call.Credential
	return integrationexecution.ConnectorResult{Outcome: domain.AttemptSucceeded}
}

func (connector *credentialInspectingConnector) Reconcile(_ context.Context, call integrationexecution.ConnectorCall) integrationexecution.ConnectorResult {
	connector.material = call.Credential
	return integrationexecution.ConnectorResult{Outcome: domain.AttemptSucceeded}
}

func (broker *executionCredentialBroker) Acquire(_ context.Context, request integrationexecution.CredentialRequest) (integrationexecution.CredentialLease, error) {
	broker.request = request
	if broker.err != nil {
		return nil, broker.err
	}
	broker.lease = &executionCredentialLease{material: []byte("one-operation-credential")}
	return broker.lease, nil
}

func (source executionPayloadSource) Load(context.Context, integrationexecution.Claim) (integrationexecution.Payload, error) {
	return source.payload, source.err
}

func TestProcessOneExecutesAndSettlesScriptedConnectorOutcome(t *testing.T) {
	now := time.Date(2026, 8, 23, 22, 0, 0, 0, time.UTC)
	manifest := []byte(`{"release_id":"test","version":3}`)
	claim := executionClaim(manifest, domain.AttemptExecute)
	repository := &executionRepository{claim: claim, found: true}
	broker := &executionCredentialBroker{}
	connector := mockconnector.New(map[ids.IntegrationExecutionID][]mockconnector.Step{claim.ExecutionID: {{Mode: domain.AttemptExecute,
		Result: integrationexecution.ConnectorResult{Outcome: domain.AttemptSucceeded}}}})
	service, err := integrationexecution.New(repository, executionAuthority{}, executionPayloadSource{payload: integrationexecution.Payload{
		CanonicalManifest: manifest, ProviderPayload: []byte("provider payload remains runtime-only")}}, broker, fixedGenerator("a1f00000-0000-4000-8000-00000000000f"),
		&fixedClock{at: now}, time.Minute, []integrationexecution.Definition{{Capability: domain.CapabilityEmailSend, Timeout: 10 * time.Second, Connector: connector}})
	if err != nil {
		t.Fatal(err)
	}
	worked, err := service.ProcessOne(context.Background())
	if err != nil || !worked || repository.completion == nil || repository.completion.Outcome != domain.AttemptSucceeded || len(connector.Calls()) != 1 || broker.lease == nil || !broker.lease.closed {
		t.Fatalf("worked=%t completion=%+v calls=%v err=%v", worked, repository.completion, connector.Calls(), err)
	}
	if broker.request.CredentialProvider != claim.CredentialProvider || broker.request.ReferenceSHA256 != claim.CredentialReferenceSHA256 {
		t.Fatalf("broker request did not preserve credential attestation: %+v", broker.request)
	}
}

func TestProcessOneReconcilesUnknownAndPermitsRetryOnlyAfterDefiniteAbsence(t *testing.T) {
	now := time.Date(2026, 8, 23, 22, 0, 0, 0, time.UTC)
	manifest := []byte(`{"release_id":"test","version":3}`)
	claim := executionClaim(manifest, domain.AttemptReconcile)
	retryAt := now.Add(time.Minute)
	repository := &executionRepository{claim: claim, found: true}
	connector := mockconnector.New(map[ids.IntegrationExecutionID][]mockconnector.Step{claim.ExecutionID: {{Mode: domain.AttemptReconcile,
		Result: integrationexecution.ConnectorResult{Outcome: domain.AttemptNotApplied, ErrorCode: "provider_confirmed_absent", RetryAt: &retryAt}}}})
	service, err := integrationexecution.New(repository, executionAuthority{}, executionPayloadSource{payload: integrationexecution.Payload{
		CanonicalManifest: manifest, ProviderPayload: []byte("runtime payload")}}, &executionCredentialBroker{}, fixedGenerator("a2f00000-0000-4000-8000-00000000000f"),
		&fixedClock{at: now}, time.Minute, []integrationexecution.Definition{{Capability: domain.CapabilityEmailSend, Timeout: 10 * time.Second, Connector: connector}})
	if err != nil {
		t.Fatal(err)
	}
	worked, err := service.ProcessOne(context.Background())
	if !worked || !errors.Is(err, integrationexecution.ErrUnavailable) || repository.completion == nil ||
		repository.completion.Outcome != domain.AttemptNotApplied || repository.completion.RetryAt == nil || !repository.completion.RetryAt.Equal(retryAt) ||
		connector.Calls()[0].Mode != domain.AttemptReconcile {
		t.Fatalf("worked=%t completion=%+v calls=%v err=%v", worked, repository.completion, connector.Calls(), err)
	}
}

func TestProcessOneZerosCredentialViewAndClosesOneOperationLease(t *testing.T) {
	now := time.Date(2026, 8, 23, 22, 0, 0, 0, time.UTC)
	manifest := []byte(`{"release_id":"test","version":3}`)
	claim := executionClaim(manifest, domain.AttemptExecute)
	repository := &executionRepository{claim: claim, found: true}
	broker := &executionCredentialBroker{}
	connector := &credentialInspectingConnector{}
	service, err := integrationexecution.New(repository, executionAuthority{}, executionPayloadSource{payload: integrationexecution.Payload{
		CanonicalManifest: manifest, ProviderPayload: []byte("runtime payload")}}, broker, fixedGenerator("a4f00000-0000-4000-8000-00000000000f"),
		&fixedClock{at: now}, time.Minute, []integrationexecution.Definition{{Capability: domain.CapabilityEmailSend, Timeout: time.Second, Connector: connector}})
	if err != nil {
		t.Fatal(err)
	}
	if worked, err := service.ProcessOne(context.Background()); err != nil || !worked {
		t.Fatalf("worked=%t err=%v", worked, err)
	}
	if broker.lease == nil || !broker.lease.closed || len(connector.material) == 0 {
		t.Fatalf("lease=%+v material=%v", broker.lease, connector.material)
	}
	for _, value := range connector.material {
		if value != 0 {
			t.Fatalf("credential view retained material: %v", connector.material)
		}
	}
}

func TestProcessOneFailsClosedBeforeConnectorAndTreatsMalformedProviderResultAsUnknown(t *testing.T) {
	now := time.Date(2026, 8, 23, 22, 0, 0, 0, time.UTC)
	manifest := []byte(`{"release_id":"test","version":3}`)
	for _, testCase := range []struct {
		name         string
		authorityErr error
		brokerErr    error
		payload      integrationexecution.Payload
		result       integrationexecution.ConnectorResult
		wantOutcome  domain.AttemptOutcome
		wantCalls    int
	}{
		{name: "authority", authorityErr: errors.New("packages changed"), payload: integrationexecution.Payload{CanonicalManifest: manifest, ProviderPayload: []byte("payload")},
			result: integrationexecution.ConnectorResult{Outcome: domain.AttemptSucceeded}, wantOutcome: domain.AttemptFailed},
		{name: "manifest", payload: integrationexecution.Payload{CanonicalManifest: []byte("changed"), ProviderPayload: []byte("payload")},
			result: integrationexecution.ConnectorResult{Outcome: domain.AttemptSucceeded}, wantOutcome: domain.AttemptFailed},
		{name: "credential", brokerErr: errors.New("broker denied lease"), payload: integrationexecution.Payload{CanonicalManifest: manifest, ProviderPayload: []byte("payload")},
			result: integrationexecution.ConnectorResult{Outcome: domain.AttemptSucceeded}, wantOutcome: domain.AttemptFailed},
		{name: "malformed_result", payload: integrationexecution.Payload{CanonicalManifest: manifest, ProviderPayload: []byte("payload")},
			result:      integrationexecution.ConnectorResult{Outcome: domain.AttemptNotApplied, ErrorCode: "provider_confirmed_absent", RetryAt: pointerTime(now.Add(time.Minute))},
			wantOutcome: domain.AttemptUnknown, wantCalls: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			claim := executionClaim(manifest, domain.AttemptExecute)
			repository := &executionRepository{claim: claim, found: true}
			connector := mockconnector.New(map[ids.IntegrationExecutionID][]mockconnector.Step{claim.ExecutionID: {{Mode: domain.AttemptExecute, Result: testCase.result}}})
			service, err := integrationexecution.New(repository, executionAuthority{err: testCase.authorityErr}, executionPayloadSource{payload: testCase.payload},
				&executionCredentialBroker{err: testCase.brokerErr}, fixedGenerator("a3f00000-0000-4000-8000-00000000000f"), &fixedClock{at: now}, time.Minute,
				[]integrationexecution.Definition{{Capability: domain.CapabilityEmailSend, Timeout: time.Second, Connector: connector}})
			if err != nil {
				t.Fatal(err)
			}
			worked, processErr := service.ProcessOne(context.Background())
			if !worked || processErr == nil || repository.completion == nil || repository.completion.Outcome != testCase.wantOutcome || len(connector.Calls()) != testCase.wantCalls {
				t.Fatalf("worked=%t completion=%+v calls=%v err=%v", worked, repository.completion, connector.Calls(), processErr)
			}
		})
	}
}

func executionClaim(manifest []byte, mode domain.AttemptMode) integrationexecution.Claim {
	return integrationexecution.Claim{AccountID: "a1100000-0000-4000-8000-000000000001", ExecutionID: "a1200000-0000-4000-8000-000000000002",
		Mode: mode, Capability: domain.CapabilityEmailSend, ReleaseID: "a1300000-0000-4000-8000-000000000003", ReleaseVersion: 3,
		ApprovalID: "a1400000-0000-4000-8000-000000000004", ConnectionID: "a1500000-0000-4000-8000-000000000005",
		ConnectionRevisionID: "a1600000-0000-4000-8000-000000000006", ConnectionRevision: 1,
		CredentialID: "a1700000-0000-4000-8000-000000000007", CredentialGeneration: 1,
		CredentialProvider: "mock_smtp", CredentialReferenceSHA256: sha256.Sum256([]byte("env://local/mock-smtp")),
		ManifestSHA256: sha256.Sum256(manifest), IdempotencyKey: "a1200000-0000-4000-8000-000000000002"}
}

func pointerTime(value time.Time) *time.Time { return &value }

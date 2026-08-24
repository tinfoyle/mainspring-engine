package integrationhealth_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationcredentials"
	"github.com/tinfoyle/spyglass-engine/internal/application/integrationhealth"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type repository struct {
	claim      integrationhealth.Claim
	completion integrationhealth.Completion
}

func (value *repository) Claim(_ context.Context, probeID ids.IntegrationHealthObservationID, _ time.Time, lease time.Time) (integrationhealth.Claim, bool, error) {
	value.claim.ProbeID, value.claim.LeaseExpiresAt = probeID, lease
	return value.claim, true, nil
}
func (value *repository) Complete(_ context.Context, completion integrationhealth.Completion) error {
	value.completion = completion
	return nil
}

type authority struct{ err error }

func (value authority) AuthorizeAccount(context.Context, ids.AccountID) error { return value.err }

type broker struct {
	request integrationcredentials.Request
	err     error
}

func (value *broker) Acquire(_ context.Context, request integrationcredentials.Request) (integrationcredentials.Lease, error) {
	value.request = request
	if value.err != nil {
		return nil, value.err
	}
	return &lease{material: []byte("credential")}, nil
}

type lease struct{ material []byte }

func (value *lease) Material() []byte { return value.material }
func (value *lease) Close() error {
	for index := range value.material {
		value.material[index] = 0
	}
	return nil
}

type probe struct {
	call       integrationhealth.ProbeCall
	credential string
}

func (value *probe) Probe(_ context.Context, call integrationhealth.ProbeCall) integrationhealth.ProbeResult {
	value.call = call
	value.credential = string(call.Credential)
	return integrationhealth.ProbeResult{State: domain.HealthHealthy}
}

type generator string

func (value generator) New() string { return string(value) }

type clock struct{ at time.Time }

func (value clock) Now() time.Time { return value.at }

func TestServiceProbesExactAttestedBindingAndRecordsHealth(t *testing.T) {
	now := time.Date(2026, 8, 23, 17, 0, 0, 0, time.UTC)
	repository := &repository{claim: claim(now)}
	broker := &broker{}
	probe := &probe{}
	service, err := integrationhealth.New(repository, authority{}, broker, generator("a1600000-0000-4000-8000-000000000006"), clock{at: now}, time.Minute,
		[]integrationhealth.Definition{{Kind: domain.ConnectorEmail, Timeout: time.Second, Probe: probe}})
	if err != nil {
		t.Fatal(err)
	}
	worked, err := service.ProcessOne(context.Background())
	if err != nil || !worked || repository.completion.State != domain.HealthHealthy ||
		broker.request.Purpose != integrationcredentials.PurposeHealth || broker.request.OperationID != string(repository.claim.ProbeID) ||
		broker.request.ReferenceSHA256 != repository.claim.CredentialReferenceSHA256 || probe.credential != "credential" {
		t.Fatalf("worked=%t err=%v completion=%+v request=%+v call=%+v", worked, err, repository.completion, broker.request, probe.call)
	}
}

func TestServiceUsesDriveReadForGoogleHealth(t *testing.T) {
	now := time.Date(2026, 8, 23, 17, 0, 0, 0, time.UTC)
	driveClaim := claim(now)
	driveClaim.ConnectorKind = domain.ConnectorGoogleDrive
	driveClaim.Capabilities = []domain.Capability{domain.CapabilityDriveRead}
	driveClaim.Scope = domain.ConnectionScope{DriveFolderIDs: []string{"folder-a"}}
	driveClaim.CredentialProvider = "google_oauth"
	repository := &repository{claim: driveClaim}
	broker := &broker{}
	service, err := integrationhealth.New(repository, authority{}, broker, generator("a1600000-0000-4000-8000-000000000006"), clock{at: now}, time.Minute,
		[]integrationhealth.Definition{{Kind: domain.ConnectorGoogleDrive, Timeout: time.Second, Probe: &probe{}}})
	if err != nil {
		t.Fatal(err)
	}
	if worked, err := service.ProcessOne(context.Background()); err != nil || !worked || broker.request.Capability != domain.CapabilityDriveRead {
		t.Fatalf("worked=%t err=%v request=%+v", worked, err, broker.request)
	}
}

func TestServiceRoutesIMAPHealthWithEmailReadAuthority(t *testing.T) {
	now := time.Date(2026, 8, 23, 17, 0, 0, 0, time.UTC)
	imapClaim := claim(now)
	imapClaim.Capabilities = []domain.Capability{domain.CapabilityEmailRead}
	imapClaim.Scope = domain.ConnectionScope{EmailAddress: "reader@example.com"}
	imapClaim.CredentialProvider = "imap"
	repository := &repository{claim: imapClaim}
	broker := &broker{}
	imapProbe, smtpProbe := &probe{}, &probe{}
	service, err := integrationhealth.New(repository, authority{}, broker, generator("a1600000-0000-4000-8000-000000000006"), clock{at: now}, time.Minute,
		[]integrationhealth.Definition{
			{Kind: domain.ConnectorEmail, CredentialProvider: "smtp", Capability: domain.CapabilityEmailSend, Timeout: time.Second, Probe: smtpProbe},
			{Kind: domain.ConnectorEmail, CredentialProvider: "imap", Capability: domain.CapabilityEmailRead, Timeout: time.Second, Probe: imapProbe},
		})
	if err != nil {
		t.Fatal(err)
	}
	if worked, err := service.ProcessOne(context.Background()); err != nil || !worked || broker.request.Capability != domain.CapabilityEmailRead ||
		imapProbe.call.Claim.CredentialProvider != "imap" || smtpProbe.call.Claim.ConnectionID != "" {
		t.Fatalf("worked=%t err=%v request=%+v imap=%+v smtp=%+v", worked, err, broker.request, imapProbe.call, smtpProbe.call)
	}
}

func TestServiceRecordsCredentialFailureWithoutProviderCall(t *testing.T) {
	now := time.Date(2026, 8, 23, 17, 0, 0, 0, time.UTC)
	repository := &repository{claim: claim(now)}
	broker := &broker{err: errors.New("secret missing")}
	probe := &probe{}
	service, err := integrationhealth.New(repository, authority{}, broker, generator("a1700000-0000-4000-8000-000000000007"), clock{at: now}, time.Minute,
		[]integrationhealth.Definition{{Kind: domain.ConnectorEmail, Timeout: time.Second, Probe: probe}})
	if err != nil {
		t.Fatal(err)
	}
	worked, err := service.ProcessOne(context.Background())
	if !worked || err == nil || repository.completion.State != domain.HealthUnavailable || repository.completion.ErrorCode != "credential_unavailable" || probe.call.Claim.ConnectionID != "" {
		t.Fatalf("worked=%t err=%v completion=%+v call=%+v", worked, err, repository.completion, probe.call)
	}
}

func claim(now time.Time) integrationhealth.Claim {
	return integrationhealth.Claim{AccountID: "a1100000-0000-4000-8000-000000000001", ConnectionID: "a1200000-0000-4000-8000-000000000002",
		ConnectionRevisionID: "a1300000-0000-4000-8000-000000000003", ConnectionRevision: 1, ConnectorKind: domain.ConnectorEmail,
		Capabilities: []domain.Capability{domain.CapabilityEmailSend}, Scope: domain.ConnectionScope{EmailAddress: "launch@example.com", AudienceReference: "audience:v1"},
		CredentialID: "a1400000-0000-4000-8000-000000000004", CredentialGeneration: 1, CredentialProvider: "smtp",
		CredentialReferenceSHA256: sha256.Sum256([]byte("secret://smtp/v1")), LeaseExpiresAt: now.Add(time.Minute)}
}

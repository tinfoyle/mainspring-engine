package mountedcredentials

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationexecution"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
)

const (
	testAccount    = "c1100000-0000-4000-8000-000000000001"
	testExecution  = "c1200000-0000-4000-8000-000000000002"
	testAttempt    = "c1300000-0000-4000-8000-000000000003"
	testConnection = "c1400000-0000-4000-8000-000000000004"
	testCredential = "c1500000-0000-4000-8000-000000000005"
)

func TestBrokerLeasesOnlyTheExactAttestedCredentialAndReloadsIndex(t *testing.T) {
	root := t.TempDir()
	writeSecret(t, root, "smtp-v1.json", []byte(`{"api_key":"first"}`))
	reference := "secret://stage/integrations/smtp/v1"
	writeIndex(t, root, reference, "smtp-v1.json")
	broker, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	request := credentialRequest(reference)
	lease, err := broker.Acquire(context.Background(), request)
	if err != nil || string(lease.Material()) != `{"api_key":"first"}` {
		t.Fatalf("material=%q err=%v", lease.Material(), err)
	}
	view := lease.Material()
	if err := lease.Close(); err != nil || len(lease.Material()) != 0 {
		t.Fatalf("closed material=%q err=%v", lease.Material(), err)
	}
	for _, value := range view {
		if value != 0 {
			t.Fatalf("lease retained credential bytes: %v", view)
		}
	}

	writeSecret(t, root, "smtp-v2.json", []byte(`{"api_key":"second"}`))
	rotatedReference := "secret://stage/integrations/smtp/v2"
	writeIndex(t, root, rotatedReference, "smtp-v2.json")
	if _, err := broker.Acquire(context.Background(), request); err == nil {
		t.Fatal("stale attestation received a rotated credential")
	}
	rotated := credentialRequest(rotatedReference)
	rotatedLease, err := broker.Acquire(context.Background(), rotated)
	if err != nil || string(rotatedLease.Material()) != `{"api_key":"second"}` {
		t.Fatalf("rotated material=%q err=%v", rotatedLease.Material(), err)
	}
	_ = rotatedLease.Close()
}

func TestBrokerRejectsChangedBindingAndUnsafeMaterial(t *testing.T) {
	root := t.TempDir()
	writeSecret(t, root, "smtp.json", []byte("secret"))
	reference := "secret://production/integrations/smtp/v1"
	writeIndex(t, root, reference, "smtp.json")
	broker, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*integrationexecution.CredentialRequest){
		"account": func(value *integrationexecution.CredentialRequest) {
			value.AccountID = "c2100000-0000-4000-8000-000000000001"
		},
		"provider": func(value *integrationexecution.CredentialRequest) { value.CredentialProvider = "other" },
		"reference": func(value *integrationexecution.CredentialRequest) {
			value.ReferenceSHA256 = sha256.Sum256([]byte("different"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			request := credentialRequest(reference)
			mutate(&request)
			if _, err := broker.Acquire(context.Background(), request); err == nil {
				t.Fatal("changed binding received credential material")
			}
		})
	}
	if err := os.Chmod(filepath.Join(root, "smtp.json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := broker.Acquire(context.Background(), credentialRequest(reference)); err == nil {
		t.Fatal("world-readable credential material was leased")
	}
}

func credentialRequest(reference string) integrationexecution.CredentialRequest {
	return integrationexecution.CredentialRequest{AccountID: testAccount, ExecutionID: testExecution, AttemptID: testAttempt,
		Mode: domain.AttemptExecute, Capability: domain.CapabilityEmailSend, ConnectionID: testConnection, CredentialID: testCredential,
		CredentialGeneration: 1, CredentialProvider: "smtp", ReferenceSHA256: sha256.Sum256([]byte(reference)), ExpiresAt: time.Now().Add(time.Minute)}
}

func writeSecret(t *testing.T, root, name string, value []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), value, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeIndex(t *testing.T, root, reference, filename string) {
	t.Helper()
	value := fmt.Sprintf(`{"version":1,"credentials":[{"account_id":"%s","credential_id":"%s","generation":1,"provider":"smtp","reference":"%s","file":"%s"}]}`,
		testAccount, testCredential, reference, filename)
	if err := os.WriteFile(filepath.Join(root, indexFilename), []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

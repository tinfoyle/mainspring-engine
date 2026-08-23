package mockconnector

import (
	"context"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationexecution"
)

func TestBrokerIssuesOneCopiedLeaseAndRecordsNoMaterial(t *testing.T) {
	credential := Credential{ID: "e1100000-0000-4000-8000-000000000001", Generation: 2, Material: []byte("local-secret")}
	broker, err := NewBroker([]Credential{credential})
	if err != nil {
		t.Fatal(err)
	}
	credential.Material[0] = 'X'
	request := integrationexecution.CredentialRequest{ExecutionID: "e1200000-0000-4000-8000-000000000002",
		AttemptID: "e1300000-0000-4000-8000-000000000003", CredentialID: credential.ID, CredentialGeneration: 2, ExpiresAt: time.Now().Add(time.Minute)}
	lease, err := broker.Acquire(context.Background(), request)
	if err != nil || string(lease.Material()) != "local-secret" {
		t.Fatalf("material=%q err=%v", lease.Material(), err)
	}
	if err := lease.Close(); err != nil || len(lease.Material()) != 0 {
		t.Fatalf("closed material=%q err=%v", lease.Material(), err)
	}
	acquisitions := broker.Acquisitions()
	if len(acquisitions) != 1 || acquisitions[0].CredentialID != credential.ID || acquisitions[0].Generation != 2 {
		t.Fatalf("acquisitions=%+v", acquisitions)
	}
	if _, err := broker.Acquire(context.Background(), integrationexecution.CredentialRequest{ExecutionID: request.ExecutionID,
		AttemptID: request.AttemptID, CredentialID: credential.ID, CredentialGeneration: 3, ExpiresAt: request.ExpiresAt}); err == nil {
		t.Fatal("unconfigured credential generation was leased")
	}
}

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

func TestTokenIssuerAndBrokerEnforcePersonaGrant(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	issuer, err := NewTokenIssuer([]byte("test-secret-that-is-at-least-thirty-two-bytes"), 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	issuer.now = func() time.Time { return now }
	tenantID := domain.NewTenantID()
	invocation := domain.InvocationContext{
		TenantID: tenantID, BoardroomID: domain.NewBoardroomID(), RunID: domain.NewRunID(),
		PersonaID: domain.NewPersonaID(), InvocationID: domain.NewInvocationID(),
		Grants: []domain.ToolGrant{{Capability: domain.CapabilitySchedulePropose}}, ExpiresAt: now.Add(5 * time.Minute),
	}
	token, err := issuer.Mint(invocation)
	if err != nil {
		t.Fatal(err)
	}

	auditor := &recordingAuditor{}
	broker := NewBroker(issuer, auditor)
	if err := broker.Register(domain.CapabilitySchedulePropose, func(_ context.Context, call AuthorizedCall) (json.RawMessage, error) {
		return json.RawMessage(`{"accepted":true}`), nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := broker.Invoke(context.Background(), Call{Token: token, TenantID: tenantID, Capability: domain.CapabilitySchedulePropose}); err != nil {
		t.Fatalf("granted capability denied: %v", err)
	}
	if _, err := broker.Invoke(context.Background(), Call{Token: token, TenantID: tenantID, Capability: domain.CapabilityScheduleModify}); !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("modify capability error = %v, want denial", err)
	}
	if len(auditor.records) != 2 || !auditor.records[0].Allowed || auditor.records[1].Allowed {
		t.Fatalf("unexpected audit records: %#v", auditor.records)
	}
}

func TestTokenIssuerRejectsTamperingAndExpiry(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	issuer, err := NewTokenIssuer([]byte("test-secret-that-is-at-least-thirty-two-bytes"), 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	issuer.now = func() time.Time { return now }
	invocation := domain.InvocationContext{
		TenantID: domain.NewTenantID(), BoardroomID: domain.NewBoardroomID(), RunID: domain.NewRunID(),
		PersonaID: domain.NewPersonaID(), InvocationID: domain.NewInvocationID(), ExpiresAt: now.Add(time.Minute),
	}
	token, err := issuer.Mint(invocation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := issuer.Verify(token + "x"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("tampered token error = %v", err)
	}
	issuer.now = func() time.Time { return now.Add(2 * time.Minute) }
	if _, err := issuer.Verify(token); !errors.Is(err, ErrExpiredToken) {
		t.Fatalf("expired token error = %v", err)
	}
}

type recordingAuditor struct {
	records []AuditRecord
}

func (a *recordingAuditor) RecordToolCall(_ context.Context, record AuditRecord) error {
	a.records = append(a.records, record)
	return nil
}

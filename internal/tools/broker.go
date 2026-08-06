package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

var (
	ErrCapabilityDenied = errors.New("tool capability denied")
	ErrToolUnavailable  = errors.New("tool is not registered")
)

type Call struct {
	Token      string
	TenantID   domain.TenantID
	Capability domain.Capability
	Input      json.RawMessage
}

type AuthorizedCall struct {
	Claims     Claims
	Capability domain.Capability
	Input      json.RawMessage
}

type Handler func(context.Context, AuthorizedCall) (json.RawMessage, error)

type AuditRecord struct {
	TenantID, RunID, PersonaID, InvocationID string
	Capability                               domain.Capability
	Allowed                                  bool
	Error                                    string
	CreatedAt                                time.Time
}

type Auditor interface {
	RecordToolCall(context.Context, AuditRecord) error
}

type Broker struct {
	issuer   *TokenIssuer
	auditor  Auditor
	handlers map[domain.Capability]Handler
}

func NewBroker(issuer *TokenIssuer, auditor Auditor) *Broker {
	return &Broker{issuer: issuer, auditor: auditor, handlers: make(map[domain.Capability]Handler)}
}

func (b *Broker) Register(capability domain.Capability, handler Handler) error {
	if capability == "" || handler == nil {
		return errors.New("tool capability and handler are required")
	}
	if _, exists := b.handlers[capability]; exists {
		return fmt.Errorf("tool capability %q is already registered", capability)
	}
	b.handlers[capability] = handler
	return nil
}

func (b *Broker) Invoke(ctx context.Context, call Call) (json.RawMessage, error) {
	claims, err := b.issuer.Verify(call.Token)
	if err != nil {
		return nil, err
	}
	allowed := claims.TenantID == call.TenantID.String() && hasGrant(claims.Grants, call.Capability)
	if !allowed {
		b.audit(ctx, claims, call.Capability, false, ErrCapabilityDenied)
		return nil, ErrCapabilityDenied
	}
	handler, exists := b.handlers[call.Capability]
	if !exists {
		b.audit(ctx, claims, call.Capability, false, ErrToolUnavailable)
		return nil, ErrToolUnavailable
	}
	if err := b.audit(ctx, claims, call.Capability, true, nil); err != nil {
		return nil, fmt.Errorf("record tool authorization: %w", err)
	}
	result, err := handler(ctx, AuthorizedCall{Claims: claims, Capability: call.Capability, Input: call.Input})
	if err != nil {
		_ = b.audit(ctx, claims, call.Capability, true, err)
	}
	return result, err
}

func (b *Broker) audit(ctx context.Context, claims Claims, capability domain.Capability, allowed bool, callErr error) error {
	if b.auditor == nil {
		return nil
	}
	record := AuditRecord{
		TenantID: claims.TenantID, RunID: claims.RunID, PersonaID: claims.PersonaID, InvocationID: claims.InvocationID,
		Capability: capability, Allowed: allowed, CreatedAt: time.Now().UTC(),
	}
	if callErr != nil {
		record.Error = callErr.Error()
	}
	return b.auditor.RecordToolCall(ctx, record)
}

func hasGrant(grants []string, capability domain.Capability) bool {
	for _, grant := range grants {
		if grant == string(capability) {
			return true
		}
	}
	return false
}

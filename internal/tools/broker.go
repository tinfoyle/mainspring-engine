package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
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

type Definition struct {
	Name        string
	Capability  domain.Capability
	Description string
	InputSchema json.RawMessage
}

type AuditRecord struct {
	TenantID, RunID, PersonaID, InvocationID string
	ActorType, ActorID                       string
	Capability                               domain.Capability
	Allowed                                  bool
	Error                                    string
	CreatedAt                                time.Time
}

type Auditor interface {
	RecordToolCall(context.Context, AuditRecord) error
}

type Broker struct {
	issuer      *TokenIssuer
	auditor     Auditor
	handlers    map[domain.Capability]Handler
	named       map[string]Handler
	definitions map[string]Definition
}

func NewBroker(issuer *TokenIssuer, auditor Auditor) *Broker {
	return &Broker{issuer: issuer, auditor: auditor, handlers: make(map[domain.Capability]Handler), named: make(map[string]Handler), definitions: make(map[string]Definition)}
}

func (b *Broker) Register(capability domain.Capability, handler Handler) error {
	return b.RegisterDefinition(Definition{Name: string(capability), Capability: capability}, handler)
}

func (b *Broker) RegisterDefinition(definition Definition, handler Handler) error {
	capability := definition.Capability
	if capability == "" || handler == nil {
		return errors.New("tool capability and handler are required")
	}
	if definition.Name == "" {
		return errors.New("tool name is required")
	}
	if _, exists := b.definitions[definition.Name]; exists {
		return fmt.Errorf("tool name %q is already registered", definition.Name)
	}
	if _, exists := b.handlers[capability]; !exists {
		b.handlers[capability] = handler
	}
	b.named[definition.Name] = handler
	b.definitions[definition.Name] = definition
	return nil
}

func (b *Broker) Definitions(grants []domain.ToolGrant) []Definition {
	allowed := make(map[domain.Capability]bool, len(grants))
	for _, grant := range grants {
		allowed[grant.Capability] = true
	}
	definitions := make([]Definition, 0, len(b.definitions))
	for _, definition := range b.definitions {
		if allowed[definition.Capability] {
			definitions = append(definitions, definition)
		}
	}
	slices.SortFunc(definitions, func(left, right Definition) int { return strings.Compare(left.Name, right.Name) })
	return definitions
}

func (b *Broker) InvokeNamed(ctx context.Context, token string, tenantID domain.TenantID, name string, input json.RawMessage) (json.RawMessage, error) {
	definition, exists := b.definitions[name]
	if !exists {
		return nil, ErrToolUnavailable
	}
	handler, exists := b.named[name]
	if !exists {
		return nil, ErrToolUnavailable
	}
	return b.invoke(ctx, Call{Token: token, TenantID: tenantID, Capability: definition.Capability, Input: input}, handler)
}

func (b *Broker) Invoke(ctx context.Context, call Call) (json.RawMessage, error) {
	return b.invoke(ctx, call, b.handlers[call.Capability])
}

func (b *Broker) invoke(ctx context.Context, call Call, handler Handler) (json.RawMessage, error) {
	claims, err := b.issuer.Verify(call.Token)
	if err != nil {
		return nil, err
	}
	allowed := claims.TenantID == call.TenantID.String() && hasGrant(claims.Grants, call.Capability)
	if conditions := claims.Conditions[string(call.Capability)]; conditions["disabled"] == "true" {
		allowed = false
	}
	if !allowed {
		b.audit(ctx, claims, call.Capability, false, ErrCapabilityDenied)
		return nil, ErrCapabilityDenied
	}
	if handler == nil {
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
		ActorType: claims.ActorType, ActorID: claims.ActorID,
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

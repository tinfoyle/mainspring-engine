package integrations

import (
	"net/mail"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type ConnectorKind string

const (
	ConnectorEmail       ConnectorKind = "email"
	ConnectorGoogleDrive ConnectorKind = "google_drive"
	ConnectorWebResearch ConnectorKind = "web_research"
	ConnectorWebPublish  ConnectorKind = "web_publish"
)

type Capability string

const (
	CapabilityEmailRead   Capability = "email.read"
	CapabilityEmailSend   Capability = "email.send"
	CapabilityDriveRead   Capability = "google_drive.read"
	CapabilityWebResearch Capability = "web.research"
	CapabilityWebPublish  Capability = "web.publish"
)

type ConnectionState string

const (
	ConnectionPending  ConnectionState = "pending"
	ConnectionActive   ConnectionState = "active"
	ConnectionDisabled ConnectionState = "disabled"
	ConnectionRevoked  ConnectionState = "revoked"
)

// ConnectionScope contains only non-secret, customer-visible authorization.
// Provider-specific settings and credentials live behind the credential broker.
type ConnectionScope struct {
	EmailAddress      string `json:"email_address,omitempty"`
	AudienceReference string `json:"audience_reference,omitempty"`
	HTTPSOrigin       string `json:"https_origin,omitempty"`
	PathPrefix        string `json:"path_prefix,omitempty"`
}

func (scope ConnectionScope) normalize(kind ConnectorKind, capabilities []Capability) (ConnectionScope, error) {
	scope.EmailAddress = strings.TrimSpace(scope.EmailAddress)
	scope.AudienceReference = strings.TrimSpace(scope.AudienceReference)
	scope.HTTPSOrigin = strings.TrimSpace(scope.HTTPSOrigin)
	scope.PathPrefix = strings.TrimSpace(scope.PathPrefix)
	switch kind {
	case ConnectorEmail:
		if scope.HTTPSOrigin != "" || scope.PathPrefix != "" || !validText(scope.EmailAddress, 320, true) || !validText(scope.AudienceReference, MaximumReferenceBytes, slices.Contains(capabilities, CapabilityEmailSend)) {
			return ConnectionScope{}, ErrInvalid
		}
		address, err := mail.ParseAddress(scope.EmailAddress)
		if err != nil || !strings.EqualFold(address.Address, scope.EmailAddress) {
			return ConnectionScope{}, ErrInvalid
		}
		separator := strings.LastIndexByte(address.Address, '@')
		if separator < 1 || separator == len(address.Address)-1 {
			return ConnectionScope{}, ErrInvalid
		}
		scope.EmailAddress = address.Address[:separator+1] + strings.ToLower(address.Address[separator+1:])
	case ConnectorWebPublish:
		if scope.EmailAddress != "" || scope.AudienceReference != "" || !validText(scope.HTTPSOrigin, MaximumReferenceBytes, true) || !validText(scope.PathPrefix, MaximumReferenceBytes, true) {
			return ConnectionScope{}, ErrInvalid
		}
		parsed, err := url.Parse(scope.HTTPSOrigin)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
			return ConnectionScope{}, ErrInvalid
		}
		parsed.Path, parsed.RawPath = "", ""
		scope.HTTPSOrigin = strings.ToLower(parsed.String())
		clean := path.Clean(scope.PathPrefix)
		if !strings.HasPrefix(scope.PathPrefix, "/") || clean != scope.PathPrefix || strings.Contains(scope.PathPrefix, "//") {
			return ConnectionScope{}, ErrInvalid
		}
		scope.PathPrefix = clean
	default:
		// Drive and research receive their reviewed scope shapes in later slices.
		return ConnectionScope{}, ErrCapability
	}
	return scope, nil
}

func normalizeCapabilities(kind ConnectorKind, values []Capability) ([]Capability, error) {
	values = append([]Capability(nil), values...)
	if len(values) == 0 || len(values) > 4 {
		return nil, ErrCapability
	}
	for index, value := range values {
		if slices.Contains(values[:index], value) || !capabilityMatches(kind, value) {
			return nil, ErrCapability
		}
	}
	slices.Sort(values)
	return values, nil
}

func capabilityMatches(kind ConnectorKind, capability Capability) bool {
	switch kind {
	case ConnectorEmail:
		return capability == CapabilityEmailRead || capability == CapabilityEmailSend
	case ConnectorGoogleDrive:
		return capability == CapabilityDriveRead
	case ConnectorWebResearch:
		return capability == CapabilityWebResearch
	case ConnectorWebPublish:
		return capability == CapabilityWebPublish
	default:
		return false
	}
}

type ConnectionRevision struct {
	ID           ids.IntegrationConnectionRevisionID `json:"id"`
	AccountID    ids.AccountID                       `json:"account_id"`
	ConnectionID ids.IntegrationConnectionID         `json:"connection_id"`
	Revision     uint64                              `json:"revision"`
	Capabilities []Capability                        `json:"capabilities"`
	Scope        ConnectionScope                     `json:"scope"`
	CreatedBy    Actor                               `json:"created_by"`
	CreatedAt    time.Time                           `json:"created_at"`
}

func RestoreConnectionRevision(value ConnectionRevision, kind ConnectorKind) (ConnectionRevision, error) {
	if ids.Validate(string(value.ID)) != nil || ids.Validate(string(value.AccountID)) != nil || ids.Validate(string(value.ConnectionID)) != nil || value.Revision == 0 || !value.CreatedBy.valid() || value.CreatedAt.IsZero() {
		return ConnectionRevision{}, ErrInvalid
	}
	capabilities, err := normalizeCapabilities(kind, value.Capabilities)
	if err != nil {
		return ConnectionRevision{}, err
	}
	scope, err := value.Scope.normalize(kind, capabilities)
	if err != nil {
		return ConnectionRevision{}, err
	}
	value.Capabilities, value.Scope, value.CreatedAt = capabilities, scope, value.CreatedAt.UTC()
	return value, nil
}

type Connection struct {
	ID                   ids.IntegrationConnectionID         `json:"id"`
	AccountID            ids.AccountID                       `json:"account_id"`
	Name                 string                              `json:"name"`
	Kind                 ConnectorKind                       `json:"kind"`
	State                ConnectionState                     `json:"state"`
	CurrentRevisionID    ids.IntegrationConnectionRevisionID `json:"current_revision_id"`
	CurrentRevision      uint64                              `json:"current_revision"`
	CredentialID         ids.IntegrationCredentialID         `json:"credential_id,omitempty"`
	CredentialGeneration uint64                              `json:"credential_generation,omitempty"`
	Version              uint64                              `json:"version"`
	CreatedBy            Actor                               `json:"created_by"`
	RevokedBy            *Actor                              `json:"revoked_by,omitempty"`
	CreatedAt            time.Time                           `json:"created_at"`
	UpdatedAt            time.Time                           `json:"updated_at"`
	RevokedAt            *time.Time                          `json:"revoked_at,omitempty"`
}

type ConnectionInput struct {
	ID           ids.IntegrationConnectionID
	RevisionID   ids.IntegrationConnectionRevisionID
	AccountID    ids.AccountID
	Name         string
	Kind         ConnectorKind
	Capabilities []Capability
	Scope        ConnectionScope
	CreatedBy    Actor
	CreatedAt    time.Time
}

type ConnectionRevisionInput struct {
	ID           ids.IntegrationConnectionRevisionID
	Name         string
	Capabilities []Capability
	Scope        ConnectionScope
}

func NewConnection(input ConnectionInput, role accounts.MembershipRole) (Connection, ConnectionRevision, error) {
	if !canManage(role) || !input.CreatedBy.valid() {
		return Connection{}, ConnectionRevision{}, ErrRole
	}
	connection := Connection{ID: input.ID, AccountID: input.AccountID, Name: strings.TrimSpace(input.Name), Kind: input.Kind, State: ConnectionPending,
		CurrentRevisionID: input.RevisionID, CurrentRevision: 1, Version: 1, CreatedBy: input.CreatedBy, CreatedAt: input.CreatedAt.UTC(), UpdatedAt: input.CreatedAt.UTC()}
	revision := ConnectionRevision{ID: input.RevisionID, AccountID: input.AccountID, ConnectionID: input.ID, Revision: 1, Capabilities: input.Capabilities, Scope: input.Scope, CreatedBy: input.CreatedBy, CreatedAt: input.CreatedAt.UTC()}
	connection, err := RestoreConnection(connection)
	if err != nil {
		return Connection{}, ConnectionRevision{}, err
	}
	revision, err = RestoreConnectionRevision(revision, connection.Kind)
	return connection, revision, err
}

func RestoreConnection(value Connection) (Connection, error) {
	value.Name, value.CreatedAt, value.UpdatedAt = strings.TrimSpace(value.Name), value.CreatedAt.UTC(), value.UpdatedAt.UTC()
	if value.RevokedAt != nil {
		at := value.RevokedAt.UTC()
		value.RevokedAt = &at
	}
	if ids.Validate(string(value.ID)) != nil || ids.Validate(string(value.AccountID)) != nil || ids.Validate(string(value.CurrentRevisionID)) != nil || !validText(value.Name, MaximumConnectionNameBytes, true) ||
		!capabilityMatches(value.Kind, firstCapabilityForKind(value.Kind)) || value.CurrentRevision == 0 || value.Version == 0 || !value.CreatedBy.valid() || value.CreatedAt.IsZero() || value.UpdatedAt.Before(value.CreatedAt) {
		return Connection{}, ErrInvalid
	}
	switch value.State {
	case ConnectionPending:
		if value.CredentialID != "" || value.CredentialGeneration != 0 || value.RevokedBy != nil || value.RevokedAt != nil {
			return Connection{}, ErrInvalid
		}
	case ConnectionActive, ConnectionDisabled:
		if ids.Validate(string(value.CredentialID)) != nil || value.CredentialGeneration == 0 || value.RevokedBy != nil || value.RevokedAt != nil {
			return Connection{}, ErrInvalid
		}
	case ConnectionRevoked:
		if value.RevokedBy == nil || !value.RevokedBy.valid() || value.RevokedAt == nil || value.RevokedAt.Before(value.CreatedAt) || !value.UpdatedAt.Equal(*value.RevokedAt) {
			return Connection{}, ErrInvalid
		}
	default:
		return Connection{}, ErrInvalid
	}
	return value, nil
}

func firstCapabilityForKind(kind ConnectorKind) Capability {
	switch kind {
	case ConnectorEmail:
		return CapabilityEmailRead
	case ConnectorGoogleDrive:
		return CapabilityDriveRead
	case ConnectorWebResearch:
		return CapabilityWebResearch
	case ConnectorWebPublish:
		return CapabilityWebPublish
	default:
		return ""
	}
}

func (value Connection) Activate(expectedVersion uint64, credential CredentialBinding, actor Actor, role accounts.MembershipRole, at time.Time) (Connection, error) {
	if expectedVersion != value.Version {
		return Connection{}, ErrConflict
	}
	if !canManage(role) || !actor.valid() {
		return Connection{}, ErrRole
	}
	if (value.State != ConnectionPending && value.State != ConnectionDisabled) || credential.AccountID != value.AccountID || credential.ConnectionID != value.ID || credential.State != CredentialActive || !validTime(at, value.UpdatedAt) {
		return Connection{}, ErrCredential
	}
	value.State, value.CredentialID, value.CredentialGeneration, value.Version, value.UpdatedAt = ConnectionActive, credential.ID, credential.Generation, value.Version+1, at.UTC()
	return RestoreConnection(value)
}

func (value Connection) Revise(expectedVersion uint64, input ConnectionRevisionInput, actor Actor, role accounts.MembershipRole, at time.Time) (Connection, ConnectionRevision, error) {
	if expectedVersion != value.Version {
		return Connection{}, ConnectionRevision{}, ErrConflict
	}
	if !canManage(role) || !actor.valid() {
		return Connection{}, ConnectionRevision{}, ErrRole
	}
	if value.State == ConnectionRevoked || ids.Validate(string(input.ID)) != nil || !validTime(at, value.UpdatedAt) {
		return Connection{}, ConnectionRevision{}, ErrState
	}
	revision := ConnectionRevision{ID: input.ID, AccountID: value.AccountID, ConnectionID: value.ID, Revision: value.CurrentRevision + 1,
		Capabilities: input.Capabilities, Scope: input.Scope, CreatedBy: actor, CreatedAt: at.UTC()}
	revision, err := RestoreConnectionRevision(revision, value.Kind)
	if err != nil {
		return Connection{}, ConnectionRevision{}, err
	}
	value.Name, value.CurrentRevisionID, value.CurrentRevision, value.Version, value.UpdatedAt = strings.TrimSpace(input.Name), revision.ID, revision.Revision, value.Version+1, at.UTC()
	value, err = RestoreConnection(value)
	return value, revision, err
}

func (value Connection) BindCredential(expectedVersion uint64, credential CredentialBinding, actor Actor, role accounts.MembershipRole, at time.Time) (Connection, error) {
	if expectedVersion != value.Version {
		return Connection{}, ErrConflict
	}
	if !canManage(role) || !actor.valid() {
		return Connection{}, ErrRole
	}
	if (value.State != ConnectionActive && value.State != ConnectionDisabled) || credential.AccountID != value.AccountID || credential.ConnectionID != value.ID || credential.State != CredentialActive || credential.Generation != value.CredentialGeneration+1 || !credential.Available(at) || !validTime(at, value.UpdatedAt) {
		return Connection{}, ErrCredential
	}
	value.CredentialID, value.CredentialGeneration, value.Version, value.UpdatedAt = credential.ID, credential.Generation, value.Version+1, at.UTC()
	return RestoreConnection(value)
}

func (value Connection) Disable(expectedVersion uint64, actor Actor, role accounts.MembershipRole, at time.Time) (Connection, error) {
	if expectedVersion != value.Version {
		return Connection{}, ErrConflict
	}
	if !canManage(role) || !actor.valid() {
		return Connection{}, ErrRole
	}
	if value.State != ConnectionActive || !validTime(at, value.UpdatedAt) {
		return Connection{}, ErrState
	}
	value.State, value.Version, value.UpdatedAt = ConnectionDisabled, value.Version+1, at.UTC()
	return RestoreConnection(value)
}

func (value Connection) Revoke(expectedVersion uint64, actor Actor, role accounts.MembershipRole, at time.Time) (Connection, error) {
	if expectedVersion != value.Version {
		return Connection{}, ErrConflict
	}
	if !canManage(role) || !actor.valid() {
		return Connection{}, ErrRole
	}
	if value.State == ConnectionRevoked || !validTime(at, value.UpdatedAt) {
		return Connection{}, ErrState
	}
	at, value.State, value.Version = at.UTC(), ConnectionRevoked, value.Version+1
	value.RevokedBy, value.RevokedAt, value.UpdatedAt = &actor, &at, at
	return RestoreConnection(value)
}

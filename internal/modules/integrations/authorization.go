package integrations

import (
	"crypto/sha256"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	GoogleOAuthProvider  = "google_oauth"
	GoogleDriveReadScope = "https://www.googleapis.com/auth/drive.readonly"
)

type AuthorizationStatus string

const (
	AuthorizationPending    AuthorizationStatus = "pending"
	AuthorizationExchanging AuthorizationStatus = "exchanging"
	AuthorizationCompleted  AuthorizationStatus = "completed"
	AuthorizationFailed     AuthorizationStatus = "failed"
	AuthorizationExpired    AuthorizationStatus = "expired"
)

// AuthorizationSession freezes a one-use provider authorization request. It
// contains digests and public protocol bindings only; state, PKCE verifier,
// authorization code and provider tokens never enter this aggregate.
type AuthorizationSession struct {
	ID                   ids.IntegrationAuthorizationSessionID `json:"id"`
	AccountID            ids.AccountID                         `json:"account_id"`
	ConnectionID         ids.IntegrationConnectionID           `json:"connection_id"`
	ConnectionRevision   ids.IntegrationConnectionRevisionID   `json:"connection_revision_id"`
	Provider             string                                `json:"provider"`
	Scope                string                                `json:"scope"`
	ScopeRevisionSHA256  [sha256.Size]byte                     `json:"scope_revision_sha256"`
	RedirectURI          string                                `json:"redirect_uri"`
	StateSHA256          [sha256.Size]byte                     `json:"state_sha256"`
	PKCEChallengeSHA256  [sha256.Size]byte                     `json:"pkce_challenge_sha256"`
	Status               AuthorizationStatus                   `json:"status"`
	Version              uint64                                `json:"version"`
	CreatedBy            Actor                                 `json:"created_by"`
	CredentialID         ids.IntegrationCredentialID           `json:"credential_id,omitempty"`
	CredentialGeneration uint64                                `json:"credential_generation,omitempty"`
	ErrorCode            string                                `json:"error_code,omitempty"`
	CreatedAt            time.Time                             `json:"created_at"`
	UpdatedAt            time.Time                             `json:"updated_at"`
	ExpiresAt            time.Time                             `json:"expires_at"`
	ClaimedAt            *time.Time                            `json:"claimed_at,omitempty"`
	CompletedAt          *time.Time                            `json:"completed_at,omitempty"`
}

type AuthorizationSessionInput struct {
	ID                  ids.IntegrationAuthorizationSessionID
	AccountID           ids.AccountID
	ConnectionID        ids.IntegrationConnectionID
	ConnectionRevision  ids.IntegrationConnectionRevisionID
	Provider            string
	Scope               string
	ScopeRevisionSHA256 [sha256.Size]byte
	RedirectURI         string
	StateSHA256         [sha256.Size]byte
	PKCEChallengeSHA256 [sha256.Size]byte
	CreatedBy           Actor
	CreatedAt           time.Time
	ExpiresAt           time.Time
}

func NewAuthorizationSession(input AuthorizationSessionInput, role accounts.MembershipRole) (AuthorizationSession, error) {
	if !canManage(role) || !input.CreatedBy.valid() {
		return AuthorizationSession{}, ErrRole
	}
	value := AuthorizationSession{
		ID: input.ID, AccountID: input.AccountID, ConnectionID: input.ConnectionID, ConnectionRevision: input.ConnectionRevision,
		Provider: strings.TrimSpace(input.Provider), Scope: strings.TrimSpace(input.Scope), ScopeRevisionSHA256: input.ScopeRevisionSHA256,
		RedirectURI: strings.TrimSpace(input.RedirectURI), StateSHA256: input.StateSHA256, PKCEChallengeSHA256: input.PKCEChallengeSHA256,
		Status: AuthorizationPending, Version: 1, CreatedBy: input.CreatedBy, CreatedAt: input.CreatedAt.UTC(), UpdatedAt: input.CreatedAt.UTC(),
		ExpiresAt: input.ExpiresAt.UTC(),
	}
	return RestoreAuthorizationSession(value)
}

func RestoreAuthorizationSession(value AuthorizationSession) (AuthorizationSession, error) {
	value.Provider, value.Scope, value.RedirectURI = strings.TrimSpace(value.Provider), strings.TrimSpace(value.Scope), strings.TrimSpace(value.RedirectURI)
	value.ErrorCode = strings.TrimSpace(value.ErrorCode)
	value.CreatedAt, value.UpdatedAt, value.ExpiresAt = value.CreatedAt.UTC(), value.UpdatedAt.UTC(), value.ExpiresAt.UTC()
	value.ClaimedAt = normalizeAuthorizationTime(value.ClaimedAt)
	value.CompletedAt = normalizeAuthorizationTime(value.CompletedAt)
	if ids.Validate(string(value.ID)) != nil || ids.Validate(string(value.AccountID)) != nil || ids.Validate(string(value.ConnectionID)) != nil ||
		ids.Validate(string(value.ConnectionRevision)) != nil || value.Provider != GoogleOAuthProvider || value.Scope != GoogleDriveReadScope ||
		!nonzeroDigest(value.ScopeRevisionSHA256) || !nonzeroDigest(value.StateSHA256) || !nonzeroDigest(value.PKCEChallengeSHA256) ||
		!validAuthorizationRedirect(value.RedirectURI) || !value.CreatedBy.valid() || value.Version == 0 || value.CreatedAt.IsZero() ||
		value.UpdatedAt.Before(value.CreatedAt) || !value.ExpiresAt.After(value.CreatedAt) || value.ExpiresAt.Sub(value.CreatedAt) > 15*time.Minute {
		return AuthorizationSession{}, ErrInvalid
	}
	switch value.Status {
	case AuthorizationPending:
		if value.Version != 1 || !value.UpdatedAt.Equal(value.CreatedAt) || value.ClaimedAt != nil || value.CompletedAt != nil ||
			value.CredentialID != "" || value.CredentialGeneration != 0 || value.ErrorCode != "" {
			return AuthorizationSession{}, ErrInvalid
		}
	case AuthorizationExchanging:
		if value.Version != 2 || value.ClaimedAt == nil || !value.UpdatedAt.Equal(*value.ClaimedAt) || value.CompletedAt != nil ||
			value.CredentialID != "" || value.CredentialGeneration != 0 || value.ErrorCode != "" || !value.ClaimedAt.Before(value.ExpiresAt) {
			return AuthorizationSession{}, ErrInvalid
		}
	case AuthorizationCompleted:
		if value.Version != 3 || value.ClaimedAt == nil || value.CompletedAt == nil || !value.UpdatedAt.Equal(*value.CompletedAt) ||
			value.CompletedAt.Before(*value.ClaimedAt) || ids.Validate(string(value.CredentialID)) != nil || value.CredentialGeneration == 0 || value.ErrorCode != "" {
			return AuthorizationSession{}, ErrInvalid
		}
	case AuthorizationFailed:
		if value.Version != 3 || value.ClaimedAt == nil || value.CompletedAt == nil || !value.UpdatedAt.Equal(*value.CompletedAt) ||
			value.CompletedAt.Before(*value.ClaimedAt) || value.CredentialID != "" || value.CredentialGeneration != 0 || !validCodeValue(value.ErrorCode) {
			return AuthorizationSession{}, ErrInvalid
		}
	case AuthorizationExpired:
		if value.Version != 2 || value.ClaimedAt != nil || value.CompletedAt == nil || !value.UpdatedAt.Equal(*value.CompletedAt) ||
			value.CompletedAt.Before(value.ExpiresAt) || value.CredentialID != "" || value.CredentialGeneration != 0 || value.ErrorCode != "authorization_expired" {
			return AuthorizationSession{}, ErrInvalid
		}
	default:
		return AuthorizationSession{}, ErrInvalid
	}
	return value, nil
}

func (value AuthorizationSession) BeginExchange(stateSHA256 [sha256.Size]byte, expectedVersion uint64, at time.Time) (AuthorizationSession, error) {
	if value.Version != expectedVersion {
		return AuthorizationSession{}, ErrConflict
	}
	at = at.UTC()
	if value.Status != AuthorizationPending || stateSHA256 != value.StateSHA256 || at.Before(value.CreatedAt) {
		return AuthorizationSession{}, ErrState
	}
	if !at.Before(value.ExpiresAt) {
		return value.Expire(expectedVersion, at)
	}
	value.Status, value.Version, value.UpdatedAt, value.ClaimedAt = AuthorizationExchanging, value.Version+1, at, &at
	return RestoreAuthorizationSession(value)
}

func (value AuthorizationSession) Complete(credentialID ids.IntegrationCredentialID, generation, expectedVersion uint64, at time.Time) (AuthorizationSession, error) {
	if value.Version != expectedVersion {
		return AuthorizationSession{}, ErrConflict
	}
	at = at.UTC()
	if value.Status != AuthorizationExchanging || ids.Validate(string(credentialID)) != nil || generation == 0 || value.ClaimedAt == nil || at.Before(*value.ClaimedAt) {
		return AuthorizationSession{}, ErrState
	}
	value.Status, value.Version, value.CredentialID, value.CredentialGeneration = AuthorizationCompleted, value.Version+1, credentialID, generation
	value.UpdatedAt, value.CompletedAt = at, &at
	return RestoreAuthorizationSession(value)
}

func (value AuthorizationSession) Fail(code string, expectedVersion uint64, at time.Time) (AuthorizationSession, error) {
	if value.Version != expectedVersion {
		return AuthorizationSession{}, ErrConflict
	}
	at, code = at.UTC(), strings.TrimSpace(code)
	if value.Status != AuthorizationExchanging || value.ClaimedAt == nil || at.Before(*value.ClaimedAt) || !validCodeValue(code) || code == "authorization_expired" {
		return AuthorizationSession{}, ErrState
	}
	value.Status, value.Version, value.ErrorCode = AuthorizationFailed, value.Version+1, code
	value.UpdatedAt, value.CompletedAt = at, &at
	return RestoreAuthorizationSession(value)
}

func (value AuthorizationSession) Expire(expectedVersion uint64, at time.Time) (AuthorizationSession, error) {
	if value.Version != expectedVersion {
		return AuthorizationSession{}, ErrConflict
	}
	at = at.UTC()
	if value.Status != AuthorizationPending || at.Before(value.ExpiresAt) {
		return AuthorizationSession{}, ErrState
	}
	value.Status, value.Version, value.ErrorCode = AuthorizationExpired, value.Version+1, "authorization_expired"
	value.UpdatedAt, value.CompletedAt = at, &at
	return RestoreAuthorizationSession(value)
}

func normalizeAuthorizationTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	at := value.UTC()
	return &at
}

func validAuthorizationRedirect(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.Host == "" || parsed.Fragment != "" || parsed.RawQuery != "" || parsed.Path == "" || pathHasTraversal(parsed.EscapedPath()) {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	if parsed.Scheme != "http" {
		return false
	}
	host := parsed.Hostname()
	return strings.EqualFold(host, "localhost") || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
}

func pathHasTraversal(value string) bool {
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." || strings.EqualFold(segment, "%2e") || strings.EqualFold(segment, "%2e%2e") {
			return true
		}
	}
	return false
}

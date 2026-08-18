// Package passkeys owns system-wide WebAuthn ceremonies and credential
// lifecycle. Passkeys authenticate a User identity; they never imply Account
// Membership, role, placement, or package access.
package passkeys

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	webauthnlib "github.com/go-webauthn/webauthn/webauthn"

	"github.com/tinfoyle/spyglass-engine/internal/application/abuse"
	"github.com/tinfoyle/spyglass-engine/internal/modules/identity"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	CeremonyTTL      = 3 * time.Minute
	Reauthentication = 10 * time.Minute
	MaximumPasskeys  = 10
)

var (
	ErrInvalidCeremony         = errors.New("passkey ceremony is invalid or expired")
	ErrInvalidCredential       = errors.New("passkey credential is invalid")
	ErrReauthenticationNeeded  = errors.New("recent reauthentication is required")
	ErrCredentialLimit         = errors.New("passkey credential limit reached")
	ErrCredentialNotFound      = errors.New("passkey credential not found")
	ErrCredentialStateConflict = errors.New("passkey credential state changed")
)

type CeremonyKind string

const (
	CeremonyRegistration     CeremonyKind = "registration"
	CeremonyLogin            CeremonyKind = "login"
	CeremonyReauthentication CeremonyKind = "reauthentication"
)

type CredentialEvent string

const (
	EventAuthenticated   CredentialEvent = "passkey_authenticated"
	EventReauthenticated CredentialEvent = "passkey_reauthenticated"
)

type Config struct {
	RelyingPartyID string
	Origins        []string
}

type CredentialRecord struct {
	UserID     ids.UserID
	Name       string
	Credential webauthnlib.Credential
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

type CredentialSummary struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	CreatedAt      time.Time  `json:"created_at"`
	LastUsedAt     *time.Time `json:"last_used_at,omitempty"`
	BackupEligible bool       `json:"backup_eligible"`
	BackedUp       bool       `json:"backed_up"`
}

type User struct {
	Identity    identity.User
	Handle      []byte
	Credentials []CredentialRecord
}

func (u User) WebAuthnID() []byte          { return append([]byte(nil), u.Handle...) }
func (u User) WebAuthnName() string        { return u.Identity.PrimaryEmail }
func (u User) WebAuthnDisplayName() string { return u.Identity.DisplayName }
func (u User) WebAuthnCredentials() []webauthnlib.Credential {
	result := make([]webauthnlib.Credential, 0, len(u.Credentials))
	for _, credential := range u.Credentials {
		result = append(result, credential.Credential)
	}
	return result
}

type Ceremony struct {
	ID        string
	Kind      CeremonyKind
	UserID    ids.UserID
	SessionID ids.SessionID
	Data      webauthnlib.SessionData
	ExpiresAt time.Time
	CreatedAt time.Time
}

type Repository interface {
	EnsureUser(context.Context, ids.UserID, []byte, time.Time) (User, error)
	User(context.Context, ids.UserID) (User, error)
	UserByHandle(context.Context, []byte, []byte) (User, error)
	CreateCeremony(context.Context, Ceremony) error
	ConsumeCeremony(context.Context, string, CeremonyKind, ids.UserID, ids.SessionID, time.Time) (Ceremony, error)
	CreateCredential(context.Context, ids.UserID, string, webauthnlib.Credential, time.Time, int) error
	UpdateCredential(context.Context, ids.UserID, []byte, uint32, webauthnlib.Credential, CredentialEvent, time.Time) (bool, error)
	RecordCloneWarning(context.Context, ids.UserID, []byte, time.Time) error
	ListCredentials(context.Context, ids.UserID) ([]CredentialRecord, error)
	DeleteCredential(context.Context, ids.UserID, []byte, time.Time) (bool, error)
}

type NetworkGuard interface {
	Allow(context.Context, abuse.Scope, [32]byte, time.Time, abuse.Policy) (bool, error)
}

type Service struct {
	repository Repository
	webauthn   *webauthnlib.WebAuthn
	sessions   *sessions.Service
	network    NetworkGuard
	ids        ids.Generator
	clock      sessions.Clock
}

func NewService(repository Repository, sessionService *sessions.Service, network NetworkGuard, generator ids.Generator, clock sessions.Clock, config Config) (*Service, error) {
	if repository == nil || sessionService == nil || network == nil || generator == nil || clock == nil || strings.TrimSpace(config.RelyingPartyID) == "" || len(config.Origins) == 0 {
		return nil, errors.New("passkey dependencies and relying-party configuration are required")
	}
	for _, origin := range config.Origins {
		if strings.TrimSpace(origin) == "" {
			return nil, errors.New("passkey origins must not be empty")
		}
	}
	webAuthn, err := webauthnlib.New(&webauthnlib.Config{
		RPID:                  strings.TrimSpace(config.RelyingPartyID),
		RPDisplayName:         "Infinite Ocean: Spyglass",
		RPOrigins:             append([]string(nil), config.Origins...),
		RPAllowCrossOrigin:    false,
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementRequired,
			UserVerification: protocol.VerificationRequired,
		},
		Timeouts: webauthnlib.TimeoutsConfig{
			Login:        webauthnlib.TimeoutConfig{Enforce: true, Timeout: CeremonyTTL, TimeoutUVD: CeremonyTTL},
			Registration: webauthnlib.TimeoutConfig{Enforce: true, Timeout: CeremonyTTL, TimeoutUVD: CeremonyTTL},
		},
	})
	if err != nil {
		return nil, err
	}
	return &Service{repository: repository, webauthn: webAuthn, sessions: sessionService, network: network, ids: generator, clock: clock}, nil
}

type BeginResult struct {
	CeremonyID string          `json:"ceremony_id"`
	PublicKey  json.RawMessage `json:"public_key"`
	ExpiresAt  time.Time       `json:"expires_at"`
}

func (s *Service) BeginRegistration(ctx context.Context, session sessions.Session) (BeginResult, error) {
	if !s.sessions.RecentlyReauthenticated(session, Reauthentication) {
		return BeginResult{}, ErrReauthenticationNeeded
	}
	now := s.clock.Now().UTC()
	handle := make([]byte, 32)
	if _, err := rand.Read(handle); err != nil {
		return BeginResult{}, err
	}
	user, err := s.repository.EnsureUser(ctx, session.UserID, handle, now)
	if err != nil {
		return BeginResult{}, err
	}
	if len(user.Credentials) >= MaximumPasskeys {
		return BeginResult{}, ErrCredentialLimit
	}
	creation, data, err := s.webauthn.BeginRegistration(user,
		webauthnlib.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthnlib.WithAuthenticatorSelection(protocol.AuthenticatorSelection{ResidentKey: protocol.ResidentKeyRequirementRequired, UserVerification: protocol.VerificationRequired}),
		webauthnlib.WithConveyancePreference(protocol.PreferNoAttestation),
	)
	if err != nil {
		return BeginResult{}, err
	}
	return s.storeCeremony(ctx, CeremonyRegistration, session.UserID, session.ID, creation, data, now)
}

func (s *Service) CompleteRegistration(ctx context.Context, session sessions.Session, ceremonyID, name string, response []byte) (CredentialSummary, error) {
	if !s.sessions.RecentlyReauthenticated(session, Reauthentication) {
		return CredentialSummary{}, ErrReauthenticationNeeded
	}
	name = strings.TrimSpace(name)
	if len(name) < 2 || len(name) > 80 {
		return CredentialSummary{}, ErrInvalidCredential
	}
	now := s.clock.Now().UTC()
	ceremony, err := s.repository.ConsumeCeremony(ctx, ceremonyID, CeremonyRegistration, session.UserID, session.ID, now)
	if err != nil {
		return CredentialSummary{}, ErrInvalidCeremony
	}
	user, err := s.repository.User(ctx, session.UserID)
	if err != nil {
		return CredentialSummary{}, ErrInvalidCredential
	}
	parsed, err := protocol.ParseCredentialCreationResponseBody(bytes.NewReader(response))
	if err != nil {
		return CredentialSummary{}, ErrInvalidCredential
	}
	credential, err := s.webauthn.CreateCredential(user, ceremony.Data, parsed)
	if err != nil || credential == nil || !credential.Flags.UserPresent || !credential.Flags.UserVerified || len(credential.ID) == 0 {
		return CredentialSummary{}, ErrInvalidCredential
	}
	if err := s.repository.CreateCredential(ctx, session.UserID, name, *credential, now, MaximumPasskeys); err != nil {
		return CredentialSummary{}, err
	}
	return summarize(CredentialRecord{UserID: session.UserID, Name: name, Credential: *credential, CreatedAt: now}), nil
}

func (s *Service) BeginLogin(ctx context.Context, networkActor [32]byte) (BeginResult, error) {
	now := s.clock.Now().UTC()
	allowed, err := s.network.Allow(ctx, abuse.ScopeLogin, networkActor, now, abuse.LoginPolicy)
	if err != nil {
		return BeginResult{}, err
	}
	if !allowed {
		return BeginResult{}, ErrInvalidCredential
	}
	assertion, data, err := s.webauthn.BeginDiscoverableLogin(webauthnlib.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return BeginResult{}, err
	}
	return s.storeCeremony(ctx, CeremonyLogin, "", "", assertion, data, now)
}

type LoginCommand struct {
	CeremonyID  string
	Response    []byte
	ClientLabel string
}

func (s *Service) CompleteLogin(ctx context.Context, command LoginCommand) (sessions.Issued, error) {
	now := s.clock.Now().UTC()
	ceremony, err := s.repository.ConsumeCeremony(ctx, command.CeremonyID, CeremonyLogin, "", "", now)
	if err != nil {
		return sessions.Issued{}, ErrInvalidCeremony
	}
	parsed, err := protocol.ParseCredentialRequestResponseBody(bytes.NewReader(command.Response))
	if err != nil {
		return sessions.Issued{}, ErrInvalidCredential
	}
	var loaded User
	var expected uint32
	user, credential, err := s.webauthn.ValidatePasskeyLogin(func(rawID, userHandle []byte) (webauthnlib.User, error) {
		loaded, err = s.repository.UserByHandle(ctx, userHandle, rawID)
		if err != nil || loaded.Identity.State != identity.UserActive {
			return nil, ErrInvalidCredential
		}
		for _, current := range loaded.Credentials {
			if bytes.Equal(current.Credential.ID, rawID) {
				expected = current.Credential.Authenticator.SignCount
				break
			}
		}
		return loaded, nil
	}, ceremony.Data, parsed)
	if err != nil || user == nil || credential == nil || !credential.Flags.UserPresent || !credential.Flags.UserVerified || loaded.Identity.ID == "" {
		return sessions.Issued{}, ErrInvalidCredential
	}
	if credential.Authenticator.CloneWarning {
		_ = s.repository.RecordCloneWarning(ctx, loaded.Identity.ID, credential.ID, now)
		return sessions.Issued{}, ErrInvalidCredential
	}
	updated, err := s.repository.UpdateCredential(ctx, loaded.Identity.ID, credential.ID, expected, *credential, EventAuthenticated, now)
	if err != nil {
		return sessions.Issued{}, err
	}
	if !updated {
		return sessions.Issued{}, ErrCredentialStateConflict
	}
	return s.sessions.IssueForClientWithMethod(ctx, loaded.Identity.ID, loaded.Identity.SecurityVersion, command.ClientLabel, sessions.AuthenticationMethodPasskey)
}

func (s *Service) BeginReauthentication(ctx context.Context, session sessions.Session) (BeginResult, error) {
	now := s.clock.Now().UTC()
	user, err := s.repository.User(ctx, session.UserID)
	if err != nil || len(user.Credentials) == 0 {
		return BeginResult{}, ErrCredentialNotFound
	}
	assertion, data, err := s.webauthn.BeginLogin(user, webauthnlib.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return BeginResult{}, err
	}
	return s.storeCeremony(ctx, CeremonyReauthentication, session.UserID, session.ID, assertion, data, now)
}

func (s *Service) CompleteReauthentication(ctx context.Context, session sessions.Session, ceremonyID string, response []byte) error {
	now := s.clock.Now().UTC()
	ceremony, err := s.repository.ConsumeCeremony(ctx, ceremonyID, CeremonyReauthentication, session.UserID, session.ID, now)
	if err != nil {
		return ErrInvalidCeremony
	}
	user, err := s.repository.User(ctx, session.UserID)
	if err != nil {
		return ErrInvalidCredential
	}
	parsed, err := protocol.ParseCredentialRequestResponseBody(bytes.NewReader(response))
	if err != nil {
		return ErrInvalidCredential
	}
	expected := credentialCounter(user, parsed.RawID)
	credential, err := s.webauthn.ValidateLogin(user, ceremony.Data, parsed)
	if err != nil || credential == nil || !credential.Flags.UserPresent || !credential.Flags.UserVerified {
		return ErrInvalidCredential
	}
	if credential.Authenticator.CloneWarning {
		_ = s.repository.RecordCloneWarning(ctx, session.UserID, credential.ID, now)
		return ErrInvalidCredential
	}
	updated, err := s.repository.UpdateCredential(ctx, session.UserID, credential.ID, expected, *credential, EventReauthenticated, now)
	if err != nil {
		return err
	}
	if !updated {
		return ErrCredentialStateConflict
	}
	return s.sessions.MarkReauthenticatedWithMethod(ctx, session.UserID, session.ID, sessions.AuthenticationMethodPasskey)
}

func (s *Service) Credentials(ctx context.Context, userID ids.UserID) ([]CredentialSummary, error) {
	values, err := s.repository.ListCredentials(ctx, userID)
	if err != nil {
		return nil, err
	}
	result := make([]CredentialSummary, 0, len(values))
	for _, value := range values {
		result = append(result, summarize(value))
	}
	return result, nil
}

func (s *Service) Delete(ctx context.Context, session sessions.Session, encodedID string) error {
	if !s.sessions.RecentlyReauthenticated(session, Reauthentication) {
		return ErrReauthenticationNeeded
	}
	credentialID, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(encodedID))
	if err != nil || len(credentialID) == 0 {
		return ErrCredentialNotFound
	}
	deleted, err := s.repository.DeleteCredential(ctx, session.UserID, credentialID, s.clock.Now().UTC())
	if err != nil {
		return err
	}
	if !deleted {
		return ErrCredentialNotFound
	}
	return nil
}

func (s *Service) storeCeremony(ctx context.Context, kind CeremonyKind, userID ids.UserID, sessionID ids.SessionID, options any, data *webauthnlib.SessionData, now time.Time) (BeginResult, error) {
	if data == nil {
		return BeginResult{}, ErrInvalidCeremony
	}
	expires := now.Add(CeremonyTTL)
	data.Expires = expires
	raw, err := json.Marshal(options)
	if err != nil {
		return BeginResult{}, err
	}
	id := s.ids.New()
	if err := s.repository.CreateCeremony(ctx, Ceremony{ID: id, Kind: kind, UserID: userID, SessionID: sessionID, Data: *data, ExpiresAt: expires, CreatedAt: now}); err != nil {
		return BeginResult{}, err
	}
	return BeginResult{CeremonyID: id, PublicKey: raw, ExpiresAt: expires}, nil
}

func credentialCounter(user User, id []byte) uint32 {
	for _, credential := range user.Credentials {
		if bytes.Equal(credential.Credential.ID, id) {
			return credential.Credential.Authenticator.SignCount
		}
	}
	return 0
}

func summarize(value CredentialRecord) CredentialSummary {
	return CredentialSummary{
		ID:             base64.RawURLEncoding.EncodeToString(value.Credential.ID),
		Name:           value.Name,
		CreatedAt:      value.CreatedAt,
		LastUsedAt:     value.LastUsedAt,
		BackupEligible: value.Credential.Flags.BackupEligible,
		BackedUp:       value.Credential.Flags.BackupState,
	}
}

package sessions

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrInvalidSession = errors.New("invalid session")
	ErrExpiredSession = errors.New("expired session")
)

type Session struct {
	ID                     ids.SessionID
	UserID                 ids.UserID
	TokenHash              [32]byte
	SecurityVersion        uint64
	AuthenticatedAt        time.Time
	ReauthenticatedAt      time.Time
	LastSeenAt             time.Time
	RotatedAt              time.Time
	ExpiresAt              time.Time
	RevokedAt              *time.Time
	ClientLabel            string
	AuthenticationMethod   AuthenticationMethod
	ReauthenticationMethod AuthenticationMethod
}

type AuthenticationMethod string

const (
	AuthenticationMethodPassword AuthenticationMethod = "password"
	AuthenticationMethodPasskey  AuthenticationMethod = "passkey"
	AuthenticationMethodOIDC     AuthenticationMethod = "oidc"
	AuthenticationMethodSMSOTP   AuthenticationMethod = "sms_otp"
	AuthenticationMethodEmailOTP AuthenticationMethod = "email_otp"
)

type AuthenticationAssurance string

const (
	AssuranceUnknown                   AuthenticationAssurance = "unknown"
	AssuranceSingleFactor              AuthenticationAssurance = "single_factor"
	AssuranceUserVerifiedCryptographic AuthenticationAssurance = "user_verified_cryptographic"
	AssuranceMultiFactor               AuthenticationAssurance = "multi_factor"
)

func (m AuthenticationMethod) Valid() bool {
	return m == AuthenticationMethodPassword || m == AuthenticationMethodPasskey || m == AuthenticationMethodOIDC || m == AuthenticationMethodSMSOTP || m == AuthenticationMethodEmailOTP
}

func (m AuthenticationMethod) Assurance() AuthenticationAssurance {
	switch m {
	case AuthenticationMethodPassword, AuthenticationMethodOIDC:
		return AssuranceSingleFactor
	case AuthenticationMethodPasskey:
		return AssuranceUserVerifiedCryptographic
	case AuthenticationMethodSMSOTP, AuthenticationMethodEmailOTP:
		return AssuranceMultiFactor
	default:
		return AssuranceUnknown
	}
}

type Repository interface {
	Create(context.Context, Session) error
	Use(context.Context, [32]byte, time.Time, time.Duration) (Session, error)
	Rotate(context.Context, ids.SessionID, [32]byte, [32]byte, time.Time) (bool, error)
	Revoke(context.Context, ids.SessionID, time.Time) error
	RevokeAll(context.Context, ids.UserID, time.Time) error
	RevokeOwned(context.Context, ids.UserID, ids.SessionID, time.Time) (bool, error)
	Active(context.Context, ids.UserID, time.Time, time.Duration) ([]Session, error)
	MarkReauthenticated(context.Context, ids.UserID, ids.SessionID, AuthenticationMethod, time.Time) (bool, error)
	SecurityEvents(context.Context, ids.UserID, int) ([]SecurityEvent, error)
}

type SecurityEventType string

const (
	EventSessionCreated              SecurityEventType = "session_created"
	EventSessionReauthenticated      SecurityEventType = "session_reauthenticated"
	EventSessionRevoked              SecurityEventType = "session_revoked"
	EventSessionsRevoked             SecurityEventType = "sessions_revoked"
	EventCredentialRecovered         SecurityEventType = "credential_recovered"
	EventPasskeyAdded                SecurityEventType = "passkey_added"
	EventPasskeyRemoved              SecurityEventType = "passkey_removed"
	EventPasskeyRenamed              SecurityEventType = "passkey_renamed"
	EventPasskeyCompromised          SecurityEventType = "passkey_compromised"
	EventPasskeyAuthenticated        SecurityEventType = "passkey_authenticated"
	EventPasskeyReauthenticated      SecurityEventType = "passkey_reauthenticated"
	EventPasskeyCloneWarning         SecurityEventType = "passkey_clone_warning"
	EventRecoveryCodesRotated        SecurityEventType = "recovery_codes_rotated"
	EventRecoveryCodeConsumed        SecurityEventType = "recovery_code_consumed"
	EventPrimaryEmailChangeRequested SecurityEventType = "primary_email_change_requested"
	EventPrimaryEmailChanged         SecurityEventType = "primary_email_changed"
	EventMFAMethodAdded              SecurityEventType = "mfa_method_added"
	EventMFAReauthenticated          SecurityEventType = "mfa_reauthenticated"
)

type SecurityEvent struct {
	Type       SecurityEventType `json:"type"`
	SessionID  ids.SessionID     `json:"session_id,omitempty"`
	OccurredAt time.Time         `json:"occurred_at"`
}

type Clock interface{ Now() time.Time }

type Service struct {
	repository       Repository
	ids              ids.Generator
	clock            Clock
	absoluteTTL      time.Duration
	idleTTL          time.Duration
	rotationInterval time.Duration
}

func NewService(repository Repository, generator ids.Generator, clock Clock, absoluteTTL, idleTTL, rotationInterval time.Duration) (*Service, error) {
	if repository == nil || generator == nil || clock == nil {
		return nil, errors.New("session dependencies are required")
	}
	if absoluteTTL <= 0 || idleTTL <= 0 || rotationInterval <= 0 || idleTTL > absoluteTTL || rotationInterval > idleTTL {
		return nil, errors.New("invalid session lifetime policy")
	}
	return &Service{repository: repository, ids: generator, clock: clock, absoluteTTL: absoluteTTL, idleTTL: idleTTL, rotationInterval: rotationInterval}, nil
}

type Issued struct {
	Session Session
	Token   string
}

func (s *Service) Issue(ctx context.Context, userID ids.UserID, securityVersion uint64) (Issued, error) {
	return s.IssueForClient(ctx, userID, securityVersion, "Unknown browser")
}

func (s *Service) IssueForClient(ctx context.Context, userID ids.UserID, securityVersion uint64, clientLabel string) (Issued, error) {
	return s.IssueForClientWithMethod(ctx, userID, securityVersion, clientLabel, AuthenticationMethodPassword)
}

func (s *Service) IssueForClientWithMethod(ctx context.Context, userID ids.UserID, securityVersion uint64, clientLabel string, method AuthenticationMethod) (Issued, error) {
	if userID == "" || securityVersion == 0 {
		return Issued{}, errors.New("user ID and security version are required")
	}
	if !method.Valid() {
		return Issued{}, errors.New("authentication method is invalid")
	}
	token, hash, err := newToken()
	if err != nil {
		return Issued{}, err
	}
	now := s.clock.Now().UTC()
	clientLabel = strings.TrimSpace(clientLabel)
	if clientLabel == "" {
		clientLabel = "Unknown browser"
	}
	if len(clientLabel) > 160 {
		clientLabel = clientLabel[:160]
	}
	session := Session{ID: ids.SessionID(s.ids.New()), UserID: userID, TokenHash: hash, SecurityVersion: securityVersion, AuthenticatedAt: now, ReauthenticatedAt: now, LastSeenAt: now, RotatedAt: now, ExpiresAt: now.Add(s.absoluteTTL), ClientLabel: clientLabel, AuthenticationMethod: method, ReauthenticationMethod: method}
	if err := s.repository.Create(ctx, session); err != nil {
		return Issued{}, err
	}
	return Issued{Session: session, Token: token}, nil
}

type ActiveSession struct {
	ID                        ids.SessionID           `json:"id"`
	ClientLabel               string                  `json:"client_label"`
	AuthenticatedAt           time.Time               `json:"authenticated_at"`
	ReauthenticatedAt         time.Time               `json:"reauthenticated_at"`
	LastSeenAt                time.Time               `json:"last_seen_at"`
	ExpiresAt                 time.Time               `json:"expires_at"`
	Current                   bool                    `json:"current"`
	AuthenticationMethod      AuthenticationMethod    `json:"authentication_method"`
	AuthenticationAssurance   AuthenticationAssurance `json:"authentication_assurance"`
	ReauthenticationMethod    AuthenticationMethod    `json:"reauthentication_method"`
	ReauthenticationAssurance AuthenticationAssurance `json:"reauthentication_assurance"`
}

func (s *Service) Active(ctx context.Context, userID ids.UserID, currentID ids.SessionID) ([]ActiveSession, error) {
	if userID == "" || currentID == "" {
		return nil, errors.New("user ID and current session ID are required")
	}
	values, err := s.repository.Active(ctx, userID, s.clock.Now().UTC(), s.idleTTL)
	if err != nil {
		return nil, err
	}
	result := make([]ActiveSession, 0, len(values))
	for _, value := range values {
		result = append(result, ActiveSession{ID: value.ID, ClientLabel: value.ClientLabel, AuthenticatedAt: value.AuthenticatedAt, ReauthenticatedAt: value.ReauthenticatedAt, LastSeenAt: value.LastSeenAt, ExpiresAt: value.ExpiresAt, Current: value.ID == currentID, AuthenticationMethod: value.AuthenticationMethod, AuthenticationAssurance: value.AuthenticationMethod.Assurance(), ReauthenticationMethod: value.ReauthenticationMethod, ReauthenticationAssurance: value.ReauthenticationMethod.Assurance()})
	}
	return result, nil
}

func (s *Service) SecurityEvents(ctx context.Context, userID ids.UserID, limit int) ([]SecurityEvent, error) {
	if userID == "" {
		return nil, errors.New("user ID is required")
	}
	if limit == 0 {
		limit = 25
	}
	if limit < 1 || limit > 100 {
		return nil, errors.New("security event limit must be between 1 and 100")
	}
	return s.repository.SecurityEvents(ctx, userID, limit)
}

func (s *Service) RevokeOwned(ctx context.Context, userID ids.UserID, sessionID ids.SessionID) (bool, error) {
	if userID == "" || sessionID == "" {
		return false, errors.New("user ID and session ID are required")
	}
	return s.repository.RevokeOwned(ctx, userID, sessionID, s.clock.Now().UTC())
}

func (s *Service) MarkReauthenticated(ctx context.Context, userID ids.UserID, sessionID ids.SessionID) error {
	return s.MarkReauthenticatedWithMethod(ctx, userID, sessionID, AuthenticationMethodPassword)
}

func (s *Service) MarkReauthenticatedWithMethod(ctx context.Context, userID ids.UserID, sessionID ids.SessionID, method AuthenticationMethod) error {
	if userID == "" || sessionID == "" {
		return errors.New("user ID and session ID are required")
	}
	if !method.Valid() {
		return errors.New("reauthentication method is invalid")
	}
	updated, err := s.repository.MarkReauthenticated(ctx, userID, sessionID, method, s.clock.Now().UTC())
	if err != nil {
		return err
	}
	if !updated {
		return ErrInvalidSession
	}
	return nil
}

func RecentlyReauthenticatedWithAssurance(value Session, now time.Time, maximumAge time.Duration, assurance AuthenticationAssurance) bool {
	return assurance != AssuranceUnknown && RecentlyReauthenticated(value, now, maximumAge) && value.ReauthenticationMethod.Assurance() == assurance
}

func RecentlyStronglyReauthenticated(value Session, now time.Time, maximumAge time.Duration) bool {
	if !RecentlyReauthenticated(value, now, maximumAge) {
		return false
	}
	assurance := value.ReauthenticationMethod.Assurance()
	return assurance == AssuranceUserVerifiedCryptographic || assurance == AssuranceMultiFactor
}

func (s *Service) RecentlyReauthenticatedWithAssurance(value Session, maximumAge time.Duration, assurance AuthenticationAssurance) bool {
	return RecentlyReauthenticatedWithAssurance(value, s.clock.Now().UTC(), maximumAge, assurance)
}

func RecentlyReauthenticated(value Session, now time.Time, maximumAge time.Duration) bool {
	return maximumAge > 0 && !value.ReauthenticatedAt.IsZero() && !value.ReauthenticatedAt.After(now) && now.Sub(value.ReauthenticatedAt) <= maximumAge
}

func (s *Service) RecentlyReauthenticated(value Session, maximumAge time.Duration) bool {
	return RecentlyReauthenticated(value, s.clock.Now().UTC(), maximumAge)
}

type Authenticated struct {
	Session      Session
	RotatedToken string
}

func (s *Service) Authenticate(ctx context.Context, token string) (Authenticated, error) {
	if token == "" {
		return Authenticated{}, ErrInvalidSession
	}
	now := s.clock.Now().UTC()
	oldHash := sha256.Sum256([]byte(token))
	session, err := s.repository.Use(ctx, oldHash, now, s.idleTTL)
	if err != nil {
		return Authenticated{}, err
	}
	result := Authenticated{Session: session}
	if now.Sub(session.RotatedAt) < s.rotationInterval {
		return result, nil
	}
	rotatedToken, newHash, err := newToken()
	if err != nil {
		return Authenticated{}, err
	}
	rotated, err := s.repository.Rotate(ctx, session.ID, oldHash, newHash, now)
	if err != nil {
		return Authenticated{}, err
	}
	if !rotated {
		return Authenticated{}, ErrInvalidSession
	}
	result.Session.TokenHash = newHash
	result.Session.RotatedAt = now
	result.RotatedToken = rotatedToken
	return result, nil
}

func (s *Service) RevokeAll(ctx context.Context, userID ids.UserID) error {
	if userID == "" {
		return errors.New("user ID is required")
	}
	return s.repository.RevokeAll(ctx, userID, s.clock.Now().UTC())
}

func (s *Service) Revoke(ctx context.Context, sessionID ids.SessionID) error {
	if sessionID == "" {
		return errors.New("session ID is required")
	}
	return s.repository.Revoke(ctx, sessionID, s.clock.Now().UTC())
}

func newToken() (string, [32]byte, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", [32]byte{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	return token, sha256.Sum256([]byte(token)), nil
}

// Package mcpauth owns MCP OAuth authorization requests, one-use codes,
// audience-bound access tokens, rotating refresh tokens, and revocation.
package mcpauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	ScopeMCP                = "spyglass:mcp"
	AuthorizationRequestTTL = 10 * time.Minute
	AuthorizationCodeTTL    = 5 * time.Minute
	AccessTokenTTL          = 15 * time.Minute
	RefreshTokenTTL         = 30 * 24 * time.Hour
	maximumClientIDBytes    = 2048
	maximumRedirectURIBytes = 2048
	maximumClientNameBytes  = 160
	maximumOAuthStateBytes  = 2048
)

var (
	ErrInvalid       = errors.New("invalid MCP OAuth request")
	ErrNotFound      = errors.New("MCP OAuth request not found")
	ErrExpired       = errors.New("MCP OAuth credential expired")
	ErrConsumed      = errors.New("MCP OAuth credential already consumed")
	ErrAccessDenied  = errors.New("MCP OAuth access denied")
	ErrRefreshReuse  = errors.New("MCP OAuth refresh-token reuse detected")
	pkceValuePattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)
)

type Clock interface{ Now() time.Time }

type SecretGenerator interface {
	New() (string, [32]byte, error)
}

type RandomSecrets struct{}

func (RandomSecrets) New() (string, [32]byte, error) {
	var material [32]byte
	if _, err := rand.Read(material[:]); err != nil {
		return "", [32]byte{}, err
	}
	value := base64.RawURLEncoding.EncodeToString(material[:])
	return value, sha256.Sum256([]byte(value)), nil
}

type Client struct {
	ID           string
	Name         string
	RedirectURIs []string
}

type AuthorizationCommand struct {
	UserID        ids.UserID
	SessionID     ids.SessionID
	Client        Client
	RedirectURI   string
	Resource      string
	Scope         string
	State         string
	CodeChallenge string
}

type PendingAuthorization struct {
	ID            string
	UserID        ids.UserID
	SessionID     ids.SessionID
	ClientID      string
	ClientName    string
	RedirectURI   string
	Resource      string
	Scope         string
	State         string
	CodeChallenge string
	ExpiresAt     time.Time
	CreatedAt     time.Time
}

type AuthorizationDecision struct {
	PendingID string
	UserID    ids.UserID
	SessionID ids.SessionID
	Approve   bool
}

type AuthorizationResult struct {
	RedirectURI string
	State       string
	Issuer      string
	Code        string
	Approved    bool
}

type CodeExchange struct {
	CodeHash          [32]byte
	ClientID          string
	RedirectURI       string
	Resource          string
	VerifierChallenge string
	Access            Credential
	Refresh           RefreshCredential
	Now               time.Time
}

type RefreshExchange struct {
	RefreshHash [32]byte
	ClientID    string
	Resource    string
	Access      Credential
	Refresh     RefreshCredential
	Now         time.Time
}

type Credential struct {
	Hash      [32]byte
	ExpiresAt time.Time
}

type RefreshCredential struct {
	Hash      [32]byte
	ExpiresAt time.Time
}

type IssuedAuthority struct {
	UserID   ids.UserID
	Resource string
	Scope    string
}

type TokenSet struct {
	AccessToken  string
	RefreshToken string
	TokenType    string
	Scope        string
	ExpiresIn    int64
}

type TokenRequirement struct {
	Audience string
	Scope    string
}

type Repository interface {
	CreateAuthorization(context.Context, PendingAuthorization) error
	DecideAuthorization(context.Context, AuthorizationDecision, [32]byte, string, time.Time, time.Time) (PendingAuthorization, error)
	ExchangeCode(context.Context, CodeExchange) (IssuedAuthority, error)
	RotateRefresh(context.Context, RefreshExchange) (IssuedAuthority, error)
	AuthenticateAccess(context.Context, [32]byte, TokenRequirement, time.Time) (access.Actor, error)
	Revoke(context.Context, [32]byte, string, time.Time) error
}

type Service struct {
	repository Repository
	ids        ids.Generator
	secrets    SecretGenerator
	clock      Clock
	issuer     string
	resource   string
}

func New(repository Repository, generator ids.Generator, secrets SecretGenerator, clock Clock, issuer, resource string) (*Service, error) {
	issuer, resource = strings.TrimSpace(issuer), strings.TrimSpace(resource)
	if repository == nil || generator == nil || secrets == nil || clock == nil || !canonicalResource(issuer) || !canonicalResource(resource) {
		return nil, errors.New("MCP OAuth dependencies and canonical origins are required")
	}
	return &Service{repository: repository, ids: generator, secrets: secrets, clock: clock, issuer: issuer, resource: resource}, nil
}

func (s *Service) Begin(ctx context.Context, command AuthorizationCommand) (PendingAuthorization, error) {
	if err := validateAuthorization(command, s.resource); err != nil {
		return PendingAuthorization{}, err
	}
	now := s.clock.Now().UTC()
	pending := PendingAuthorization{
		ID: s.ids.New(), UserID: command.UserID, SessionID: command.SessionID,
		ClientID: command.Client.ID, ClientName: strings.TrimSpace(command.Client.Name), RedirectURI: command.RedirectURI,
		Resource: command.Resource, Scope: command.Scope, State: command.State, CodeChallenge: command.CodeChallenge,
		ExpiresAt: now.Add(AuthorizationRequestTTL), CreatedAt: now,
	}
	if ids.Validate(pending.ID) != nil {
		return PendingAuthorization{}, ErrInvalid
	}
	if err := s.repository.CreateAuthorization(ctx, pending); err != nil {
		return PendingAuthorization{}, err
	}
	return pending, nil
}

func (s *Service) Decide(ctx context.Context, decision AuthorizationDecision) (AuthorizationResult, error) {
	if ids.Validate(decision.PendingID) != nil || ids.Validate(string(decision.UserID)) != nil || ids.Validate(string(decision.SessionID)) != nil {
		return AuthorizationResult{}, ErrInvalid
	}
	now := s.clock.Now().UTC()
	code, codeHash, err := s.secrets.New()
	if err != nil {
		return AuthorizationResult{}, err
	}
	if !decision.Approve {
		code, codeHash = "", [32]byte{}
	}
	grantID := s.ids.New()
	if ids.Validate(grantID) != nil {
		return AuthorizationResult{}, ErrInvalid
	}
	pending, err := s.repository.DecideAuthorization(ctx, decision, codeHash, grantID, now, now.Add(AuthorizationCodeTTL))
	if err != nil {
		return AuthorizationResult{}, err
	}
	return AuthorizationResult{RedirectURI: pending.RedirectURI, State: pending.State, Issuer: s.issuer, Code: code, Approved: decision.Approve}, nil
}

func (s *Service) ExchangeCode(ctx context.Context, code, clientID, redirectURI, resource, verifier string) (TokenSet, error) {
	if !validSecret(code) || !validClientID(clientID) || !validRedirectURI(redirectURI) || resource != s.resource || !pkceValuePattern.MatchString(verifier) {
		return TokenSet{}, ErrInvalid
	}
	accessToken, accessHash, err := s.secrets.New()
	if err != nil {
		return TokenSet{}, err
	}
	refreshToken, refreshHash, err := s.secrets.New()
	if err != nil {
		return TokenSet{}, err
	}
	now := s.clock.Now().UTC()
	issued, err := s.repository.ExchangeCode(ctx, CodeExchange{
		CodeHash: sha256.Sum256([]byte(code)), ClientID: clientID, RedirectURI: redirectURI, Resource: resource, VerifierChallenge: pkceChallenge(verifier),
		Access: Credential{Hash: accessHash, ExpiresAt: now.Add(AccessTokenTTL)}, Refresh: RefreshCredential{Hash: refreshHash, ExpiresAt: now.Add(RefreshTokenTTL)}, Now: now,
	})
	if err != nil {
		return TokenSet{}, err
	}
	if issued.UserID == "" || issued.Resource != resource || issued.Scope != ScopeMCP {
		return TokenSet{}, ErrInvalid
	}
	return tokenSet(accessToken, refreshToken), nil
}

func (s *Service) Refresh(ctx context.Context, refreshToken, clientID, resource string) (TokenSet, error) {
	if !validSecret(refreshToken) || !validClientID(clientID) || resource != s.resource {
		return TokenSet{}, ErrInvalid
	}
	accessToken, accessHash, err := s.secrets.New()
	if err != nil {
		return TokenSet{}, err
	}
	rotatedToken, rotatedHash, err := s.secrets.New()
	if err != nil {
		return TokenSet{}, err
	}
	now := s.clock.Now().UTC()
	issued, err := s.repository.RotateRefresh(ctx, RefreshExchange{
		RefreshHash: sha256.Sum256([]byte(refreshToken)), ClientID: clientID, Resource: resource,
		Access: Credential{Hash: accessHash, ExpiresAt: now.Add(AccessTokenTTL)}, Refresh: RefreshCredential{Hash: rotatedHash, ExpiresAt: now.Add(RefreshTokenTTL)}, Now: now,
	})
	if err != nil {
		return TokenSet{}, err
	}
	if issued.UserID == "" || issued.Resource != resource || issued.Scope != ScopeMCP {
		return TokenSet{}, ErrInvalid
	}
	return tokenSet(accessToken, rotatedToken), nil
}

func (s *Service) Authenticate(ctx context.Context, token string, requirement TokenRequirement) (access.Actor, error) {
	if !validSecret(token) || requirement.Audience != s.resource || requirement.Scope != ScopeMCP {
		return access.Actor{}, ErrAccessDenied
	}
	actor, err := s.repository.AuthenticateAccess(ctx, sha256.Sum256([]byte(token)), requirement, s.clock.Now().UTC())
	if err != nil || !actor.Valid() {
		return access.Actor{}, ErrAccessDenied
	}
	return actor, nil
}

func (s *Service) Revoke(ctx context.Context, token, clientID string) error {
	if !validSecret(token) || !validClientID(clientID) {
		return nil
	}
	return s.repository.Revoke(ctx, sha256.Sum256([]byte(token)), clientID, s.clock.Now().UTC())
}

func tokenSet(accessToken, refreshToken string) TokenSet {
	return TokenSet{AccessToken: accessToken, RefreshToken: refreshToken, TokenType: "Bearer", Scope: ScopeMCP, ExpiresIn: int64(AccessTokenTTL / time.Second)}
}

func validateAuthorization(command AuthorizationCommand, resource string) error {
	if ids.Validate(string(command.UserID)) != nil || ids.Validate(string(command.SessionID)) != nil || !validClientID(command.Client.ID) || len(strings.TrimSpace(command.Client.Name)) == 0 || len(strings.TrimSpace(command.Client.Name)) > maximumClientNameBytes || len(command.Client.RedirectURIs) == 0 || command.Resource != resource || command.Scope != ScopeMCP || len(command.State) > maximumOAuthStateBytes || !validPKCEChallenge(command.CodeChallenge) || !validRedirectURI(command.RedirectURI) || !slices.Contains(command.Client.RedirectURIs, command.RedirectURI) {
		return ErrInvalid
	}
	for _, redirect := range command.Client.RedirectURIs {
		if !validRedirectURI(redirect) {
			return ErrInvalid
		}
	}
	return nil
}

func validPKCEChallenge(value string) bool {
	if len(value) != 43 {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(raw) == sha256.Size && base64.RawURLEncoding.EncodeToString(raw) == value
}

func pkceChallenge(verifier string) string {
	digest := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func validSecret(value string) bool {
	if len(value) != 43 || value != strings.TrimSpace(value) {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(raw) == 32 && base64.RawURLEncoding.EncodeToString(raw) == value
}

func validClientID(value string) bool {
	return len(value) > 0 && len(value) <= maximumClientIDBytes && canonicalHTTPS(value, true)
}

func validRedirectURI(value string) bool {
	if len(value) == 0 || len(value) > maximumRedirectURIBytes || strings.ContainsAny(value, "\r\n\x00") {
		return false
	}
	return validOAuthRedirect(value)
}

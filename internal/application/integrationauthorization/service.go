package integrationauthorization

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationcredentials"
	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const AuthorizationTTL = 15 * time.Minute

type Authorizer interface {
	Authorize(context.Context, access.Actor, ids.AccountID, access.Requirement) (access.AccountContext, error)
}

type Clock interface{ Now() time.Time }

type Authority struct {
	Connection domain.Connection
	Revision   domain.ConnectionRevision
}

type Event struct {
	ID            string
	Type          string
	ActorKind     string
	ActorID       string
	CorrelationID string
	At            time.Time
}

func (value Event) Valid() bool {
	return ids.Validate(value.ID) == nil && value.Type == "authorization_started" && value.ActorKind == "user" &&
		ids.Validate(value.ActorID) == nil && ids.Validate(value.CorrelationID) == nil && !value.At.IsZero()
}

type Repository interface {
	Authority(context.Context, ids.AccountID, ids.IntegrationConnectionID) (Authority, error)
	Create(context.Context, domain.AuthorizationSession, accounts.MembershipRole, Event) (domain.AuthorizationSession, bool, error)
	Get(context.Context, ids.AccountID, ids.IntegrationAuthorizationSessionID) (domain.AuthorizationSession, error)
}

type Service struct {
	authorizer Authorizer
	repository Repository
	secrets    integrationcredentials.Store
	provider   Provider
	generator  SecretGenerator
	clock      Clock
}

func NewService(authorizer Authorizer, repository Repository, secrets integrationcredentials.Store, provider Provider, generator SecretGenerator, clock Clock) (*Service, error) {
	if authorizer == nil || repository == nil || secrets == nil || provider == nil || generator == nil || clock == nil {
		return nil, errors.New("Integration authorization dependencies are required")
	}
	return &Service{authorizer: authorizer, repository: repository, secrets: secrets, provider: provider, generator: generator, clock: clock}, nil
}

type BeginCommand struct {
	Actor        access.Actor
	Session      sessions.Session
	AccountID    ids.AccountID
	RequestID    string
	ConnectionID ids.IntegrationConnectionID
	RedirectURI  string
}

type BeginResult struct {
	Session          domain.AuthorizationSession
	AuthorizationURL string
}

func (service *Service) Begin(ctx context.Context, command BeginCommand) (BeginResult, error) {
	if !command.Actor.Valid() || command.Actor.UserID == "" || ids.Validate(string(command.Actor.UserID)) != nil ||
		ids.Validate(string(command.AccountID)) != nil || ids.Validate(command.RequestID) != nil || ids.Validate(string(command.ConnectionID)) != nil {
		return BeginResult{}, ErrInvalid
	}
	authorized, err := service.authorizer.Authorize(ctx, command.Actor, command.AccountID, access.Requirement{Package: catalog.PackageIntegrations,
		Mutation: true, Roles: []accounts.MembershipRole{accounts.RoleOwner, accounts.RoleAdministrator}})
	if err != nil {
		return BeginResult{}, err
	}
	if authorized.AccountID != command.AccountID || (authorized.Role != accounts.RoleOwner && authorized.Role != accounts.RoleAdministrator) {
		return BeginResult{}, ErrInvalid
	}
	now := service.clock.Now().UTC()
	if err := strongauth.Require(command.Session, command.Actor.UserID, now); err != nil {
		return BeginResult{}, err
	}
	sessionID := ids.IntegrationAuthorizationSessionID(command.RequestID)
	if existing, err := service.repository.Get(ctx, command.AccountID, sessionID); err == nil {
		return service.replay(ctx, existing, command, now)
	} else if !errors.Is(err, ErrNotFound) {
		return BeginResult{}, classify(err)
	}
	authority, err := service.repository.Authority(ctx, command.AccountID, command.ConnectionID)
	if err != nil {
		return BeginResult{}, classify(err)
	}
	if !validAuthority(authority, command.AccountID, command.ConnectionID) {
		return BeginResult{}, ErrInvalid
	}
	state, err := service.generator.New()
	if err != nil {
		return BeginResult{}, ErrRepository
	}
	defer wipe(state)
	verifier, err := service.generator.New()
	if err != nil {
		return BeginResult{}, ErrRepository
	}
	defer wipe(verifier)
	expiresAt := now.Add(AuthorizationTTL)
	putErr := service.secrets.PutAuthorization(ctx, integrationcredentials.AuthorizationSecret{AccountID: command.AccountID, SessionID: sessionID,
		State: state, Verifier: verifier, ExpiresAt: expiresAt})
	material, materialErr := service.secrets.Authorization(ctx, command.AccountID, sessionID, now)
	if materialErr != nil {
		if putErr != nil {
			return BeginResult{}, ErrRepository
		}
		return BeginResult{}, ErrRepository
	}
	defer material.Close()
	expiresAt = material.ExpiresAt.UTC()
	challenge := pkceChallenge(material.Verifier)
	defer wipe(challenge)
	input := domain.AuthorizationSessionInput{ID: sessionID, AccountID: command.AccountID, ConnectionID: command.ConnectionID,
		ConnectionRevision: authority.Revision.ID, Provider: domain.GoogleOAuthProvider, Scope: domain.GoogleDriveReadScope,
		ScopeRevisionSHA256: ScopeRevisionDigest(authority.Revision), RedirectURI: command.RedirectURI,
		StateSHA256: sha256.Sum256(material.State), PKCEChallengeSHA256: sha256.Sum256(challenge), CreatedBy: domain.Actor{UserID: command.Actor.UserID},
		CreatedAt: now, ExpiresAt: expiresAt}
	pending, err := domain.NewAuthorizationSession(input, authorized.Role)
	if err != nil {
		return BeginResult{}, ErrInvalid
	}
	eventID, err := ids.Derive(command.RequestID, "integration-authorization-started")
	if err != nil {
		return BeginResult{}, ErrInvalid
	}
	stored, _, err := service.repository.Create(ctx, pending, authorized.Role, Event{ID: eventID, Type: "authorization_started", ActorKind: "user",
		ActorID: string(command.Actor.UserID), CorrelationID: command.RequestID, At: now})
	if err != nil {
		if existing, loadErr := service.repository.Get(ctx, command.AccountID, sessionID); loadErr == nil {
			return service.replayWithMaterial(existing, command, material, now)
		}
		return BeginResult{}, classify(err)
	}
	if stored != pending {
		return BeginResult{}, ErrConflict
	}
	return service.result(stored, command, material, now)
}

func (service *Service) replay(ctx context.Context, existing domain.AuthorizationSession, command BeginCommand, now time.Time) (BeginResult, error) {
	material, err := service.secrets.Authorization(ctx, command.AccountID, existing.ID, now)
	if err != nil {
		return BeginResult{}, ErrRepository
	}
	defer material.Close()
	return service.replayWithMaterial(existing, command, material, now)
}

func (service *Service) replayWithMaterial(existing domain.AuthorizationSession, command BeginCommand, material integrationcredentials.AuthorizationMaterial, now time.Time) (BeginResult, error) {
	if existing.Status != domain.AuthorizationPending || existing.AccountID != command.AccountID || existing.ConnectionID != command.ConnectionID ||
		existing.RedirectURI != strings.TrimSpace(command.RedirectURI) || existing.CreatedBy.UserID != command.Actor.UserID || !existing.ExpiresAt.After(now) ||
		!existing.ExpiresAt.Equal(material.ExpiresAt.UTC()) || existing.StateSHA256 != sha256.Sum256(material.State) {
		return BeginResult{}, ErrConflict
	}
	challenge := pkceChallenge(material.Verifier)
	defer wipe(challenge)
	if existing.PKCEChallengeSHA256 != sha256.Sum256(challenge) {
		return BeginResult{}, ErrConflict
	}
	return service.result(existing, command, material, now)
}

func (service *Service) result(session domain.AuthorizationSession, command BeginCommand, material integrationcredentials.AuthorizationMaterial, now time.Time) (BeginResult, error) {
	if !session.ExpiresAt.After(now) {
		return BeginResult{}, ErrConflict
	}
	challenge := pkceChallenge(material.Verifier)
	defer wipe(challenge)
	authorizationURL, err := service.provider.AuthorizationURL(material.State, challenge, command.RedirectURI)
	if err != nil {
		return BeginResult{}, classify(err)
	}
	return BeginResult{Session: session, AuthorizationURL: authorizationURL}, nil
}

func ScopeRevisionDigest(revision domain.ConnectionRevision) [sha256.Size]byte {
	parts := []string{"spyglass/integration-authorization-scope/v1", string(revision.AccountID), string(revision.ConnectionID), string(revision.ID),
		strconv.FormatUint(revision.Revision, 10), string(domain.CapabilityDriveRead), domain.GoogleDriveReadScope}
	folders := append([]string(nil), revision.Scope.DriveFolderIDs...)
	slices.Sort(folders)
	parts = append(parts, folders...)
	return sha256.Sum256([]byte(strings.Join(parts, "\n")))
}

func validAuthority(authority Authority, accountID ids.AccountID, connectionID ids.IntegrationConnectionID) bool {
	connection, err := domain.RestoreConnection(authority.Connection)
	if err != nil {
		return false
	}
	revision, err := domain.RestoreConnectionRevision(authority.Revision, connection.Kind)
	return err == nil && connection.AccountID == accountID && connection.ID == connectionID && connection.Kind == domain.ConnectorGoogleDrive &&
		connection.State != domain.ConnectionRevoked && connection.CurrentRevisionID == revision.ID && connection.CurrentRevision == revision.Revision &&
		revision.AccountID == accountID && revision.ConnectionID == connectionID && len(revision.Capabilities) == 1 && revision.Capabilities[0] == domain.CapabilityDriveRead
}

func pkceChallenge(verifier []byte) []byte {
	digest := sha256.Sum256(verifier)
	return []byte(base64.RawURLEncoding.EncodeToString(digest[:]))
}

func classify(err error) error {
	if err == nil || errors.Is(err, ErrInvalid) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) ||
		errors.Is(err, ErrProviderUnavailable) || errors.Is(err, ErrProviderRejected) || errors.Is(err, ErrScopeMismatch) {
		return err
	}
	return errors.Join(ErrRepository, err)
}

func wipe(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

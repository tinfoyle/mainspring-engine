package integrationauthorization

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
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

const (
	AuthorizationTTL      = 15 * time.Minute
	ProviderExchangeLease = 2 * time.Minute
)

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
	ClaimCallback(context.Context, ids.AccountID, [sha256.Size]byte, [sha256.Size]byte, time.Time) (CallbackProgress, error)
	StartProviderExchange(context.Context, ids.AccountID, ids.IntegrationAuthorizationSessionID, time.Time) (CallbackProgress, bool, error)
	MarkCredentialStored(context.Context, ids.AccountID, ids.IntegrationAuthorizationSessionID, time.Time) (CallbackProgress, error)
	MarkPreviousFenced(context.Context, ids.AccountID, ids.IntegrationAuthorizationSessionID, time.Time) (CallbackProgress, error)
	CompleteCallback(context.Context, ids.AccountID, ids.IntegrationAuthorizationSessionID, time.Time) (CallbackProgress, error)
	FailCallback(context.Context, ids.AccountID, ids.IntegrationAuthorizationSessionID, string, time.Time) (CallbackProgress, error)
}

type WorkflowState string

const (
	WorkflowClaimed            WorkflowState = "claimed"
	WorkflowProviderExchanging WorkflowState = "provider_exchanging"
	WorkflowCredentialStored   WorkflowState = "credential_stored"
	WorkflowPreviousFenced     WorkflowState = "previous_fenced"
	WorkflowCompleted          WorkflowState = "completed"
	WorkflowFailed             WorkflowState = "failed"
)

type CredentialWorkflow struct {
	AccountID            ids.AccountID
	SessionID            ids.IntegrationAuthorizationSessionID
	State                WorkflowState
	ConnectionID         ids.IntegrationConnectionID
	ConnectionVersion    uint64
	TargetCredentialID   ids.IntegrationCredentialID
	TargetGeneration     uint64
	ReferenceSHA256      [sha256.Size]byte
	PreviousCredentialID ids.IntegrationCredentialID
	PreviousGeneration   uint64
	CodeSHA256           [sha256.Size]byte
	ExchangeStartedAt    *time.Time
	FailureCode          string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type CallbackProgress struct {
	Session  domain.AuthorizationSession
	Workflow CredentialWorkflow
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

type CallbackCommand struct {
	AccountID ids.AccountID
	State     []byte
	Code      []byte
}

type CallbackResult struct {
	Session domain.AuthorizationSession
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

func (service *Service) Callback(ctx context.Context, command CallbackCommand) (CallbackResult, error) {
	if ids.Validate(string(command.AccountID)) != nil || !validOAuthSecret(command.State) || !validAuthorizationCode(command.Code) {
		return CallbackResult{}, ErrInvalid
	}
	now := service.clock.Now().UTC()
	progress, err := service.repository.ClaimCallback(ctx, command.AccountID, sha256.Sum256(command.State), sha256.Sum256(command.Code), now)
	if err != nil {
		return CallbackResult{}, classify(err)
	}
	return service.advanceCallback(ctx, progress, command.Code, now)
}

func (service *Service) advanceCallback(ctx context.Context, progress CallbackProgress, code []byte, now time.Time) (CallbackResult, error) {
	if progress.Session.AccountID != progress.Workflow.AccountID || progress.Session.ID != progress.Workflow.SessionID ||
		progress.Session.ConnectionID != progress.Workflow.ConnectionID ||
		(progress.Session.Status == domain.AuthorizationCompleted) != (progress.Workflow.State == WorkflowCompleted) ||
		(progress.Session.Status == domain.AuthorizationFailed) != (progress.Workflow.State == WorkflowFailed) {
		return CallbackResult{}, ErrRepository
	}
	if progress.Workflow.State == WorkflowCompleted {
		_ = service.secrets.DeleteAuthorization(ctx, progress.Session.AccountID, progress.Session.ID)
		return CallbackResult{Session: progress.Session}, nil
	}
	if progress.Workflow.State == WorkflowFailed {
		_ = service.secrets.DeleteAuthorization(ctx, progress.Session.AccountID, progress.Session.ID)
		return CallbackResult{Session: progress.Session}, ErrProviderRejected
	}
	if progress.Workflow.State == WorkflowClaimed {
		material, err := service.secrets.Authorization(ctx, progress.Session.AccountID, progress.Session.ID, now)
		if err != nil {
			return CallbackResult{}, ErrRepository
		}
		defer material.Close()
		challenge := pkceChallenge(material.Verifier)
		if progress.Session.StateSHA256 != sha256.Sum256(material.State) || progress.Session.PKCEChallengeSHA256 != sha256.Sum256(challenge) ||
			!progress.Session.ExpiresAt.Equal(material.ExpiresAt.UTC()) {
			wipe(challenge)
			return CallbackResult{}, ErrConflict
		}
		wipe(challenge)
		started, changed, err := service.repository.StartProviderExchange(ctx, progress.Session.AccountID, progress.Session.ID, now)
		if err != nil {
			return CallbackResult{}, classify(err)
		}
		progress = started
		if !changed {
			return service.advanceCallback(ctx, progress, code, now)
		}
		codeCopy := append([]byte(nil), code...)
		credential, err := service.provider.Exchange(ctx, ExchangeRequest{Code: codeCopy, PKCEVerifier: material.Verifier, RedirectURI: progress.Session.RedirectURI})
		wipe(codeCopy)
		if err != nil {
			failed, failErr := service.repository.FailCallback(ctx, progress.Session.AccountID, progress.Session.ID, providerFailureCode(err), now)
			_ = service.secrets.DeleteAuthorization(ctx, progress.Session.AccountID, progress.Session.ID)
			if failErr != nil {
				return CallbackResult{}, classify(failErr)
			}
			return CallbackResult{Session: failed.Session}, err
		}
		defer credential.Close()
		materialJSON, err := json.Marshal(struct {
			RefreshToken string `json:"refresh_token"`
		}{RefreshToken: string(credential.RefreshToken)})
		if err != nil {
			return CallbackResult{}, ErrRepository
		}
		defer wipe(materialJSON)
		reference := CredentialReference(progress.Workflow)
		defer wipe(reference)
		secret := integrationcredentials.CredentialSecret{AccountID: progress.Session.AccountID, CredentialID: progress.Workflow.TargetCredentialID,
			Generation: progress.Workflow.TargetGeneration, Provider: domain.GoogleOAuthProvider, Reference: reference, Material: materialJSON}
		for attempt := 0; attempt < 2; attempt++ {
			_ = service.secrets.PutCredential(ctx, secret)
			exists, existsErr := service.secrets.CredentialExists(ctx, secret.AccountID, secret.CredentialID, secret.Generation, secret.Provider, progress.Workflow.ReferenceSHA256)
			if existsErr == nil && exists {
				progress, err = service.repository.MarkCredentialStored(ctx, progress.Session.AccountID, progress.Session.ID, now)
				if err != nil {
					return CallbackResult{}, classify(err)
				}
				return service.advanceCallback(ctx, progress, code, now)
			}
		}
		return CallbackResult{}, ErrRepository
	}
	if progress.Workflow.State == WorkflowProviderExchanging {
		exists, err := service.secrets.CredentialExists(ctx, progress.Session.AccountID, progress.Workflow.TargetCredentialID,
			progress.Workflow.TargetGeneration, domain.GoogleOAuthProvider, progress.Workflow.ReferenceSHA256)
		if err == nil && exists {
			progress, err = service.repository.MarkCredentialStored(ctx, progress.Session.AccountID, progress.Session.ID, now)
			if err != nil {
				return CallbackResult{}, classify(err)
			}
			return service.advanceCallback(ctx, progress, code, now)
		}
		if progress.Workflow.ExchangeStartedAt == nil || now.Sub(progress.Workflow.ExchangeStartedAt.UTC()) < ProviderExchangeLease {
			return CallbackResult{}, ErrConflict
		}
		failed, failErr := service.repository.FailCallback(ctx, progress.Session.AccountID, progress.Session.ID, "provider_exchange_outcome_unknown", now)
		_ = service.secrets.DeleteAuthorization(ctx, progress.Session.AccountID, progress.Session.ID)
		if failErr != nil {
			return CallbackResult{}, classify(failErr)
		}
		return CallbackResult{Session: failed.Session}, ErrProviderUnavailable
	}
	if progress.Workflow.State == WorkflowCredentialStored && progress.Workflow.PreviousCredentialID != "" {
		if err := service.secrets.FenceCredential(ctx, progress.Session.AccountID, progress.Workflow.PreviousCredentialID,
			progress.Workflow.PreviousGeneration, integrationcredentials.CredentialRotated); err != nil {
			return CallbackResult{}, ErrRepository
		}
		var err error
		progress, err = service.repository.MarkPreviousFenced(ctx, progress.Session.AccountID, progress.Session.ID, now)
		if err != nil {
			return CallbackResult{}, classify(err)
		}
		return service.advanceCallback(ctx, progress, code, now)
	}
	if progress.Workflow.State != WorkflowCredentialStored && progress.Workflow.State != WorkflowPreviousFenced {
		return CallbackResult{}, ErrRepository
	}
	completed, err := service.repository.CompleteCallback(ctx, progress.Session.AccountID, progress.Session.ID, now)
	if err != nil {
		return CallbackResult{}, classify(err)
	}
	_ = service.secrets.DeleteAuthorization(ctx, completed.Session.AccountID, completed.Session.ID)
	return CallbackResult{Session: completed.Session}, nil
}

func TargetCredentialID(sessionID ids.IntegrationAuthorizationSessionID) (ids.IntegrationCredentialID, error) {
	value, err := ids.Derive(string(sessionID), "integration-oauth-credential")
	return ids.IntegrationCredentialID(value), err
}

func CredentialReference(workflow CredentialWorkflow) []byte {
	return []byte("spyglass-encrypted://accounts/" + string(workflow.AccountID) + "/credentials/" + string(workflow.TargetCredentialID) + "/generations/" + strconv.FormatUint(workflow.TargetGeneration, 10))
}

func providerFailureCode(err error) string {
	switch {
	case errors.Is(err, ErrScopeMismatch):
		return "provider_scope_mismatch"
	case errors.Is(err, ErrProviderRejected):
		return "provider_exchange_rejected"
	default:
		return "provider_exchange_unavailable"
	}
}

func validOAuthSecret(value []byte) bool {
	if len(value) < 43 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if !(character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || strings.ContainsRune("-._~", rune(character))) {
			return false
		}
	}
	return true
}

func validAuthorizationCode(value []byte) bool {
	if len(value) < 16 || len(value) > 2048 {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
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
	if err == nil || errors.Is(err, ErrInvalid) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) || errors.Is(err, ErrExpired) ||
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

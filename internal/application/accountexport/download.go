package accountexport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/exportcapability"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrArtifactUnavailable = errors.New("Account export artifact is not available")
	ErrCapabilityInvalid   = errors.New("Account export download capability is invalid")
)

type ArtifactStore interface {
	GetArtifact(context.Context, ids.AccountID, string) (Status, Artifact, error)
}

type ArtifactReader interface {
	Open(context.Context, Artifact) (io.ReadCloser, error)
}

type CapabilitySigner interface {
	Issue(exportcapability.Authority, time.Time) (string, time.Time, error)
}

type CapabilityVerifier interface {
	Verify(string) (exportcapability.Claims, error)
}

type DownloadService struct {
	issuer   *CapabilityIssuer
	store    ArtifactStore
	reader   ArtifactReader
	verifier CapabilityVerifier
}

func NewDownloadService(store ArtifactStore, reader ArtifactReader, authorizer Authorizer, signer CapabilitySigner, verifier CapabilityVerifier, clock Clock) (*DownloadService, error) {
	if store == nil || reader == nil || authorizer == nil || signer == nil || verifier == nil || clock == nil {
		return nil, ErrInvalid
	}
	issuer, err := NewCapabilityIssuer(store, authorizer, signer, clock)
	if err != nil {
		return nil, err
	}
	return &DownloadService{issuer: issuer, store: store, reader: reader, verifier: verifier}, nil
}

// CapabilityIssuer is the global control-plane half of Account-export
// downloads. It can authorize and bind a short-lived capability to the current
// durable artifact but cannot read artifact content from object storage.
type CapabilityIssuer struct {
	store      ArtifactStore
	authorizer Authorizer
	signer     CapabilitySigner
	clock      Clock
}

func NewCapabilityIssuer(store ArtifactStore, authorizer Authorizer, signer CapabilitySigner, clock Clock) (*CapabilityIssuer, error) {
	if store == nil || authorizer == nil || signer == nil || clock == nil {
		return nil, ErrInvalid
	}
	return &CapabilityIssuer{store: store, authorizer: authorizer, signer: signer, clock: clock}, nil
}

type CapabilityCommand struct {
	AccountID ids.AccountID
	Actor     ids.UserID
	ExportID  string
	Session   sessions.Session
}

type Capability struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (service *DownloadService) Issue(ctx context.Context, command CapabilityCommand) (Capability, error) {
	return service.issuer.Issue(ctx, command)
}

func (service *CapabilityIssuer) Issue(ctx context.Context, command CapabilityCommand) (Capability, error) {
	if _, err := service.authorizer.Authorize(ctx, access.Actor{UserID: command.Actor}, command.AccountID, access.Requirement{Roles: []accounts.MembershipRole{accounts.RoleOwner}, AllowRestricted: true}); err != nil {
		return Capability{}, err
	}
	now := service.clock.Now().UTC()
	if err := strongauth.Require(command.Session, command.Actor, now); err != nil {
		return Capability{}, err
	}
	status, artifact, err := service.store.GetArtifact(ctx, command.AccountID, command.ExportID)
	if err != nil {
		return Capability{}, err
	}
	if !downloadable(status, artifact, now) {
		return Capability{}, ErrArtifactUnavailable
	}
	token, expiresAt, err := service.signer.Issue(exportcapability.Authority{
		AccountID: status.AccountID, UserID: command.Actor, ExportID: status.ID, RequestVersion: status.Version,
		ArtifactSHA256: hex.EncodeToString(artifact.SHA256[:]), ArtifactBytes: artifact.Bytes,
	}, status.ExpiresAt)
	if err != nil {
		return Capability{}, err
	}
	return Capability{Token: token, ExpiresAt: expiresAt}, nil
}

type Download struct {
	Body      io.ReadCloser
	Bytes     int64
	SHA256    [sha256.Size]byte
	ExportID  string
	ExpiresAt time.Time
}

func (service *DownloadService) Open(ctx context.Context, token string) (Download, error) {
	claims, err := service.verifier.Verify(token)
	if err != nil {
		return Download{}, errors.Join(ErrCapabilityInvalid, err)
	}
	status, artifact, err := service.store.GetArtifact(ctx, claims.Authority.AccountID, claims.Authority.ExportID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Download{}, ErrCapabilityInvalid
		}
		return Download{}, err
	}
	now := service.issuer.clock.Now().UTC()
	if !downloadable(status, artifact, now) || status.Version != claims.Authority.RequestVersion ||
		artifact.Bytes != claims.Authority.ArtifactBytes || hex.EncodeToString(artifact.SHA256[:]) != claims.Authority.ArtifactSHA256 {
		return Download{}, ErrCapabilityInvalid
	}
	body, err := service.reader.Open(ctx, artifact)
	if err != nil {
		return Download{}, err
	}
	return Download{Body: body, Bytes: artifact.Bytes, SHA256: artifact.SHA256, ExportID: status.ID, ExpiresAt: status.ExpiresAt}, nil
}

func downloadable(status Status, artifact Artifact, now time.Time) bool {
	return status.State == StateAvailable && status.AvailableAt != nil && status.ExpiresAt.After(now) && validArtifact(artifact) &&
		status.ArtifactBytes == artifact.Bytes
}

package passkeys

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
)

const MaximumRotationBatch = 500

var ErrInvalidRotation = errors.New("passkey encryption rotation request is invalid")

type EncryptionVersionCount struct {
	Version int    `json:"version"`
	Count   uint64 `json:"count"`
}

type RotationStatus struct {
	ActiveVersion      int                      `json:"active_version"`
	CredentialVersions []EncryptionVersionCount `json:"credential_versions"`
	CeremonyVersions   []EncryptionVersionCount `json:"ceremony_versions"`
}

type RotationResult struct {
	RotationStatus
	Updated uint64 `json:"updated"`
}

type RotationStore interface {
	InspectEncryption(context.Context, RotationEvidence) (RotationStatus, error)
	ReencryptEnvelopeBatch(context.Context, int, RotationEvidence) (RotationResult, error)
}

type RotationEvidence struct {
	Actor, Reason, Environment string
	OccurredAt                 time.Time
}

type RotationService struct {
	store RotationStore
	clock RotationClock
}

type RotationClock interface{ Now() time.Time }

func NewRotationService(store RotationStore, clock RotationClock) (*RotationService, error) {
	if store == nil || clock == nil {
		return nil, ErrInvalidRotation
	}
	return &RotationService{store: store, clock: clock}, nil
}

func (s *RotationService) Inspect(ctx context.Context, actor, reason, environment string) (RotationStatus, error) {
	evidence, err := rotationEvidence(actor, reason, environment, s.clock.Now().UTC())
	if err != nil {
		return RotationStatus{}, err
	}
	return s.store.InspectEncryption(ctx, evidence)
}

func (s *RotationService) Reencrypt(ctx context.Context, batch int, actor, reason, environment string) (RotationResult, error) {
	if batch < 1 || batch > MaximumRotationBatch {
		return RotationResult{}, ErrInvalidRotation
	}
	evidence, err := rotationEvidence(actor, reason, environment, s.clock.Now().UTC())
	if err != nil {
		return RotationResult{}, err
	}
	return s.store.ReencryptEnvelopeBatch(ctx, batch, evidence)
}

var rotationEnvironment = regexp.MustCompile(`^[a-z][a-z0-9-]{0,99}$`)

func rotationEvidence(actor, reason, environment string, now time.Time) (RotationEvidence, error) {
	actor, reason, environment = strings.TrimSpace(actor), strings.TrimSpace(reason), strings.TrimSpace(environment)
	if len(actor) < 3 || len(actor) > 200 || len(reason) < 8 || len(reason) > 500 || !rotationEnvironment.MatchString(environment) || now.IsZero() {
		return RotationEvidence{}, ErrInvalidRotation
	}
	return RotationEvidence{Actor: actor, Reason: reason, Environment: environment, OccurredAt: now}, nil
}

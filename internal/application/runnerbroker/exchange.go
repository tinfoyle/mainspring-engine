package runnerbroker

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnercontrol"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	SchemaVersion         = 1
	MaximumInputBytes     = 768 << 10
	MaximumOutputBytes    = 768 << 10
	MaximumEnvelopeBytes  = 1 << 20
	MaximumCapabilities   = 32
	MaximumInvocationLife = 24 * time.Hour
)

var (
	ErrInvalidExchange  = errors.New("runner exchange is invalid")
	ErrExchangeConflict = errors.New("runner exchange conflicts with existing state")
	ErrExchangeNotReady = errors.New("runner exchange is not ready")
	ErrExchangeCanceled = errors.New("runner exchange was canceled")
	ErrExchangeExpired  = errors.New("runner exchange expired")
	ErrCapabilityDenied = errors.New("runner capability was denied")
	validKind           = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,63}$`)
	validCapability     = regexp.MustCompile(`^[a-z][a-z0-9.:/-]{0,127}$`)
	validErrorCode      = regexp.MustCompile(`^[a-z][a-z0-9_]{0,99}$`)
	validProfile        = regexp.MustCompile(`^[a-z][a-z0-9-]{0,49}$`)
)

type Request struct {
	SchemaVersion int             `json:"schema_version"`
	Kind          string          `json:"kind"`
	Input         json.RawMessage `json:"input"`
	Capabilities  []string        `json:"capabilities"`
	ExpiresAt     time.Time       `json:"expires_at"`
}

type Result struct {
	SchemaVersion int             `json:"schema_version"`
	Outcome       string          `json:"outcome"`
	Output        json.RawMessage `json:"output"`
	ErrorCode     string          `json:"error_code,omitempty"`
}

type ProvisionCommand struct {
	Invocation runnercontrol.Invocation
	Request    Request
}

type CapabilityGrant struct {
	Identity   Identity
	AccountID  ids.AccountID
	Kind       string
	Capability string
	ExpiresAt  time.Time
}

type StoredRequest struct {
	InvocationID string
	AccountID    ids.AccountID
	Profile      string
	Ciphertext   []byte
	Nonce        []byte
	KeyVersion   int
	Digest       [sha256.Size]byte
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

type StoredResult struct {
	InvocationID string
	PodUID       string
	Outcome      string
	Ciphertext   []byte
	Nonce        []byte
	KeyVersion   int
	Digest       [sha256.Size]byte
	SubmittedAt  time.Time
}

type Repository interface {
	Provision(context.Context, runnercontrol.Invocation, StoredRequest) (bool, error)
	Claim(context.Context, Identity, time.Time) (StoredRequest, error)
	Submit(context.Context, Identity, StoredResult, time.Time) (bool, error)
}

type Clock interface{ Now() time.Time }

type Cipher struct {
	keys          map[int]cipher.AEAD
	activeVersion int
}

func NewCipher(keys map[int][]byte, activeVersion int) (*Cipher, error) {
	if len(keys) == 0 || len(keys) > 20 || activeVersion <= 0 {
		return nil, ErrInvalidExchange
	}
	result := &Cipher{keys: make(map[int]cipher.AEAD, len(keys)), activeVersion: activeVersion}
	for version, key := range keys {
		if version <= 0 || len(key) != 32 {
			return nil, ErrInvalidExchange
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, err
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		result.keys[version] = aead
	}
	if _, ok := result.keys[activeVersion]; !ok {
		return nil, ErrInvalidExchange
	}
	return result, nil
}

type Service struct {
	repository Repository
	verifier   IdentityVerifier
	cipher     *Cipher
	clock      Clock
}

func NewService(repository Repository, verifier IdentityVerifier, envelopeCipher *Cipher, clock Clock) (*Service, error) {
	if repository == nil || verifier == nil || envelopeCipher == nil || clock == nil {
		return nil, ErrInvalidExchange
	}
	return &Service{repository: repository, verifier: verifier, cipher: envelopeCipher, clock: clock}, nil
}

func (s *Service) Provision(ctx context.Context, command ProvisionCommand) (bool, error) {
	now := s.clock.Now().UTC()
	invocation := command.Invocation
	if ids.Validate(invocation.ID) != nil || ids.Validate(string(invocation.AccountID)) != nil || !validProfile.MatchString(invocation.Profile) || invocation.QueuedAt.IsZero() {
		return false, ErrInvalidExchange
	}
	request, raw, err := canonicalRequest(command.Request, invocation.QueuedAt.UTC(), now)
	if err != nil {
		return false, err
	}
	ciphertext, nonce, version, err := s.cipher.seal(raw, requestAAD(invocation.ID, invocation.AccountID, invocation.Profile))
	if err != nil {
		return false, err
	}
	stored := StoredRequest{InvocationID: invocation.ID, AccountID: invocation.AccountID, Profile: invocation.Profile, Ciphertext: ciphertext, Nonce: nonce, KeyVersion: version, Digest: sha256.Sum256(raw), CreatedAt: invocation.QueuedAt.UTC(), ExpiresAt: request.ExpiresAt.UTC()}
	return s.repository.Provision(ctx, invocation, stored)
}

func (s *Service) Fetch(ctx context.Context, token, invocationID string) (Request, error) {
	_, request, _, err := s.fetchVerified(ctx, token, invocationID)
	return request, err
}

func (s *Service) AuthorizeCapability(ctx context.Context, token, invocationID, capability string) (CapabilityGrant, error) {
	if !validCapability.MatchString(capability) {
		return CapabilityGrant{}, ErrCapabilityDenied
	}
	identity, request, accountID, err := s.fetchVerified(ctx, token, invocationID)
	if err != nil {
		return CapabilityGrant{}, err
	}
	index, found := slices.BinarySearch(request.Capabilities, capability)
	if !found || index >= len(request.Capabilities) {
		return CapabilityGrant{}, ErrCapabilityDenied
	}
	return CapabilityGrant{Identity: identity, AccountID: accountID, Kind: request.Kind, Capability: capability, ExpiresAt: request.ExpiresAt}, nil
}

func (s *Service) fetchVerified(ctx context.Context, token, invocationID string) (Identity, Request, ids.AccountID, error) {
	identity, err := s.verifier.Verify(ctx, token, invocationID)
	if err != nil {
		return Identity{}, Request{}, "", err
	}
	stored, err := s.repository.Claim(ctx, identity, s.clock.Now().UTC())
	if err != nil {
		return Identity{}, Request{}, "", err
	}
	if stored.InvocationID != identity.InvocationID || stored.Profile != identity.Profile || ids.Validate(string(stored.AccountID)) != nil {
		return Identity{}, Request{}, "", ErrIdentityDenied
	}
	raw, err := s.cipher.open(stored.Ciphertext, stored.Nonce, stored.KeyVersion, requestAAD(stored.InvocationID, stored.AccountID, stored.Profile))
	if err != nil || sha256.Sum256(raw) != stored.Digest {
		return Identity{}, Request{}, "", ErrExchangeConflict
	}
	var request Request
	if err := json.Unmarshal(raw, &request); err != nil {
		return Identity{}, Request{}, "", ErrExchangeConflict
	}
	canonical, _, err := canonicalRequest(request, stored.CreatedAt, time.Time{})
	if err != nil || !canonical.ExpiresAt.Equal(stored.ExpiresAt) {
		return Identity{}, Request{}, "", ErrExchangeConflict
	}
	if !canonical.ExpiresAt.After(s.clock.Now().UTC()) {
		return Identity{}, Request{}, "", ErrExchangeExpired
	}
	return identity, canonical, stored.AccountID, nil
}

func ValidCapability(value string) bool { return validCapability.MatchString(value) }

func (s *Service) Submit(ctx context.Context, token, invocationID string, result Result) (bool, error) {
	identity, err := s.verifier.Verify(ctx, token, invocationID)
	if err != nil {
		return false, err
	}
	canonical, raw, err := canonicalResult(result)
	if err != nil {
		return false, err
	}
	ciphertext, nonce, version, err := s.cipher.seal(raw, resultAAD(identity.InvocationID, identity.PodUID))
	if err != nil {
		return false, err
	}
	now := s.clock.Now().UTC()
	stored := StoredResult{InvocationID: identity.InvocationID, PodUID: identity.PodUID, Outcome: canonical.Outcome, Ciphertext: ciphertext, Nonce: nonce, KeyVersion: version, Digest: sha256.Sum256(raw), SubmittedAt: now}
	return s.repository.Submit(ctx, identity, stored, now)
}

// DecodeResult opens a stored result only when its authenticated invocation
// and Pod binding, canonical envelope, declared outcome, and plaintext digest
// all agree. It is intended for trusted projection workers, not runners or
// broker HTTP handlers.
func (c *Cipher) DecodeResult(stored StoredResult) (Result, error) {
	if ids.Validate(stored.InvocationID) != nil || ids.Validate(stored.PodUID) != nil || stored.SubmittedAt.IsZero() {
		return Result{}, ErrExchangeConflict
	}
	raw, err := c.open(stored.Ciphertext, stored.Nonce, stored.KeyVersion, resultAAD(stored.InvocationID, stored.PodUID))
	if err != nil || sha256.Sum256(raw) != stored.Digest {
		return Result{}, ErrExchangeConflict
	}
	var result Result
	if err := json.Unmarshal(raw, &result); err != nil {
		return Result{}, ErrExchangeConflict
	}
	canonical, canonicalRaw, err := canonicalResult(result)
	if err != nil || canonical.Outcome != stored.Outcome || !slices.Equal(canonicalRaw, raw) {
		return Result{}, ErrExchangeConflict
	}
	return canonical, nil
}

func (c *Cipher) seal(plaintext, aad []byte) ([]byte, []byte, int, error) {
	aead := c.keys[c.activeVersion]
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, 0, err
	}
	return aead.Seal(nil, nonce, plaintext, aad), nonce, c.activeVersion, nil
}

func (c *Cipher) open(ciphertext, nonce []byte, version int, aad []byte) ([]byte, error) {
	aead, ok := c.keys[version]
	if !ok || len(nonce) != aead.NonceSize() || len(ciphertext) < aead.Overhead() || len(ciphertext) > MaximumEnvelopeBytes+aead.Overhead() {
		return nil, ErrExchangeConflict
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, ErrExchangeConflict
	}
	return plaintext, nil
}

func canonicalRequest(request Request, createdAt, now time.Time) (Request, []byte, error) {
	if request.SchemaVersion != SchemaVersion || !validKind.MatchString(request.Kind) || len(request.Input) > MaximumInputBytes || !validJSONObject(request.Input) || len(request.Capabilities) > MaximumCapabilities || request.ExpiresAt.IsZero() || !request.ExpiresAt.After(createdAt) || request.ExpiresAt.After(createdAt.Add(MaximumInvocationLife)) || (!now.IsZero() && !request.ExpiresAt.After(now)) {
		return Request{}, nil, ErrInvalidExchange
	}
	capabilities := append([]string(nil), request.Capabilities...)
	slices.Sort(capabilities)
	for index, capability := range capabilities {
		if !validCapability.MatchString(capability) || (index > 0 && capability == capabilities[index-1]) {
			return Request{}, nil, ErrInvalidExchange
		}
	}
	request.Capabilities = capabilities
	request.ExpiresAt = request.ExpiresAt.UTC()
	raw, err := json.Marshal(request)
	if err != nil || len(raw) > MaximumEnvelopeBytes {
		return Request{}, nil, ErrInvalidExchange
	}
	return request, raw, nil
}

func canonicalResult(result Result) (Result, []byte, error) {
	if result.SchemaVersion != SchemaVersion || len(result.Output) > MaximumOutputBytes || !validJSONObject(result.Output) {
		return Result{}, nil, ErrInvalidExchange
	}
	switch result.Outcome {
	case "completed":
		if result.ErrorCode != "" {
			return Result{}, nil, ErrInvalidExchange
		}
	case "execution_failed":
		if !validErrorCode.MatchString(result.ErrorCode) {
			return Result{}, nil, ErrInvalidExchange
		}
	default:
		return Result{}, nil, ErrInvalidExchange
	}
	raw, err := json.Marshal(result)
	if err != nil || len(raw) > MaximumEnvelopeBytes {
		return Result{}, nil, ErrInvalidExchange
	}
	return result, raw, nil
}

func validJSONObject(raw json.RawMessage) bool {
	if len(raw) == 0 || raw[0] != '{' {
		return false
	}
	var value map[string]json.RawMessage
	return json.Unmarshal(raw, &value) == nil && value != nil
}

func requestAAD(invocationID string, accountID ids.AccountID, profile string) []byte {
	return []byte("spyglass/runner-request/v1/" + invocationID + "/" + string(accountID) + "/" + profile)
}

func resultAAD(invocationID, podUID string) []byte {
	return []byte("spyglass/runner-result/v1/" + invocationID + "/" + podUID)
}

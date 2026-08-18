// Package recoverycodes owns system-wide, single-use recovery codes for
// replacing a lost passkey. A recovery grant is bound to one authenticated
// User session and never grants Account or operator authority.
package recoverycodes

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	CodeCount                 = 10
	CodeBytes                 = 16
	GrantTTL                  = 10 * time.Minute
	maxCodeGenerationAttempts = CodeCount * 4
)

var (
	ErrInvalidCode      = errors.New("recovery code is invalid or already used")
	ErrPasswordRequired = errors.New("recent password authentication is required")
	ErrInvalidOperation = errors.New("recovery code operation is invalid")
)

type CodeHash [sha256.Size]byte

type Set struct {
	ID        ids.RecoveryCodeSetID
	UserID    ids.UserID
	Version   uint64
	Hashes    []CodeHash
	CreatedAt time.Time
}

type Status struct {
	Configured bool      `json:"configured"`
	Version    uint64    `json:"version,omitempty"`
	Remaining  int       `json:"remaining"`
	CreatedAt  time.Time `json:"created_at,omitempty"`
}

type Rotation struct {
	Status Status   `json:"status"`
	Codes  []string `json:"codes"`
}

type Store interface {
	Rotate(context.Context, Set) (Status, error)
	Status(context.Context, ids.UserID) (Status, error)
	Consume(context.Context, ids.UserID, ids.SessionID, CodeHash, time.Time, time.Time) (bool, error)
	Granted(context.Context, ids.UserID, ids.SessionID, time.Time) (bool, error)
}

type CodeGenerator interface{ Generate() (string, error) }
type Clock interface{ Now() time.Time }

type Service struct {
	store Store
	ids   ids.Generator
	codes CodeGenerator
	clock Clock
}

func NewService(store Store, idGenerator ids.Generator, codeGenerator CodeGenerator, clock Clock) (*Service, error) {
	if store == nil || idGenerator == nil || codeGenerator == nil || clock == nil {
		return nil, ErrInvalidOperation
	}
	return &Service{store: store, ids: idGenerator, codes: codeGenerator, clock: clock}, nil
}

func (s *Service) Status(ctx context.Context, session sessions.Session) (Status, error) {
	if session.UserID == "" || session.ID == "" {
		return Status{}, ErrInvalidOperation
	}
	return s.store.Status(ctx, session.UserID)
}

func (s *Service) Rotate(ctx context.Context, session sessions.Session) (Rotation, error) {
	now := s.clock.Now().UTC()
	if err := strongauth.Require(session, session.UserID, now); err != nil {
		return Rotation{}, err
	}
	codes := make([]string, 0, CodeCount)
	hashes := make([]CodeHash, 0, CodeCount)
	seen := map[CodeHash]struct{}{}
	for attempts := 0; len(codes) < CodeCount && attempts < maxCodeGenerationAttempts; attempts++ {
		code, err := s.codes.Generate()
		if err != nil {
			return Rotation{}, err
		}
		normalized, err := normalize(code)
		if err != nil {
			return Rotation{}, err
		}
		hash := hashCode(normalized)
		if _, exists := seen[hash]; exists {
			continue
		}
		seen[hash] = struct{}{}
		codes = append(codes, format(normalized))
		hashes = append(hashes, hash)
	}
	if len(codes) != CodeCount {
		return Rotation{}, ErrInvalidOperation
	}
	setID := s.ids.New()
	if ids.Validate(setID) != nil {
		return Rotation{}, ErrInvalidOperation
	}
	status, err := s.store.Rotate(ctx, Set{ID: ids.RecoveryCodeSetID(setID), UserID: session.UserID, Hashes: hashes, CreatedAt: now})
	if err != nil {
		return Rotation{}, err
	}
	return Rotation{Status: status, Codes: codes}, nil
}

func (s *Service) Consume(ctx context.Context, session sessions.Session, code string) error {
	now := s.clock.Now().UTC()
	if session.UserID == "" || session.ID == "" ||
		!sessions.RecentlyReauthenticatedWithAssurance(session, now, strongauth.MaximumAge, sessions.AssuranceSingleFactor) {
		return ErrPasswordRequired
	}
	normalized, err := normalize(code)
	if err != nil {
		return ErrInvalidCode
	}
	consumed, err := s.store.Consume(ctx, session.UserID, session.ID, hashCode(normalized), now, now.Add(GrantTTL))
	if err != nil {
		return err
	}
	if !consumed {
		return ErrInvalidCode
	}
	return nil
}

func (s *Service) Granted(ctx context.Context, session sessions.Session) (bool, error) {
	if session.UserID == "" || session.ID == "" {
		return false, ErrInvalidOperation
	}
	return s.store.Granted(ctx, session.UserID, session.ID, s.clock.Now().UTC())
}

type RandomGenerator struct{}

func (RandomGenerator) Generate() (string, error) {
	raw := make([]byte, CodeBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func normalize(value string) (string, error) {
	value = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "-", ""))
	if len(value) != CodeBytes*2 {
		return "", ErrInvalidCode
	}
	if _, err := hex.DecodeString(value); err != nil {
		return "", ErrInvalidCode
	}
	return value, nil
}

func format(value string) string {
	parts := make([]string, 0, len(value)/4)
	for index := 0; index < len(value); index += 4 {
		parts = append(parts, value[index:index+4])
	}
	return strings.Join(parts, "-")
}

func hashCode(normalized string) CodeHash {
	return sha256.Sum256([]byte("spyglass-recovery-code-v1\x00" + normalized))
}

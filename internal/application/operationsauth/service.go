package operationsauth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/passkeys"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var ErrDenied = errors.New("admin authentication was not accepted")
var ErrLimited = errors.New("too many attempts; wait fifteen minutes and try again")

type Challenge struct {
	Hash                          [32]byte
	UserID                        ids.UserID
	SecurityVersion, StaffVersion uint64
	ExpiresAt                     time.Time
	GoogleAt                      *time.Time
	Pending                       passkeys.Envelope
	RecoveryOnly                  bool
	Consumed                      bool
	DisplayName                   string
	NewSession                    *sessions.Session
}
type Credential struct {
	RecoveryLocked bool
	UserID         ids.UserID
	Secret         passkeys.Envelope
	LastStep       int64
	RecoveryHashes [][]byte
	Failed         int
	WindowStart    time.Time
}
type Repository interface {
	Begin(context.Context, [32]byte, time.Time) error
	Bind(context.Context, [32]byte, string, time.Time) error
	Load(context.Context, [32]byte, time.Time) (Challenge, Credential, error)
	// Update serializes attempts for the User across all login challenges. Denials
	// still commit the attempt count and audit; all other failures roll back.
	Update(context.Context, [32]byte, time.Time, func(*Challenge, *Credential) (string, error)) error
	Reauthenticate(context.Context, ids.UserID, ids.SessionID, time.Time, func(*Credential) error) error
}
type Service struct {
	repo     Repository
	cipher   *passkeys.Cipher
	sessions *sessions.Service
	clock    sessions.Clock
	audience string
}

func New(repo Repository, cipher *passkeys.Cipher, sessionService *sessions.Service, clock sessions.Clock, audience string) (*Service, error) {
	if repo == nil || cipher == nil || sessionService == nil || clock == nil || audience == "" {
		return nil, ErrDenied
	}
	return &Service{repo, cipher, sessionService, clock, audience}, nil
}
func (s *Service) Begin(ctx context.Context) (string, error) {
	token, err := RandomToken()
	if err != nil {
		return "", err
	}
	return token, s.repo.Begin(ctx, Hash(token), s.clock.Now().UTC())
}
func (s *Service) Google(ctx context.Context, token, raw string) error {
	t, err := OpenTicket(s.cipher, raw, s.audience, token, s.clock.Now().UTC())
	if err != nil {
		return err
	}
	return s.repo.Bind(ctx, Hash(token), t.Identifier, s.clock.Now().UTC())
}

type Status struct {
	Stage       string `json:"stage"`
	DisplayName string `json:"display_name"`
}

func (s *Service) Status(ctx context.Context, token string) (Status, error) {
	if !ValidToken(token) {
		return Status{}, ErrDenied
	}
	c, k, err := s.repo.Load(ctx, Hash(token), s.clock.Now().UTC())
	if err != nil {
		return Status{}, err
	}
	stage := "code"
	if len(k.Secret.Ciphertext) == 0 || c.RecoveryOnly {
		stage = "enroll"
	}
	return Status{stage, c.DisplayName}, nil
}

type Setup struct {
	Secret string `json:"secret"`
	URI    string `json:"uri"`
}

func (s *Service) Setup(ctx context.Context, token string) (Setup, error) {
	if !ValidToken(token) {
		return Setup{}, ErrDenied
	}
	var out Setup
	err := s.repo.Update(ctx, Hash(token), s.clock.Now().UTC(), func(c *Challenge, k *Credential) (string, error) {
		if len(k.Secret.Ciphertext) > 0 && !c.RecoveryOnly {
			return "", ErrDenied
		}
		if len(c.Pending.Ciphertext) == 0 {
			secret := make([]byte, 20)
			if _, err := rand.Read(secret); err != nil {
				return "", err
			}
			defer clear(secret)
			e, err := s.cipher.Seal("totp/"+string(c.UserID), secret)
			if err != nil {
				return "", err
			}
			c.Pending = e
		}
		secret, err := s.cipher.Open("totp/"+string(c.UserID), c.Pending)
		if err != nil {
			return "", err
		}
		defer clear(secret)
		out.Secret, out.URI = SetupURI(secret, c.DisplayName)
		return "enrollment_started", nil
	})
	return out, err
}

type Result struct {
	Issued        *sessions.Issued
	RecoveryCodes []string
	RecoveryOnly  bool
}

func allowAttempt(k *Credential, now time.Time) error {
	if !now.Before(k.WindowStart.Add(15 * time.Minute)) {
		k.WindowStart = now
		k.Failed = 0
	}
	if k.Failed >= 5 {
		return ErrLimited
	}
	k.Failed++
	return nil
}
func (s *Service) Verify(ctx context.Context, token, code string, recovery bool) (Result, error) {
	if !ValidToken(token) {
		return Result{}, ErrDenied
	}
	now := s.clock.Now().UTC()
	var out Result

	err := s.repo.Update(ctx, Hash(token), now, func(c *Challenge, k *Credential) (string, error) {
		if err := allowAttempt(k, now); err != nil {
			return "", err
		}
		if recovery {
			if len(k.Secret.Ciphertext) == 0 || c.RecoveryOnly {
				return "", ErrDenied
			}
			hash := RecoveryHash(code)
			for i, saved := range k.RecoveryHashes {
				if subtle.ConstantTimeCompare(hash, saved) == 1 {
					k.RecoveryHashes = append(k.RecoveryHashes[:i], k.RecoveryHashes[i+1:]...)
					c.RecoveryOnly = true
					k.RecoveryLocked = true
					c.Pending = passkeys.Envelope{}
					k.Failed = 0
					out.RecoveryOnly = true
					return "recovery_used", nil
				}
			}
			return "", ErrDenied
		}
		if k.RecoveryLocked && !c.RecoveryOnly {
			return "", ErrDenied
		}
		enrolling := len(k.Secret.Ciphertext) == 0 || c.RecoveryOnly
		envelope := k.Secret
		if enrolling {
			envelope = c.Pending
		}
		if len(envelope.Ciphertext) == 0 {
			return "", ErrDenied
		}
		secret, err := s.cipher.Open("totp/"+string(c.UserID), envelope)
		if err != nil {
			return "", err
		}
		defer clear(secret)
		last := k.LastStep
		if enrolling {
			last = -1
		}
		step, ok := Match(secret, code, now, last)
		if !ok {
			return "", ErrDenied
		}
		action := "code_verified"
		if enrolling {
			codes, hashes, err := recoveryCodes()
			if err != nil {
				return "", err
			}
			out.RecoveryCodes = codes
			k.RecoveryHashes = hashes
			k.Secret = c.Pending
			k.RecoveryLocked = false
			action = "authenticator_enrolled"
		}
		k.LastStep = step
		k.Failed = 0
		c.Consumed = true
		issued, err := s.sessions.PrepareForClientWithMethod(c.UserID, c.SecurityVersion, "Admin console", sessions.AuthenticationMethodGoogleTOTP)
		if err != nil {
			return "", err
		}
		out.Issued = &issued
		c.NewSession = &issued.Session
		return action, nil
	})
	if err != nil {
		return Result{}, err
	}
	if out.RecoveryOnly {
		return out, nil
	}

	return out, nil
}
func (s *Service) Reauthenticate(ctx context.Context, userID ids.UserID, sessionID ids.SessionID, code string) error {
	now := s.clock.Now().UTC()
	return s.repo.Reauthenticate(ctx, userID, sessionID, now, func(k *Credential) error {
		if k.RecoveryLocked {
			return ErrDenied
		}
		if err := allowAttempt(k, now); err != nil {
			return err
		}
		secret, err := s.cipher.Open("totp/"+string(userID), k.Secret)
		if err != nil {
			return err
		}
		defer clear(secret)
		step, ok := Match(secret, code, now, k.LastStep)
		if !ok {
			return ErrDenied
		}
		k.LastStep = step
		k.Failed = 0
		return nil
	})
}

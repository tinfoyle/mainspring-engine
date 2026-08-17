package authentication_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/memory"
	"github.com/tinfoyle/spyglass-engine/internal/application/authentication"
	"github.com/tinfoyle/spyglass-engine/internal/modules/identity"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/authn"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type identitySource struct {
	value authentication.LocalIdentity
	err   error
}

func (s identitySource) LocalIdentity(context.Context, string) (authentication.LocalIdentity, error) {
	return s.value, s.err
}

type limiter struct {
	attempts int
	blocked  bool
}

func (l *limiter) Blocked(context.Context, [32]byte, time.Time) (bool, error) { return l.blocked, nil }
func (l *limiter) Failure(context.Context, [32]byte, time.Time, int, time.Duration) error {
	l.attempts++
	return nil
}
func (l *limiter) Success(context.Context, [32]byte) error { l.attempts = 0; return nil }

type clock struct{ now time.Time }

func (c clock) Now() time.Time { return c.now }

type generator struct{ n int }

func (g *generator) New() string { g.n++; return "00000000-0000-4000-8000-000000000001" }

func TestLoginIssuesSessionAndUsesGenericFailures(t *testing.T) {
	passwords := authn.Passwords{}
	hash, err := passwords.Hash("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	dummy, err := passwords.Hash("dummy password material")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	sessionService, err := sessions.NewService(memory.NewSessionStore(), &generator{}, clock{now}, 24*time.Hour, time.Hour, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	attempts := &limiter{}
	service, err := authentication.NewService(identitySource{value: authentication.LocalIdentity{User: identity.User{ID: ids.UserID("user-a"), State: identity.UserActive, SecurityVersion: 1}, PasswordHash: hash}}, attempts, passwords, sessionService, clock{now}, dummy)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := service.Login(context.Background(), authentication.LoginCommand{Email: "owner@example.com", Password: "correct horse battery staple"})
	if err != nil || issued.Token == "" {
		t.Fatalf("login failed: %v", err)
	}
	_, err = service.Login(context.Background(), authentication.LoginCommand{Email: "owner@example.com", Password: "wrong password material"})
	if !errors.Is(err, authentication.ErrInvalidCredentials) || attempts.attempts != 1 {
		t.Fatalf("unexpected failure: attempts=%d err=%v", attempts.attempts, err)
	}
	attempts.blocked = true
	_, err = service.Login(context.Background(), authentication.LoginCommand{Email: "owner@example.com", Password: "wrong password material"})
	if !errors.Is(err, authentication.ErrInvalidCredentials) || attempts.attempts != 1 {
		t.Fatalf("blocked attempts must not extend their own lock: attempts=%d err=%v", attempts.attempts, err)
	}
}

func TestUnknownIdentityStillUsesFailureLimiter(t *testing.T) {
	passwords := authn.Passwords{}
	dummy, err := passwords.Hash("dummy password material")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	sessionService, err := sessions.NewService(memory.NewSessionStore(), &generator{}, clock{now}, time.Hour, 30*time.Minute, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	attempts := &limiter{}
	service, err := authentication.NewService(identitySource{err: authentication.ErrIdentityNotFound}, attempts, passwords, sessionService, clock{now}, dummy)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Login(context.Background(), authentication.LoginCommand{Email: "missing@example.com", Password: "some password material"})
	if !errors.Is(err, authentication.ErrInvalidCredentials) || attempts.attempts != 1 {
		t.Fatalf("unexpected result: attempts=%d err=%v", attempts.attempts, err)
	}
}

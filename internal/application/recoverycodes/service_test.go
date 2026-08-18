package recoverycodes_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/recoverycodes"
	"github.com/tinfoyle/spyglass-engine/internal/application/strongauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type store struct {
	set     recoverycodes.Set
	used    map[recoverycodes.CodeHash]bool
	granted map[ids.SessionID]time.Time
}

func (s *store) Rotate(_ context.Context, set recoverycodes.Set) (recoverycodes.Status, error) {
	set.Version = s.set.Version + 1
	s.set = set
	s.used = map[recoverycodes.CodeHash]bool{}
	s.granted = map[ids.SessionID]time.Time{}
	return recoverycodes.Status{Configured: true, Version: set.Version, Remaining: len(set.Hashes), CreatedAt: set.CreatedAt}, nil
}
func (s *store) Status(context.Context, ids.UserID) (recoverycodes.Status, error) {
	remaining := 0
	for _, hash := range s.set.Hashes {
		if !s.used[hash] {
			remaining++
		}
	}
	return recoverycodes.Status{Configured: s.set.ID != "", Version: s.set.Version, Remaining: remaining, CreatedAt: s.set.CreatedAt}, nil
}
func (s *store) Consume(_ context.Context, userID ids.UserID, sessionID ids.SessionID, hash recoverycodes.CodeHash, _ time.Time, expires time.Time) (bool, error) {
	if userID != s.set.UserID || s.used[hash] {
		return false, nil
	}
	for _, candidate := range s.set.Hashes {
		if candidate == hash {
			s.used[hash] = true
			s.granted[sessionID] = expires
			return true, nil
		}
	}
	return false, nil
}
func (s *store) Granted(_ context.Context, userID ids.UserID, sessionID ids.SessionID, now time.Time) (bool, error) {
	return userID == s.set.UserID && s.granted[sessionID].After(now), nil
}

type codeSequence struct{ next int }

func (s *codeSequence) Generate() (string, error) {
	s.next++
	return fmt.Sprintf("%032x", s.next), nil
}

type fixedCode string

func (c fixedCode) Generate() (string, error) { return string(c), nil }

type fixedID string

func (i fixedID) New() string { return string(i) }

type idSequence struct{ next int }

func (s *idSequence) New() string {
	s.next++
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", s.next)
}

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

func TestRecoveryCodesRequirePasskeyRotationAndPasswordConsumption(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	userID := ids.UserID("11111111-1111-4111-8111-111111111111")
	sessionID := ids.SessionID("22222222-2222-4222-8222-222222222222")
	repository := &store{}
	service, err := recoverycodes.NewService(repository, &idSequence{}, &codeSequence{}, &clock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	password := sessions.Session{ID: sessionID, UserID: userID, ReauthenticatedAt: now, ReauthenticationMethod: sessions.AuthenticationMethodPassword}
	if _, err := service.Rotate(context.Background(), password); !errors.Is(err, strongauth.ErrRequired) {
		t.Fatalf("password-only rotation=%v", err)
	}
	passkey := password
	passkey.ReauthenticationMethod = sessions.AuthenticationMethodPasskey
	rotation, err := service.Rotate(context.Background(), passkey)
	if err != nil || len(rotation.Codes) != recoverycodes.CodeCount || rotation.Status.Remaining != recoverycodes.CodeCount {
		t.Fatalf("rotation=%+v err=%v", rotation, err)
	}
	if len(rotation.Codes[0]) != 39 || rotation.Codes[0] == rotation.Codes[1] {
		t.Fatalf("recovery code format/uniqueness=%q %q", rotation.Codes[0], rotation.Codes[1])
	}
	if err := service.Consume(context.Background(), passkey, rotation.Codes[0]); !errors.Is(err, recoverycodes.ErrPasswordRequired) {
		t.Fatalf("passkey consumption=%v", err)
	}
	if err := service.Consume(context.Background(), password, rotation.Codes[0]); err != nil {
		t.Fatal(err)
	}
	if err := service.Consume(context.Background(), password, rotation.Codes[0]); !errors.Is(err, recoverycodes.ErrInvalidCode) {
		t.Fatalf("replayed code=%v", err)
	}
	granted, err := service.Granted(context.Background(), password)
	if err != nil || !granted {
		t.Fatalf("grant=%v err=%v", granted, err)
	}
	status, err := service.Status(context.Background(), password)
	if err != nil || status.Remaining != recoverycodes.CodeCount-1 {
		t.Fatalf("status=%+v err=%v", status, err)
	}
}

func TestRecoveryCodesRejectMalformedAndStaleEvidence(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	service, _ := recoverycodes.NewService(&store{}, &idSequence{}, &codeSequence{}, &clock{now: now})
	stale := sessions.Session{ID: "22222222-2222-4222-8222-222222222222", UserID: "11111111-1111-4111-8111-111111111111", ReauthenticatedAt: now.Add(-strongauth.MaximumAge - time.Second), ReauthenticationMethod: sessions.AuthenticationMethodPassword}
	if err := service.Consume(context.Background(), stale, "not-a-code"); !errors.Is(err, recoverycodes.ErrPasswordRequired) {
		t.Fatalf("stale evidence=%v", err)
	}
	stale.ReauthenticatedAt = now
	if err := service.Consume(context.Background(), stale, "not-a-code"); !errors.Is(err, recoverycodes.ErrInvalidCode) {
		t.Fatalf("malformed code=%v", err)
	}
}

func TestRecoveryCodeRotationRejectsBrokenGeneratorsWithoutWriting(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	passkey := sessions.Session{
		ID:                     "22222222-2222-4222-8222-222222222222",
		UserID:                 "11111111-1111-4111-8111-111111111111",
		ReauthenticatedAt:      now,
		ReauthenticationMethod: sessions.AuthenticationMethodPasskey,
	}
	repository := &store{}
	duplicates, _ := recoverycodes.NewService(repository, &idSequence{}, fixedCode("00000000000000000000000000000001"), &clock{now: now})
	if _, err := duplicates.Rotate(context.Background(), passkey); !errors.Is(err, recoverycodes.ErrInvalidOperation) {
		t.Fatalf("duplicate generator rotation=%v", err)
	}
	if repository.set.ID != "" {
		t.Fatal("duplicate generator wrote a partial recovery-code set")
	}

	invalidID, _ := recoverycodes.NewService(repository, fixedID("not-a-uuid"), &codeSequence{}, &clock{now: now})
	if _, err := invalidID.Rotate(context.Background(), passkey); !errors.Is(err, recoverycodes.ErrInvalidOperation) {
		t.Fatalf("invalid ID generator rotation=%v", err)
	}
	if repository.set.ID != "" {
		t.Fatal("invalid ID generator wrote a recovery-code set")
	}
}

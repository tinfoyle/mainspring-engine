package multifactor_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/memory"
	"github.com/tinfoyle/spyglass-engine/internal/application/multifactor"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type store struct {
	recipient multifactor.Recipient
	challenge multifactor.Challenge
	method    multifactor.Method
}

func (s *store) Recipient(context.Context, ids.UserID) (multifactor.Recipient, error) {
	return s.recipient, nil
}
func (s *store) CreateChallenge(_ context.Context, value multifactor.Challenge) error {
	s.challenge = value
	return nil
}
func (s *store) CancelChallenge(context.Context, string, ids.UserID, ids.SessionID) error { return nil }
func (s *store) CompleteChallenge(_ context.Context, challengeID string, _ ids.UserID, _ ids.SessionID, supplied [32]byte, now time.Time) (multifactor.Method, multifactor.Purpose, multifactor.Kind, bool, error) {
	if challengeID != s.challenge.ID || !multifactor.EqualHash(s.challenge.CodeHash, supplied) {
		return multifactor.Method{}, s.challenge.Purpose, s.challenge.Kind, false, nil
	}
	s.method = multifactor.Method{ID: s.challenge.ResultMethodID, Kind: s.challenge.Kind, DestinationHint: s.challenge.DestinationHint, CreatedAt: now, LastUsedAt: &now}
	return s.method, s.challenge.Purpose, s.challenge.Kind, true, nil
}
func (s *store) Methods(context.Context, ids.UserID) ([]multifactor.Method, error) {
	return []multifactor.Method{s.method}, nil
}
func (s *store) MethodDestination(context.Context, ids.UserID, string) (multifactor.Method, multifactor.Envelope, error) {
	return s.method, s.challenge.Destination, nil
}

type sender struct{ message multifactor.Message }

func (s *sender) SendMultifactor(_ context.Context, value multifactor.Message) error {
	s.message = value
	return nil
}

type generator struct{ values []string }

func (g *generator) New() string {
	value := g.values[0]
	g.values = g.values[1:]
	return value
}

type clock struct{ now time.Time }

func (c clock) Now() time.Time { return c.now }

func TestSMSEnrollmentVerifiesOwnershipAndMarksStrongReauthentication(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	userID := ids.UserID("10000000-0000-4000-8000-000000000001")
	sessionID := ids.SessionID("20000000-0000-4000-8000-000000000002")
	session := sessions.Session{ID: sessionID, UserID: userID, SecurityVersion: 1, AuthenticatedAt: now, ReauthenticatedAt: now, LastSeenAt: now, RotatedAt: now, ExpiresAt: now.Add(time.Hour), AuthenticationMethod: sessions.AuthenticationMethodPassword, ReauthenticationMethod: sessions.AuthenticationMethodPassword}
	sessionStore := memory.NewSessionStore()
	if err := sessionStore.Create(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	sessionService, err := sessions.NewService(sessionStore, &generator{values: []string{"50000000-0000-4000-8000-000000000005"}}, clock{now}, 24*time.Hour, time.Hour, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	destinationCipher, err := multifactor.NewCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	repository := &store{recipient: multifactor.Recipient{Email: "owner@example.com", DisplayName: "Owner"}}
	delivery := &sender{}
	service, err := multifactor.NewService(repository, delivery, sessionService, destinationCipher, &generator{values: []string{"30000000-0000-4000-8000-000000000003", "40000000-0000-4000-8000-000000000004"}}, clock{now}, true)
	if err != nil {
		t.Fatal(err)
	}

	started, err := service.BeginEnrollment(context.Background(), session, multifactor.KindSMS, "(202) 555-0199")
	if err != nil {
		t.Fatal(err)
	}
	if delivery.message.Destination != "+12025550199" || delivery.message.Code == "" || started.DevelopmentCode != delivery.message.Code || started.DestinationHint != "phone ending in 0199" {
		t.Fatalf("unexpected delivery or response: message=%+v result=%+v", delivery.message, started)
	}
	if bytes.Contains(repository.challenge.Destination.Ciphertext, []byte(delivery.message.Destination)) {
		t.Fatal("stored SMS destination contains plaintext")
	}
	if _, err := service.Complete(context.Background(), session, started.ChallengeID, "00000x"); err != multifactor.ErrInvalidChallenge {
		t.Fatalf("invalid code error=%v", err)
	}
	method, err := service.Complete(context.Background(), session, started.ChallengeID, delivery.message.Code)
	if err != nil || method.Kind != multifactor.KindSMS {
		t.Fatalf("method=%+v err=%v", method, err)
	}
	active, err := sessionService.Active(context.Background(), userID, sessionID)
	if err != nil || len(active) != 1 || active[0].ReauthenticationMethod != sessions.AuthenticationMethodSMSOTP || active[0].ReauthenticationAssurance != sessions.AssuranceMultiFactor {
		t.Fatalf("active sessions=%+v err=%v", active, err)
	}
}

func TestPhoneNormalizationRejectsAmbiguousInput(t *testing.T) {
	for input, expected := range map[string]string{"2025550199": "+12025550199", "1-202-555-0199": "+12025550199", "+44 20 7946 0958": "+442079460958"} {
		actual, err := multifactor.NormalizePhone(input)
		if err != nil || actual != expected {
			t.Fatalf("NormalizePhone(%q)=%q,%v want %q", input, actual, err, expected)
		}
	}
	for _, input := range []string{"", "555-0199", "+0123456789", "+1 call me"} {
		if _, err := multifactor.NormalizePhone(input); err != multifactor.ErrInvalidRequest {
			t.Fatalf("NormalizePhone(%q) error=%v", input, err)
		}
	}
}

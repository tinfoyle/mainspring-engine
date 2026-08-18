package passkeys_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/passkeys"
)

type rotationStore struct {
	inspected, reencrypted bool
}

func (s *rotationStore) InspectEncryption(_ context.Context, evidence passkeys.RotationEvidence) (passkeys.RotationStatus, error) {
	s.inspected = evidence.Actor == "security@example.com"
	return passkeys.RotationStatus{ActiveVersion: 2}, nil
}

func (s *rotationStore) ReencryptEnvelopeBatch(_ context.Context, batch int, evidence passkeys.RotationEvidence) (passkeys.RotationResult, error) {
	s.reencrypted = batch == 100 && evidence.Environment == "production"
	return passkeys.RotationResult{Updated: 4}, nil
}

func TestRotationServiceRequiresBoundedAttributedOperations(t *testing.T) {
	store := &rotationStore{}
	service, err := passkeys.NewRotationService(store, &testClock{now: time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Inspect(context.Background(), "security@example.com", "inspect key migration state", "production"); err != nil || !store.inspected {
		t.Fatalf("inspect err=%v called=%v", err, store.inspected)
	}
	if result, err := service.Reencrypt(context.Background(), 100, "security@example.com", "rotate passkey envelope key", "production"); err != nil || result.Updated != 4 || !store.reencrypted {
		t.Fatalf("reencrypt=%+v err=%v called=%v", result, err, store.reencrypted)
	}
	for name, value := range map[string]struct {
		batch                      int
		actor, reason, environment string
	}{
		"zero batch":      {0, "security@example.com", "rotate passkey envelope key", "production"},
		"large batch":     {passkeys.MaximumRotationBatch + 1, "security@example.com", "rotate passkey envelope key", "production"},
		"missing actor":   {1, "", "rotate passkey envelope key", "production"},
		"short reason":    {1, "security@example.com", "rotate", "production"},
		"bad environment": {1, "security@example.com", "rotate passkey envelope key", "Production"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.Reencrypt(context.Background(), value.batch, value.actor, value.reason, value.environment); !errors.Is(err, passkeys.ErrInvalidRotation) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

package privacytoken_test

import (
	"errors"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/privacytoken"
	"github.com/tinfoyle/spyglass-engine/internal/modules/privacy"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestTokenBindsSubjectPolicyAndSurface(t *testing.T) {
	signer, _ := privacytoken.New([]byte("0123456789abcdef0123456789abcdef"))
	claims := privacytoken.Claims{SubjectID: ids.ConsentSubjectID("10000000-0000-4000-8000-000000000001"), PolicyVersion: 3, Surface: privacy.SurfacePublic}
	token, err := signer.Sign(claims)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := signer.Verify(token, privacy.SurfacePublic)
	if err != nil || verified != claims {
		t.Fatalf("verified=%+v err=%v", verified, err)
	}
	if _, err := signer.Verify(token, privacy.SurfacePrivate); !errors.Is(err, privacytoken.ErrInvalid) {
		t.Fatalf("cross-surface verification returned %v", err)
	}
	if _, err := signer.Verify(token+"x", privacy.SurfacePublic); !errors.Is(err, privacytoken.ErrInvalid) {
		t.Fatalf("tampered verification returned %v", err)
	}
}

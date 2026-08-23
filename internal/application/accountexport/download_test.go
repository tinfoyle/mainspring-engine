package accountexport

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/exportcapability"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type downloadStoreStub struct {
	status   Status
	artifact Artifact
}

func (store *downloadStoreStub) GetArtifact(context.Context, ids.AccountID, string) (Status, Artifact, error) {
	return store.status, store.artifact, nil
}

type downloadReaderStub struct{ opened bool }

func (reader *downloadReaderStub) Open(context.Context, Artifact) (io.ReadCloser, error) {
	reader.opened = true
	return io.NopCloser(strings.NewReader("zip")), nil
}

func TestDownloadCapabilityIsStronglyAuthenticatedAndBoundToCurrentArtifactState(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	accountID := ids.AccountID("10000000-0000-4000-8000-000000000001")
	userID := ids.UserID("20000000-0000-4000-8000-000000000002")
	exportID := "30000000-0000-4000-8000-000000000003"
	digest := sha256.Sum256([]byte("zip"))
	availableAt := now.Add(-time.Minute)
	store := &downloadStoreStub{status: Status{ID: exportID, AccountID: accountID, RequestedBy: userID, State: StateAvailable, ArtifactBytes: 3, Version: 4, AvailableAt: &availableAt, ExpiresAt: now.Add(time.Hour)}, artifact: Artifact{Reference: "s3-export-v1.ref", SHA256: digest, Bytes: 3}}
	reader := &downloadReaderStub{}
	key := []byte(strings.Repeat("k", exportcapability.MinimumKeyBytes))
	clock := exportClock{now: now}
	signer, _ := exportcapability.NewSigner("https://app.example.test", "1", key, time.Minute, clock)
	verifier, _ := exportcapability.NewVerifier("https://app.example.test", map[string][]byte{"1": key}, exportcapability.MaximumLifetime, time.Second, clock)
	authorizer := &exportAuthorizer{}
	service, err := NewDownloadService(store, reader, authorizer, signer, verifier, clock)
	if err != nil {
		t.Fatal(err)
	}
	session := sessions.Session{UserID: userID, ReauthenticationMethod: sessions.AuthenticationMethodPasskey, ReauthenticatedAt: now.Add(-time.Minute)}
	capability, err := service.Issue(context.Background(), CapabilityCommand{AccountID: accountID, Actor: userID, ExportID: exportID, Session: session})
	if err != nil || capability.Token == "" {
		t.Fatalf("issue: %+v %v", capability, err)
	}
	download, err := service.Open(context.Background(), capability.Token)
	if err != nil || download.Bytes != 3 || !reader.opened {
		t.Fatalf("open: %+v %v", download, err)
	}
	_ = download.Body.Close()
	store.status.Version++
	if _, err := service.Open(context.Background(), capability.Token); !errors.Is(err, ErrCapabilityInvalid) {
		t.Fatalf("state change did not revoke capability: %v", err)
	}
}

func TestDownloadCapabilityRejectsUnavailableArtifact(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	accountID := ids.AccountID("10000000-0000-4000-8000-000000000001")
	userID := ids.UserID("20000000-0000-4000-8000-000000000002")
	store := &downloadStoreStub{status: Status{ID: "30000000-0000-4000-8000-000000000003", AccountID: accountID, State: StateBuilding, Version: 1, ExpiresAt: now.Add(time.Hour)}}
	key := []byte(strings.Repeat("k", exportcapability.MinimumKeyBytes))
	clock := exportClock{now: now}
	signer, _ := exportcapability.NewSigner("https://app.example.test", "1", key, time.Minute, clock)
	verifier, _ := exportcapability.NewVerifier("https://app.example.test", map[string][]byte{"1": key}, exportcapability.MaximumLifetime, 0, clock)
	service, _ := NewDownloadService(store, &downloadReaderStub{}, &exportAuthorizer{}, signer, verifier, clock)
	session := sessions.Session{UserID: userID, ReauthenticationMethod: sessions.AuthenticationMethodPasskey, ReauthenticatedAt: now}
	if _, err := service.Issue(context.Background(), CapabilityCommand{AccountID: accountID, Actor: userID, ExportID: store.status.ID, Session: session}); !errors.Is(err, ErrArtifactUnavailable) {
		t.Fatalf("unavailable artifact: %v", err)
	}
}

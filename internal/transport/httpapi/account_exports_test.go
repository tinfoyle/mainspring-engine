package httpapi

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountexport"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/platform/exportcapability"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type exportHTTPClock struct{ now time.Time }

func (clock exportHTTPClock) Now() time.Time { return clock.now }

type exportHTTPStore struct {
	status   accountexport.Status
	artifact accountexport.Artifact
}

func (store exportHTTPStore) GetArtifact(context.Context, ids.AccountID, string) (accountexport.Status, accountexport.Artifact, error) {
	return store.status, store.artifact, nil
}

type exportHTTPReader struct{ body string }

func (reader exportHTTPReader) Open(context.Context, accountexport.Artifact) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(reader.body)), nil
}

type exportHTTPAuthorizer struct{}

func (exportHTTPAuthorizer) Authorize(context.Context, access.Actor, ids.AccountID, access.Requirement) (access.AccountContext, error) {
	return access.AccountContext{}, nil
}

func TestAccountExportDownloadRequiresHeaderCapabilityAndStreamsBoundArtifact(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	accountID := ids.AccountID("10000000-0000-4000-8000-000000000001")
	userID := ids.UserID("20000000-0000-4000-8000-000000000002")
	exportID := "30000000-0000-4000-8000-000000000003"
	body := "zip"
	digest := sha256.Sum256([]byte(body))
	availableAt := now.Add(-time.Minute)
	store := exportHTTPStore{status: accountexport.Status{ID: exportID, AccountID: accountID, RequestedBy: userID, State: accountexport.StateAvailable, ArtifactBytes: int64(len(body)), Version: 2, AvailableAt: &availableAt, ExpiresAt: now.Add(time.Hour)}, artifact: accountexport.Artifact{Reference: "s3-export-v1.reference", SHA256: digest, Bytes: int64(len(body))}}
	key := []byte(strings.Repeat("k", exportcapability.MinimumKeyBytes))
	clock := exportHTTPClock{now}
	signer, _ := exportcapability.NewSigner("https://app.example.test", "1", key, time.Minute, clock)
	verifier, _ := exportcapability.NewVerifier("https://app.example.test", map[string][]byte{"1": key}, exportcapability.MaximumLifetime, 0, clock)
	downloads, _ := accountexport.NewDownloadService(store, exportHTTPReader{body}, exportHTTPAuthorizer{}, signer, verifier, clock)
	// The transport accepts only a token whose digest matches current state.
	capabilityToken, _, err := signer.Issue(exportcapability.Authority{AccountID: accountID, UserID: userID, ExportID: exportID, RequestVersion: 2, ArtifactSHA256: fmt.Sprintf("%x", digest), ArtifactBytes: int64(len(body))}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{exportDownloads: downloads, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/account-exports/"+exportID+"/artifact", nil)
	request.SetPathValue("exportID", exportID)
	request.Header.Set("Authorization", exportcapability.TokenType+" "+capabilityToken)
	recorder := httptest.NewRecorder()
	server.downloadAccountExport(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Body.String() != body || recorder.Header().Get("Content-Type") != "application/zip" || recorder.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("status=%d headers=%v body=%q", recorder.Code, recorder.Header(), recorder.Body.String())
	}

	queryRequest := httptest.NewRequest(http.MethodGet, "/api/v1/account-exports/"+exportID+"/artifact?token="+capabilityToken, nil)
	queryRequest.SetPathValue("exportID", exportID)
	queryRecorder := httptest.NewRecorder()
	server.downloadAccountExport(queryRecorder, queryRequest)
	if queryRecorder.Code != http.StatusBadRequest {
		t.Fatalf("query capability status=%d", queryRecorder.Code)
	}
}

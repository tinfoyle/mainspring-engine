package googledrive

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationhealth"
	"github.com/tinfoyle/spyglass-engine/internal/application/integrationsync"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
)

func TestProviderCapturesBoundedInitialDirectChildren(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("/token", tokenHandler)
	handler.HandleFunc("/drive/v3/changes/startPageToken", func(writer http.ResponseWriter, request *http.Request) {
		assertBearer(t, request)
		writeJSON(t, writer, map[string]any{"startPageToken": "start=7"})
	})
	handler.HandleFunc("/drive/v3/files", func(writer http.ResponseWriter, request *http.Request) {
		assertBearer(t, request)
		if got := request.URL.Query().Get("q"); got != "'folder-a' in parents and trashed = false" {
			t.Fatalf("query=%q", got)
		}
		if request.URL.Query().Get("supportsAllDrives") != "true" || request.URL.Query().Get("includeItemsFromAllDrives") != "true" {
			t.Fatal("shared-drive flags are absent")
		}
		writeJSON(t, writer, map[string]any{"files": []any{
			driveFileFixture("file-1", "Operating / plan.txt", "text/plain", "folder-a", "11", "5", true),
			driveFileFixture("shortcut-1", "Outside shortcut", "application/vnd.google-apps.shortcut", "folder-a", "12", "", true),
		}})
	})
	handler.HandleFunc("/drive/v3/files/file-1", func(writer http.ResponseWriter, request *http.Request) {
		assertBearer(t, request)
		if request.URL.Query().Get("alt") != "media" {
			t.Fatalf("alt=%q", request.URL.Query().Get("alt"))
		}
		_, _ = io.WriteString(writer, "hello")
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	provider := testProvider(t, server, 4)
	page, err := provider.Sync(context.Background(), integrationsync.ProviderRequest{
		CredentialProvider: ProviderCode, FolderIDs: []string{"folder-a"}, Credential: testCredential(),
	})
	if err != nil || len(page.Changes) != 1 || !page.HasMore {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	change := page.Changes[0]
	if change.FolderID != "folder-a" || change.ObjectID != "file-1" || change.RevisionID != "version:11" ||
		change.Title != "Operating / plan.txt" || change.Filename != "Operating _ plan.txt" || change.MediaType != "text/plain" || string(change.Content) != "hello" {
		t.Fatalf("change=%+v", change)
	}
	cursor, initial, err := decodeCursor(page.NextCursor, 1)
	if err != nil || initial || cursor.Phase != "changes" || cursor.PageToken != "start=7" {
		t.Fatalf("cursor=%+v initial=%t err=%v", cursor, initial, err)
	}
}

func TestFixtureConstructorRequiresExplicitValidEndpoints(t *testing.T) {
	config := Config{ClientID: "client", ClientSecret: "secret"}
	if _, err := NewFixture(config, FixtureEndpoints{}); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("empty fixture endpoints error=%v", err)
	}
	provider, err := NewFixture(config, FixtureEndpoints{Token: "http://fixture.test/token", Drive: "http://fixture.test/drive/v3"})
	if err != nil || provider.tokenEndpoint != "http://fixture.test/token" || provider.driveAPI != "http://fixture.test/drive/v3" {
		t.Fatalf("provider=%+v err=%v", provider, err)
	}
}

func TestProviderConsumesChangesAndRemovesLostMembership(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("/token", tokenHandler)
	handler.HandleFunc("/drive/v3/changes", func(writer http.ResponseWriter, request *http.Request) {
		assertBearer(t, request)
		query := request.URL.Query()
		if query.Get("pageToken") != "old=token" || query.Get("includeRemoved") != "true" || query.Get("includeCorpusRemovals") != "true" ||
			query.Get("restrictToMyDrive") != "false" {
			t.Fatalf("query=%v", query)
		}
		writeJSON(t, writer, map[string]any{"newStartPageToken": "new=token", "changes": []any{
			map[string]any{"fileId": "file-2", "changeType": "file", "file": driveFileFixture("file-2", "record.md", "text/markdown", "folder-a", "22", "6", true)},
			map[string]any{"fileId": "file-1", "changeType": "file", "file": driveFileFixture("file-1", "old.txt", "text/plain", "outside", "23", "3", true)},
			map[string]any{"fileId": "file-3", "changeType": "file", "removed": true},
			map[string]any{"fileId": "shortcut-1", "changeType": "file", "file": driveFileFixture("shortcut-1", "shortcut", "application/vnd.google-apps.shortcut", "folder-a", "24", "", true)},
		}})
	})
	handler.HandleFunc("/drive/v3/files/file-2", func(writer http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(writer, "# plan") })
	server := httptest.NewServer(handler)
	defer server.Close()
	provider := testProvider(t, server, 4)
	cursor, err := encodeCursor(syncCursor{Version: cursorVersion, Phase: "changes", PageToken: "old=token"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := provider.Sync(context.Background(), integrationsync.ProviderRequest{CredentialProvider: ProviderCode, FolderIDs: []string{"folder-a"}, Cursor: cursor, Credential: testCredential()})
	if err != nil || len(page.Changes) != 4 || page.HasMore {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if page.Changes[0].Deleted || string(page.Changes[0].Content) != "# plan" {
		t.Fatalf("admission=%+v", page.Changes[0])
	}
	for _, change := range page.Changes[1:] {
		if !change.Deleted || change.FolderID != "" || change.Title != "" || change.Filename != "" || change.MediaType != "" || len(change.Content) != 0 {
			t.Fatalf("removal=%+v", change)
		}
	}
	next, _, err := decodeCursor(page.NextCursor, 1)
	if err != nil || next.PageToken != "new=token" {
		t.Fatalf("cursor=%+v err=%v", next, err)
	}
}

func TestProviderExportsSupportedWorkspaceDocuments(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("/token", tokenHandler)
	handler.HandleFunc("/drive/v3/changes/startPageToken", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, map[string]any{"startPageToken": "7"})
	})
	handler.HandleFunc("/drive/v3/files", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, map[string]any{"files": []any{driveFileFixture("doc-1", "Quarterly plan", "application/vnd.google-apps.document", "folder-a", "31", "", true)}})
	})
	handler.HandleFunc("/drive/v3/files/doc-1/export", func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("mimeType") != "application/pdf" {
			t.Fatalf("mimeType=%q", request.URL.Query().Get("mimeType"))
		}
		_, _ = io.WriteString(writer, "%PDF-fixture")
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	provider := testProvider(t, server, 3)
	page, err := provider.Sync(context.Background(), integrationsync.ProviderRequest{CredentialProvider: ProviderCode, FolderIDs: []string{"folder-a"}, Credential: testCredential()})
	if err != nil || len(page.Changes) != 1 || page.Changes[0].Filename != "Quarterly plan.pdf" ||
		page.Changes[0].MediaType != "application/pdf" || string(page.Changes[0].Content) != "%PDF-fixture" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}

func TestProviderRejectsCredentialDriftAndRedirects(t *testing.T) {
	provider, err := newProvider(Config{Client: &http.Client{}, PageSize: 1, ClientID: "client", ClientSecret: "secret"}, "http://127.0.0.1/token", "http://127.0.0.1/drive", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Sync(context.Background(), integrationsync.ProviderRequest{CredentialProvider: ProviderCode, FolderIDs: []string{"folder-a"}, Credential: []byte(`{"refresh_token":"c","access_token":"forbidden"}`)}); !errors.Is(err, ErrCredential) {
		t.Fatalf("credential err=%v", err)
	}

	redirected := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected = true }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", target.URL)
		writer.WriteHeader(http.StatusFound)
	}))
	defer source.Close()
	provider, err = newProvider(Config{ClientID: "client", ClientSecret: "secret"}, source.URL, source.URL+"/drive", true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Sync(context.Background(), integrationsync.ProviderRequest{CredentialProvider: ProviderCode, FolderIDs: []string{"folder-a"}, Credential: testCredential()})
	if !errors.Is(err, ErrProvider) || redirected {
		t.Fatalf("err=%v redirected=%t", err, redirected)
	}
}

func TestProviderLoadsRestrictiveOAuthClientFile(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "oauth-client.json")
	if err := os.WriteFile(filename, []byte(`{"client_id":"client","client_secret":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	provider, err := NewFromClientFile(Config{PageSize: 2}, filename)
	if err != nil || provider.clientID != "client" || provider.clientSecret != "secret" {
		t.Fatalf("provider=%+v err=%v", provider, err)
	}
	if err := os.Chmod(filename, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFromClientFile(Config{}, filename); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("permissive file err=%v", err)
	}
}

func TestProviderHealthChecksTokenAndEveryExactFolderWithoutContent(t *testing.T) {
	handler := http.NewServeMux()
	handler.HandleFunc("/token", tokenHandler)
	handler.HandleFunc("/drive/v3/changes/startPageToken", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, map[string]any{"startPageToken": "9"})
	})
	handler.HandleFunc("/drive/v3/files/folder-a", func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("fields") != "id,mimeType,trashed,capabilities(canListChildren)" {
			t.Fatalf("fields=%q", request.URL.Query().Get("fields"))
		}
		writeJSON(t, writer, map[string]any{"id": "folder-a", "mimeType": "application/vnd.google-apps.folder",
			"capabilities": map[string]any{"canListChildren": true}})
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	provider := testProvider(t, server, 2)
	result := provider.Probe(context.Background(), integrationhealth.ProbeCall{Claim: integrationhealth.Claim{
		ConnectorKind: domain.ConnectorGoogleDrive, Capabilities: []domain.Capability{domain.CapabilityDriveRead},
		Scope: domain.ConnectionScope{DriveFolderIDs: []string{"folder-a"}}, CredentialProvider: ProviderCode,
	}, Credential: testCredential()})
	if result.State != domain.HealthHealthy || result.ErrorCode != "" {
		t.Fatalf("result=%+v", result)
	}
}

func testProvider(t *testing.T, server *httptest.Server, pageSize int) *Provider {
	t.Helper()
	provider, err := newProvider(Config{Client: server.Client(), PageSize: pageSize, ClientID: "client", ClientSecret: "secret"}, server.URL+"/token", server.URL+"/drive/v3", true)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func tokenHandler(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || request.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
		http.Error(writer, "bad request", http.StatusBadRequest)
		return
	}
	if err := request.ParseForm(); err != nil || request.Form.Get("grant_type") != "refresh_token" || request.Form.Get("client_id") != "client" ||
		request.Form.Get("client_secret") != "secret" || request.Form.Get("refresh_token") != "refresh" {
		http.Error(writer, "bad credential", http.StatusUnauthorized)
		return
	}
	writeJSON(nil, writer, map[string]any{"access_token": "access", "token_type": "Bearer", "expires_in": 3600, "scope": "drive.readonly"})
}

func testCredential() []byte {
	return []byte(`{"refresh_token":"refresh"}`)
}

func driveFileFixture(id, name, mediaType, parent, version, size string, canDownload bool) map[string]any {
	return map[string]any{"id": id, "name": name, "mimeType": mediaType, "parents": []string{parent}, "version": version,
		"size": size, "capabilities": map[string]any{"canDownload": canDownload}}
}

func assertBearer(t *testing.T, request *http.Request) {
	t.Helper()
	if request.Header.Get("Authorization") != "Bearer access" {
		t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
	}
}

func writeJSON(t *testing.T, writer http.ResponseWriter, value any) {
	if err := json.NewEncoder(writer).Encode(value); err != nil && t != nil {
		t.Fatal(err)
	}
}

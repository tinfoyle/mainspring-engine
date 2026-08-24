package mockconnector

import (
	"context"
	"testing"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationsync"
)

func TestDriveProviderAdvancesOnlyItsBoundedCursor(t *testing.T) {
	provider := DriveProvider{}
	request := integrationsync.ProviderRequest{CredentialProvider: "mock", FolderIDs: []string{"folder-a"}, Credential: []byte("leased")}
	page, err := provider.Sync(context.Background(), request)
	if err != nil || len(page.Changes) != 0 || page.HasMore || len(page.NextCursor) == 0 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	request.Cursor = page.NextCursor
	if _, err := provider.Sync(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.Cursor = []byte("foreign")
	if _, err := provider.Sync(context.Background(), request); err == nil {
		t.Fatal("foreign cursor was accepted")
	}
}

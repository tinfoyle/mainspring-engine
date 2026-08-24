package mockconnector

import (
	"context"
	"errors"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationsync"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
)

var mockDriveCursor = []byte(`{"v":1,"provider":"mock-drive"}`)

// DriveProvider is a content-free local provider fixture. It exercises the
// complete claim, credential, cursor and settlement path without network
// access. Scripted content fixtures are added separately from delivery scripts
// so local certification cannot accidentally execute a real provider.
type DriveProvider struct{}

func (DriveProvider) Sync(ctx context.Context, request integrationsync.ProviderRequest) (integrationsync.ProviderPage, error) {
	if ctx == nil || ctx.Err() != nil || request.SourceKind != domain.ConnectorGoogleDrive || len(request.FolderIDs) == 0 || len(request.Credential) == 0 ||
		request.CredentialProvider != "mock" ||
		(len(request.Cursor) != 0 && string(request.Cursor) != string(mockDriveCursor)) {
		return integrationsync.ProviderPage{}, errors.New("mock Drive request is invalid")
	}
	return integrationsync.ProviderPage{NextCursor: append([]byte(nil), mockDriveCursor...)}, nil
}

var _ integrationsync.Provider = DriveProvider{}

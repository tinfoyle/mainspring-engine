// Package mountedcredentials leases provider credentials from a read-only
// filesystem mount. The mount may be backed by Docker secrets, Kubernetes
// projected Secrets, or a CSI secret-store driver. The database supplies only
// an attestation digest; the opaque reference and material never enter it.
package mountedcredentials

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationcredentials"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	indexFilename     = "index.json"
	maximumIndexBytes = 1 << 20
	maximumEntries    = 10000
	maximumReference  = 2048
	maximumMaterial   = 64 << 10
)

var (
	ErrConfiguration = errors.New("mounted credential broker configuration is invalid")
	ErrUnavailable   = errors.New("mounted credential is unavailable")
	validProvider    = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,99}$`)
)

type indexDocument struct {
	Version     uint64       `json:"version"`
	Credentials []indexEntry `json:"credentials"`
}

type indexEntry struct {
	AccountID    ids.AccountID               `json:"account_id"`
	CredentialID ids.IntegrationCredentialID `json:"credential_id"`
	Generation   uint64                      `json:"generation"`
	Provider     string                      `json:"provider"`
	Reference    string                      `json:"reference"`
	File         string                      `json:"file"`
}

type Broker struct{ root string }

func New(root string) (*Broker, error) {
	root = strings.TrimSpace(root)
	if root == "" || !filepath.IsAbs(root) {
		return nil, ErrConfiguration
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return nil, fmt.Errorf("%w: resolve root: %v", ErrConfiguration, err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("%w: root is not a directory", ErrConfiguration)
	}
	broker := &Broker{root: resolved}
	if _, err := broker.loadIndex(); err != nil {
		return nil, err
	}
	return broker, nil
}

func (broker *Broker) Acquire(ctx context.Context, request integrationcredentials.Request) (integrationcredentials.Lease, error) {
	if broker == nil || !validRequest(request, time.Now().UTC()) {
		return nil, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(ErrUnavailable, err)
	}
	entries, err := broker.loadIndex()
	if err != nil {
		return nil, err
	}
	entry, exists := entries[entryKey(request.AccountID, request.CredentialID, request.CredentialGeneration)]
	if !exists || entry.Provider != request.CredentialProvider || sha256.Sum256([]byte(entry.Reference)) != request.ReferenceSHA256 {
		return nil, ErrUnavailable
	}
	material, err := broker.readMaterial(entry.File)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		wipe(material)
		return nil, errors.Join(ErrUnavailable, err)
	}
	return &lease{material: material}, nil
}

func (broker *Broker) loadIndex() (map[string]indexEntry, error) {
	file, err := os.Open(filepath.Join(broker.root, indexFilename))
	if err != nil {
		return nil, fmt.Errorf("%w: open index: %v", ErrConfiguration, err)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maximumIndexBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > maximumIndexBytes {
		return nil, fmt.Errorf("%w: read index", ErrConfiguration)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document indexDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("%w: decode index: %v", ErrConfiguration, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: index contains trailing JSON", ErrConfiguration)
	}
	if document.Version != 1 || len(document.Credentials) == 0 || len(document.Credentials) > maximumEntries {
		return nil, ErrConfiguration
	}
	entries := make(map[string]indexEntry, len(document.Credentials))
	for _, entry := range document.Credentials {
		rawProvider, rawReference, rawFile := entry.Provider, entry.Reference, entry.File
		entry.Provider, entry.Reference, entry.File = strings.TrimSpace(entry.Provider), strings.TrimSpace(entry.Reference), strings.TrimSpace(entry.File)
		cleanFile := filepath.Clean(entry.File)
		key := entryKey(entry.AccountID, entry.CredentialID, entry.Generation)
		if ids.Validate(string(entry.AccountID)) != nil || ids.Validate(string(entry.CredentialID)) != nil || entry.Generation == 0 ||
			!validProvider.MatchString(entry.Provider) || rawProvider != entry.Provider || entry.Reference == "" || len(entry.Reference) > maximumReference ||
			rawReference != entry.Reference || rawFile != entry.File || entry.File == "" || filepath.IsAbs(entry.File) || cleanFile != entry.File ||
			cleanFile == "." || cleanFile == ".." || strings.HasPrefix(cleanFile, ".."+string(filepath.Separator)) || entries[key].CredentialID != "" {
			return nil, ErrConfiguration
		}
		entry.File = cleanFile
		entries[key] = entry
	}
	return entries, nil
}

func (broker *Broker) readMaterial(relative string) ([]byte, error) {
	target := filepath.Join(broker.root, relative)
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil || !withinRoot(broker.root, resolved) {
		return nil, ErrUnavailable
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, fmt.Errorf("%w: open material: %v", ErrUnavailable, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o037 != 0 {
		return nil, fmt.Errorf("%w: material permissions", ErrUnavailable)
	}
	material, err := io.ReadAll(io.LimitReader(file, maximumMaterial+1))
	if err != nil || len(material) == 0 || len(material) > maximumMaterial {
		wipe(material)
		return nil, fmt.Errorf("%w: read material", ErrUnavailable)
	}
	return material, nil
}

func validRequest(request integrationcredentials.Request, now time.Time) bool {
	return ids.Validate(string(request.AccountID)) == nil && ids.Validate(request.OperationID) == nil && ids.Validate(string(request.ConnectionID)) == nil &&
		ids.Validate(string(request.CredentialID)) == nil && request.CredentialGeneration > 0 &&
		credentialPurposeAllowed(request) &&
		validProvider.MatchString(request.CredentialProvider) && request.ReferenceSHA256 != [sha256.Size]byte{} && request.ExpiresAt.After(now)
}

func credentialPurposeAllowed(request integrationcredentials.Request) bool {
	switch request.Purpose {
	case integrationcredentials.PurposeSync:
		return request.Capability == domain.CapabilityDriveRead ||
			(request.Capability == domain.CapabilityEmailRead && request.CredentialProvider == "imap")
	case integrationcredentials.PurposeHealth:
		return request.Capability == domain.CapabilityDriveRead || request.Capability == domain.CapabilityWebResearch || request.Capability == domain.CapabilityWebPublish ||
			(request.Capability == domain.CapabilityEmailRead && request.CredentialProvider == "imap") ||
			(request.Capability == domain.CapabilityEmailSend && request.CredentialProvider != "imap")
	case integrationcredentials.PurposeResearch:
		return request.Capability == domain.CapabilityWebResearch
	case integrationcredentials.PurposeExecute, integrationcredentials.PurposeReconcile:
		return request.Capability == domain.CapabilityWebPublish ||
			(request.Capability == domain.CapabilityEmailSend && request.CredentialProvider != "imap")
	default:
		return false
	}
}

func withinRoot(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func entryKey(accountID ids.AccountID, credentialID ids.IntegrationCredentialID, generation uint64) string {
	return string(accountID) + "/" + string(credentialID) + "/" + fmt.Sprint(generation)
}

type lease struct {
	mu       sync.Mutex
	material []byte
	closed   bool
}

func (value *lease) Material() []byte {
	value.mu.Lock()
	defer value.mu.Unlock()
	if value.closed {
		return nil
	}
	return value.material
}

func (value *lease) Close() error {
	value.mu.Lock()
	defer value.mu.Unlock()
	if !value.closed {
		wipe(value.material)
		value.material, value.closed = nil, true
	}
	return nil
}

func wipe(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

var _ integrationcredentials.Broker = (*Broker)(nil)

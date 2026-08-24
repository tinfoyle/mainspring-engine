// Package encryptedcredentials stores provider authorization and credential
// material as AES-256-GCM sealed, restrictive, atomically replaced files. The
// filesystem contains no plaintext provider secret, and PostgreSQL needs only
// the opaque credential identity, generation, provider, and reference digest.
package encryptedcredentials

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationcredentials"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	sealVersion       = byte(1)
	maximumSealedFile = 128 << 10
	maximumMaterial   = 64 << 10
	maximumReference  = 2048
	maximumVerifier   = 128
	lockFilename      = ".provider-secrets.lock"
)

var (
	ErrConfiguration = errors.New("encrypted provider-secret store configuration is invalid")
	ErrUnavailable   = errors.New("encrypted provider secret is unavailable")
	ErrConflict      = errors.New("encrypted provider-secret replay conflicts")
	validProvider    = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,99}$`)
)

type document struct {
	Version      int    `json:"version"`
	Kind         string `json:"kind"`
	AccountID    string `json:"account_id"`
	SessionID    string `json:"session_id,omitempty"`
	CredentialID string `json:"credential_id,omitempty"`
	Generation   uint64 `json:"generation,omitempty"`
	Provider     string `json:"provider,omitempty"`
	Reference    []byte `json:"reference,omitempty"`
	Material     []byte `json:"material,omitempty"`
	Verifier     []byte `json:"verifier,omitempty"`
	OAuthState   []byte `json:"oauth_state,omitempty"`
	State        string `json:"state"`
	ExpiresAt    string `json:"expires_at,omitempty"`
}

type Vault struct {
	root string
	aead cipher.AEAD
	mu   sync.Mutex
}

func New(root, keyFile string) (*Vault, error) {
	root, keyFile = strings.TrimSpace(root), strings.TrimSpace(keyFile)
	if root == "" || keyFile == "" || !filepath.IsAbs(root) || !filepath.IsAbs(keyFile) {
		return nil, ErrConfiguration
	}
	if err := os.MkdirAll(filepath.Clean(root), 0o700); err != nil {
		return nil, fmt.Errorf("%w: create root", ErrConfiguration)
	}
	root, err := restrictiveDirectory(root)
	if err != nil {
		return nil, err
	}
	key, err := restrictiveKey(keyFile)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	wipe(key)
	if err != nil {
		return nil, ErrConfiguration
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrConfiguration
	}
	return &Vault{root: root, aead: aead}, nil
}

func restrictiveDirectory(root string) (string, error) {
	clean := filepath.Clean(root)
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil || resolved != clean {
		return "", fmt.Errorf("%w: resolve root", ErrConfiguration)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%w: root permissions", ErrConfiguration)
	}
	return resolved, nil
}

func restrictiveKey(filename string) ([]byte, error) {
	clean := filepath.Clean(filename)
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil || resolved != clean {
		return nil, fmt.Errorf("%w: resolve key", ErrConfiguration)
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, fmt.Errorf("%w: open key", ErrConfiguration)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%w: key permissions", ErrConfiguration)
	}
	key, err := io.ReadAll(io.LimitReader(file, 33))
	if err != nil || len(key) != 32 {
		wipe(key)
		return nil, fmt.Errorf("%w: key size", ErrConfiguration)
	}
	return key, nil
}

func (vault *Vault) PutAuthorization(ctx context.Context, secret integrationcredentials.AuthorizationSecret) error {
	if vault == nil || vault.aead == nil || ctx == nil || ctx.Err() != nil || ids.Validate(string(secret.AccountID)) != nil ||
		ids.Validate(string(secret.SessionID)) != nil || !validVerifier(secret.State) || !validVerifier(secret.Verifier) || secret.ExpiresAt.IsZero() {
		return ErrUnavailable
	}
	doc := document{Version: 1, Kind: "authorization", AccountID: string(secret.AccountID), SessionID: string(secret.SessionID),
		OAuthState: append([]byte(nil), secret.State...), Verifier: append([]byte(nil), secret.Verifier...), State: "active", ExpiresAt: secret.ExpiresAt.UTC().Format(time.RFC3339Nano)}
	defer doc.wipe()
	return vault.put(ctx, vault.authorizationPath(secret.AccountID, secret.SessionID), doc)
}

func (vault *Vault) Authorization(ctx context.Context, accountID ids.AccountID, sessionID ids.IntegrationAuthorizationSessionID, now time.Time) (integrationcredentials.AuthorizationMaterial, error) {
	if vault == nil || vault.aead == nil || ctx == nil || ctx.Err() != nil || ids.Validate(string(accountID)) != nil || ids.Validate(string(sessionID)) != nil || now.IsZero() {
		return integrationcredentials.AuthorizationMaterial{}, ErrUnavailable
	}
	doc, err := vault.read(vault.authorizationPath(accountID, sessionID))
	if err != nil {
		return integrationcredentials.AuthorizationMaterial{}, err
	}
	defer doc.wipe()
	expiresAt, err := time.Parse(time.RFC3339Nano, doc.ExpiresAt)
	if err != nil || doc.Version != 1 || doc.Kind != "authorization" || doc.AccountID != string(accountID) || doc.SessionID != string(sessionID) ||
		doc.State != "active" || !validVerifier(doc.OAuthState) || !validVerifier(doc.Verifier) || !expiresAt.After(now.UTC()) || len(doc.Material) != 0 || len(doc.Reference) != 0 {
		return integrationcredentials.AuthorizationMaterial{}, ErrUnavailable
	}
	return integrationcredentials.AuthorizationMaterial{State: append([]byte(nil), doc.OAuthState...), Verifier: append([]byte(nil), doc.Verifier...), ExpiresAt: expiresAt}, nil
}

func (vault *Vault) DeleteAuthorization(ctx context.Context, accountID ids.AccountID, sessionID ids.IntegrationAuthorizationSessionID) error {
	if vault == nil || ctx == nil || ctx.Err() != nil || ids.Validate(string(accountID)) != nil || ids.Validate(string(sessionID)) != nil {
		return ErrUnavailable
	}
	return vault.remove(ctx, vault.authorizationPath(accountID, sessionID))
}

func (vault *Vault) PutCredential(ctx context.Context, secret integrationcredentials.CredentialSecret) error {
	if vault == nil || vault.aead == nil || ctx == nil || ctx.Err() != nil || ids.Validate(string(secret.AccountID)) != nil ||
		ids.Validate(string(secret.CredentialID)) != nil || secret.Generation == 0 || !validProvider.MatchString(secret.Provider) ||
		!validReference(secret.Reference) || len(secret.Material) == 0 || len(secret.Material) > maximumMaterial {
		return ErrUnavailable
	}
	doc := document{Version: 1, Kind: "credential", AccountID: string(secret.AccountID), CredentialID: string(secret.CredentialID),
		Generation: secret.Generation, Provider: secret.Provider, Reference: append([]byte(nil), secret.Reference...),
		Material: append([]byte(nil), secret.Material...), State: "active"}
	defer doc.wipe()
	return vault.put(ctx, vault.credentialPath(secret.AccountID, secret.CredentialID, secret.Generation), doc)
}

func (vault *Vault) FenceCredential(ctx context.Context, accountID ids.AccountID, credentialID ids.IntegrationCredentialID, generation uint64, state integrationcredentials.CredentialEndState) error {
	if vault == nil || ctx == nil || ctx.Err() != nil || ids.Validate(string(accountID)) != nil || ids.Validate(string(credentialID)) != nil || generation == 0 ||
		(state != integrationcredentials.CredentialRotated && state != integrationcredentials.CredentialRevoked) {
		return ErrUnavailable
	}
	path := vault.credentialPath(accountID, credentialID, generation)
	return vault.locked(ctx, func() error {
		doc, err := vault.read(path)
		if err != nil {
			return err
		}
		defer doc.wipe()
		if !doc.matchesCredential(accountID, credentialID, generation) {
			return ErrUnavailable
		}
		if doc.State == string(state) {
			return nil
		}
		if doc.State != "active" {
			return ErrConflict
		}
		doc.State = string(state)
		return vault.replace(path, doc)
	})
}

func (vault *Vault) PurgeCredential(ctx context.Context, accountID ids.AccountID, credentialID ids.IntegrationCredentialID, generation uint64) error {
	if vault == nil || ctx == nil || ctx.Err() != nil || ids.Validate(string(accountID)) != nil || ids.Validate(string(credentialID)) != nil || generation == 0 {
		return ErrUnavailable
	}
	return vault.remove(ctx, vault.credentialPath(accountID, credentialID, generation))
}

func (vault *Vault) Acquire(ctx context.Context, request integrationcredentials.Request) (integrationcredentials.Lease, error) {
	if vault == nil || vault.aead == nil || !validRequest(request, time.Now().UTC()) || ctx == nil || ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	doc, err := vault.read(vault.credentialPath(request.AccountID, request.CredentialID, request.CredentialGeneration))
	if err != nil {
		return nil, err
	}
	defer doc.wipe()
	if !doc.matchesCredential(request.AccountID, request.CredentialID, request.CredentialGeneration) || doc.State != "active" ||
		doc.Provider != request.CredentialProvider || sha256.Sum256(doc.Reference) != request.ReferenceSHA256 || len(doc.Material) == 0 || len(doc.Material) > maximumMaterial {
		return nil, ErrUnavailable
	}
	return newLease(doc.Material), nil
}

func validRequest(request integrationcredentials.Request, now time.Time) bool {
	capabilityAllowed := request.Purpose == integrationcredentials.PurposeSync && request.Capability == domain.CapabilityDriveRead ||
		request.Purpose == integrationcredentials.PurposeHealth && (request.Capability == domain.CapabilityDriveRead || request.Capability == domain.CapabilityEmailSend || request.Capability == domain.CapabilityWebPublish) ||
		(request.Purpose == integrationcredentials.PurposeExecute || request.Purpose == integrationcredentials.PurposeReconcile) &&
			(request.Capability == domain.CapabilityEmailSend || request.Capability == domain.CapabilityWebPublish)
	return ids.Validate(string(request.AccountID)) == nil && ids.Validate(request.OperationID) == nil && ids.Validate(string(request.ConnectionID)) == nil &&
		ids.Validate(string(request.CredentialID)) == nil && request.CredentialGeneration > 0 && capabilityAllowed &&
		validProvider.MatchString(request.CredentialProvider) && request.ReferenceSHA256 != [sha256.Size]byte{} && request.ExpiresAt.After(now)
}

func (vault *Vault) put(ctx context.Context, path string, value document) error {
	return vault.locked(ctx, func() error {
		existing, err := vault.read(path)
		if err == nil {
			defer existing.wipe()
			if existing.equal(value) {
				return nil
			}
			return ErrConflict
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return vault.replace(path, value)
	})
}

func (vault *Vault) remove(ctx context.Context, path string) error {
	return vault.locked(ctx, func() error {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return ErrUnavailable
		}
		return syncDirectory(filepath.Dir(path))
	})
}

func (vault *Vault) locked(ctx context.Context, operation func() error) error {
	if err := ctx.Err(); err != nil {
		return errors.Join(ErrUnavailable, err)
	}
	vault.mu.Lock()
	defer vault.mu.Unlock()
	lockPath := filepath.Join(vault.root, lockFilename)
	lock, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return ErrUnavailable
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return ErrUnavailable
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	if err := ctx.Err(); err != nil {
		return errors.Join(ErrUnavailable, err)
	}
	return operation()
}

func (vault *Vault) replace(path string, value document) error {
	if err := secureParents(vault.root, filepath.Dir(path)); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) == 0 || len(raw) > maximumSealedFile/2 {
		wipe(raw)
		return ErrUnavailable
	}
	defer wipe(raw)
	nonce := make([]byte, vault.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return ErrUnavailable
	}
	relative, err := filepath.Rel(vault.root, path)
	if err != nil || relative == "." || strings.HasPrefix(relative, "..") {
		return ErrUnavailable
	}
	sealed := append([]byte{sealVersion}, nonce...)
	sealed = vault.aead.Seal(sealed, nonce, raw, []byte("spyglass/provider-secret/v1/"+filepath.ToSlash(relative)))
	defer wipe(sealed)
	if len(sealed) > maximumSealedFile {
		return ErrUnavailable
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".sealed-*")
	if err != nil {
		return ErrUnavailable
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(sealed)
	}
	if err == nil {
		err = temporary.Sync()
	}
	closeErr := temporary.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return ErrUnavailable
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return ErrUnavailable
	}
	return syncDirectory(filepath.Dir(path))
}

func (vault *Vault) read(path string) (document, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return document{}, os.ErrNotExist
		}
		return document{}, ErrUnavailable
	}
	if resolved != filepath.Clean(path) || !within(vault.root, resolved) {
		return document{}, ErrUnavailable
	}
	file, err := os.Open(resolved)
	if err != nil {
		return document{}, ErrUnavailable
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return document{}, ErrUnavailable
	}
	sealed, err := io.ReadAll(io.LimitReader(file, maximumSealedFile+1))
	if err != nil || len(sealed) <= 1+vault.aead.NonceSize() || len(sealed) > maximumSealedFile || sealed[0] != sealVersion {
		wipe(sealed)
		return document{}, ErrUnavailable
	}
	defer wipe(sealed)
	nonce := sealed[1 : 1+vault.aead.NonceSize()]
	relative, err := filepath.Rel(vault.root, resolved)
	if err != nil {
		return document{}, ErrUnavailable
	}
	raw, err := vault.aead.Open(nil, nonce, sealed[1+vault.aead.NonceSize():], []byte("spyglass/provider-secret/v1/"+filepath.ToSlash(relative)))
	if err != nil {
		wipe(raw)
		return document{}, ErrUnavailable
	}
	defer wipe(raw)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value document
	if err := decoder.Decode(&value); err != nil || requireEnd(decoder) != nil {
		value.wipe()
		return document{}, ErrUnavailable
	}
	return value, nil
}

func secureParents(root, target string) error {
	if !within(root, target) {
		return ErrUnavailable
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		return ErrUnavailable
	}
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return ErrUnavailable
	}
	current := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
			return ErrUnavailable
		}
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return ErrUnavailable
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return ErrUnavailable
	}
	return nil
}

func within(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func (vault *Vault) authorizationPath(accountID ids.AccountID, sessionID ids.IntegrationAuthorizationSessionID) string {
	return filepath.Join(vault.root, "authorizations", string(accountID), string(sessionID)+".sealed")
}

func (vault *Vault) credentialPath(accountID ids.AccountID, credentialID ids.IntegrationCredentialID, generation uint64) string {
	return filepath.Join(vault.root, "credentials", string(accountID), string(credentialID), strconv.FormatUint(generation, 10)+".sealed")
}

func (value document) matchesCredential(accountID ids.AccountID, credentialID ids.IntegrationCredentialID, generation uint64) bool {
	return value.Version == 1 && value.Kind == "credential" && value.AccountID == string(accountID) &&
		value.CredentialID == string(credentialID) && value.Generation == generation && validProvider.MatchString(value.Provider) &&
		validReference(value.Reference) && len(value.Material) > 0 && len(value.Material) <= maximumMaterial && value.SessionID == "" && len(value.Verifier) == 0 && len(value.OAuthState) == 0
}

func (value document) equal(other document) bool {
	return value.Version == other.Version && value.Kind == other.Kind && value.AccountID == other.AccountID && value.SessionID == other.SessionID &&
		value.CredentialID == other.CredentialID && value.Generation == other.Generation && value.Provider == other.Provider &&
		bytes.Equal(value.Reference, other.Reference) && bytes.Equal(value.Material, other.Material) && bytes.Equal(value.Verifier, other.Verifier) && bytes.Equal(value.OAuthState, other.OAuthState) &&
		value.State == other.State && value.ExpiresAt == other.ExpiresAt
}

func (value *document) wipe() {
	wipe(value.Reference)
	wipe(value.Material)
	wipe(value.Verifier)
	wipe(value.OAuthState)
	value.Reference, value.Material, value.Verifier, value.OAuthState = nil, nil, nil, nil
}

func validReference(value []byte) bool {
	if len(value) < 16 || len(value) > maximumReference {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

func validVerifier(value []byte) bool {
	if len(value) < 43 || len(value) > maximumVerifier {
		return false
	}
	for _, character := range value {
		if !(character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || strings.ContainsRune("-._~", rune(character))) {
			return false
		}
	}
	return true
}

func requireEnd(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrUnavailable
	}
	return nil
}

type lease struct {
	mu       sync.Mutex
	material []byte
	closed   bool
}

func newLease(material []byte) *lease { return &lease{material: append([]byte(nil), material...)} }

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

var _ integrationcredentials.Store = (*Vault)(nil)
var _ integrationcredentials.Broker = (*Vault)(nil)

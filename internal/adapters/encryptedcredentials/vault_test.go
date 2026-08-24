package encryptedcredentials

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationcredentials"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	vaultAccount    = ids.AccountID("a1000000-0000-4000-8000-000000000001")
	vaultSession    = ids.IntegrationAuthorizationSessionID("a2000000-0000-4000-8000-000000000002")
	vaultCredential = ids.IntegrationCredentialID("a3000000-0000-4000-8000-000000000003")
	vaultConnection = ids.IntegrationConnectionID("a4000000-0000-4000-8000-000000000004")
	vaultOperation  = "a5000000-0000-4000-8000-000000000005"
)

func newTestVault(t *testing.T) (*Vault, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "secrets")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(base, "key")
	if err := os.WriteFile(keyFile, bytes.Repeat([]byte{0x72}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(keyFile, 0o600); err != nil {
		t.Fatal(err)
	}
	vault, err := New(root, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	return vault, root
}

func TestVaultSealsAuthorizationVerifierAndDeletesExactly(t *testing.T) {
	vault, root := newTestVault(t)
	now := time.Now().UTC()
	state := []byte("0123456789abcdefghijklmnopqrstuvwxyzABCDEFG")
	verifier := []byte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~")
	secret := integrationcredentials.AuthorizationSecret{AccountID: vaultAccount, SessionID: vaultSession, State: state, Verifier: verifier, ExpiresAt: now.Add(10 * time.Minute)}
	if err := vault.PutAuthorization(context.Background(), secret); err != nil {
		t.Fatal(err)
	}
	if err := vault.PutAuthorization(context.Background(), secret); err != nil {
		t.Fatalf("exact authorization replay: %v", err)
	}
	altered := secret
	altered.Verifier = append([]byte(nil), verifier...)
	altered.Verifier[0] = 'Z'
	if err := vault.PutAuthorization(context.Background(), altered); !errors.Is(err, ErrConflict) {
		t.Fatalf("altered authorization replay=%v", err)
	}
	material, err := vault.Authorization(context.Background(), vaultAccount, vaultSession, now)
	if err != nil || !bytes.Equal(material.State, state) || !bytes.Equal(material.Verifier, verifier) {
		t.Fatalf("authorization state=%q verifier=%q err=%v", material.State, material.Verifier, err)
	}
	material.Close()
	if material.State != nil || material.Verifier != nil {
		t.Fatalf("closed authorization material=%+v", material)
	}
	if _, err := vault.Authorization(context.Background(), vaultAccount, vaultSession, secret.ExpiresAt); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expired authorization=%v", err)
	}
	assertNoPlaintext(t, root, verifier)
	assertNoPlaintext(t, root, state)
	if err := vault.DeleteAuthorization(context.Background(), vaultAccount, vaultSession); err != nil {
		t.Fatal(err)
	}
	if err := vault.DeleteAuthorization(context.Background(), vaultAccount, vaultSession); err != nil {
		t.Fatalf("delete replay: %v", err)
	}
}

func TestVaultCredentialLeaseRequiresExactActiveGenerationAndPurpose(t *testing.T) {
	vault, root := newTestVault(t)
	reference := []byte("vault-reference-a3000000")
	material := []byte(`{"refresh_token":"local-refresh-material"}`)
	secret := integrationcredentials.CredentialSecret{AccountID: vaultAccount, CredentialID: vaultCredential, Generation: 1,
		Provider: domain.GoogleOAuthProvider, Reference: reference, Material: material}
	if err := vault.PutCredential(context.Background(), secret); err != nil {
		t.Fatal(err)
	}
	exists, err := vault.CredentialExists(context.Background(), vaultAccount, vaultCredential, 1, domain.GoogleOAuthProvider, sha256.Sum256(reference))
	if err != nil || !exists {
		t.Fatalf("credential exists=%t err=%v", exists, err)
	}
	materialLease, err := vault.CredentialMaterial(context.Background(), vaultAccount, vaultCredential, 1, domain.GoogleOAuthProvider, sha256.Sum256(reference))
	if err != nil || !bytes.Equal(materialLease.Material(), material) {
		t.Fatalf("credential material=%q err=%v", materialLease.Material(), err)
	}
	_ = materialLease.Close()
	if err := vault.PutCredential(context.Background(), secret); err != nil {
		t.Fatalf("exact credential replay: %v", err)
	}
	altered := secret
	altered.Material = []byte(`{"refresh_token":"different"}`)
	if err := vault.PutCredential(context.Background(), altered); !errors.Is(err, ErrConflict) {
		t.Fatalf("altered credential replay=%v", err)
	}
	request := integrationcredentials.Request{AccountID: vaultAccount, OperationID: vaultOperation, Purpose: integrationcredentials.PurposeSync,
		Capability: domain.CapabilityDriveRead, ConnectionID: vaultConnection, CredentialID: vaultCredential, CredentialGeneration: 1,
		CredentialProvider: domain.GoogleOAuthProvider, ReferenceSHA256: sha256.Sum256(reference), ExpiresAt: time.Now().UTC().Add(time.Minute)}
	lease, err := vault.Acquire(context.Background(), request)
	if err != nil || !bytes.Equal(lease.Material(), material) {
		t.Fatalf("credential lease=%q err=%v", lease.Material(), err)
	}
	_ = lease.Close()
	request.Purpose = integrationcredentials.PurposeHealth
	if lease, err = vault.Acquire(context.Background(), request); err != nil {
		t.Fatalf("Drive health lease: %v", err)
	}
	_ = lease.Close()
	request.Purpose = integrationcredentials.PurposeExecute
	if _, err := vault.Acquire(context.Background(), request); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Drive execute lease=%v", err)
	}
	request.Purpose = integrationcredentials.PurposeSync
	request.ReferenceSHA256 = sha256.Sum256([]byte("wrong-reference"))
	if _, err := vault.Acquire(context.Background(), request); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("wrong reference lease=%v", err)
	}
	assertNoPlaintext(t, root, material)
	assertNoPlaintext(t, root, reference)
	if err := vault.FenceCredential(context.Background(), vaultAccount, vaultCredential, 1, integrationcredentials.CredentialRotated); err != nil {
		t.Fatal(err)
	}
	if err := vault.FenceCredential(context.Background(), vaultAccount, vaultCredential, 1, integrationcredentials.CredentialRotated); err != nil {
		t.Fatalf("fence replay: %v", err)
	}
	request.ReferenceSHA256 = sha256.Sum256(reference)
	if _, err := vault.Acquire(context.Background(), request); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("fenced credential lease=%v", err)
	}
	if _, err := vault.CredentialMaterial(context.Background(), vaultAccount, vaultCredential, 1, domain.GoogleOAuthProvider, sha256.Sum256(reference)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("fenced lifecycle credential=%v", err)
	}
	if err := vault.FenceCredential(context.Background(), vaultAccount, vaultCredential, 1, integrationcredentials.CredentialRevoked); !errors.Is(err, ErrConflict) {
		t.Fatalf("ended-state rewrite=%v", err)
	}
	if err := vault.PurgeCredential(context.Background(), vaultAccount, vaultCredential, 1); err != nil {
		t.Fatal(err)
	}
	if exists, err := vault.CredentialExists(context.Background(), vaultAccount, vaultCredential, 1, domain.GoogleOAuthProvider, sha256.Sum256(reference)); err != nil || exists {
		t.Fatalf("purged credential exists=%t err=%v", exists, err)
	}
	if err := vault.PurgeCredential(context.Background(), vaultAccount, vaultCredential, 1); err != nil {
		t.Fatalf("purge replay: %v", err)
	}
}

func TestVaultEmailReadCredentialCannotCrossIntoSendExecution(t *testing.T) {
	vault, _ := newTestVault(t)
	reference := []byte("vault-imap-reference-a3000000")
	material := []byte(`{"username":"reader@example.com","password":"fixture"}`)
	secret := integrationcredentials.CredentialSecret{AccountID: vaultAccount, CredentialID: vaultCredential, Generation: 1,
		Provider: "imap", Reference: reference, Material: material}
	if err := vault.PutCredential(context.Background(), secret); err != nil {
		t.Fatal(err)
	}
	request := integrationcredentials.Request{AccountID: vaultAccount, OperationID: vaultOperation, Purpose: integrationcredentials.PurposeSync,
		Capability: domain.CapabilityEmailRead, ConnectionID: vaultConnection, CredentialID: vaultCredential, CredentialGeneration: 1,
		CredentialProvider: "imap", ReferenceSHA256: sha256.Sum256(reference), ExpiresAt: time.Now().UTC().Add(time.Minute)}
	lease, err := vault.Acquire(context.Background(), request)
	if err != nil || !bytes.Equal(lease.Material(), material) {
		t.Fatalf("email read lease=%q err=%v", lease.Material(), err)
	}
	_ = lease.Close()
	request.Purpose, request.Capability = integrationcredentials.PurposeExecute, domain.CapabilityEmailSend
	if _, err := vault.Acquire(context.Background(), request); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("IMAP material crossed into send execution: %v", err)
	}
}

func TestVaultRejectsWeakFilesAndCiphertextTamper(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "secrets")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(base, "key")
	if err := os.WriteFile(keyFile, bytes.Repeat([]byte{0x31}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(root, keyFile); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("weak root error=%v", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(keyFile, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(root, keyFile); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("weak key error=%v", err)
	}
	if err := os.Chmod(keyFile, 0o600); err != nil {
		t.Fatal(err)
	}
	vault, err := New(root, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	verifier := []byte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~")
	state := []byte("0123456789abcdefghijklmnopqrstuvwxyzABCDEFG")
	if err := vault.PutAuthorization(context.Background(), integrationcredentials.AuthorizationSecret{AccountID: vaultAccount, SessionID: vaultSession,
		State: state, Verifier: verifier, ExpiresAt: time.Now().UTC().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	path := vault.authorizationPath(vaultAccount, vaultSession)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 0xff
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Authorization(context.Background(), vaultAccount, vaultSession, time.Now().UTC()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("tampered authorization=%v", err)
	}
}

func assertNoPlaintext(t *testing.T, root string, needle []byte) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || entry.Name() == lockFilename {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(raw, needle) {
			t.Fatalf("plaintext material appeared in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

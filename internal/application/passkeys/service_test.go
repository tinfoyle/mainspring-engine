package passkeys_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/memory"
	"github.com/tinfoyle/spyglass-engine/internal/application/abuse"
	"github.com/tinfoyle/spyglass-engine/internal/application/passkeys"
	"github.com/tinfoyle/spyglass-engine/internal/application/recoverycodes"
	"github.com/tinfoyle/spyglass-engine/internal/modules/identity"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestDiscoverableLoginVerifiesSignatureUpdatesCounterAndRejectsReplay(t *testing.T) {
	fixture := newFixture(t)
	begun, err := fixture.service.BeginLogin(context.Background(), [32]byte{1})
	if err != nil {
		t.Fatal(err)
	}
	response := signedAssertion(t, fixture.privateKey, fixture.credentialID, fixture.handle, fixture.repository.ceremonies[begun.CeremonyID].Data.Challenge, 8)
	issued, err := fixture.service.CompleteLogin(context.Background(), passkeys.LoginCommand{CeremonyID: begun.CeremonyID, Response: response, ClientLabel: "test browser"})
	if err != nil {
		t.Fatalf("complete signed passkey login: %v", err)
	}
	if issued.Session.UserID != fixture.userID || issued.Session.ClientLabel != "test browser" || issued.Token == "" || issued.Session.AuthenticationMethod != sessions.AuthenticationMethodPasskey || issued.Session.ReauthenticationMethod != sessions.AuthenticationMethodPasskey {
		t.Fatalf("unexpected issued session: %+v", issued.Session)
	}
	if got := fixture.repository.user.Credentials[0].Credential.Authenticator.SignCount; got != 8 {
		t.Fatalf("sign counter = %d, want 8", got)
	}
	if fixture.repository.lastEvent != passkeys.EventAuthenticated {
		t.Fatalf("event = %q", fixture.repository.lastEvent)
	}
	if _, err := fixture.service.CompleteLogin(context.Background(), passkeys.LoginCommand{CeremonyID: begun.CeremonyID, Response: response}); !errors.Is(err, passkeys.ErrInvalidCeremony) {
		t.Fatalf("replay error = %v, want invalid ceremony", err)
	}
}

func TestLoginRejectsTamperedSignatureAndExpiredCeremony(t *testing.T) {
	fixture := newFixture(t)
	begun, err := fixture.service.BeginLogin(context.Background(), [32]byte{2})
	if err != nil {
		t.Fatal(err)
	}
	response := signedAssertion(t, fixture.privateKey, fixture.credentialID, fixture.handle, fixture.repository.ceremonies[begun.CeremonyID].Data.Challenge, 8)
	response = tamperedSignature(t, response)
	if _, err := fixture.service.CompleteLogin(context.Background(), passkeys.LoginCommand{CeremonyID: begun.CeremonyID, Response: response}); !errors.Is(err, passkeys.ErrInvalidCredential) {
		t.Fatalf("tampered signature error = %v", err)
	}

	begun, err = fixture.service.BeginLogin(context.Background(), [32]byte{3})
	if err != nil {
		t.Fatal(err)
	}
	fixture.clock.now = fixture.clock.now.Add(passkeys.CeremonyTTL + time.Second)
	if _, err := fixture.service.CompleteLogin(context.Background(), passkeys.LoginCommand{CeremonyID: begun.CeremonyID, Response: []byte(`{}`)}); !errors.Is(err, passkeys.ErrInvalidCeremony) {
		t.Fatalf("expired ceremony error = %v", err)
	}
}

func TestLoginRejectsAuthenticatorCloneWarning(t *testing.T) {
	fixture := newFixture(t)
	begun, err := fixture.service.BeginLogin(context.Background(), [32]byte{4})
	if err != nil {
		t.Fatal(err)
	}
	response := signedAssertion(t, fixture.privateKey, fixture.credentialID, fixture.handle, fixture.repository.ceremonies[begun.CeremonyID].Data.Challenge, 6)
	if _, err := fixture.service.CompleteLogin(context.Background(), passkeys.LoginCommand{CeremonyID: begun.CeremonyID, Response: response}); !errors.Is(err, passkeys.ErrInvalidCredential) {
		t.Fatalf("counter rollback error = %v", err)
	}
	if fixture.repository.cloneEvents != 1 {
		t.Fatalf("clone warning events = %d, want 1", fixture.repository.cloneEvents)
	}
}

func TestRegistrationOptionsRequireResidentKeyAndUserVerification(t *testing.T) {
	fixture := newFixture(t)
	fixture.repository.user.Handle = nil
	fixture.repository.user.Credentials = nil
	session := sessions.Session{ID: ids.SessionID("00000000-0000-4000-8000-000000000090"), UserID: fixture.userID, ReauthenticatedAt: fixture.clock.now}
	result, err := fixture.service.BeginRegistration(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	var options struct {
		PublicKey struct {
			Attestation            string `json:"attestation"`
			AuthenticatorSelection struct {
				ResidentKey      string `json:"residentKey"`
				UserVerification string `json:"userVerification"`
			} `json:"authenticatorSelection"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(result.PublicKey, &options); err != nil {
		t.Fatal(err)
	}
	if options.PublicKey.Attestation != "none" || options.PublicKey.AuthenticatorSelection.ResidentKey != "required" || options.PublicKey.AuthenticatorSelection.UserVerification != "required" {
		t.Fatalf("unsafe registration options: %+v", options.PublicKey)
	}
	stored := fixture.repository.ceremonies[result.CeremonyID]
	if stored.UserID != fixture.userID || stored.SessionID != session.ID || len(fixture.repository.user.Handle) != 32 {
		t.Fatalf("registration ceremony was not bound to identity and session: %+v", stored)
	}
}

func TestCompletedRegistrationPromotesRecoveredPasswordSessionToStrongAssurance(t *testing.T) {
	fixture := newFixture(t)
	publicKey := append([]byte(nil), fixture.repository.user.Credentials[0].Credential.PublicKey...)
	fixture.repository.user.Credentials = nil
	issued, err := fixture.sessions.IssueForClientWithMethod(context.Background(), fixture.userID, 1, "recovered browser", sessions.AuthenticationMethodPassword)
	if err != nil {
		t.Fatal(err)
	}
	begun, err := fixture.service.BeginRegistration(context.Background(), issued.Session)
	if err != nil {
		t.Fatal(err)
	}
	credentialID := []byte("new-credential-id")
	response := registrationResponse(t, publicKey, credentialID, fixture.repository.ceremonies[begun.CeremonyID].Data.Challenge)
	created, err := fixture.service.CompleteRegistration(context.Background(), issued.Session, begun.CeremonyID, "Recovery key", response)
	if err != nil {
		t.Fatalf("complete registration: %v", err)
	}
	if created.Name != "Recovery key" || created.ID != base64.RawURLEncoding.EncodeToString(credentialID) {
		t.Fatalf("unexpected credential summary: %+v", created)
	}
	authenticated, err := fixture.sessions.Authenticate(context.Background(), issued.Token)
	if err != nil {
		t.Fatal(err)
	}
	if authenticated.Session.AuthenticationMethod != sessions.AuthenticationMethodPassword || authenticated.Session.ReauthenticationMethod != sessions.AuthenticationMethodPasskey ||
		!fixture.sessions.RecentlyReauthenticatedWithAssurance(authenticated.Session, 10*time.Minute, sessions.AssuranceUserVerifiedCryptographic) {
		t.Fatalf("registration did not promote recent assurance: %+v", authenticated.Session)
	}
}

func TestRegistrationAndLastDeletionEnforceRecoveryPolicy(t *testing.T) {
	fixture := newFixture(t)
	policy := &recoveryPolicy{status: recoverycodes.Status{Configured: true, Remaining: recoverycodes.CodeCount}}
	service, err := passkeys.NewService(fixture.repository, fixture.sessions, allowGuard{}, &sequence{}, fixture.clock, passkeys.Config{
		RelyingPartyID: "app.infiniteocean.net",
		Origins:        []string{"https://app.infiniteocean.net"},
		RecoveryPolicy: policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	password := sessions.Session{
		ID:                     "00000000-0000-4000-8000-000000000091",
		UserID:                 fixture.userID,
		ReauthenticatedAt:      fixture.clock.now,
		ReauthenticationMethod: sessions.AuthenticationMethodPassword,
	}
	if _, err := service.BeginRegistration(context.Background(), password); !errors.Is(err, passkeys.ErrReauthenticationNeeded) {
		t.Fatalf("additional passkey with password assurance=%v", err)
	}
	passkey := password
	passkey.ReauthenticationMethod = sessions.AuthenticationMethodPasskey
	if _, err := service.BeginRegistration(context.Background(), passkey); err != nil {
		t.Fatalf("additional passkey with passkey assurance=%v", err)
	}
	if _, err := service.BeginRegistration(context.Background(), password); !errors.Is(err, passkeys.ErrReauthenticationNeeded) {
		t.Fatalf("lost authenticator with retained credential and no recovery grant=%v", err)
	}
	policy.granted = true
	if _, err := service.BeginRegistration(context.Background(), password); err != nil {
		t.Fatalf("lost authenticator with retained credential and recovery grant=%v", err)
	}

	credential := fixture.repository.user.Credentials[0]
	fixture.repository.user.Credentials = nil
	policy.granted = false
	if _, err := service.BeginRegistration(context.Background(), password); !errors.Is(err, passkeys.ErrRecoveryCodeRequired) {
		t.Fatalf("lost-passkey replacement without recovery grant=%v", err)
	}
	policy.granted = true
	if _, err := service.BeginRegistration(context.Background(), password); err != nil {
		t.Fatalf("lost-passkey replacement with recovery grant=%v", err)
	}

	fixture.repository.user.Credentials = []passkeys.CredentialRecord{credential}
	policy.status = recoverycodes.Status{}
	encodedID := base64.RawURLEncoding.EncodeToString(credential.Credential.ID)
	if err := service.Delete(context.Background(), passkey, encodedID); !errors.Is(err, passkeys.ErrRecoveryCodesRequired) {
		t.Fatalf("last passkey deletion without recovery codes=%v", err)
	}
	policy.status = recoverycodes.Status{Configured: true, Remaining: 1}
	if err := service.Delete(context.Background(), passkey, encodedID); err != nil {
		t.Fatalf("last passkey deletion with recovery codes=%v", err)
	}
	if len(fixture.repository.user.Credentials) != 0 {
		t.Fatalf("credentials remaining=%d, want 0", len(fixture.repository.user.Credentials))
	}
}

func TestRenameAndCompromiseRequireStrongAuthAndTargetOwnedCredential(t *testing.T) {
	fixture := newFixture(t)
	encodedID := base64.RawURLEncoding.EncodeToString(fixture.credentialID)
	password := sessions.Session{
		ID:                     "00000000-0000-4000-8000-000000000092",
		UserID:                 fixture.userID,
		ReauthenticatedAt:      fixture.clock.now,
		ReauthenticationMethod: sessions.AuthenticationMethodPassword,
	}
	if err := fixture.service.Rename(context.Background(), password, encodedID, "Office key"); !errors.Is(err, passkeys.ErrReauthenticationNeeded) {
		t.Fatalf("rename with password assurance=%v", err)
	}
	passkey := password
	passkey.ReauthenticationMethod = sessions.AuthenticationMethodPasskey
	if err := fixture.service.Rename(context.Background(), passkey, encodedID, "  Office key  "); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if got := fixture.repository.user.Credentials[0].Name; got != "Office key" {
		t.Fatalf("renamed credential=%q", got)
	}
	if err := fixture.service.Rename(context.Background(), passkey, encodedID, "x"); !errors.Is(err, passkeys.ErrCredentialNameInvalid) {
		t.Fatalf("invalid name error=%v", err)
	}
	if err := fixture.service.Compromise(context.Background(), password, encodedID); !errors.Is(err, passkeys.ErrReauthenticationNeeded) {
		t.Fatalf("compromise with password assurance=%v", err)
	}
	if err := fixture.service.Compromise(context.Background(), passkey, encodedID); err != nil {
		t.Fatalf("compromise last credential: %v", err)
	}
	if len(fixture.repository.user.Credentials) != 0 {
		t.Fatalf("credentials remaining=%d, want 0", len(fixture.repository.user.Credentials))
	}
	if err := fixture.service.Compromise(context.Background(), passkey, encodedID); !errors.Is(err, passkeys.ErrCredentialNotFound) {
		t.Fatalf("repeat compromise error=%v", err)
	}
}

func tamperedSignature(t *testing.T, response []byte) []byte {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(response, &value); err != nil {
		t.Fatal(err)
	}
	assertion := value["response"].(map[string]any)
	raw, err := base64.RawURLEncoding.DecodeString(assertion["signature"].(string))
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 1
	assertion["signature"] = base64.RawURLEncoding.EncodeToString(raw)
	result, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestCipherAuthenticatesPayloadLabelAndKeyVersion(t *testing.T) {
	cipher, err := passkeys.NewCipher(bytes.Repeat([]byte{7}, 32), 3)
	if err != nil {
		t.Fatal(err)
	}
	first, err := cipher.Seal("credential/a", []byte("private credential material"))
	if err != nil {
		t.Fatal(err)
	}
	second, _ := cipher.Seal("credential/a", []byte("private credential material"))
	if bytes.Equal(first.Ciphertext, second.Ciphertext) || bytes.Contains(first.Ciphertext, []byte("private credential material")) {
		t.Fatal("credential envelope was deterministic or exposed plaintext")
	}
	if opened, err := cipher.Open("credential/a", first); err != nil || string(opened) != "private credential material" {
		t.Fatalf("open valid envelope: %q, %v", opened, err)
	}
	if _, err := cipher.Open("credential/b", first); err == nil {
		t.Fatal("changed record label was accepted")
	}
	first.Ciphertext[0] ^= 1
	if _, err := cipher.Open("credential/a", first); err == nil {
		t.Fatal("tampered ciphertext was accepted")
	}
	first.KeyVersion++
	if _, err := cipher.Open("credential/a", first); err == nil {
		t.Fatal("wrong key version was accepted")
	}
}

func TestCipherKeyringReadsOldVersionAndWritesOnlyActiveVersion(t *testing.T) {
	oldKey := bytes.Repeat([]byte{0x31}, 32)
	newKey := bytes.Repeat([]byte{0x42}, 32)
	oldCipher, err := passkeys.NewCipher(oldKey, 1)
	if err != nil {
		t.Fatal(err)
	}
	oldEnvelope, err := oldCipher.Seal("credential/a", []byte("old credential"))
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := passkeys.NewCipherKeyring(map[int][]byte{1: oldKey, 2: newKey}, 2)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := keyring.Open("credential/a", oldEnvelope)
	if err != nil || string(opened) != "old credential" {
		t.Fatalf("old envelope=%q err=%v", opened, err)
	}
	newEnvelope, err := keyring.Seal("credential/a", []byte("new credential"))
	if err != nil || newEnvelope.KeyVersion != 2 || keyring.ActiveVersion() != 2 {
		t.Fatalf("new envelope version=%d active=%d err=%v", newEnvelope.KeyVersion, keyring.ActiveVersion(), err)
	}
	newOnly, _ := passkeys.NewCipher(newKey, 2)
	if opened, err := newOnly.Open("credential/a", newEnvelope); err != nil || string(opened) != "new credential" {
		t.Fatalf("active envelope=%q err=%v", opened, err)
	}
	if _, err := newOnly.Open("credential/a", oldEnvelope); err == nil {
		t.Fatal("envelope using an omitted old version was accepted")
	}
}

type fixture struct {
	service      *passkeys.Service
	sessions     *sessions.Service
	repository   *repository
	clock        *testClock
	privateKey   *ecdsa.PrivateKey
	credentialID []byte
	handle       []byte
	userID       ids.UserID
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := webauthncbor.Marshal(webauthncose.EC2PublicKeyData{
		PublicKeyData: webauthncose.PublicKeyData{KeyType: int64(webauthncose.EllipticKey), Algorithm: int64(webauthncose.AlgES256)},
		Curve:         int64(webauthncose.P256), XCoord: privateKey.X.FillBytes(make([]byte, 32)), YCoord: privateKey.Y.FillBytes(make([]byte, 32)),
	})
	if err != nil {
		t.Fatal(err)
	}
	userID := ids.UserID("00000000-0000-4000-8000-000000000001")
	handle := bytes.Repeat([]byte{4}, 32)
	credentialID := []byte("credential-id-0001")
	repository := &repository{user: passkeys.User{
		Identity:    identity.User{ID: userID, PrimaryEmail: "owner@example.com", DisplayName: "Owner", State: identity.UserActive, SecurityVersion: 1},
		Handle:      handle,
		Credentials: []passkeys.CredentialRecord{{UserID: userID, Name: "Test key", Credential: webauthn.Credential{ID: credentialID, PublicKey: publicKey, Authenticator: webauthn.Authenticator{SignCount: 7}}}},
	}, ceremonies: map[string]passkeys.Ceremony{}}
	// go-webauthn validates SessionData.Expires against the process wall clock.
	// Keep the injected application clock deterministic but safely ahead of the
	// wall clock so this fixture does not become date-dependent.
	clock := &testClock{now: time.Date(2099, 8, 18, 12, 0, 0, 0, time.UTC)}
	sessionService, err := sessions.NewService(memory.NewSessionStore(), &sequence{}, clock, time.Hour, 30*time.Minute, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	service, err := passkeys.NewService(repository, sessionService, allowGuard{}, &sequence{}, clock, passkeys.Config{RelyingPartyID: "app.infiniteocean.net", Origins: []string{"https://app.infiniteocean.net"}})
	if err != nil {
		t.Fatal(err)
	}
	return fixture{service: service, sessions: sessionService, repository: repository, clock: clock, privateKey: privateKey, credentialID: credentialID, handle: handle, userID: userID}
}

func registrationResponse(t *testing.T, publicKey, credentialID []byte, challenge string) []byte {
	t.Helper()
	clientData := []byte(fmt.Sprintf(`{"type":"webauthn.create","challenge":%q,"origin":"https://app.infiniteocean.net"}`, challenge))
	rpHash := sha256.Sum256([]byte("app.infiniteocean.net"))
	authenticatorData := make([]byte, 0, 37+16+2+len(credentialID)+len(publicKey))
	authenticatorData = append(authenticatorData, rpHash[:]...)
	authenticatorData = append(authenticatorData, 0x45) // user present, user verified, attested credential data
	authenticatorData = append(authenticatorData, 0, 0, 0, 0)
	authenticatorData = append(authenticatorData, make([]byte, 16)...)
	credentialLength := make([]byte, 2)
	binary.BigEndian.PutUint16(credentialLength, uint16(len(credentialID)))
	authenticatorData = append(authenticatorData, credentialLength...)
	authenticatorData = append(authenticatorData, credentialID...)
	authenticatorData = append(authenticatorData, publicKey...)
	attestation, err := webauthncbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": authenticatorData})
	if err != nil {
		t.Fatal(err)
	}
	encodedID := base64.RawURLEncoding.EncodeToString(credentialID)
	response := map[string]any{
		"id": encodedID, "rawId": encodedID, "type": "public-key",
		"response": map[string]any{
			"clientDataJSON":    base64.RawURLEncoding.EncodeToString(clientData),
			"attestationObject": base64.RawURLEncoding.EncodeToString(attestation),
			"transports":        []string{"internal"},
		},
	}
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func signedAssertion(t *testing.T, privateKey *ecdsa.PrivateKey, credentialID, userHandle []byte, challenge string, counter uint32) []byte {
	t.Helper()
	return signedAssertionAtOrigin(t, privateKey, credentialID, userHandle, challenge, counter, "https://app.infiniteocean.net")
}

func signedAssertionAtOrigin(t *testing.T, privateKey *ecdsa.PrivateKey, credentialID, userHandle []byte, challenge string, counter uint32, origin string) []byte {
	t.Helper()
	clientData := []byte(fmt.Sprintf(`{"type":"webauthn.get","challenge":%q,"origin":%q}`, challenge, origin))
	rpHash := sha256.Sum256([]byte("app.infiniteocean.net"))
	authenticatorData := make([]byte, 37)
	copy(authenticatorData, rpHash[:])
	authenticatorData[32] = 0x05
	binary.BigEndian.PutUint32(authenticatorData[33:], counter)
	clientHash := sha256.Sum256(clientData)
	signed := append(append([]byte(nil), authenticatorData...), clientHash[:]...)
	digest := sha256.Sum256(signed)
	signature, err := ecdsa.SignASN1(rand.Reader, privateKey, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	encodedID := base64.RawURLEncoding.EncodeToString(credentialID)
	response := map[string]any{
		"id": encodedID, "rawId": encodedID, "type": "public-key",
		"response": map[string]string{
			"authenticatorData": base64.RawURLEncoding.EncodeToString(authenticatorData),
			"clientDataJSON":    base64.RawURLEncoding.EncodeToString(clientData),
			"signature":         base64.RawURLEncoding.EncodeToString(signature),
			"userHandle":        base64.RawURLEncoding.EncodeToString(userHandle),
		},
	}
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestRelatedOperationsOriginKeepsAppRPAndRejectsOtherOrigins(t *testing.T) {
	for _, origin := range []string{"https://ops.infiniteocean.net", "https://app.infiniteocean.net", "https://untrusted.infiniteocean.net"} {
		t.Run(origin, func(t *testing.T) {
			fixture := newFixture(t)
			service, err := passkeys.NewService(fixture.repository, fixture.sessions, allowGuard{}, &sequence{}, fixture.clock, passkeys.Config{RelyingPartyID: "app.infiniteocean.net", Origins: []string{"https://ops.infiniteocean.net"}})
			if err != nil {
				t.Fatal(err)
			}
			begun, err := service.BeginLogin(context.Background(), [32]byte{1})
			if err != nil {
				t.Fatal(err)
			}
			response := signedAssertionAtOrigin(t, fixture.privateKey, fixture.credentialID, fixture.handle, fixture.repository.ceremonies[begun.CeremonyID].Data.Challenge, 8, origin)
			_, err = service.CompleteLogin(context.Background(), passkeys.LoginCommand{CeremonyID: begun.CeremonyID, Response: response, ClientLabel: "Related origin test"})
			if origin == "https://ops.infiniteocean.net" && err != nil {
				t.Fatalf("related origin rejected: %v", err)
			}
			if origin != "https://ops.infiniteocean.net" && !errors.Is(err, passkeys.ErrInvalidCredential) {
				t.Fatalf("unapproved origin accepted: %v", err)
			}
		})
	}
}

type repository struct {
	user        passkeys.User
	ceremonies  map[string]passkeys.Ceremony
	lastEvent   passkeys.CredentialEvent
	cloneEvents int
}

func (r *repository) EnsureUser(_ context.Context, _ ids.UserID, handle []byte, _ time.Time) (passkeys.User, error) {
	if len(r.user.Handle) == 0 {
		r.user.Handle = append([]byte(nil), handle...)
	}
	return r.user, nil
}
func (r *repository) User(context.Context, ids.UserID) (passkeys.User, error) { return r.user, nil }
func (r *repository) UserByHandle(_ context.Context, handle, credentialID []byte) (passkeys.User, error) {
	if !bytes.Equal(handle, r.user.Handle) || !bytes.Equal(credentialID, r.user.Credentials[0].Credential.ID) {
		return passkeys.User{}, passkeys.ErrCredentialNotFound
	}
	return r.user, nil
}
func (r *repository) CreateCeremony(_ context.Context, value passkeys.Ceremony) error {
	r.ceremonies[value.ID] = value
	return nil
}
func (r *repository) ConsumeCeremony(_ context.Context, id string, kind passkeys.CeremonyKind, userID ids.UserID, sessionID ids.SessionID, now time.Time) (passkeys.Ceremony, error) {
	value, ok := r.ceremonies[id]
	if !ok || value.Kind != kind || value.UserID != userID || value.SessionID != sessionID || !value.ExpiresAt.After(now) {
		return passkeys.Ceremony{}, passkeys.ErrInvalidCeremony
	}
	delete(r.ceremonies, id)
	return value, nil
}
func (r *repository) CreateCredential(_ context.Context, userID ids.UserID, name string, credential webauthn.Credential, now time.Time, _ int) error {
	r.user.Credentials = append(r.user.Credentials, passkeys.CredentialRecord{UserID: userID, Name: name, Credential: credential, CreatedAt: now})
	return nil
}
func (r *repository) UpdateCredential(_ context.Context, _ ids.UserID, credentialID []byte, expected uint32, credential webauthn.Credential, event passkeys.CredentialEvent, now time.Time) (bool, error) {
	for index := range r.user.Credentials {
		if bytes.Equal(r.user.Credentials[index].Credential.ID, credentialID) && r.user.Credentials[index].Credential.Authenticator.SignCount == expected {
			r.user.Credentials[index].Credential = credential
			r.user.Credentials[index].LastUsedAt = &now
			r.lastEvent = event
			return true, nil
		}
	}
	return false, nil
}
func (r *repository) RecordCloneWarning(context.Context, ids.UserID, []byte, time.Time) error {
	r.cloneEvents++
	return nil
}
func (r *repository) ListCredentials(context.Context, ids.UserID) ([]passkeys.CredentialRecord, error) {
	return r.user.Credentials, nil
}
func (r *repository) RenameCredential(_ context.Context, userID ids.UserID, credentialID []byte, name string, _ time.Time) (bool, error) {
	if userID != r.user.Identity.ID {
		return false, nil
	}
	for index := range r.user.Credentials {
		if bytes.Equal(r.user.Credentials[index].Credential.ID, credentialID) {
			r.user.Credentials[index].Name = name
			return true, nil
		}
	}
	return false, nil
}
func (r *repository) DeleteCredential(_ context.Context, userID ids.UserID, credentialID []byte, allowLast bool, _ time.Time) (bool, error) {
	if userID != r.user.Identity.ID {
		return false, nil
	}
	for index := range r.user.Credentials {
		if bytes.Equal(r.user.Credentials[index].Credential.ID, credentialID) {
			if len(r.user.Credentials) == 1 && !allowLast {
				return false, passkeys.ErrRecoveryCodesRequired
			}
			r.user.Credentials = append(r.user.Credentials[:index], r.user.Credentials[index+1:]...)
			return true, nil
		}
	}
	return false, nil
}
func (r *repository) CompromiseCredential(_ context.Context, userID ids.UserID, credentialID []byte, _ time.Time) (bool, error) {
	if userID != r.user.Identity.ID {
		return false, nil
	}
	for index := range r.user.Credentials {
		if bytes.Equal(r.user.Credentials[index].Credential.ID, credentialID) {
			r.user.Credentials = append(r.user.Credentials[:index], r.user.Credentials[index+1:]...)
			return true, nil
		}
	}
	return false, nil
}

type recoveryPolicy struct {
	status  recoverycodes.Status
	granted bool
}

func (p *recoveryPolicy) Status(context.Context, sessions.Session) (recoverycodes.Status, error) {
	return p.status, nil
}

func (p *recoveryPolicy) Granted(context.Context, sessions.Session) (bool, error) {
	return p.granted, nil
}

type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time { return c.now }

type sequence struct{ next int }

func (s *sequence) New() string {
	s.next++
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", s.next)
}

type allowGuard struct{}

func (allowGuard) Allow(context.Context, abuse.Scope, [32]byte, time.Time, abuse.Policy) (bool, error) {
	return true, nil
}

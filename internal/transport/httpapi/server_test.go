package httpapi_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"

	"github.com/tinfoyle/spyglass-engine/internal/bootstrap/development"
	"github.com/tinfoyle/spyglass-engine/internal/testsupport/openapifixture"
)

var (
	responseContractOnce sync.Once
	responseContract     *openapifixture.Contract
	responseContractErr  error
)

func TestPublicCatalogDoesNotLeakStripeReferences(t *testing.T) {
	server := httptest.NewServer(development.Handler(slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	response, err := http.Get(server.URL + "/api/v1/catalog/public")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	validateOpenAPIResponse(t, http.MethodGet, server.URL+"/api/v1/catalog/public", response.StatusCode, response.Header, body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", response.StatusCode, body)
	}
	if bytes.Contains(body, []byte("stripe")) {
		t.Fatalf("public catalog leaked provider mapping: %s", body)
	}
	if !bytes.Contains(body, []byte(`"work"`)) || !bytes.Contains(body, []byte(`"agents"`)) {
		t.Fatalf("catalog missing packages: %s", body)
	}
	if !bytes.Contains(body, []byte(`"concurrent_runs"`)) || !bytes.Contains(body, []byte(`"combine":"maximum"`)) {
		t.Fatalf("catalog missing governed limit definitions: %s", body)
	}
}

func TestRegistrationHTTPJourney(t *testing.T) {
	server := httptest.NewServer(development.Handler(slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	invalidOffer := postJSON(t, server.URL+"/api/v1/registrations", `{"email":"invalid-offer@example.com","display_name":"Invalid Offer","account_name":"Northstar Studio","region":"us-east","offer_code":"invented-offer"}`)
	if invalidOffer.StatusCode != http.StatusBadRequest || !bytes.Contains(invalidOffer.Body, []byte(`"code":"offer_unavailable"`)) {
		t.Fatalf("invalid offer status %d: %s", invalidOffer.StatusCode, invalidOffer.Body)
	}
	begin := postJSON(t, server.URL+"/api/v1/registrations", `{"email":"avery@example.com","display_name":"Avery Johnson","account_name":"Northstar Studio","region":"us-east","offer_code":"team-monthly-v1"}`)
	if begin.StatusCode != http.StatusAccepted {
		t.Fatalf("begin status %d: %s", begin.StatusCode, begin.Body)
	}
	var accepted map[string]any
	if err := json.Unmarshal(begin.Body, &accepted); err != nil {
		t.Fatal(err)
	}
	token, ok := accepted["development_verification_token"].(string)
	if !ok || token == "" {
		t.Fatal("development token missing")
	}
	complete := postJSON(t, server.URL+"/api/v1/registrations/verify", `{"token":"`+token+`","password":"correct horse battery staple"}`)
	if complete.StatusCode != http.StatusCreated {
		t.Fatalf("complete status %d: %s", complete.StatusCode, complete.Body)
	}
	if !bytes.Contains(complete.Body, []byte(`"role":"owner"`)) || !bytes.Contains(complete.Body, []byte(`"type":"free"`)) {
		t.Fatalf("unexpected response: %s", complete.Body)
	}
	var provisioned struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
	}
	if err := json.Unmarshal(complete.Body, &provisioned); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(complete.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("sensitive response was cacheable")
	}
	login := postJSON(t, server.URL+"/api/v1/sessions", `{"email":"avery@example.com","password":"correct horse battery staple"}`)
	if login.StatusCode != http.StatusCreated {
		t.Fatalf("login status %d: %s", login.StatusCode, login.Body)
	}
	if !bytes.Contains(login.Body, []byte(`"authentication_method":"password"`)) || !bytes.Contains(login.Body, []byte(`"authentication_assurance":"single_factor"`)) {
		t.Fatalf("password login assurance missing: %s", login.Body)
	}
	cookies := (&http.Response{Header: login.Header}).Cookies()
	if len(cookies) != 1 || cookies[0].Name != "spyglass_development_session" || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("unexpected session cookie: %#v", cookies)
	}
	initialPosture := requestJSONCookie(t, http.MethodGet, server.URL+"/api/v1/security-posture", "", cookies[0])
	if initialPosture.StatusCode != http.StatusOK || !bytes.Contains(initialPosture.Body, []byte(`"passkey_count":0`)) || !bytes.Contains(initialPosture.Body, []byte(`"recovery_codes_configured":false`)) || !bytes.Contains(initialPosture.Body, []byte(`"owner_ready":false`)) {
		t.Fatalf("initial security posture: %d %s", initialPosture.StatusCode, initialPosture.Body)
	}
	secondLogin := postJSON(t, server.URL+"/api/v1/sessions", `{"email":"avery@example.com","password":"correct horse battery staple"}`)
	if secondLogin.StatusCode != http.StatusCreated {
		t.Fatalf("second login status %d: %s", secondLogin.StatusCode, secondLogin.Body)
	}
	secondCookies := (&http.Response{Header: secondLogin.Header}).Cookies()
	passkeyListRequest, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/passkeys", nil)
	passkeyListRequest.AddCookie(cookies[0])
	passkeyListResponse, err := http.DefaultClient.Do(passkeyListRequest)
	if err != nil {
		t.Fatal(err)
	}
	passkeyListBody, _ := io.ReadAll(passkeyListResponse.Body)
	passkeyListResponse.Body.Close()
	validateOpenAPIResponse(t, http.MethodGet, passkeyListRequest.URL.String(), passkeyListResponse.StatusCode, passkeyListResponse.Header, passkeyListBody)
	if passkeyListResponse.StatusCode != http.StatusOK || !bytes.Contains(passkeyListBody, []byte(`"passkeys":[]`)) || bytes.Contains(passkeyListBody, []byte(provisioned.Account.ID)) {
		t.Fatalf("identity-only passkey list: %d %s", passkeyListResponse.StatusCode, passkeyListBody)
	}
	passkeyRegistration := postJSONCookie(t, server.URL+"/api/v1/passkey-registrations", `{}`, cookies[0])
	if passkeyRegistration.StatusCode != http.StatusCreated || !bytes.Contains(passkeyRegistration.Body, []byte(`"residentKey":"required"`)) || !bytes.Contains(passkeyRegistration.Body, []byte(`"userVerification":"required"`)) {
		t.Fatalf("passkey registration options: %d %s", passkeyRegistration.StatusCode, passkeyRegistration.Body)
	}
	var passkeyCeremony struct {
		CeremonyID string `json:"ceremony_id"`
	}
	if err := json.Unmarshal(passkeyRegistration.Body, &passkeyCeremony); err != nil {
		t.Fatal(err)
	}
	crossSessionCompletion := postJSONCookie(t, server.URL+"/api/v1/passkey-registrations/"+passkeyCeremony.CeremonyID+"/complete", `{"name":"Test passkey","credential":{}}`, secondCookies[0])
	if crossSessionCompletion.StatusCode != http.StatusBadRequest || !bytes.Contains(crossSessionCompletion.Body, []byte(`"code":"passkey_invalid"`)) {
		t.Fatalf("cross-session passkey ceremony: %d %s", crossSessionCompletion.StatusCode, crossSessionCompletion.Body)
	}
	ownerCompletion := postJSONCookie(t, server.URL+"/api/v1/passkey-registrations/"+passkeyCeremony.CeremonyID+"/complete", `{"name":"Test passkey","credential":{}}`, cookies[0])
	if ownerCompletion.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid registration credential: %d %s", ownerCompletion.StatusCode, ownerCompletion.Body)
	}
	passkeyLogin := postJSON(t, server.URL+"/api/v1/passkey-login/challenges", `{}`)
	if passkeyLogin.StatusCode != http.StatusCreated || !bytes.Contains(passkeyLogin.Body, []byte(`"userVerification":"required"`)) {
		t.Fatalf("passkey login options: %d %s", passkeyLogin.StatusCode, passkeyLogin.Body)
	}
	sessionsRequest, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/sessions", nil)
	sessionsRequest.AddCookie(cookies[0])
	sessionsResponse, err := http.DefaultClient.Do(sessionsRequest)
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		Sessions []struct {
			ID                      string `json:"id"`
			Current                 bool   `json:"current"`
			AuthenticationMethod    string `json:"authentication_method"`
			AuthenticationAssurance string `json:"authentication_assurance"`
		} `json:"sessions"`
	}
	sessionsBody, _ := io.ReadAll(sessionsResponse.Body)
	if err := json.Unmarshal(sessionsBody, &inventory); err != nil {
		t.Fatal(err)
	}
	sessionsResponse.Body.Close()
	validateOpenAPIResponse(t, http.MethodGet, sessionsRequest.URL.String(), sessionsResponse.StatusCode, sessionsResponse.Header, sessionsBody)
	if sessionsResponse.StatusCode != http.StatusOK || len(inventory.Sessions) != 2 {
		t.Fatalf("session inventory: %d %+v", sessionsResponse.StatusCode, inventory.Sessions)
	}
	otherSessionID := ""
	for _, item := range inventory.Sessions {
		if item.AuthenticationMethod != "password" || item.AuthenticationAssurance != "single_factor" {
			t.Fatalf("password session assurance = %+v", item)
		}
		if !item.Current {
			otherSessionID = item.ID
		}
	}
	if otherSessionID == "" {
		t.Fatal("session inventory did not distinguish the current session")
	}
	revokeRequest, _ := http.NewRequest(http.MethodDelete, server.URL+"/api/v1/sessions/"+otherSessionID, nil)
	revokeRequest.AddCookie(cookies[0])
	revokeResponse, err := http.DefaultClient.Do(revokeRequest)
	if err != nil {
		t.Fatal(err)
	}
	revokeBody, _ := io.ReadAll(revokeResponse.Body)
	revokeResponse.Body.Close()
	validateOpenAPIResponse(t, http.MethodDelete, revokeRequest.URL.String(), revokeResponse.StatusCode, revokeResponse.Header, revokeBody)
	if revokeResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke other session status: %d", revokeResponse.StatusCode)
	}
	wrongConfirmation := postJSONCookie(t, server.URL+"/api/v1/session/reauthenticate", `{"password":"wrong password"}`, cookies[0])
	if wrongConfirmation.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong reauthentication status: %d", wrongConfirmation.StatusCode)
	}
	confirmation := postJSONCookie(t, server.URL+"/api/v1/session/reauthenticate", `{"password":"correct horse battery staple"}`, cookies[0])
	if confirmation.StatusCode != http.StatusNoContent {
		t.Fatalf("reauthentication status: %d %s", confirmation.StatusCode, confirmation.Body)
	}
	securityEventsRequest, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/security-events", nil)
	securityEventsRequest.AddCookie(cookies[0])
	securityEventsResponse, err := http.DefaultClient.Do(securityEventsRequest)
	if err != nil {
		t.Fatal(err)
	}
	securityEventsBody, _ := io.ReadAll(securityEventsResponse.Body)
	securityEventsResponse.Body.Close()
	validateOpenAPIResponse(t, http.MethodGet, securityEventsRequest.URL.String(), securityEventsResponse.StatusCode, securityEventsResponse.Header, securityEventsBody)
	if securityEventsResponse.StatusCode != http.StatusOK || !bytes.Contains(securityEventsBody, []byte(`"type":"session_created"`)) || !bytes.Contains(securityEventsBody, []byte(`"type":"session_reauthenticated"`)) {
		t.Fatalf("security events response: %d %s", securityEventsResponse.StatusCode, securityEventsBody)
	}
	accountsRequest, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/session/accounts", nil)
	accountsRequest.AddCookie(cookies[0])
	accountsResponse, err := http.DefaultClient.Do(accountsRequest)
	if err != nil {
		t.Fatal(err)
	}
	accountsBody, _ := io.ReadAll(accountsResponse.Body)
	accountsResponse.Body.Close()
	validateOpenAPIResponse(t, http.MethodGet, accountsRequest.URL.String(), accountsResponse.StatusCode, accountsResponse.Header, accountsBody)
	if accountsResponse.StatusCode != http.StatusOK || !bytes.Contains(accountsBody, []byte(provisioned.Account.ID)) || !bytes.Contains(accountsBody, []byte(`"owner_enrollment_required":true`)) {
		t.Fatalf("accounts response: %d %s", accountsResponse.StatusCode, accountsBody)
	}
	selectRequest, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/session/account", strings.NewReader(`{"account_id":"`+provisioned.Account.ID+`"}`))
	selectRequest.Header.Set("Content-Type", "application/json")
	selectRequest.AddCookie(cookies[0])
	selectResponse, err := http.DefaultClient.Do(selectRequest)
	if err != nil {
		t.Fatal(err)
	}
	selectBody, _ := io.ReadAll(selectResponse.Body)
	selectResponse.Body.Close()
	validateOpenAPIResponse(t, http.MethodPost, selectRequest.URL.String(), selectResponse.StatusCode, selectResponse.Header, selectBody)
	if selectResponse.StatusCode != http.StatusForbidden || !bytes.Contains(selectBody, []byte(`"code":"owner_security_enrollment_required"`)) {
		t.Fatalf("unenrolled owner select response: %d %s", selectResponse.StatusCode, selectBody)
	}
	invite := postJSONCookie(t, server.URL+"/api/v1/accounts/"+provisioned.Account.ID+"/invitations", `{"email":"member@example.com","role":"member"}`, cookies[0])
	if invite.StatusCode != http.StatusForbidden || !bytes.Contains(invite.Body, []byte(`"code":"owner_security_enrollment_required"`)) {
		t.Fatalf("unenrolled owner invitation: %d %s", invite.StatusCode, invite.Body)
	}
	strongRegistration := postJSONCookie(t, server.URL+"/api/v1/passkey-registrations", `{}`, cookies[0])
	if strongRegistration.StatusCode != http.StatusCreated {
		t.Fatalf("strong passkey registration options: %d %s", strongRegistration.StatusCode, strongRegistration.Body)
	}
	var strongCeremony struct {
		CeremonyID string `json:"ceremony_id"`
		PublicKey  struct {
			PublicKey struct {
				Challenge string `json:"challenge"`
			} `json:"publicKey"`
		} `json:"public_key"`
	}
	if err := json.Unmarshal(strongRegistration.Body, &strongCeremony); err != nil || strongCeremony.CeremonyID == "" || strongCeremony.PublicKey.PublicKey.Challenge == "" {
		t.Fatalf("decode strong passkey ceremony: %+v err=%v", strongCeremony, err)
	}
	credential := registrationCredential(t, strongCeremony.PublicKey.PublicKey.Challenge)
	strongCompletion := postJSONCookie(t, server.URL+"/api/v1/passkey-registrations/"+strongCeremony.CeremonyID+"/complete", `{"name":"Test passkey","credential":`+credential+`}`, cookies[0])
	if strongCompletion.StatusCode != http.StatusCreated {
		t.Fatalf("strong passkey enrollment: %d %s", strongCompletion.StatusCode, strongCompletion.Body)
	}
	var enrolledPasskey struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(strongCompletion.Body, &enrolledPasskey); err != nil || enrolledPasskey.ID == "" {
		t.Fatalf("decode enrolled passkey: %+v err=%v", enrolledPasskey, err)
	}
	ownerEnrollmentCodes := postJSONCookie(t, server.URL+"/api/v1/recovery-codes", `{}`, cookies[0])
	if ownerEnrollmentCodes.StatusCode != http.StatusCreated || !bytes.Contains(ownerEnrollmentCodes.Body, []byte(`"remaining":10`)) {
		t.Fatalf("owner recovery-code enrollment: %d %s", ownerEnrollmentCodes.StatusCode, ownerEnrollmentCodes.Body)
	}
	readyPosture := requestJSONCookie(t, http.MethodGet, server.URL+"/api/v1/security-posture", "", cookies[0])
	if readyPosture.StatusCode != http.StatusOK || !bytes.Contains(readyPosture.Body, []byte(`"passkey_count":1`)) || !bytes.Contains(readyPosture.Body, []byte(`"recovery_codes_remaining":10`)) || !bytes.Contains(readyPosture.Body, []byte(`"owner_ready":true`)) {
		t.Fatalf("ready security posture: %d %s", readyPosture.StatusCode, readyPosture.Body)
	}
	selectRequest, _ = http.NewRequest(http.MethodPost, server.URL+"/api/v1/session/account", strings.NewReader(`{"account_id":"`+provisioned.Account.ID+`"}`))
	selectRequest.Header.Set("Content-Type", "application/json")
	selectRequest.AddCookie(cookies[0])
	selectResponse, err = http.DefaultClient.Do(selectRequest)
	if err != nil {
		t.Fatal(err)
	}
	selectBody, _ = io.ReadAll(selectResponse.Body)
	selectResponse.Body.Close()
	validateOpenAPIResponse(t, http.MethodPost, selectRequest.URL.String(), selectResponse.StatusCode, selectResponse.Header, selectBody)
	if selectResponse.StatusCode != http.StatusOK || !bytes.Contains(selectBody, []byte(`"placement_generation":1`)) {
		t.Fatalf("ready owner select response: %d %s", selectResponse.StatusCode, selectBody)
	}
	selectedCookies := selectResponse.Cookies()
	if len(selectedCookies) != 1 || selectedCookies[0].Name != "spyglass_development_account" || !selectedCookies[0].HttpOnly {
		t.Fatalf("unexpected account cookie: %#v", selectedCookies)
	}
	invite = postJSONCookie(t, server.URL+"/api/v1/accounts/"+provisioned.Account.ID+"/invitations", `{"email":"member@example.com","role":"member"}`, cookies[0])
	if invite.StatusCode != http.StatusCreated {
		t.Fatalf("passkey-confirmed invite: %d %s", invite.StatusCode, invite.Body)
	}
	var invited map[string]any
	if err := json.Unmarshal(invite.Body, &invited); err != nil {
		t.Fatal(err)
	}
	invitationToken, _ := invited["development_invitation_token"].(string)
	if invitationToken == "" {
		t.Fatal("development invitation token missing")
	}
	memberBegin := postJSON(t, server.URL+"/api/v1/registrations", `{"email":"member@example.com","display_name":"Morgan Lee","account_name":"Member Sandbox","region":"us-east"}`)
	var memberPending map[string]any
	if err := json.Unmarshal(memberBegin.Body, &memberPending); err != nil {
		t.Fatal(err)
	}
	memberToken, _ := memberPending["development_verification_token"].(string)
	memberComplete := postJSON(t, server.URL+"/api/v1/registrations/verify", `{"token":"`+memberToken+`","password":"member password material"}`)
	if memberComplete.StatusCode != http.StatusCreated {
		t.Fatalf("member registration: %d %s", memberComplete.StatusCode, memberComplete.Body)
	}
	memberLogin := postJSON(t, server.URL+"/api/v1/sessions", `{"email":"member@example.com","password":"member password material"}`)
	memberCookies := (&http.Response{Header: memberLogin.Header}).Cookies()
	if len(memberCookies) != 1 {
		t.Fatalf("member login: %d %s", memberLogin.StatusCode, memberLogin.Body)
	}
	acceptedInvitation := postJSONCookie(t, server.URL+"/api/v1/invitations/accept", `{"token":"`+invitationToken+`"}`, memberCookies[0])
	if acceptedInvitation.StatusCode != http.StatusCreated || !bytes.Contains(acceptedInvitation.Body, []byte(`"account_id":"`+provisioned.Account.ID+`"`)) || bytes.Contains(acceptedInvitation.Body, []byte(`"AccountID"`)) {
		t.Fatalf("accept invitation: %d %s", acceptedInvitation.StatusCode, acceptedInvitation.Body)
	}
	memberAccountsRequest, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/session/accounts", nil)
	memberAccountsRequest.AddCookie(memberCookies[0])
	memberAccountsResponse, err := http.DefaultClient.Do(memberAccountsRequest)
	if err != nil {
		t.Fatal(err)
	}
	memberAccountsBody, _ := io.ReadAll(memberAccountsResponse.Body)
	memberAccountsResponse.Body.Close()
	validateOpenAPIResponse(t, http.MethodGet, memberAccountsRequest.URL.String(), memberAccountsResponse.StatusCode, memberAccountsResponse.Header, memberAccountsBody)
	if memberAccountsResponse.StatusCode != http.StatusOK || !bytes.Contains(memberAccountsBody, []byte(provisioned.Account.ID)) {
		t.Fatalf("invited account missing: %d %s", memberAccountsResponse.StatusCode, memberAccountsBody)
	}
	membershipURL := server.URL + "/api/v1/accounts/" + provisioned.Account.ID + "/memberships"
	membershipList := requestJSONCookie(t, http.MethodGet, membershipURL, "", cookies[0])
	var roster struct {
		Memberships []struct {
			MembershipID string `json:"membership_id"`
			Email        string `json:"email"`
			Role         string `json:"role"`
			Version      uint64 `json:"version"`
		} `json:"memberships"`
	}
	if err := json.Unmarshal(membershipList.Body, &roster); err != nil || membershipList.StatusCode != http.StatusOK || len(roster.Memberships) != 2 {
		t.Fatalf("initial Membership roster: %d %+v err=%v body=%s", membershipList.StatusCode, roster, err, membershipList.Body)
	}
	ownerMembershipID, memberMembershipID := "", ""
	var ownerVersion, memberVersion uint64
	for _, item := range roster.Memberships {
		switch item.Email {
		case "avery@example.com":
			ownerMembershipID, ownerVersion = item.MembershipID, item.Version
		case "member@example.com":
			memberMembershipID, memberVersion = item.MembershipID, item.Version
		}
	}
	if ownerMembershipID == "" || memberMembershipID == "" || ownerVersion != 1 || memberVersion != 1 {
		t.Fatalf("Membership identities/versions: %+v", roster.Memberships)
	}
	var memberChoices struct {
		Accounts []struct {
			AccountID string `json:"account_id"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(memberAccountsBody, &memberChoices); err != nil {
		t.Fatal(err)
	}
	sandboxAccountID := ""
	for _, choice := range memberChoices.Accounts {
		if choice.AccountID != provisioned.Account.ID {
			sandboxAccountID = choice.AccountID
		}
	}
	if sandboxAccountID == "" {
		t.Fatalf("member sandbox Account missing: %s", memberAccountsBody)
	}
	memberPasskeyRegistration := postJSONCookie(t, server.URL+"/api/v1/passkey-registrations", `{}`, memberCookies[0])
	var memberPasskeyCeremony struct {
		CeremonyID string `json:"ceremony_id"`
		PublicKey  struct {
			PublicKey struct {
				Challenge string `json:"challenge"`
			} `json:"publicKey"`
		} `json:"public_key"`
	}
	if err := json.Unmarshal(memberPasskeyRegistration.Body, &memberPasskeyCeremony); err != nil || memberPasskeyRegistration.StatusCode != http.StatusCreated {
		t.Fatalf("new-owner passkey ceremony: %d %+v err=%v", memberPasskeyRegistration.StatusCode, memberPasskeyCeremony, err)
	}
	memberCredential := registrationCredential(t, memberPasskeyCeremony.PublicKey.PublicKey.Challenge)
	memberPasskeyCompletion := postJSONCookie(t, server.URL+"/api/v1/passkey-registrations/"+memberPasskeyCeremony.CeremonyID+"/complete", `{"name":"New owner passkey","credential":`+memberCredential+`}`, memberCookies[0])
	if memberPasskeyCompletion.StatusCode != http.StatusCreated {
		t.Fatalf("new-owner passkey enrollment: %d %s", memberPasskeyCompletion.StatusCode, memberPasskeyCompletion.Body)
	}
	memberRecoveryCodes := postJSONCookie(t, server.URL+"/api/v1/recovery-codes", `{}`, memberCookies[0])
	if memberRecoveryCodes.StatusCode != http.StatusCreated || !bytes.Contains(memberRecoveryCodes.Body, []byte(`"remaining":10`)) {
		t.Fatalf("new-owner recovery-code enrollment: %d %s", memberRecoveryCodes.StatusCode, memberRecoveryCodes.Body)
	}
	sandboxRoster := requestJSONCookie(t, http.MethodGet, server.URL+"/api/v1/accounts/"+sandboxAccountID+"/memberships", "", memberCookies[0])
	var sandboxMembers struct {
		Memberships []struct {
			MembershipID string `json:"membership_id"`
		} `json:"memberships"`
	}
	if err := json.Unmarshal(sandboxRoster.Body, &sandboxMembers); err != nil || sandboxRoster.StatusCode != http.StatusOK || len(sandboxMembers.Memberships) != 1 {
		t.Fatalf("sandbox Membership roster: %d %s err=%v", sandboxRoster.StatusCode, sandboxRoster.Body, err)
	}
	crossAccountChange := requestJSONCookie(t, http.MethodPatch, membershipURL+"/"+sandboxMembers.Memberships[0].MembershipID, `{"role":"viewer","expected_version":1,"reason":"Cross Account attack"}`, cookies[0])
	if crossAccountChange.StatusCode != http.StatusNotFound || !bytes.Contains(crossAccountChange.Body, []byte(`"code":"membership_not_found"`)) {
		t.Fatalf("cross-Account Membership mutation: %d %s", crossAccountChange.StatusCode, crossAccountChange.Body)
	}
	roleChangeURL := membershipURL + "/" + memberMembershipID
	roleChange := requestJSONCookie(t, http.MethodPatch, roleChangeURL, `{"role":"viewer","expected_version":1,"reason":"Limit access during transition"}`, cookies[0])
	if roleChange.StatusCode != http.StatusOK || !bytes.Contains(roleChange.Body, []byte(`"role":"viewer"`)) || !bytes.Contains(roleChange.Body, []byte(`"version":2`)) {
		t.Fatalf("Membership role change: %d %s", roleChange.StatusCode, roleChange.Body)
	}
	staleRoleChange := requestJSONCookie(t, http.MethodPatch, roleChangeURL, `{"role":"member","expected_version":1,"reason":"Stale browser update"}`, cookies[0])
	if staleRoleChange.StatusCode != http.StatusConflict || !bytes.Contains(staleRoleChange.Body, []byte(`"code":"membership_version_conflict"`)) {
		t.Fatalf("stale Membership role change: %d %s", staleRoleChange.StatusCode, staleRoleChange.Body)
	}
	suspensionURL := roleChangeURL + "/suspensions"
	suspended := requestJSONCookie(t, http.MethodPost, suspensionURL, `{"expected_version":2,"reason":"Temporary access review"}`, cookies[0])
	if suspended.StatusCode != http.StatusOK || !bytes.Contains(suspended.Body, []byte(`"state":"suspended"`)) || !bytes.Contains(suspended.Body, []byte(`"version":3`)) {
		t.Fatalf("suspend Membership: %d %s", suspended.StatusCode, suspended.Body)
	}
	suspendedAccounts := requestJSONCookie(t, http.MethodGet, server.URL+"/api/v1/session/accounts", "", memberCookies[0])
	if suspendedAccounts.StatusCode != http.StatusOK || bytes.Contains(suspendedAccounts.Body, []byte(provisioned.Account.ID)) {
		t.Fatalf("suspended Membership retained Account access: %d %s", suspendedAccounts.StatusCode, suspendedAccounts.Body)
	}
	suspendedRosterAccess := requestJSONCookie(t, http.MethodGet, membershipURL, "", memberCookies[0])
	if suspendedRosterAccess.StatusCode != http.StatusForbidden || !bytes.Contains(suspendedRosterAccess.Body, []byte(`"code":"membership_denied"`)) {
		t.Fatalf("suspended Membership retained roster access: %d %s", suspendedRosterAccess.StatusCode, suspendedRosterAccess.Body)
	}
	staleReactivate := requestJSONCookie(t, http.MethodDelete, suspensionURL, `{"expected_version":2,"reason":"Stale restore"}`, cookies[0])
	if staleReactivate.StatusCode != http.StatusConflict || !bytes.Contains(staleReactivate.Body, []byte(`"code":"membership_version_conflict"`)) {
		t.Fatalf("stale reactivation: %d %s", staleReactivate.StatusCode, staleReactivate.Body)
	}
	reactivated := requestJSONCookie(t, http.MethodDelete, suspensionURL, `{"expected_version":3,"reason":"Access review completed"}`, cookies[0])
	if reactivated.StatusCode != http.StatusOK || !bytes.Contains(reactivated.Body, []byte(`"state":"active"`)) || !bytes.Contains(reactivated.Body, []byte(`"version":4`)) {
		t.Fatalf("reactivate Membership: %d %s", reactivated.StatusCode, reactivated.Body)
	}
	transfer := postJSONCookie(t, server.URL+"/api/v1/accounts/"+provisioned.Account.ID+"/ownership-transfers", fmt.Sprintf(`{"target_membership_id":%q,"expected_actor_version":%d,"expected_target_version":4,"reason":"Planned leadership transition"}`, memberMembershipID, ownerVersion), cookies[0])
	if transfer.StatusCode != http.StatusOK || !bytes.Contains(transfer.Body, []byte(`"role":"owner"`)) || !bytes.Contains(transfer.Body, []byte(`"role":"administrator"`)) {
		t.Fatalf("ownership transfer: %d %s", transfer.StatusCode, transfer.Body)
	}
	removeOwnerAttempt := requestJSONCookie(t, http.MethodDelete, membershipURL+"/"+memberMembershipID, `{"expected_version":5,"reason":"Cannot remove current owner"}`, cookies[0])
	if removeOwnerAttempt.StatusCode != http.StatusConflict || !bytes.Contains(removeOwnerAttempt.Body, []byte(`"code":"ownership_required"`)) {
		t.Fatalf("previous owner continuity guard: %d %s", removeOwnerAttempt.StatusCode, removeOwnerAttempt.Body)
	}
	ownerLeaveAttempt := requestJSONCookie(t, http.MethodDelete, server.URL+"/api/v1/accounts/"+provisioned.Account.ID+"/membership", `{"expected_version":5,"reason":"Owner cannot abandon Account"}`, memberCookies[0])
	if ownerLeaveAttempt.StatusCode != http.StatusConflict || !bytes.Contains(ownerLeaveAttempt.Body, []byte(`"code":"ownership_required"`)) {
		t.Fatalf("owner self-leave guard: %d %s", ownerLeaveAttempt.StatusCode, ownerLeaveAttempt.Body)
	}
	closureURL := server.URL + "/api/v1/accounts/" + provisioned.Account.ID + "/closure"
	closure := postJSONCookie(t, closureURL, `{"expected_account_version":1,"reason":"Business operation concluded"}`, memberCookies[0])
	if closure.StatusCode != http.StatusAccepted || !bytes.Contains(closure.Body, []byte(`"state":"cooling_off"`)) || !bytes.Contains(closure.Body, []byte(`"account_state":"closing"`)) || !bytes.Contains(closure.Body, []byte(`"account_version":2`)) {
		t.Fatalf("Account closure request: %d %s", closure.StatusCode, closure.Body)
	}
	frozenAccounts := requestJSONCookie(t, http.MethodGet, server.URL+"/api/v1/session/accounts", "", memberCookies[0])
	if frozenAccounts.StatusCode != http.StatusOK || bytes.Contains(frozenAccounts.Body, []byte(provisioned.Account.ID)) || !bytes.Contains(frozenAccounts.Body, []byte(sandboxAccountID)) {
		t.Fatalf("closing Account access freeze: %d %s", frozenAccounts.StatusCode, frozenAccounts.Body)
	}
	closureHistory := requestJSONCookie(t, http.MethodGet, server.URL+"/api/v1/account-closures", "", memberCookies[0])
	if closureHistory.StatusCode != http.StatusOK || !bytes.Contains(closureHistory.Body, []byte(provisioned.Account.ID)) || !bytes.Contains(closureHistory.Body, []byte(`"state":"cooling_off"`)) {
		t.Fatalf("global closure recovery list: %d %s", closureHistory.StatusCode, closureHistory.Body)
	}
	staleRestore := requestJSONCookie(t, http.MethodDelete, closureURL, `{"expected_account_version":1,"reason":"Stale restoration"}`, memberCookies[0])
	if staleRestore.StatusCode != http.StatusConflict || !bytes.Contains(staleRestore.Body, []byte(`"code":"account_version_conflict"`)) {
		t.Fatalf("stale Account restoration: %d %s", staleRestore.StatusCode, staleRestore.Body)
	}
	restored := requestJSONCookie(t, http.MethodDelete, closureURL, `{"expected_account_version":2,"reason":"Operations will continue"}`, memberCookies[0])
	if restored.StatusCode != http.StatusOK || !bytes.Contains(restored.Body, []byte(`"state":"canceled"`)) || !bytes.Contains(restored.Body, []byte(`"account_state":"active"`)) || !bytes.Contains(restored.Body, []byte(`"account_version":3`)) {
		t.Fatalf("Account restoration: %d %s", restored.StatusCode, restored.Body)
	}
	leavePreviousOwner := requestJSONCookie(t, http.MethodDelete, server.URL+"/api/v1/accounts/"+provisioned.Account.ID+"/membership", `{"expected_version":2,"reason":"Previous owner chose to leave"}`, cookies[0])
	if leavePreviousOwner.StatusCode != http.StatusNoContent {
		t.Fatalf("previous owner self-leave: %d %s", leavePreviousOwner.StatusCode, leavePreviousOwner.Body)
	}
	removedOwnerList := requestJSONCookie(t, http.MethodGet, membershipURL, "", cookies[0])
	if removedOwnerList.StatusCode != http.StatusForbidden || !bytes.Contains(removedOwnerList.Body, []byte(`"code":"membership_denied"`)) {
		t.Fatalf("removed Membership retained Account access: %d %s", removedOwnerList.StatusCode, removedOwnerList.Body)
	}
	recoveryCodes := postJSONCookie(t, server.URL+"/api/v1/recovery-codes", `{}`, cookies[0])
	var recoveryCodeSet struct {
		Status struct {
			Configured bool `json:"configured"`
			Remaining  int  `json:"remaining"`
		} `json:"status"`
		Codes []string `json:"codes"`
	}
	if err := json.Unmarshal(recoveryCodes.Body, &recoveryCodeSet); err != nil || recoveryCodes.StatusCode != http.StatusCreated || !recoveryCodeSet.Status.Configured || len(recoveryCodeSet.Codes) != 10 || bytes.Contains(recoveryCodes.Body, []byte(provisioned.Account.ID)) {
		t.Fatalf("recovery-code rotation: %d %+v err=%v body=%s", recoveryCodes.StatusCode, recoveryCodeSet, err, recoveryCodes.Body)
	}
	removedPasskey := requestJSONCookie(t, http.MethodDelete, server.URL+"/api/v1/passkeys/"+enrolledPasskey.ID, "", cookies[0])
	if removedPasskey.StatusCode != http.StatusNoContent {
		t.Fatalf("last passkey deletion with recovery codes: %d %s", removedPasskey.StatusCode, removedPasskey.Body)
	}
	passwordAgain := postJSONCookie(t, server.URL+"/api/v1/session/reauthenticate", `{"password":"correct horse battery staple"}`, cookies[0])
	if passwordAgain.StatusCode != http.StatusNoContent {
		t.Fatalf("lost-passkey password confirmation: %d %s", passwordAgain.StatusCode, passwordAgain.Body)
	}
	consumedCode := postJSONCookie(t, server.URL+"/api/v1/recovery-codes/consume", `{"code":"`+recoveryCodeSet.Codes[0]+`"}`, cookies[0])
	if consumedCode.StatusCode != http.StatusNoContent {
		t.Fatalf("recovery-code consumption: %d %s", consumedCode.StatusCode, consumedCode.Body)
	}
	replayedCode := postJSONCookie(t, server.URL+"/api/v1/recovery-codes/consume", `{"code":"`+recoveryCodeSet.Codes[0]+`"}`, cookies[0])
	if replayedCode.StatusCode != http.StatusBadRequest || !bytes.Contains(replayedCode.Body, []byte(`"code":"recovery_code_invalid"`)) {
		t.Fatalf("recovery-code replay: %d %s", replayedCode.StatusCode, replayedCode.Body)
	}
	replacementPasskey := postJSONCookie(t, server.URL+"/api/v1/passkey-registrations", `{}`, cookies[0])
	if replacementPasskey.StatusCode != http.StatusCreated {
		t.Fatalf("lost-passkey replacement registration: %d %s", replacementPasskey.StatusCode, replacementPasskey.Body)
	}
	unknownRecovery := postJSON(t, server.URL+"/api/v1/recovery-challenges", `{"email":"missing@example.com"}`)
	if unknownRecovery.StatusCode != http.StatusAccepted || bytes.Contains(unknownRecovery.Body, []byte("development_recovery_token")) {
		t.Fatalf("unknown recovery response: %d %s", unknownRecovery.StatusCode, unknownRecovery.Body)
	}
	beginRecovery := postJSON(t, server.URL+"/api/v1/recovery-challenges", `{"email":"avery@example.com"}`)
	if beginRecovery.StatusCode != http.StatusAccepted {
		t.Fatalf("begin recovery: %d %s", beginRecovery.StatusCode, beginRecovery.Body)
	}
	var recoveryResponse map[string]any
	if err := json.Unmarshal(beginRecovery.Body, &recoveryResponse); err != nil {
		t.Fatal(err)
	}
	recoveryToken, _ := recoveryResponse["development_recovery_token"].(string)
	if recoveryToken == "" {
		t.Fatal("development recovery token missing")
	}
	completeRecovery := postJSON(t, server.URL+"/api/v1/recovery-challenges/complete", `{"token":"`+recoveryToken+`","password":"replacement password material"}`)
	if completeRecovery.StatusCode != http.StatusNoContent {
		t.Fatalf("complete recovery: %d %s", completeRecovery.StatusCode, completeRecovery.Body)
	}
	staleSessionRequest, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/sessions", nil)
	staleSessionRequest.AddCookie(cookies[0])
	staleSessionResponse, err := http.DefaultClient.Do(staleSessionRequest)
	if err != nil {
		t.Fatal(err)
	}
	staleSessionBody, _ := io.ReadAll(staleSessionResponse.Body)
	staleSessionResponse.Body.Close()
	validateOpenAPIResponse(t, http.MethodGet, staleSessionRequest.URL.String(), staleSessionResponse.StatusCode, staleSessionResponse.Header, staleSessionBody)
	if staleSessionResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("pre-recovery session status: %d", staleSessionResponse.StatusCode)
	}
	oldCredential := postJSON(t, server.URL+"/api/v1/sessions", `{"email":"avery@example.com","password":"correct horse battery staple"}`)
	if oldCredential.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old password login status: %d", oldCredential.StatusCode)
	}
	newCredential := postJSON(t, server.URL+"/api/v1/sessions", `{"email":"avery@example.com","password":"replacement password material"}`)
	if newCredential.StatusCode != http.StatusCreated {
		t.Fatalf("new password login: %d %s", newCredential.StatusCode, newCredential.Body)
	}
	cookies = (&http.Response{Header: newCredential.Header}).Cookies()
	request, _ := http.NewRequest(http.MethodDelete, server.URL+"/api/v1/session", nil)
	request.AddCookie(cookies[0])
	logout, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer logout.Body.Close()
	logoutBody, _ := io.ReadAll(logout.Body)
	validateOpenAPIResponse(t, http.MethodDelete, request.URL.String(), logout.StatusCode, logout.Header, logoutBody)
	if logout.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status: %d", logout.StatusCode)
	}
}

func TestVerifiedContactChangeHTTPJourney(t *testing.T) {
	server := httptest.NewServer(development.Handler(slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	begin := postJSON(t, server.URL+"/api/v1/registrations", `{"email":"old-contact@example.com","display_name":"Casey Morgan","account_name":"Beacon Works","region":"us-east"}`)
	var accepted map[string]any
	if err := json.Unmarshal(begin.Body, &accepted); err != nil || begin.StatusCode != http.StatusAccepted {
		t.Fatalf("begin identity registration: %d %s err=%v", begin.StatusCode, begin.Body, err)
	}
	verificationToken, _ := accepted["development_verification_token"].(string)
	complete := postJSON(t, server.URL+"/api/v1/registrations/verify", `{"token":"`+verificationToken+`","password":"contact change password"}`)
	var provisioned struct {
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
	}
	if err := json.Unmarshal(complete.Body, &provisioned); err != nil || complete.StatusCode != http.StatusCreated || provisioned.Account.ID == "" {
		t.Fatalf("complete identity registration: %d %s err=%v", complete.StatusCode, complete.Body, err)
	}

	login := postJSON(t, server.URL+"/api/v1/sessions", `{"email":"old-contact@example.com","password":"contact change password"}`)
	cookies := (&http.Response{Header: login.Header}).Cookies()
	if login.StatusCode != http.StatusCreated || len(cookies) != 1 {
		t.Fatalf("initial login: %d %s cookies=%#v", login.StatusCode, login.Body, cookies)
	}
	withoutPasskey := postJSONCookie(t, server.URL+"/api/v1/contact-change-requests", `{"new_email":"new-contact@example.com"}`, cookies[0])
	if withoutPasskey.StatusCode != http.StatusForbidden || !bytes.Contains(withoutPasskey.Body, []byte(`"code":"strong_reauthentication_required"`)) {
		t.Fatalf("password-only contact change: %d %s", withoutPasskey.StatusCode, withoutPasskey.Body)
	}

	passwordConfirmation := postJSONCookie(t, server.URL+"/api/v1/session/reauthenticate", `{"password":"contact change password"}`, cookies[0])
	if passwordConfirmation.StatusCode != http.StatusNoContent {
		t.Fatalf("password confirmation: %d %s", passwordConfirmation.StatusCode, passwordConfirmation.Body)
	}
	registration := postJSONCookie(t, server.URL+"/api/v1/passkey-registrations", `{}`, cookies[0])
	var ceremony struct {
		CeremonyID string `json:"ceremony_id"`
		PublicKey  struct {
			PublicKey struct {
				Challenge string `json:"challenge"`
			} `json:"publicKey"`
		} `json:"public_key"`
	}
	if err := json.Unmarshal(registration.Body, &ceremony); err != nil || registration.StatusCode != http.StatusCreated || ceremony.CeremonyID == "" || ceremony.PublicKey.PublicKey.Challenge == "" {
		t.Fatalf("passkey registration ceremony: %d %s err=%v", registration.StatusCode, registration.Body, err)
	}
	credential := registrationCredential(t, ceremony.PublicKey.PublicKey.Challenge)
	enrolled := postJSONCookie(t, server.URL+"/api/v1/passkey-registrations/"+ceremony.CeremonyID+"/complete", `{"name":"Contact confirmation passkey","credential":`+credential+`}`, cookies[0])
	if enrolled.StatusCode != http.StatusCreated {
		t.Fatalf("passkey enrollment: %d %s", enrolled.StatusCode, enrolled.Body)
	}

	sameEmail := postJSONCookie(t, server.URL+"/api/v1/contact-change-requests", `{"new_email":" OLD-CONTACT@example.com "}`, cookies[0])
	if sameEmail.StatusCode != http.StatusBadRequest || !bytes.Contains(sameEmail.Body, []byte(`"code":"contact_change_same_email"`)) {
		t.Fatalf("same normalized contact: %d %s", sameEmail.StatusCode, sameEmail.Body)
	}
	requested := postJSONCookie(t, server.URL+"/api/v1/contact-change-requests", `{"new_email":" NEW-CONTACT@example.com "}`, cookies[0])
	var pending struct {
		ID        string `json:"contact_change_id"`
		NewEmail  string `json:"new_email"`
		Token     string `json:"development_verification_token"`
		Status    string `json:"status"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(requested.Body, &pending); err != nil || requested.StatusCode != http.StatusAccepted || pending.ID == "" || pending.NewEmail != "new-contact@example.com" || pending.Token == "" || pending.Status != "verification_required" || pending.ExpiresAt == "" {
		t.Fatalf("contact change request: %d %s err=%v", requested.StatusCode, requested.Body, err)
	}

	oldStillWorks := postJSON(t, server.URL+"/api/v1/sessions", `{"email":"old-contact@example.com","password":"contact change password"}`)
	oldStillWorksCookies := (&http.Response{Header: oldStillWorks.Header}).Cookies()
	if oldStillWorks.StatusCode != http.StatusCreated || len(oldStillWorksCookies) != 1 {
		t.Fatalf("old login before verification: %d %s", oldStillWorks.StatusCode, oldStillWorks.Body)
	}
	newTooSoon := postJSON(t, server.URL+"/api/v1/sessions", `{"email":"new-contact@example.com","password":"contact change password"}`)
	if newTooSoon.StatusCode != http.StatusUnauthorized {
		t.Fatalf("new login before verification: %d %s", newTooSoon.StatusCode, newTooSoon.Body)
	}

	changed := postJSON(t, server.URL+"/api/v1/contact-change-verifications", `{"token":"`+pending.Token+`"}`)
	if changed.StatusCode != http.StatusOK || !bytes.Contains(changed.Body, []byte(`"status":"email_changed"`)) || !bytes.Contains(changed.Body, []byte(`"new_email":"new-contact@example.com"`)) || !bytes.Contains(changed.Body, []byte(`"sessions_revoked":true`)) {
		t.Fatalf("complete contact change: %d %s", changed.StatusCode, changed.Body)
	}
	if expiredCookies := (&http.Response{Header: changed.Header}).Cookies(); len(expiredCookies) != 2 || expiredCookies[0].MaxAge >= 0 || expiredCookies[1].MaxAge >= 0 {
		t.Fatalf("identity cookies were not expired: %#v", expiredCookies)
	}
	replay := postJSON(t, server.URL+"/api/v1/contact-change-verifications", `{"token":"`+pending.Token+`"}`)
	if replay.StatusCode != http.StatusConflict || !bytes.Contains(replay.Body, []byte(`"code":"contact_change_consumed"`)) {
		t.Fatalf("contact token replay: %d %s", replay.StatusCode, replay.Body)
	}
	for _, staleCookie := range []*http.Cookie{cookies[0], oldStillWorksCookies[0]} {
		stale := requestJSONCookie(t, http.MethodGet, server.URL+"/api/v1/sessions", "", staleCookie)
		if stale.StatusCode != http.StatusUnauthorized {
			t.Fatalf("pre-change session retained access: %d %s", stale.StatusCode, stale.Body)
		}
	}
	oldAfterChange := postJSON(t, server.URL+"/api/v1/sessions", `{"email":"old-contact@example.com","password":"contact change password"}`)
	if oldAfterChange.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old login after verification: %d %s", oldAfterChange.StatusCode, oldAfterChange.Body)
	}
	newLogin := postJSON(t, server.URL+"/api/v1/sessions", `{"email":"new-contact@example.com","password":"contact change password"}`)
	newCookies := (&http.Response{Header: newLogin.Header}).Cookies()
	if newLogin.StatusCode != http.StatusCreated || len(newCookies) != 1 {
		t.Fatalf("new login after verification: %d %s", newLogin.StatusCode, newLogin.Body)
	}
	accounts := requestJSONCookie(t, http.MethodGet, server.URL+"/api/v1/session/accounts", "", newCookies[0])
	if accounts.StatusCode != http.StatusOK || !bytes.Contains(accounts.Body, []byte(provisioned.Account.ID)) {
		t.Fatalf("Account memberships survived contact change: %d %s", accounts.StatusCode, accounts.Body)
	}
	events := requestJSONCookie(t, http.MethodGet, server.URL+"/api/v1/security-events", "", newCookies[0])
	if events.StatusCode != http.StatusOK || !bytes.Contains(events.Body, []byte(`"type":"primary_email_change_requested"`)) || !bytes.Contains(events.Body, []byte(`"type":"primary_email_changed"`)) {
		t.Fatalf("contact security events: %d %s", events.StatusCode, events.Body)
	}
}

func postJSONCookie(t *testing.T, url, body string, cookie *http.Cookie) response {
	return requestJSONCookie(t, http.MethodPost, url, body, cookie)
}

func requestJSONCookie(t *testing.T, method, url, body string, cookie *http.Cookie) response {
	t.Helper()
	request, _ := http.NewRequest(method, url, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	request.AddCookie(cookie)
	client := http.Client{Timeout: 3 * time.Second}
	result, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Body.Close()
	payload, _ := io.ReadAll(result.Body)
	validateOpenAPIResponse(t, method, url, result.StatusCode, result.Header, payload)
	return response{StatusCode: result.StatusCode, Header: result.Header.Clone(), Body: payload}
}

func TestStripeWebhookHTTPAcceptsThenDeduplicatesSignedEvent(t *testing.T) {
	secret := "whsec_http_test_secret"
	t.Setenv("SPYGLASS_STRIPE_WEBHOOK_SECRET", secret)
	server := httptest.NewServer(development.Handler(slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()
	now := time.Now().UTC()
	payload := []byte(fmt.Sprintf(`{"id":"evt_http_phase2","type":"customer.subscription.updated","created":%d,"livemode":false,"data":{"object":{"id":"sub_http"}}}`, now.Unix()))
	header := stripeSignature(secret, now.Unix(), payload)
	first := postWebhook(t, server.URL+"/webhooks/stripe", payload, header)
	if first.StatusCode != http.StatusOK || !bytes.Contains(first.Body, []byte(`"status":"accepted"`)) {
		t.Fatalf("first delivery: %d %s", first.StatusCode, first.Body)
	}
	second := postWebhook(t, server.URL+"/webhooks/stripe", payload, header)
	if second.StatusCode != http.StatusOK || !bytes.Contains(second.Body, []byte(`"status":"duplicate"`)) {
		t.Fatalf("duplicate delivery: %d %s", second.StatusCode, second.Body)
	}
	tampered := postWebhook(t, server.URL+"/webhooks/stripe", append(payload, ' '), header)
	if tampered.StatusCode != http.StatusBadRequest {
		t.Fatalf("tampered delivery status: %d", tampered.StatusCode)
	}
}

type response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

func postJSON(t *testing.T, url, body string) response {
	t.Helper()
	request, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 2 * time.Second}
	result, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Body.Close()
	payload, _ := io.ReadAll(result.Body)
	validateOpenAPIResponse(t, http.MethodPost, url, result.StatusCode, result.Header, payload)
	return response{StatusCode: result.StatusCode, Header: result.Header.Clone(), Body: payload}
}

func postWebhook(t *testing.T, url string, body []byte, signature string) response {
	t.Helper()
	request, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Stripe-Signature", signature)
	client := http.Client{Timeout: 2 * time.Second}
	result, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Body.Close()
	responseBody, _ := io.ReadAll(result.Body)
	validateOpenAPIResponse(t, http.MethodPost, url, result.StatusCode, result.Header, responseBody)
	return response{StatusCode: result.StatusCode, Header: result.Header.Clone(), Body: responseBody}
}

func validateOpenAPIResponse(t *testing.T, method, target string, status int, headers http.Header, body []byte) {
	t.Helper()
	responseContractOnce.Do(func() {
		responseContract, responseContractErr = openapifixture.Load(filepath.Join("..", "..", "..", "api", "spyglass.openapi.json"))
	})
	if responseContractErr != nil {
		t.Fatalf("load OpenAPI response contract: %v", responseContractErr)
	}
	if err := responseContract.ValidateResponse(method, target, status, headers, body); err != nil {
		t.Fatalf("response violates OpenAPI contract: %v\nbody: %s", err, body)
	}
}

func registrationCredential(t *testing.T, challenge string) string {
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
	credentialHash := sha256.Sum256([]byte(challenge))
	credentialID := append([]byte(nil), credentialHash[:20]...)
	clientData := []byte(fmt.Sprintf(`{"type":"webauthn.create","challenge":%q,"origin":"http://localhost:8080"}`, challenge))
	rpHash := sha256.Sum256([]byte("localhost"))
	authenticatorData := append([]byte(nil), rpHash[:]...)
	authenticatorData = append(authenticatorData, 0x45, 0, 0, 0, 0)
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
	value := map[string]any{
		"id": encodedID, "rawId": encodedID, "type": "public-key",
		"response": map[string]any{
			"clientDataJSON":    base64.RawURLEncoding.EncodeToString(clientData),
			"attestationObject": base64.RawURLEncoding.EncodeToString(attestation),
			"transports":        []string{"internal"},
		},
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func stripeSignature(secret string, timestamp int64, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(append([]byte(fmt.Sprintf("%d.", timestamp)), payload...))
	return fmt.Sprintf("t=%d,v1=%s", timestamp, hex.EncodeToString(mac.Sum(nil)))
}

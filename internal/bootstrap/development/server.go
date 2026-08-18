package development

import (
	"crypto/rand"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/memory"
	"github.com/tinfoyle/spyglass-engine/internal/application/abuse"
	"github.com/tinfoyle/spyglass-engine/internal/application/accountaccess"
	"github.com/tinfoyle/spyglass-engine/internal/application/accountmembers"
	"github.com/tinfoyle/spyglass-engine/internal/application/authentication"
	"github.com/tinfoyle/spyglass-engine/internal/application/invitations"
	"github.com/tinfoyle/spyglass-engine/internal/application/passkeys"
	"github.com/tinfoyle/spyglass-engine/internal/application/recovery"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/billing"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/placement"
	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/authn"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/networkactor"
	"github.com/tinfoyle/spyglass-engine/internal/transport/browserapp"
	"github.com/tinfoyle/spyglass-engine/internal/transport/httpapi"
)

func Handler(logger *slog.Logger) http.Handler {
	clock := registration.SystemClock{}
	publishedCatalog := catalog.Default(clock.Now())
	store := memory.NewStore(publishedCatalog, []placement.Cell{{ID: ids.CellID("cell-us-east-01"), Region: "us-east", State: "active", SoftLimit: 1000}})
	networkGuard, err := abuse.NewGuard(store)
	if err != nil {
		panic(err)
	}
	var actorKey [32]byte
	if _, err := rand.Read(actorKey[:]); err != nil {
		panic(err)
	}
	actorResolver, err := networkactor.New(actorKey[:], nil)
	if err != nil {
		panic(err)
	}
	verification := &memory.VerificationSink{}
	passwords := authn.Passwords{}
	service := registration.NewService(store, verification, store, func() catalog.PublishedCatalog { return publishedCatalog }, ids.RandomGenerator{}, clock, passwords)
	sessionStore := memory.NewSessionStore()
	sessionService, err := sessions.NewService(sessionStore, ids.RandomGenerator{}, clock, 24*time.Hour, time.Hour, 15*time.Minute)
	if err != nil {
		panic(err)
	}
	recoverySink := &memory.RecoverySink{}
	recoveryService, err := recovery.NewService(memory.NewRecoveryRepository(store, sessionStore), recoverySink, store, networkGuard, passwords, ids.RandomGenerator{}, clock)
	if err != nil {
		panic(err)
	}
	dummyHash, err := passwords.Hash("development dummy password")
	if err != nil {
		panic(err)
	}
	authenticationService, err := authentication.NewService(store, store, networkGuard, passwords, sessionService, clock, dummyHash)
	if err != nil {
		panic(err)
	}
	passkeyService, err := passkeys.NewService(memory.NewPasskeyRepository(store, sessionStore), sessionService, networkGuard, ids.RandomGenerator{}, clock, passkeys.Config{RelyingPartyID: "localhost", Origins: []string{"http://localhost:8080"}})
	if err != nil {
		panic(err)
	}
	authorizer, err := access.NewAuthorizer(store)
	if err != nil {
		panic(err)
	}
	accountAccess, err := accountaccess.NewService(store, authorizer)
	if err != nil {
		panic(err)
	}
	memberService, err := accountmembers.NewService(store, authorizer, ids.RandomGenerator{}, clock)
	if err != nil {
		panic(err)
	}
	invitationSink := &memory.InvitationSink{}
	invitationService, err := invitations.NewService(store, invitationSink, authorizer, ids.RandomGenerator{}, clock)
	if err != nil {
		panic(err)
	}
	options := []httpapi.Option{httpapi.WithAuthentication(authenticationService, sessionService, httpapi.SessionCookie{Name: "spyglass_development_session"}), httpapi.WithAccountAccess(accountAccess), httpapi.WithAccountMembers(memberService), httpapi.WithInvitations(invitationService, invitationSink, true), httpapi.WithRecovery(recoveryService, recoverySink, true), httpapi.WithPasskeys(passkeyService)}
	if secret := os.Getenv("SPYGLASS_STRIPE_WEBHOOK_SECRET"); secret != "" {
		verifier, err := billing.NewSignatureVerifier(secret, 5*time.Minute, clock)
		if err != nil {
			panic(err)
		}
		webhook, err := billing.NewWebhookService(verifier, memory.NewBillingInbox(), "test", clock)
		if err != nil {
			panic(err)
		}
		options = append(options, httpapi.WithBillingWebhook(webhook))
	}
	apiHandler := httpapi.NewServer(service, store.Catalog, verification, true, logger, options...).Handler()
	browser, err := browserapp.New(service, authenticationService, sessionService, accountAccess, invitationService, store.Catalog, verification, invitationSink, browserapp.Config{SessionCookieName: "spyglass_development_session", AccountCookieName: "spyglass_development_account", TrustedOrigins: []string{"http://localhost:8080", "http://127.0.0.1:8080", "https://infiniteocean.net"}, ExposeDevelopmentTokens: true}, logger, browserapp.WithAccountMembers(memberService), browserapp.WithRecovery(recoveryService, recoverySink), browserapp.WithPasskeys(passkeyService))
	if err != nil {
		panic(err)
	}
	return actorResolver.Handler(browser.Handler(apiHandler))
}

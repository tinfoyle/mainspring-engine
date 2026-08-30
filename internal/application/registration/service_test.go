package registration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/memory"
	"github.com/tinfoyle/spyglass-engine/internal/application/registration"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	"github.com/tinfoyle/spyglass-engine/internal/modules/placement"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type fixedClock struct{ value time.Time }

func (c fixedClock) Now() time.Time { return c.value }

type sequenceIDs struct{ next int }

type passwordHasher struct{}

func (passwordHasher) Hash(value string) (string, error) {
	if len(value) < 12 {
		return "", errors.New("password must be at least 12 characters")
	}
	return "hashed:" + value, nil
}

func (g *sequenceIDs) New() string { g.next++; return "00000000-0000-4000-8000-" + pad(g.next) }
func pad(value int) string {
	digits := "000000000000"
	raw := []byte(digits)
	raw[len(raw)-1] = byte('0' + value%10)
	return string(raw)
}

func TestBeginReplacesExpiredChallenge(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	clock := &fixedClock{value: now}
	published := catalog.Default(now)
	store := memory.NewStore(published, []placement.Cell{{ID: ids.CellID("cell-1"), Region: "us-east", State: "active", SoftLimit: 10}})
	sink := &memory.VerificationSink{}
	service := registration.NewService(store, sink, store, func() catalog.PublishedCatalog { return published }, ids.RandomGenerator{}, clock, passwordHasher{})
	command := registration.BeginCommand{Email: "owner@example.com", DisplayName: "Owner", AccountName: "Example", Region: "us-east"}
	if _, err := service.Begin(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	clock.value = now.Add(31 * time.Minute)
	if _, err := service.Begin(context.Background(), command); err != nil {
		t.Fatalf("expired challenge should be replaceable: %v", err)
	}
}

func TestRegistrationRequiresVerificationThenProvisionsInactiveShellAtomically(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	clock := fixedClock{value: now}
	published := catalog.Default(now)
	store := memory.NewStore(published, []placement.Cell{{ID: ids.CellID("cell-us-east-01"), Region: "us-east", State: "active", SoftLimit: 10}})
	messages := &memory.VerificationSink{}
	service := registration.NewService(store, messages, store, func() catalog.PublishedCatalog { return published }, &sequenceIDs{}, clock, passwordHasher{})

	begin, err := service.Begin(context.Background(), registration.BeginCommand{Email: "Avery@Example.com", DisplayName: "Avery Johnson", AccountName: "Northstar Studio", Region: "us-east"})
	if err != nil {
		t.Fatal(err)
	}
	if begin.RegistrationID == "" {
		t.Fatal("missing registration ID")
	}
	message, ok := messages.Latest()
	if !ok {
		t.Fatal("verification message not sent")
	}

	result, err := service.Complete(context.Background(), registration.CompleteCommand{Token: message.Token, Password: "strong-password"})
	if err != nil {
		t.Fatal(err)
	}
	if result.User.PrimaryEmail != "avery@example.com" {
		t.Fatalf("email not normalized: %s", result.User.PrimaryEmail)
	}
	if result.Account.Type != "inactive" || result.Membership.Role != "owner" {
		t.Fatalf("unexpected provisioning: %#v", result)
	}
	if result.Assignment.CellID != "cell-us-east-01" {
		t.Fatalf("unexpected placement: %s", result.Assignment.CellID)
	}
	if len(result.Snapshot.Packages) != 0 || len(result.Grants) != 0 {
		t.Fatalf("inactive shell received product access: grants=%+v snapshot=%+v", result.Grants, result.Snapshot)
	}
	if _, err := service.Complete(context.Background(), registration.CompleteCommand{Token: message.Token, Password: "strong-password"}); err != registration.ErrRegistrationConsumed {
		t.Fatalf("expected consumed error, got %v", err)
	}
}

func TestRegistrationRejectsDuplicatePendingEmail(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	published := catalog.Default(now)
	store := memory.NewStore(published, []placement.Cell{{ID: ids.CellID("cell-us-east-01"), Region: "us-east", State: "active", SoftLimit: 10}})
	service := registration.NewService(store, &memory.VerificationSink{}, store, func() catalog.PublishedCatalog { return published }, &sequenceIDs{}, fixedClock{value: now}, passwordHasher{})
	command := registration.BeginCommand{Email: "avery@example.com", DisplayName: "Avery", AccountName: "Northstar"}
	if _, err := service.Begin(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Begin(context.Background(), command); err != registration.ErrEmailExists {
		t.Fatalf("expected duplicate email error, got %v", err)
	}
}

func TestRegistrationCarriesOnlyPublishedPaidOfferToVerification(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	published := catalog.Default(now)
	store := memory.NewStore(published, []placement.Cell{{ID: ids.CellID("cell-us-east-01"), Region: "us-east", State: "active", SoftLimit: 10}})
	messages := &memory.VerificationSink{}
	service := registration.NewService(store, messages, store, func() catalog.PublishedCatalog { return published }, &sequenceIDs{}, fixedClock{value: now}, passwordHasher{})
	command := registration.BeginCommand{Email: "buyer@example.com", DisplayName: "Buyer", AccountName: "Buyer Co", OfferCode: "team-monthly-v2", ReturnTo: "/app/checkout?offer=team-monthly-v2&ref=IO-PARTNER1"}
	if _, err := service.Begin(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	message, ok := messages.Latest()
	if !ok || message.OfferCode != command.OfferCode || message.ReturnTo != command.ReturnTo {
		t.Fatalf("verification intent = offer %q, return %q", message.OfferCode, message.ReturnTo)
	}
	command.Email = "other@example.com"
	command.OfferCode = "invented-offer"
	if _, err := service.Begin(context.Background(), command); !errors.Is(err, registration.ErrOfferUnavailable) {
		t.Fatalf("unpublished offer error = %v", err)
	}
	command.OfferCode = "free-v1"
	if _, err := service.Begin(context.Background(), command); !errors.Is(err, registration.ErrOfferUnavailable) {
		t.Fatalf("free offer error = %v", err)
	}
	for index := range published.Offers {
		if published.Offers[index].Code == "team-monthly-v2" {
			published.Offers[index].EffectiveFrom = now.Add(time.Hour)
		}
	}
	command.OfferCode = "team-monthly-v2"
	if _, err := service.Begin(context.Background(), command); !errors.Is(err, registration.ErrOfferUnavailable) {
		t.Fatalf("future offer error = %v", err)
	}
}

func TestExternalRegistrationProvisionsVerifiedInactiveShellWithoutLocalCredential(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	published := catalog.Default(now)
	store := memory.NewStore(published, []placement.Cell{{ID: ids.CellID("cell-us-east-01"), Region: "us-east", State: "active", SoftLimit: 10}})
	service := registration.NewService(store, &memory.VerificationSink{}, store, func() catalog.PublishedCatalog { return published }, &sequenceIDs{}, fixedClock{value: now}, passwordHasher{})
	command := registration.ExternalCommand{
		Email: "Avery@Example.com", DisplayName: "Avery Johnson", AccountName: "Northstar Studio", Region: "us-east",
		OfferCode: "team-monthly-v2", Provider: "oidc", Identifier: "https://accounts.google.com\x1fgoogle-subject-1",
	}

	result, err := service.CompleteExternal(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if result.User.PrimaryEmail != "avery@example.com" || result.User.EmailVerifiedAt == nil || result.User.State != "active" {
		t.Fatalf("external user was not provider-verified: %+v", result.User)
	}
	if result.Credential.UserID != "" || result.ExternalIdentity == nil || result.ExternalIdentity.Provider != "oidc" || result.ExternalIdentity.Identifier != command.Identifier {
		t.Fatalf("unexpected login identities: credential=%+v external=%+v", result.Credential, result.ExternalIdentity)
	}
	if result.Account.Type != "inactive" || result.Membership.Role != "owner" || len(result.Grants) != 0 || len(result.Snapshot.Packages) != 0 {
		t.Fatalf("unexpected external provisioning: %+v", result)
	}
	command.Email = "other@example.com"
	command.AccountName = "Other Studio"
	if _, err := service.CompleteExternal(context.Background(), command); !errors.Is(err, registration.ErrIdentityExists) {
		t.Fatalf("duplicate external identity error = %v", err)
	}
}

func TestExternalRegistrationNeverAutoLinksAnExistingEmail(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	published := catalog.Default(now)
	store := memory.NewStore(published, []placement.Cell{{ID: ids.CellID("cell-us-east-01"), Region: "us-east", State: "active", SoftLimit: 10}})
	service := registration.NewService(store, &memory.VerificationSink{}, store, func() catalog.PublishedCatalog { return published }, &sequenceIDs{}, fixedClock{value: now}, passwordHasher{})
	first := registration.ExternalCommand{Email: "avery@example.com", DisplayName: "Avery", AccountName: "First Studio", Provider: "oidc", Identifier: "issuer\x1ffirst"}
	if _, err := service.CompleteExternal(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.AccountName = "Second Studio"
	second.Identifier = "issuer\x1fsecond"
	if _, err := service.CompleteExternal(context.Background(), second); !errors.Is(err, registration.ErrEmailExists) {
		t.Fatalf("matching email must fail closed, got %v", err)
	}
}

package main

import "testing"

func TestConfigIsLocalOnlyAndRequiresExactIdentity(t *testing.T) {
	t.Setenv("SPYGLASS_ENVIRONMENT", "production")
	if _, err := configFromEnvironment(); err == nil {
		t.Fatal("production fixture configuration was accepted")
	}
	t.Setenv("SPYGLASS_ENVIRONMENT", "local")
	t.Setenv("SPYGLASS_FIXTURE_ACCOUNT_ID", "a1100000-0000-4000-8000-000000000001")
	t.Setenv("SPYGLASS_FIXTURE_BINDING_REQUEST_ID", "a1200000-0000-4000-8000-000000000002")
	t.Setenv("SPYGLASS_FIXTURE_CREDENTIAL_GENERATION", "1")
	t.Setenv("SPYGLASS_FIXTURE_CREDENTIAL_PROVIDER", "imap")
	t.Setenv("SPYGLASS_FIXTURE_CREDENTIAL_REFERENCE", "local://imap-fixture/operations")
	t.Setenv("SPYGLASS_FIXTURE_CREDENTIAL_MATERIAL", `{"address":"imap-fixture:9993"}`)
	value, err := configFromEnvironment()
	if err != nil || value.provider != "imap" || value.generation != 1 || value.credentialID == "" {
		t.Fatalf("config=%+v err=%v", value, err)
	}
	wipe(value.material)
}

// Command provider-credential-fixture seeds one encrypted provider credential
// for local Docker certification. It is never included in the release image.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/tinfoyle/spyglass-engine/internal/adapters/encryptedcredentials"
	"github.com/tinfoyle/spyglass-engine/internal/application/integrationcredentials"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type config struct {
	root, keyFile, provider, reference string
	accountID                          ids.AccountID
	credentialID                       ids.IntegrationCredentialID
	generation                         uint64
	material                           []byte
}

func main() {
	configuration, err := configFromEnvironment()
	if err != nil {
		fatal(err)
	}
	defer wipe(configuration.material)
	vault, err := encryptedcredentials.New(configuration.root, configuration.keyFile)
	if err != nil {
		fatal(err)
	}
	err = vault.PutCredential(context.Background(), integrationcredentials.CredentialSecret{
		AccountID: configuration.accountID, CredentialID: configuration.credentialID, Generation: configuration.generation,
		Provider: configuration.provider, Reference: []byte(configuration.reference), Material: configuration.material,
	})
	if err != nil {
		fatal(err)
	}
}

func configFromEnvironment() (config, error) {
	if os.Getenv("SPYGLASS_ENVIRONMENT") != "local" && os.Getenv("SPYGLASS_ENVIRONMENT") != "local-secure" {
		return config{}, errors.New("provider credential fixture requires a local environment")
	}
	generation, generationErr := strconv.ParseUint(os.Getenv("SPYGLASS_FIXTURE_CREDENTIAL_GENERATION"), 10, 64)
	credentialID := os.Getenv("SPYGLASS_FIXTURE_CREDENTIAL_ID")
	var deriveErr error
	if credentialID == "" {
		credentialID, deriveErr = ids.Derive(os.Getenv("SPYGLASS_FIXTURE_BINDING_REQUEST_ID"), "integration-credential")
	}
	value := config{
		root:         envOr("SPYGLASS_PROVIDER_SECRET_ROOT", "/var/lib/spyglass/provider/vault"),
		keyFile:      envOr("SPYGLASS_PROVIDER_SECRET_KEY_FILE", "/var/lib/spyglass/provider/key"),
		accountID:    ids.AccountID(os.Getenv("SPYGLASS_FIXTURE_ACCOUNT_ID")),
		credentialID: ids.IntegrationCredentialID(credentialID),
		generation:   generation,
		provider:     strings.TrimSpace(os.Getenv("SPYGLASS_FIXTURE_CREDENTIAL_PROVIDER")),
		reference:    os.Getenv("SPYGLASS_FIXTURE_CREDENTIAL_REFERENCE"),
		material:     []byte(os.Getenv("SPYGLASS_FIXTURE_CREDENTIAL_MATERIAL")),
	}
	if generationErr != nil || deriveErr != nil || ids.Validate(string(value.accountID)) != nil || ids.Validate(string(value.credentialID)) != nil || value.generation == 0 ||
		value.provider == "" || value.reference == "" || len(value.reference) > 2048 || len(value.material) == 0 || len(value.material) > 64<<10 ||
		value.root == "" || value.keyFile == "" {
		wipe(value.material)
		return config{}, errors.New("provider credential fixture configuration is invalid")
	}
	return value, nil
}

func wipe(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

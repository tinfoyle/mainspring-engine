package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareRoutesRejectsBothDirectionsOfDrift(t *testing.T) {
	contract := []route{{Service: "account-api", Method: "GET", Path: "/api/v1/catalog/public"}}
	if err := compareRoutes(contract, append(contract, route{Service: "account-api", Method: "POST", Path: "/api/v1/undocumented"})); err == nil || !strings.Contains(err.Error(), "undocumented") {
		t.Fatalf("undocumented route error=%v", err)
	}
	if err := compareRoutes(contract, nil); err == nil || !strings.Contains(err.Error(), "missing registrations") {
		t.Fatalf("missing route error=%v", err)
	}
}

func TestValidatePathParametersRequiresExactReferences(t *testing.T) {
	valid := json.RawMessage(`[{"$ref":"#/components/parameters/accountID"},{"$ref":"#/components/parameters/itemID"}]`)
	path := "/api/v1/accounts/{accountID}/work-items/{itemID}"
	if err := validatePathParameters(path, valid); err != nil {
		t.Fatalf("valid parameters: %v", err)
	}
	if err := validatePathParameters(path, json.RawMessage(`[{"$ref":"#/components/parameters/accountID"}]`)); err == nil {
		t.Fatal("missing path parameter was accepted")
	}
}

func TestValidateReferencesRejectsMissingOrExternalTargets(t *testing.T) {
	root := map[string]any{"components": map[string]any{"schemas": map[string]any{"Known": map[string]any{"type": "string"}}}, "schema": map[string]any{"$ref": "#/components/schemas/Known"}}
	if err := validateReferences(root, root); err != nil {
		t.Fatalf("known local reference: %v", err)
	}
	root["schema"] = map[string]any{"$ref": "#/components/schemas/Missing"}
	if err := validateReferences(root, root); err == nil {
		t.Fatal("missing reference was accepted")
	}
	root["schema"] = map[string]any{"$ref": "https://example.com/schema.json"}
	if err := validateReferences(root, root); err == nil {
		t.Fatal("external reference was accepted")
	}
}

func TestRenderTypeScriptSchemasPreservesRequiredOptionalEnumsAndNulls(t *testing.T) {
	schemas := map[string]json.RawMessage{
		"State": json.RawMessage(`{"type":"string","enum":["open","done"]}`),
		"Item":  json.RawMessage(`{"type":"object","required":["state","context","metadata","enabled"],"properties":{"state":{"$ref":"#/components/schemas/State"},"note":{"type":"string"},"context":{"anyOf":[{"type":"object","additionalProperties":{"type":"integer"}},{"type":"null"}]},"metadata":{"type":"object"},"enabled":{"type":"boolean","const":false}}}`),
	}
	generated := string(renderTypeScriptSchemas(schemas))
	for _, expected := range []string{
		`export type State = "open" | "done";`,
		`readonly "state": State;`,
		`readonly "note"?: string;`,
		`readonly "context": Readonly<Record<string, number>> | null;`,
		`readonly "metadata": Readonly<Record<string, unknown>>;`,
		`readonly "enabled": false;`,
		`readonly Item: Item;`,
	} {
		if !strings.Contains(generated, expected) {
			t.Fatalf("generated schemas lack %q:\n%s", expected, generated)
		}
	}
}

func TestCellPackageOperationsRemainTyped(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	routes, err := loadContract(filepath.Join(root, contractPath))
	if err != nil {
		t.Fatal(err)
	}
	accountWork, agents := 0, 0
	for _, route := range routes {
		if route.Service != "cell-api" {
			continue
		}
		switch {
		case route.Path == "/api/v1/accounts/{accountID}/context" || strings.Contains(route.Path, "/work-items"):
			accountWork++
		case strings.Contains(route.Path, "/agent-"):
			agents++
		default:
			continue
		}
		if route.Contract != "typed" {
			t.Errorf("%s %s regressed to %q contract", route.Method, route.Path, route.Contract)
		}
	}
	if accountWork != 10 || agents != 11 {
		t.Fatalf("typed operation counts Account/Work=%d Agents=%d, want 10 and 11", accountWork, agents)
	}
}

func TestPublicAccountEntryOperationsRemainTyped(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	routes, err := loadContract(filepath.Join(root, contractPath))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"publicCatalog":        false,
		"beginRegistration":    false,
		"completeRegistration": false,
		"login":                false,
		"listAccounts":         false,
		"selectAccount":        false,
	}
	for _, route := range routes {
		if _, exists := want[route.OperationID]; !exists {
			continue
		}
		if route.Service != "account-api" {
			t.Errorf("%s is owned by %q, want account-api", route.OperationID, route.Service)
		}
		if route.Contract != "typed" {
			t.Errorf("%s regressed to %q contract", route.OperationID, route.Contract)
		}
		want[route.OperationID] = true
	}
	for operationID, found := range want {
		if !found {
			t.Errorf("typed public Account-entry operation %q is missing", operationID)
		}
	}
}

func TestIdentitySecurityOperationsRemainTyped(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	routes, err := loadContract(filepath.Join(root, contractPath))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"beginRecovery":                   false,
		"completeRecovery":                false,
		"beginContactChange":              false,
		"completeContactChange":           false,
		"beginPasskeyLogin":               false,
		"completePasskeyLogin":            false,
		"listPasskeys":                    false,
		"beginPasskeyRegistration":        false,
		"completePasskeyRegistration":     false,
		"renamePasskey":                   false,
		"deletePasskey":                   false,
		"compromisePasskey":               false,
		"beginPasskeyReauthentication":    false,
		"completePasskeyReauthentication": false,
		"recoveryCodeStatus":              false,
		"rotateRecoveryCodes":             false,
		"consumeRecoveryCode":             false,
		"securityPostureStatus":           false,
		"listSessions":                    false,
		"listSecurityEvents":              false,
		"revokeSession":                   false,
		"logoutAll":                       false,
		"reauthenticate":                  false,
		"logout":                          false,
	}
	for _, route := range routes {
		if _, exists := want[route.OperationID]; !exists {
			continue
		}
		if route.Service != "account-api" {
			t.Errorf("%s is owned by %q, want account-api", route.OperationID, route.Service)
		}
		if route.Contract != "typed" {
			t.Errorf("%s regressed to %q contract", route.OperationID, route.Contract)
		}
		want[route.OperationID] = true
	}
	for operationID, found := range want {
		if !found {
			t.Errorf("typed identity-security operation %q is missing", operationID)
		}
	}
}

func TestEveryCustomerOperationRemainsTyped(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	routes, err := loadContract(filepath.Join(root, contractPath))
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 213 {
		t.Fatalf("customer operation count = %d, want 213", len(routes))
	}
	for _, route := range routes {
		if route.Contract != "typed" {
			t.Errorf("%s %s regressed to %q contract", route.Method, route.Path, route.Contract)
		}
	}
}

func TestCommittedContractMatchesTransportsAndGeneratedFiles(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	routes, _, err := loadContracts(root)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := registeredRoutes(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareRoutes(routes, actual); err != nil {
		t.Fatal(err)
	}
}

func TestOperationsContractIsSeparatePasskeyOnlyAndTyped(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	routes, err := loadContract(filepath.Join(root, operationsContractPath))
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 19 {
		t.Fatalf("operations route count=%d, want 19", len(routes))
	}
	for _, route := range routes {
		if route.Service != "operations-api" || route.Contract != "typed" {
			t.Fatalf("unsafe operations route: %+v", route)
		}
		if route.Authentication != "operationsCookie" && !strings.Contains(route.OperationID, "PasskeyLogin") {
			t.Fatalf("operations route lacks isolated cookie: %+v", route)
		}
	}
}

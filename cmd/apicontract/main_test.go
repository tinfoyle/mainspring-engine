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
		"Item":  json.RawMessage(`{"type":"object","required":["state","context"],"properties":{"state":{"$ref":"#/components/schemas/State"},"note":{"type":"string"},"context":{"anyOf":[{"type":"object","additionalProperties":{"type":"integer"}},{"type":"null"}]}}}`),
	}
	generated := string(renderTypeScriptSchemas(schemas))
	for _, expected := range []string{
		`export type State = "open" | "done";`,
		`readonly "state": State;`,
		`readonly "note"?: string;`,
		`readonly "context": Readonly<Record<string, number>> | null;`,
		`readonly Item: Item;`,
	} {
		if !strings.Contains(generated, expected) {
			t.Fatalf("generated schemas lack %q:\n%s", expected, generated)
		}
	}
}

func TestWorkAndAccountContextOperationsRemainTyped(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	routes, err := loadContract(filepath.Join(root, contractPath))
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, route := range routes {
		if route.Service != "cell-api" || (route.Path != "/api/v1/accounts/{accountID}/context" && !strings.Contains(route.Path, "/work-items")) {
			continue
		}
		found++
		if route.Contract != "typed" {
			t.Errorf("%s %s regressed to %q contract", route.Method, route.Path, route.Contract)
		}
	}
	if found != 8 {
		t.Fatalf("typed Account context/Work operation count=%d, want 8", found)
	}
}

func TestCommittedContractMatchesTransportsAndGeneratedFiles(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	routes, err := loadContract(filepath.Join(root, contractPath))
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

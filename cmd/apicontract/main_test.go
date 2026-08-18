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

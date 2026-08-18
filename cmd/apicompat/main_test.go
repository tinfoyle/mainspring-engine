package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareAcceptsCompatibleRequestAndResponseGrowth(t *testing.T) {
	base := testContract(t, `{"type":"object","required":["name"],"properties":{"name":{"type":"string","enum":["a"]}},"additionalProperties":false}`, `{"type":"object","required":["id"],"properties":{"id":{"type":"string"}},"additionalProperties":false}`)
	head := testContract(t, `{"type":"object","required":["name"],"properties":{"name":{"type":"string","enum":["a","b"]},"note":{"type":"string"}},"additionalProperties":false}`, `{"type":"object","required":["id","note"],"properties":{"id":{"type":"string"},"note":{"type":"string"}},"additionalProperties":false}`)
	if breaks := compare(base, head); len(breaks) != 0 {
		t.Fatalf("compatible change rejected: %v", breaks)
	}
}

func TestCompareRejectsRemovedOperationAndNarrowedRequest(t *testing.T) {
	base := testContract(t, `{"type":"object","properties":{"mode":{"type":"string","enum":["a","b"]}},"additionalProperties":false}`, `{"type":"object","required":["id"],"properties":{"id":{"type":"string"}},"additionalProperties":false}`)
	head := testContract(t, `{"type":"object","required":["mode"],"properties":{"mode":{"type":"string","enum":["a"]}},"additionalProperties":false}`, `{"type":"object","required":["id"],"properties":{"id":{"type":"string"}},"additionalProperties":false}`)
	breaks := strings.Join(compare(base, head), "\n")
	if !strings.Contains(breaks, "narrowed accepted enum") || !strings.Contains(breaks, "made property mode required") {
		t.Fatalf("request breaks were not classified: %s", breaks)
	}
	delete(head.operations, "POST /items")
	if breaks := strings.Join(compare(base, head), "\n"); !strings.Contains(breaks, "was removed") {
		t.Fatalf("removed operation was not classified: %s", breaks)
	}
}

func TestCompareRejectsResponseWideningAndMissingRequiredField(t *testing.T) {
	base := testContract(t, `{"type":"object"}`, `{"type":"object","required":["state","id"],"properties":{"state":{"type":"string","enum":["ready"]},"id":{"type":"string"}},"additionalProperties":false}`)
	head := testContract(t, `{"type":"object"}`, `{"type":"object","properties":{"state":{"type":"string","enum":["ready","unknown"]}},"additionalProperties":false}`)
	breaks := strings.Join(compare(base, head), "\n")
	for _, expected := range []string{"removed property id", "widened emitted enum values", "made required response property state optional"} {
		if !strings.Contains(breaks, expected) {
			t.Fatalf("response break %q was not classified: %s", expected, breaks)
		}
	}
}

func testContract(t *testing.T, request, response string) contract {
	t.Helper()
	document := `{"openapi":"3.1.0","paths":{"/items":{"post":{"operationId":"create","x-spyglass-service":"account-api","security":[],"requestBody":{"required":true,"content":{"application/json":{"schema":` + request + `}}},"responses":{"201":{"description":"created","content":{"application/json":{"schema":` + response + `}}}}}}}}`
	path := filepath.Join(t.TempDir(), "openapi.json")
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

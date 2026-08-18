package openapifixture

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateResponse(t *testing.T) {
	contract := testContract(t)
	header := http.Header{}
	header.Set("Content-Type", "application/json; charset=utf-8")
	header.Set("ETag", `W/"2"`)
	body := []byte(`{"id":"20b35ca6-27cf-48ca-acc5-c5a60f860eec","name":"Release","tags":["ready"]}`)

	if err := contract.ValidateResponse(http.MethodGet, "https://spyglass.test/api/widgets/20b35ca6-27cf-48ca-acc5-c5a60f860eec?view=full", http.StatusOK, header, body); err != nil {
		t.Fatal(err)
	}
}

func TestValidateResponseRejectsSchemaDrift(t *testing.T) {
	contract := testContract(t)
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("ETag", `W/"2"`)

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "missing required", body: `{"id":"20b35ca6-27cf-48ca-acc5-c5a60f860eec","tags":[]}`, want: "$.name is required"},
		{name: "additional property", body: `{"id":"20b35ca6-27cf-48ca-acc5-c5a60f860eec","name":"Release","tags":[],"internal":true}`, want: "$.internal is not allowed"},
		{name: "duplicate array value", body: `{"id":"20b35ca6-27cf-48ca-acc5-c5a60f860eec","name":"Release","tags":["ready","ready"]}`, want: "$.tags must contain unique items"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := contract.ValidateResponse(http.MethodGet, "/api/widgets/20b35ca6-27cf-48ca-acc5-c5a60f860eec", http.StatusOK, header, []byte(test.body))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestValidateResponseRejectsTransportDrift(t *testing.T) {
	contract := testContract(t)
	body := []byte(`{"id":"20b35ca6-27cf-48ca-acc5-c5a60f860eec","name":"Release","tags":[]}`)

	t.Run("required header", func(t *testing.T) {
		err := contract.ValidateResponse(http.MethodGet, "/api/widgets/id", http.StatusOK, http.Header{"Content-Type": []string{"application/json"}}, body)
		if err == nil || !strings.Contains(err.Error(), "required header ETag is missing") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("undocumented content", func(t *testing.T) {
		err := contract.ValidateResponse(http.MethodDelete, "/api/widgets/id", http.StatusNoContent, http.Header{}, []byte(`{}`))
		if err == nil || !strings.Contains(err.Error(), "undocumented body") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("default problem", func(t *testing.T) {
		header := http.Header{"Content-Type": []string{"application/problem+json"}}
		body := []byte(`{"type":"https://spyglass.test/problems/not-found","title":"Not found","status":404}`)
		if err := contract.ValidateResponse(http.MethodGet, "/api/widgets/id", http.StatusNotFound, header, body); err != nil {
			t.Fatal(err)
		}
	})
}

func TestLoadReportsMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("expected missing file error")
	}
}

func testContract(t *testing.T) *Contract {
	t.Helper()
	path := filepath.Join(t.TempDir(), "openapi.json")
	document := `{
  "openapi":"3.1.0",
  "paths":{
    "/api/widgets/{widget_id}":{
      "get":{"responses":{
        "200":{"headers":{"ETag":{"required":true,"schema":{"type":"string","pattern":"^W/\\\"[1-9][0-9]*\\\"$"}}},"content":{"application/json":{"schema":{"$ref":"#/components/schemas/Widget"}}}},
        "default":{"content":{"application/problem+json":{"schema":{"$ref":"#/components/schemas/Problem"}}}}
      }},
      "delete":{"responses":{"204":{"description":"Deleted"}}}
    }
  },
  "components":{"schemas":{
    "Widget":{"type":"object","additionalProperties":false,"required":["id","name","tags"],"properties":{"id":{"type":"string","format":"uuid"},"name":{"type":"string","minLength":1},"tags":{"type":"array","uniqueItems":true,"items":{"type":"string"}}}},
    "Problem":{"type":"object","additionalProperties":false,"required":["type","title","status"],"properties":{"type":{"type":"string","format":"uri"},"title":{"type":"string","minLength":1},"status":{"type":"integer","minimum":400,"maximum":599}}}
  }}
}`
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	contract, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

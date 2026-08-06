package tenant

import "testing"

func TestDocumentMediaType(t *testing.T) {
	tests := map[string]string{
		"notes.txt": "text/plain", "manual.md": "text/markdown", "prices.csv": "text/csv",
		"config.json": "application/json", "policy.yaml": "text/yaml", "page.html": "text/html",
	}
	for filename, expected := range tests {
		actual, ok := documentMediaType(filename)
		if !ok || actual != expected {
			t.Fatalf("documentMediaType(%q) = %q, %v; want %q, true", filename, actual, ok, expected)
		}
	}
	if _, ok := documentMediaType("invoice.pdf"); ok {
		t.Fatal("PDF should not be accepted until extraction is implemented")
	}
}

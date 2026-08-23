package accountexport

import (
	"errors"
	"testing"
)

func TestLaunchRegistryClassifiesPortableAndNonPortableState(t *testing.T) {
	registry, err := LaunchRegistry()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		schema, table, section string
		disposition            Disposition
	}{
		{schema: "public", table: "accounts", section: "account", disposition: Included},
		{schema: "public", table: "billing_event_inbox", disposition: Secret},
		{schema: "public", table: "billing_reconciliation_queue", disposition: Operational},
		{schema: "spyglass", table: "integration_credentials", disposition: Secret},
		{schema: "spyglass", table: "knowledge_document_chunks", disposition: Derived},
		{schema: "spyglass", table: "work_items", section: "work", disposition: Included},
	}
	for _, test := range tests {
		coverage, found := registry.Coverage(test.schema, test.table)
		if !found || coverage.Section != test.section || coverage.Disposition != test.disposition {
			t.Fatalf("coverage %s.%s=%+v found=%v", test.schema, test.table, coverage, found)
		}
		if coverage.Disposition != Included && coverage.Reason == "" {
			t.Fatalf("excluded coverage %s.%s has no reason", test.schema, test.table)
		}
	}
	sections := registry.Sections()
	sections[0].Stores[0] = "modified"
	reloaded := registry.Sections()
	if reloaded[0].Stores[0] == "modified" {
		t.Fatal("registry exposed mutable descriptor storage")
	}
}

func TestRegistryAndBuilderFailClosed(t *testing.T) {
	descriptor := Descriptor{Code: "account", SchemaVersion: 1, Stores: []string{"global-postgresql"}}
	if _, err := NewRegistry([]Descriptor{descriptor}, []TableCoverage{{Schema: "public", Table: "accounts", Disposition: Secret}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("secret table without reason error=%v", err)
	}
	registry, err := NewRegistry([]Descriptor{descriptor}, []TableCoverage{{Schema: "public", Table: "accounts", Section: "account", Disposition: Included}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewBuilder(registry, nil, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing source error=%v", err)
	}
	wrong := testSection{descriptor: Descriptor{Code: "account", SchemaVersion: 2, Stores: []string{"global-postgresql"}}}
	if _, err := NewBuilder(registry, []SectionSource{wrong}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("source version drift error=%v", err)
	}
}

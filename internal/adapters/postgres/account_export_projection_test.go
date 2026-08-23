package postgres

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountexport"
)

func projectionTestTx() *pgxpool.Tx { return nil }

func TestAccountExportProjectionQueryUsesOnlyExplicitColumns(t *testing.T) {
	table := AccountExportProjectionTable{Tx: projectionTestTx(), Schema: "public", Table: "invitations", AccountColumn: "account_id",
		KeyColumns: []string{"id"}, Columns: []string{"id", "account_id", "email", "role", "state"}, OmittedColumns: []string{"token_hash"}}
	if !validProjectionTable(table) {
		t.Fatal("valid projection table rejected")
	}
	query := projectionQuery(table)
	for _, required := range []string{`FROM "public"."invitations"`, `WHERE "account_id"=$1`, `'email',"email"`, `jsonb_build_array("id")`} {
		if !strings.Contains(query, required) {
			t.Fatalf("query missing %q: %s", required, query)
		}
	}
	if strings.Contains(query, "token_hash") {
		t.Fatalf("unlisted secret entered query: %s", query)
	}
}

func TestAccountExportProjectionCanonicalizesAndBindsStableKey(t *testing.T) {
	record, err := canonicalProjectionRecord("public.invitations/5b226964225d", []byte(`{"state":"pending","email":"owner@example.com","id":"ed1"}`))
	want := []byte(`{"email":"owner@example.com","id":"ed1","key":"public.invitations/5b226964225d","state":"pending"}`)
	if err != nil || !bytes.Equal(record, want) {
		t.Fatalf("record=%s want=%s err=%v", record, want, err)
	}
	for _, payload := range [][]byte{nil, []byte(`[]`), []byte(`{"key":"caller-controlled"}`), []byte(`{"id":1} trailing`)} {
		if _, err := canonicalProjectionRecord("table/key", payload); !errors.Is(err, accountexport.ErrInvalid) {
			t.Fatalf("payload=%q err=%v", payload, err)
		}
	}
}

func TestAccountExportProjectionTableValidationFailsClosed(t *testing.T) {
	valid := AccountExportProjectionTable{Tx: projectionTestTx(), Schema: "public", Table: "accounts", AccountColumn: "id",
		KeyColumns: []string{"id"}, Columns: []string{"id", "display_name", "state"}}
	tests := []AccountExportProjectionTable{
		{Schema: valid.Schema, Table: valid.Table, AccountColumn: valid.AccountColumn, KeyColumns: valid.KeyColumns, Columns: valid.Columns},
		{Tx: valid.Tx, Schema: `public;drop`, Table: valid.Table, AccountColumn: valid.AccountColumn, KeyColumns: valid.KeyColumns, Columns: valid.Columns},
		{Tx: valid.Tx, Schema: valid.Schema, Table: valid.Table, AccountColumn: valid.AccountColumn, KeyColumns: []string{"missing"}, Columns: valid.Columns},
		{Tx: valid.Tx, Schema: valid.Schema, Table: valid.Table, AccountColumn: valid.AccountColumn, KeyColumns: valid.KeyColumns, Columns: []string{"id", "id"}},
		{Tx: valid.Tx, Schema: valid.Schema, Table: valid.Table, AccountColumn: valid.AccountColumn, KeyColumns: valid.KeyColumns, Columns: []string{"id", "key"}},
		{Tx: valid.Tx, Schema: valid.Schema, Table: valid.Table, AccountColumn: valid.AccountColumn, KeyColumns: valid.KeyColumns, Columns: valid.Columns, OmittedColumns: []string{"state"}},
	}
	for index, table := range tests {
		if validProjectionTable(table) {
			t.Fatalf("invalid table %d accepted: %+v", index, table)
		}
	}
}

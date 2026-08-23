package accountexport

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"testing"
	"time"
)

type testSection struct {
	descriptor Descriptor
	records    []json.RawMessage
}

func (source testSection) Descriptor() Descriptor { return source.descriptor }
func (source testSection) Open(context.Context, BuildRequest) (RecordCursor, error) {
	return &testRecordCursor{records: append([]json.RawMessage(nil), source.records...)}, nil
}

type testRecordCursor struct {
	records []json.RawMessage
	closed  bool
}

func (cursor *testRecordCursor) Next(context.Context) (json.RawMessage, bool, error) {
	if len(cursor.records) == 0 {
		return nil, false, nil
	}
	value := cursor.records[0]
	cursor.records = cursor.records[1:]
	return value, true, nil
}
func (cursor *testRecordCursor) Close() error { cursor.closed = true; return nil }

type testObjects struct {
	descriptor Descriptor
	values     []testObject
}
type testObject struct {
	path, media string
	body        []byte
}

func (source testObjects) Descriptor() Descriptor { return source.descriptor }
func (source testObjects) OpenObjects(context.Context, BuildRequest) (ObjectCursor, error) {
	return &testObjectCursor{values: append([]testObject(nil), source.values...)}, nil
}

type testObjectCursor struct{ values []testObject }

func (cursor *testObjectCursor) Next(context.Context) (Object, bool, error) {
	if len(cursor.values) == 0 {
		return Object{}, false, nil
	}
	value := cursor.values[0]
	cursor.values = cursor.values[1:]
	return Object{Path: value.path, MediaType: value.media, Size: int64(len(value.body)), SHA256: sha256.Sum256(value.body), Body: io.NopCloser(bytes.NewReader(value.body))}, true, nil
}
func (cursor *testObjectCursor) Close() error { return nil }

func exportRequest() BuildRequest {
	at := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	return BuildRequest{AccountID: "e1000000-0000-4000-8000-000000000001", ExportID: "e2000000-0000-4000-8000-000000000002",
		RequestedBy: "e3000000-0000-4000-8000-000000000003", RequestedAt: at, ExpiresAt: at.Add(7 * 24 * time.Hour),
		Snapshot: Snapshot{CellID: "cell-us-east-01", PlacementGeneration: 3, AccountVersion: 5, GlobalAt: at, CellAt: at.Add(time.Second)}}
}

func testRegistry(t *testing.T) *Registry {
	t.Helper()
	registry, err := NewRegistry([]Descriptor{
		{Code: "account", SchemaVersion: 1, Stores: []string{"global-postgresql"}},
		{Code: "documents", SchemaVersion: 1, Stores: []string{"versioned-object-store"}},
		{Code: "work", SchemaVersion: 1, Stores: []string{"cell-postgresql"}},
	}, []TableCoverage{{Schema: "public", Table: "accounts", Section: "account", Disposition: Included}})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func TestBuilderProducesDeterministicHashedArchive(t *testing.T) {
	sections := []SectionSource{
		testSection{descriptor: Descriptor{Code: "work", SchemaVersion: 1, Stores: []string{"cell-postgresql"}}, records: []json.RawMessage{json.RawMessage(`{"id":"work-1","key":"work_items/work-1","state":"completed"}`)}},
		testSection{descriptor: Descriptor{Code: "account", SchemaVersion: 1, Stores: []string{"global-postgresql"}}, records: []json.RawMessage{json.RawMessage(`{"key":"accounts/e1000000-0000-4000-8000-000000000001","name":"Example","state":"active"}`)}},
	}
	objects := []ObjectSource{testObjects{descriptor: Descriptor{Code: "documents", SchemaVersion: 1, Stores: []string{"versioned-object-store"}}, values: []testObject{
		{path: "document-1/revision-1/source.txt", media: "text/plain", body: []byte("original evidence")},
	}}}
	builder, err := NewBuilder(testRegistry(t), sections, objects)
	if err != nil {
		t.Fatal(err)
	}
	var first, second bytes.Buffer
	result, err := builder.Build(context.Background(), exportRequest(), &first)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := builder.Build(context.Background(), exportRequest(), &second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) || result.ArtifactSHA256 != replayed.ArtifactSHA256 || result.ArtifactSHA256 != sha256.Sum256(first.Bytes()) || result.ArtifactBytes != int64(first.Len()) {
		t.Fatalf("artifact replay drift bytes=%d/%d digest=%x/%x", first.Len(), second.Len(), result.ArtifactSHA256, replayed.ArtifactSHA256)
	}
	reader, err := zip.NewReader(bytes.NewReader(first.Bytes()), int64(first.Len()))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(reader.File))
	contents := map[string][]byte{}
	for _, file := range reader.File {
		names = append(names, file.Name)
		body, readErr := file.Open()
		if readErr != nil {
			t.Fatal(readErr)
		}
		contents[file.Name], readErr = io.ReadAll(body)
		closeErr := body.Close()
		if readErr != nil || closeErr != nil {
			t.Fatal(readErr, closeErr)
		}
	}
	wantNames := []string{"sections/account.jsonl", "sections/work.jsonl", "objects/documents/document-1/revision-1/source.txt", "manifest.json"}
	if !slices.Equal(names, wantNames) || string(contents["objects/documents/document-1/revision-1/source.txt"]) != "original evidence" {
		t.Fatalf("entries=%v contents=%q", names, contents)
	}
	var manifest Manifest
	if err := json.Unmarshal(contents["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Sections) != 2 || manifest.Sections[0].Code != "account" || manifest.Sections[0].Records != 1 || len(manifest.Objects) != 1 || manifest.Objects[0].SHA256 == "" {
		t.Fatalf("manifest=%+v", manifest)
	}
}

func TestBuilderRejectsUnsafeOrNonCanonicalSourceOutput(t *testing.T) {
	tests := []struct {
		name    string
		section []json.RawMessage
		objects []testObject
	}{
		{name: "noncanonical record", section: []json.RawMessage{json.RawMessage(`{ "key": "one" }`)}},
		{name: "array record", section: []json.RawMessage{json.RawMessage(`[]`)}},
		{name: "missing record key", section: []json.RawMessage{json.RawMessage(`{"id":"one"}`)}},
		{name: "unsorted records", section: []json.RawMessage{json.RawMessage(`{"key":"z"}`), json.RawMessage(`{"key":"a"}`)}},
		{name: "unsorted objects", section: []json.RawMessage{json.RawMessage(`{"key":"one"}`)}, objects: []testObject{{path: "z", media: "text/plain", body: []byte("z")}, {path: "a", media: "text/plain", body: []byte("a")}}},
		{name: "unsafe object path", section: []json.RawMessage{json.RawMessage(`{"key":"one"}`)}, objects: []testObject{{path: "../secret", media: "text/plain", body: []byte("x")}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			builder, err := NewBuilder(testRegistry(t), []SectionSource{
				testSection{descriptor: Descriptor{Code: "account", SchemaVersion: 1, Stores: []string{"global-postgresql"}}, records: test.section},
				testSection{descriptor: Descriptor{Code: "work", SchemaVersion: 1, Stores: []string{"cell-postgresql"}}},
			}, []ObjectSource{testObjects{descriptor: Descriptor{Code: "documents", SchemaVersion: 1, Stores: []string{"versioned-object-store"}}, values: test.objects}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := builder.Build(context.Background(), exportRequest(), io.Discard); !errors.Is(err, ErrInvalid) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

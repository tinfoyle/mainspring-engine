// Package accountexport builds a deterministic, private Account portability
// artifact from explicitly registered customer-data projections and objects.
// It never discovers tables, follows stored object references, or exports
// provider credentials implicitly.
package accountexport

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

const (
	ArchiveSchemaVersion = 1
	MaximumSectionBytes  = int64(2 << 30)
	MaximumObjectBytes   = int64(64 << 20)
	MaximumArtifactBytes = int64(32 << 30)
	MaximumRecords       = 5_000_000
	MaximumObjects       = 100_000
	MaximumSnapshotDelay = 15 * time.Minute
)

var (
	ErrInvalid     = errors.New("Account export input is invalid")
	ErrUnavailable = errors.New("Account export source is unavailable")
	validCode      = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	validStore     = regexp.MustCompile(`^(global-postgresql|cell-postgresql|versioned-object-store)$`)
)

type Snapshot struct {
	CellID              ids.CellID `json:"cell_id"`
	PlacementGeneration uint64     `json:"placement_generation"`
	AccountVersion      uint64     `json:"account_version"`
	GlobalAt            time.Time  `json:"global_at"`
	CellAt              time.Time  `json:"cell_at"`
}

type Request struct {
	AccountID   ids.AccountID `json:"account_id"`
	ExportID    string        `json:"export_id"`
	RequestedBy ids.UserID    `json:"requested_by"`
	RequestedAt time.Time     `json:"requested_at"`
	ExpiresAt   time.Time     `json:"expires_at"`
	Snapshot    Snapshot      `json:"snapshot"`
}

type Descriptor struct {
	Code          string   `json:"code"`
	SchemaVersion uint64   `json:"schema_version"`
	Stores        []string `json:"stores"`
}

type RecordCursor interface {
	Next(context.Context) (json.RawMessage, bool, error)
	Close() error
}

type SectionSource interface {
	Descriptor() Descriptor
	Open(context.Context, Request) (RecordCursor, error)
}

type Object struct {
	Path      string
	MediaType string
	Size      int64
	SHA256    [sha256.Size]byte
	Body      io.ReadCloser
}

type ObjectCursor interface {
	Next(context.Context) (Object, bool, error)
	Close() error
}

type ObjectSource interface {
	Descriptor() Descriptor
	OpenObjects(context.Context, Request) (ObjectCursor, error)
}

type SectionManifest struct {
	Descriptor
	Path    string `json:"path"`
	Records uint64 `json:"records"`
	Bytes   int64  `json:"bytes"`
	SHA256  string `json:"sha256"`
}

type ObjectManifest struct {
	Source    string `json:"source"`
	Path      string `json:"path"`
	MediaType string `json:"media_type"`
	Bytes     int64  `json:"bytes"`
	SHA256    string `json:"sha256"`
}

type Manifest struct {
	SchemaVersion uint64            `json:"schema_version"`
	AccountID     ids.AccountID     `json:"account_id"`
	ExportID      string            `json:"export_id"`
	RequestedBy   ids.UserID        `json:"requested_by"`
	RequestedAt   time.Time         `json:"requested_at"`
	ExpiresAt     time.Time         `json:"expires_at"`
	Snapshot      Snapshot          `json:"snapshot"`
	Sections      []SectionManifest `json:"sections"`
	Objects       []ObjectManifest  `json:"objects"`
}

type Result struct {
	Manifest       Manifest
	ArtifactBytes  int64
	ArtifactSHA256 [sha256.Size]byte
}

type Builder struct {
	sections []SectionSource
	objects  []ObjectSource
}

func NewBuilder(registry *Registry, sections []SectionSource, objects []ObjectSource) (*Builder, error) {
	sections = append([]SectionSource(nil), sections...)
	objects = append([]ObjectSource(nil), objects...)
	if registry.validateSources(sections, objects) != nil {
		return nil, ErrInvalid
	}
	seen := make(map[string]bool, len(sections)+len(objects))
	for _, source := range sections {
		if source == nil || !validDescriptor(source.Descriptor()) || seen[source.Descriptor().Code] {
			return nil, ErrInvalid
		}
		seen[source.Descriptor().Code] = true
	}
	for _, source := range objects {
		if source == nil || !validDescriptor(source.Descriptor()) || seen[source.Descriptor().Code] {
			return nil, ErrInvalid
		}
		seen[source.Descriptor().Code] = true
	}
	slices.SortFunc(sections, func(left, right SectionSource) int {
		return strings.Compare(left.Descriptor().Code, right.Descriptor().Code)
	})
	slices.SortFunc(objects, func(left, right ObjectSource) int {
		return strings.Compare(left.Descriptor().Code, right.Descriptor().Code)
	})
	return &Builder{sections: sections, objects: objects}, nil
}

func (builder *Builder) Build(ctx context.Context, request Request, target io.Writer) (Result, error) {
	if builder == nil || target == nil || !validRequest(request) {
		return Result{}, ErrInvalid
	}
	artifactHash := sha256.New()
	bounded := &boundedWriter{target: io.MultiWriter(target, artifactHash), remaining: MaximumArtifactBytes}
	archive := zip.NewWriter(bounded)
	manifest := Manifest{SchemaVersion: ArchiveSchemaVersion, AccountID: request.AccountID, ExportID: request.ExportID,
		RequestedBy: request.RequestedBy, RequestedAt: request.RequestedAt.UTC(), ExpiresAt: request.ExpiresAt.UTC(), Snapshot: normalizedSnapshot(request.Snapshot),
		Sections: make([]SectionManifest, 0, len(builder.sections)), Objects: []ObjectManifest{}}
	for _, source := range builder.sections {
		entry, err := builder.writeSection(ctx, archive, request, source)
		if err != nil {
			_ = archive.Close()
			return Result{}, err
		}
		manifest.Sections = append(manifest.Sections, entry)
	}
	for _, source := range builder.objects {
		entries, err := builder.writeObjects(ctx, archive, request, source, len(manifest.Objects))
		if err != nil {
			_ = archive.Close()
			return Result{}, err
		}
		manifest.Objects = append(manifest.Objects, entries...)
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		_ = archive.Close()
		return Result{}, errors.Join(ErrInvalid, err)
	}
	manifestWriter, err := archive.CreateHeader(archiveHeader("manifest.json"))
	if err != nil {
		_ = archive.Close()
		return Result{}, errors.Join(ErrUnavailable, err)
	}
	if _, err := manifestWriter.Write(append(encoded, '\n')); err != nil {
		_ = archive.Close()
		return Result{}, errors.Join(ErrUnavailable, err)
	}
	if err := archive.Close(); err != nil {
		return Result{}, errors.Join(ErrUnavailable, err)
	}
	var digest [sha256.Size]byte
	copy(digest[:], artifactHash.Sum(nil))
	return Result{Manifest: manifest, ArtifactBytes: bounded.written, ArtifactSHA256: digest}, nil
}

func (builder *Builder) writeSection(ctx context.Context, archive *zip.Writer, request Request, source SectionSource) (SectionManifest, error) {
	descriptor := source.Descriptor()
	cursor, err := source.Open(ctx, request)
	if err != nil || cursor == nil {
		return SectionManifest{}, errors.Join(ErrUnavailable, err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = cursor.Close()
		}
	}()
	entryPath := "sections/" + descriptor.Code + ".jsonl"
	writer, err := archive.CreateHeader(archiveHeader(entryPath))
	if err != nil {
		return SectionManifest{}, errors.Join(ErrUnavailable, err)
	}
	hasher := sha256.New()
	section := &boundedWriter{target: io.MultiWriter(writer, hasher), remaining: MaximumSectionBytes}
	var count uint64
	previousKey := ""
	for {
		if err := ctx.Err(); err != nil {
			return SectionManifest{}, errors.Join(ErrUnavailable, err)
		}
		raw, found, err := cursor.Next(ctx)
		if err != nil {
			return SectionManifest{}, errors.Join(ErrUnavailable, err)
		}
		if !found {
			break
		}
		key, valid := canonicalObjectKey(raw)
		if count == MaximumRecords || !valid || (previousKey != "" && key <= previousKey) {
			return SectionManifest{}, ErrInvalid
		}
		previousKey = key
		if _, err := section.Write(append(append([]byte(nil), raw...), '\n')); err != nil {
			return SectionManifest{}, err
		}
		count++
	}
	err = cursor.Close()
	closed = true
	if err != nil {
		return SectionManifest{}, errors.Join(ErrUnavailable, err)
	}
	return SectionManifest{Descriptor: descriptor, Path: entryPath, Records: count, Bytes: section.written, SHA256: hex.EncodeToString(hasher.Sum(nil))}, nil
}

func (builder *Builder) writeObjects(ctx context.Context, archive *zip.Writer, request Request, source ObjectSource, existing int) ([]ObjectManifest, error) {
	descriptor := source.Descriptor()
	cursor, err := source.OpenObjects(ctx, request)
	if err != nil || cursor == nil {
		return nil, errors.Join(ErrUnavailable, err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = cursor.Close()
		}
	}()
	entries := make([]ObjectManifest, 0)
	previous := ""
	for {
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(ErrUnavailable, err)
		}
		object, found, err := cursor.Next(ctx)
		if err != nil {
			return nil, errors.Join(ErrUnavailable, err)
		}
		if !found {
			break
		}
		if existing+len(entries) == MaximumObjects || !validObject(object) || (previous != "" && object.Path <= previous) {
			if object.Body != nil {
				_ = object.Body.Close()
			}
			return nil, ErrInvalid
		}
		previous = object.Path
		entryPath := "objects/" + descriptor.Code + "/" + object.Path
		writer, err := archive.CreateHeader(archiveHeader(entryPath))
		if err != nil {
			_ = object.Body.Close()
			return nil, errors.Join(ErrUnavailable, err)
		}
		hasher := sha256.New()
		written, copyErr := io.Copy(io.MultiWriter(writer, hasher), io.LimitReader(object.Body, object.Size+1))
		closeErr := object.Body.Close()
		if copyErr != nil || closeErr != nil {
			return nil, errors.Join(ErrUnavailable, copyErr, closeErr)
		}
		if written != object.Size || written > MaximumObjectBytes || !bytes.Equal(hasher.Sum(nil), object.SHA256[:]) {
			return nil, ErrInvalid
		}
		entries = append(entries, ObjectManifest{Source: descriptor.Code, Path: entryPath, MediaType: object.MediaType, Bytes: written, SHA256: hex.EncodeToString(object.SHA256[:])})
	}
	err = cursor.Close()
	closed = true
	if err != nil {
		return nil, errors.Join(ErrUnavailable, err)
	}
	return entries, nil
}

func validRequest(value Request) bool {
	snapshot := value.Snapshot
	minimumSnapshot := value.RequestedAt.Add(-time.Minute)
	maximumSnapshot := value.RequestedAt.Add(MaximumSnapshotDelay)
	return ids.Validate(string(value.AccountID)) == nil && ids.Validate(value.ExportID) == nil && ids.Validate(string(value.RequestedBy)) == nil &&
		!value.RequestedAt.IsZero() && value.ExpiresAt.After(value.RequestedAt) && value.ExpiresAt.Sub(value.RequestedAt) <= 30*24*time.Hour &&
		routecontext.ValidCellID(snapshot.CellID) && snapshot.PlacementGeneration > 0 && snapshot.AccountVersion > 0 &&
		!snapshot.GlobalAt.Before(minimumSnapshot) && !snapshot.CellAt.Before(minimumSnapshot) &&
		!snapshot.GlobalAt.After(maximumSnapshot) && !snapshot.CellAt.After(maximumSnapshot) &&
		!snapshot.GlobalAt.After(value.ExpiresAt) && !snapshot.CellAt.After(value.ExpiresAt)
}

func validDescriptor(value Descriptor) bool {
	if !validCode.MatchString(value.Code) || value.SchemaVersion == 0 || len(value.Stores) == 0 || len(value.Stores) > 3 {
		return false
	}
	stores := append([]string(nil), value.Stores...)
	slices.Sort(stores)
	for index, store := range stores {
		if !validStore.MatchString(store) || (index > 0 && stores[index-1] == store) {
			return false
		}
	}
	return slices.Equal(stores, value.Stores)
}

func validObject(value Object) bool {
	clean := path.Clean(value.Path)
	return value.Body != nil && value.Path != "" && len(value.Path) <= 512 && clean == value.Path && clean != "." && clean != ".." &&
		!strings.HasPrefix(clean, "../") && !strings.HasPrefix(clean, "/") && !strings.Contains(clean, "\\") &&
		value.Size >= 0 && value.Size <= MaximumObjectBytes && value.SHA256 != [sha256.Size]byte{} &&
		value.MediaType != "" && len(value.MediaType) <= 200 && value.MediaType == strings.TrimSpace(value.MediaType) && !strings.ContainsAny(value.MediaType, "\r\n")
}

func canonicalObjectKey(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || len(raw) > 1<<20 || raw[0] != '{' || raw[len(raw)-1] != '}' {
		return "", false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if decoder.Decode(&value) != nil || value == nil || !decoderAtEnd(decoder) {
		return "", false
	}
	canonical, err := json.Marshal(value)
	key, keyOK := value["key"].(string)
	validKey := keyOK && key != "" && len(key) <= 512 && strings.TrimSpace(key) == key && !strings.ContainsAny(key, "\x00\r\n")
	return key, err == nil && bytes.Equal(canonical, raw) && validKey
}

func decoderAtEnd(decoder *json.Decoder) bool {
	var trailing any
	return errors.Is(decoder.Decode(&trailing), io.EOF)
}

func normalizedSnapshot(value Snapshot) Snapshot {
	value.GlobalAt, value.CellAt = value.GlobalAt.UTC(), value.CellAt.UTC()
	return value
}

func archiveHeader(name string) *zip.FileHeader {
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetModTime(time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC))
	header.SetMode(0o600)
	return header
}

type boundedWriter struct {
	target    io.Writer
	remaining int64
	written   int64
}

func (writer *boundedWriter) Write(value []byte) (int, error) {
	if int64(len(value)) > writer.remaining {
		return 0, ErrInvalid
	}
	n, err := writer.target.Write(value)
	writer.remaining -= int64(n)
	writer.written += int64(n)
	if err == nil && n != len(value) {
		err = io.ErrShortWrite
	}
	return n, err
}

func (descriptor Descriptor) String() string {
	return fmt.Sprintf("%s@%d", descriptor.Code, descriptor.SchemaVersion)
}

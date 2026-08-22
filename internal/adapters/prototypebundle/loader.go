// Package prototypebundle loads a sealed prototype migration export from a
// local operator-controlled directory without following bundle-internal links.
package prototypebundle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/tinfoyle/spyglass-engine/internal/application/prototypemigration"
)

const maximumManifestBytes = 16 << 20

type rollbackCheckpoint struct {
	Version           string `json:"version"`
	AccountID         string `json:"account_id"`
	TenantID          string `json:"tenant_id"`
	SourceCheckpoint  string `json:"source_checkpoint"`
	ManifestSHA256    string `json:"manifest_sha256"`
	DestinationWrites bool   `json:"destination_writes"`
}

func Load(directory string) (prototypemigration.Bundle, error) {
	root, err := filepath.Abs(strings.TrimSpace(directory))
	if err != nil || strings.TrimSpace(directory) == "" {
		return prototypemigration.Bundle{}, prototypemigration.ErrManifest
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return prototypemigration.Bundle{}, errors.Join(err, prototypemigration.ErrManifest)
	}
	manifestRaw, err := readRegular(root, "manifest.json", maximumManifestBytes)
	if err != nil {
		return prototypemigration.Bundle{}, err
	}
	var manifest prototypemigration.Manifest
	if err := decodeExact(manifestRaw, &manifest); err != nil {
		return prototypemigration.Bundle{}, prototypemigration.ErrManifest
	}
	unresolvedRaw, err := readRegular(root, "unresolved.json", maximumManifestBytes)
	if err != nil {
		return prototypemigration.Bundle{}, err
	}
	var unresolved []prototypemigration.UnresolvedRecord
	if err := decodeExact(unresolvedRaw, &unresolved); err != nil || !reflect.DeepEqual(unresolved, manifest.Unresolved) {
		return prototypemigration.Bundle{}, prototypemigration.ErrManifest
	}
	checkpointRaw, err := readRegular(root, "rollback-checkpoint.json", 1<<20)
	if err != nil {
		return prototypemigration.Bundle{}, err
	}
	var checkpoint rollbackCheckpoint
	if err := decodeExact(checkpointRaw, &checkpoint); err != nil || checkpoint.Version != manifest.Version || checkpoint.AccountID != string(manifest.AccountID) || checkpoint.TenantID != manifest.Source.TenantID || checkpoint.SourceCheckpoint != manifest.Source.Checkpoint || checkpoint.ManifestSHA256 != manifest.ContentSHA256 || checkpoint.DestinationWrites {
		return prototypemigration.Bundle{}, prototypemigration.ErrManifest
	}
	objectsRoot := filepath.Join(root, "objects")
	objectsInfo, err := os.Lstat(objectsRoot)
	if err != nil || !objectsInfo.IsDir() || objectsInfo.Mode()&os.ModeSymlink != 0 {
		return prototypemigration.Bundle{}, errors.Join(err, prototypemigration.ErrManifest)
	}
	bundle := prototypemigration.Bundle{Manifest: manifest, Objects: map[string][]byte{}}
	expected := map[string]struct{}{}
	for _, document := range manifest.Documents {
		if document.Action != "import_reconstructed_text" {
			continue
		}
		clean := filepath.Clean(filepath.FromSlash(document.ObjectPath))
		if filepath.IsAbs(clean) || filepath.Dir(clean) != "objects" || filepath.Base(clean) == "." || strings.Contains(clean, "..") {
			return prototypemigration.Bundle{}, prototypemigration.ErrManifest
		}
		name := filepath.Base(clean)
		body, err := readRegular(objectsRoot, name, document.ByteSize)
		if err != nil || int64(len(body)) != document.ByteSize {
			return prototypemigration.Bundle{}, errors.Join(err, prototypemigration.ErrManifest)
		}
		expected[name] = struct{}{}
		bundle.Objects[filepath.ToSlash(clean)] = body
	}
	entries, err := os.ReadDir(objectsRoot)
	if err != nil || len(entries) != len(expected) {
		return prototypemigration.Bundle{}, errors.Join(err, prototypemigration.ErrManifest)
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
			return prototypemigration.Bundle{}, prototypemigration.ErrManifest
		}
		if _, exists := expected[entry.Name()]; !exists {
			return prototypemigration.Bundle{}, prototypemigration.ErrManifest
		}
	}
	rootEntries, err := os.ReadDir(root)
	if err != nil || len(rootEntries) != 4 {
		return prototypemigration.Bundle{}, errors.Join(err, prototypemigration.ErrManifest)
	}
	for _, entry := range rootEntries {
		switch entry.Name() {
		case "manifest.json", "unresolved.json", "rollback-checkpoint.json", "objects":
		default:
			return prototypemigration.Bundle{}, prototypemigration.ErrManifest
		}
	}
	if err := prototypemigration.Verify(bundle); err != nil {
		return prototypemigration.Bundle{}, err
	}
	return bundle, nil
}

func readRegular(parent, name string, maximum int64) ([]byte, error) {
	path := filepath.Join(parent, name)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 0 || info.Size() > maximum {
		return nil, errors.Join(err, prototypemigration.ErrManifest)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.Join(err, prototypemigration.ErrManifest)
	}
	defer file.Close()
	value, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(value)) != info.Size() {
		return nil, errors.Join(err, prototypemigration.ErrManifest)
	}
	return value, nil
}

func decodeExact(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("expected exactly one JSON value")
	}
	return nil
}

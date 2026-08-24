package main

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const applicationUID = 65532

func main() {
	if len(os.Args) < 2 {
		fatal(errors.New("at least one provider-secret volume root is required"))
	}
	for _, root := range os.Args[1:] {
		if err := initialize(root); err != nil {
			fatal(err)
		}
	}
}

func initialize(root string) error {
	return initializeForOwner(root, applicationUID, applicationUID)
}

func initializeForOwner(root string, uid, gid int) error {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return errors.New("provider-secret volume root must be an absolute clean path")
	}
	vaultRoot := filepath.Join(root, "vault")
	if err := os.MkdirAll(vaultRoot, 0o700); err != nil {
		return fmt.Errorf("create provider-secret vault: %w", err)
	}
	if err := os.Chown(vaultRoot, uid, gid); err != nil {
		return fmt.Errorf("own provider-secret vault: %w", err)
	}
	if err := os.Chmod(vaultRoot, 0o700); err != nil {
		return fmt.Errorf("restrict provider-secret vault: %w", err)
	}
	keyPath := filepath.Join(root, "key")
	file, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		info, statErr := os.Lstat(keyPath)
		if statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() != 32 {
			return errors.New("existing provider-secret key is invalid")
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("create provider-secret key: %w", err)
	}
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(keyPath)
		}
	}()
	if _, err := io.CopyN(file, rand.Reader, 32); err != nil {
		return fmt.Errorf("generate provider-secret key: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync provider-secret key: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close provider-secret key: %w", err)
	}
	if err := os.Chown(keyPath, uid, gid); err != nil {
		return fmt.Errorf("own provider-secret key: %w", err)
	}
	ok = true
	return nil
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

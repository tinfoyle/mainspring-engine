package localartifacts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountexport"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

var (
	ErrConfiguration = errors.New("Account export staging configuration is invalid")
	ErrUnavailable   = errors.New("Account export staging is unavailable")
)

type Stager struct {
	root     string
	rootInfo os.FileInfo
}

// Prepare creates only the final staging directory with private permissions.
// Its parent must already exist; this lets orchestrators mount a bounded
// ephemeral volume at the parent without allowing an arbitrary directory tree
// to be manufactured from configuration.
func Prepare(root string) error {
	root = strings.TrimSpace(root)
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root || root == string(filepath.Separator) {
		return ErrConfiguration
	}
	err := os.Mkdir(root, 0o700)
	if err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%w: create root: %v", ErrConfiguration, err)
	}
	info, inspectErr := os.Lstat(root)
	if inspectErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return ErrConfiguration
	}
	return nil
}

func New(root string) (*Stager, error) {
	root = strings.TrimSpace(root)
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root || root == string(filepath.Separator) {
		return nil, ErrConfiguration
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, fmt.Errorf("%w: inspect root: %v", ErrConfiguration, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return nil, ErrConfiguration
	}
	return &Stager{root: root, rootInfo: info}, nil
}

func (stager *Stager) Create(ctx context.Context, exportID string) (accountexport.StagedArtifact, error) {
	if stager == nil || ids.Validate(exportID) != nil {
		return nil, accountexport.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(ErrUnavailable, err)
	}
	current, err := os.Lstat(stager.root)
	if err != nil || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || current.Mode().Perm()&0o077 != 0 || !os.SameFile(stager.rootInfo, current) {
		return nil, fmt.Errorf("%w: staging root identity changed", ErrConfiguration)
	}
	file, err := os.CreateTemp(stager.root, "export-"+exportID+"-*.zip")
	if err != nil {
		return nil, fmt.Errorf("%w: create staging file: %v", ErrUnavailable, err)
	}
	stage := &fileStage{file: file, path: file.Name()}
	if err := file.Chmod(0o600); err != nil {
		_ = stage.Cleanup()
		return nil, fmt.Errorf("%w: secure staging file: %v", ErrUnavailable, err)
	}
	if err := ctx.Err(); err != nil {
		_ = stage.Cleanup()
		return nil, errors.Join(ErrUnavailable, err)
	}
	return stage, nil
}

type fileStage struct {
	file    *os.File
	path    string
	sealed  bool
	cleaned bool
}

func (stage *fileStage) Write(value []byte) (int, error) {
	if stage == nil || stage.file == nil || stage.sealed || stage.cleaned {
		return 0, ErrUnavailable
	}
	return stage.file.Write(value)
}

func (stage *fileStage) Rewind() (io.Reader, error) {
	if stage == nil || stage.file == nil || stage.cleaned {
		return nil, ErrUnavailable
	}
	if _, err := stage.file.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("%w: rewind staging file: %v", ErrUnavailable, err)
	}
	stage.sealed = true
	return stage.file, nil
}

func (stage *fileStage) Cleanup() error {
	if stage == nil || stage.cleaned {
		return nil
	}
	stage.cleaned = true
	var closeErr, removeErr error
	if stage.file != nil {
		closeErr = stage.file.Close()
	}
	if stage.path != "" {
		removeErr = os.Remove(stage.path)
		if errors.Is(removeErr, os.ErrNotExist) {
			removeErr = nil
		}
	}
	if closeErr != nil || removeErr != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, errors.Join(closeErr, removeErr))
	}
	return nil
}

var _ accountexport.ArtifactStager = (*Stager)(nil)

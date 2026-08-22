package requestbody

import (
	"crypto/sha256"
	"errors"
	"io"
	"os"
)

var ErrTooLarge = errors.New("request body exceeds its limit")

type Capture struct {
	path   string
	size   int64
	digest [sha256.Size]byte
}

func Read(source io.Reader, maximum int64) (*Capture, error) {
	if source == nil || maximum <= 0 {
		return nil, errors.New("request body source and positive limit are required")
	}
	file, err := os.CreateTemp("", "spyglass-request-body-*")
	if err != nil {
		return nil, err
	}
	path := file.Name()
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(path)
	}
	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hasher), io.LimitReader(source, maximum+1))
	if err != nil {
		cleanup()
		return nil, err
	}
	if written > maximum {
		cleanup()
		return nil, ErrTooLarge
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	var digest [sha256.Size]byte
	copy(digest[:], hasher.Sum(nil))
	return &Capture{path: path, size: written, digest: digest}, nil
}

func (value *Capture) Open() (*os.File, error) {
	if value == nil || value.path == "" {
		return nil, errors.New("request body capture is closed")
	}
	return os.Open(value.path)
}

func (value *Capture) Size() int64 { return value.size }

func (value *Capture) SHA256() [sha256.Size]byte { return value.digest }

func (value *Capture) Close() error {
	if value == nil || value.path == "" {
		return nil
	}
	path := value.path
	value.path = ""
	return os.Remove(path)
}

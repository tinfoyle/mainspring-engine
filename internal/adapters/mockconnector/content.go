package mockconnector

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"

	marketingdomain "github.com/tinfoyle/spyglass-engine/internal/modules/marketing"
)

// ContentSource is a deterministic, no-network immutable object reader for
// local connector certification. Values are copied at construction and open.
type ContentSource struct {
	mu      sync.RWMutex
	objects map[string][]byte
}

func NewContentSource(objects map[string][]byte) (*ContentSource, error) {
	if len(objects) == 0 {
		return nil, errors.New("mock connector content is required")
	}
	copyObjects := make(map[string][]byte, len(objects))
	for reference, body := range objects {
		if reference == "" || len(body) == 0 {
			return nil, errors.New("mock connector content is invalid")
		}
		copyObjects[reference] = append([]byte(nil), body...)
	}
	return &ContentSource{objects: copyObjects}, nil
}

func (source *ContentSource) OpenContent(_ context.Context, asset marketingdomain.AssetRevision) (io.ReadCloser, error) {
	if source == nil {
		return nil, errors.New("mock connector content is unavailable")
	}
	source.mu.RLock()
	body, exists := source.objects[asset.ContentReference]
	copyBody := append([]byte(nil), body...)
	source.mu.RUnlock()
	if !exists {
		return nil, errors.New("mock connector content is unavailable")
	}
	return io.NopCloser(bytes.NewReader(copyBody)), nil
}

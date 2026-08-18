package catalog

import (
	"errors"
	"sync"
)

// Cache holds one immutable published Catalog snapshot. Replacements may move
// to a lower version when an operator deliberately republishes an older version.
type Cache struct {
	mu      sync.RWMutex
	current PublishedCatalog
}

func NewCache(initial PublishedCatalog) (*Cache, error) {
	if err := initial.Validate(); err != nil {
		return nil, errors.Join(errors.New("initial published catalog is invalid"), err)
	}
	return &Cache{current: initial}, nil
}

func (c *Cache) Current() PublishedCatalog {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.current
}

func (c *Cache) Replace(next PublishedCatalog) error {
	if err := next.Validate(); err != nil {
		return err
	}
	c.mu.Lock()
	c.current = next
	c.mu.Unlock()
	return nil
}

// Package accountdirectory resolves Account placement into a routable cell
// origin without allowing stale placement data to cross the cell boundary.
package accountdirectory

import (
	"container/list"
	"context"
	"errors"
	"net/url"
	"sync"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

const (
	DefaultTTL      = 30 * time.Second
	DefaultCapacity = 10_000
	MaxCapacity     = 1_000_000
)

var (
	ErrNotFound          = errors.New("Account directory entry was not found")
	ErrUnroutable        = errors.New("Account directory entry is not routable")
	ErrPlacementMismatch = errors.New("Account directory placement does not match authorization")
)

type Entry struct {
	AccountID           ids.AccountID
	CellID              ids.CellID
	PlacementGeneration uint64
	State               string
	DataRegion          string
	CellState           string
	RouteOrigin         string
	UpdatedAt           time.Time
}

type Route struct {
	CellID              ids.CellID
	PlacementGeneration uint64
	Origin              url.URL
	DataRegion          string
}

type Source interface {
	Lookup(context.Context, ids.AccountID) (Entry, error)
}

type Clock interface{ Now() time.Time }

type Config struct {
	TTL       time.Duration
	Capacity  int
	AllowHTTP bool
}

type Stats struct {
	Entries               int    `json:"entries"`
	ExpiredEntries        int    `json:"expired_entries"`
	Capacity              int    `json:"capacity"`
	TTLSeconds            int64  `json:"ttl_seconds"`
	OldestEntryAgeSeconds int64  `json:"oldest_entry_age_seconds"`
	Hits                  uint64 `json:"hits"`
	Misses                uint64 `json:"misses"`
	RefreshFailures       uint64 `json:"refresh_failures"`
	Evictions             uint64 `json:"evictions"`
}

type Cache struct {
	source    Source
	clock     Clock
	ttl       time.Duration
	capacity  int
	allowHTTP bool
	mu        sync.Mutex
	entries   map[ids.AccountID]*list.Element
	lru       *list.List
	inflight  map[ids.AccountID]*flight
	hits      uint64
	misses    uint64
	failures  uint64
	evictions uint64
}

type cachedEntry struct {
	accountID ids.AccountID
	route     Route
	loadedAt  time.Time
}

type flight struct {
	done  chan struct{}
	route Route
	err   error
}

func NewCache(source Source, clock Clock, config Config) (*Cache, error) {
	if source == nil || clock == nil {
		return nil, errors.New("Account directory source and clock are required")
	}
	if config.TTL == 0 {
		config.TTL = DefaultTTL
	}
	if config.Capacity == 0 {
		config.Capacity = DefaultCapacity
	}
	if config.TTL <= 0 || config.TTL > 5*time.Minute || config.Capacity <= 0 || config.Capacity > MaxCapacity {
		return nil, errors.New("Account directory cache bounds are invalid")
	}
	return &Cache{source: source, clock: clock, ttl: config.TTL, capacity: config.Capacity, allowHTTP: config.AllowHTTP, entries: make(map[ids.AccountID]*list.Element), lru: list.New(), inflight: make(map[ids.AccountID]*flight)}, nil
}

// Resolve returns only an unexpired route that exactly matches the placement
// generation authorized for this request. A mismatch forces a source refresh.
func (c *Cache) Resolve(ctx context.Context, accountID ids.AccountID, cellID ids.CellID, generation uint64) (Route, error) {
	if ids.Validate(string(accountID)) != nil || !routecontext.ValidCellID(cellID) || generation == 0 {
		return Route{}, ErrPlacementMismatch
	}
	for {
		now := c.clock.Now().UTC()
		c.mu.Lock()
		if element := c.entries[accountID]; element != nil {
			cached := element.Value.(cachedEntry)
			unexpired := now.Before(cached.loadedAt.Add(c.ttl))
			if unexpired && cached.route.CellID == cellID && cached.route.PlacementGeneration == generation {
				c.hits++
				c.lru.MoveToFront(element)
				c.mu.Unlock()
				return cached.route, nil
			}
			if !unexpired {
				delete(c.entries, accountID)
				c.lru.Remove(element)
			}
		}
		if current := c.inflight[accountID]; current != nil {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return Route{}, ctx.Err()
			case <-current.done:
				if current.err != nil {
					return Route{}, current.err
				}
				if current.route.CellID != cellID || current.route.PlacementGeneration != generation {
					return Route{}, ErrPlacementMismatch
				}
				return current.route, nil
			}
		}
		current := &flight{done: make(chan struct{})}
		c.inflight[accountID] = current
		c.misses++
		c.mu.Unlock()

		entry, err := c.source.Lookup(ctx, accountID)
		var route Route
		if err == nil {
			route, err = c.validate(entry)
		}

		c.mu.Lock()
		if err == nil && entry.AccountID != accountID {
			err = ErrUnroutable
		}
		if err == nil {
			c.store(accountID, route, c.clock.Now().UTC())
		} else {
			c.failures++
		}
		current.route, current.err = route, err
		delete(c.inflight, accountID)
		close(current.done)
		c.mu.Unlock()
		if err != nil {
			return Route{}, err
		}
		if route.CellID != cellID || route.PlacementGeneration != generation {
			return Route{}, ErrPlacementMismatch
		}
		return route, nil
	}
}

func (c *Cache) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.clock.Now().UTC()
	stats := Stats{Entries: len(c.entries), Capacity: c.capacity, TTLSeconds: int64(c.ttl / time.Second), Hits: c.hits, Misses: c.misses, RefreshFailures: c.failures, Evictions: c.evictions}
	for _, element := range c.entries {
		cached := element.Value.(cachedEntry)
		if !now.Before(cached.loadedAt.Add(c.ttl)) {
			stats.ExpiredEntries++
		}
		age := max(int64(now.Sub(cached.loadedAt)/time.Second), 0)
		if age > stats.OldestEntryAgeSeconds {
			stats.OldestEntryAgeSeconds = age
		}
	}
	return stats
}

func (c *Cache) validate(entry Entry) (Route, error) {
	if ids.Validate(string(entry.AccountID)) != nil || !routecontext.ValidCellID(entry.CellID) || entry.PlacementGeneration == 0 {
		return Route{}, ErrUnroutable
	}
	if entry.State != "active" && entry.State != "draining" && entry.State != "frozen" {
		return Route{}, ErrUnroutable
	}
	if entry.CellState != "active" && entry.CellState != "draining" {
		return Route{}, ErrUnroutable
	}
	parsed, err := url.Parse(entry.RouteOrigin)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") || (parsed.Scheme != "https" && !(c.allowHTTP && parsed.Scheme == "http")) {
		return Route{}, ErrUnroutable
	}
	parsed.Path = ""
	return Route{CellID: entry.CellID, PlacementGeneration: entry.PlacementGeneration, Origin: *parsed, DataRegion: entry.DataRegion}, nil
}

func (c *Cache) store(accountID ids.AccountID, route Route, loadedAt time.Time) {
	if element := c.entries[accountID]; element != nil {
		element.Value = cachedEntry{accountID: accountID, route: route, loadedAt: loadedAt}
		c.lru.MoveToFront(element)
		return
	}
	element := c.lru.PushFront(cachedEntry{accountID: accountID, route: route, loadedAt: loadedAt})
	c.entries[accountID] = element
	if c.lru.Len() <= c.capacity {
		return
	}
	oldest := c.lru.Back()
	delete(c.entries, oldest.Value.(cachedEntry).accountID)
	c.lru.Remove(oldest)
	c.evictions++
}

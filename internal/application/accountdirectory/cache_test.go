package accountdirectory

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const directoryAccount = ids.AccountID("10000000-0000-4000-8000-000000000001")

func TestCacheHitsAndRefreshesPlacementMismatch(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)}
	source := &testSource{entry: directoryEntry("cell-a", 1)}
	cache, err := NewCache(source, clock, Config{TTL: time.Minute, Capacity: 2, AllowHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Resolve(context.Background(), directoryAccount, "cell-a", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Resolve(context.Background(), directoryAccount, "cell-a", 1); err != nil {
		t.Fatal(err)
	}
	source.set(directoryEntry("cell-b", 2), nil)
	route, err := cache.Resolve(context.Background(), directoryAccount, "cell-b", 2)
	if err != nil || route.CellID != "cell-b" || route.PlacementGeneration != 2 {
		t.Fatalf("route=%+v err=%v", route, err)
	}
	if calls := source.callCount(); calls != 2 {
		t.Fatalf("source calls=%d", calls)
	}
	stats := cache.Stats()
	if stats.Hits != 1 || stats.Misses != 2 || stats.Entries != 1 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestCacheNeverUsesExpiredEntryWhenRefreshFails(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)}
	source := &testSource{entry: directoryEntry("cell-a", 1)}
	cache, _ := NewCache(source, clock, Config{TTL: 10 * time.Second, Capacity: 2, AllowHTTP: true})
	if _, err := cache.Resolve(context.Background(), directoryAccount, "cell-a", 1); err != nil {
		t.Fatal(err)
	}
	clock.advance(11 * time.Second)
	source.set(Entry{}, errors.New("database unavailable"))
	if _, err := cache.Resolve(context.Background(), directoryAccount, "cell-a", 1); err == nil {
		t.Fatal("expected expired route refresh to fail closed")
	}
	stats := cache.Stats()
	if stats.Entries != 0 || stats.RefreshFailures != 1 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestCacheRejectsUnroutableDirectoryData(t *testing.T) {
	tests := map[string]func(*Entry){
		"moving Account":  func(entry *Entry) { entry.State = "moving" },
		"disabled cell":   func(entry *Entry) { entry.CellState = "disabled" },
		"unsafe origin":   func(entry *Entry) { entry.RouteOrigin = "https://cell.internal/path" },
		"empty query":     func(entry *Entry) { entry.RouteOrigin = "https://cell.internal?" },
		"HTTP production": func(entry *Entry) { entry.RouteOrigin = "http://cell.internal" },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			entry := directoryEntry("cell-a", 1)
			change(&entry)
			cache, _ := NewCache(&testSource{entry: entry}, &testClock{now: time.Now()}, Config{})
			if _, err := cache.Resolve(context.Background(), directoryAccount, "cell-a", 1); !errors.Is(err, ErrUnroutable) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestCacheEvictsLeastRecentlyUsedRoute(t *testing.T) {
	clock := &testClock{now: time.Now()}
	source := &accountSource{entries: map[ids.AccountID]Entry{}}
	cache, _ := NewCache(source, clock, Config{Capacity: 2, AllowHTTP: true})
	accounts := []ids.AccountID{
		"10000000-0000-4000-8000-000000000001",
		"10000000-0000-4000-8000-000000000002",
		"10000000-0000-4000-8000-000000000003",
	}
	for index, accountID := range accounts {
		entry := directoryEntry(ids.CellID("cell-"+string(rune('a'+index))), 1)
		entry.AccountID = accountID
		source.entries[accountID] = entry
		if _, err := cache.Resolve(context.Background(), accountID, entry.CellID, 1); err != nil {
			t.Fatal(err)
		}
	}
	stats := cache.Stats()
	if stats.Entries != 2 || stats.Evictions != 1 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestCacheCoalescesConcurrentRefresh(t *testing.T) {
	source := &blockingSource{started: make(chan struct{}), release: make(chan struct{})}
	cache, _ := NewCache(source, &testClock{now: time.Now()}, Config{AllowHTTP: true})
	results := make(chan error, 8)
	for range 8 {
		go func() {
			_, err := cache.Resolve(context.Background(), directoryAccount, "cell-a", 1)
			results <- err
		}()
	}
	<-source.started
	close(source.release)
	for range 8 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if source.calls.Load() != 1 {
		t.Fatalf("source calls=%d", source.calls.Load())
	}
}

func directoryEntry(cellID ids.CellID, generation uint64) Entry {
	return Entry{AccountID: directoryAccount, CellID: cellID, PlacementGeneration: generation, State: "active", DataRegion: "us-east", CellState: "active", RouteOrigin: "http://cell.internal", UpdatedAt: time.Now()}
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *testClock) advance(value time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(value)
	c.mu.Unlock()
}

type testSource struct {
	mu    sync.Mutex
	entry Entry
	err   error
	calls int
}

func (s *testSource) Lookup(context.Context, ids.AccountID) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.entry, s.err
}
func (s *testSource) set(entry Entry, err error) {
	s.mu.Lock()
	s.entry, s.err = entry, err
	s.mu.Unlock()
}
func (s *testSource) callCount() int { s.mu.Lock(); defer s.mu.Unlock(); return s.calls }

type accountSource struct{ entries map[ids.AccountID]Entry }

func (s *accountSource) Lookup(_ context.Context, accountID ids.AccountID) (Entry, error) {
	return s.entries[accountID], nil
}

type blockingSource struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	calls   atomic.Int32
}

func (s *blockingSource) Lookup(context.Context, ids.AccountID) (Entry, error) {
	s.calls.Add(1)
	s.once.Do(func() { close(s.started) })
	<-s.release
	return directoryEntry("cell-a", 1), nil
}

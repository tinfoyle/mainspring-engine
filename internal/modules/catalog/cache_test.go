package catalog

import (
	"testing"
	"time"
)

func TestCacheAllowsReviewedRollbackToOlderVersion(t *testing.T) {
	now := time.Now().UTC()
	current := Default(now)
	cache, err := NewCache(current)
	if err != nil {
		t.Fatal(err)
	}
	rollback := Default(now.Add(time.Minute))
	rollback.Version = 1
	if err := cache.Replace(rollback); err != nil {
		t.Fatal(err)
	}
	if cache.Current().Version != 1 {
		t.Fatal("cache rejected deliberate rollback to an older catalog version")
	}
}

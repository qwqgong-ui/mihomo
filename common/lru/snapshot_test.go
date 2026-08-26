package lru

import (
	"testing"
	"time"
)

// Snapshot must return every entry with its expiry, in least-recently-used
// order so that replaying it into a fresh cache preserves recency.
func TestSnapshotReturnsAllEntriesInLRUOrder(t *testing.T) {
	c := New[string, int](WithSize[string, int](4))
	deadline := time.Now().Add(time.Minute).Truncate(time.Second)
	for _, k := range []string{"a", "b", "c"} {
		c.SetWithExpire(k, 1, deadline)
	}

	// Touch "a" so it becomes most-recent; it must move to the end.
	c.Get("a")

	items := c.Snapshot()
	if len(items) != 3 {
		t.Fatalf("Snapshot() = %d items, want 3", len(items))
	}
	if items[len(items)-1].Key != "a" {
		t.Fatalf("Snapshot() last key = %q, want \"a\" (most recent)", items[len(items)-1].Key)
	}
	for _, item := range items {
		if !item.Expires.Equal(deadline) {
			t.Fatalf("key %q: expires = %v, want %v", item.Key, item.Expires, deadline)
		}
	}
}

// A caller persisting the cache needs stale entries too, so Snapshot must not
// filter on expiry.
func TestSnapshotKeepsExpiredEntries(t *testing.T) {
	c := New[string, int](WithSize[string, int](4), WithStale[string, int](true))
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	c.SetWithExpire("stale", 7, past)

	items := c.Snapshot()
	if len(items) != 1 {
		t.Fatalf("Snapshot() = %d items, want 1", len(items))
	}
	if items[0].Value != 7 || !items[0].Expires.Equal(past) {
		t.Fatalf("Snapshot() = %+v, want value 7 expires %v", items[0], past)
	}
}

// Snapshot must not refresh recency the way Get does.
func TestSnapshotIsSideEffectFree(t *testing.T) {
	c := New[string, int](WithSize[string, int](4))
	deadline := time.Now().Add(time.Minute)
	for _, k := range []string{"a", "b", "c"} {
		c.SetWithExpire(k, 1, deadline)
	}

	first := c.Snapshot()
	second := c.Snapshot()
	for i := range first {
		if first[i].Key != second[i].Key {
			t.Fatalf("Snapshot() order changed between calls: %v then %v", first, second)
		}
	}
}

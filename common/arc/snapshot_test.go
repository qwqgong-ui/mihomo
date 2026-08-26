package arc

import (
	"testing"
	"time"
)

// Snapshot must return every live entry with its expiry, and must not return
// ghost entries (ARC keeps those as eviction bookkeeping without a value).
func TestSnapshotReturnsLiveEntriesOnly(t *testing.T) {
	a := New[string, int](WithSize[string, int](2))
	deadline := time.Now().Add(time.Minute).Truncate(time.Second)

	a.SetWithExpire("a", 1, deadline)
	a.SetWithExpire("b", 2, deadline)

	got := map[string]int{}
	for _, item := range a.Snapshot() {
		got[item.Key] = item.Value
		if !item.Expires.Equal(deadline) {
			t.Fatalf("key %q: expires = %v, want %v", item.Key, item.Expires, deadline)
		}
	}
	if len(got) != 2 || got["a"] != 1 || got["b"] != 2 {
		t.Fatalf("Snapshot() = %v, want a=1 b=2", got)
	}

	// Overflow the cache so at least one key is demoted to a ghost entry.
	a.SetWithExpire("c", 3, deadline)
	a.SetWithExpire("d", 4, deadline)

	items := a.Snapshot()
	if len(items) > a.Len() {
		t.Fatalf("Snapshot() returned %d items, cache reports %d live", len(items), a.Len())
	}
	for _, item := range items {
		if _, ok := a.Get(item.Key); !ok {
			t.Fatalf("Snapshot() returned ghost key %q", item.Key)
		}
	}
}

// Snapshot must not perturb ARC's replacement state the way Get does.
func TestSnapshotIsSideEffectFree(t *testing.T) {
	a := New[string, int](WithSize[string, int](4))
	deadline := time.Now().Add(time.Minute)
	for _, k := range []string{"a", "b", "c", "d"} {
		a.SetWithExpire(k, 1, deadline)
	}

	before := a.Len()
	_ = a.Snapshot()
	if after := a.Len(); after != before {
		t.Fatalf("Len() = %d after Snapshot, want %d", after, before)
	}
}

// Expired entries stay in ARC (get does not check expires), so Snapshot must
// hand back their real expiry rather than dropping or rewriting them.
func TestSnapshotKeepsExpiredEntries(t *testing.T) {
	a := New[string, int](WithSize[string, int](4))
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	a.SetWithExpire("stale", 7, past)

	items := a.Snapshot()
	if len(items) != 1 {
		t.Fatalf("Snapshot() = %d items, want 1", len(items))
	}
	if items[0].Value != 7 || !items[0].Expires.Equal(past) {
		t.Fatalf("Snapshot() = %+v, want value 7 expires %v", items[0], past)
	}
}

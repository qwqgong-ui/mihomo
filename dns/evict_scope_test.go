package dns

import (
	"testing"
	"time"

	D "github.com/miekg/dns"
)

func TestEvictNetworkScopeRemovesOnlyTheRetiredBranch(t *testing.T) {
	persistMu.Lock()
	previous := persistCaches
	persistCaches = make(map[string]dnsCache)
	persistMu.Unlock()
	t.Cleanup(func() {
		persistMu.Lock()
		persistCaches = previous
		persistMu.Unlock()
	})

	cache := Config{}.newCache()
	registerPersistentCache("direct-source-1", cache)

	const retired = "environment|retired-fingerprint"
	const kept = "environment|kept-fingerprint"

	answer := new(D.Msg).SetQuestion("example.com.", D.TypeA)
	expires := time.Now().Add(24 * time.Hour)
	retiredKey := retired + keySep + "example.com. IN A" + keySep + "1.1.1.1"
	keptKey := kept + keySep + "example.com. IN A" + keySep + "1.1.1.1"
	cache.SetWithExpire(retiredKey, answer, expires)
	cache.SetWithExpire(keptKey, answer, expires)

	if got := EvictNetworkScope(retired); got != 1 {
		t.Fatalf("evicted %d entries, want 1", got)
	}

	if _, _, hit := cache.GetWithExpire(retiredKey); hit {
		t.Fatal("a retired network's answers survived eviction")
	}
	if _, _, hit := cache.GetWithExpire(keptKey); !hit {
		t.Fatal("eviction removed a network that is still tracked")
	}
}

func TestEvictNetworkScopeRejectsEmptyScope(t *testing.T) {
	// An empty environment must never be turned into a prefix: every scoped key
	// would match it and one blank call would wipe every network's branch.
	persistMu.Lock()
	previous := persistCaches
	persistCaches = make(map[string]dnsCache)
	persistMu.Unlock()
	t.Cleanup(func() {
		persistMu.Lock()
		persistCaches = previous
		persistMu.Unlock()
	})

	cache := Config{}.newCache()
	registerPersistentCache("direct-source-1", cache)
	key := "environment|kept" + keySep + "example.com. IN A"
	cache.SetWithExpire(key, new(D.Msg), time.Now().Add(time.Hour))

	if got := EvictNetworkScope("   "); got != 0 {
		t.Fatalf("blank scope evicted %d entries, want 0", got)
	}
	if _, _, hit := cache.GetWithExpire(key); !hit {
		t.Fatal("a blank scope wiped a live network's answers")
	}
}

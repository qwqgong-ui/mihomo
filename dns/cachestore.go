package dns

import (
	"strings"
	"sync"
	"time"

	"github.com/metacubex/mihomo/common/arc"
	"github.com/metacubex/mihomo/common/lru"
	"github.com/metacubex/mihomo/component/profile/cachefile"
	"github.com/metacubex/mihomo/log"

	D "github.com/miekg/dns"
)

// StoreInterval is how often the DNS answer cache is written to the cache file
// on top of the flush that runs at shutdown.
const StoreInterval = time.Hour

// arcSnapshot and lruSnapshot are the snapshot APIs of the two cache
// implementations behind cache-algorithm. They return different item types, so
// snapshotOf normalises them; a cache providing neither is simply not
// persisted.
type arcSnapshot interface {
	Snapshot() []arc.Item[string, *D.Msg]
}

type lruSnapshot interface {
	Snapshot() []lru.Item[string, *D.Msg]
}

// cacheItem is one entry from either implementation.
type cacheItem struct {
	Key     string
	Value   *D.Msg
	Expires time.Time
}

// snapshotOf returns c's entries, or ok=false when c cannot be snapshotted.
func snapshotOf(c dnsCache) (items []cacheItem, ok bool) {
	switch sc := c.(type) {
	case arcSnapshot:
		for _, item := range sc.Snapshot() {
			items = append(items, cacheItem{Key: item.Key, Value: item.Value, Expires: item.Expires})
		}
	case lruSnapshot:
		for _, item := range sc.Snapshot() {
			items = append(items, cacheItem{Key: item.Key, Value: item.Value, Expires: item.Expires})
		}
	default:
		return nil, false
	}
	return items, true
}

// keySep separates a resolver's name from the DNS question inside a persisted
// key. mihomo runs several resolvers (main, proxy-server, direct) with
// independent caches; they share one bucket, so their keys must not collide.
const keySep = "\x00"

var (
	persistMu     sync.Mutex
	persistCaches = make(map[string]dnsCache)
	storeOnce     sync.Once
)

// registerPersistentCache remembers the live cache so StoreCache can snapshot
// it later, and restores whatever the previous run left behind.
//
// It must not touch the cache file: resolvers are built before the runtime has
// finished pointing C.Path at the -d directory, and cachefile.Cache() is a
// singleton that permanently latches whatever path it first sees. Calling it
// here made the whole process open (and fail on) the default
// $HOME/.config/mihomo/cache.db, which left the fake-ip pool with no store at
// all. LoadPersistentCache does the file work, once the runtime is ready.
func registerPersistentCache(name string, c dnsCache) {
	if _, ok := snapshotOf(c); !ok {
		return
	}

	persistMu.Lock()
	persistCaches[name] = c
	persistMu.Unlock()
}

// LoadPersistentCache restores previously persisted answers into every
// registered cache and starts the periodic snapshot. Call it from the runtime
// after the DNS resolvers are in place, never during their construction.
func LoadPersistentCache() {
	persistMu.Lock()
	caches := make(map[string]dnsCache, len(persistCaches))
	for name, c := range persistCaches {
		caches[name] = c
	}
	persistMu.Unlock()

	if len(caches) == 0 {
		return
	}
	entries := cachefile.Cache().DNSCache()
	for name, c := range caches {
		loadCache(name, c, entries)
	}
	storeOnce.Do(startStoreLoop)
}

// loadCache restores persisted answers. Entries keep their original expiry, so
// an answer that went stale while mihomo was down is restored stale: the
// optimistic-cache path serves it once and refreshes it in the background,
// exactly as it would have without a restart.
func loadCache(name string, c dnsCache, entries []cachefile.DNSEntry) {
	if len(entries) == 0 {
		return
	}

	prefix := name + keySep
	restored := 0
	for _, entry := range entries {
		key, ok := strings.CutPrefix(entry.Key, prefix)
		if !ok {
			continue
		}
		msg := new(D.Msg)
		if err := msg.Unpack(entry.Msg); err != nil {
			continue
		}
		c.SetWithExpire(key, msg, entry.Expires)
		restored++
	}
	if restored > 0 {
		log.Infoln("[DNS] restored %d cached answers for the %s resolver", restored, name)
	}
}

// StoreCache writes the current DNS answer cache to the cache file. It is safe
// to call when the cache is absent or cannot be snapshotted.
func StoreCache() {
	persistMu.Lock()
	caches := make(map[string]dnsCache, len(persistCaches))
	for name, c := range persistCaches {
		caches[name] = c
	}
	persistMu.Unlock()

	var entries []cachefile.DNSEntry
	for name, c := range caches {
		items, ok := snapshotOf(c)
		if !ok {
			continue
		}
		for _, item := range items {
			if item.Value == nil {
				continue
			}
			packed, err := item.Value.Pack()
			if err != nil {
				continue
			}
			entries = append(entries, cachefile.DNSEntry{
				Key:     name + keySep + item.Key,
				Msg:     packed,
				Expires: item.Expires,
			})
		}
	}

	cachefile.Cache().SetDNSCache(entries)
	log.Debugln("[DNS] stored %d cached answers to cache file", len(entries))
}

func startStoreLoop() {
	go func() {
		ticker := time.NewTicker(StoreInterval)
		defer ticker.Stop()
		for range ticker.C {
			StoreCache()
		}
	}()
}

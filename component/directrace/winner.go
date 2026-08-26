package directrace

import (
	"net/netip"
	"strings"
	"sync"
	"time"
)

const winnerTTL = 30 * time.Second

type winnerKey struct {
	host    string
	adapter string
	ipv6    bool
}

type winnerEntry struct {
	ip      netip.Addr
	expires time.Time
}

var winners = struct {
	sync.Mutex
	entries map[winnerKey]winnerEntry
}{entries: make(map[winnerKey]winnerEntry)}

func Store(host, adapter string, ip netip.Addr) {
	ip = ip.Unmap()
	if host == "" || adapter == "" || !ip.IsValid() {
		return
	}
	winners.Lock()
	winners.entries[winnerKey{host: canonicalHost(host), adapter: adapter, ipv6: ip.Is6()}] = winnerEntry{
		ip:      ip,
		expires: time.Now().Add(winnerTTL),
	}
	winners.Unlock()
}

// Prefer returns a recent ICMP winner only while it remains in the current
// DNS RRset. Callers use it as a warm preference, never as proof that an
// application protocol is reachable.
func Prefer(host, adapter string, candidates []netip.Addr) (netip.Addr, bool) {
	if host == "" || adapter == "" || len(candidates) == 0 {
		return netip.Addr{}, false
	}
	key := winnerKey{host: canonicalHost(host), adapter: adapter, ipv6: candidates[0].Unmap().Is6()}
	winners.Lock()
	entry, loaded := winners.entries[key]
	if loaded && time.Now().After(entry.expires) {
		delete(winners.entries, key)
		loaded = false
	}
	winners.Unlock()
	if !loaded {
		return netip.Addr{}, false
	}
	for _, candidate := range candidates {
		if candidate.Unmap() == entry.ip {
			return entry.ip, true
		}
	}
	return netip.Addr{}, false
}

func canonicalHost(host string) string {
	return strings.TrimSuffix(strings.ToLower(host), ".")
}

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

// maxWinners bounds the table. An entry is only dropped when a later lookup of
// that exact key finds it expired, so a destination visited once and never
// again would otherwise sit here for the life of the process. Winners are a
// warm preference with a 30s life; losing some of them costs a race, not
// correctness.
const maxWinners = 2048

func Store(host, adapter string, ip netip.Addr) {
	ip = ip.Unmap()
	if host == "" || adapter == "" || !ip.IsValid() {
		return
	}
	now := time.Now()
	key := winnerKey{host: canonicalHost(host), adapter: adapter, ipv6: ip.Is6()}
	winners.Lock()
	if _, replacing := winners.entries[key]; !replacing && len(winners.entries) >= maxWinners {
		sweepExpiredLocked(now)
		if len(winners.entries) >= maxWinners {
			// Everything still live: drop one arbitrary entry rather than let
			// destination names grow the table without bound.
			for existing := range winners.entries {
				delete(winners.entries, existing)
				break
			}
		}
	}
	winners.entries[key] = winnerEntry{
		ip:      ip,
		expires: now.Add(winnerTTL),
	}
	winners.Unlock()
}

// sweepExpiredLocked must be called with winners held.
func sweepExpiredLocked(now time.Time) {
	for key, entry := range winners.entries {
		if now.After(entry.expires) {
			delete(winners.entries, key)
		}
	}
}

// Prefer returns a recent path winner only while it remains in the current DNS
// RRset. Callers use it as a warm preference, never as proof that an application
// protocol is still reachable.
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

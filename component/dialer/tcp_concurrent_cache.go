package dialer

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/metacubex/mihomo/common/lru"
)

const (
	defaultTCPConcurrentCacheSize = 4096
	defaultTCPConcurrentCacheTTL  = 30 * time.Minute
	// tcpConcurrentFastPathTimeout is the fast-path budget used when no RTT
	// sample is available yet for a destination (first hit after a cache
	// entry is created, or after NewTCPConcurrentCache/Set without a sample).
	tcpConcurrentFastPathTimeout = 100 * time.Millisecond

	// minFastPathTimeout/maxFastPathTimeout bound the RTT-derived fast-path
	// timeout so a very fast local RTT sample doesn't starve a slightly
	// slower retry, and a very slow sample doesn't stall the fallback race
	// for too long.
	minFastPathTimeout    = 30 * time.Millisecond
	maxFastPathTimeout    = 500 * time.Millisecond
	fastPathRTTMultiplier = 2
)

type tcpConcurrentCacheEntry struct {
	winner   netip.Addr
	rtt      time.Duration
	expireAt time.Time
}

// fastPathTimeoutFor derives the fast-path dial budget from the last
// measured connect RTT for a destination. Without a sample it falls back to
// the fixed default so first-hit behavior is unchanged.
func fastPathTimeoutFor(rtt time.Duration) time.Duration {
	if rtt <= 0 {
		return tcpConcurrentFastPathTimeout
	}
	timeout := min(max(rtt*fastPathRTTMultiplier, minFastPathTimeout), maxFastPathTimeout)
	return timeout
}

// TCPConcurrentCache stores the most recent successful address for a TCP
// destination. It is deliberately independent from the DNS cache: callers
// still have to validate a cached winner against the current DNS candidates.
type TCPConcurrentCache struct {
	entries *lru.LruCache[string, tcpConcurrentCacheEntry]
	ttl     time.Duration
	now     func() time.Time
}

// NewTCPConcurrentCache creates a bounded winner cache. Non-positive values
// use the production defaults.
func NewTCPConcurrentCache(maxSize int, ttl time.Duration) *TCPConcurrentCache {
	if maxSize <= 0 {
		maxSize = defaultTCPConcurrentCacheSize
	}
	if ttl <= 0 {
		ttl = defaultTCPConcurrentCacheTTL
	}
	return &TCPConcurrentCache{
		entries: lru.New(lru.WithSize[string, tcpConcurrentCacheEntry](maxSize)),
		ttl:     ttl,
		now:     time.Now,
	}
}

func (c *TCPConcurrentCache) getEntry(key string) (tcpConcurrentCacheEntry, bool) {
	if c == nil || key == "" {
		return tcpConcurrentCacheEntry{}, false
	}
	now := c.now()
	entry, loaded := c.entries.Compute(key, func(entry tcpConcurrentCacheEntry, loaded bool) (tcpConcurrentCacheEntry, bool) {
		if !loaded || !now.Before(entry.expireAt) {
			return tcpConcurrentCacheEntry{}, true
		}
		return entry, false
	})
	if !loaded {
		return tcpConcurrentCacheEntry{}, false
	}
	return entry, true
}

// Get returns an unexpired cached winner.
func (c *TCPConcurrentCache) Get(key string) (netip.Addr, bool) {
	entry, loaded := c.getEntry(key)
	if !loaded {
		return netip.Addr{}, false
	}
	return entry.winner, true
}

// RTT returns the connect latency observed the last time this destination's
// winner was recorded, if any sample was captured.
func (c *TCPConcurrentCache) RTT(key string) (time.Duration, bool) {
	entry, loaded := c.getEntry(key)
	if !loaded || entry.rtt <= 0 {
		return 0, false
	}
	return entry.rtt, true
}

// Set records a successful TCP winner and starts a fresh lifetime, without a
// latency sample (the fast-path timeout for it falls back to the default
// until a sample is recorded via SetWithRTT).
func (c *TCPConcurrentCache) Set(key string, winner netip.Addr) {
	c.SetWithRTT(key, winner, 0)
}

// SetWithRTT records a successful TCP winner together with how long the
// winning connect took, so future fast-path attempts to this destination can
// size their timeout from real latency instead of the fixed default.
func (c *TCPConcurrentCache) SetWithRTT(key string, winner netip.Addr, rtt time.Duration) {
	if c == nil || key == "" || !winner.IsValid() {
		return
	}
	if rtt < 0 {
		rtt = 0
	}
	c.entries.Set(key, tcpConcurrentCacheEntry{
		winner:   winner.Unmap(),
		rtt:      rtt,
		expireAt: c.now().Add(c.ttl),
	})
}

// SetIfFaster records winner only when it is the first measured candidate or
// beats the still-live sample already stored for the scoped destination.
func (c *TCPConcurrentCache) SetIfFaster(key string, winner netip.Addr, rtt time.Duration) bool {
	if c == nil || key == "" || !winner.IsValid() || rtt <= 0 {
		return false
	}
	now := c.now()
	updated := false
	c.entries.Compute(key, func(current tcpConcurrentCacheEntry, loaded bool) (tcpConcurrentCacheEntry, bool) {
		if loaded && now.Before(current.expireAt) && current.rtt > 0 && current.rtt <= rtt {
			return current, false
		}
		updated = true
		return tcpConcurrentCacheEntry{winner: winner.Unmap(), rtt: rtt, expireAt: now.Add(c.ttl)}, false
	})
	return updated
}

// Delete removes a destination from the cache.
func (c *TCPConcurrentCache) Delete(key string) {
	if c == nil || key == "" {
		return
	}
	c.entries.Delete(key)
}

// Clear removes every cached TCP winner.
func (c *TCPConcurrentCache) Clear() {
	if c == nil {
		return
	}
	c.entries.Clear()
}

var tcpConcurrentCache = NewTCPConcurrentCache(0, 0)

// ClearTCPConcurrentCache clears the winner cache without changing DNS cache
// entries or DNS semantics.
func ClearTCPConcurrentCache() {
	tcpConcurrentCache.Clear()
}

func tcpConcurrentCacheKey(host, port, network string) (string, bool) {
	return tcpConcurrentCacheScopedKey(host, port, network, "")
}

func tcpConcurrentCacheScopedKey(host, port, network, scope string) (string, bool) {
	switch network {
	case "tcp", "tcp4", "tcp6":
	default:
		return "", false
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" {
		return "", false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return "", false
	}
	key := net.JoinHostPort(host, port) + "/" + network
	if scope != "" {
		key += "\x00" + scope
	}
	return key, true
}

func containsTCPConcurrentCandidate(candidates []netip.Addr, winner netip.Addr) bool {
	winner = winner.Unmap()
	for _, candidate := range candidates {
		if candidate.Unmap() == winner {
			return true
		}
	}
	return false
}

func tcpConcurrentDialContext(ctx context.Context, network, host string, ips []netip.Addr, port string, opt option, fallback dialFunc) dialResult {
	key, cacheable := tcpConcurrentCacheKey(host, port, network)
	if !cacheable {
		return fallback(ctx, network, ips, port, opt)
	}

	if cachedIP, loaded := tcpConcurrentCache.Get(key); loaded {
		if !containsTCPConcurrentCandidate(ips, cachedIP) {
			tcpConcurrentCache.Delete(key)
		} else {
			fastCtx, cancelFast := context.WithCancel(ctx)
			fastResult := make(chan dialResult)
			fastStart := time.Now()
			go func() {
				result := dialResult{ip: cachedIP}
				result.Conn, result.error = dialContext(fastCtx, network, cachedIP, port, opt)
				select {
				case fastResult <- result:
				case <-fastCtx.Done():
					if result.Conn != nil {
						_ = result.Conn.Close()
					}
				}
			}()

			rtt, _ := tcpConcurrentCache.RTT(key)
			fastTimer := time.NewTimer(fastPathTimeoutFor(rtt))
			select {
			case result := <-fastResult:
				if !fastTimer.Stop() {
					select {
					case <-fastTimer.C:
					default:
					}
				}
				cancelFast()
				if result.error == nil {
					if tfoDialIsAsynchronous(opt) {
						// The fast-path dial handed back a stub instantly;
						// keep the winner but don't record a bogus near-zero
						// RTT sample from it.
						tcpConcurrentCache.Set(key, cachedIP)
					} else {
						tcpConcurrentCache.SetWithRTT(key, cachedIP, measuredDialDuration(fastStart))
					}
					return result
				}
				if ctx.Err() != nil {
					return dialResult{error: ctx.Err()}
				}
				tcpConcurrentCache.Delete(key)
			case <-fastTimer.C:
				cancelFast()
				tcpConcurrentCache.Delete(key)
			case <-ctx.Done():
				if !fastTimer.Stop() {
					select {
					case <-fastTimer.C:
					default:
					}
				}
				cancelFast()
				return dialResult{error: ctx.Err()}
			}
		}
	}

	result := fallback(ctx, network, ips, port, opt)
	if result.error == nil && result.ip.IsValid() {
		// Use the winning candidate's own measured connect time, never the
		// wall-clock span of the whole fallback call: when fallback is a
		// dual-stack race, a non-preferred winner can sit ready for up to
		// dualStackFallbackTimeout before being returned, and timing the call
		// would fold that unrelated waiting into the sample. dialDuration is
		// zero only when the dial was never really timed -- the lazy TFO
		// dialer returns a stub before any network I/O -- and SetWithRTT then
		// keeps the winner without recording a sample for it.
		tcpConcurrentCache.SetWithRTT(key, result.ip, result.dialDuration)
	}
	return result
}

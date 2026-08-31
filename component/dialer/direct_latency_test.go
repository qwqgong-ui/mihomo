package dialer

import (
	"context"
	"net"
	"net/netip"
	"sort"
	"testing"
	"time"
)

// The tests here measure DIRECT's *internal* handling cost: DNS answers and
// TCP connects are served instantly by fakes, so every measured nanosecond is
// mihomo's own work (goroutine fan-out, channel hand-offs, timers, cache
// lookups). Thresholds are deliberately loose so a loaded CI box doesn't turn
// them into flakes; the useful output is the logged distribution.

type instantLookup struct {
	controlledLookup
}

func newInstantLookup(v4, v6 []netip.Addr) *instantLookup {
	return &instantLookup{controlledLookup{
		v4Gate:  closedGate(),
		v6Gate:  closedGate(),
		v4:      v4,
		v6:      v6,
		timeout: 100 * time.Millisecond,
	}}
}

// instantDialer completes every connect immediately, without the attempt
// bookkeeping controlledDialer does (an unbuffered channel would dominate the
// measurement).
type instantDialer struct{}

func (instantDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	left, right := net.Pipe()
	_ = right.Close()
	return left, nil
}

type latencyStats struct {
	p50, p95, p99, p100 time.Duration
}

func percentiles(samples []time.Duration) latencyStats {
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	at := func(p float64) time.Duration {
		if len(sorted) == 0 {
			return 0
		}
		index := max(int(float64(len(sorted))*p/100+0.5)-1, 0)
		if index >= len(sorted) {
			index = len(sorted) - 1
		}
		return sorted[index]
	}
	return latencyStats{p50: at(50), p95: at(95), p99: at(99), p100: at(100)}
}

func measure(t *testing.T, name string, iterations int, dial func() (net.Conn, error)) latencyStats {
	t.Helper()
	// Warm up caches and let the allocator settle before sampling.
	for range 20 {
		conn, err := dial()
		if err != nil {
			t.Fatalf("%s warmup failed: %v", name, err)
		}
		_ = conn.Close()
	}
	samples := make([]time.Duration, 0, iterations)
	for i := range iterations {
		start := time.Now()
		conn, err := dial()
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("%s iteration %d failed: %v", name, i, err)
		}
		_ = conn.Close()
		samples = append(samples, elapsed)
	}
	stats := percentiles(samples)
	t.Logf("%s over %d dials: p50=%s p95=%s p99=%s p100=%s",
		name, iterations, stats.p50, stats.p95, stats.p99, stats.p100)
	return stats
}

var (
	latencyV4 = []netip.Addr{netip.MustParseAddr("198.51.100.1"), netip.MustParseAddr("198.51.100.2")}
	latencyV6 = []netip.Addr{netip.MustParseAddr("2001:db8::1"), netip.MustParseAddr("2001:db8::2")}
)

// TestDirectDialInternalLatencyBaseline records what a bare dial through the
// injected dialer costs, so the race numbers below can be read as overhead
// rather than absolute time.
func TestDirectDialInternalLatencyBaseline(t *testing.T) {
	stats := measure(t, "baseline netDialer", 500, func() (net.Conn, error) {
		return instantDialer{}.DialContext(context.Background(), "tcp", "198.51.100.1:443")
	})
	if stats.p100 > 20*time.Millisecond {
		t.Fatalf("baseline p100 %s is implausibly slow; machine too loaded to trust the other numbers", stats.p100)
	}
}

// TestDirectDualStackInternalLatencyPercentiles covers the default DIRECT TCP
// path: A and AAAA answered at once, four candidates racing.
func TestDirectDualStackInternalLatencyPercentiles(t *testing.T) {
	SetTcpConcurrent(false)
	lookup := newInstantLookup(latencyV4, latencyV6)
	dial := instantDialer{}

	stats := measure(t, "direct dual-stack race", 500, func() (net.Conn, error) {
		return DialContext(context.Background(), "tcp", "example.test:443",
			WithResolver(lookup), WithNetDialer(dial), WithDirectDualStack(),
			WithDirectRacePreference("DIRECT"))
	})
	if stats.p95 > 5*time.Millisecond {
		t.Fatalf("direct dual-stack p95 %s exceeds 5ms of internal overhead", stats.p95)
	}
	if stats.p100 > 50*time.Millisecond {
		t.Fatalf("direct dual-stack p100 %s exceeds 50ms; a timer or lookup budget is leaking into the fast path", stats.p100)
	}
}

// TestDirectPreferredInternalLatencyPercentiles covers prefer: ipv6, whose
// state machine arms an extra preference timer.
func TestDirectPreferredInternalLatencyPercentiles(t *testing.T) {
	SetTcpConcurrent(false)
	lookup := newInstantLookup(latencyV4, latencyV6)
	dial := instantDialer{}

	stats := measure(t, "direct prefer-ipv6 race", 500, func() (net.Conn, error) {
		return DialContext(context.Background(), "tcp", "example.test:443",
			WithResolver(lookup), WithNetDialer(dial), WithDirectDualStack(),
			WithDirectRacePreference("DIRECT"), WithPreferIPv6())
	})
	if stats.p95 > 5*time.Millisecond {
		t.Fatalf("direct prefer-ipv6 p95 %s exceeds 5ms of internal overhead", stats.p95)
	}
	if stats.p100 > 50*time.Millisecond {
		t.Fatalf("direct prefer-ipv6 p100 %s exceeds 50ms; the preference timer is being waited on", stats.p100)
	}
}

// TestTCPConcurrentCacheInternalLatencyPercentiles covers the tcp-concurrent
// path, where a warm cache entry should make every dial a single fast-path
// connect.
func TestTCPConcurrentCacheInternalLatencyPercentiles(t *testing.T) {
	SetTcpConcurrent(true)
	t.Cleanup(func() {
		SetTcpConcurrent(false)
		ClearTCPConcurrentCache()
	})
	ClearTCPConcurrentCache()
	lookup := newInstantLookup(latencyV4, latencyV6)
	dial := instantDialer{}

	stats := measure(t, "tcp-concurrent cached fast path", 500, func() (net.Conn, error) {
		return DialContext(context.Background(), "tcp", "example.test:443",
			WithResolver(lookup), WithNetDialer(dial))
	})
	if stats.p95 > 5*time.Millisecond {
		t.Fatalf("tcp-concurrent p95 %s exceeds 5ms of internal overhead", stats.p95)
	}
	if stats.p100 > 50*time.Millisecond {
		t.Fatalf("tcp-concurrent p100 %s exceeds 50ms; the fast-path timer is firing on a warm entry", stats.p100)
	}
}

func BenchmarkDirectDualStackInternal(b *testing.B) {
	SetTcpConcurrent(false)
	lookup := newInstantLookup(latencyV4, latencyV6)
	dial := instantDialer{}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		conn, err := DialContext(context.Background(), "tcp", "example.test:443",
			WithResolver(lookup), WithNetDialer(dial), WithDirectDualStack(),
			WithDirectRacePreference("DIRECT"))
		if err != nil {
			b.Fatal(err)
		}
		_ = conn.Close()
	}
}

func TestJoinAddrPortMatchesJoinHostPort(t *testing.T) {
	for _, destination := range []netip.Addr{
		netip.MustParseAddr("198.51.100.1"),
		netip.MustParseAddr("2001:db8::1"),
		netip.MustParseAddr("::ffff:198.51.100.1"),
		netip.MustParseAddr("fe80::1%eth0"),
		netip.IPv4Unspecified(),
		netip.IPv6Unspecified(),
		{},
	} {
		want := net.JoinHostPort(destination.String(), "443")
		if got := joinAddrPort(destination, "443"); got != want {
			t.Fatalf("joinAddrPort(%v) = %q, want %q", destination, got, want)
		}
	}
}

func BenchmarkJoinAddrPort(b *testing.B) {
	destination := netip.MustParseAddr("2001:db8::1")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = joinAddrPort(destination, "443")
	}
}

func TestUnmapAddrsDoesNotMutateResolverSlice(t *testing.T) {
	mapped := netip.MustParseAddr("::ffff:198.51.100.1")
	shared := []netip.Addr{netip.MustParseAddr("2001:db8::1"), mapped}
	unmapped := unmapAddrs(shared)

	if shared[1] != mapped {
		t.Fatalf("resolver slice was rewritten in place: %v", shared[1])
	}
	if want := netip.MustParseAddr("198.51.100.1"); unmapped[1] != want {
		t.Fatalf("unmapAddrs returned %v, want %v", unmapped[1], want)
	}
	if unmapped[0] != shared[0] {
		t.Fatalf("unmapAddrs changed an already-unmapped address: %v", unmapped[0])
	}

	clean := []netip.Addr{netip.MustParseAddr("198.51.100.1")}
	if got := unmapAddrs(clean); &got[0] != &clean[0] {
		t.Fatal("unmapAddrs copied a slice that needed no change")
	}
}

package dialer

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	R "github.com/metacubex/mihomo/component/resolver"
)

type progressiveTestResolver struct {
	v4       chan R.IPCandidateBatch
	v6       chan R.IPCandidateBatch
	promoted chan netip.Addr
}

func (r *progressiveTestResolver) LookupIPCandidates(_ context.Context, _ string, ipv6 bool, _ string) <-chan R.IPCandidateBatch {
	if ipv6 {
		return r.v6
	}
	return r.v4
}

func (r *progressiveTestResolver) PromoteIP(_ string, _ bool, _ string, ip netip.Addr) {
	r.promoted <- ip.Unmap()
}

func TestProgressiveDirectReturnsFirstDNSRaceAndAcceptsLaterFasterSource(t *testing.T) {
	ClearTCPConcurrentCache()
	t.Cleanup(ClearTCPConcurrentCache)
	v4 := make(chan R.IPCandidateBatch, 2)
	v6 := make(chan R.IPCandidateBatch)
	close(v6)
	progressive := &progressiveTestResolver{v4: v4, v6: v6, promoted: make(chan netip.Addr, 4)}
	firstIP := netip.MustParseAddr("192.0.2.1")
	laterIP := netip.MustParseAddr("192.0.2.2")
	v4 <- R.IPCandidateBatch{IPs: []netip.Addr{firstIP}, Source: 0}

	dial := NetDialerFunc(func(ctx context.Context, _, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		delay := 25 * time.Millisecond
		if host == laterIP.String() {
			delay = 5 * time.Millisecond
		}
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
		client, peer := net.Pipe()
		go func() {
			<-ctx.Done()
			_ = peer.Close()
		}()
		return client, nil
	})

	conn, err := directProgressiveDialContext(context.Background(), "tcp", "race.example:443", option{netDialer: dial}, progressive)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	select {
	case got := <-progressive.promoted:
		if got != firstIP {
			t.Fatalf("first promoted IP = %s", got)
		}
	case <-time.After(time.Second):
		t.Fatal("first DNS source did not start TCP promptly")
	}

	v4 <- R.IPCandidateBatch{IPs: []netip.Addr{laterIP}, Source: 1}
	close(v4)
	select {
	case got := <-progressive.promoted:
		if got != laterIP {
			t.Fatalf("later promoted IP = %s", got)
		}
	case <-time.After(time.Second):
		t.Fatal("later DNS source did not refresh the TCP winner")
	}

	key, ok := tcpConcurrentCacheScopedKey("race.example", "443", "tcp", "default")
	if !ok {
		t.Fatal("missing TCP winner cache key")
	}
	winner, loaded := tcpConcurrentCache.Get(key)
	if !loaded || winner != laterIP {
		t.Fatalf("TCP winner = %s, loaded=%v", winner, loaded)
	}
}

func TestProgressiveDirectUsesTCPConcurrentWinnerBeforeStaleCandidates(t *testing.T) {
	cache := installTestTCPConcurrentCache(t)
	previousConcurrent := GetTcpConcurrent()
	SetTcpConcurrent(true)
	t.Cleanup(func() { SetTcpConcurrent(previousConcurrent) })
	SetDirectNetworkEnvironment("tcp-cache-test")
	t.Cleanup(func() { SetDirectNetworkEnvironment("") })

	cachedIP := netip.MustParseAddr("192.0.2.2")
	otherIP := netip.MustParseAddr("192.0.2.1")
	key, ok := tcpConcurrentCacheScopedKey("stale.example", "443", "tcp", "environment|tcp-cache-test")
	if !ok {
		t.Fatal("missing scoped TCP winner cache key")
	}
	cache.SetWithRTT(key, cachedIP, 5*time.Millisecond)

	v4 := make(chan R.IPCandidateBatch, 1)
	v4 <- R.IPCandidateBatch{IPs: []netip.Addr{otherIP, cachedIP}, Source: -1}
	close(v4)
	v6 := make(chan R.IPCandidateBatch)
	close(v6)
	progressive := &progressiveTestResolver{v4: v4, v6: v6, promoted: make(chan netip.Addr, 4)}
	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })
	dial := newTestTCPDialer(map[netip.Addr][]testDialBehavior{
		otherIP:  {{release: blocked}},
		cachedIP: {{release: closedTestGate()}},
	})

	conn, err := directProgressiveDialContext(context.Background(), "tcp", "stale.example:443", option{netDialer: dial}, progressive)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	time.Sleep(10 * time.Millisecond)
	if dial.count(cachedIP) != 1 || dial.count(otherIP) != 0 {
		t.Fatalf("attempt counts = cached:%d other:%d; want cached:1 other:0", dial.count(cachedIP), dial.count(otherIP))
	}
	if winner, loaded := cache.Get(key); !loaded || winner != cachedIP {
		t.Fatalf("TCP winner = %s, loaded=%v; want %s, true", winner, loaded, cachedIP)
	}
}

func TestProgressiveDirectTCPConcurrentFailureFallsBackToAllCandidates(t *testing.T) {
	cache := installTestTCPConcurrentCache(t)
	previousConcurrent := GetTcpConcurrent()
	SetTcpConcurrent(true)
	t.Cleanup(func() { SetTcpConcurrent(previousConcurrent) })
	SetDirectNetworkEnvironment("tcp-cache-fallback-test")
	t.Cleanup(func() { SetDirectNetworkEnvironment("") })

	cachedIP := netip.MustParseAddr("192.0.2.2")
	otherIP := netip.MustParseAddr("192.0.2.1")
	key, ok := tcpConcurrentCacheScopedKey("fallback.example", "443", "tcp", "environment|tcp-cache-fallback-test")
	if !ok {
		t.Fatal("missing scoped TCP winner cache key")
	}
	cache.SetWithRTT(key, cachedIP, 5*time.Millisecond)

	v4 := make(chan R.IPCandidateBatch, 1)
	v4 <- R.IPCandidateBatch{IPs: []netip.Addr{otherIP, cachedIP}, Source: -1}
	close(v4)
	v6 := make(chan R.IPCandidateBatch)
	close(v6)
	progressive := &progressiveTestResolver{v4: v4, v6: v6, promoted: make(chan netip.Addr, 4)}
	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })
	dial := newTestTCPDialer(map[netip.Addr][]testDialBehavior{
		otherIP: {{release: closedTestGate()}},
		cachedIP: {
			{release: closedTestGate(), err: errors.New("cached address failed")},
			{release: blocked},
		},
	})

	conn, err := directProgressiveDialContext(context.Background(), "tcp", "fallback.example:443", option{netDialer: dial}, progressive)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	waitForAttemptCount(t, dial, cachedIP, 2)
	if dial.count(otherIP) != 1 {
		t.Fatalf("fallback attempts for other candidate = %d, want 1", dial.count(otherIP))
	}
	if winner, loaded := cache.Get(key); !loaded || winner != otherIP {
		t.Fatalf("replacement TCP winner = %s, loaded=%v; want %s, true", winner, loaded, otherIP)
	}
}

func TestProgressiveDirectTCPConcurrentTimeoutFallsBackToAllCandidates(t *testing.T) {
	cache := installTestTCPConcurrentCache(t)
	previousConcurrent := GetTcpConcurrent()
	SetTcpConcurrent(true)
	t.Cleanup(func() { SetTcpConcurrent(previousConcurrent) })
	SetDirectNetworkEnvironment("tcp-cache-timeout-test")
	t.Cleanup(func() { SetDirectNetworkEnvironment("") })

	cachedIP := netip.MustParseAddr("192.0.2.2")
	otherIP := netip.MustParseAddr("192.0.2.1")
	key, ok := tcpConcurrentCacheScopedKey("timeout.example", "443", "tcp", "environment|tcp-cache-timeout-test")
	if !ok {
		t.Fatal("missing scoped TCP winner cache key")
	}
	cache.SetWithRTT(key, cachedIP, time.Millisecond)

	v4 := make(chan R.IPCandidateBatch, 1)
	v4 <- R.IPCandidateBatch{IPs: []netip.Addr{otherIP, cachedIP}, Source: -1}
	close(v4)
	v6 := make(chan R.IPCandidateBatch)
	close(v6)
	progressive := &progressiveTestResolver{v4: v4, v6: v6, promoted: make(chan netip.Addr, 4)}
	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })
	dial := newTestTCPDialer(map[netip.Addr][]testDialBehavior{
		otherIP: {{release: closedTestGate()}},
		cachedIP: {
			{release: nil},
			{release: blocked},
		},
	})

	started := time.Now()
	conn, err := directProgressiveDialContext(context.Background(), "tcp", "timeout.example:443", option{netDialer: dial}, progressive)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if elapsed := time.Since(started); elapsed < minFastPathTimeout {
		t.Fatalf("fallback started before cached fast-path timeout: %s", elapsed)
	}
	waitForAttemptCount(t, dial, cachedIP, 2)
	if dial.count(otherIP) != 1 {
		t.Fatalf("fallback attempts for other candidate = %d, want 1", dial.count(otherIP))
	}
	if winner, loaded := cache.Get(key); !loaded || winner != otherIP {
		t.Fatalf("replacement TCP winner = %s, loaded=%v; want %s, true", winner, loaded, otherIP)
	}
}

func TestProgressiveDirectIgnoresCachedWinnerFromUnavailableFamily(t *testing.T) {
	cache := installTestTCPConcurrentCache(t)
	previousConcurrent := GetTcpConcurrent()
	SetTcpConcurrent(true)
	t.Cleanup(func() { SetTcpConcurrent(previousConcurrent) })
	SetDirectNetworkEnvironment("tcp-cache-family-test")
	t.Cleanup(func() { SetDirectNetworkEnvironment("") })

	cachedIPv6 := netip.MustParseAddr("2001:db8::2")
	currentIPv4 := netip.MustParseAddr("192.0.2.1")
	key, ok := tcpConcurrentCacheScopedKey("family.example", "443", "tcp4", "environment|tcp-cache-family-test")
	if !ok {
		t.Fatal("missing scoped TCP winner cache key")
	}
	cache.SetWithRTT(key, cachedIPv6, 5*time.Millisecond)

	v4 := make(chan R.IPCandidateBatch, 1)
	v4 <- R.IPCandidateBatch{IPs: []netip.Addr{currentIPv4}, Source: -1}
	close(v4)
	v6 := make(chan R.IPCandidateBatch)
	close(v6)
	progressive := &progressiveTestResolver{v4: v4, v6: v6, promoted: make(chan netip.Addr, 4)}
	dial := newTestTCPDialer(map[netip.Addr][]testDialBehavior{
		currentIPv4: {{release: closedTestGate()}},
		cachedIPv6:  {{release: closedTestGate()}},
	})

	conn, err := directProgressiveDialContext(context.Background(), "tcp4", "family.example:443", option{netDialer: dial}, progressive)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if dial.count(currentIPv4) != 1 || dial.count(cachedIPv6) != 0 {
		t.Fatalf("attempt counts = current:%d unavailable:%d; want current:1 unavailable:0", dial.count(currentIPv4), dial.count(cachedIPv6))
	}
	if winner, loaded := cache.Get(key); !loaded || winner != currentIPv4 {
		t.Fatalf("replacement TCP winner = %s, loaded=%v; want %s, true", winner, loaded, currentIPv4)
	}
}

func TestScopeForPrefixesUsesRequestedPrivate16Boundary(t *testing.T) {
	scopeA := scopeForPrefixes("wlan0", []netip.Prefix{netip.MustParsePrefix("192.168.12.34/24")})
	scopeB := scopeForPrefixes("wlan0", []netip.Prefix{netip.MustParsePrefix("192.168.99.8/24")})
	scopeC := scopeForPrefixes("wlan0", []netip.Prefix{netip.MustParsePrefix("192.169.1.8/24")})
	if scopeA != "wlan0|192.168.0.0/16" || scopeB != scopeA {
		t.Fatalf("192.168 /16 scopes: A=%q B=%q", scopeA, scopeB)
	}
	if scopeC == scopeA {
		t.Fatalf("different /16 unexpectedly shared scope %q", scopeC)
	}
}

func TestDirectNetworkScopePrefersPlatformEnvironment(t *testing.T) {
	SetDirectNetworkEnvironment("wifi-fingerprint")
	t.Cleanup(func() { SetDirectNetworkEnvironment("") })
	if got := directNetworkScope(option{interfaceName: "ignored"}); got != "environment|wifi-fingerprint" {
		t.Fatalf("direct network scope = %q", got)
	}
}

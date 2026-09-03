package dialer

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	R "github.com/metacubex/mihomo/component/resolver"
)

// The tests here put the real DIRECT progressive race behind a simulated
// Wi-Fi link: jittered RTT, SYN loss with exponential RTO backoff, a slower
// IPv6 leg, and the two failure shapes that actually show up on Wi-Fi (a
// cached winner that went dead after roaming, and an IPv6 black hole). The
// dialer code under test is unmodified: only the netDialer and the
// progressive resolver are simulated, so every fixed constant in the race
// (fastPathTimeoutFor, dualStackFallbackTimeout) meets realistic timings.

type wifiProfile struct {
	name    string
	rtt     time.Duration // floor for one connect
	jitter  time.Duration // uniform [0, jitter) added on top
	loss    float64       // per-SYN loss probability
	v6Extra time.Duration // IPv6 leg penalty
}

var wifiProfiles = []wifiProfile{
	{name: "5GHz close", rtt: 8 * time.Millisecond, jitter: 6 * time.Millisecond, loss: 0.005, v6Extra: 2 * time.Millisecond},
	{name: "2.4GHz busy", rtt: 30 * time.Millisecond, jitter: 35 * time.Millisecond, loss: 0.02, v6Extra: 8 * time.Millisecond},
	{name: "weak/edge", rtt: 90 * time.Millisecond, jitter: 160 * time.Millisecond, loss: 0.08, v6Extra: 25 * time.Millisecond},
}

type wifiLink struct {
	profile   wifiProfile
	mu        sync.Mutex
	rnd       *rand.Rand
	dead      map[netip.Addr]bool // answers RST after one RTT
	blackhole map[netip.Addr]bool // never answers at all
	attempts  atomic.Int64        // every connect the race actually issues
}

// wifiConn reports the address that was dialed. net.Pipe's own RemoteAddr is
// the literal string "pipe", which would make every winner unidentifiable.
type wifiConn struct {
	net.Conn
	remote net.Addr
}

func (c wifiConn) RemoteAddr() net.Addr { return c.remote }

type wifiAddr string

func (a wifiAddr) Network() string { return "tcp" }
func (a wifiAddr) String() string  { return string(a) }

func newWiFiLink(profile wifiProfile) *wifiLink {
	return &wifiLink{
		profile:   profile,
		rnd:       rand.New(rand.NewPCG(0x5eed, 0xda7a)),
		dead:      map[netip.Addr]bool{},
		blackhole: map[netip.Addr]bool{},
	}
}

// sample returns how long this handshake takes and whether it completes at
// all. A lost SYN costs a full RTO before the retransmit, doubling each time,
// which is what makes Wi-Fi tails so much longer than their median.
func (l *wifiLink) sample(ipv6 bool) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delay := l.profile.rtt
	if l.profile.jitter > 0 {
		delay += time.Duration(l.rnd.Int64N(int64(l.profile.jitter)))
	}
	if ipv6 {
		delay += l.profile.v6Extra
	}
	rto := time.Second
	for range 3 {
		if l.rnd.Float64() >= l.profile.loss {
			return delay, true
		}
		delay += rto
		rto *= 2
	}
	return delay, false
}

func (l *wifiLink) flag(ip netip.Addr) (dead bool, blackhole bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.dead[ip], l.blackhole[ip]
}

func (l *wifiLink) netDialer() NetDialerFunc {
	return func(ctx context.Context, _, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ip := netip.MustParseAddr(host)
		l.attempts.Add(1)
		dead, blackhole := l.flag(ip)
		if blackhole {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		delay, completed := l.sample(ip.Is6())
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
		if dead || !completed {
			return nil, errors.New("connection refused")
		}
		left, right := net.Pipe()
		_ = right.Close()
		return wifiConn{Conn: left, remote: wifiAddr(address)}, nil
	}
}

// wifiResolver stands in for the direct-nameserver progressive resolver. warm
// mirrors a live 24h source-cache entry (candidates published with no network
// I/O); a cold lookup pays one resolver round trip over the same Wi-Fi link.
type wifiResolver struct {
	link   *wifiLink
	v4, v6 []netip.Addr
	warm   bool
}

func (r *wifiResolver) LookupIPCandidates(ctx context.Context, _ string, ipv6 bool, _ string) <-chan R.IPCandidateBatch {
	out := make(chan R.IPCandidateBatch, 1)
	ips := r.v4
	if ipv6 {
		ips = r.v6
	}
	go func() {
		defer close(out)
		if !r.warm {
			delay, completed := r.link.sample(false)
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			if !completed {
				out <- R.IPCandidateBatch{Err: errors.New("dns timeout")}
				return
			}
		}
		out <- R.IPCandidateBatch{IPs: ips, Source: 0}
	}()
	return out
}

func (r *wifiResolver) PromoteIP(string, bool, string, netip.Addr) {}

var (
	wifiV4 = []netip.Addr{netip.MustParseAddr("203.0.113.10"), netip.MustParseAddr("203.0.113.11")}
	wifiV6 = []netip.Addr{netip.MustParseAddr("2001:db8:f00d::10"), netip.MustParseAddr("2001:db8:f00d::11")}
)

func runWiFiScenario(t *testing.T, iterations int, host string, opt option, link *wifiLink, progressive *wifiResolver, before func(key string)) {
	t.Helper()
	key, ok := tcpConcurrentCacheScopedKey(host, "443", "tcp", directNetworkScope(opt))
	if !ok {
		t.Fatalf("no cache key for %s", host)
	}
	link.attempts.Store(0)
	samples := make([]time.Duration, 0, iterations)
	v6Wins, failures := 0, 0
	for range iterations {
		tcpConcurrentCache.Delete(key)
		if before != nil {
			before(key)
		}
		start := time.Now()
		conn, err := directProgressiveDialContext(context.Background(), "tcp", net.JoinHostPort(host, "443"), opt, progressive)
		elapsed := time.Since(start)
		samples = append(samples, elapsed)
		if err != nil {
			failures++
			continue
		}
		if remote, parseErr := netip.ParseAddrPort(conn.RemoteAddr().String()); parseErr == nil && remote.Addr().Is6() {
			v6Wins++
		}
		_ = conn.Close()
	}
	stats := percentiles(samples)
	t.Logf("    p50=%-9s p95=%-9s p99=%-9s max=%-9s  connects/dial=%.2f  v6 wins=%d/%d  failed=%d",
		roundWiFi(stats.p50), roundWiFi(stats.p95), roundWiFi(stats.p99), roundWiFi(stats.p100),
		float64(link.attempts.Load())/float64(iterations), v6Wins, iterations, failures)
}

func roundWiFi(d time.Duration) time.Duration { return d.Round(100 * time.Microsecond) }

const wifiIterations = 60

func TestDirectUnderSimulatedWiFi(t *testing.T) {
	if testing.Short() {
		t.Skip("simulated Wi-Fi timings run in real time; several minutes")
	}
	SetTcpConcurrent(true)
	t.Cleanup(func() {
		SetTcpConcurrent(false)
		ClearTCPConcurrentCache()
	})

	for index, profile := range wifiProfiles {
		t.Logf("=== %s (rtt %s + jitter<%s, loss %.1f%%, v6 +%s) ===",
			profile.name, profile.rtt, profile.jitter, profile.loss*100, profile.v6Extra)

		seedWinner := func(key string) { tcpConcurrentCache.SetWithRTT(key, wifiV4[0], profile.rtt) }

		// 1. Cold: DNS costs a round trip, then four candidates race.
		link := newWiFiLink(profile)
		opt := option{netDialer: link.netDialer()}
		t.Logf("  [1] cold start (DNS needs one round trip)")
		runWiFiScenario(t, wifiIterations, fmt.Sprintf("cold%d.example", index), opt, link,
			&wifiResolver{link: link, v4: wifiV4, v6: wifiV6}, nil)

		// 2. Steady state: source cache hit plus a live winner, one connect.
		link = newWiFiLink(profile)
		opt = option{netDialer: link.netDialer()}
		t.Logf("  [2] steady state (DNS cache hit + live winner, fastPathTimeoutFor=%s)", fastPathTimeoutFor(profile.rtt))
		runWiFiScenario(t, wifiIterations, fmt.Sprintf("warm%d.example", index), opt, link,
			&wifiResolver{link: link, v4: wifiV4, v6: wifiV6, warm: true}, seedWinner)

		// 2b. The exact control for [2]: same warm DNS and the same healthy
		// link, but no winner cached, so all four candidates race.
		link = newWiFiLink(profile)
		opt = option{netDialer: link.netDialer()}
		t.Logf("  [2b] control: warm DNS, no winner cached (full race)")
		runWiFiScenario(t, wifiIterations, fmt.Sprintf("race%d.example", index), opt, link,
			&wifiResolver{link: link, v4: wifiV4, v6: wifiV6, warm: true}, nil)

		// 2c. Steady state with two live winners cached: what racing the pair
		// costs when nothing is actually wrong.
		link = newWiFiLink(profile)
		opt = option{netDialer: link.netDialer()}
		t.Logf("  [2c] steady state, two live winners cached")
		runWiFiScenario(t, wifiIterations, fmt.Sprintf("pair%d.example", index), opt, link,
			&wifiResolver{link: link, v4: wifiV4, v6: wifiV6, warm: true},
			func(key string) {
				tcpConcurrentCache.SetWithRTT(key, wifiV4[1], profile.rtt+time.Millisecond)
				tcpConcurrentCache.SetWithRTT(key, wifiV4[0], profile.rtt)
			})

		// 3a. Winner refuses: an RST comes back after one RTT, so the fast
		// path fails fast and the timer never matters.
		link = newWiFiLink(profile)
		link.dead[wifiV4[0]] = true
		opt = option{netDialer: link.netDialer()}
		t.Logf("  [3a] dead winner, answers RST (fast failure)")
		runWiFiScenario(t, wifiIterations, fmt.Sprintf("rst%d.example", index), opt, link,
			&wifiResolver{link: link, v4: wifiV4, v6: wifiV6, warm: true}, seedWinner)

		// 3b. Winner unreachable after roaming: nothing comes back at all, so
		// the whole fast-path budget is spent before the race restarts.
		link = newWiFiLink(profile)
		link.blackhole[wifiV4[0]] = true
		opt = option{netDialer: link.netDialer()}
		t.Logf("  [3b] dead winner, black hole (roamed AP; budget=%s)", fastPathTimeoutFor(profile.rtt))
		runWiFiScenario(t, wifiIterations, fmt.Sprintf("hole%d.example", index), opt, link,
			&wifiResolver{link: link, v4: wifiV4, v6: wifiV6, warm: true}, seedWinner)

		// 3c. Same black hole, but a second winner is cached behind it, so the
		// expired budget hands over instead of releasing the whole field.
		link = newWiFiLink(profile)
		link.blackhole[wifiV4[0]] = true
		opt = option{netDialer: link.netDialer()}
		t.Logf("  [3c] dead winner, black hole, second winner cached")
		runWiFiScenario(t, wifiIterations, fmt.Sprintf("two%d.example", index), opt, link,
			&wifiResolver{link: link, v4: wifiV4, v6: wifiV6, warm: true},
			func(key string) {
				tcpConcurrentCache.SetWithRTT(key, wifiV4[1], profile.rtt+time.Millisecond)
				tcpConcurrentCache.SetWithRTT(key, wifiV4[0], profile.rtt)
			})

		// 4. IPv6 black hole: AAAA answers, nothing ever comes back.
		link = newWiFiLink(profile)
		for _, ip := range wifiV6 {
			link.blackhole[ip] = true
		}
		opt = option{netDialer: link.netDialer()}
		t.Logf("  [4] IPv6 black hole + dual (current config)")
		runWiFiScenario(t, wifiIterations, fmt.Sprintf("v6hole%d.example", index), opt, link,
			&wifiResolver{link: link, v4: wifiV4, v6: wifiV6, warm: true}, nil)

		// 5. Same black hole under prefer: ipv6, as a control.
		link = newWiFiLink(profile)
		for _, ip := range wifiV6 {
			link.blackhole[ip] = true
		}
		opt = option{netDialer: link.netDialer(), prefer: 6}
		t.Logf("  [5] IPv6 black hole + prefer:ipv6 (control; dualStackFallbackTimeout=%s)", dualStackFallbackTimeout)
		runWiFiScenario(t, wifiIterations, fmt.Sprintf("v6pref%d.example", index), opt, link,
			&wifiResolver{link: link, v4: wifiV4, v6: wifiV6, warm: true}, nil)
	}
}

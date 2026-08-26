package dialer

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"

	DR "github.com/metacubex/mihomo/component/directrace"
	R "github.com/metacubex/mihomo/component/resolver"

	D "github.com/miekg/dns"
)

type controlledLookup struct {
	v4Gate  <-chan struct{}
	v6Gate  <-chan struct{}
	v4      []netip.Addr
	v6      []netip.Addr
	timeout time.Duration
}

func (r *controlledLookup) lookup(ctx context.Context, gate <-chan struct{}, ips []netip.Addr) ([]netip.Addr, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-gate:
		return ips, nil
	}
}

func (r *controlledLookup) LookupIP(ctx context.Context, _ string) ([]netip.Addr, error) {
	v4, err := r.LookupIPv4(ctx, "")
	if err != nil {
		return nil, err
	}
	v6, _ := r.LookupIPv6(ctx, "")
	return append(v4, v6...), nil
}

func (r *controlledLookup) LookupIPv4(ctx context.Context, _ string) ([]netip.Addr, error) {
	return r.lookup(ctx, r.v4Gate, r.v4)
}

func (r *controlledLookup) LookupIPv6(ctx context.Context, _ string) ([]netip.Addr, error) {
	return r.lookup(ctx, r.v6Gate, r.v6)
}

func (r *controlledLookup) IPv6Timeout() time.Duration { return r.timeout }
func (r *controlledLookup) ResolveECH(context.Context, string) ([]byte, error) {
	return nil, errors.New("not implemented")
}
func (r *controlledLookup) ExchangeContext(context.Context, *D.Msg) (*D.Msg, error) {
	return nil, errors.New("not implemented")
}
func (r *controlledLookup) Invalid() bool    { return true }
func (r *controlledLookup) ClearCache()      {}
func (r *controlledLookup) ResetConnection() {}

type connectBehavior struct {
	gate <-chan struct{}
	err  error
}

type controlledDialer struct {
	attempts  chan netip.Addr
	behaviors map[netip.Addr]connectBehavior
}

func (d *controlledDialer) DialContext(ctx context.Context, _, address string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ip := netip.MustParseAddr(host)
	d.attempts <- ip
	behavior := d.behaviors[ip]
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-behavior.gate:
		if behavior.err != nil {
			return nil, behavior.err
		}
		left, right := net.Pipe()
		_ = right.Close()
		return left, nil
	}
}

func closedGate() <-chan struct{} {
	gate := make(chan struct{})
	close(gate)
	return gate
}

func TestMain(m *testing.M) {
	R.DisableIPv6.Store(false)
	os.Exit(m.Run())
}

func receiveAttempt(t *testing.T, attempts <-chan netip.Addr) netip.Addr {
	t.Helper()
	select {
	case ip := <-attempts:
		return ip
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for TCP attempt")
		return netip.Addr{}
	}
}

func runDirectRace(t *testing.T, lookup *controlledLookup, dial *controlledDialer, options ...Option) <-chan dialResult {
	t.Helper()
	result := make(chan dialResult, 1)
	go func() {
		options = append([]Option{WithResolver(lookup), WithNetDialer(dial), WithDirectDualStack()}, options...)
		conn, err := DialContext(context.Background(), "tcp", "example.test:443", options...)
		result <- dialResult{Conn: conn, error: err}
	}()
	return result
}

func requireRaceSuccess(t *testing.T, result <-chan dialResult) {
	t.Helper()
	select {
	case result := <-result:
		if result.error != nil {
			t.Fatalf("race failed: %v", result.error)
		}
		_ = result.Conn.Close()
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for race winner")
	}
}

func TestDirectDualStackIPv4StartsBeforeAAAA(t *testing.T) {
	v6Lookup := make(chan struct{})
	v4Winner := make(chan struct{})
	never := make(chan struct{})
	v4a := netip.MustParseAddr("192.0.2.1")
	v4b := netip.MustParseAddr("192.0.2.2")
	lookup := &controlledLookup{
		v4Gate: closedGate(), v6Gate: v6Lookup,
		v4: []netip.Addr{v4a, v4b}, v6: []netip.Addr{netip.MustParseAddr("2001:db8::1")},
		timeout: 200 * time.Millisecond,
	}
	dial := &controlledDialer{
		attempts: make(chan netip.Addr, 4),
		behaviors: map[netip.Addr]connectBehavior{
			v4a: {gate: v4Winner},
			v4b: {gate: never},
		},
	}
	result := runDirectRace(t, lookup, dial)

	seen := map[netip.Addr]bool{receiveAttempt(t, dial.attempts): true, receiveAttempt(t, dial.attempts): true}
	if !seen[v4a] || !seen[v4b] {
		t.Fatalf("all IPv4 addresses did not start concurrently: %v", seen)
	}
	close(v4Winner)
	requireRaceSuccess(t, result)
}

func TestDirectDualStackAAAAJoinsIPv4Race(t *testing.T) {
	v6Lookup := make(chan struct{})
	v6Winner := make(chan struct{})
	never := make(chan struct{})
	v4 := netip.MustParseAddr("192.0.2.1")
	v6 := netip.MustParseAddr("2001:db8::1")
	lookup := &controlledLookup{
		v4Gate: closedGate(), v6Gate: v6Lookup,
		v4: []netip.Addr{v4}, v6: []netip.Addr{v6}, timeout: 200 * time.Millisecond,
	}
	dial := &controlledDialer{
		attempts: make(chan netip.Addr, 4),
		behaviors: map[netip.Addr]connectBehavior{
			v4: {gate: never},
			v6: {gate: v6Winner},
		},
	}
	result := runDirectRace(t, lookup, dial)
	if got := receiveAttempt(t, dial.attempts); got != v4 {
		t.Fatalf("first attempt = %s, want IPv4 %s", got, v4)
	}
	close(v6Lookup)
	if got := receiveAttempt(t, dial.attempts); got != v6 {
		t.Fatalf("joined attempt = %s, want IPv6 %s", got, v6)
	}
	close(v6Winner)
	requireRaceSuccess(t, result)
}

func TestDirectDualStackAAAAFirstWaitsBeforeIPv4(t *testing.T) {
	v4Lookup := make(chan struct{})
	v4Winner := make(chan struct{})
	never := make(chan struct{})
	v4 := netip.MustParseAddr("192.0.2.1")
	v6 := netip.MustParseAddr("2001:db8::1")
	waitBudget := 80 * time.Millisecond
	lookup := &controlledLookup{
		v4Gate: v4Lookup, v6Gate: closedGate(),
		v4: []netip.Addr{v4}, v6: []netip.Addr{v6}, timeout: waitBudget,
	}
	dial := &controlledDialer{
		attempts: make(chan netip.Addr, 4),
		behaviors: map[netip.Addr]connectBehavior{
			v4: {gate: v4Winner},
			v6: {gate: never},
		},
	}
	result := runDirectRace(t, lookup, dial)
	if got := receiveAttempt(t, dial.attempts); got != v6 {
		t.Fatalf("first attempt = %s, want IPv6 %s", got, v6)
	}
	close(v4Lookup)
	select {
	case got := <-dial.attempts:
		t.Fatalf("IPv4 %s started before ipv6-timeout", got)
	case <-time.After(waitBudget / 2):
	}
	if got := receiveAttempt(t, dial.attempts); got != v4 {
		t.Fatalf("fallback attempt = %s, want IPv4 %s", got, v4)
	}
	close(v4Winner)
	requireRaceSuccess(t, result)
}

func TestDirectDualStackIPv6FailureTriggersImmediateIPv4(t *testing.T) {
	v4Lookup := make(chan struct{})
	v6Failure := make(chan struct{})
	v4Winner := make(chan struct{})
	v4 := netip.MustParseAddr("192.0.2.1")
	v6 := netip.MustParseAddr("2001:db8::1")
	waitBudget := 500 * time.Millisecond
	lookup := &controlledLookup{
		v4Gate: v4Lookup, v6Gate: closedGate(),
		v4: []netip.Addr{v4}, v6: []netip.Addr{v6}, timeout: waitBudget,
	}
	dial := &controlledDialer{
		attempts: make(chan netip.Addr, 4),
		behaviors: map[netip.Addr]connectBehavior{
			v4: {gate: v4Winner},
			v6: {gate: v6Failure, err: errors.New("unreachable")},
		},
	}
	result := runDirectRace(t, lookup, dial)
	if got := receiveAttempt(t, dial.attempts); got != v6 {
		t.Fatalf("first attempt = %s, want IPv6 %s", got, v6)
	}
	close(v4Lookup)
	started := time.Now()
	close(v6Failure)
	if got := receiveAttempt(t, dial.attempts); got != v4 {
		t.Fatalf("fallback attempt = %s, want IPv4 %s", got, v4)
	}
	if elapsed := time.Since(started); elapsed >= waitBudget/2 {
		t.Fatalf("IPv4 fallback waited for timeout after explicit IPv6 failure: %v", elapsed)
	}
	close(v4Winner)
	requireRaceSuccess(t, result)
}

func TestDirectPreferIPv6HoldsReadyIPv4ForIPv6(t *testing.T) {
	v6Lookup := make(chan struct{})
	v4Winner := make(chan struct{})
	v6Winner := make(chan struct{})
	v4 := netip.MustParseAddr("192.0.2.1")
	v6 := netip.MustParseAddr("2001:db8::1")
	lookup := &controlledLookup{
		v4Gate: closedGate(), v6Gate: v6Lookup,
		v4: []netip.Addr{v4}, v6: []netip.Addr{v6}, timeout: 100 * time.Millisecond,
	}
	dial := &controlledDialer{
		attempts: make(chan netip.Addr, 4),
		behaviors: map[netip.Addr]connectBehavior{
			v4: {gate: v4Winner},
			v6: {gate: v6Winner},
		},
	}
	result := runDirectRace(t, lookup, dial, WithPreferIPv6())
	if got := receiveAttempt(t, dial.attempts); got != v4 {
		t.Fatalf("first attempt = %s, want IPv4 %s", got, v4)
	}
	close(v4Winner)
	select {
	case early := <-result:
		if early.Conn != nil {
			_ = early.Conn.Close()
		}
		t.Fatal("ready IPv4 returned before preferred IPv6 was tested")
	case <-time.After(20 * time.Millisecond):
	}
	close(v6Lookup)
	if got := receiveAttempt(t, dial.attempts); got != v6 {
		t.Fatalf("preferred attempt = %s, want IPv6 %s", got, v6)
	}
	close(v6Winner)
	requireRaceSuccess(t, result)
}

func TestDirectPreferIPv6FailureUsesReadyIPv4(t *testing.T) {
	v6Lookup := make(chan struct{})
	v4Winner := make(chan struct{})
	v6Failure := make(chan struct{})
	v4 := netip.MustParseAddr("192.0.2.1")
	v6 := netip.MustParseAddr("2001:db8::1")
	lookup := &controlledLookup{
		v4Gate: closedGate(), v6Gate: v6Lookup,
		v4: []netip.Addr{v4}, v6: []netip.Addr{v6}, timeout: 100 * time.Millisecond,
	}
	dial := &controlledDialer{
		attempts: make(chan netip.Addr, 4),
		behaviors: map[netip.Addr]connectBehavior{
			v4: {gate: v4Winner},
			v6: {gate: v6Failure, err: errors.New("unreachable")},
		},
	}
	result := runDirectRace(t, lookup, dial, WithPreferIPv6())
	if got := receiveAttempt(t, dial.attempts); got != v4 {
		t.Fatalf("first attempt = %s, want IPv4 %s", got, v4)
	}
	close(v4Winner)
	close(v6Lookup)
	if got := receiveAttempt(t, dial.attempts); got != v6 {
		t.Fatalf("preferred attempt = %s, want IPv6 %s", got, v6)
	}
	started := time.Now()
	close(v6Failure)
	requireRaceSuccess(t, result)
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("ready IPv4 was not returned immediately after IPv6 failed: %v", elapsed)
	}
}

func TestDirectPreferIPv6LookupTimeoutUsesReadyIPv4(t *testing.T) {
	v6Lookup := make(chan struct{})
	v4Winner := make(chan struct{})
	v4 := netip.MustParseAddr("192.0.2.1")
	lookupBudget := 40 * time.Millisecond
	lookup := &controlledLookup{
		v4Gate: closedGate(), v6Gate: v6Lookup,
		v4: []netip.Addr{v4}, v6: []netip.Addr{netip.MustParseAddr("2001:db8::1")}, timeout: lookupBudget,
	}
	dial := &controlledDialer{
		attempts: make(chan netip.Addr, 2),
		behaviors: map[netip.Addr]connectBehavior{
			v4: {gate: v4Winner},
		},
	}
	result := runDirectRace(t, lookup, dial, WithPreferIPv6())
	if got := receiveAttempt(t, dial.attempts); got != v4 {
		t.Fatalf("first attempt = %s, want IPv4 %s", got, v4)
	}
	started := time.Now()
	close(v4Winner)
	requireRaceSuccess(t, result)
	if elapsed := time.Since(started); elapsed < lookupBudget/2 || elapsed > 200*time.Millisecond {
		t.Fatalf("IPv4 fallback did not follow the AAAA wait budget: %v", elapsed)
	}
}

func TestDirectPreferIPv6BlackholeUsesWarmIPv4AfterFallbackTimeout(t *testing.T) {
	v4Lookup := make(chan struct{})
	v4Winner := make(chan struct{})
	never := make(chan struct{})
	v4 := netip.MustParseAddr("192.0.2.1")
	v6 := netip.MustParseAddr("2001:db8::1")
	lookup := &controlledLookup{
		v4Gate: v4Lookup, v6Gate: closedGate(),
		v4: []netip.Addr{v4}, v6: []netip.Addr{v6}, timeout: 100 * time.Millisecond,
	}
	dial := &controlledDialer{
		attempts: make(chan netip.Addr, 4),
		behaviors: map[netip.Addr]connectBehavior{
			v4: {gate: v4Winner},
			v6: {gate: never},
		},
	}
	result := runDirectRace(t, lookup, dial, WithPreferIPv6())
	if got := receiveAttempt(t, dial.attempts); got != v6 {
		t.Fatalf("first attempt = %s, want IPv6 %s", got, v6)
	}
	close(v4Lookup)
	if got := receiveAttempt(t, dial.attempts); got != v4 {
		t.Fatalf("fallback attempt = %s, want IPv4 %s", got, v4)
	}
	started := time.Now()
	close(v4Winner)
	requireRaceSuccess(t, result)
	if elapsed := time.Since(started); elapsed < dualStackFallbackTimeout/2 || elapsed > dualStackFallbackTimeout+200*time.Millisecond {
		t.Fatalf("warm IPv4 fallback did not respect preference timeout: %v", elapsed)
	}
}

func TestDirectPreferIPv4MirrorsPreferredFamilySelection(t *testing.T) {
	v4Lookup := make(chan struct{})
	v4Winner := make(chan struct{})
	v6Winner := make(chan struct{})
	v4 := netip.MustParseAddr("192.0.2.1")
	v6 := netip.MustParseAddr("2001:db8::1")
	lookup := &controlledLookup{
		v4Gate: v4Lookup, v6Gate: closedGate(),
		v4: []netip.Addr{v4}, v6: []netip.Addr{v6}, timeout: 100 * time.Millisecond,
	}
	dial := &controlledDialer{
		attempts: make(chan netip.Addr, 4),
		behaviors: map[netip.Addr]connectBehavior{
			v4: {gate: v4Winner},
			v6: {gate: v6Winner},
		},
	}
	result := runDirectRace(t, lookup, dial, WithPreferIPv4())
	if got := receiveAttempt(t, dial.attempts); got != v6 {
		t.Fatalf("first attempt = %s, want IPv6 fallback %s", got, v6)
	}
	close(v6Winner)
	select {
	case early := <-result:
		if early.Conn != nil {
			_ = early.Conn.Close()
		}
		t.Fatal("ready IPv6 returned before preferred IPv4 was tested")
	case <-time.After(20 * time.Millisecond):
	}
	close(v4Lookup)
	if got := receiveAttempt(t, dial.attempts); got != v4 {
		t.Fatalf("preferred attempt = %s, want IPv4 %s", got, v4)
	}
	close(v4Winner)
	requireRaceSuccess(t, result)
}

var _ R.Resolver = (*controlledLookup)(nil)
var _ NetDialer = (*controlledDialer)(nil)

func TestPrioritizeDirectWinnerValidatesRRSet(t *testing.T) {
	host := "tcp-game.example"
	adapter := "DIRECT-tcp-test"
	first := netip.MustParseAddr("192.0.2.70")
	winner := netip.MustParseAddr("192.0.2.71")
	DR.Store(host, adapter, winner)
	ordered := prioritizeDirectWinner(host, adapter, []netip.Addr{first, winner})
	if ordered[0] != winner || ordered[1] != first {
		t.Fatalf("ordered = %v, want winner first", ordered)
	}
	unchanged := prioritizeDirectWinner(host, adapter, []netip.Addr{first})
	if len(unchanged) != 1 || unchanged[0] != first {
		t.Fatalf("stale winner changed RRset: %v", unchanged)
	}
}

func TestDirectDualStackIPv6WinnerNotHeldBySlowA(t *testing.T) {
	v6Winner := make(chan struct{})
	never := make(chan struct{})
	v4 := netip.MustParseAddr("192.0.2.1")
	v6 := netip.MustParseAddr("2001:db8::1")
	waitBudget := 30 * time.Millisecond
	lookup := &controlledLookup{
		v4Gate: never, v6Gate: closedGate(),
		v4: []netip.Addr{v4}, v6: []netip.Addr{v6}, timeout: waitBudget,
	}
	dial := &controlledDialer{
		attempts: make(chan netip.Addr, 4),
		behaviors: map[netip.Addr]connectBehavior{
			v4: {gate: never},
			v6: {gate: v6Winner},
		},
	}
	result := runDirectRace(t, lookup, dial)
	if got := receiveAttempt(t, dial.attempts); got != v6 {
		t.Fatalf("first attempt = %s, want IPv6 %s", got, v6)
	}
	// Let the ipv6-timeout expire while the A query is still outstanding,
	// then complete the IPv6 handshake: the winner must not wait for DNS.
	time.Sleep(waitBudget * 2)
	close(v6Winner)
	requireRaceSuccess(t, result)
}

func TestDirectDualStackFailedIPv4WaitsForPendingAAAA(t *testing.T) {
	v6Lookup := make(chan struct{})
	v4Failure := make(chan struct{})
	v4 := netip.MustParseAddr("192.0.2.1")
	v6 := netip.MustParseAddr("2001:db8::1")
	waitBudget := 30 * time.Millisecond
	lookup := &controlledLookup{
		v4Gate: closedGate(), v6Gate: v6Lookup,
		v4: []netip.Addr{v4}, v6: []netip.Addr{v6}, timeout: waitBudget,
	}
	dial := &controlledDialer{
		attempts: make(chan netip.Addr, 4),
		behaviors: map[netip.Addr]connectBehavior{
			v4: {gate: v4Failure, err: errors.New("unreachable")},
			v6: {gate: closedGate()},
		},
	}
	result := runDirectRace(t, lookup, dial)
	if got := receiveAttempt(t, dial.attempts); got != v4 {
		t.Fatalf("first attempt = %s, want IPv4 %s", got, v4)
	}
	close(v4Failure)
	// Every IPv4 candidate is gone before the AAAA answer arrives; the race
	// must still use it instead of failing when the wait budget expires.
	time.Sleep(waitBudget * 2)
	close(v6Lookup)
	if got := receiveAttempt(t, dial.attempts); got != v6 {
		t.Fatalf("fallback attempt = %s, want IPv6 %s", got, v6)
	}
	requireRaceSuccess(t, result)
}

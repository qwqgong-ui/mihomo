package outbound

import (
	"context"
	"errors"
	"fmt"
	randv2 "math/rand/v2"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"

	N "github.com/metacubex/mihomo/common/net"
	"github.com/metacubex/mihomo/component/dialer"
	"github.com/metacubex/mihomo/component/directrace"
	"github.com/metacubex/mihomo/component/resolver"
	C "github.com/metacubex/mihomo/constant"
)

const (
	directUDPRaceDatagrams = 4
	directUDPRaceBytes     = 16 * 1024
	directUDPRaceWindow    = 300 * time.Millisecond
)

type directUDPReadResult struct {
	data []byte
	put  func()
	addr net.Addr
	err  error
}

type directUDPTarget struct {
	logical    netip.AddrPort
	candidates []netip.AddrPort
	live       map[netip.AddrPort]bool
	winner     netip.AddrPort
	started    time.Time
	datagrams  int
	bytes      int
}

type directUDPRacePacketConn struct {
	mu        sync.Mutex
	closeOnce sync.Once
	closed    chan struct{}
	reads     chan directUDPReadResult
	conns     map[int]net.PacketConn
	alive     int
	targets   map[netip.AddrPort]*directUDPTarget
	sources   map[netip.AddrPort][]*directUDPTarget
	factory   func(context.Context, int, netip.AddrPort) (net.PacketConn, error)
	readBy    time.Time
	writeBy   time.Time
}

func (d *Direct) listenPacketRaceContext(ctx context.Context, metadata *C.Metadata) (C.PacketConn, error) {
	candidates, fallback, err := d.resolveUDPRaceCandidates(ctx, metadata)
	if err != nil {
		return nil, err
	}
	metadata.DstIP = fallback
	opts := d.DialOptions()
	race := newDirectUDPRacePacketConn(func(ctx context.Context, family int, remote netip.AddrPort) (net.PacketConn, error) {
		return dialer.NewDialer(opts...).ListenPacket(ctx, fmt.Sprintf("udp%d", family), "", remote)
	})
	logical := metadata.AddrPort()
	if err := race.register(ctx, logical, candidates); err != nil {
		return nil, err
	}
	resolveUDP := func(ctx context.Context, metadata *C.Metadata) error {
		candidates, fallback, err := d.resolveUDPRaceCandidates(ctx, metadata)
		if err != nil {
			return err
		}
		metadata.DstIP = fallback
		return race.register(ctx, metadata.AddrPort(), candidates)
	}
	return d.loopBack.NewPacketConn(newPacketConn(race, d, resolveUDP)), nil
}

func (d *Direct) resolveUDPRaceCandidates(ctx context.Context, metadata *C.Metadata) ([]netip.AddrPort, netip.Addr, error) {
	type result struct {
		ips []netip.Addr
		err error
	}
	results := make(chan result, 2)
	go func() {
		ips, err := resolver.LookupIPv4WithResolver(ctx, metadata.Host, resolver.DirectHostResolver)
		results <- result{ips: ips, err: err}
	}()
	go func() {
		ips, err := resolver.LookupIPv6WithResolver(ctx, metadata.Host, resolver.DirectHostResolver)
		results <- result{ips: ips, err: err}
	}()

	var ips []netip.Addr
	var lookupErrs []error
	for completed := 0; completed < 2; completed++ {
		select {
		case <-ctx.Done():
			return nil, netip.Addr{}, ctx.Err()
		case result := <-results:
			if result.err != nil {
				lookupErrs = append(lookupErrs, result.err)
			}
			for _, ip := range result.ips {
				ip = ip.Unmap()
				if ip.IsValid() {
					ips = append(ips, ip)
				}
			}
		}
	}

	// Preserve an already-resolved destination when the old ResolveUDP path
	// would have done so, while still discovering the other race candidates.
	fallback := netip.Addr{}
	if metadata.Resolved() && resolver.DirectHostResolver == resolver.DefaultResolver {
		fallback = metadata.DstIP.Unmap()
		ips = append(ips, fallback)
	}
	if len(ips) == 0 {
		lookupErrs = append(lookupErrs, fmt.Errorf("%w: %s", resolver.ErrIPNotFound, metadata.Host))
		return nil, netip.Addr{}, fmt.Errorf("can't resolve ip: %w", errors.Join(lookupErrs...))
	}

	ipv4s, ipv6s := resolver.SortationAddr(uniqueDirectUDPIPs(ips))
	if !fallback.IsValid() {
		switch {
		case d.prefer == C.IPv6Prefer && len(ipv6s) > 0:
			fallback = ipv6s[randv2.IntN(len(ipv6s))]
		case len(ipv4s) > 0:
			fallback = ipv4s[randv2.IntN(len(ipv4s))]
		case len(ipv6s) > 0:
			fallback = ipv6s[randv2.IntN(len(ipv6s))]
		}
	}
	preferredFamily := ipv4s
	if fallback.Is6() {
		preferredFamily = ipv6s
	}
	if preferred, loaded := directrace.Prefer(metadata.Host, d.Name(), preferredFamily); loaded {
		fallback = preferred
	}

	ordered := []netip.Addr{fallback}
	if d.prefer == C.IPv6Prefer {
		ordered = append(ordered, ipv6s...)
		ordered = append(ordered, ipv4s...)
	} else {
		ordered = append(ordered, ipv4s...)
		ordered = append(ordered, ipv6s...)
	}
	ordered = uniqueDirectUDPIPs(ordered)
	candidates := make([]netip.AddrPort, 0, len(ordered))
	for _, ip := range ordered {
		candidates = append(candidates, netip.AddrPortFrom(ip, metadata.DstPort))
	}
	return candidates, fallback, nil
}

func uniqueDirectUDPIPs(ips []netip.Addr) []netip.Addr {
	seen := make(map[netip.Addr]struct{}, len(ips))
	unique := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		ip = ip.Unmap()
		if !ip.IsValid() {
			continue
		}
		if _, loaded := seen[ip]; loaded {
			continue
		}
		seen[ip] = struct{}{}
		unique = append(unique, ip)
	}
	return unique
}

func newDirectUDPRacePacketConn(factory func(context.Context, int, netip.AddrPort) (net.PacketConn, error)) *directUDPRacePacketConn {
	return &directUDPRacePacketConn{
		closed:  make(chan struct{}),
		reads:   make(chan directUDPReadResult, 32),
		conns:   make(map[int]net.PacketConn, 2),
		targets: make(map[netip.AddrPort]*directUDPTarget),
		sources: make(map[netip.AddrPort][]*directUDPTarget),
		factory: factory,
	}
}

func (c *directUDPRacePacketConn) register(ctx context.Context, logical netip.AddrPort, candidates []netip.AddrPort) error {
	for _, candidate := range candidates {
		family := 6
		if candidate.Addr().Is4() {
			family = 4
		}
		if err := c.ensureConn(ctx, family, candidate); err != nil {
			continue
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.conns) == 0 {
		return errors.New("can't create DIRECT UDP socket")
	}
	if _, loaded := c.targets[logical]; loaded {
		return nil
	}
	target := &directUDPTarget{logical: logical, live: make(map[netip.AddrPort]bool)}
	for _, candidate := range candidates {
		family := 6
		if candidate.Addr().Is4() {
			family = 4
		}
		if c.conns[family] == nil {
			continue
		}
		target.candidates = append(target.candidates, candidate)
		target.live[candidate] = true
		c.sources[candidate] = append(c.sources[candidate], target)
	}
	if len(target.candidates) == 0 {
		return errors.New("no usable DIRECT UDP candidate")
	}
	c.targets[logical] = target
	return nil
}

func (c *directUDPRacePacketConn) ensureConn(ctx context.Context, family int, remote netip.AddrPort) error {
	c.mu.Lock()
	if c.conns[family] != nil {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	pc, err := c.factory(ctx, family, remote)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if existing := c.conns[family]; existing != nil {
		c.mu.Unlock()
		_ = pc.Close()
		return nil
	}
	c.conns[family] = pc
	c.alive++
	readBy := c.readBy
	writeBy := c.writeBy
	c.mu.Unlock()
	if !readBy.IsZero() {
		_ = pc.SetReadDeadline(readBy)
	}
	if !writeBy.IsZero() {
		_ = pc.SetWriteDeadline(writeBy)
	}
	go c.readLoop(pc)
	return nil
}

func (c *directUDPRacePacketConn) readLoop(pc net.PacketConn) {
	epc := N.NewEnhancePacketConn(pc)
	for {
		data, put, addr, err := epc.WaitReadFrom()
		if err != nil && !isTerminalDirectUDPReadError(err) {
			// An unconnected UDP socket can surface an asynchronous ICMP error
			// from one loser without identifying its destination. Consume that
			// error and keep the family reader alive for the other candidates.
			if put != nil {
				put()
			}
			continue
		}
		result := directUDPReadResult{data: data, put: put, addr: addr, err: err}
		if err != nil {
			// Publish the terminal error only after updating the live-reader
			// count. WaitReadFrom can then reliably identify the last socket;
			// doing this after the channel send races with the receiver.
			c.mu.Lock()
			c.alive--
			c.mu.Unlock()
		}
		select {
		case c.reads <- result:
		case <-c.closed:
			if put != nil {
				put()
			}
			return
		}
		if err != nil {
			return
		}
	}
}

func isTerminalDirectUDPReadError(err error) bool {
	if errors.Is(err, net.ErrClosed) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func (c *directUDPRacePacketConn) WriteTo(payload []byte, addr net.Addr) (int, error) {
	udpAddr, ok := addr.(*net.UDPAddr)
	if !ok || udpAddr == nil {
		return 0, fmt.Errorf("DIRECT UDP race requires UDP address, got %T", addr)
	}
	logical := unmapDirectUDPAddrPort(udpAddr.AddrPort())
	now := time.Now()

	c.mu.Lock()
	target := c.targets[logical]
	if target == nil {
		family := 6
		if logical.Addr().Is4() {
			family = 4
		}
		pc := c.conns[family]
		c.mu.Unlock()
		if pc == nil {
			return 0, errors.New("DIRECT UDP address family unavailable")
		}
		return pc.WriteTo(payload, addr)
	}
	if !target.winner.IsValid() && !target.started.IsZero() && (now.Sub(target.started) >= directUDPRaceWindow || target.datagrams >= directUDPRaceDatagrams || target.bytes+len(payload)*len(target.candidates) > directUDPRaceBytes) {
		target.winner = firstLiveDirectUDPCandidate(target)
	}
	if target.winner.IsValid() {
		candidates := []netip.AddrPort{target.winner}
		c.mu.Unlock()
		return c.writeCandidates(payload, candidates)
	}
	if target.started.IsZero() {
		target.started = now
	}
	target.datagrams++
	candidates := make([]netip.AddrPort, 0, len(target.candidates))
	for _, candidate := range target.candidates {
		if target.live[candidate] {
			candidates = append(candidates, candidate)
		}
	}
	target.bytes += len(payload) * len(candidates)
	c.mu.Unlock()

	n, err, failed := c.writeCandidateSet(payload, candidates)
	if len(failed) > 0 {
		c.mu.Lock()
		for _, candidate := range failed {
			delete(target.live, candidate)
		}
		if !target.winner.IsValid() && countLiveDirectUDPCandidates(target) == 1 {
			target.winner = firstLiveDirectUDPCandidate(target)
		}
		c.mu.Unlock()
	}
	return n, err
}

func (c *directUDPRacePacketConn) writeCandidates(payload []byte, candidates []netip.AddrPort) (int, error) {
	n, err, _ := c.writeCandidateSet(payload, candidates)
	return n, err
}

func (c *directUDPRacePacketConn) writeCandidateSet(payload []byte, candidates []netip.AddrPort) (int, error, []netip.AddrPort) {
	var errs []error
	var failed []netip.AddrPort
	succeeded := false
	for _, candidate := range candidates {
		family := 6
		if candidate.Addr().Is4() {
			family = 4
		}
		c.mu.Lock()
		pc := c.conns[family]
		c.mu.Unlock()
		if pc == nil {
			failed = append(failed, candidate)
			continue
		}
		if _, err := pc.WriteTo(payload, net.UDPAddrFromAddrPort(candidate)); err != nil {
			errs = append(errs, fmt.Errorf("send to %s: %w", candidate, err))
			failed = append(failed, candidate)
		} else {
			succeeded = true
		}
	}
	if succeeded {
		return len(payload), nil, failed
	}
	return 0, errors.Join(errs...), failed
}

func firstLiveDirectUDPCandidate(target *directUDPTarget) netip.AddrPort {
	for _, candidate := range target.candidates {
		if target.live[candidate] {
			return candidate
		}
	}
	return netip.AddrPort{}
}

func countLiveDirectUDPCandidates(target *directUDPTarget) int {
	count := 0
	for _, candidate := range target.candidates {
		if target.live[candidate] {
			count++
		}
	}
	return count
}

func (c *directUDPRacePacketConn) WaitReadFrom() ([]byte, func(), net.Addr, error) {
	for {
		var result directUDPReadResult
		select {
		case <-c.closed:
			return nil, nil, nil, net.ErrClosed
		case result = <-c.reads:
		}
		if result.err != nil {
			c.mu.Lock()
			alive := c.alive
			c.mu.Unlock()
			if alive == 0 {
				return nil, nil, nil, result.err
			}
			continue
		}
		udpAddr, ok := result.addr.(*net.UDPAddr)
		if !ok || udpAddr == nil {
			return result.data, result.put, result.addr, nil
		}
		source := unmapDirectUDPAddrPort(udpAddr.AddrPort())
		now := time.Now()
		c.mu.Lock()
		var logical netip.AddrPort
		for _, target := range c.sources[source] {
			if target.started.IsZero() {
				continue
			}
			if !target.winner.IsValid() && now.Sub(target.started) >= directUDPRaceWindow {
				target.winner = firstLiveDirectUDPCandidate(target)
			}
			if target.winner.IsValid() {
				if target.winner == source {
					logical = target.logical
					break
				}
				continue
			}
			if target.live[source] {
				target.winner = source
				logical = target.logical
				break
			}
		}
		_, knownSource := c.sources[source]
		c.mu.Unlock()
		if logical.IsValid() {
			return result.data, result.put, net.UDPAddrFromAddrPort(logical), nil
		}
		if !knownSource {
			return result.data, result.put, result.addr, nil
		}
		if result.put != nil {
			result.put()
		}
	}
}

func unmapDirectUDPAddrPort(addrPort netip.AddrPort) netip.AddrPort {
	return netip.AddrPortFrom(addrPort.Addr().Unmap(), addrPort.Port())
}

func (c *directUDPRacePacketConn) ReadFrom(payload []byte) (int, net.Addr, error) {
	data, put, addr, err := c.WaitReadFrom()
	if put != nil {
		defer put()
	}
	return copy(payload, data), addr, err
}

func (c *directUDPRacePacketConn) Close() error {
	var errs []error
	c.closeOnce.Do(func() {
		close(c.closed)
		c.mu.Lock()
		for _, pc := range c.conns {
			errs = append(errs, pc.Close())
		}
		c.mu.Unlock()
	})
	return errors.Join(errs...)
}

func (c *directUDPRacePacketConn) LocalAddr() net.Addr {
	c.mu.Lock()
	defer c.mu.Unlock()
	if pc := c.conns[4]; pc != nil {
		return pc.LocalAddr()
	}
	if pc := c.conns[6]; pc != nil {
		return pc.LocalAddr()
	}
	return &net.UDPAddr{}
}

func (c *directUDPRacePacketConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	c.readBy = t
	c.writeBy = t
	c.mu.Unlock()
	return errors.Join(c.setDeadline(func(pc net.PacketConn) error { return pc.SetDeadline(t) })...)
}

func (c *directUDPRacePacketConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.readBy = t
	c.mu.Unlock()
	return errors.Join(c.setDeadline(func(pc net.PacketConn) error { return pc.SetReadDeadline(t) })...)
}

func (c *directUDPRacePacketConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	c.writeBy = t
	c.mu.Unlock()
	return errors.Join(c.setDeadline(func(pc net.PacketConn) error { return pc.SetWriteDeadline(t) })...)
}

func (c *directUDPRacePacketConn) setDeadline(set func(net.PacketConn) error) []error {
	c.mu.Lock()
	conns := make([]net.PacketConn, 0, len(c.conns))
	for _, pc := range c.conns {
		conns = append(conns, pc)
	}
	c.mu.Unlock()
	errs := make([]error, 0, len(conns))
	for _, pc := range conns {
		if err := set(pc); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}
